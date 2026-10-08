package server

import (
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"net/http"
	"os"
	"path/filepath"
	"sort"

	"github.com/bunnyzr/pushrun/internal/fsutil"
	"github.com/bunnyzr/pushrun/internal/instance"
	"github.com/bunnyzr/pushrun/internal/run"
)

func (s *Server) listInstances(w http.ResponseWriter, r *http.Request) {
	states, err := s.allStates()
	if err != nil {
		s.fail(w, r, http.StatusInternalServerError, "internal", err)
		return
	}
	if states == nil {
		states = []*instance.State{}
	}
	writeJSON(w, http.StatusOK, map[string]any{"instances": states})
}

// allStates loads every <root>/state/<project>/<instance>/latest.json.
func (s *Server) allStates() ([]*instance.State, error) {
	projs, err := os.ReadDir(s.paths.State)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return nil, nil
		}
		return nil, fmt.Errorf("list instances: %w", err)
	}
	var out []*instance.State
	for _, pd := range projs {
		if !pd.IsDir() {
			continue
		}
		insts, err := os.ReadDir(filepath.Join(s.paths.State, pd.Name()))
		if err != nil {
			continue
		}
		for _, id := range insts {
			if !id.IsDir() {
				continue
			}
			st, err := instance.LoadState(s.paths, pd.Name(), id.Name())
			if err != nil {
				continue
			}
			out = append(out, st)
		}
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Project != out[j].Project {
			return out[i].Project < out[j].Project
		}
		return out[i].Instance < out[j].Instance
	})
	return out, nil
}

func (s *Server) getInstance(w http.ResponseWriter, r *http.Request) {
	proj, inst := r.PathValue("project"), r.PathValue("instance")
	st, err := instance.LoadState(s.paths, proj, inst)
	if err != nil {
		s.fail(w, r, http.StatusNotFound, "not_found", err)
		return
	}
	writeJSON(w, http.StatusOK, st)
}

// instanceAction triggers the run engine for an instance. The id is the
// <project>/<instance> pair of path segments.
func (s *Server) instanceAction(w http.ResponseWriter, r *http.Request) {
	proj, inst := r.PathValue("project"), r.PathValue("instance")
	action := r.PathValue("action")
	switch action {
	case run.ActionRun, run.ActionStop, run.ActionRestart, run.ActionRerun, run.ActionStart:
	default:
		s.fail(w, r, http.StatusBadRequest, "invalid",
			fmt.Errorf("unknown action %q (want run|start|stop|restart|rerun)", action))
		return
	}
	var body struct {
		Branch string `json:"branch"`
		Commit string `json:"commit"`
		User   string `json:"user"`
	}
	if r.Body != nil && r.ContentLength != 0 {
		if err := decodeJSONBody(r, &body); err != nil {
			s.fail(w, r, http.StatusBadRequest, "invalid", err)
			return
		}
	}
	log := s.requestLogger(r).With("project", proj, "instance", inst)
	log.Info("instance action", "action", action, "branch", body.Branch, "commit", body.Commit)
	// Engine actions run detached from the request context: with the
	// synchronous design a run legitimately takes minutes, and a client
	// disconnect or proxy timeout must not cancel a deploy mid-pipeline.
	// Only server shutdown cancels it.
	res, err := s.deps.Manager.Run(context.WithoutCancel(r.Context()), run.Request{
		Project:  proj,
		Instance: inst,
		Branch:   body.Branch,
		Commit:   body.Commit,
		Action:   action,
		User:     body.User,
	}, io.Discard)
	if err != nil {
		if res != nil && res.Status == run.StatusFailed {
			s.fail(w, r, http.StatusInternalServerError, "run_failed",
				fmt.Errorf("%s %s/%s failed: %w", action, proj, inst, err))
			return
		}
		s.fail(w, r, http.StatusInternalServerError, "internal", err)
		return
	}
	if res.Status == run.StatusBusy {
		s.fail(w, r, http.StatusConflict, "busy",
			fmt.Errorf("instance %s/%s has another run in progress", proj, inst))
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"run_id": res.RunID,
		"status": res.Status,
		"port":   res.Port,
		"url":    res.URL,
	})
}

// deleteInstance stops the supervised process, releases the port lease, and
// removes the instance's directory, state, and run history. A run in flight
// (holding the instance lock) blocks the delete with 409. The project
// definition is never touched.
func (s *Server) deleteInstance(w http.ResponseWriter, r *http.Request) {
	proj, inst := r.PathValue("project"), r.PathValue("instance")
	if !fsutil.ValidName(proj) || !fsutil.ValidName(inst) {
		s.fail(w, r, http.StatusBadRequest, "invalid",
			fmt.Errorf("invalid instance id %q/%q", proj, inst))
		return
	}
	st, stErr := instance.LoadState(s.paths, proj, inst)
	_, dirErr := os.Stat(filepath.Join(s.paths.Instances, proj, inst))
	if stErr != nil && dirErr != nil {
		s.fail(w, r, http.StatusNotFound, "not_found",
			fmt.Errorf("instance %s/%s not found", proj, inst))
		return
	}
	log := s.requestLogger(r).With("project", proj, "instance", inst)
	// A stop doubles as a lock probe: a run in flight holds the instance
	// lock, and deleting underneath it would corrupt the run. BUSY → 409.
	// Stop runs detached from the request context (see instanceAction).
	res, err := s.deps.Manager.Run(context.WithoutCancel(r.Context()), run.Request{
		Project: proj, Instance: inst, Action: run.ActionStop,
	}, io.Discard)
	if err != nil {
		s.fail(w, r, http.StatusInternalServerError, "internal",
			fmt.Errorf("stop instance %s/%s: %w", proj, inst, err))
		return
	}
	if res != nil && res.Status == run.StatusBusy {
		s.fail(w, r, http.StatusConflict, "busy",
			fmt.Errorf("instance %s/%s has a run in progress; wait for it or stop it first", proj, inst))
		return
	}
	if stErr == nil && st.Port > 0 {
		s.deps.Pool.Release(st.Port)
		log.Info("port released", "port", st.Port)
	}
	for _, dir := range []string{
		filepath.Join(s.paths.Instances, proj, inst),
		filepath.Join(s.paths.State, proj, inst),
		filepath.Join(s.paths.Runs, proj, inst),
	} {
		if err := os.RemoveAll(dir); err != nil {
			s.fail(w, r, http.StatusInternalServerError, "internal", err)
			return
		}
	}
	log.Info("instance deleted")
	writeJSON(w, http.StatusOK, map[string]any{"status": "deleted"})
}

func (s *Server) listRuns(w http.ResponseWriter, r *http.Request) {
	proj, inst := r.PathValue("project"), r.PathValue("instance")
	recs, err := run.ListRuns(s.paths, proj, inst)
	if err != nil {
		s.fail(w, r, http.StatusBadRequest, "invalid", err)
		return
	}
	if recs == nil {
		recs = []*run.Record{}
	}
	writeJSON(w, http.StatusOK, map[string]any{"runs": recs})
}

// runJSON is the API view of a run record, located by its global id.
type runJSON struct {
	Project  string `json:"project"`
	Instance string `json:"instance"`
	run.Record
}

func (s *Server) getRun(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	proj, inst, rec, err := s.findRun(id)
	if err != nil {
		s.fail(w, r, http.StatusNotFound, "not_found", err)
		return
	}
	writeJSON(w, http.StatusOK, runJSON{Project: proj, Instance: inst, Record: *rec})
}

// findRun locates a run id across all instances under <root>/runs.
func (s *Server) findRun(id string) (proj, inst string, rec *run.Record, err error) {
	if !fsutil.ValidName(id) {
		return "", "", nil, fmt.Errorf("run %q not found", id)
	}
	projs, err := os.ReadDir(s.paths.Runs)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return "", "", nil, fmt.Errorf("run %q not found", id)
		}
		return "", "", nil, fmt.Errorf("list runs: %w", err)
	}
	for _, pd := range projs {
		if !pd.IsDir() {
			continue
		}
		insts, err := os.ReadDir(filepath.Join(s.paths.Runs, pd.Name()))
		if err != nil {
			continue
		}
		for _, idir := range insts {
			if !idir.IsDir() {
				continue
			}
			rec, err := run.GetRun(s.paths, pd.Name(), idir.Name(), id)
			if err == nil {
				return pd.Name(), idir.Name(), rec, nil
			}
		}
	}
	return "", "", nil, fmt.Errorf("run %q not found", id)
}

func (s *Server) getVersion(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{
		"version": s.deps.Version,
		"auth":    map[string]bool{"enabled": s.cfg.Auth.Enabled},
	})
}
