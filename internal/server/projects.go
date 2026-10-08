package server

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"gopkg.in/yaml.v3"

	"github.com/bunnyzr/pushrun/internal/project"
	"github.com/bunnyzr/pushrun/internal/provider"
	"github.com/bunnyzr/pushrun/internal/run"
)

func (s *Server) listProjects(w http.ResponseWriter, r *http.Request) {
	names, err := project.List(s.paths)
	if err != nil {
		s.fail(w, r, http.StatusInternalServerError, "internal", err)
		return
	}
	out := []*project.Project{}
	for _, name := range names {
		p, err := project.Load(s.paths, name)
		if err != nil {
			s.requestLogger(r).Warn("skipping unloadable project", "project", name, "error", err)
			continue
		}
		out = append(out, s.maskProject(p))
	}
	writeJSON(w, http.StatusOK, map[string]any{"projects": out})
}

func (s *Server) createProject(w http.ResponseWriter, r *http.Request) {
	var p project.Project
	if err := decodeJSONBody(r, &p); err != nil {
		s.fail(w, r, http.StatusBadRequest, "invalid", err)
		return
	}
	if p.Schema == "" {
		p.Schema = project.SchemaV1
	}
	if p.Schema != project.SchemaV1 {
		s.fail(w, r, http.StatusBadRequest, "invalid",
			fmt.Errorf("schema: must be %q, got %q", project.SchemaV1, p.Schema))
		return
	}
	if err := p.Validate(); err != nil {
		s.fail(w, r, http.StatusBadRequest, "invalid", err)
		return
	}
	if _, err := project.Load(s.paths, p.Name); err == nil {
		s.fail(w, r, http.StatusConflict, "conflict",
			fmt.Errorf("project %q already exists", p.Name))
		return
	}
	if err := project.Save(s.paths, &p); err != nil {
		s.fail(w, r, http.StatusBadRequest, "invalid", err)
		return
	}
	s.requestLogger(r).Info("project created", "project", p.Name)
	writeJSON(w, http.StatusCreated, s.maskProject(&p))
}

func (s *Server) getProject(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")
	p, err := project.Load(s.paths, name)
	if err != nil {
		s.fail(w, r, http.StatusNotFound, "not_found", err)
		return
	}
	writeJSON(w, http.StatusOK, s.maskProject(p))
}

func (s *Server) putProject(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")
	var p project.Project
	if err := decodeJSONBody(r, &p); err != nil {
		s.fail(w, r, http.StatusBadRequest, "invalid", err)
		return
	}
	if p.Name == "" {
		p.Name = name
	}
	if p.Name != name {
		s.fail(w, r, http.StatusBadRequest, "invalid",
			fmt.Errorf("name: body name %q does not match URL name %q", p.Name, name))
		return
	}
	if p.Schema == "" {
		p.Schema = project.SchemaV1
	}
	if p.Schema != project.SchemaV1 {
		s.fail(w, r, http.StatusBadRequest, "invalid",
			fmt.Errorf("schema: must be %q, got %q", project.SchemaV1, p.Schema))
		return
	}
	existing, err := project.Load(s.paths, name)
	if err != nil {
		existing = nil
	}
	orphans := s.restoreSecrets(&p, existing)
	if len(orphans) > 0 {
		s.failExtra(w, r, http.StatusBadRequest, "secret_params_orphaned",
			fmt.Errorf("secret parameter values could not be restored (the node was renamed or moved, or the secret is new): re-enter them for %s", strings.Join(orphans, ", ")),
			map[string]any{"orphaned_params": orphans})
		return
	}
	if err := p.Validate(); err != nil {
		s.fail(w, r, http.StatusBadRequest, "invalid", err)
		return
	}
	if err := project.Save(s.paths, &p); err != nil {
		s.fail(w, r, http.StatusBadRequest, "invalid", err)
		return
	}
	s.requestLogger(r).Info("project saved", "project", p.Name)
	writeJSON(w, http.StatusOK, s.maskProject(&p))
}

// deleteProject removes the project definition only; running instances are
// never stopped or touched.
func (s *Server) deleteProject(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")
	if err := project.Delete(s.paths, name); err != nil {
		s.fail(w, r, http.StatusNotFound, "not_found", err)
		return
	}
	s.requestLogger(r).Info("project deleted", "project", name)
	writeJSON(w, http.StatusOK, map[string]any{"status": "deleted"})
}

// warmupProject warms every provider mount of the project tree via the
// same EnsureWarmed the run engine uses (see docs/design.md), reporting
// per-mount status. Dedupe is by (provider id, warmup fingerprint), so two
// mounts of one provider with distinct warmup params each warm exactly once.
func (s *Server) warmupProject(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")
	p, err := project.Load(s.paths, name)
	if err != nil {
		s.fail(w, r, http.StatusNotFound, "not_found", err)
		return
	}
	type warmResult struct {
		Provider string `json:"provider"`
		Path     string `json:"path"`
		Status   string `json:"status"`
		Error    string `json:"error,omitempty"`
	}
	seen := map[string]bool{}
	results := []warmResult{}
	overall := run.StatusSuccess
	for _, n := range p.Tree {
		if n.Mount == nil {
			continue
		}
		def, _ := s.providerDef(n.Mount.Provider)
		key := n.Mount.Provider
		if def != nil {
			key += "\x00" + provider.WarmupFingerprint(def, n.Mount.Params)
		}
		if seen[key] {
			continue
		}
		seen[key] = true
		res := warmResult{Provider: n.Mount.Provider, Path: n.Path, Status: run.StatusSuccess}
		if def == nil {
			res.Status = run.StatusFailed
			res.Error = fmt.Sprintf("provider %q not found (import or create it first)", n.Mount.Provider)
		} else {
			log := s.requestLogger(r).With("provider", def.ID, "phase", "warmup")
			tail := &tailBuffer{max: 16 << 10}
			// Warmup runs detached from the request context: a client
			// disconnect must not cancel a warmup mid-script.
			if _, err := s.deps.Executor.EnsureWarmed(context.WithoutCancel(r.Context()), def, n.Mount.Params, tail); err != nil {
				res.Status = run.StatusFailed
				res.Error = err.Error()
				if out := strings.TrimSpace(tail.String()); out != "" {
					res.Error += ": " + out
				}
				log.Error("provider warmup failed", "project", name, "error", err)
			} else {
				log.Info("provider warmed", "project", name)
			}
		}
		if res.Status != run.StatusSuccess {
			overall = run.StatusFailed
		}
		results = append(results, res)
	}
	writeJSON(w, http.StatusOK, map[string]any{"status": overall, "results": results})
}

// warmupStatus reports the warmup state of every mounted tree node —
// "cold", "warming" (in flight), or "warm" with the completion time —
// without warming anything. POST on the same path is the trigger (see
// warmupProject). Status comes from the executor's WarmStatus, which for the
// git builtin checks the bare repo's snapshot ref (git owns no ProviderData
// cache dir, so Warmups is always empty for it).
func (s *Server) warmupStatus(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")
	p, err := project.Load(s.paths, name)
	if err != nil {
		s.fail(w, r, http.StatusNotFound, "not_found", err)
		return
	}
	type mountStatus struct {
		Path     string `json:"path"`
		Provider string `json:"provider"`
		Status   string `json:"status"`
		WarmedAt string `json:"warmed_at,omitempty"`
	}
	mounts := []mountStatus{}
	for _, n := range p.Tree {
		if n.Mount == nil {
			continue
		}
		ms := mountStatus{Path: n.Path, Provider: n.Mount.Provider, Status: "cold"}
		if def, _ := s.providerDef(n.Mount.Provider); def != nil {
			status, warmedAt := s.deps.Executor.WarmStatus(def, n.Mount.Params)
			ms.Status = status
			if !warmedAt.IsZero() {
				ms.WarmedAt = warmedAt.UTC().Format(time.RFC3339)
			}
		}
		mounts = append(mounts, ms)
	}
	writeJSON(w, http.StatusOK, map[string]any{"mounts": mounts})
}

// exportProject streams the project definition as a .tar.gz bundle holding
// the project YAML.
func (s *Server) exportProject(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")
	data, err := os.ReadFile(filepath.Join(s.paths.Projects, name+".yaml"))
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			s.fail(w, r, http.StatusNotFound, "not_found",
				fmt.Errorf("project %q not found", name))
			return
		}
		s.fail(w, r, http.StatusInternalServerError, "internal", err)
		return
	}
	w.Header().Set("Content-Type", "application/gzip")
	w.Header().Set("Content-Disposition", fmt.Sprintf("attachment; filename=%q", name+".tar.gz"))
	if err := writeTarGz(w, []tarEntry{{name: name + ".yaml", mode: 0o644, data: data}}); err != nil {
		s.requestLogger(r).Error("export project failed", "project", name, "error", err)
	}
}

// importProject reads a .tar.gz project bundle, validates the schema, and
// refuses the import — without writing anything — when referenced providers
// are missing.
func (s *Server) importProject(w http.ResponseWriter, r *http.Request) {
	files, err := readTarGz(http.MaxBytesReader(w, r.Body, maxBundleBytes))
	if err != nil {
		s.fail(w, r, http.StatusBadRequest, "invalid", err)
		return
	}
	var yamlData []byte
	for name, data := range files {
		if strings.HasSuffix(name, ".yaml") {
			if yamlData != nil {
				s.fail(w, r, http.StatusBadRequest, "invalid",
					fmt.Errorf("bundle contains multiple .yaml files; want exactly one project definition"))
				return
			}
			yamlData = data
		}
	}
	if yamlData == nil {
		s.fail(w, r, http.StatusBadRequest, "invalid",
			fmt.Errorf("bundle contains no .yaml project definition"))
		return
	}
	var p project.Project
	if err := yaml.Unmarshal(yamlData, &p); err != nil {
		s.fail(w, r, http.StatusBadRequest, "invalid", fmt.Errorf("parse project yaml: %w", err))
		return
	}
	if p.Schema != project.SchemaV1 {
		s.fail(w, r, http.StatusBadRequest, "invalid",
			fmt.Errorf("schema: must be %q, got %q", project.SchemaV1, p.Schema))
		return
	}
	if err := p.Validate(); err != nil {
		s.fail(w, r, http.StatusBadRequest, "invalid", err)
		return
	}
	if missing := s.missingProviders(&p); len(missing) > 0 {
		s.failExtra(w, r, http.StatusBadRequest, "missing_providers",
			fmt.Errorf("project %q references missing providers: %s (import them first, then retry)", p.Name, strings.Join(missing, ", ")),
			map[string]any{"missing_providers": missing})
		return
	}
	if err := project.Save(s.paths, &p); err != nil {
		s.fail(w, r, http.StatusInternalServerError, "internal", err)
		return
	}
	s.requestLogger(r).Info("project imported", "project", p.Name)
	writeJSON(w, http.StatusCreated, map[string]any{"status": "imported", "project": s.maskProject(&p)})
}

// tailBuffer keeps only the last max bytes written to it.
type tailBuffer struct {
	buf []byte
	max int
}

func (t *tailBuffer) Write(p []byte) (int, error) {
	t.buf = append(t.buf, p...)
	if len(t.buf) > t.max {
		t.buf = t.buf[len(t.buf)-t.max:]
	}
	return len(p), nil
}

func (t *tailBuffer) String() string { return string(t.buf) }
