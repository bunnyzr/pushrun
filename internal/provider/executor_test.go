package provider

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/bunnyzr/pushrun/internal/config"
)

func newTestExecutor(t *testing.T) (*Executor, config.Paths) {
	t.Helper()
	paths := config.NewPaths(t.TempDir())
	return NewExecutor(paths, nil), paths
}

// writeProvider registers p via the store and writes its script files into
// the provider dir.
func writeProvider(t *testing.T, paths config.Paths, p *Provider, scripts map[string]string) {
	t.Helper()
	if err := Save(paths, p); err != nil {
		t.Fatalf("Save: %v", err)
	}
	d := filepath.Join(paths.Providers, p.ID)
	for name, content := range scripts {
		full := filepath.Join(d, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			t.Fatalf("mkdir for %s: %v", name, err)
		}
		if err := os.WriteFile(full, []byte(content), 0o755); err != nil {
			t.Fatalf("write script %s: %v", name, err)
		}
	}
}

// countLines returns the number of lines in path, or 0 when it does not
// exist.
func countLines(t *testing.T, path string) int {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return 0
		}
		t.Fatalf("read %s: %v", path, err)
	}
	return len(strings.Split(strings.TrimRight(string(data), "\n"), "\n"))
}

func counterScript(counter string, extra string) string {
	return fmt.Sprintf("#!/bin/bash\nset -e\necho run >> %q\n%s\n", counter, extra)
}

func TestEnsureWarmedSkipsWhenReady(t *testing.T) {
	ex, paths := newTestExecutor(t)
	counter := filepath.Join(paths.Root, "counter")
	p := &Provider{
		ID: "demo", Name: "Demo",
		Warmup:  "scripts/warmup.sh",
		Install: "scripts/install.sh",
		Params:  []Param{{ID: "version", Type: ParamTypeString, Scope: ScopeWarmup}},
	}
	writeProvider(t, paths, p, map[string]string{
		"scripts/warmup.sh":  counterScript(counter, "mkdir -p \"$PUSHRUN_PROVIDER_CACHE_DIR/artifact\""),
		"scripts/install.sh": "#!/bin/bash\nexit 0\n",
	})

	ctx := context.Background()
	params := map[string]string{"version": "1"}
	dir1, err := ex.EnsureWarmed(ctx, p, params, io.Discard)
	if err != nil {
		t.Fatalf("first EnsureWarmed: %v", err)
	}
	dir2, err := ex.EnsureWarmed(ctx, p, params, io.Discard)
	if err != nil {
		t.Fatalf("second EnsureWarmed: %v", err)
	}
	if dir1 != dir2 {
		t.Fatalf("cache dir changed between calls: %q vs %q", dir1, dir2)
	}
	if got := countLines(t, counter); got != 1 {
		t.Fatalf("warmup script ran %d times, want 1", got)
	}
	if _, err := os.Stat(filepath.Join(dir1, "artifact")); err != nil {
		t.Fatalf("artifact missing from cache dir: %v", err)
	}
	fp := Fingerprint(p.ID, map[string]string{"version": "1"})
	marker, err := os.ReadFile(filepath.Join(dir1, ".pushrun-ready"))
	if err != nil {
		t.Fatalf("read marker: %v", err)
	}
	if string(marker) != fp {
		t.Fatalf("marker content %q, want fingerprint %q", marker, fp)
	}
}

func TestEnsureWarmedFingerprintChangeRewarms(t *testing.T) {
	ex, paths := newTestExecutor(t)
	counter := filepath.Join(paths.Root, "counter")
	p := &Provider{
		ID: "demo", Name: "Demo",
		Warmup:  "scripts/warmup.sh",
		Install: "scripts/install.sh",
		Params:  []Param{{ID: "version", Type: ParamTypeString, Scope: ScopeWarmup}},
	}
	writeProvider(t, paths, p, map[string]string{
		"scripts/warmup.sh":  counterScript(counter, ""),
		"scripts/install.sh": "#!/bin/bash\nexit 0\n",
	})

	ctx := context.Background()
	dir1, err := ex.EnsureWarmed(ctx, p, map[string]string{"version": "1"}, io.Discard)
	if err != nil {
		t.Fatalf("first EnsureWarmed: %v", err)
	}
	dir2, err := ex.EnsureWarmed(ctx, p, map[string]string{"version": "2"}, io.Discard)
	if err != nil {
		t.Fatalf("second EnsureWarmed: %v", err)
	}
	if dir1 == dir2 {
		t.Fatalf("cache dir did not change with fingerprint: %q", dir1)
	}
	if got := countLines(t, counter); got != 2 {
		t.Fatalf("warmup script ran %d times, want 2", got)
	}
}

func TestWarmupFailureLeavesNoMarker(t *testing.T) {
	ex, paths := newTestExecutor(t)
	counter := filepath.Join(paths.Root, "counter")
	p := &Provider{
		ID: "demo", Name: "Demo",
		Warmup:  "scripts/warmup.sh",
		Install: "scripts/install.sh",
		Params:  []Param{{ID: "version", Type: ParamTypeString, Scope: ScopeWarmup}},
	}
	writeProvider(t, paths, p, map[string]string{
		"scripts/warmup.sh": counterScript(counter,
			"mkdir -p \"$PUSHRUN_PROVIDER_CACHE_DIR/partial\"\n"+
				"echo data > \"$PUSHRUN_PROVIDER_CACHE_DIR/partial/file\"\n"+
				"exit 1"),
		"scripts/install.sh": "#!/bin/bash\nexit 0\n",
	})

	params := map[string]string{"version": "1"}
	cacheDir := filepath.Join(paths.ProviderData, p.ID+"-"+Fingerprint(p.ID, params))

	ctx := context.Background()
	if _, err := ex.EnsureWarmed(ctx, p, params, io.Discard); err == nil {
		t.Fatal("first EnsureWarmed: want error, got nil")
	}
	if _, err := os.Stat(filepath.Join(cacheDir, ".pushrun-ready")); !os.IsNotExist(err) {
		t.Fatalf("ready marker exists after failed warmup: %v", err)
	}
	if _, err := os.Stat(cacheDir); !os.IsNotExist(err) {
		t.Fatalf("cache dir left behind after failed warmup: %v", err)
	}
	// A failed warmup must not block a retry.
	if _, err := ex.EnsureWarmed(ctx, p, params, io.Discard); err == nil {
		t.Fatal("second EnsureWarmed: want error, got nil")
	}
	if got := countLines(t, counter); got != 2 {
		t.Fatalf("warmup script ran %d times, want 2 (retry not blocked)", got)
	}
	if _, err := os.Stat(cacheDir); !os.IsNotExist(err) {
		t.Fatalf("cache dir left behind after second failure: %v", err)
	}
}

func TestEnsureWarmedNoWarmupScript(t *testing.T) {
	ex, paths := newTestExecutor(t)
	// Shaped like the symlink builtin: no warmup phase, install-only params.
	p := &Provider{
		ID: "demo", Name: "Demo",
		Install: "scripts/install.sh",
		Params:  []Param{{ID: "source", Type: ParamTypeString, Scope: ScopeInstall, Required: true}},
	}
	writeProvider(t, paths, p, map[string]string{
		"scripts/install.sh": "#!/bin/bash\nexit 0\n",
	})

	ctx := context.Background()
	wantDir := filepath.Join(paths.ProviderData, p.ID+"-"+Fingerprint(p.ID, map[string]string{}))

	dir1, err := ex.EnsureWarmed(ctx, p, map[string]string{"source": "/data"}, io.Discard)
	if err != nil {
		t.Fatalf("first EnsureWarmed: %v", err)
	}
	if dir1 != wantDir {
		t.Fatalf("cache dir = %q, want %q", dir1, wantDir)
	}
	fp := Fingerprint(p.ID, map[string]string{})
	marker, err := os.ReadFile(filepath.Join(dir1, ".pushrun-ready"))
	if err != nil {
		t.Fatalf("read marker: %v", err)
	}
	if string(marker) != fp {
		t.Fatalf("marker content %q, want fingerprint %q", marker, fp)
	}

	// Idempotent: a second call returns the same dir and does not rebuild it
	// (a sentinel file dropped into the cache dir survives).
	sentinel := filepath.Join(dir1, "sentinel")
	if err := os.WriteFile(sentinel, []byte("keep"), 0o644); err != nil {
		t.Fatal(err)
	}
	dir2, err := ex.EnsureWarmed(ctx, p, map[string]string{"source": "/other"}, io.Discard)
	if err != nil {
		t.Fatalf("second EnsureWarmed: %v", err)
	}
	if dir2 != dir1 {
		t.Fatalf("cache dir changed between calls: %q vs %q", dir1, dir2)
	}
	if _, err := os.Stat(sentinel); err != nil {
		t.Fatalf("cache dir was rebuilt on second call (sentinel gone): %v", err)
	}
}

func TestEnsureWarmedConcurrentSingleExecution(t *testing.T) {
	ex, paths := newTestExecutor(t)
	counter := filepath.Join(paths.Root, "counter")
	p := &Provider{
		ID: "demo", Name: "Demo",
		Warmup:  "scripts/warmup.sh",
		Install: "scripts/install.sh",
		Params:  []Param{{ID: "version", Type: ParamTypeString, Scope: ScopeWarmup}},
	}
	writeProvider(t, paths, p, map[string]string{
		"scripts/warmup.sh":  counterScript(counter, "sleep 0.3"),
		"scripts/install.sh": "#!/bin/bash\nexit 0\n",
	})

	ctx := context.Background()
	params := map[string]string{"version": "1"}
	const n = 8
	var wg sync.WaitGroup
	dirs := make([]string, n)
	errs := make([]error, n)
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			dirs[i], errs[i] = ex.EnsureWarmed(ctx, p, params, io.Discard)
		}(i)
	}
	wg.Wait()
	for i, err := range errs {
		if err != nil {
			t.Fatalf("goroutine %d: %v", i, err)
		}
		if dirs[i] != dirs[0] {
			t.Fatalf("goroutine %d got cache dir %q, want %q", i, dirs[i], dirs[0])
		}
	}
	if got := countLines(t, counter); got != 1 {
		t.Fatalf("warmup script ran %d times under concurrency, want 1", got)
	}
}

func TestInstallEnvContract(t *testing.T) {
	ex, paths := newTestExecutor(t)
	p := &Provider{
		ID: "demo", Name: "Demo",
		Install: "scripts/install.sh",
		Params:  []Param{{ID: "version", Type: ParamTypeString, Scope: ScopeInstall}},
	}
	writeProvider(t, paths, p, map[string]string{
		"scripts/install.sh": "#!/bin/bash\nset -e\n" +
			"printf '%s|%s|%s' \"$PUSHRUN_PROVIDER_TARGET_DIR\" \"$PUSHRUN_PROVIDER_PHASE\" \"$PUSHRUN_PROVIDER_PARAM_VERSION\" " +
			"> \"$PUSHRUN_PROVIDER_TARGET_DIR/got.txt\"\n",
	})

	target := filepath.Join(paths.Root, "target")
	if err := os.MkdirAll(target, 0o755); err != nil {
		t.Fatal(err)
	}
	err := ex.Install(context.Background(), p, map[string]string{"version": "2.5"}, target, io.Discard)
	if err != nil {
		t.Fatalf("Install: %v", err)
	}
	got, err := os.ReadFile(filepath.Join(target, "got.txt"))
	if err != nil {
		t.Fatalf("read got.txt: %v", err)
	}
	want := target + "|install|2.5"
	if string(got) != want {
		t.Fatalf("got.txt = %q, want %q", got, want)
	}
}

func TestSecretParamRedactedInLog(t *testing.T) {
	ex, paths := newTestExecutor(t)
	p := &Provider{
		ID: "demo", Name: "Demo",
		Install: "scripts/install.sh",
		Params: []Param{
			{ID: "token", Type: ParamTypeString, Scope: ScopeInstall, Required: true, Secret: true},
			{ID: "flavor", Type: ParamTypeString, Scope: ScopeInstall},
		},
	}
	writeProvider(t, paths, p, map[string]string{
		"scripts/install.sh": "#!/bin/bash\nenv\n",
	})

	var log bytes.Buffer
	err := ex.Install(context.Background(), p, map[string]string{
		"token":  "s3cr3t-value",
		"flavor": "vanilla-flavor",
	}, filepath.Join(paths.Root, "target"), &log)
	if err != nil {
		t.Fatalf("Install: %v", err)
	}
	out := log.String()
	if strings.Contains(out, "s3cr3t-value") {
		t.Fatalf("log contains secret value:\n%s", out)
	}
	if !strings.Contains(out, "vanilla-flavor") {
		t.Fatalf("log is missing non-secret value:\n%s", out)
	}
	if !strings.Contains(out, "PUSHRUN_PROVIDER_PARAM_TOKEN=***") {
		t.Fatalf("log is missing redacted secret marker:\n%s", out)
	}
}

// A secret split across stream chunks (and buried in more than 64 KiB of
// output) must still be redacted: the redact writer withholds a tail of
// maxSecretLen-1 bytes exactly so chunk boundaries cannot leak it.
func TestSecretRedactionAcrossChunks(t *testing.T) {
	var buf bytes.Buffer
	secret := "s3cr3t-cross-chunk-value"
	rw := newRedactWriter(&buf, []string{secret})

	filler := strings.Repeat("A", 128<<10) // 128 KiB of noise on each side
	half := len(secret) / 2
	chunks := []string{
		filler,
		filler[:100] + secret[:half], // first half of the secret ends a chunk
		secret[half:] + filler,       // second half starts the next
		filler,
	}
	for _, c := range chunks {
		if _, err := rw.Write([]byte(c)); err != nil {
			t.Fatalf("Write: %v", err)
		}
	}
	if err := rw.Flush(); err != nil {
		t.Fatalf("Flush: %v", err)
	}

	out := buf.String()
	if len(out) <= 64<<10 {
		t.Fatalf("stream is only %d bytes, want > 64 KiB", len(out))
	}
	if strings.Contains(out, secret) {
		t.Fatal("log contains the secret value split across chunks")
	}
	if !strings.Contains(out, "***") {
		t.Fatal("log is missing the redaction marker")
	}
}

func TestWarmupJSONRecorded(t *testing.T) {
	ex, paths := newTestExecutor(t)
	p := &Provider{
		ID: "demo", Name: "Demo",
		Warmup:  "scripts/warmup.sh",
		Install: "scripts/install.sh",
		Params: []Param{
			{ID: "token", Type: ParamTypeString, Scope: ScopeWarmup, Secret: true},
			{ID: "version", Type: ParamTypeString, Scope: ScopeWarmup},
		},
	}
	writeProvider(t, paths, p, map[string]string{
		"scripts/warmup.sh":  "#!/bin/bash\nexit 0\n",
		"scripts/install.sh": "#!/bin/bash\nexit 0\n",
	})

	params := map[string]string{"token": "s3cr3t-value", "version": "1.2"}
	dir, err := ex.EnsureWarmed(context.Background(), p, params, io.Discard)
	if err != nil {
		t.Fatalf("EnsureWarmed: %v", err)
	}
	data, err := os.ReadFile(filepath.Join(dir, "warmup.json"))
	if err != nil {
		t.Fatalf("read warmup.json: %v", err)
	}
	var rec struct {
		Fingerprint string            `json:"fingerprint"`
		WarmedAt    time.Time         `json:"warmed_at"`
		Params      map[string]string `json:"params"`
	}
	if err := json.Unmarshal(data, &rec); err != nil {
		t.Fatalf("unmarshal warmup.json: %v", err)
	}
	if want := WarmupFingerprint(p, params); rec.Fingerprint != want {
		t.Fatalf("warmup.json fingerprint = %q, want %q", rec.Fingerprint, want)
	}
	if rec.WarmedAt.IsZero() {
		t.Fatal("warmup.json warmed_at is zero")
	}
	if got := rec.Params["token"]; got != "***" {
		t.Fatalf("warmup.json secret param = %q, want redacted %q", got, "***")
	}
	if got := rec.Params["version"]; got != "1.2" {
		t.Fatalf("warmup.json version param = %q, want %q", got, "1.2")
	}
}

func TestWarmupsEnumeration(t *testing.T) {
	ex, paths := newTestExecutor(t)
	if got := ex.Warmups("nope"); len(got) != 0 {
		t.Fatalf("Warmups for unknown id returned %d entries, want 0", len(got))
	}

	p := &Provider{
		ID: "demo", Name: "Demo",
		Warmup:  "scripts/warmup.sh",
		Install: "scripts/install.sh",
		Params:  []Param{{ID: "version", Type: ParamTypeString, Scope: ScopeWarmup}},
	}
	writeProvider(t, paths, p, map[string]string{
		"scripts/warmup.sh":  "#!/bin/bash\nexit 0\n",
		"scripts/install.sh": "#!/bin/bash\nexit 0\n",
	})

	ctx := context.Background()
	paramsA := map[string]string{"version": "1"}
	if _, err := ex.EnsureWarmed(ctx, p, paramsA, io.Discard); err != nil {
		t.Fatalf("EnsureWarmed A: %v", err)
	}
	got := ex.Warmups("demo")
	if len(got) != 1 {
		t.Fatalf("Warmups returned %d entries, want 1", len(got))
	}
	if got[0].Warming {
		t.Fatal("entry is marked warming after EnsureWarmed returned")
	}
	if want := WarmupFingerprint(p, paramsA); got[0].Fingerprint != want {
		t.Fatalf("entry fingerprint = %q, want %q", got[0].Fingerprint, want)
	}
	if v := got[0].Params["version"]; v != "1" {
		t.Fatalf("entry params version = %q, want %q", v, "1")
	}
	if got[0].WarmedAt.IsZero() {
		t.Fatal("entry WarmedAt is zero")
	}

	paramsB := map[string]string{"version": "2"}
	if _, err := ex.EnsureWarmed(ctx, p, paramsB, io.Discard); err != nil {
		t.Fatalf("EnsureWarmed B: %v", err)
	}
	got = ex.Warmups("demo")
	if len(got) != 2 {
		t.Fatalf("Warmups returned %d entries, want 2", len(got))
	}
	versions := map[string]bool{}
	for _, info := range got {
		versions[info.Params["version"]] = true
	}
	if !versions["1"] || !versions["2"] {
		t.Fatalf("entry params = %v, want versions 1 and 2", versions)
	}
	if got[0].WarmedAt.Before(got[1].WarmedAt) {
		t.Fatal("entries not sorted by WarmedAt descending")
	}
}

func TestMissingRequiredParamFails(t *testing.T) {
	ex, paths := newTestExecutor(t)
	counter := filepath.Join(paths.Root, "counter")
	p := &Provider{
		ID: "demo", Name: "Demo",
		Warmup:  "scripts/warmup.sh",
		Install: "scripts/install.sh",
		Params: []Param{
			{ID: "version", Type: ParamTypeString, Scope: ScopeWarmup, Required: true},
			{ID: "mode", Type: ParamTypeString, Scope: ScopeInstall, Required: true},
		},
	}
	writeProvider(t, paths, p, map[string]string{
		"scripts/warmup.sh":  counterScript(counter, ""),
		"scripts/install.sh": counterScript(counter, ""),
	})

	ctx := context.Background()
	if _, err := ex.EnsureWarmed(ctx, p, nil, io.Discard); err == nil {
		t.Fatal("EnsureWarmed with missing required param: want error, got nil")
	} else if !strings.Contains(err.Error(), "version") {
		t.Fatalf("error does not name the missing param: %v", err)
	}
	if err := ex.Install(ctx, p, map[string]string{"version": "1"}, filepath.Join(paths.Root, "target"), io.Discard); err == nil {
		t.Fatal("Install with missing required param: want error, got nil")
	} else if !strings.Contains(err.Error(), "mode") {
		t.Fatalf("error does not name the missing param: %v", err)
	}
	if _, err := os.Stat(counter); !os.IsNotExist(err) {
		t.Fatalf("a script ran despite missing required params (counter exists): %v", err)
	}
}
