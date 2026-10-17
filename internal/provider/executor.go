package provider

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/bunnyzr/pushrun/internal/config"
	"github.com/bunnyzr/pushrun/internal/gitx"
)

// gitBuiltinID is the id of the builtin git provider, which is code, not a
// script: the daemon's bare repo for the repo identity is the shared cache,
// and install checks out the requested commit (trigger node) or the snapshot
// ref refs/pushrun/for/<branch> (non-trigger nodes) into the target dir.
const gitBuiltinID = "git"

// readyMarker is the file written into a warmed cache dir once its warmup
// script has exited zero; its content is the warmup fingerprint.
const readyMarker = ".pushrun-ready"

// warmupRecordFile is the JSON record (WarmupInfo without Warming) written
// alongside the ready marker, so warmup state is observable after the fact.
const warmupRecordFile = "warmup.json"

// WarmupInfo describes one shared-artifact cache entry for a provider: the
// fingerprint it was warmed under, when, and with which (secret-redacted)
// params. Warming reports an in-flight warmup. It doubles as the warmup.json
// on-disk schema; Warming is runtime-only and never persisted.
type WarmupInfo struct {
	Fingerprint string            `json:"fingerprint"`
	WarmedAt    time.Time         `json:"warmed_at"`
	Params      map[string]string `json:"params,omitempty"`
	Warming     bool              `json:"warming,omitempty"`
}

// Fingerprint returns the sha256 hex digest of a provider id plus its
// canonicalized warmup-scoped parameters (sorted "k=v" pairs, one per line).
func Fingerprint(id string, warmupParams map[string]string) string {
	keys := make([]string, 0, len(warmupParams))
	for k := range warmupParams {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	var sb strings.Builder
	sb.WriteString(id)
	for _, k := range keys {
		sb.WriteByte('\n')
		sb.WriteString(k)
		sb.WriteByte('=')
		sb.WriteString(warmupParams[k])
	}
	sum := sha256.Sum256([]byte(sb.String()))
	return hex.EncodeToString(sum[:])
}

// WarmupFingerprint returns the cache fingerprint EnsureWarmed would use for
// p with params: the provider id plus the resolved (defaults applied)
// warmup-scoped parameters. Callers that dedupe warmups across a project
// tree key on this, so two mounts of one provider with distinct warmup
// params each warm once.
func WarmupFingerprint(p *Provider, params map[string]string) string {
	resolved, _ := applyParams(p, ScopeWarmup, params)
	return Fingerprint(p.ID, resolved)
}

// Executor runs provider lifecycle scripts (warmup and install) per the
// contract in docs/provider-contract.md.
type Executor struct {
	paths     config.Paths
	gitScheme string
	// warming tracks in-flight warmups keyed by id+"\x00"+fingerprint, so
	// Warmups can report a not-yet-ready cache as warming.
	warming sync.Map
}

// NewExecutor returns an Executor rooted at the data-root layout in paths.
// cfg supplies the git source-scheme default; nil means the platform
// defaults (https).
func NewExecutor(paths config.Paths, cfg *config.Config) *Executor {
	scheme := "https"
	if cfg != nil && cfg.Git.Scheme != "" {
		scheme = cfg.Git.Scheme
	}
	return &Executor{paths: paths, gitScheme: scheme}
}

// EnsureWarmed returns the warmed shared-artifact cache dir for
// (p.ID, fingerprint of warmup-scoped params), running the warmup script if
// the ready marker is missing or stale. Warmups are serialized per
// (id, fingerprint) with a flock, so concurrent callers share one build. A
// failed warmup leaves neither marker nor cache dir behind.
func (e *Executor) EnsureWarmed(ctx context.Context, p *Provider, params map[string]string, log io.Writer) (string, error) {
	resolved, secrets := applyParams(p, ScopeWarmup, params)
	if err := checkRequired(p, ScopeWarmup, resolved); err != nil {
		return "", err
	}
	fp := Fingerprint(p.ID, resolved)
	cacheDir := e.cacheDir(p.ID, fp)

	unlock, err := e.lock(p.ID, fp)
	if err != nil {
		return "", err
	}
	defer unlock()

	if p.ID == gitBuiltinID {
		return e.warmupGit(p, resolved, log)
	}

	if markerMatches(cacheDir, fp) {
		return cacheDir, nil
	}

	if p.Warmup == "" {
		// No warmup phase: the cache dir is an empty shared artifact dir.
		if err := os.RemoveAll(cacheDir); err != nil {
			return "", fmt.Errorf("provider %q: clear stale cache dir: %w", p.ID, err)
		}
		if err := os.MkdirAll(cacheDir, 0o755); err != nil {
			return "", fmt.Errorf("provider %q: create cache dir: %w", p.ID, err)
		}
		if err := os.WriteFile(filepath.Join(cacheDir, readyMarker), []byte(fp), 0o644); err != nil {
			return "", fmt.Errorf("provider %q: write ready marker: %w", p.ID, err)
		}
		if err := writeWarmupRecord(cacheDir, p, fp, resolved); err != nil {
			return "", fmt.Errorf("provider %q: write warmup record: %w", p.ID, err)
		}
		return cacheDir, nil
	}

	// Build in a sibling staging dir; rename into place only on success.
	if err := os.MkdirAll(e.paths.ProviderData, 0o755); err != nil {
		return "", fmt.Errorf("provider %q: create provider-data dir: %w", p.ID, err)
	}
	staging, err := os.MkdirTemp(e.paths.ProviderData, ".staging-"+p.ID+"-*")
	if err != nil {
		return "", fmt.Errorf("provider %q: create staging dir: %w", p.ID, err)
	}
	defer func() { _ = os.RemoveAll(staging) }()

	warmingKey := p.ID + "\x00" + fp
	e.warming.Store(warmingKey, struct{}{})
	defer e.warming.Delete(warmingKey)

	env := scriptEnv(ScopeWarmup, resolved, staging, "")
	if err := e.runScript(ctx, p, p.Warmup, env, secrets, log); err != nil {
		return "", fmt.Errorf("provider %q warmup failed: %w", p.ID, err)
	}
	if err := os.WriteFile(filepath.Join(staging, readyMarker), []byte(fp), 0o644); err != nil {
		return "", fmt.Errorf("provider %q: write ready marker: %w", p.ID, err)
	}
	if err := writeWarmupRecord(staging, p, fp, resolved); err != nil {
		return "", fmt.Errorf("provider %q: write warmup record: %w", p.ID, err)
	}
	if err := os.RemoveAll(cacheDir); err != nil {
		return "", fmt.Errorf("provider %q: clear stale cache dir: %w", p.ID, err)
	}
	if err := os.Rename(staging, cacheDir); err != nil {
		return "", fmt.Errorf("provider %q: promote cache dir: %w", p.ID, err)
	}
	return cacheDir, nil
}

// Warm reports whether the shared cache for (p, params) is present and
// current, so callers can tell a skipped warmup from a real one. For the git
// builtin, warm means the snapshot ref refs/pushrun/for/<branch> exists in
// the bare repo — snapshot content only changes when a push lands, so a warm
// git node is never re-fetched.
func (e *Executor) Warm(p *Provider, params map[string]string) bool {
	resolved, _ := applyParams(p, ScopeWarmup, params)
	if p.ID == gitBuiltinID {
		repoDir, err := gitx.BareRepoDir(e.paths, resolved["repo"])
		if err != nil {
			return false
		}
		_, err = gitx.ResolveRef(repoDir, snapshotRef(resolved["branch"]))
		return err == nil
	}
	fp := Fingerprint(p.ID, resolved)
	return markerMatches(e.cacheDir(p.ID, fp), fp)
}

// WarmStatus reports the warmup state of the cache EnsureWarmed would use
// for (p, params): "warming" while a warmup of that cache is in flight in
// this process, "warm" when the cache is present and current, otherwise
// "cold". The git builtin owns no ProviderData cache dir — its cache is the
// bare repo itself — so it is warm iff the snapshot ref
// refs/pushrun/for/<branch> exists, and Warmups has nothing to say about it.
// The returned time is the warmup's completion time when known: from the
// warmup.json record (falling back to the ready marker's mtime) for scripted
// providers, or the snapshot ref's mtime for git.
func (e *Executor) WarmStatus(p *Provider, params map[string]string) (status string, warmedAt time.Time) {
	resolved, _ := applyParams(p, ScopeWarmup, params)
	if p.ID == gitBuiltinID {
		repoDir, err := gitx.BareRepoDir(e.paths, resolved["repo"])
		if err != nil {
			return "cold", time.Time{}
		}
		ref := snapshotRef(resolved["branch"])
		if _, err := gitx.ResolveRef(repoDir, ref); err != nil {
			return "cold", time.Time{}
		}
		if fi, err := os.Stat(filepath.Join(repoDir, filepath.FromSlash(ref))); err == nil {
			warmedAt = fi.ModTime()
		}
		return "warm", warmedAt
	}
	fp := Fingerprint(p.ID, resolved)
	if _, ok := e.warming.Load(p.ID + "\x00" + fp); ok {
		return "warming", time.Time{}
	}
	cacheDir := e.cacheDir(p.ID, fp)
	if !markerMatches(cacheDir, fp) {
		return "cold", time.Time{}
	}
	if data, err := os.ReadFile(filepath.Join(cacheDir, warmupRecordFile)); err == nil {
		var rec WarmupInfo
		if err := json.Unmarshal(data, &rec); err == nil {
			warmedAt = rec.WarmedAt
		}
	}
	if warmedAt.IsZero() {
		if fi, err := os.Stat(filepath.Join(cacheDir, readyMarker)); err == nil {
			warmedAt = fi.ModTime()
		}
	}
	return "warm", warmedAt
}

// Warmups enumerates the shared-artifact cache dirs of provider id
// (ProviderData/<id>-<fingerprint>), newest first. An entry is Warming while
// its warmup is in flight in this process; otherwise it is warm iff its
// ready marker matches the fingerprint in the dir name. Params and WarmedAt
// come from the warmup.json record when present, falling back to the ready
// marker's mtime for caches warmed before records existed.
func (e *Executor) Warmups(id string) []WarmupInfo {
	entries, err := os.ReadDir(e.paths.ProviderData)
	if err != nil {
		return nil
	}
	prefix := id + "-"
	var infos []WarmupInfo
	for _, entry := range entries {
		if !entry.IsDir() || !strings.HasPrefix(entry.Name(), prefix) {
			continue
		}
		fp := strings.TrimPrefix(entry.Name(), prefix)
		dir := filepath.Join(e.paths.ProviderData, entry.Name())
		info := WarmupInfo{Fingerprint: fp}
		if _, ok := e.warming.Load(id + "\x00" + fp); ok {
			info.Warming = true
		}
		if data, err := os.ReadFile(filepath.Join(dir, warmupRecordFile)); err == nil {
			var rec WarmupInfo
			if err := json.Unmarshal(data, &rec); err == nil {
				info.Params = rec.Params
				info.WarmedAt = rec.WarmedAt
			}
		}
		if info.WarmedAt.IsZero() {
			if fi, err := os.Stat(filepath.Join(dir, readyMarker)); err == nil {
				info.WarmedAt = fi.ModTime()
			}
		}
		infos = append(infos, info)
	}
	sort.Slice(infos, func(i, j int) bool { return infos[i].WarmedAt.After(infos[j].WarmedAt) })
	return infos
}

// Install runs the provider's install script against targetDir, injecting
// the contract environment (target dir, phase, cache dir of the warmed
// artifact, install-scoped params). The git builtin is dispatched to code:
// it checks out the requested commit (or the snapshot ref) from the bare
// repo.
func (e *Executor) Install(ctx context.Context, p *Provider, params map[string]string, targetDir string, log io.Writer) error {
	if p.ID == gitBuiltinID {
		return e.installGit(p, params, targetDir, log)
	}
	resolved, secrets := applyParams(p, ScopeInstall, params)
	if err := checkRequired(p, ScopeInstall, resolved); err != nil {
		return err
	}
	// The cache dir is keyed by warmup-scoped params; resolve them without
	// required-checks so install-only callers still get a stable pointer.
	warmupResolved, _ := applyParams(p, ScopeWarmup, params)
	cacheDir := e.cacheDir(p.ID, Fingerprint(p.ID, warmupResolved))

	env := scriptEnv(ScopeInstall, resolved, cacheDir, targetDir)
	if err := e.runScript(ctx, p, p.Install, env, secrets, log); err != nil {
		return fmt.Errorf("provider %q install failed: %w", p.ID, err)
	}
	return nil
}

// cacheDir returns the shared-artifact cache dir for a provider id and
// warmup fingerprint. The git builtin never uses it: its cache is the bare
// repo itself (see warmupGit).
func (e *Executor) cacheDir(id, fp string) string {
	return filepath.Join(e.paths.ProviderData, id+"-"+fp)
}

// snapshotRef is the bare-repo ref holding the content a git node installs
// when it did not trigger the run.
func snapshotRef(branch string) string {
	return "refs/pushrun/for/" + branch
}

// warmupGit ensures the bare repo for the node's repo identity exists and
// holds a snapshot of the declared branch. Snapshot semantics: when
// refs/pushrun/for/<branch> already exists the repo is warm and left
// untouched — content only changes when someone pushes the branch through
// pushrun. Only a cold repo (no snapshot ref) is seeded, with a single fetch
// of the branch head from the source remote.
func (e *Executor) warmupGit(p *Provider, resolved map[string]string, log io.Writer) (string, error) {
	repoDir, err := gitx.BareRepoDir(e.paths, resolved["repo"])
	if err != nil {
		return "", fmt.Errorf("provider %q warmup failed: %w", p.ID, err)
	}
	ref := snapshotRef(resolved["branch"])
	if _, err := gitx.ResolveRef(repoDir, ref); err == nil {
		return repoDir, nil // warm: snapshot ref already exists
	}
	if err := gitx.InitBareRepo(repoDir); err != nil {
		return "", fmt.Errorf("provider %q warmup failed: %w", p.ID, err)
	}
	source := gitx.SourceURL(resolved["repo"], e.gitScheme)
	if log != nil {
		_, _ = fmt.Fprintf(log, "seeding %s from %s\n", ref, source)
	}
	if err := gitx.FetchSnapshot(repoDir, source, resolved["branch"]); err != nil {
		return "", fmt.Errorf("provider %q warmup failed: %w", p.ID, err)
	}
	return repoDir, nil
}

// installGit checks out content from the bare repo into targetDir. A node
// that triggered the run carries an explicit commit param and gets exactly
// that commit; any other node gets the snapshot ref
// refs/pushrun/for/<branch>. A per-checkout git index file makes concurrent
// installs from the shared bare repo safe.
func (e *Executor) installGit(p *Provider, params map[string]string, targetDir string, log io.Writer) error {
	resolved, _ := applyParams(p, ScopeInstall, params)
	warmupResolved, _ := applyParams(p, ScopeWarmup, params)
	repoDir, err := gitx.BareRepoDir(e.paths, warmupResolved["repo"])
	if err != nil {
		return fmt.Errorf("provider %q install failed: %w", p.ID, err)
	}

	commit := resolved["commit"]
	if commit == "" {
		ref := snapshotRef(warmupResolved["branch"])
		c, err := gitx.ResolveRef(repoDir, ref)
		if err != nil {
			return fmt.Errorf("provider %q install failed: resolve %s: %w", p.ID, ref, err)
		}
		commit = c
	}

	parent := filepath.Dir(targetDir)
	if err := os.MkdirAll(parent, 0o755); err != nil {
		return fmt.Errorf("provider %q install failed: %w", p.ID, err)
	}
	tmp, err := os.CreateTemp(parent, ".pushrun-git-index-*")
	if err != nil {
		return fmt.Errorf("provider %q install failed: %w", p.ID, err)
	}
	indexFile := tmp.Name()
	_ = tmp.Close()
	// git creates the index fresh; remove the placeholder so checkout
	// starts from an empty index.
	_ = os.Remove(indexFile)
	defer func() { _ = os.Remove(indexFile) }()

	if log != nil {
		_, _ = fmt.Fprintf(log, "checkout %s from %s\n", commit, warmupResolved["repo"])
	}
	if err := gitx.CheckoutCommitWithIndex(repoDir, targetDir, commit, indexFile); err != nil {
		return fmt.Errorf("provider %q install failed: %w", p.ID, err)
	}
	return nil
}

// applyParams resolves the declared params of one scope: caller-supplied
// values win, declared defaults fill the rest. It returns the resolved
// values plus the values of secret params (for log redaction).
func applyParams(p *Provider, scope string, params map[string]string) (resolved map[string]string, secrets []string) {
	resolved = make(map[string]string)
	for _, prm := range p.Params {
		if prm.Scope != scope {
			continue
		}
		v := params[prm.ID]
		if v == "" {
			v = prm.Default
		}
		resolved[prm.ID] = v
		if prm.Secret && v != "" {
			secrets = append(secrets, v)
		}
	}
	return resolved, secrets
}

// checkRequired verifies that every required param of the scope has a
// non-empty resolved value, before any script runs.
func checkRequired(p *Provider, scope string, resolved map[string]string) error {
	for _, prm := range p.Params {
		if prm.Scope == scope && prm.Required && resolved[prm.ID] == "" {
			return fmt.Errorf("provider %q: missing required %s parameter %q", p.ID, scope, prm.ID)
		}
	}
	return nil
}

// scriptEnv builds the contract environment for a phase: the cache/target
// dir, the phase name, and one PUSHRUN_PROVIDER_PARAM_<ID> per resolved
// param of that scope (id upper-cased).
func scriptEnv(phase string, resolved map[string]string, cacheDir, targetDir string) []string {
	env := []string{
		"PUSHRUN_PROVIDER_CACHE_DIR=" + cacheDir,
		"PUSHRUN_PROVIDER_TARGET_DIR=" + targetDir,
		"PUSHRUN_PROVIDER_PHASE=" + phase,
	}
	keys := make([]string, 0, len(resolved))
	for k := range resolved {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		env = append(env, "PUSHRUN_PROVIDER_PARAM_"+strings.ToUpper(k)+"="+resolved[k])
	}
	return env
}

// runScript executes script via /bin/bash with the contract env, streaming
// output to log with secret values redacted. Cancelling ctx kills the
// process.
func (e *Executor) runScript(ctx context.Context, p *Provider, script string, env []string, secrets []string, log io.Writer) error {
	if log == nil {
		log = io.Discard
	}
	rw := newRedactWriter(log, secrets)
	var cmd *exec.Cmd
	if content, ok := builtinScript(p.ID, script); ok {
		// Builtin scripts are embedded, not stored under the data root's
		// provider dir; feed the content to bash on stdin.
		cmd = exec.CommandContext(ctx, "/bin/bash", "-s")
		cmd.Stdin = strings.NewReader(content)
		cmd.Dir = e.paths.Root
	} else {
		cmd = exec.CommandContext(ctx, "/bin/bash", filepath.Join(e.paths.Providers, p.ID, filepath.FromSlash(script)))
		cmd.Dir = filepath.Join(e.paths.Providers, p.ID)
	}
	cmd.Env = append(os.Environ(), env...)
	cmd.Stdout = rw
	cmd.Stderr = rw
	err := cmd.Run()
	if ferr := rw.Flush(); err == nil {
		err = ferr
	}
	return err
}

// lock takes the per-(id, fingerprint) flock that serializes concurrent
// warmups, and returns the unlock function.
func (e *Executor) lock(id, fp string) (func(), error) {
	if err := os.MkdirAll(e.paths.Locks, 0o755); err != nil {
		return nil, fmt.Errorf("provider %q: create locks dir: %w", id, err)
	}
	name := filepath.Join(e.paths.Locks, "provider-"+id+"-"+fp)
	f, err := os.OpenFile(name, os.O_CREATE|os.O_RDWR, 0o644)
	if err != nil {
		return nil, fmt.Errorf("provider %q: open lock file: %w", id, err)
	}
	if err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX); err != nil {
		_ = f.Close()
		return nil, fmt.Errorf("provider %q: acquire lock: %w", id, err)
	}
	return func() {
		_ = syscall.Flock(int(f.Fd()), syscall.LOCK_UN)
		_ = f.Close()
	}, nil
}

// markerMatches reports whether dir holds a ready marker whose content is
// exactly fp.
func markerMatches(dir, fp string) bool {
	data, err := os.ReadFile(filepath.Join(dir, readyMarker))
	return err == nil && string(data) == fp
}

// writeWarmupRecord writes the warmup.json record for (p, fp) into dir, with
// the resolved warmup-scoped params and secret values replaced by "***".
func writeWarmupRecord(dir string, p *Provider, fp string, resolved map[string]string) error {
	params := make(map[string]string, len(resolved))
	for _, prm := range p.Params {
		if prm.Scope != ScopeWarmup {
			continue
		}
		v := resolved[prm.ID]
		if prm.Secret && v != "" {
			v = "***"
		}
		params[prm.ID] = v
	}
	rec := WarmupInfo{
		Fingerprint: fp,
		WarmedAt:    time.Now().UTC().Truncate(time.Second),
		Params:      params,
	}
	data, err := json.Marshal(rec)
	if err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(dir, warmupRecordFile), data, 0o644)
}

// redactWriter streams to an underlying writer while replacing secret
// values with "***". It withholds a tail of maxSecretLen-1 bytes so secrets
// split across writes are still caught; Flush emits the tail.
type redactWriter struct {
	w       io.Writer
	secrets [][]byte
	buf     []byte
	keep    int
}

func newRedactWriter(w io.Writer, secrets []string) *redactWriter {
	rw := &redactWriter{w: w}
	maxLen := 0
	for _, s := range secrets {
		if s == "" {
			continue
		}
		rw.secrets = append(rw.secrets, []byte(s))
		if len(s) > maxLen {
			maxLen = len(s)
		}
	}
	if maxLen > 0 {
		rw.keep = maxLen - 1
	}
	return rw
}

func (rw *redactWriter) Write(p []byte) (int, error) {
	rw.buf = append(rw.buf, p...)
	if n := len(rw.buf) - rw.keep; n > 0 {
		if err := rw.emit(rw.buf[:n]); err != nil {
			return 0, err
		}
		rw.buf = rw.buf[n:]
	}
	return len(p), nil
}

// Flush writes any withheld tail. It must be called once the output stream
// ends.
func (rw *redactWriter) Flush() error {
	if len(rw.buf) == 0 {
		return nil
	}
	err := rw.emit(rw.buf)
	rw.buf = nil
	return err
}

func (rw *redactWriter) emit(p []byte) error {
	for _, s := range rw.secrets {
		p = bytes.ReplaceAll(p, s, []byte("***"))
	}
	_, err := rw.w.Write(p)
	return err
}
