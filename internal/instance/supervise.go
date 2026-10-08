package instance

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"sync"
	"syscall"
	"time"

	"github.com/bunnyzr/pushrun/internal/config"
)

// supervised tracks the reaper goroutine of each supervised process by group
// id. KillGroup uses it to distinguish a dead group from a group id that the
// OS reused for an unrelated process: once the leader has been reaped, no
// process we started can still be running.
var supervised sync.Map // pgid int -> chan struct{} (closed when reaped)

// Supervise starts cmd in its own process group (Setpgid) so the whole
// group can later be signalled via KillGroup. The returned pgid equals
// pid. A background goroutine reaps the process on exit; callers must not
// call cmd.Wait themselves.
func Supervise(cmd *exec.Cmd) (pid, pgid int, err error) {
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	if err := cmd.Start(); err != nil {
		return 0, 0, fmt.Errorf("start supervised process: %w", err)
	}
	// Reap on exit so the dead leader does not linger as a zombie (a
	// zombie would keep Alive reporting true).
	pgid = cmd.Process.Pid
	done := make(chan struct{})
	go func() { _ = cmd.Wait(); close(done) }()
	supervised.Store(pgid, done)
	// Bound the registry: a handle only matters while the process lives or
	// shortly after; after a minute a stale entry buys nothing.
	time.AfterFunc(time.Minute, func() { supervised.Delete(pgid) })
	return cmd.Process.Pid, pgid, nil
}

// Alive reports whether any process in group pgid still exists.
func Alive(pgid int) bool {
	if pgid <= 0 {
		return false
	}
	err := syscall.Kill(-pgid, 0)
	return err == nil || errors.Is(err, syscall.EPERM)
}

// KillGroup sends SIGKILL to every process in group pgid and waits until
// the group is gone. A group that is already dead is not an error. When the
// group id survives only because the OS reused it for an unrelated process
// (the leader was reaped, so nothing we started is left), the kill reports
// success rather than waiting out the deadline.
func KillGroup(pgid int) error {
	if pgid <= 0 {
		return fmt.Errorf("invalid pgid %d", pgid)
	}
	if err := syscall.Kill(-pgid, syscall.SIGKILL); err != nil && !errors.Is(err, syscall.ESRCH) {
		return fmt.Errorf("kill process group %d: %w", pgid, err)
	}
	deadline := time.Now().Add(5 * time.Second)
	for Alive(pgid) {
		if ch, ok := supervised.Load(pgid); ok {
			select {
			case <-ch.(chan struct{}):
				// The leader has been reaped: every process we started in
				// this group is gone. A group id that still answers belongs
				// to a reused pid, not to our service.
				return nil
			default:
			}
		}
		if time.Now().After(deadline) {
			return fmt.Errorf("process group %d still alive after SIGKILL", pgid)
		}
		time.Sleep(10 * time.Millisecond)
	}
	return nil
}

// Recover runs at daemon boot. It scans every
// <root>/state/<project>/<instance>/current.json: a recorded supervised
// process that is still alive is re-adopted (left running, state
// untouched); one that is dead is reaped by marking the instance stopped
// and clearing its pid/pgid (which drops current.json, keeping the marked
// record in latest.json). A nil log discards daemon-log records.
func Recover(paths config.Paths, log *slog.Logger) error {
	if log == nil {
		log = slog.New(slog.NewTextHandler(io.Discard, nil))
	}
	states, err := currentStates(paths)
	if err != nil {
		return err
	}
	var errs []error
	for _, st := range states {
		if st.PGID <= 0 || Alive(st.PGID) {
			continue // nothing recorded, or still running: re-adopt
		}
		log.Warn("crash recovery: reaping dead supervised process",
			"project", st.Project, "instance", st.Instance, "pid", st.PID, "pgid", st.PGID)
		st.Status = StatusStopped
		st.PID = 0
		st.PGID = 0
		st.UpdatedAt = time.Now().UTC()
		if err := SaveState(paths, st); err != nil {
			errs = append(errs, err)
		}
	}
	return errors.Join(errs...)
}

// currentStates loads every current.json under paths.State. Files that
// fail to parse are skipped.
func currentStates(paths config.Paths) ([]*State, error) {
	var out []*State
	err := filepath.WalkDir(paths.State, func(path string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() || d.Name() != "current.json" {
			return nil
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return nil
		}
		var st State
		if err := json.Unmarshal(data, &st); err != nil {
			return nil
		}
		if st.Project == "" || st.Instance == "" {
			return nil
		}
		out = append(out, &st)
		return nil
	})
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return nil, nil
		}
		return nil, fmt.Errorf("scan instance states: %w", err)
	}
	return out, nil
}
