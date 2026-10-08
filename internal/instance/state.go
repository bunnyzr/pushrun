// Package instance manages per-instance runtime state, the port lease
// pool, and supervision of background pipeline processes.
package instance

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/bunnyzr/pushrun/internal/config"
	"github.com/bunnyzr/pushrun/internal/fsutil"
)

// Status values recorded in State.Status.
const (
	StatusRunning = "RUNNING"
	StatusStopped = "STOPPED"
	StatusSuccess = "SUCCESS"
	StatusFailed  = "FAILED"
)

// State is the persisted runtime state of one instance.
type State struct {
	Project  string `json:"project"`
	Instance string `json:"instance"`
	Status   string `json:"status"`
	Branch   string `json:"branch,omitempty"`
	Commit   string `json:"commit,omitempty"`
	RunID    string `json:"run_id,omitempty"`
	Port     int    `json:"port,omitempty"`
	PID      int    `json:"pid,omitempty"`
	PGID     int    `json:"pgid,omitempty"`
	URL      string `json:"url,omitempty"`
	// TriggerRepo is the repo identity whose push triggered the last run
	// (empty for API-triggered runs). start/restart use it to rebuild the
	// CI_TRIGGER_* environment of the original run.
	TriggerRepo string    `json:"trigger_repo,omitempty"`
	UpdatedAt   time.Time `json:"updated_at"`
}

func stateDir(paths config.Paths, project, inst string) (string, error) {
	if !fsutil.ValidName(project) || !fsutil.ValidName(inst) {
		return "", fmt.Errorf("instance: invalid project/instance name %q/%q", project, inst)
	}
	return filepath.Join(paths.State, project, inst), nil
}

// LoadState reads the most recently saved state from
// <root>/state/<project>/<instance>/latest.json.
func LoadState(paths config.Paths, project, inst string) (*State, error) {
	dir, err := stateDir(paths, project, inst)
	if err != nil {
		return nil, err
	}
	data, err := os.ReadFile(filepath.Join(dir, "latest.json"))
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return nil, fmt.Errorf("instance %s/%s: %w", project, inst, err)
		}
		return nil, fmt.Errorf("read instance state %s/%s: %w", project, inst, err)
	}
	var st State
	if err := json.Unmarshal(data, &st); err != nil {
		return nil, fmt.Errorf("parse instance state %s/%s: %w", project, inst, err)
	}
	return &st, nil
}

// SaveState atomically writes st to latest.json. When st carries a
// supervised process (PGID > 0) it also writes current.json; otherwise
// current.json is removed so crash recovery never resurrects a stale
// process record. UpdatedAt is set to now when zero.
func SaveState(paths config.Paths, st *State) error {
	if st == nil {
		return errors.New("instance: nil state")
	}
	dir, err := stateDir(paths, st.Project, st.Instance)
	if err != nil {
		return err
	}
	if st.UpdatedAt.IsZero() {
		st.UpdatedAt = time.Now().UTC()
	}
	data, err := json.MarshalIndent(st, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal instance state %s/%s: %w", st.Project, st.Instance, err)
	}
	if err := fsutil.WriteFileAtomic(filepath.Join(dir, "latest.json"), data, 0o644); err != nil {
		return fmt.Errorf("write instance state %s/%s: %w", st.Project, st.Instance, err)
	}
	current := filepath.Join(dir, "current.json")
	if st.PGID > 0 {
		if err := fsutil.WriteFileAtomic(current, data, 0o644); err != nil {
			return fmt.Errorf("write current state %s/%s: %w", st.Project, st.Instance, err)
		}
	} else if err := os.Remove(current); err != nil && !errors.Is(err, fs.ErrNotExist) {
		return fmt.Errorf("remove current state %s/%s: %w", st.Project, st.Instance, err)
	}
	return nil
}

// SafeJoin joins rel onto root, rejecting absolute paths and any rel that
// escapes root via "..". The returned path is cleaned. A root with a
// trailing slash is accepted.
func SafeJoin(root, rel string) (string, error) {
	root = filepath.Clean(root)
	if rel == "" || rel == "." {
		return "", fmt.Errorf("unsafe path %q: empty", rel)
	}
	if filepath.IsAbs(rel) {
		return "", fmt.Errorf("unsafe path %q: absolute paths not allowed", rel)
	}
	clean := filepath.Clean(rel)
	if clean == ".." || strings.HasPrefix(clean, ".."+string(filepath.Separator)) {
		return "", fmt.Errorf("unsafe path %q: escapes root", rel)
	}
	joined := filepath.Join(root, clean)
	if joined != root && !strings.HasPrefix(joined, root+string(filepath.Separator)) {
		return "", fmt.Errorf("unsafe path %q: escapes root", rel)
	}
	return joined, nil
}
