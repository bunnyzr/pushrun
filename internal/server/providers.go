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

	"gopkg.in/yaml.v3"

	"github.com/bunnyzr/pushrun/internal/project"
	"github.com/bunnyzr/pushrun/internal/provider"
	"github.com/bunnyzr/pushrun/internal/run"
)

// providerJSON is the API view of a provider: the descriptor, whether it is
// a platform builtin, and its shared-artifact cache entries (always an
// array, empty for a never-warmed provider — e.g. the git builtin, whose
// cache is the bare repo, not a ProviderData dir). Scripts carries the
// provider's on-disk files and is populated only by the single-provider GET.
type providerJSON struct {
	*provider.Provider
	Builtin bool                  `json:"builtin"`
	Warmups []provider.WarmupInfo `json:"warmups"`
	Scripts map[string]string     `json:"scripts,omitempty"`
}

// newProviderJSON builds the API view of p with its warmup cache entries.
func (s *Server) newProviderJSON(p *provider.Provider, builtin bool) providerJSON {
	warmups := s.deps.Executor.Warmups(p.ID)
	if warmups == nil {
		warmups = []provider.WarmupInfo{}
	}
	return providerJSON{Provider: p, Builtin: builtin, Warmups: warmups}
}

// providerPayload is the create/update body: the descriptor plus optional
// inline script contents keyed by slash-separated relative path.
type providerPayload struct {
	provider.Provider
	Scripts map[string]string `json:"scripts,omitempty"`
}

func (s *Server) listProviders(w http.ResponseWriter, r *http.Request) {
	out := []providerJSON{}
	for _, b := range provider.Builtins() {
		bp := b
		out = append(out, s.newProviderJSON(&bp, true))
	}
	ids, err := provider.List(s.paths)
	if err != nil {
		s.fail(w, r, http.StatusInternalServerError, "internal", err)
		return
	}
	for _, id := range ids {
		if isBuiltinID(id) {
			continue // builtin ids are platform-owned; never listed twice
		}
		p, err := provider.Load(s.paths, id)
		if err != nil {
			s.requestLogger(r).Warn("skipping unloadable provider", "provider", id, "error", err)
			continue
		}
		out = append(out, s.newProviderJSON(p, false))
	}
	writeJSON(w, http.StatusOK, map[string]any{"providers": out})
}

func (s *Server) getProvider(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	p, builtin := s.providerDef(id)
	if p == nil {
		s.fail(w, r, http.StatusNotFound, "not_found",
			fmt.Errorf("provider %q not found", id))
		return
	}
	out := s.newProviderJSON(p, builtin)
	// Builtins own no on-disk dir; only stored providers expose their
	// script files. The list endpoint stays light and never fills this in.
	if !builtin {
		out.Scripts = s.providerScripts(id)
	}
	writeJSON(w, http.StatusOK, out)
}

// maxScriptFileBytes caps a single file included in a provider's scripts
// map; anything larger is skipped (defensive — scripts are authored text).
const maxScriptFileBytes = 1 << 20

// providerScripts reads every regular file under the provider's dir into a
// map keyed by slash-separated relative path. Unreadable entries, files
// over maxScriptFileBytes, and a missing dir all yield a smaller (or nil)
// map rather than an error: the descriptor is already loaded, so a partial
// view is still useful.
func (s *Server) providerScripts(id string) map[string]string {
	dir := filepath.Join(s.paths.Providers, id)
	scripts := map[string]string{}
	err := filepath.WalkDir(dir, func(path string, d fs.DirEntry, err error) error {
		if err != nil || !d.Type().IsRegular() {
			return nil
		}
		info, err := d.Info()
		if err != nil || info.Size() > maxScriptFileBytes {
			return nil
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return nil
		}
		rel, err := filepath.Rel(dir, path)
		if err != nil {
			return nil
		}
		scripts[filepath.ToSlash(rel)] = string(data)
		return nil
	})
	if err != nil || len(scripts) == 0 {
		return nil
	}
	return scripts
}

func (s *Server) createProvider(w http.ResponseWriter, r *http.Request) {
	s.saveProvider(w, r, "", true)
}

func (s *Server) putProvider(w http.ResponseWriter, r *http.Request) {
	s.saveProvider(w, r, r.PathValue("id"), false)
}

// saveProvider validates and persists a provider descriptor plus any inline
// scripts. Builtin ids are platform-owned: saving one is a conflict.
// urlID is empty on create (collection POST).
func (s *Server) saveProvider(w http.ResponseWriter, r *http.Request, urlID string, create bool) {
	var payload providerPayload
	if err := decodeJSONBody(r, &payload); err != nil {
		s.fail(w, r, http.StatusBadRequest, "invalid", err)
		return
	}
	p := payload.Provider
	if p.ID == "" {
		p.ID = urlID
	}
	if urlID != "" && p.ID != urlID {
		s.fail(w, r, http.StatusBadRequest, "invalid",
			fmt.Errorf("id: body id %q does not match URL id %q", p.ID, urlID))
		return
	}
	if p.Schema == "" {
		p.Schema = provider.SchemaV1
	}
	if p.Schema != provider.SchemaV1 {
		s.fail(w, r, http.StatusBadRequest, "invalid",
			fmt.Errorf("schema: must be %q, got %q", provider.SchemaV1, p.Schema))
		return
	}
	if isBuiltinID(p.ID) {
		s.fail(w, r, http.StatusConflict, "conflict",
			fmt.Errorf("provider id %q is reserved for a platform builtin", p.ID))
		return
	}
	if err := p.Validate(); err != nil {
		s.fail(w, r, http.StatusBadRequest, "invalid", err)
		return
	}
	dir := filepath.Join(s.paths.Providers, p.ID)
	if create {
		if _, err := os.Stat(filepath.Join(dir, "provider.yaml")); err == nil {
			s.fail(w, r, http.StatusConflict, "conflict",
				fmt.Errorf("provider %q already exists", p.ID))
			return
		}
	}
	// Validate inline script paths, and that every declared script will
	// exist after the write.
	for name := range payload.Scripts {
		if !cleanRelPath(name) {
			s.fail(w, r, http.StatusBadRequest, "invalid",
				fmt.Errorf("scripts: path %q must be relative, clean, and under the provider dir", name))
			return
		}
	}
	for _, script := range []struct {
		field string
		path  string
	}{
		{"warmup", p.Warmup},
		{"install", p.Install},
	} {
		if script.path == "" {
			continue
		}
		if _, ok := payload.Scripts[script.path]; ok {
			continue
		}
		if _, err := os.Stat(filepath.Join(dir, filepath.FromSlash(script.path))); err != nil {
			s.fail(w, r, http.StatusBadRequest, "invalid",
				fmt.Errorf("%s: script %q not found; include it in scripts or upload it via import", script.field, script.path))
			return
		}
	}
	for name, content := range payload.Scripts {
		fp := filepath.Join(dir, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(fp), 0o755); err != nil {
			s.fail(w, r, http.StatusInternalServerError, "internal", err)
			return
		}
		mode := fs.FileMode(0o644)
		if strings.HasSuffix(name, ".sh") {
			mode = 0o755
		}
		if err := os.WriteFile(fp, []byte(content), mode); err != nil {
			s.fail(w, r, http.StatusInternalServerError, "internal", err)
			return
		}
	}
	if err := provider.Save(s.paths, &p); err != nil {
		s.fail(w, r, http.StatusBadRequest, "invalid", err)
		return
	}
	s.requestLogger(r).Info("provider saved", "provider", p.ID)
	status := http.StatusOK
	if create {
		status = http.StatusCreated
	}
	writeJSON(w, status, s.newProviderJSON(&p, false))
}

// deleteProvider removes a provider unless it is a builtin or still
// referenced by a project (409 with the referencing project list).
func (s *Server) deleteProvider(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if isBuiltinID(id) {
		s.fail(w, r, http.StatusConflict, "conflict",
			fmt.Errorf("provider %q is a platform builtin and cannot be deleted", id))
		return
	}
	refs, err := s.referencingProjects(id)
	if err != nil {
		s.fail(w, r, http.StatusInternalServerError, "internal", err)
		return
	}
	if len(refs) > 0 {
		s.failExtra(w, r, http.StatusConflict, "conflict",
			fmt.Errorf("provider %q is still referenced by projects: %s", id, strings.Join(refs, ", ")),
			map[string]any{"referenced_by": refs})
		return
	}
	if err := provider.Delete(s.paths, id); err != nil {
		s.fail(w, r, http.StatusNotFound, "not_found", err)
		return
	}
	s.requestLogger(r).Info("provider deleted", "provider", id)
	writeJSON(w, http.StatusOK, map[string]any{"status": "deleted"})
}

// referencingProjects returns the sorted names of projects that mount id.
func (s *Server) referencingProjects(id string) ([]string, error) {
	names, err := project.List(s.paths)
	if err != nil {
		return nil, err
	}
	var refs []string
	for _, name := range names {
		p, err := project.Load(s.paths, name)
		if err != nil {
			continue
		}
		for _, n := range p.Tree {
			if n.Mount != nil && n.Mount.Provider == id {
				refs = append(refs, name)
				break
			}
		}
	}
	return refs, nil
}

// warmupProvider warms one provider with the params in the request body via
// the same EnsureWarmed the run engine uses (see docs/design.md).
func (s *Server) warmupProvider(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	def, _ := s.providerDef(id)
	if def == nil {
		s.fail(w, r, http.StatusNotFound, "not_found",
			fmt.Errorf("provider %q not found", id))
		return
	}
	var body struct {
		Params map[string]string `json:"params"`
	}
	if r.Body != nil && r.ContentLength != 0 {
		if err := decodeJSONBody(r, &body); err != nil {
			s.fail(w, r, http.StatusBadRequest, "invalid", err)
			return
		}
	}
	log := s.requestLogger(r).With("provider", id, "phase", "warmup")
	// Warmup runs detached from the request context: a client disconnect
	// must not cancel a warmup mid-script.
	tail := &tailBuffer{max: 16 << 10}
	if _, err := s.deps.Executor.EnsureWarmed(context.WithoutCancel(r.Context()), def, body.Params, tail); err != nil {
		log.Error("provider warmup failed", "error", err)
		msg := err.Error()
		if out := strings.TrimSpace(tail.String()); out != "" {
			msg += ": " + out
		}
		writeJSON(w, http.StatusOK, map[string]any{"provider": id, "status": run.StatusFailed, "error": msg})
		return
	}
	log.Info("provider warmed")
	writeJSON(w, http.StatusOK, map[string]any{"provider": id, "status": run.StatusSuccess})
}

// exportProvider streams the provider dir (provider.yaml + scripts) as a
// .tar.gz bundle. Builtins are code and cannot be exported.
func (s *Server) exportProvider(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if isBuiltinID(id) {
		s.fail(w, r, http.StatusConflict, "conflict",
			fmt.Errorf("provider %q is a platform builtin and cannot be exported", id))
		return
	}
	if _, err := provider.Load(s.paths, id); err != nil {
		s.fail(w, r, http.StatusNotFound, "not_found", err)
		return
	}
	dir := filepath.Join(s.paths.Providers, id)
	var files []tarEntry
	err := filepath.WalkDir(dir, func(path string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		rel, err := filepath.Rel(dir, path)
		if err != nil {
			return err
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		mode := int64(0o644)
		if strings.HasSuffix(rel, ".sh") {
			mode = 0o755
		}
		files = append(files, tarEntry{name: filepath.ToSlash(rel), mode: mode, data: data})
		return nil
	})
	if err != nil {
		s.fail(w, r, http.StatusInternalServerError, "internal", err)
		return
	}
	w.Header().Set("Content-Type", "application/gzip")
	w.Header().Set("Content-Disposition", fmt.Sprintf("attachment; filename=%q", id+".tar.gz"))
	if err := writeTarGz(w, files); err != nil {
		s.requestLogger(r).Error("export provider failed", "provider", id, "error", err)
	}
}

// importProvider reads a .tar.gz provider bundle, validates the descriptor
// and script set fully, and only then swaps it into the store — a failed
// import never leaves a partial provider behind.
func (s *Server) importProvider(w http.ResponseWriter, r *http.Request) {
	files, err := readTarGz(http.MaxBytesReader(w, r.Body, maxBundleBytes))
	if err != nil {
		s.fail(w, r, http.StatusBadRequest, "invalid", err)
		return
	}
	yamlData, ok := files["provider.yaml"]
	if !ok {
		s.fail(w, r, http.StatusBadRequest, "invalid",
			errors.New("bundle contains no provider.yaml"))
		return
	}
	var p provider.Provider
	if err := yaml.Unmarshal(yamlData, &p); err != nil {
		s.fail(w, r, http.StatusBadRequest, "invalid", fmt.Errorf("parse provider.yaml: %w", err))
		return
	}
	if p.Schema != provider.SchemaV1 {
		s.fail(w, r, http.StatusBadRequest, "invalid",
			fmt.Errorf("schema: must be %q, got %q", provider.SchemaV1, p.Schema))
		return
	}
	if isBuiltinID(p.ID) {
		s.fail(w, r, http.StatusConflict, "conflict",
			fmt.Errorf("provider id %q is reserved for a platform builtin", p.ID))
		return
	}
	if err := p.Validate(); err != nil {
		s.fail(w, r, http.StatusBadRequest, "invalid", err)
		return
	}
	for _, script := range []struct {
		field string
		path  string
	}{
		{"warmup", p.Warmup},
		{"install", p.Install},
	} {
		if script.path == "" {
			continue
		}
		if _, ok := files[script.path]; !ok {
			s.fail(w, r, http.StatusBadRequest, "invalid",
				fmt.Errorf("%s: script %q missing from the bundle", script.field, script.path))
			return
		}
	}

	// Stage the provider dir, then swap it into place atomically enough:
	// validation is complete before anything is written. An existing target
	// is renamed aside first and only removed once the staging dir is in
	// place, so a failed swap restores the previous provider instead of
	// leaving a gap.
	if err := os.MkdirAll(s.paths.Providers, 0o755); err != nil {
		s.fail(w, r, http.StatusInternalServerError, "internal", err)
		return
	}
	staging, err := os.MkdirTemp(s.paths.Providers, ".import-*")
	if err != nil {
		s.fail(w, r, http.StatusInternalServerError, "internal", err)
		return
	}
	defer func() { _ = os.RemoveAll(staging) }()
	for name, data := range files {
		fp := filepath.Join(staging, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(fp), 0o755); err != nil {
			s.fail(w, r, http.StatusInternalServerError, "internal", err)
			return
		}
		mode := fs.FileMode(0o644)
		if strings.HasSuffix(name, ".sh") {
			mode = 0o755
		}
		if err := os.WriteFile(fp, data, mode); err != nil {
			s.fail(w, r, http.StatusInternalServerError, "internal", err)
			return
		}
	}
	target := filepath.Join(s.paths.Providers, p.ID)
	var aside string
	if _, err := os.Stat(target); err == nil {
		// Vacate a unique sibling name, then move the existing provider
		// aside: if the staging rename fails, the aside moves back.
		tmp, err := os.MkdirTemp(s.paths.Providers, ".import-aside-*")
		if err != nil {
			s.fail(w, r, http.StatusInternalServerError, "internal", err)
			return
		}
		if err := os.Remove(tmp); err != nil {
			s.fail(w, r, http.StatusInternalServerError, "internal", err)
			return
		}
		if err := os.Rename(target, tmp); err != nil {
			s.fail(w, r, http.StatusInternalServerError, "internal", err)
			return
		}
		aside = tmp
	}
	if err := os.Rename(staging, target); err != nil {
		if aside != "" {
			_ = os.Rename(aside, target)
		}
		s.fail(w, r, http.StatusInternalServerError, "internal", err)
		return
	}
	if aside != "" {
		_ = os.RemoveAll(aside)
	}
	s.requestLogger(r).Info("provider imported", "provider", p.ID)
	writeJSON(w, http.StatusCreated, map[string]any{"status": "imported", "provider": s.newProviderJSON(&p, false)})
}
