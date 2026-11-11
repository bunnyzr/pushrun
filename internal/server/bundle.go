package server

import (
	"errors"
	"fmt"
	"io/fs"
	"net/http"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"

	"gopkg.in/yaml.v3"

	"github.com/bunnyzr/pushrun/internal/fsutil"
	"github.com/bunnyzr/pushrun/internal/project"
	"github.com/bunnyzr/pushrun/internal/provider"
)

// A full deployment bundle carries every stored provider directory and every
// project definition in one archive:
//
//	bundle.yaml
//	providers/<id>/provider.yaml
//	providers/<id>/<script files>
//	projects/<name>.yaml
//
// Project YAML is copied verbatim, so secret mount parameters are included:
// the bundle is a backup/transfer artifact, protected by the deployment token.
const (
	bundleSchema       = "pushrun.bundle/v1"
	bundleManifestName = "bundle.yaml"
)

type bundleManifest struct {
	Schema string `yaml:"schema"`
}

type bundleProvider struct {
	def   *provider.Provider
	files map[string][]byte
}

// exportBundle streams every stored provider and project as one tar.gz.
// Builtin providers are platform-owned and deliberately excluded; importers
// resolve them from their own binary.
func (s *Server) exportBundle(w http.ResponseWriter, r *http.Request) {
	manifest, err := yaml.Marshal(bundleManifest{Schema: bundleSchema})
	if err != nil {
		s.fail(w, r, http.StatusInternalServerError, "internal", err)
		return
	}
	files := []tarEntry{{name: bundleManifestName, mode: 0o644, data: manifest}}

	ids, err := provider.List(s.paths)
	if err != nil {
		s.fail(w, r, http.StatusInternalServerError, "internal", err)
		return
	}
	for _, id := range ids {
		if isBuiltinID(id) {
			continue
		}
		entries, err := collectProviderFiles(s.paths.Providers, id)
		if err != nil {
			s.fail(w, r, http.StatusInternalServerError, "internal", err)
			return
		}
		files = append(files, entries...)
	}

	names, err := project.List(s.paths)
	if err != nil {
		s.fail(w, r, http.StatusInternalServerError, "internal", err)
		return
	}
	for _, name := range names {
		data, err := os.ReadFile(filepath.Join(s.paths.Projects, name+".yaml"))
		if err != nil {
			if errors.Is(err, fs.ErrNotExist) {
				continue
			}
			s.fail(w, r, http.StatusInternalServerError, "internal", err)
			return
		}
		files = append(files, tarEntry{
			name: path.Join("projects", name+".yaml"),
			mode: 0o644,
			data: data,
		})
	}

	w.Header().Set("Content-Type", "application/gzip")
	w.Header().Set("Content-Disposition", `attachment; filename="pushrun-bundle.tar.gz"`)
	if err := writeTarGz(w, files); err != nil {
		s.requestLogger(r).Error("export bundle failed", "error", err)
	}
}

// collectProviderFiles reads one provider directory into bundle entries
// prefixed with providers/<id>/.
func collectProviderFiles(root, id string) ([]tarEntry, error) {
	dir := filepath.Join(root, id)
	var out []tarEntry
	err := filepath.WalkDir(dir, func(fp string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			return nil
		}
		rel, err := filepath.Rel(dir, fp)
		if err != nil {
			return err
		}
		data, err := os.ReadFile(fp)
		if err != nil {
			return err
		}
		mode := int64(0o644)
		if strings.HasSuffix(rel, ".sh") {
			mode = 0o755
		}
		out = append(out, tarEntry{
			name: path.Join("providers", id, filepath.ToSlash(rel)),
			mode: mode,
			data: data,
		})
		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("read provider %q: %w", id, err)
	}
	return out, nil
}

// importBundle validates the whole archive before writing anything, then
// installs providers first and projects second. Existing definitions with
// the same ids/names are replaced.
func (s *Server) importBundle(w http.ResponseWriter, r *http.Request) {
	files, err := readTarGz(http.MaxBytesReader(w, r.Body, maxBundleBytes))
	if err != nil {
		s.fail(w, r, http.StatusBadRequest, "invalid", err)
		return
	}
	manifestData, ok := files[bundleManifestName]
	if !ok {
		s.fail(w, r, http.StatusBadRequest, "invalid",
			fmt.Errorf("bundle contains no %s", bundleManifestName))
		return
	}
	var manifest bundleManifest
	if err := yaml.Unmarshal(manifestData, &manifest); err != nil {
		s.fail(w, r, http.StatusBadRequest, "invalid",
			fmt.Errorf("parse %s: %w", bundleManifestName, err))
		return
	}
	if manifest.Schema != bundleSchema {
		s.fail(w, r, http.StatusBadRequest, "invalid",
			fmt.Errorf("schema: must be %q, got %q", bundleSchema, manifest.Schema))
		return
	}
	delete(files, bundleManifestName)

	providers, err := parseBundleProviders(files)
	if err != nil {
		s.fail(w, r, http.StatusBadRequest, "invalid", err)
		return
	}
	projects, err := parseBundleProjects(files)
	if err != nil {
		s.fail(w, r, http.StatusBadRequest, "invalid", err)
		return
	}
	if len(providers) == 0 && len(projects) == 0 {
		s.fail(w, r, http.StatusBadRequest, "invalid",
			errors.New("bundle contains no providers or projects"))
		return
	}

	available := map[string]bool{}
	for _, builtin := range provider.Builtins() {
		available[builtin.ID] = true
	}
	stored, err := provider.List(s.paths)
	if err != nil {
		s.fail(w, r, http.StatusInternalServerError, "internal", err)
		return
	}
	for _, id := range stored {
		available[id] = true
	}
	for id := range providers {
		available[id] = true
	}
	var missing []string
	seenMissing := map[string]bool{}
	for _, p := range projects {
		for _, node := range p.Tree {
			if node.Mount == nil || available[node.Mount.Provider] || seenMissing[node.Mount.Provider] {
				continue
			}
			seenMissing[node.Mount.Provider] = true
			missing = append(missing, node.Mount.Provider)
		}
	}
	if len(missing) > 0 {
		sort.Strings(missing)
		s.failExtra(w, r, http.StatusBadRequest, "missing_providers",
			fmt.Errorf("projects reference providers missing from the bundle and this deployment: %s", strings.Join(missing, ", ")),
			map[string]any{"missing_providers": missing})
		return
	}

	providerIDs := make([]string, 0, len(providers))
	for id := range providers {
		providerIDs = append(providerIDs, id)
	}
	sort.Strings(providerIDs)
	for _, id := range providerIDs {
		if err := s.writeProviderDir(id, providers[id].files); err != nil {
			s.fail(w, r, http.StatusInternalServerError, "internal", err)
			return
		}
	}

	projectNames := make([]string, 0, len(projects))
	for name := range projects {
		projectNames = append(projectNames, name)
	}
	sort.Strings(projectNames)
	for _, name := range projectNames {
		if err := project.Save(s.paths, projects[name]); err != nil {
			s.fail(w, r, http.StatusInternalServerError, "internal", err)
			return
		}
	}

	s.requestLogger(r).Info("bundle imported",
		"providers", len(providerIDs), "projects", len(projectNames))
	writeJSON(w, http.StatusCreated, map[string]any{
		"status":    "imported",
		"providers": providerIDs,
		"projects":  projectNames,
	})
}

func parseBundleProviders(files map[string][]byte) (map[string]*bundleProvider, error) {
	out := map[string]*bundleProvider{}
	for name, data := range files {
		if !strings.HasPrefix(name, "providers/") {
			continue
		}
		rest := strings.TrimPrefix(name, "providers/")
		id, rel, ok := strings.Cut(rest, "/")
		if !ok || id == "" || rel == "" {
			return nil, fmt.Errorf("provider entry %q must be providers/<id>/<file>", name)
		}
		if !fsutil.ValidName(id) {
			return nil, fmt.Errorf("provider entry %q has invalid id %q", name, id)
		}
		if !cleanRelPath(rel) {
			return nil, fmt.Errorf("provider entry %q has unsafe file path %q", name, rel)
		}
		bp := out[id]
		if bp == nil {
			bp = &bundleProvider{files: map[string][]byte{}}
			out[id] = bp
		}
		bp.files[rel] = data
	}
	for id, bp := range out {
		yamlData, ok := bp.files["provider.yaml"]
		if !ok {
			return nil, fmt.Errorf("provider %q is missing provider.yaml", id)
		}
		var p provider.Provider
		if err := yaml.Unmarshal(yamlData, &p); err != nil {
			return nil, fmt.Errorf("parse provider %q: %w", id, err)
		}
		if p.Schema != provider.SchemaV1 {
			return nil, fmt.Errorf("provider %q: schema must be %q, got %q", id, provider.SchemaV1, p.Schema)
		}
		if p.ID != id {
			return nil, fmt.Errorf("provider %q: provider.yaml declares id %q", id, p.ID)
		}
		if isBuiltinID(id) {
			return nil, fmt.Errorf("provider id %q is reserved for a platform builtin", id)
		}
		if err := p.Validate(); err != nil {
			return nil, err
		}
		for _, script := range []string{p.Warmup, p.Install} {
			if script == "" {
				continue
			}
			if _, ok := bp.files[script]; !ok {
				return nil, fmt.Errorf("provider %q: script %q is missing from the bundle", id, script)
			}
		}
		bp.def = &p
	}
	return out, nil
}

func parseBundleProjects(files map[string][]byte) (map[string]*project.Project, error) {
	out := map[string]*project.Project{}
	for name, data := range files {
		if !strings.HasPrefix(name, "projects/") || !strings.HasSuffix(name, ".yaml") {
			continue
		}
		rel := strings.TrimPrefix(name, "projects/")
		if strings.Contains(rel, "/") {
			return nil, fmt.Errorf("project entry %q must be projects/<name>.yaml", name)
		}
		var p project.Project
		if err := yaml.Unmarshal(data, &p); err != nil {
			return nil, fmt.Errorf("parse project %q: %w", name, err)
		}
		if p.Schema != project.SchemaV1 {
			return nil, fmt.Errorf("project %q: schema must be %q, got %q", name, project.SchemaV1, p.Schema)
		}
		if base := strings.TrimSuffix(rel, ".yaml"); p.Name != base {
			return nil, fmt.Errorf("project entry %q declares name %q", name, p.Name)
		}
		if err := p.Validate(); err != nil {
			return nil, err
		}
		out[p.Name] = &p
	}
	return out, nil
}
