package run_test

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"

	"github.com/bunnyzr/pushrun/internal/config"
	"github.com/bunnyzr/pushrun/internal/gitx"
	"github.com/bunnyzr/pushrun/internal/instance"
	"github.com/bunnyzr/pushrun/internal/project"
	"github.com/bunnyzr/pushrun/internal/provider"
	"github.com/bunnyzr/pushrun/internal/run"
)

func testPaths(t *testing.T) config.Paths {
	t.Helper()
	return config.NewPaths(t.TempDir())
}

func newManager(paths config.Paths, portFrom, portTo int) *run.Manager {
	return newManagerWithLogger(paths, portFrom, portTo, nil)
}

func newManagerWithLogger(paths config.Paths, portFrom, portTo int, log *slog.Logger) *run.Manager {
	cfg := &config.Config{PortPool: config.PortPoolConfig{From: portFrom, To: portTo}}
	return run.NewManager(paths, cfg, provider.NewExecutor(paths, cfg), instance.NewPortPool(paths, portFrom, portTo), log)
}

func git(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(),
		"GIT_AUTHOR_NAME=pushrun-test", "GIT_AUTHOR_EMAIL=test@example.com",
		"GIT_COMMITTER_NAME=pushrun-test", "GIT_COMMITTER_EMAIL=test@example.com")
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %s: %v\n%s", strings.Join(args, " "), err, out)
	}
	return strings.TrimSpace(string(out))
}

// commitFixture writes hello.txt with content and commits it on main.
func commitFixture(t *testing.T, repoDir, content string) string {
	t.Helper()
	if err := os.WriteFile(filepath.Join(repoDir, "hello.txt"), []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	git(t, repoDir, "add", ".")
	git(t, repoDir, "commit", "-m", "set "+content)
	return git(t, repoDir, "rev-parse", "HEAD")
}

func newFixtureRepo(t *testing.T, content string) (repoDir, commit string) {
	t.Helper()
	repoDir = filepath.Join(t.TempDir(), "repo")
	if err := os.MkdirAll(repoDir, 0o755); err != nil {
		t.Fatal(err)
	}
	git(t, repoDir, "init", "-b", "main")
	return repoDir, commitFixture(t, repoDir, content)
}

func writeScriptProvider(t *testing.T, paths config.Paths, id string, params []provider.Param, warmup, install string) {
	t.Helper()
	dir := filepath.Join(paths.Providers, id, "scripts")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	p := &provider.Provider{ID: id, Name: id, Params: params, Install: "scripts/install.sh"}
	if warmup != "" {
		p.Warmup = "scripts/warmup.sh"
		if err := os.WriteFile(filepath.Join(dir, "warmup.sh"), []byte(warmup), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(dir, "install.sh"), []byte(install), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := provider.Save(paths, p); err != nil {
		t.Fatal(err)
	}
}

func killInstance(t *testing.T, paths config.Paths, proj, inst string) {
	t.Helper()
	if st, err := instance.LoadState(paths, proj, inst); err == nil && st.PGID > 0 {
		_ = instance.KillGroup(st.PGID)
	}
}

func TestRunFullChainSuccess(t *testing.T) {
	paths := testPaths(t)
	repoDir, commit := newFixtureRepo(t, "hello-v1\n")
	linkSrc := t.TempDir()
	if err := os.WriteFile(filepath.Join(linkSrc, "shared.txt"), []byte("shared\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	writeScriptProvider(t, paths, "test-prov",
		[]provider.Param{{ID: "token", Type: provider.ParamTypeString, Scope: provider.ScopeWarmup, Required: true}},
		`echo "warming $PUSHRUN_PROVIDER_PARAM_TOKEN" && echo artifact > "$PUSHRUN_PROVIDER_CACHE_DIR/artifact.txt"`,
		`mkdir -p "$PUSHRUN_PROVIDER_TARGET_DIR" && cp "$PUSHRUN_PROVIDER_CACHE_DIR/artifact.txt" "$PUSHRUN_PROVIDER_TARGET_DIR/"`)

	proj := &project.Project{
		Name: "demo",
		Tree: []project.Node{
			{Path: "app", Mount: &project.Mount{Provider: "git", Primary: true, Params: map[string]string{"repo": repoDir, "branch": "main"}}},
			{Path: "linked", Mount: &project.Mount{Provider: "symlink", Params: map[string]string{"source": linkSrc}}},
			{Path: "extra", Mount: &project.Mount{Provider: "test-prov", Params: map[string]string{"token": "sekrit"}}},
		},
		Pipeline: []project.Step{
			{Name: "build", Run: "echo build-output-marker && cat app/hello.txt > built.txt"},
			{Name: "check", Run: "test -f extra/artifact.txt && test -L linked && test -n \"$CI_PORT\" && test \"$CI_PROJECT\" = demo"},
			{Name: "serve", Run: "sleep 300", Background: true, Health: &project.Health{Type: "command", Target: "true", Interval: 1, Retries: 3}},
		},
	}
	if err := project.Save(paths, proj); err != nil {
		t.Fatal(err)
	}

	t.Cleanup(func() { killInstance(t, paths, "demo", "main") })

	m := newManager(paths, 23100, 23110)
	var out bytes.Buffer
	res, err := m.Run(context.Background(), run.Request{
		Project: "demo", Instance: "main", Branch: "main", Commit: commit,
		Action: "run", User: "tester",
	}, &out)
	if err != nil {
		t.Fatalf("Run: %v\nout:\n%s", err, out.String())
	}
	if res.Status != run.StatusSuccess {
		t.Fatalf("status = %q, want SUCCESS\nout:\n%s", res.Status, out.String())
	}
	if res.RunID == "" {
		t.Fatal("empty run id")
	}
	if res.Port < 23100 || res.Port > 23110 {
		t.Fatalf("port %d outside pool range 23100-23110", res.Port)
	}

	// Node contents match the checked-out commit tree and provider installs.
	instDir := filepath.Join(paths.Instances, "demo", "main")
	data, err := os.ReadFile(filepath.Join(instDir, "app", "hello.txt"))
	if err != nil || string(data) != "hello-v1\n" {
		t.Fatalf("app/hello.txt = %q, %v; want commit tree content", data, err)
	}
	if target, err := os.Readlink(filepath.Join(instDir, "linked")); err != nil || target != linkSrc {
		t.Fatalf("linked -> %q, %v; want %q", target, err, linkSrc)
	}
	data, err = os.ReadFile(filepath.Join(instDir, "extra", "artifact.txt"))
	if err != nil || strings.TrimSpace(string(data)) != "artifact" {
		t.Fatalf("extra/artifact.txt = %q, %v", data, err)
	}
	// The build step ran in the instance dir.
	if data, err := os.ReadFile(filepath.Join(instDir, "built.txt")); err != nil || string(data) != "hello-v1\n" {
		t.Fatalf("built.txt = %q, %v", data, err)
	}

	// Instance state reflects the running supervised process.
	st, err := instance.LoadState(paths, "demo", "main")
	if err != nil {
		t.Fatalf("LoadState: %v", err)
	}
	if st.Status != instance.StatusRunning || st.Port != res.Port || st.Commit != commit || st.RunID != res.RunID {
		t.Fatalf("state = %+v", st)
	}
	if st.PGID <= 0 || !instance.Alive(st.PGID) {
		t.Fatalf("supervised process pgid %d not alive", st.PGID)
	}

	// build.log carries framed step output.
	logData, err := os.ReadFile(filepath.Join(paths.Runs, "demo", "main", res.RunID, "build.log"))
	if err != nil {
		t.Fatalf("read build.log: %v", err)
	}
	for _, want := range []string{"# ==> step: build", "build-output-marker", "# ==> step: serve"} {
		if !strings.Contains(string(logData), want) {
			t.Fatalf("build.log missing %q:\n%s", want, logData)
		}
	}

	// Trailer lines on out.
	for _, want := range []string{"CI_STATUS=SUCCESS", "CI_PORT=" + strconv.Itoa(res.Port), "CI_URL=http://127.0.0.1:" + strconv.Itoa(res.Port)} {
		if !strings.Contains(out.String(), want) {
			t.Fatalf("out missing %q:\n%s", want, out.String())
		}
	}
	if res.URL == "" {
		t.Fatal("result URL empty on success")
	}

	// Run record APIs.
	rec, err := run.GetRun(paths, "demo", "main", res.RunID)
	if err != nil {
		t.Fatalf("GetRun: %v", err)
	}
	if rec.Status != run.StatusSuccess || rec.Commit != commit || rec.Branch != "main" {
		t.Fatalf("record = %+v", rec)
	}
	if len(rec.Steps) != 3 || rec.Steps[0].Name != "build" || rec.Steps[2].Name != "serve" {
		t.Fatalf("record steps = %+v", rec.Steps)
	}
	// The record is self-contained: triggering user and a
	// pipeline snapshot ride along with the outcome.
	if rec.User != "tester" {
		t.Fatalf("record user = %q, want tester", rec.User)
	}
	if len(rec.Pipeline) != 3 || rec.Pipeline[0].Name != "build" || rec.Pipeline[0].Run == "" ||
		!rec.Pipeline[2].Background {
		t.Fatalf("record pipeline snapshot = %+v", rec.Pipeline)
	}
	recs, err := run.ListRuns(paths, "demo", "main")
	if err != nil || len(recs) != 1 {
		t.Fatalf("ListRuns = %v, %v", recs, err)
	}
	rc, err := run.OpenBuildLog(paths, "demo", "main", res.RunID)
	if err != nil {
		t.Fatalf("OpenBuildLog: %v", err)
	}
	streamed, err := io.ReadAll(rc)
	_ = rc.Close()
	if err != nil || !strings.Contains(string(streamed), "build-output-marker") {
		t.Fatalf("streamed build log = %q, %v", streamed, err)
	}
}

func TestRunUnknownProjectError(t *testing.T) {
	paths := testPaths(t)
	m := newManager(paths, 23200, 23210)
	_, err := m.Run(context.Background(), run.Request{Project: "ghost", Instance: "main", Action: "run"}, io.Discard)
	if err == nil {
		t.Fatal("expected error for unknown project")
	}
	if !strings.Contains(err.Error(), "ghost") || !strings.Contains(err.Error(), "create") {
		t.Fatalf("error %q does not name the project and how to create it", err)
	}
}

func TestRunBusyOnContention(t *testing.T) {
	paths := testPaths(t)
	if err := project.Save(paths, &project.Project{Name: "demo"}); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(paths.Locks, 0o755); err != nil {
		t.Fatal(err)
	}
	f, err := os.OpenFile(filepath.Join(paths.Locks, "4:demo-4:main"), os.O_CREATE|os.O_RDWR, 0o644)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = f.Close() }()
	if err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		t.Fatalf("hold instance lock: %v", err)
	}

	m := newManager(paths, 23300, 23310)
	var out bytes.Buffer
	res, err := m.Run(context.Background(), run.Request{Project: "demo", Instance: "main", Action: "run"}, &out)
	if err != nil {
		t.Fatalf("busy run should not error, got %v", err)
	}
	if res.Status != run.StatusBusy {
		t.Fatalf("status = %q, want BUSY", res.Status)
	}
	if !strings.Contains(out.String(), "CI_STATUS=BUSY") {
		t.Fatalf("out missing CI_STATUS=BUSY:\n%s", out.String())
	}
}

func TestWarmupFailureStopsBeforeInstall(t *testing.T) {
	paths := testPaths(t)
	writeScriptProvider(t, paths, "bad-prov", nil,
		`echo nope && exit 1`,
		`mkdir -p "$PUSHRUN_PROVIDER_TARGET_DIR"`)
	proj := &project.Project{
		Name: "demo",
		Tree: []project.Node{
			{Path: "extra", Mount: &project.Mount{Provider: "bad-prov"}},
		},
	}
	if err := project.Save(paths, proj); err != nil {
		t.Fatal(err)
	}

	m := newManager(paths, 23400, 23410)
	var out bytes.Buffer
	res, err := m.Run(context.Background(), run.Request{Project: "demo", Instance: "main", Action: "run"}, &out)
	if err == nil {
		t.Fatal("expected warmup failure error")
	}
	if !strings.Contains(err.Error(), "bad-prov") || !strings.Contains(err.Error(), "warmup") {
		t.Fatalf("error %q does not name provider and phase", err)
	}
	if res == nil || res.Status != run.StatusFailed {
		t.Fatalf("result = %+v, want FAILED", res)
	}
	if _, err := os.Stat(filepath.Join(paths.Instances, "demo", "main", "extra")); !errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("target node should never be created, stat err = %v", err)
	}
	if !strings.Contains(out.String(), "CI_STATUS=FAILED") {
		t.Fatalf("out missing CI_STATUS=FAILED:\n%s", out.String())
	}
	// The persisted record carries the reason, naming provider and phase.
	rec, err := run.GetRun(paths, "demo", "main", res.RunID)
	if err != nil {
		t.Fatalf("GetRun: %v", err)
	}
	if rec.Status != run.StatusFailed || !strings.Contains(rec.Error, "bad-prov") || !strings.Contains(rec.Error, "warmup") {
		t.Fatalf("record = %+v, want FAILED naming provider and phase", rec)
	}
}

func TestFailedStepReportsRealState(t *testing.T) {
	paths := testPaths(t)
	proj := &project.Project{
		Name: "demo",
		Pipeline: []project.Step{
			{Name: "one", Run: "true"},
			{Name: "two", Run: "echo oops && exit 1"},
			{Name: "three", Run: "true"},
		},
	}
	if err := project.Save(paths, proj); err != nil {
		t.Fatal(err)
	}

	m := newManager(paths, 23500, 23510)
	var out bytes.Buffer
	res, err := m.Run(context.Background(), run.Request{Project: "demo", Instance: "main", Action: "run"}, &out)
	if err == nil {
		t.Fatal("expected step failure error")
	}
	if res.Status != run.StatusFailed {
		t.Fatalf("status = %q, want FAILED", res.Status)
	}

	// latest.json shows the failed run — never a stale success.
	st, err := instance.LoadState(paths, "demo", "main")
	if err != nil {
		t.Fatalf("LoadState: %v", err)
	}
	if st.Status != instance.StatusFailed || st.RunID != res.RunID {
		t.Fatalf("state = %+v, want FAILED run %s", st, res.RunID)
	}
	// No supervised process: current.json must not linger.
	if _, err := os.Stat(filepath.Join(paths.State, "demo", "main", "current.json")); !errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("current.json should be absent, stat err = %v", err)
	}

	rec, err := run.GetRun(paths, "demo", "main", res.RunID)
	if err != nil {
		t.Fatalf("GetRun: %v", err)
	}
	if rec.Status != run.StatusFailed {
		t.Fatalf("record status = %q", rec.Status)
	}
	if len(rec.Steps) != 2 {
		t.Fatalf("record steps = %+v, want steps one+two only", rec.Steps)
	}
	if rec.Steps[0].Status != run.StatusSuccess || rec.Steps[1].Status != run.StatusFailed || rec.Steps[1].ExitCode != 1 {
		t.Fatalf("record steps = %+v", rec.Steps)
	}
	// The record carries the failure reason, naming step and exit.
	if !strings.Contains(rec.Error, `step "two"`) || !strings.Contains(rec.Error, "exit status 1") {
		t.Fatalf("record error = %q, want the failing step and its exit", rec.Error)
	}
}

func TestSyncActionAssemblesOnly(t *testing.T) {
	paths := testPaths(t)
	linkSrc := t.TempDir()
	proj := &project.Project{
		Name: "demo",
		Tree: []project.Node{
			{Path: "linked", Mount: &project.Mount{Provider: "symlink", Params: map[string]string{"source": linkSrc}}},
		},
		Pipeline: []project.Step{
			{Name: "must-not-run", Run: "touch marker.txt"},
		},
	}
	if err := project.Save(paths, proj); err != nil {
		t.Fatal(err)
	}

	m := newManager(paths, 23600, 23610)
	var out bytes.Buffer
	res, err := m.Run(context.Background(), run.Request{Project: "demo", Instance: "main", Action: "sync"}, &out)
	if err != nil {
		t.Fatalf("Run sync: %v\nout:\n%s", err, out.String())
	}
	if res.Status != run.StatusSuccess {
		t.Fatalf("status = %q, want SUCCESS", res.Status)
	}
	if res.Port != 0 {
		t.Fatalf("sync allocated port %d", res.Port)
	}
	if strings.Contains(out.String(), "CI_PORT=") {
		t.Fatalf("sync output should not carry CI_PORT:\n%s", out.String())
	}
	instDir := filepath.Join(paths.Instances, "demo", "main")
	if target, err := os.Readlink(filepath.Join(instDir, "linked")); err != nil || target != linkSrc {
		t.Fatalf("linked -> %q, %v", target, err)
	}
	if _, err := os.Stat(filepath.Join(instDir, "marker.txt")); !errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("step must not run during sync, stat err = %v", err)
	}
	// No port lease was persisted.
	entries, err := os.ReadDir(paths.Ports)
	if err == nil && len(entries) > 0 {
		t.Fatalf("port leases after sync: %v", entries)
	} else if err != nil && !errors.Is(err, fs.ErrNotExist) {
		t.Fatal(err)
	}
}

func TestRerunUsesLastCommit(t *testing.T) {
	paths := testPaths(t)
	repoDir, commitA := newFixtureRepo(t, "hello-v1\n")
	proj := &project.Project{
		Name: "demo",
		Tree: []project.Node{
			{Path: "app", Mount: &project.Mount{Provider: "git", Primary: true, Params: map[string]string{"repo": repoDir, "branch": "main"}}},
		},
		Pipeline: []project.Step{
			{Name: "snapshot", Run: "cat app/hello.txt > seen.txt"},
		},
	}
	if err := project.Save(paths, proj); err != nil {
		t.Fatal(err)
	}

	m := newManager(paths, 23700, 23710)
	var out bytes.Buffer
	res, err := m.Run(context.Background(), run.Request{
		Project: "demo", Instance: "main", Branch: "main", Commit: commitA, Action: "run",
	}, &out)
	if err != nil || res.Status != run.StatusSuccess {
		t.Fatalf("first run: %v %s\n%s", err, res.Status, out.String())
	}

	// Advance the repo; a rerun must not pick up the newer commit.
	commitFixture(t, repoDir, "hello-v2\n")

	out.Reset()
	res2, err := m.Run(context.Background(), run.Request{
		Project: "demo", Instance: "main", Action: "rerun",
	}, &out)
	if err != nil || res2.Status != run.StatusSuccess {
		t.Fatalf("rerun: %v %s\n%s", err, res2.Status, out.String())
	}
	if res2.RunID == res.RunID {
		t.Fatal("rerun reused the first run id")
	}
	data, err := os.ReadFile(filepath.Join(paths.Instances, "demo", "main", "app", "hello.txt"))
	if err != nil || string(data) != "hello-v1\n" {
		t.Fatalf("rerun checked out %q, want last run's commit content hello-v1", data)
	}
	st, err := instance.LoadState(paths, "demo", "main")
	if err != nil {
		t.Fatal(err)
	}
	if st.Commit != commitA {
		t.Fatalf("state commit = %q, want %q", st.Commit, commitA)
	}
}

// An API run with Branch set and no Commit resolves that branch's snapshot
// ref of the triggering repo instead of the declared branch's snapshot.
func TestRunHonorsRequestedBranch(t *testing.T) {
	paths := testPaths(t)
	repoDir, mainCommit := newFixtureRepo(t, "hello-main\n")
	// A feature branch with different content.
	git(t, repoDir, "checkout", "-b", "feature")
	featureCommit := commitFixture(t, repoDir, "hello-feature\n")
	git(t, repoDir, "checkout", "main")

	proj := &project.Project{
		Name: "demo",
		Tree: []project.Node{
			{Path: "app", Mount: &project.Mount{Provider: "git", Primary: true, Params: map[string]string{"repo": repoDir, "branch": "main"}}},
		},
		Pipeline: []project.Step{
			{Name: "snapshot", Run: "cat app/hello.txt > seen.txt"},
		},
	}
	if err := project.Save(paths, proj); err != nil {
		t.Fatal(err)
	}
	m := newManager(paths, 23820, 23830)
	instDir := filepath.Join(paths.Instances, "demo", "main")

	// Seed the bare repo (declared branch main) with an explicit-commit run.
	res, err := m.Run(context.Background(), run.Request{
		Project: "demo", Instance: "main", Branch: "main", Commit: mainCommit, Action: "run",
	}, io.Discard)
	if err != nil || res.Status != run.StatusSuccess {
		t.Fatalf("seed run: %v %v", res, err)
	}
	assertNodeContent(t, instDir, "seen.txt", "hello-main\n")

	// A branch with no snapshot ref fails with a clear, ref-naming error.
	res, err = m.Run(context.Background(), run.Request{
		Project: "demo", Instance: "main", Branch: "ghost", Action: "run",
	}, io.Discard)
	if err == nil || !strings.Contains(err.Error(), "refs/pushrun/for/ghost") {
		t.Fatalf("run with un-pushed branch: %v %v, want resolve error naming the ref", res, err)
	}

	// Move the feature snapshot ref the way a push through pushrun would.
	bare, err := gitx.BareRepoDir(paths, repoDir)
	if err != nil {
		t.Fatal(err)
	}
	git(t, bare, "--git-dir="+bare, "fetch", repoDir, "+refs/heads/feature:refs/pushrun/for/feature")

	// Branch without Commit resolves the feature snapshot, not main's.
	res, err = m.Run(context.Background(), run.Request{
		Project: "demo", Instance: "main", Branch: "feature", Action: "run",
	}, io.Discard)
	if err != nil || res.Status != run.StatusSuccess {
		t.Fatalf("feature run: %v %v", res, err)
	}
	assertNodeContent(t, instDir, "seen.txt", "hello-feature\n")
	st, err := instance.LoadState(paths, "demo", "main")
	if err != nil {
		t.Fatal(err)
	}
	if st.Commit != featureCommit {
		t.Fatalf("state commit = %q, want feature commit %q", st.Commit, featureCommit)
	}
}

func TestRunRetentionPrunesOldRuns(t *testing.T) {
	paths := testPaths(t)
	if err := project.Save(paths, &project.Project{Name: "demo"}); err != nil {
		t.Fatal(err)
	}
	runsDir := filepath.Join(paths.Runs, "demo", "main")
	base := time.Now().Add(-time.Hour)
	for i := range 21 {
		dir := filepath.Join(runsDir, runDirName(i))
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
		mt := base.Add(time.Duration(i) * time.Second)
		if err := os.Chtimes(dir, mt, mt); err != nil {
			t.Fatal(err)
		}
	}

	m := newManager(paths, 23800, 23810)
	res, err := m.Run(context.Background(), run.Request{Project: "demo", Instance: "main", Action: "sync"}, io.Discard)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	entries, err := os.ReadDir(runsDir)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 20 {
		names := make([]string, 0, len(entries))
		for _, e := range entries {
			names = append(names, e.Name())
		}
		t.Fatalf("%d run dirs remain, want 20: %v", len(entries), names)
	}
	for _, gone := range []string{runDirName(0), runDirName(1)} {
		if _, err := os.Stat(filepath.Join(runsDir, gone)); !errors.Is(err, fs.ErrNotExist) {
			t.Fatalf("oldest run dir %s should be pruned, stat err = %v", gone, err)
		}
	}
	if _, err := os.Stat(filepath.Join(runsDir, res.RunID)); err != nil {
		t.Fatalf("new run dir missing: %v", err)
	}
}

func TestBuildKeepsSupervisedProcessTracked(t *testing.T) {
	paths := testPaths(t)
	proj := &project.Project{
		Name: "demo",
		Pipeline: []project.Step{
			{Name: "build", Run: "true"},
			{Name: "serve", Run: "sleep 300", Background: true},
		},
	}
	if err := project.Save(paths, proj); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { killInstance(t, paths, "demo", "main") })
	m := newManager(paths, 23900, 23910)

	res, err := m.Run(context.Background(), run.Request{Project: "demo", Instance: "main", Action: "run"}, io.Discard)
	if err != nil || res.Status != run.StatusSuccess {
		t.Fatalf("run: %v %v", res, err)
	}
	st1, err := instance.LoadState(paths, "demo", "main")
	if err != nil {
		t.Fatal(err)
	}
	pgid := st1.PGID
	if pgid <= 0 || !instance.Alive(pgid) {
		t.Fatalf("supervised process not running after run: %+v", st1)
	}

	// A build must not orphan the live supervised service.
	res2, err := m.Run(context.Background(), run.Request{Project: "demo", Instance: "main", Action: "build"}, io.Discard)
	if err != nil || res2.Status != run.StatusSuccess {
		t.Fatalf("build: %v %v", res2, err)
	}
	st2, err := instance.LoadState(paths, "demo", "main")
	if err != nil {
		t.Fatal(err)
	}
	if st2.PGID != pgid || st2.PID != st1.PID {
		t.Fatalf("build dropped process tracking: before %+v after %+v", st1, st2)
	}
	if !instance.Alive(pgid) {
		t.Fatal("supervised process died during build")
	}
	// current.json must still exist so crash recovery keeps tracking it.
	if _, err := os.Stat(filepath.Join(paths.State, "demo", "main", "current.json")); err != nil {
		t.Fatalf("current.json lost after build: %v", err)
	}

	// stop still kills the service.
	res3, err := m.Run(context.Background(), run.Request{Project: "demo", Instance: "main", Action: "stop"}, io.Discard)
	if err != nil || res3.Status != run.StatusSuccess {
		t.Fatalf("stop: %v %v", res3, err)
	}
	if instance.Alive(pgid) {
		t.Fatal("supervised process survived stop")
	}
	st3, err := instance.LoadState(paths, "demo", "main")
	if err != nil {
		t.Fatal(err)
	}
	if st3.Status != instance.StatusStopped || st3.PGID != 0 {
		t.Fatalf("state after stop = %+v", st3)
	}
}

func TestStepTimeoutKillsProcessGroup(t *testing.T) {
	paths := testPaths(t)
	pidFile := filepath.Join(t.TempDir(), "child.pid")
	proj := &project.Project{
		Name: "demo",
		Pipeline: []project.Step{
			// Spawn a grandchild, then hang past the timeout.
			{Name: "hang", Run: "sleep 300 & echo $! > '" + pidFile + "'; sleep 300", Timeout: 1},
		},
	}
	if err := project.Save(paths, proj); err != nil {
		t.Fatal(err)
	}
	m := newManager(paths, 23920, 23930)

	res, err := m.Run(context.Background(), run.Request{Project: "demo", Instance: "main", Action: "build"}, io.Discard)
	if err == nil || !strings.Contains(err.Error(), "timed out") {
		t.Fatalf("expected timeout error, got %v", err)
	}
	if res.Status != run.StatusFailed {
		t.Fatalf("status = %q, want FAILED", res.Status)
	}
	data, err := os.ReadFile(pidFile)
	if err != nil {
		t.Fatalf("grandchild pid file missing: %v", err)
	}
	var pid int
	if _, err := fmt.Sscan(strings.TrimSpace(string(data)), &pid); err != nil || pid <= 0 {
		t.Fatalf("bad grandchild pid %q: %v", data, err)
	}
	// The whole step process group must be dead, not just the bash leader.
	deadline := time.Now().Add(5 * time.Second)
	for {
		err := syscall.Kill(pid, 0)
		if errors.Is(err, syscall.ESRCH) {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("grandchild process %d survived step timeout", pid)
		}
		time.Sleep(50 * time.Millisecond)
	}
}

func TestStopStartRestartCycle(t *testing.T) {
	paths := testPaths(t)
	proj := &project.Project{
		Name: "demo",
		Pipeline: []project.Step{
			{Name: "serve", Run: "sleep 300", Background: true},
		},
	}
	if err := project.Save(paths, proj); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { killInstance(t, paths, "demo", "main") })
	m := newManager(paths, 23940, 23950)

	if res, err := m.Run(context.Background(), run.Request{Project: "demo", Instance: "main", Action: "run"}, io.Discard); err != nil || res.Status != run.StatusSuccess {
		t.Fatalf("run: %v %v", res, err)
	}
	st, err := instance.LoadState(paths, "demo", "main")
	if err != nil {
		t.Fatal(err)
	}
	pgid1 := st.PGID

	// stop kills the supervised group and clears the process state.
	if res, err := m.Run(context.Background(), run.Request{Project: "demo", Instance: "main", Action: "stop"}, io.Discard); err != nil || res.Status != run.StatusSuccess {
		t.Fatalf("stop: %v %v", res, err)
	}
	if instance.Alive(pgid1) {
		t.Fatal("process survived stop")
	}
	st, err = instance.LoadState(paths, "demo", "main")
	if err != nil {
		t.Fatal(err)
	}
	if st.Status != instance.StatusStopped || st.PGID != 0 || st.PID != 0 {
		t.Fatalf("state after stop = %+v", st)
	}

	// start on the stopped instance starts the recorded background command.
	var out bytes.Buffer
	res, err := m.Run(context.Background(), run.Request{Project: "demo", Instance: "main", Action: "start"}, &out)
	if err != nil || res.Status != run.StatusSuccess {
		t.Fatalf("start: %v %v\n%s", res, err, out.String())
	}
	st, err = instance.LoadState(paths, "demo", "main")
	if err != nil {
		t.Fatal(err)
	}
	pgid2 := st.PGID
	if pgid2 <= 0 || !instance.Alive(pgid2) || st.Status != instance.StatusRunning {
		t.Fatalf("state after start = %+v", st)
	}
	if !strings.Contains(out.String(), "CI_STATUS=SUCCESS") || !strings.Contains(out.String(), "CI_URL=") {
		t.Fatalf("start output missing trailers:\n%s", out.String())
	}

	// restart cycles the process.
	if res, err := m.Run(context.Background(), run.Request{Project: "demo", Instance: "main", Action: "restart"}, io.Discard); err != nil || res.Status != run.StatusSuccess {
		t.Fatalf("restart: %v %v", res, err)
	}
	st, err = instance.LoadState(paths, "demo", "main")
	if err != nil {
		t.Fatal(err)
	}
	if st.PGID == pgid2 || !instance.Alive(st.PGID) || st.Status != instance.StatusRunning {
		t.Fatalf("state after restart = %+v (previous pgid %d)", st, pgid2)
	}
	if instance.Alive(pgid2) {
		t.Fatal("old process survived restart")
	}
}

func TestStartNeverRunFails(t *testing.T) {
	paths := testPaths(t)
	proj := &project.Project{
		Name:     "demo",
		Pipeline: []project.Step{{Name: "serve", Run: "sleep 300", Background: true}},
	}
	if err := project.Save(paths, proj); err != nil {
		t.Fatal(err)
	}
	m := newManager(paths, 23960, 23970)

	var out bytes.Buffer
	res, err := m.Run(context.Background(), run.Request{Project: "demo", Instance: "main", Action: "start"}, &out)
	if err == nil {
		t.Fatal("start on never-run instance succeeded")
	}
	if res == nil || res.Status != run.StatusFailed {
		t.Fatalf("result = %+v, want FAILED", res)
	}
	if !strings.Contains(out.String(), "CI_STATUS=FAILED") {
		t.Fatalf("out missing CI_STATUS=FAILED:\n%s", out.String())
	}
}

func TestFailedStartPersistsState(t *testing.T) {
	paths := testPaths(t)
	proj := &project.Project{
		Name: "demo",
		Pipeline: []project.Step{
			{Name: "serve", Run: "sleep 300", Background: true,
				Health: &project.Health{Type: "command", Target: "false", Interval: 1, Retries: 2}},
		},
	}
	if err := project.Save(paths, proj); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { killInstance(t, paths, "demo", "main") })
	m := newManager(paths, 23980, 23990)

	// sync first: state exists but holds no port and no process.
	if res, err := m.Run(context.Background(), run.Request{Project: "demo", Instance: "main", Action: "sync"}, io.Discard); err != nil || res.Status != run.StatusSuccess {
		t.Fatalf("sync: %v %v", res, err)
	}

	var out bytes.Buffer
	res, err := m.Run(context.Background(), run.Request{Project: "demo", Instance: "main", Action: "start"}, &out)
	if err == nil {
		t.Fatal("start with failing health check succeeded")
	}
	if res.Status != run.StatusFailed {
		t.Fatalf("status = %q, want FAILED", res.Status)
	}
	if !strings.Contains(out.String(), "CI_STATUS=FAILED") {
		t.Fatalf("out missing CI_STATUS=FAILED:\n%s", out.String())
	}
	// The real state must be persisted: failed status, leased port
	// recorded, no live process.
	st, err := instance.LoadState(paths, "demo", "main")
	if err != nil {
		t.Fatal(err)
	}
	if st.Status != instance.StatusFailed {
		t.Fatalf("state status = %q, want FAILED", st.Status)
	}
	if st.Port < 23980 || st.Port > 23990 {
		t.Fatalf("leased port not recorded in state: %+v", st)
	}
	if st.PGID != 0 {
		t.Fatalf("state carries dead pgid: %+v", st)
	}
}

func TestRerunWithLiveService(t *testing.T) {
	paths := testPaths(t)
	repoDir, commitA := newFixtureRepo(t, "hello-v1\n")
	proj := &project.Project{
		Name: "demo",
		Tree: []project.Node{
			{Path: "app", Mount: &project.Mount{Provider: "git", Primary: true, Params: map[string]string{"repo": repoDir, "branch": "main"}}},
		},
		Pipeline: []project.Step{
			{Name: "serve", Run: "sleep 300", Background: true},
		},
	}
	if err := project.Save(paths, proj); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { killInstance(t, paths, "demo", "main") })
	m := newManager(paths, 23995, 23999)

	res, err := m.Run(context.Background(), run.Request{
		Project: "demo", Instance: "main", Branch: "main", Commit: commitA, Action: "run",
	}, io.Discard)
	if err != nil || res.Status != run.StatusSuccess {
		t.Fatalf("run: %v %v", res, err)
	}
	st, err := instance.LoadState(paths, "demo", "main")
	if err != nil {
		t.Fatal(err)
	}
	pgid1 := st.PGID

	commitFixture(t, repoDir, "hello-v2\n")

	res2, err := m.Run(context.Background(), run.Request{Project: "demo", Instance: "main", Action: "rerun"}, io.Discard)
	if err != nil || res2.Status != run.StatusSuccess {
		t.Fatalf("rerun: %v %v\n%s", res2, err, readBuildLog(t, paths, "demo", "main", res2.RunID))
	}
	st, err = instance.LoadState(paths, "demo", "main")
	if err != nil {
		t.Fatal(err)
	}
	// The old service is replaced, the new one tracked.
	if st.PGID == pgid1 || !instance.Alive(st.PGID) {
		t.Fatalf("rerun did not cycle the service: %+v (previous pgid %d)", st, pgid1)
	}
	if instance.Alive(pgid1) {
		t.Fatal("old service survived rerun")
	}
	// Content still matches the instance's last commit.
	data, err := os.ReadFile(filepath.Join(paths.Instances, "demo", "main", "app", "hello.txt"))
	if err != nil || string(data) != "hello-v1\n" {
		t.Fatalf("app/hello.txt = %q, %v", data, err)
	}
	if st.Commit != commitA {
		t.Fatalf("state commit = %q, want %q", st.Commit, commitA)
	}
}

// TestBackgroundStepLogLandsInInstanceLogDir pins the business-log location:
// the supervised background step's stdout/stderr must land in
// <root>/instances/<project>/<instance>/logs/service.log — the root the
// logs API serves — not in the daemon's top-level <root>/logs dir.
func TestBackgroundStepLogLandsInInstanceLogDir(t *testing.T) {
	paths := testPaths(t)
	proj := &project.Project{
		Name: "demo",
		Pipeline: []project.Step{
			{Name: "serve", Run: `echo service-marker && sleep 300`, Background: true},
		},
	}
	if err := project.Save(paths, proj); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { killInstance(t, paths, "demo", "main") })
	m := newManager(paths, 24000, 24010)

	res, err := m.Run(context.Background(), run.Request{Project: "demo", Instance: "main", Action: "run"}, io.Discard)
	if err != nil || res.Status != run.StatusSuccess {
		t.Fatalf("run: %v %v", res, err)
	}

	logPath := filepath.Join(paths.Instances, "demo", "main", "logs", "service.log")
	deadline := time.Now().Add(5 * time.Second)
	for {
		data, err := os.ReadFile(logPath)
		if err == nil && strings.Contains(string(data), "service-marker") {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("service.log lacks marker at %s (err=%v)", logPath, err)
		}
		time.Sleep(20 * time.Millisecond)
	}
	if _, err := os.Stat(filepath.Join(paths.Logs, "demo")); !errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("background output must not land under <root>/logs, stat err = %v", err)
	}
}

// recordHandler is a slog.Handler that captures every record for
// assertions.
type recordHandler struct {
	mu   sync.Mutex
	recs []slog.Record
}

func (h *recordHandler) Enabled(context.Context, slog.Level) bool { return true }
func (h *recordHandler) Handle(_ context.Context, r slog.Record) error {
	h.mu.Lock()
	h.recs = append(h.recs, r.Clone())
	h.mu.Unlock()
	return nil
}
func (h *recordHandler) WithAttrs([]slog.Attr) slog.Handler { return h }
func (h *recordHandler) WithGroup(string) slog.Handler      { return h }

// findRecord returns the first captured record with the given message.
func (h *recordHandler) findRecord(msg string) *slog.Record {
	h.mu.Lock()
	defer h.mu.Unlock()
	for i := range h.recs {
		if h.recs[i].Message == msg {
			return &h.recs[i]
		}
	}
	return nil
}

// attrValue returns the string rendering of the record's attr key, and
// whether it was present.
func attrValue(r *slog.Record, key string) (string, bool) {
	var v string
	ok := false
	r.Attrs(func(a slog.Attr) bool {
		if a.Key == key {
			v = a.Value.String()
			ok = true
			return false
		}
		return true
	})
	return v, ok
}

// TestRunEmitsDaemonLogRecords pins the run engine's daemon-log
// instrumentation: a run emits run-started and run-finished
// records with the required keys, plus the lifecycle events in between. It
// also covers the display host: a Request carrying DisplayHost yields a
// CI_URL for that host instead of the loopback fallback.
func TestRunEmitsDaemonLogRecords(t *testing.T) {
	paths := testPaths(t)
	proj := &project.Project{
		Name: "demo",
		Pipeline: []project.Step{
			{Name: "build", Run: "true"},
			{Name: "serve", Run: "sleep 300", Background: true,
				Health: &project.Health{Type: "command", Target: "true", Interval: 1, Retries: 3}},
		},
	}
	if err := project.Save(paths, proj); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { killInstance(t, paths, "demo", "main") })

	h := &recordHandler{}
	m := newManagerWithLogger(paths, 24020, 24030, slog.New(h))
	var out bytes.Buffer
	res, err := m.Run(context.Background(), run.Request{
		Project: "demo", Instance: "main", Action: "run",
		User: "tester", DisplayHost: "ci.example.com",
	}, &out)
	if err != nil || res.Status != run.StatusSuccess {
		t.Fatalf("run: %v %v\n%s", res, err, out.String())
	}

	started := h.findRecord("run started")
	if started == nil {
		t.Fatal("no 'run started' record")
	}
	for _, key := range []string{"run_id", "project", "instance"} {
		if _, ok := attrValue(started, key); !ok {
			t.Fatalf("'run started' record missing %q: %v", key, started)
		}
	}
	if v, _ := attrValue(started, "run_id"); v != res.RunID {
		t.Fatalf("run started run_id = %q, want %q", v, res.RunID)
	}
	if v, _ := attrValue(started, "project"); v != "demo" {
		t.Fatalf("run started project = %q, want demo", v)
	}
	if v, _ := attrValue(started, "instance"); v != "main" {
		t.Fatalf("run started instance = %q, want main", v)
	}

	finished := h.findRecord("run finished")
	if finished == nil {
		t.Fatal("no 'run finished' record")
	}
	for _, key := range []string{"run_id", "project", "instance", "duration_ms", "status"} {
		if _, ok := attrValue(finished, key); !ok {
			t.Fatalf("'run finished' record missing %q: %v", key, finished)
		}
	}
	if v, _ := attrValue(finished, "status"); v != run.StatusSuccess {
		t.Fatalf("run finished status = %q, want SUCCESS", v)
	}

	for _, msg := range []string{"port acquired", "instance started"} {
		if h.findRecord(msg) == nil {
			t.Fatalf("no %q record", msg)
		}
	}

	// The display host flows into the CI_URL trailer.
	if want := "CI_URL=http://ci.example.com:" + strconv.Itoa(res.Port); !strings.Contains(out.String(), want) {
		t.Fatalf("out missing %q:\n%s", want, out.String())
	}
	if res.URL != "http://ci.example.com:"+strconv.Itoa(res.Port) {
		t.Fatalf("res.URL = %q, want ci.example.com host", res.URL)
	}
}

// TestTrailersStartOnNewLine pins that the CI_STATUS= trailer never glues
// onto unterminated build output.
func TestTrailersStartOnNewLine(t *testing.T) {
	paths := testPaths(t)
	proj := &project.Project{
		Name: "demo",
		Pipeline: []project.Step{
			{Name: "build", Run: "printf unterminated"},
		},
	}
	if err := project.Save(paths, proj); err != nil {
		t.Fatal(err)
	}
	m := newManager(paths, 24040, 24050)
	var out bytes.Buffer
	res, err := m.Run(context.Background(), run.Request{Project: "demo", Instance: "main", Action: "build"}, &out)
	if err != nil || res.Status != run.StatusSuccess {
		t.Fatalf("run: %v %v\n%s", res, err, out.String())
	}
	if !strings.Contains(out.String(), "unterminated\nCI_STATUS=SUCCESS\n") {
		t.Fatalf("trailer not on its own line:\n%q", out.String())
	}
}

// A push from a non-primary (satellite) git node triggers a run: the pushed
// commit lands in the matching node while every other git node keeps its
// snapshot content — a satellite repo advances only when pushed through
// pushrun, never by following its source remote.
func TestSatelliteTriggerAndSnapshotSemantics(t *testing.T) {
	paths := testPaths(t)
	priDir, priCommit1 := newFixtureRepo(t, "app-v1\n")
	satDir, _ := newFixtureRepo(t, "lib-v1\n")
	satIdentity, err := gitx.NormalizeRepo(satDir)
	if err != nil {
		t.Fatal(err)
	}
	proj := &project.Project{
		Name: "demo",
		Tree: []project.Node{
			{Path: "app", Mount: &project.Mount{Provider: "git", Primary: true, Params: map[string]string{"repo": priDir, "branch": "main"}}},
			{Path: "lib", Mount: &project.Mount{Provider: "git", Params: map[string]string{"repo": satDir, "branch": "main"}}},
		},
		Pipeline: []project.Step{
			{Name: "capture", Run: `cat app/hello.txt > seen-app.txt; cat lib/hello.txt > seen-lib.txt; echo "ws=$CI_WORKSPACE"; echo "tdir=$CI_TRIGGER_PROJECT_DIR"; echo "tname=$CI_TRIGGER_PROJECT_NAME"; echo "pdir=$CI_PROJECT_DIR"; echo "pname=$CI_PROJECT_NAME"`},
		},
	}
	if err := project.Save(paths, proj); err != nil {
		t.Fatal(err)
	}
	m := newManager(paths, 24100, 24110)
	instDir := filepath.Join(paths.Instances, "demo", "main")

	// Run 1 (API-triggered): both repos are cold and get seeded from their
	// source remotes.
	var out bytes.Buffer
	res, err := m.Run(context.Background(), run.Request{
		Project: "demo", Instance: "main", Branch: "main", Commit: priCommit1, Action: "run",
	}, &out)
	if err != nil || res.Status != run.StatusSuccess {
		t.Fatalf("run 1: %v %v\n%s", err, res, out.String())
	}
	assertNodeContent(t, instDir, "seen-app.txt", "app-v1\n")
	assertNodeContent(t, instDir, "seen-lib.txt", "lib-v1\n")

	// The API-triggered run names the primary node in both env pairs.
	for _, want := range []string{
		"ws=" + instDir,
		"tdir=" + filepath.Join(instDir, "app"),
		"pdir=" + filepath.Join(instDir, "app"),
	} {
		if !strings.Contains(out.String(), want) {
			t.Fatalf("run 1 output missing %q:\n%s", want, out.String())
		}
	}
	priName, _ := gitx.NormalizeRepo(priDir)
	if want := "pname=" + gitx.RepoPath(priName); !strings.Contains(out.String(), want) {
		t.Fatalf("run 1 output missing %q:\n%s", want, out.String())
	}

	// Advance both source remotes. Nothing may leak into later runs.
	priCommit2 := commitFixture(t, priDir, "app-v2\n")
	satCommit2 := commitFixture(t, satDir, "lib-v2\n")

	// Run 2 (API-triggered again): snapshots hold.
	out.Reset()
	res, err = m.Run(context.Background(), run.Request{
		Project: "demo", Instance: "main", Branch: "main", Commit: priCommit1, Action: "run",
	}, &out)
	if err != nil || res.Status != run.StatusSuccess {
		t.Fatalf("run 2: %v %v\n%s", err, res, out.String())
	}
	assertNodeContent(t, instDir, "seen-app.txt", "app-v1\n")
	assertNodeContent(t, instDir, "seen-lib.txt", "lib-v1\n")

	// A push of the satellite repo through pushrun (emulated here by moving
	// the snapshot ref the way receive-pack would) triggers a run whose
	// commit lands in the lib node; the primary node keeps its snapshot.
	satBare, err := gitx.BareRepoDir(paths, satDir)
	if err != nil {
		t.Fatal(err)
	}
	git(t, satBare, "--git-dir="+satBare, "fetch", satDir, "+refs/heads/main:refs/pushrun/for/main")

	out.Reset()
	res, err = m.Run(context.Background(), run.Request{
		Project: "demo", Instance: "main", Branch: "main", Commit: satCommit2,
		Action: "run", TriggerRepo: satIdentity,
	}, &out)
	if err != nil || res.Status != run.StatusSuccess {
		t.Fatalf("run 3 (satellite push): %v %v\n%s", err, res, out.String())
	}
	assertNodeContent(t, instDir, "seen-lib.txt", "lib-v2\n")
	assertNodeContent(t, instDir, "seen-app.txt", "app-v1\n")
	if st, err := instance.LoadState(paths, "demo", "main"); err != nil || st.Commit != satCommit2 {
		t.Fatalf("state = %+v, %v; want commit %q", st, err, satCommit2)
	}

	// The trigger env pair names the satellite repo; the project env pair
	// still names the primary.
	for _, want := range []string{
		"tdir=" + filepath.Join(instDir, "lib"),
		"tname=" + gitx.RepoPath(satIdentity),
		"pdir=" + filepath.Join(instDir, "app"),
	} {
		if !strings.Contains(out.String(), want) {
			t.Fatalf("run 3 output missing %q:\n%s", want, out.String())
		}
	}
	_ = priCommit2
}

// A TriggerRepo the project does not mount is an engine-level error (the
// hook path rejects it earlier, in pre-receive).
func TestTriggerRepoNotMountedFails(t *testing.T) {
	paths := testPaths(t)
	repoDir, commit := newFixtureRepo(t, "hello-v1\n")
	proj := &project.Project{
		Name: "demo",
		Tree: []project.Node{
			{Path: "app", Mount: &project.Mount{Provider: "git", Primary: true, Params: map[string]string{"repo": repoDir, "branch": "main"}}},
		},
		Pipeline: []project.Step{{Name: "noop", Run: "true"}},
	}
	if err := project.Save(paths, proj); err != nil {
		t.Fatal(err)
	}
	m := newManager(paths, 24120, 24130)
	var out bytes.Buffer
	res, err := m.Run(context.Background(), run.Request{
		Project: "demo", Instance: "main", Branch: "main", Commit: commit,
		Action: "run", TriggerRepo: "git.example.com/team/ghost",
	}, &out)
	if err == nil {
		t.Fatal("run with an unmounted TriggerRepo succeeded, want error")
	}
	if !strings.Contains(err.Error(), "git.example.com/team/ghost") {
		t.Fatalf("error does not name the trigger repo: %v", err)
	}
	if res == nil || res.Status != run.StatusFailed {
		t.Fatalf("result = %+v, want FAILED", res)
	}
}

// A satellite-triggered instance must restart with the same CI_TRIGGER_*
// environment it had originally: the trigger repo rides in the instance
// state and start rebuilds the trigger env from it.
func TestStartRestoresSatelliteTriggerEnv(t *testing.T) {
	paths := testPaths(t)
	priDir, _ := newFixtureRepo(t, "app-v1\n")
	satDir, satCommit := newFixtureRepo(t, "lib-v1\n")
	satIdentity, err := gitx.NormalizeRepo(satDir)
	if err != nil {
		t.Fatal(err)
	}
	proj := &project.Project{
		Name: "demo",
		Tree: []project.Node{
			{Path: "app", Mount: &project.Mount{Provider: "git", Primary: true, Params: map[string]string{"repo": priDir, "branch": "main"}}},
			{Path: "lib", Mount: &project.Mount{Provider: "git", Params: map[string]string{"repo": satDir, "branch": "main"}}},
		},
		Pipeline: []project.Step{
			{Name: "serve", Run: `env | grep -E '^CI_(TRIGGER_PROJECT_|PROJECT_)' | sort > "$CI_WORKSPACE/bg-env.txt"; sleep 300`,
				Background: true, Health: &project.Health{Type: "command", Target: "true", Interval: 1, Retries: 3}},
		},
	}
	if err := project.Save(paths, proj); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { killInstance(t, paths, "demo", "main") })
	m := newManager(paths, 24140, 24150)
	instDir := filepath.Join(paths.Instances, "demo", "main")
	envFile := filepath.Join(instDir, "bg-env.txt")

	res, err := m.Run(context.Background(), run.Request{
		Project: "demo", Instance: "main", Branch: "main", Commit: satCommit,
		Action: "run", TriggerRepo: satIdentity,
	}, io.Discard)
	if err != nil || res.Status != run.StatusSuccess {
		t.Fatalf("run: %v %v", err, res)
	}

	st, err := instance.LoadState(paths, "demo", "main")
	if err != nil {
		t.Fatal(err)
	}
	if st.TriggerRepo != satIdentity {
		t.Fatalf("state trigger_repo = %q, want %q", st.TriggerRepo, satIdentity)
	}

	priIdentity, err := gitx.NormalizeRepo(priDir)
	if err != nil {
		t.Fatal(err)
	}
	want := "CI_PROJECT_DIR=" + filepath.Join(instDir, "app") + "\n" +
		"CI_PROJECT_NAME=" + gitx.RepoPath(priIdentity) + "\n" +
		"CI_TRIGGER_PROJECT_DIR=" + filepath.Join(instDir, "lib") + "\n" +
		"CI_TRIGGER_PROJECT_NAME=" + gitx.RepoPath(satIdentity) + "\n"
	waitFileContent(t, envFile, want)

	// stop, then start: the restarted process must see the satellite trigger
	// env, not the primary fallback.
	if res, err := m.Run(context.Background(), run.Request{Project: "demo", Instance: "main", Action: "stop"}, io.Discard); err != nil || res.Status != run.StatusSuccess {
		t.Fatalf("stop: %v %v", err, res)
	}
	if err := os.Remove(envFile); err != nil {
		t.Fatal(err)
	}
	var out bytes.Buffer
	res, err = m.Run(context.Background(), run.Request{Project: "demo", Instance: "main", Action: "start"}, &out)
	if err != nil || res.Status != run.StatusSuccess {
		t.Fatalf("start: %v %v\n%s", err, res, out.String())
	}
	waitFileContent(t, envFile, want)
}

// waitFileContent polls until path holds exactly want (a background step's
// first write races the health check that lets Run return).
func waitFileContent(t *testing.T, path, want string) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for {
		if data, err := os.ReadFile(path); err == nil && string(data) == want {
			return
		}
		if time.Now().After(deadline) {
			data, _ := os.ReadFile(path)
			t.Fatalf("%s = %q, want %q", path, data, want)
		}
		time.Sleep(20 * time.Millisecond)
	}
}

func assertNodeContent(t *testing.T, instDir, name, want string) {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(instDir, name))
	if err != nil {
		t.Fatalf("read %s: %v", name, err)
	}
	if string(data) != want {
		t.Fatalf("%s = %q, want %q", name, data, want)
	}
}

func readBuildLog(t *testing.T, paths config.Paths, proj, inst, runID string) string {
	t.Helper()
	rc, err := run.OpenBuildLog(paths, proj, inst, runID)
	if err != nil {
		return err.Error()
	}
	defer func() { _ = rc.Close() }()
	data, _ := io.ReadAll(rc)
	return string(data)
}

func runDirName(i int) string {
	return "old-" + strings.Repeat("0", 2-len(strconv.Itoa(i))) + strconv.Itoa(i)
}
