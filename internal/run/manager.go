// Package run implements the pushrun run engine (see docs/design.md): the
// full chain from project resolution through provider warmup, instance
// assembly, pipeline steps, health checks, supervision, and run-record
// persistence.
package run

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"log/slog"
	"os"
	"path/filepath"
	"sort"
	"syscall"
	"time"

	"github.com/bunnyzr/pushrun/internal/config"
	"github.com/bunnyzr/pushrun/internal/fsutil"
	"github.com/bunnyzr/pushrun/internal/gitx"
	"github.com/bunnyzr/pushrun/internal/instance"
	"github.com/bunnyzr/pushrun/internal/project"
	"github.com/bunnyzr/pushrun/internal/provider"
)

// Action values accepted in Request.Action.
const (
	ActionRun     = "run"
	ActionSync    = "sync"
	ActionBuild   = "build"
	ActionTest    = "test"
	ActionStart   = "start"
	ActionStop    = "stop"
	ActionRestart = "restart"
	ActionRerun   = "rerun"
)

// Result statuses.
const (
	StatusSuccess = "SUCCESS"
	StatusFailed  = "FAILED"
	StatusBusy    = "BUSY"
)

// defaultInstance is the instance name used when a request leaves it empty.
const defaultInstance = "default"

// maxRunDirs is how many run directories per instance are kept on disk.
const maxRunDirs = 20

// Request is one trigger of the run engine.
type Request struct {
	Project  string
	Instance string
	Branch   string
	Commit   string
	Action   string
	User     string
	// TriggerRepo is the repo identity (host/path) whose push triggered the
	// run. The commit is injected into the tree node mounting this repo.
	// Empty (API-triggered runs) falls back to the primary git node.
	TriggerRepo string
	// DisplayHost is the host the triggering client used to reach the
	// daemon (host part of the push request's Host header). It becomes the
	// host of the CI_URL trailer so a pusher on another machine gets a
	// reachable URL. Empty falls back to 127.0.0.1 (API-triggered runs).
	DisplayHost string
}

// Result is the outcome of one Run call.
type Result struct {
	RunID  string
	Status string
	Port   int
	URL    string
}

// Record is the persisted result of one run, stored as result.json in the
// run directory. It is self-contained: besides the outcome it
// carries the triggering user and a snapshot of the pipeline as it ran, so
// a record stays meaningful after the project definition changed.
type Record struct {
	ID       string         `json:"id"`
	Commit   string         `json:"commit,omitempty"`
	Branch   string         `json:"branch,omitempty"`
	User     string         `json:"user,omitempty"`
	Pipeline []project.Step `json:"pipeline,omitempty"`
	Status   string         `json:"status"`
	// Error carries the chain failure reason on a failed run: warmup
	// failures name the provider and phase, step failures the step and exit,
	// health failures say so. Empty on success.
	Error      string       `json:"error,omitempty"`
	Steps      []StepResult `json:"steps,omitempty"`
	StartedAt  time.Time    `json:"started_at"`
	FinishedAt time.Time    `json:"finished_at"`
}

// StepResult is the recorded outcome of one pipeline step.
type StepResult struct {
	Name       string    `json:"name"`
	Status     string    `json:"status"`
	ExitCode   int       `json:"exit_code"`
	StartedAt  time.Time `json:"started_at"`
	FinishedAt time.Time `json:"finished_at"`
}

// Manager executes runs against instances. It is safe for concurrent use;
// runs of the same instance are serialized by a file lock.
type Manager struct {
	paths config.Paths
	cfg   *config.Config
	exec  *provider.Executor
	pool  *instance.PortPool
	log   *slog.Logger
}

// NewManager returns a Manager over the data-root layout in paths. A nil
// log discards daemon-log records.
func NewManager(paths config.Paths, cfg *config.Config, exec *provider.Executor, pool *instance.PortPool, log *slog.Logger) *Manager {
	if log == nil {
		log = slog.New(slog.NewTextHandler(io.Discard, nil))
	}
	return &Manager{paths: paths, cfg: cfg, exec: exec, pool: pool, log: log.With("component", "run")}
}

// Run executes the requested action and streams output to out. Unknown
// projects and invalid requests return an error without a Result; lock
// contention returns Result{Status: BUSY} with a nil error; chain failures
// return Result{Status: FAILED} together with the causing error. Output
// always ends with trailer lines (CI_STATUS=, plus CI_PORT=/CI_URL= when
// available).
func (m *Manager) Run(ctx context.Context, req Request, out io.Writer) (*Result, error) {
	if out == nil {
		out = io.Discard
	}
	if req.Instance == "" {
		req.Instance = defaultInstance
	}
	if !fsutil.ValidName(req.Instance) {
		return nil, fmt.Errorf("invalid instance name %q", req.Instance)
	}
	if req.TriggerRepo != "" {
		id, err := gitx.NormalizeRepo(req.TriggerRepo)
		if err != nil {
			return nil, fmt.Errorf("invalid trigger repo: %w", err)
		}
		req.TriggerRepo = id
	}

	switch req.Action {
	case ActionStop:
		return m.stop(req, out)
	case ActionStart:
		return m.start(ctx, req, out)
	case ActionRestart:
		return m.restart(ctx, req, out)
	}

	// resolve Project + Instance; unknown projects get an actionable error.
	proj, err := project.Load(m.paths, req.Project)
	if err != nil {
		return nil, err
	}

	unlock, busy, err := m.lockInstance(req.Project, req.Instance)
	if err != nil {
		return nil, err
	}
	if busy {
		res := &Result{Status: StatusBusy}
		m.writeTrailers(out, res)
		return res, nil
	}
	defer unlock()

	// rerun adopts the instance's last commit from state — read under the
	// instance lock so it cannot race another run of the same instance.
	if req.Action == ActionRerun {
		st, err := instance.LoadState(m.paths, req.Project, req.Instance)
		if err != nil || st.Commit == "" {
			return nil, fmt.Errorf("instance %s/%s has no previous run to rerun", req.Project, req.Instance)
		}
		req.Commit = st.Commit
		if req.Branch == "" {
			req.Branch = st.Branch
		}
		if req.TriggerRepo == "" {
			req.TriggerRepo = st.TriggerRepo
		}
	}

	switch req.Action {
	case ActionRun, ActionSync, ActionBuild, ActionTest, ActionRerun:
		return m.execute(ctx, proj, req, out)
	default:
		return nil, fmt.Errorf("unknown action %q (want run|sync|test|build|start|stop|restart|rerun)", req.Action)
	}
}

// execute runs the warmup → assemble → steps → background → health chain
// for the sync/build/test/run/rerun actions, persisting a run record and
// the instance state at the end.
func (m *Manager) execute(ctx context.Context, proj *project.Project, req Request, out io.Writer) (res *Result, err error) {
	res = &Result{RunID: newRunID(), Status: StatusSuccess}
	rec := &Record{ID: res.RunID, Branch: req.Branch, User: req.User, StartedAt: time.Now().UTC()}
	rec.Pipeline = append([]project.Step(nil), proj.Pipeline...)
	st := &instance.State{Project: req.Project, Instance: req.Instance, Branch: req.Branch, RunID: res.RunID, TriggerRepo: req.TriggerRepo}

	started := time.Now()
	m.log.Info("run started",
		"run_id", res.RunID, "project", req.Project, "instance", req.Instance,
		"action", req.Action, "branch", req.Branch, "commit", req.Commit, "user", req.User)

	runDir := filepath.Join(m.paths.Runs, req.Project, req.Instance, res.RunID)
	if err := os.MkdirAll(runDir, 0o755); err != nil {
		return nil, fmt.Errorf("create run dir: %w", err)
	}
	pruneRuns(filepath.Dir(runDir), maxRunDirs)
	logFile, err := os.Create(filepath.Join(runDir, "build.log"))
	if err != nil {
		return nil, fmt.Errorf("create build log: %w", err)
	}
	defer func() { _ = logFile.Close() }()
	log := io.MultiWriter(out, logFile)

	defer func() {
		if err != nil {
			res.Status = StatusFailed
			rec.Error = err.Error()
		}
		if res.Status == StatusFailed {
			st.Status = instance.StatusFailed
		}
		// Any path that did not replace the supervised process (sync,
		// build, test, a run without a background step, or a failure
		// before the background step) must keep tracking the previously
		// supervised process while it is still alive. Dropping it would
		// orphan the service: stop could not kill it, start would spawn a
		// second copy, and crash recovery would lose it.
		if st.PGID == 0 {
			if prev, lerr := instance.LoadState(m.paths, req.Project, req.Instance); lerr == nil &&
				prev.PGID > 0 && instance.Alive(prev.PGID) {
				st.PID, st.PGID = prev.PID, prev.PGID
				if st.Port == 0 {
					st.Port = prev.Port
				}
				st.URL = prev.URL
			}
		}
		rec.Commit = req.Commit
		rec.Status = res.Status
		rec.FinishedAt = time.Now().UTC()
		m.log.Info("run finished",
			"run_id", res.RunID, "project", req.Project, "instance", req.Instance,
			"duration_ms", time.Since(started).Milliseconds(), "status", res.Status)
		if werr := writeRecord(runDir, rec); werr != nil && err == nil {
			err = fmt.Errorf("write run record: %w", werr)
		}
		if serr := instance.SaveState(m.paths, st); serr != nil && err == nil {
			err = fmt.Errorf("save instance state: %w", serr)
		}
		m.writeTrailers(out, res)
	}()

	// EnsureWarmed every tree mount provider; a warmup failure stops the
	// run before any install, naming the provider and phase.
	providers, err := m.resolveProviders(proj)
	if err != nil {
		return res, err
	}
	caches := make(map[int]string, len(providers))
	for i := range proj.Tree {
		p, ok := providers[i]
		if !ok {
			continue
		}
		warm := m.exec.Warm(p, proj.Tree[i].Mount.Params)
		_, _ = fmt.Fprintf(log, "# ==> warmup: provider %s (%s)\n", p.ID, time.Now().Format(time.RFC3339))
		cacheDir, werr := m.exec.EnsureWarmed(ctx, p, proj.Tree[i].Mount.Params, log)
		if werr != nil {
			m.log.Warn("provider warmup failed",
				"provider", p.ID, "project", req.Project, "run_id", res.RunID, "error", werr)
			return res, werr
		}
		if warm {
			m.log.Info("provider warmup skipped", "provider", p.ID, "project", req.Project, "run_id", res.RunID)
		} else {
			m.log.Info("provider warmed", "provider", p.ID, "project", req.Project, "run_id", res.RunID)
		}
		caches[i] = cacheDir
	}

	// The trigger node receives the triggering commit: the git node mounting
	// TriggerRepo, or the primary git node when no repo triggered the run.
	triggerIdx, err := triggerNode(proj, req.TriggerRepo)
	if err != nil {
		return res, err
	}

	// The triggering commit defaults to the snapshot of the requested branch
	// (req.Branch) or, absent one, the trigger node's declared branch. The
	// snapshot ref must exist: push the branch through pushrun first.
	if req.Commit == "" && triggerIdx >= 0 {
		branch := req.Branch
		if branch == "" {
			branch = proj.Tree[triggerIdx].Mount.Params["branch"]
		}
		ref := "refs/pushrun/for/" + branch
		c, rerr := gitx.ResolveRef(caches[triggerIdx], ref)
		if rerr != nil {
			return res, fmt.Errorf("resolve %s: %w (push the branch through pushrun first)", ref, rerr)
		}
		req.Commit = c
	}
	st.Commit = req.Commit

	instDir, err := m.assemble(ctx, proj, req, providers, triggerIdx, log)
	if err != nil {
		return res, err
	}
	envExtra := gitNodeEnv(proj, instDir, triggerIdx)
	if req.Action == ActionSync {
		st.Status = instance.StatusSuccess
		return res, nil
	}

	// Steps run with a leased port from the pool.
	port, err := m.pool.Acquire(req.Project, req.Instance)
	if err != nil {
		return res, err
	}
	m.log.Info("port acquired", "project", req.Project, "instance", req.Instance, "port", port, "run_id", res.RunID)
	res.Port = port
	st.Port = port

	if err := m.runSteps(ctx, proj, req, instDir, port, rec, envExtra, log); err != nil {
		return res, err
	}
	if req.Action == ActionBuild || req.Action == ActionTest {
		st.Status = instance.StatusSuccess
		return res, nil
	}

	if err := m.startBackground(ctx, proj, req, instDir, port, rec, st, envExtra, log); err != nil {
		return res, err
	}
	if st.PGID > 0 {
		res.URL = st.URL
	} else {
		st.Status = instance.StatusSuccess
	}
	return res, nil
}

// resolveProviders loads the provider definition for every mounted node,
// keyed by tree index. Builtin providers (git, symlink) come first.
func (m *Manager) resolveProviders(proj *project.Project) (map[int]*provider.Provider, error) {
	out := make(map[int]*provider.Provider)
	for i, n := range proj.Tree {
		if n.Mount == nil {
			continue
		}
		p, err := m.loadProvider(n.Mount.Provider)
		if err != nil {
			return nil, err
		}
		out[i] = p
	}
	return out, nil
}

func (m *Manager) loadProvider(id string) (*provider.Provider, error) {
	for _, b := range provider.Builtins() {
		if b.ID == id {
			bp := b
			return &bp, nil
		}
	}
	return provider.Load(m.paths, id)
}

// lockInstance takes the per-instance non-blocking flock under
// <root>/locks/. The lock file name is length-prefixed
// ("<len>:<project>-<len>:<instance>") so name pairs like ("a-b","c") and
// ("a","b-c") can never collide. busy is true when another run holds it.
func (m *Manager) lockInstance(proj, inst string) (unlock func(), busy bool, err error) {
	if err := os.MkdirAll(m.paths.Locks, 0o755); err != nil {
		return nil, false, fmt.Errorf("create locks dir: %w", err)
	}
	name := filepath.Join(m.paths.Locks, fmt.Sprintf("%d:%s-%d:%s", len(proj), proj, len(inst), inst))
	f, err := os.OpenFile(name, os.O_CREATE|os.O_RDWR, 0o644)
	if err != nil {
		return nil, false, fmt.Errorf("open instance lock: %w", err)
	}
	if err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		_ = f.Close()
		if errors.Is(err, syscall.EWOULDBLOCK) {
			return nil, true, nil
		}
		return nil, false, fmt.Errorf("lock instance %s/%s: %w", proj, inst, err)
	}
	return func() {
		_ = syscall.Flock(int(f.Fd()), syscall.LOCK_UN)
		_ = f.Close()
	}, false, nil
}

// stop terminates the supervised process without re-running any steps.
func (m *Manager) stop(req Request, out io.Writer) (*Result, error) {
	if !fsutil.ValidName(req.Project) {
		return nil, fmt.Errorf("invalid project name %q", req.Project)
	}
	unlock, busy, err := m.lockInstance(req.Project, req.Instance)
	if err != nil {
		return nil, err
	}
	if busy {
		res := &Result{Status: StatusBusy}
		m.writeTrailers(out, res)
		return res, nil
	}
	defer unlock()

	res := &Result{Status: StatusSuccess}
	st, err := instance.LoadState(m.paths, req.Project, req.Instance)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			m.writeTrailers(out, res)
			return res, nil // never ran: nothing to stop
		}
		return nil, err
	}
	res.Port = st.Port
	if st.PGID > 0 && instance.Alive(st.PGID) {
		if err := instance.KillGroup(st.PGID); err != nil {
			res.Status = StatusFailed
			m.writeTrailers(out, res)
			return res, fmt.Errorf("stop instance %s/%s: %w", req.Project, req.Instance, err)
		}
		m.log.Info("instance stopped", "project", req.Project, "instance", req.Instance, "pgid", st.PGID)
	}
	st.Status = instance.StatusStopped
	st.PID, st.PGID = 0, 0
	st.URL = ""
	st.UpdatedAt = time.Now().UTC()
	if err := instance.SaveState(m.paths, st); err != nil {
		res.Status = StatusFailed
		m.writeTrailers(out, res)
		return res, err
	}
	m.writeTrailers(out, res)
	return res, nil
}

// start launches the project's background step against the already
// assembled instance directory, without re-running earlier steps.
func (m *Manager) start(ctx context.Context, req Request, out io.Writer) (*Result, error) {
	proj, err := project.Load(m.paths, req.Project)
	if err != nil {
		return nil, err
	}
	unlock, busy, err := m.lockInstance(req.Project, req.Instance)
	if err != nil {
		return nil, err
	}
	if busy {
		res := &Result{Status: StatusBusy}
		m.writeTrailers(out, res)
		return res, nil
	}
	defer unlock()
	return m.startLocked(ctx, proj, req, out)
}

func (m *Manager) startLocked(ctx context.Context, proj *project.Project, req Request, out io.Writer) (*Result, error) {
	res := &Result{Status: StatusSuccess}
	fail := func(err error) (*Result, error) {
		res.Status = StatusFailed
		m.writeTrailers(out, res)
		return res, err
	}
	st, err := instance.LoadState(m.paths, req.Project, req.Instance)
	if err != nil {
		return fail(fmt.Errorf("instance %s/%s has never run: %w", req.Project, req.Instance, err))
	}
	res.Port = st.Port
	if st.PGID > 0 && instance.Alive(st.PGID) {
		res.URL = st.URL
		m.writeTrailers(out, res)
		return res, nil // already running
	}
	bg := backgroundStep(proj)
	if bg == nil {
		return fail(fmt.Errorf("project %q has no background step to start", proj.Name))
	}
	if st.Port == 0 {
		port, err := m.pool.Acquire(req.Project, req.Instance)
		if err != nil {
			return fail(err)
		}
		m.log.Info("port acquired", "project", req.Project, "instance", req.Instance, "port", port)
		st.Port = port
		res.Port = port
	}
	instDir := filepath.Join(m.paths.Instances, req.Project, req.Instance)
	startReq := Request{
		Project: req.Project, Instance: req.Instance,
		Branch: st.Branch, Commit: st.Commit, Action: ActionRun, User: req.User,
		TriggerRepo: st.TriggerRepo,
		DisplayHost: req.DisplayHost,
	}
	// start/restart rebuild the trigger environment from the persisted
	// trigger repo, so a satellite-triggered service restarts with the same
	// CI_TRIGGER_* values its original run had. A project that no longer
	// mounts the recorded repo falls back to the primary node.
	triggerIdx, terr := triggerNode(proj, st.TriggerRepo)
	if terr != nil {
		triggerIdx, _ = triggerNode(proj, "")
	}
	envExtra := gitNodeEnv(proj, instDir, triggerIdx)
	if err := m.superviseBackground(ctx, bg, startReq, instDir, st.Port, st.RunID, st, envExtra, out); err != nil {
		// Persist the real state: the failure, no live process, and any
		// freshly leased port.
		st.Status = instance.StatusFailed
		st.UpdatedAt = time.Now().UTC()
		if serr := instance.SaveState(m.paths, st); serr != nil {
			return fail(errors.Join(err, serr))
		}
		return fail(err)
	}
	st.UpdatedAt = time.Now().UTC()
	if err := instance.SaveState(m.paths, st); err != nil {
		res.Status = StatusFailed
		m.writeTrailers(out, res)
		return res, err
	}
	res.URL = st.URL
	m.writeTrailers(out, res)
	return res, nil
}

// restart stops the supervised process and starts it again.
func (m *Manager) restart(ctx context.Context, req Request, out io.Writer) (*Result, error) {
	proj, err := project.Load(m.paths, req.Project)
	if err != nil {
		return nil, err
	}
	unlock, busy, err := m.lockInstance(req.Project, req.Instance)
	if err != nil {
		return nil, err
	}
	if busy {
		res := &Result{Status: StatusBusy}
		m.writeTrailers(out, res)
		return res, nil
	}
	defer unlock()

	st, err := instance.LoadState(m.paths, req.Project, req.Instance)
	if err == nil && st.PGID > 0 && instance.Alive(st.PGID) {
		if err := instance.KillGroup(st.PGID); err != nil {
			res := &Result{Status: StatusFailed, Port: st.Port}
			m.writeTrailers(out, res)
			return res, fmt.Errorf("restart instance %s/%s: %w", req.Project, req.Instance, err)
		}
		st.PID, st.PGID = 0, 0
		st.URL = ""
		if err := instance.SaveState(m.paths, st); err != nil {
			return nil, err
		}
	}
	return m.startLocked(ctx, proj, req, out)
}

// writeTrailers emits the trailing CI_STATUS=/CI_PORT=/CI_URL= lines. The
// block always starts with a newline so CI_STATUS= never glues onto
// unterminated build output.
func (m *Manager) writeTrailers(out io.Writer, res *Result) {
	_, _ = fmt.Fprintf(out, "\nCI_STATUS=%s\n", res.Status)
	if res.Port > 0 {
		_, _ = fmt.Fprintf(out, "CI_PORT=%d\n", res.Port)
	}
	if res.URL != "" {
		_, _ = fmt.Fprintf(out, "CI_URL=%s\n", res.URL)
	}
}

// newRunID returns a sortable, collision-resistant run id.
func newRunID() string {
	var buf [4]byte
	if _, err := rand.Read(buf[:]); err != nil {
		panic(fmt.Sprintf("generate run id: %v", err))
	}
	return time.Now().UTC().Format("20060102-150405") + "-" + hex.EncodeToString(buf[:])
}

// runsDir returns <root>/runs/<project>/<instance> after validating names.
func runsDir(paths config.Paths, proj, inst string) (string, error) {
	if !fsutil.ValidName(proj) || !fsutil.ValidName(inst) {
		return "", fmt.Errorf("run: invalid project/instance name %q/%q", proj, inst)
	}
	return filepath.Join(paths.Runs, proj, inst), nil
}

// runDir returns the directory of one run, validating all path components.
func runDir(paths config.Paths, proj, inst, runID string) (string, error) {
	dir, err := runsDir(paths, proj, inst)
	if err != nil {
		return "", err
	}
	if !fsutil.ValidName(runID) {
		return "", fmt.Errorf("run: invalid run id %q", runID)
	}
	return filepath.Join(dir, runID), nil
}

// writeRecord persists rec as result.json inside dir.
func writeRecord(dir string, rec *Record) error {
	data, err := json.MarshalIndent(rec, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal run record: %w", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "result.json"), data, 0o644); err != nil {
		return fmt.Errorf("write result.json: %w", err)
	}
	return nil
}

// ListRuns returns the run records of an instance, newest first. Run dirs
// without a result.json are skipped.
func ListRuns(paths config.Paths, proj, inst string) ([]*Record, error) {
	dir, err := runsDir(paths, proj, inst)
	if err != nil {
		return nil, err
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return nil, nil
		}
		return nil, fmt.Errorf("list runs %s/%s: %w", proj, inst, err)
	}
	var recs []*Record
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		data, err := os.ReadFile(filepath.Join(dir, e.Name(), "result.json"))
		if err != nil {
			continue
		}
		var rec Record
		if err := json.Unmarshal(data, &rec); err != nil {
			continue
		}
		recs = append(recs, &rec)
	}
	sort.Slice(recs, func(i, j int) bool {
		if recs[i].StartedAt.Equal(recs[j].StartedAt) {
			return recs[i].ID > recs[j].ID
		}
		return recs[i].StartedAt.After(recs[j].StartedAt)
	})
	return recs, nil
}

// GetRun loads the record of one run.
func GetRun(paths config.Paths, proj, inst, runID string) (*Record, error) {
	dir, err := runDir(paths, proj, inst, runID)
	if err != nil {
		return nil, err
	}
	data, err := os.ReadFile(filepath.Join(dir, "result.json"))
	if err != nil {
		return nil, fmt.Errorf("run %s/%s/%s: %w", proj, inst, runID, err)
	}
	var rec Record
	if err := json.Unmarshal(data, &rec); err != nil {
		return nil, fmt.Errorf("parse run record %s/%s/%s: %w", proj, inst, runID, err)
	}
	return &rec, nil
}

// OpenBuildLog opens the build log of one run for reading.
func OpenBuildLog(paths config.Paths, proj, inst, runID string) (io.ReadCloser, error) {
	dir, err := runDir(paths, proj, inst, runID)
	if err != nil {
		return nil, err
	}
	f, err := os.Open(filepath.Join(dir, "build.log"))
	if err != nil {
		return nil, fmt.Errorf("open build log %s/%s/%s: %w", proj, inst, runID, err)
	}
	return f, nil
}

// pruneRuns removes the oldest run directories under dir (by modification
// time) so that at most keep remain.
func pruneRuns(dir string, keep int) {
	entries, err := os.ReadDir(dir)
	if err != nil || len(entries) <= keep {
		return
	}
	type entry struct {
		name string
		mod  time.Time
	}
	list := make([]entry, 0, len(entries))
	for _, e := range entries {
		info, err := e.Info()
		if err != nil {
			continue
		}
		list = append(list, entry{e.Name(), info.ModTime()})
	}
	sort.Slice(list, func(i, j int) bool { return list[i].mod.Before(list[j].mod) })
	for _, e := range list[:len(list)-keep] {
		_ = os.RemoveAll(filepath.Join(dir, e.name))
	}
}
