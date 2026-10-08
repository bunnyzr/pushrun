package instance_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"sync"
	"syscall"
	"testing"
	"time"

	"github.com/bunnyzr/pushrun/internal/config"
	"github.com/bunnyzr/pushrun/internal/instance"
)

// recoverLogHandler captures slog records for assertions.
type recoverLogHandler struct {
	mu    sync.Mutex
	recs  []slog.Record
	warns int
}

func (h *recoverLogHandler) Enabled(context.Context, slog.Level) bool { return true }
func (h *recoverLogHandler) Handle(_ context.Context, r slog.Record) error {
	h.mu.Lock()
	h.recs = append(h.recs, r.Clone())
	if r.Level >= slog.LevelWarn {
		h.warns++
	}
	h.mu.Unlock()
	return nil
}
func (h *recoverLogHandler) WithAttrs([]slog.Attr) slog.Handler { return h }
func (h *recoverLogHandler) WithGroup(string) slog.Handler      { return h }

func testPaths(t *testing.T) config.Paths {
	t.Helper()
	return config.NewPaths(t.TempDir())
}

func TestStateRoundTrip(t *testing.T) {
	paths := testPaths(t)
	st := &instance.State{
		Project:   "demo",
		Instance:  "main",
		Status:    instance.StatusRunning,
		Branch:    "main",
		Commit:    "abc123",
		RunID:     "run-1",
		Port:      20001,
		PID:       12345,
		PGID:      12345,
		URL:       "http://localhost:20001",
		UpdatedAt: time.Now().UTC().Truncate(time.Second),
	}
	if err := instance.SaveState(paths, st); err != nil {
		t.Fatalf("SaveState: %v", err)
	}
	got, err := instance.LoadState(paths, "demo", "main")
	if err != nil {
		t.Fatalf("LoadState: %v", err)
	}
	if *got != *st {
		t.Fatalf("round trip mismatch:\n got %+v\nwant %+v", got, st)
	}
	// Both latest.json and current.json must exist for a supervised state.
	for _, name := range []string{"latest.json", "current.json"} {
		fp := filepath.Join(paths.State, "demo", "main", name)
		if _, err := os.Stat(fp); err != nil {
			t.Fatalf("expected %s to exist: %v", fp, err)
		}
	}
}

func TestLoadStateNotFound(t *testing.T) {
	paths := testPaths(t)
	_, err := instance.LoadState(paths, "nope", "nope")
	if err == nil {
		t.Fatal("expected error for missing state")
	}
	if !errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("expected fs.ErrNotExist, got %v", err)
	}
}

func TestSaveStateWithoutProcessClearsCurrent(t *testing.T) {
	paths := testPaths(t)
	live := &instance.State{
		Project: "demo", Instance: "main", Status: instance.StatusRunning,
		PID: 12345, PGID: 12345,
	}
	if err := instance.SaveState(paths, live); err != nil {
		t.Fatalf("SaveState live: %v", err)
	}
	current := filepath.Join(paths.State, "demo", "main", "current.json")
	if _, err := os.Stat(current); err != nil {
		t.Fatalf("current.json should exist for supervised state: %v", err)
	}

	// A state without a supervised process (e.g. failed run) updates
	// latest.json but must not leave a stale current.json behind.
	failed := &instance.State{
		Project: "demo", Instance: "main", Status: instance.StatusFailed,
		RunID: "run-2",
	}
	if err := instance.SaveState(paths, failed); err != nil {
		t.Fatalf("SaveState failed: %v", err)
	}
	if _, err := os.Stat(current); !errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("current.json should be removed when PGID==0, stat err=%v", err)
	}
	got, err := instance.LoadState(paths, "demo", "main")
	if err != nil {
		t.Fatalf("LoadState latest: %v", err)
	}
	if got.Status != instance.StatusFailed || got.RunID != "run-2" {
		t.Fatalf("latest.json not updated: %+v", got)
	}
}

func TestPortPoolStickyReacquire(t *testing.T) {
	paths := testPaths(t)
	pool := instance.NewPortPool(paths, 20000, 20010)
	p1, err := pool.Acquire("demo", "main")
	if err != nil {
		t.Fatalf("Acquire: %v", err)
	}
	if p1 < 20000 || p1 > 20010 {
		t.Fatalf("port %d outside pool range", p1)
	}
	// Re-acquire from a fresh pool over the same root (simulates daemon
	// restart): the persisted lease makes the port sticky.
	pool2 := instance.NewPortPool(paths, 20000, 20010)
	p2, err := pool2.Acquire("demo", "main")
	if err != nil {
		t.Fatalf("re-Acquire: %v", err)
	}
	if p2 != p1 {
		t.Fatalf("sticky re-acquire got %d, want %d", p2, p1)
	}
	// A different instance gets a different port.
	p3, err := pool2.Acquire("demo", "dev")
	if err != nil {
		t.Fatalf("Acquire dev: %v", err)
	}
	if p3 == p1 {
		t.Fatalf("different instance reused port %d", p3)
	}
}

func TestPortPoolExhaustion(t *testing.T) {
	paths := testPaths(t)
	pool := instance.NewPortPool(paths, 20000, 20001)
	if _, err := pool.Acquire("demo", "a"); err != nil {
		t.Fatalf("Acquire a: %v", err)
	}
	if _, err := pool.Acquire("demo", "b"); err != nil {
		t.Fatalf("Acquire b: %v", err)
	}
	_, err := pool.Acquire("demo", "c")
	if err == nil {
		t.Fatal("expected exhaustion error")
	}
}

func TestPortPoolRelease(t *testing.T) {
	paths := testPaths(t)
	pool := instance.NewPortPool(paths, 20000, 20001)
	p1, err := pool.Acquire("demo", "a")
	if err != nil {
		t.Fatalf("Acquire a: %v", err)
	}
	if _, err := pool.Acquire("demo", "b"); err != nil {
		t.Fatalf("Acquire b: %v", err)
	}
	if _, err := pool.Acquire("demo", "c"); err == nil {
		t.Fatal("expected exhaustion error before release")
	}
	pool.Release(p1)
	// The released port is free again; a new instance can take it.
	p2, err := pool.Acquire("demo", "c")
	if err != nil {
		t.Fatalf("Acquire after Release: %v", err)
	}
	if p2 != p1 {
		t.Fatalf("expected released port %d to be reused first, got %d", p1, p2)
	}
	// Releasing an unknown port is a no-op.
	pool.Release(29999)
}

func TestSuperviseLifecycle(t *testing.T) {
	cmd := exec.Command("sleep", "30")
	pid, pgid, err := instance.Supervise(cmd)
	if err != nil {
		t.Fatalf("Supervise: %v", err)
	}
	if pid <= 0 {
		t.Fatalf("invalid pid %d", pid)
	}
	if pgid != pid {
		t.Fatalf("pgid %d != pid %d; process not its own group leader", pgid, pid)
	}
	if !instance.Alive(pgid) {
		t.Fatal("Alive(pgid) = false for running process")
	}
	if err := instance.KillGroup(pgid); err != nil {
		t.Fatalf("KillGroup: %v", err)
	}
	if instance.Alive(pgid) {
		t.Fatal("Alive(pgid) = true after KillGroup")
	}
	// Killing an already-dead group is not an error.
	if err := instance.KillGroup(pgid); err != nil {
		t.Fatalf("KillGroup on dead group: %v", err)
	}
}

func TestSuperviseKillGroupIncludesChildren(t *testing.T) {
	childPIDFile := filepath.Join(t.TempDir(), "child.pid")
	cmd := exec.Command("bash", "-c", "sleep 30 & echo $! > \"$1\"; wait", "_", childPIDFile)
	_, pgid, err := instance.Supervise(cmd)
	if err != nil {
		t.Fatalf("Supervise: %v", err)
	}
	var childPID int
	for range 50 {
		data, err := os.ReadFile(childPIDFile)
		if err == nil {
			if _, err := fmt.Sscan(string(data), &childPID); err == nil && childPID > 0 {
				break
			}
		}
		time.Sleep(20 * time.Millisecond)
	}
	if childPID <= 0 {
		t.Fatal("child pid file never appeared")
	}
	if err := instance.KillGroup(pgid); err != nil {
		t.Fatalf("KillGroup: %v", err)
	}
	// The backgrounded child shared the process group, so it died too.
	if err := syscall.Kill(childPID, 0); err == nil {
		t.Fatalf("child process %d survived group kill", childPID)
	}
}

// Killing the group of a process that already exited on its own returns
// promptly: the reaper knows the leader is gone, so a group id that still
// answers (an unrelated process the OS reused the pid for) must not stall
// the kill or fail it.
func TestKillGroupAfterLeaderExited(t *testing.T) {
	cmd := exec.Command("true")
	_, pgid, err := instance.Supervise(cmd)
	if err != nil {
		t.Fatalf("Supervise: %v", err)
	}
	for range 100 {
		if !instance.Alive(pgid) {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	done := make(chan error, 1)
	go func() { done <- instance.KillGroup(pgid) }()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("KillGroup after leader exit: %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("KillGroup blocked after the leader had already exited")
	}
}

func TestSafeJoin(t *testing.T) {
	root := t.TempDir()
	ok := []string{"app", "a/b", "a/./b", "deeply/nested/dir"}
	for _, rel := range ok {
		got, err := instance.SafeJoin(root, rel)
		if err != nil {
			t.Fatalf("SafeJoin(%q): unexpected error %v", rel, err)
		}
		want := filepath.Join(root, filepath.Clean(rel))
		if got != want {
			t.Fatalf("SafeJoin(%q) = %q, want %q", rel, got, want)
		}
	}
	bad := []string{"/etc", "/etc/passwd", "../x", "a/../../x", "..", "a/../../../etc", ""}
	for _, rel := range bad {
		if got, err := instance.SafeJoin(root, rel); err == nil {
			t.Fatalf("SafeJoin(%q) = %q, want error", rel, got)
		}
	}
}

func TestSafeJoinTrailingSlashRoot(t *testing.T) {
	root := t.TempDir()
	withSlash := root + string(filepath.Separator)
	got, err := instance.SafeJoin(withSlash, "app")
	if err != nil {
		t.Fatalf("SafeJoin with trailing-slash root: unexpected error %v", err)
	}
	want := filepath.Join(root, "app")
	if got != want {
		t.Fatalf("SafeJoin(%q, %q) = %q, want %q", withSlash, "app", got, want)
	}
}

func TestPortPoolReclaimsCorruptLease(t *testing.T) {
	paths := testPaths(t)
	if err := os.MkdirAll(paths.Ports, 0o755); err != nil {
		t.Fatal(err)
	}
	// Simulate a crash mid-write: a truncated lease file occupies port 20000.
	leaseFile := filepath.Join(paths.Ports, "20000")
	if err := os.WriteFile(leaseFile, []byte(`{"project":`), 0o644); err != nil {
		t.Fatal(err)
	}
	pool := instance.NewPortPool(paths, 20000, 20001)
	port, err := pool.Acquire("demo", "a")
	if err != nil {
		t.Fatalf("Acquire: %v", err)
	}
	if port != 20000 {
		t.Fatalf("corrupt lease not reclaimed: got port %d, want 20000", port)
	}
	data, err := os.ReadFile(leaseFile)
	if err != nil {
		t.Fatal(err)
	}
	var l map[string]any
	if err := json.Unmarshal(data, &l); err != nil {
		t.Fatalf("lease file is not valid JSON after reclaim: %v", err)
	}
	if l["project"] != "demo" || l["instance"] != "a" || l["port"] != float64(20000) {
		t.Fatalf("lease content = %s", data)
	}
}

func writeCurrentState(t *testing.T, paths config.Paths, st *instance.State) {
	t.Helper()
	dir := filepath.Join(paths.State, st.Project, st.Instance)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	data, err := json.Marshal(st)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "current.json"), data, 0o644); err != nil {
		t.Fatalf("write current.json: %v", err)
	}
}

func TestRecoverReapsDeadAndKeepsLive(t *testing.T) {
	paths := testPaths(t)

	// A supervised process that is now dead (killed before "restart").
	deadCmd := exec.Command("sleep", "30")
	deadPID, deadPGID, err := instance.Supervise(deadCmd)
	if err != nil {
		t.Fatalf("Supervise dead: %v", err)
	}
	if err := instance.KillGroup(deadPGID); err != nil {
		t.Fatalf("KillGroup dead: %v", err)
	}
	writeCurrentState(t, paths, &instance.State{
		Project: "gone", Instance: "main", Status: instance.StatusRunning,
		PID: deadPID, PGID: deadPGID, Port: 20000,
	})

	// A supervised process still alive across the "restart".
	liveCmd := exec.Command("sleep", "30")
	livePID, livePGID, err := instance.Supervise(liveCmd)
	if err != nil {
		t.Fatalf("Supervise live: %v", err)
	}
	t.Cleanup(func() { _ = instance.KillGroup(livePGID) })
	writeCurrentState(t, paths, &instance.State{
		Project: "alive", Instance: "main", Status: instance.StatusRunning,
		PID: livePID, PGID: livePGID, Port: 20001,
	})

	// Recover must log the reap at warn level.
	h := &recoverLogHandler{}
	if err := instance.Recover(paths, slog.New(h)); err != nil {
		t.Fatalf("Recover: %v", err)
	}
	if h.warns == 0 {
		t.Fatal("Recover logged no warn record for the reaped process")
	}
	var found bool
	for _, r := range h.recs {
		var proj, inst string
		r.Attrs(func(a slog.Attr) bool {
			switch a.Key {
			case "project":
				proj = a.Value.String()
			case "instance":
				inst = a.Value.String()
			}
			return true
		})
		if r.Level == slog.LevelWarn && proj == "gone" && inst == "main" {
			found = true
		}
	}
	if !found {
		t.Fatal("no warn record naming the reaped instance gone/main")
	}

	// Dead: current.json removed, latest.json marked stopped.
	if _, err := os.Stat(filepath.Join(paths.State, "gone", "main", "current.json")); !errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("dead instance current.json should be removed, stat err=%v", err)
	}
	got, err := instance.LoadState(paths, "gone", "main")
	if err != nil {
		t.Fatalf("LoadState dead: %v", err)
	}
	if got.Status != instance.StatusStopped {
		t.Fatalf("dead instance status = %q, want %q", got.Status, instance.StatusStopped)
	}
	if got.PID != 0 || got.PGID != 0 {
		t.Fatalf("dead instance still carries pid/pgid: %+v", got)
	}

	// Live: re-adopted, current.json untouched.
	data, err := os.ReadFile(filepath.Join(paths.State, "alive", "main", "current.json"))
	if err != nil {
		t.Fatalf("read live current.json: %v", err)
	}
	var live instance.State
	if err := json.Unmarshal(data, &live); err != nil {
		t.Fatalf("parse live current.json: %v", err)
	}
	if live.PGID != livePGID || live.Status != instance.StatusRunning {
		t.Fatalf("live instance state changed: %+v", live)
	}
}
