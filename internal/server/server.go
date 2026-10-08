// Package server implements the pushrun HTTP layer: the REST API, token
// auth, request-id correlation, and import/export bundles (see
// docs/design.md and docs/api.md).
package server

import (
	"archive/tar"
	"compress/gzip"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"io/fs"
	"log/slog"
	"net/http"
	"path"
	"sort"
	"strings"
	"sync"

	"github.com/bunnyzr/pushrun/internal/config"
	"github.com/bunnyzr/pushrun/internal/instance"
	"github.com/bunnyzr/pushrun/internal/project"
	"github.com/bunnyzr/pushrun/internal/provider"
	"github.com/bunnyzr/pushrun/internal/run"
	"github.com/bunnyzr/pushrun/web"
)

// maskedValue replaces secret parameter values in API responses.
const maskedValue = "***"

// maxBundleBytes caps the compressed size of an uploaded import bundle.
const maxBundleBytes = 64 << 20

// Deps carries the collaborators the HTTP layer needs.
type Deps struct {
	Manager  *run.Manager
	Executor *provider.Executor
	Pool     *instance.PortPool
	Token    string
	Version  string
	Logger   *slog.Logger
	Web      fs.FS // web console assets; nil uses the embedded placeholder
}

// Server serves the REST API over the data-root layout in paths.
type Server struct {
	paths config.Paths
	cfg   *config.Config
	deps  Deps
	log   *slog.Logger

	hookOnce      sync.Once
	hookSecretVal string
	hookSecretErr error
}

// New builds the root handler: method-pattern routes, token auth on
// /api/v1/*, and request-id correlation on every response.
func New(paths config.Paths, cfg *config.Config, deps Deps) http.Handler {
	logger := deps.Logger
	if logger == nil {
		logger = slog.Default()
	}
	s := &Server{paths: paths, cfg: cfg, deps: deps, log: logger.With("component", "server")}

	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/v1/projects", s.listProjects)
	mux.HandleFunc("POST /api/v1/projects", s.createProject)
	mux.HandleFunc("GET /api/v1/projects/{name}", s.getProject)
	mux.HandleFunc("PUT /api/v1/projects/{name}", s.putProject)
	mux.HandleFunc("DELETE /api/v1/projects/{name}", s.deleteProject)
	mux.HandleFunc("POST /api/v1/projects/{name}/warmup", s.warmupProject)
	mux.HandleFunc("GET /api/v1/projects/{name}/warmup", s.warmupStatus)
	mux.HandleFunc("POST /api/v1/projects/import", s.importProject)
	mux.HandleFunc("GET /api/v1/projects/{name}/export", s.exportProject)

	mux.HandleFunc("GET /api/v1/providers", s.listProviders)
	mux.HandleFunc("POST /api/v1/providers", s.createProvider)
	mux.HandleFunc("GET /api/v1/providers/{id}", s.getProvider)
	mux.HandleFunc("PUT /api/v1/providers/{id}", s.putProvider)
	mux.HandleFunc("DELETE /api/v1/providers/{id}", s.deleteProvider)
	mux.HandleFunc("POST /api/v1/providers/{id}/warmup", s.warmupProvider)
	mux.HandleFunc("POST /api/v1/providers/import", s.importProvider)
	mux.HandleFunc("GET /api/v1/providers/{id}/export", s.exportProvider)

	mux.HandleFunc("GET /api/v1/instances", s.listInstances)
	mux.HandleFunc("GET /api/v1/instances/{project}/{instance}", s.getInstance)
	mux.HandleFunc("POST /api/v1/instances/{project}/{instance}/{action}", s.instanceAction)
	mux.HandleFunc("DELETE /api/v1/instances/{project}/{instance}", s.deleteInstance)
	mux.HandleFunc("GET /api/v1/instances/{project}/{instance}/runs", s.listRuns)
	mux.HandleFunc("GET /api/v1/instances/{project}/{instance}/logs/tree", s.logsTree)
	mux.HandleFunc("GET /api/v1/instances/{project}/{instance}/logs/file", s.logsFile)
	mux.HandleFunc("GET /api/v1/runs/{id}", s.getRun)
	mux.HandleFunc("GET /api/v1/runs/{id}/output", s.runOutput)

	mux.HandleFunc("GET /api/v1/resolve", s.resolveProject)

	mux.HandleFunc("GET /api/v1/version", s.getVersion)

	// Client script distribution (see docs/design.md); token-authenticated in the
	// handlers, not by the /api/v1 middleware.
	mux.HandleFunc("GET /install.sh", s.serveInstallSh)
	mux.HandleFunc("GET /client.sh", s.serveClientSh)

	// Git smart-HTTP endpoint and the loopback-only hook callback. Both
	// carry their own auth (token Basic/Bearer for /git, hook secret for
	// /internal) and deliberately bypass the /api/v1 middleware.
	mux.HandleFunc("GET /git/{rest...}", s.serveGit)
	mux.HandleFunc("POST /git/{rest...}", s.serveGit)
	mux.HandleFunc("POST /internal/v1/hooks/post-receive", s.handleHookPostReceive)

	webFS := deps.Web
	if webFS == nil {
		if sub, err := fs.Sub(web.Dist, "dist"); err == nil {
			webFS = sub
		}
	}
	if webFS != nil {
		mux.Handle("GET /", s.staticHandler(webFS))
	}

	return s.requestIDMiddleware(s.authMiddleware(mux))
}

// --- request-id middleware and error shape ---

type ctxKey int

const (
	ctxRequestID ctxKey = iota
	ctxLogger
)

func newRequestID() string {
	var buf [16]byte
	if _, err := rand.Read(buf[:]); err != nil {
		panic(fmt.Sprintf("generate request id: %v", err))
	}
	return hex.EncodeToString(buf[:])
}

// requestIDMiddleware assigns every request an id, echoes it in the
// X-Request-Id header, and puts a request-scoped logger (with request_id,
// method, path) into the context.
func (s *Server) requestIDMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		id := newRequestID()
		w.Header().Set("X-Request-Id", id)
		reqLog := s.log.With("request_id", id, "method", r.Method, "path", r.URL.Path)
		ctx := context.WithValue(r.Context(), ctxRequestID, id)
		ctx = context.WithValue(ctx, ctxLogger, reqLog)
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

func requestID(r *http.Request) string {
	id, _ := r.Context().Value(ctxRequestID).(string)
	return id
}

func (s *Server) requestLogger(r *http.Request) *slog.Logger {
	if l, ok := r.Context().Value(ctxLogger).(*slog.Logger); ok {
		return l
	}
	return s.log
}

// errBody is the standard error payload.
type errBody struct {
	Code      string `json:"code"`
	Message   string `json:"message"`
	RequestID string `json:"request_id"`
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

// fail writes the standard error JSON and logs the causing error once, at
// the handler level: 5xx at error level, 4xx at debug.
func (s *Server) fail(w http.ResponseWriter, r *http.Request, status int, code string, err error) {
	s.failExtra(w, r, status, code, err, nil)
}

func (s *Server) failExtra(w http.ResponseWriter, r *http.Request, status int, code string, err error, extra map[string]any) {
	if err != nil {
		log := s.requestLogger(r)
		if status >= 500 {
			log.Error("request failed", "code", code, "error", err)
		} else {
			log.Debug("request rejected", "code", code, "error", err)
		}
	}
	body := map[string]any{"error": errBody{Code: code, Message: err.Error(), RequestID: requestID(r)}}
	for k, v := range extra {
		body[k] = v
	}
	writeJSON(w, status, body)
}

func decodeJSONBody(r *http.Request, v any) error {
	dec := json.NewDecoder(io.LimitReader(r.Body, 16<<20))
	if err := dec.Decode(v); err != nil {
		return fmt.Errorf("invalid JSON body: %w", err)
	}
	return nil
}

// --- shared helpers ---

// providerDef resolves a provider id: builtins win over stored providers
// (builtin ids are platform-owned and can never be shadowed).
func (s *Server) providerDef(id string) (p *provider.Provider, builtin bool) {
	for _, b := range provider.Builtins() {
		if b.ID == id {
			bp := b
			return &bp, true
		}
	}
	p, err := provider.Load(s.paths, id)
	if err != nil {
		return nil, false
	}
	return p, false
}

func isBuiltinID(id string) bool {
	for _, b := range provider.Builtins() {
		if b.ID == id {
			return true
		}
	}
	return false
}

// maskProject returns a copy of p with every secret-declared mount param
// value replaced by "***". Secret values never leave the API. When a
// mount's provider no longer exists, masking fails
// closed: every param of that mount is masked, since there is no
// declaration left to tell secrets from plain values.
func (s *Server) maskProject(p *project.Project) *project.Project {
	out := *p
	out.Tree = make([]project.Node, len(p.Tree))
	for i, n := range p.Tree {
		out.Tree[i] = n
		if n.Mount == nil || len(n.Mount.Params) == 0 {
			continue
		}
		def, _ := s.providerDef(n.Mount.Provider)
		if def == nil {
			params := make(map[string]string, len(n.Mount.Params))
			for k := range n.Mount.Params {
				params[k] = maskedValue
			}
			out.Tree[i].Mount = &project.Mount{Provider: n.Mount.Provider, Primary: n.Mount.Primary, Params: params}
			continue
		}
		secret := make(map[string]bool, len(def.Params))
		for _, prm := range def.Params {
			if prm.Secret {
				secret[prm.ID] = true
			}
		}
		if len(secret) == 0 {
			continue
		}
		params := make(map[string]string, len(n.Mount.Params))
		for k, v := range n.Mount.Params {
			if secret[k] {
				v = maskedValue
			}
			params[k] = v
		}
		out.Tree[i].Mount = &project.Mount{Provider: n.Mount.Provider, Primary: n.Mount.Primary, Params: params}
	}
	return &out
}

// restoreSecrets replaces masked "***" secret values in incoming with the
// stored values from existing, so a client that round-trips a masked GET
// body through PUT does not clobber secrets. It returns the "path: param"
// list of masked secrets that have no stored value to restore — typically
// because the node was renamed or moved — and drops those params from
// incoming so the literal "***" can never be persisted. Like maskProject it
// fails closed: when a mount's provider is unknown, every masked param is
// treated as a secret.
func (s *Server) restoreSecrets(incoming, existing *project.Project) []string {
	var orphans []string
	type mountKey struct{ path, prov string }
	stored := make(map[mountKey]map[string]string)
	if existing != nil {
		for _, n := range existing.Tree {
			if n.Mount != nil {
				stored[mountKey{n.Path, n.Mount.Provider}] = n.Mount.Params
			}
		}
	}
	for i, n := range incoming.Tree {
		if n.Mount == nil {
			continue
		}
		def, _ := s.providerDef(n.Mount.Provider)
		secret := map[string]bool{}
		if def != nil {
			for _, prm := range def.Params {
				if prm.Secret {
					secret[prm.ID] = true
				}
			}
		}
		old := stored[mountKey{n.Path, n.Mount.Provider}]
		for id, v := range n.Mount.Params {
			if v != maskedValue || (def != nil && !secret[id]) {
				continue
			}
			if sv, ok := old[id]; ok {
				incoming.Tree[i].Mount.Params[id] = sv
			} else {
				delete(incoming.Tree[i].Mount.Params, id)
				orphans = append(orphans, n.Path+": "+id)
			}
		}
	}
	sort.Strings(orphans)
	return orphans
}

// missingProviders returns the sorted ids of providers referenced by p that
// resolve to neither a builtin nor a stored provider.
func (s *Server) missingProviders(p *project.Project) []string {
	seen := map[string]bool{}
	var missing []string
	for _, n := range p.Tree {
		if n.Mount == nil || seen[n.Mount.Provider] {
			continue
		}
		seen[n.Mount.Provider] = true
		if def, _ := s.providerDef(n.Mount.Provider); def == nil {
			missing = append(missing, n.Mount.Provider)
		}
	}
	sort.Strings(missing)
	return missing
}

// --- .tar.gz bundles ---

type tarEntry struct {
	name string
	mode int64
	data []byte
}

func writeTarGz(w io.Writer, files []tarEntry) error {
	sort.Slice(files, func(i, j int) bool { return files[i].name < files[j].name })
	gz := gzip.NewWriter(w)
	tw := tar.NewWriter(gz)
	for _, f := range files {
		if err := tw.WriteHeader(&tar.Header{Name: f.name, Mode: f.mode, Size: int64(len(f.data))}); err != nil {
			return fmt.Errorf("write tar header %q: %w", f.name, err)
		}
		if _, err := tw.Write(f.data); err != nil {
			return fmt.Errorf("write tar entry %q: %w", f.name, err)
		}
	}
	if err := tw.Close(); err != nil {
		return err
	}
	return gz.Close()
}

// readTarGz decodes a .tar.gz bundle into name -> content. Only regular
// files are kept; unsafe paths (absolute, escaping) are rejected.
func readTarGz(r io.Reader) (map[string][]byte, error) {
	gz, err := gzip.NewReader(r)
	if err != nil {
		return nil, fmt.Errorf("body is not a .tar.gz bundle: %w", err)
	}
	defer func() { _ = gz.Close() }()
	tr := tar.NewReader(gz)
	files := map[string][]byte{}
	for {
		hdr, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, fmt.Errorf("read bundle: %w", err)
		}
		if hdr.Typeflag != tar.TypeReg {
			continue
		}
		name := path.Clean(strings.TrimPrefix(hdr.Name, "./"))
		if name == "." || name == ".." || strings.HasPrefix(name, "../") || path.IsAbs(name) {
			return nil, fmt.Errorf("bundle contains unsafe path %q", hdr.Name)
		}
		data, err := io.ReadAll(io.LimitReader(tr, 16<<20))
		if err != nil {
			return nil, fmt.Errorf("read bundle entry %q: %w", name, err)
		}
		files[name] = data
	}
	return files, nil
}

// cleanRelPath reports whether s is a relative, clean slash path that stays
// inside its base directory.
func cleanRelPath(s string) bool {
	return s != "" && !path.IsAbs(s) && path.Clean(s) == s && s != ".." && !strings.HasPrefix(s, "../")
}
