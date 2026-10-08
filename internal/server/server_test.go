package server_test

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"encoding/json"
	"io"
	"io/fs"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/bunnyzr/pushrun/internal/config"
	"github.com/bunnyzr/pushrun/internal/gitx"
	"github.com/bunnyzr/pushrun/internal/instance"
	"github.com/bunnyzr/pushrun/internal/project"
	"github.com/bunnyzr/pushrun/internal/provider"
	"github.com/bunnyzr/pushrun/internal/run"
	"github.com/bunnyzr/pushrun/internal/server"
)

const testToken = "test-token-0123456789abcdef"

// newTestHandler builds the full handler stack over a fresh temp data root.
func newTestHandler(t *testing.T) (http.Handler, config.Paths) {
	t.Helper()
	return newTestHandlerWith(t, nil)
}

// newTestHandlerWith is newTestHandler with web console assets injected as
// Deps.Web (nil keeps the embedded placeholder).
func newTestHandlerWith(t *testing.T, webFS fs.FS) (http.Handler, config.Paths) {
	t.Helper()
	root := t.TempDir()
	paths := config.NewPaths(root)
	cfg := &config.Config{
		HTTP:     config.HTTPConfig{Bind: "127.0.0.1", Port: 0},
		PortPool: config.PortPoolConfig{From: 25000, To: 25100},
		Auth:     config.AuthConfig{Enabled: true},
		Log:      config.LogConfig{Level: "debug"},
	}
	exec := provider.NewExecutor(paths, cfg)
	pool := instance.NewPortPool(paths, cfg.PortPool.From, cfg.PortPool.To)
	mgr := run.NewManager(paths, cfg, exec, pool, nil)
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	return server.New(paths, cfg, server.Deps{
		Manager:  mgr,
		Executor: exec,
		Pool:     pool,
		Token:    testToken,
		Version:  "test-version",
		Logger:   logger,
		Web:      webFS,
	}), paths
}

// newTestServer boots the full handler stack over a fresh temp data root.
func newTestServer(t *testing.T) (*httptest.Server, config.Paths) {
	t.Helper()
	h, paths := newTestHandler(t)
	srv := httptest.NewServer(h)
	t.Cleanup(srv.Close)
	return srv, paths
}

// doReq performs one API call. A nil body sends no body; a []byte body is
// sent raw; anything else is JSON-encoded. An empty token sends no
// Authorization header.
func doReq(t *testing.T, srv *httptest.Server, method, path, token string, body any) (int, http.Header, []byte) {
	t.Helper()
	var rdr io.Reader
	switch b := body.(type) {
	case nil:
	case []byte:
		rdr = bytes.NewReader(b)
	default:
		data, err := json.Marshal(body)
		if err != nil {
			t.Fatalf("marshal body: %v", err)
		}
		rdr = bytes.NewReader(data)
	}
	req, err := http.NewRequest(method, srv.URL+path, rdr)
	if err != nil {
		t.Fatalf("build request: %v", err)
	}
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	resp, err := srv.Client().Do(req)
	if err != nil {
		t.Fatalf("%s %s: %v", method, path, err)
	}
	defer func() { _ = resp.Body.Close() }()
	data, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("read body: %v", err)
	}
	return resp.StatusCode, resp.Header, data
}

func decodeJSON(t *testing.T, data []byte) map[string]any {
	t.Helper()
	var m map[string]any
	if err := json.Unmarshal(data, &m); err != nil {
		t.Fatalf("decode JSON %q: %v", data, err)
	}
	return m
}

// decodeErr unwraps the standard error envelope.
func decodeErr(t *testing.T, data []byte) (code, message, requestID string) {
	t.Helper()
	m := decodeJSON(t, data)
	e, ok := m["error"].(map[string]any)
	if !ok {
		t.Fatalf("body %s has no error object", data)
	}
	code, _ = e["code"].(string)
	message, _ = e["message"].(string)
	requestID, _ = e["request_id"].(string)
	return code, message, requestID
}

// fixtureProviderPayload is a script provider with a required warmup param
// and a secret install param.
func fixtureProviderPayload() map[string]any {
	return map[string]any{
		"id":      "fixture",
		"name":    "Fixture Provider",
		"warmup":  "scripts/warmup.sh",
		"install": "scripts/install.sh",
		"parameters": []any{
			map[string]any{"id": "version", "type": "string", "scope": "warmup", "required": true},
			map[string]any{"id": "token", "type": "string", "scope": "install", "secret": true},
		},
		"scripts": map[string]string{
			"scripts/warmup.sh": "#!/bin/bash\nset -euo pipefail\necho warmed >> \"$PUSHRUN_PROVIDER_CACHE_DIR/warmed.txt\"\n",
			"scripts/install.sh": "#!/bin/bash\nset -euo pipefail\nmkdir -p \"$PUSHRUN_PROVIDER_TARGET_DIR\"\n" +
				"echo installed > \"$PUSHRUN_PROVIDER_TARGET_DIR/installed.txt\"\n",
		},
	}
}

func createFixtureProvider(t *testing.T, srv *httptest.Server) {
	t.Helper()
	status, _, body := doReq(t, srv, http.MethodPost, "/api/v1/providers", testToken, fixtureProviderPayload())
	if status != http.StatusCreated {
		t.Fatalf("create fixture provider: status %d, body %s", status, body)
	}
}

func fixtureProjectPayload(name string) map[string]any {
	return map[string]any{
		"name":         name,
		"display_name": "Demo",
		"tree": []any{
			map[string]any{
				"path": "app",
				"mount": map[string]any{
					"provider": "fixture",
					"params":   map[string]string{"version": "1.0", "token": "hunter2"},
				},
			},
		},
		"pipeline": []any{map[string]any{"name": "build", "run": "echo hi"}},
	}
}

func TestVersionEndpoint(t *testing.T) {
	srv, _ := newTestServer(t)

	// The version endpoint answers without a token so the web console can
	// learn whether auth is enabled before it has one.
	status, hdr, body := doReq(t, srv, http.MethodGet, "/api/v1/version", "", nil)
	if status != http.StatusOK {
		t.Fatalf("no token: status %d, body %s", status, body)
	}
	m := decodeJSON(t, body)
	if m["version"] != "test-version" {
		t.Fatalf("version = %v, want test-version", m["version"])
	}
	auth, ok := m["auth"].(map[string]any)
	if !ok || auth["enabled"] != true {
		t.Fatalf("auth = %v, want {\"enabled\": true}", m["auth"])
	}
	if hdr.Get("X-Request-Id") == "" {
		t.Fatal("missing X-Request-Id header")
	}

	// Every other /api/v1 route stays token-guarded.
	status, _, _ = doReq(t, srv, http.MethodGet, "/api/v1/projects", "", nil)
	if status != http.StatusUnauthorized {
		t.Fatalf("projects without token: status %d, want 401", status)
	}
}

func TestAuthRequired(t *testing.T) {
	srv, _ := newTestServer(t)

	// The version endpoint is exempt; everything else under /api/v1 needs
	// the token.
	status, hdr, body := doReq(t, srv, http.MethodGet, "/api/v1/projects", "", nil)
	if status != http.StatusUnauthorized {
		t.Fatalf("no token: status %d, want 401", status)
	}
	code, _, requestID := decodeErr(t, body)
	if code != "unauthorized" {
		t.Fatalf("code = %q, want unauthorized", code)
	}
	if requestID == "" || requestID != hdr.Get("X-Request-Id") {
		t.Fatalf("request_id %q does not match header %q", requestID, hdr.Get("X-Request-Id"))
	}

	status, _, _ = doReq(t, srv, http.MethodGet, "/api/v1/projects", "wrong-token", nil)
	if status != http.StatusUnauthorized {
		t.Fatalf("wrong token: status %d, want 401", status)
	}

	status, _, _ = doReq(t, srv, http.MethodGet, "/api/v1/projects", testToken, nil)
	if status != http.StatusOK {
		t.Fatalf("good token: status %d, want 200", status)
	}

	// The web placeholder is not behind the token.
	status, _, _ = doReq(t, srv, http.MethodGet, "/", "", nil)
	if status != http.StatusOK {
		t.Fatalf("GET /: status %d, want 200", status)
	}
}

func TestProjectsCRUD(t *testing.T) {
	srv, _ := newTestServer(t)

	status, _, body := doReq(t, srv, http.MethodGet, "/api/v1/projects", testToken, nil)
	if status != http.StatusOK {
		t.Fatalf("list: status %d, body %s", status, body)
	}
	if m := decodeJSON(t, body); len(m["projects"].([]any)) != 0 {
		t.Fatalf("expected empty project list, got %s", body)
	}

	status, _, body = doReq(t, srv, http.MethodPost, "/api/v1/projects", testToken, fixtureProjectPayload("demo"))
	if status != http.StatusCreated {
		t.Fatalf("create: status %d, body %s", status, body)
	}

	status, _, body = doReq(t, srv, http.MethodGet, "/api/v1/projects/demo", testToken, nil)
	if status != http.StatusOK {
		t.Fatalf("get: status %d, body %s", status, body)
	}
	if m := decodeJSON(t, body); m["name"] != "demo" || m["display_name"] != "Demo" {
		t.Fatalf("unexpected project: %s", body)
	}

	updated := fixtureProjectPayload("demo")
	updated["display_name"] = "Renamed"
	status, _, body = doReq(t, srv, http.MethodPut, "/api/v1/projects/demo", testToken, updated)
	if status != http.StatusOK {
		t.Fatalf("update: status %d, body %s", status, body)
	}
	_, _, body = doReq(t, srv, http.MethodGet, "/api/v1/projects/demo", testToken, nil)
	if m := decodeJSON(t, body); m["display_name"] != "Renamed" {
		t.Fatalf("update not applied: %s", body)
	}

	status, _, body = doReq(t, srv, http.MethodDelete, "/api/v1/projects/demo", testToken, nil)
	if status != http.StatusOK {
		t.Fatalf("delete: status %d, body %s", status, body)
	}
	status, _, body = doReq(t, srv, http.MethodGet, "/api/v1/projects/demo", testToken, nil)
	if status != http.StatusNotFound {
		t.Fatalf("get after delete: status %d, body %s", status, body)
	}
	code, _, _ := decodeErr(t, body)
	if code != "not_found" {
		t.Fatalf("code = %q, want not_found", code)
	}
}

func TestProvidersCRUD(t *testing.T) {
	srv, _ := newTestServer(t)
	createFixtureProvider(t, srv)

	status, _, body := doReq(t, srv, http.MethodGet, "/api/v1/providers/fixture", testToken, nil)
	if status != http.StatusOK {
		t.Fatalf("get: status %d, body %s", status, body)
	}
	m := decodeJSON(t, body)
	if m["id"] != "fixture" || m["name"] != "Fixture Provider" {
		t.Fatalf("unexpected provider: %s", body)
	}
	if b, _ := m["builtin"].(bool); b {
		t.Fatalf("fixture must not be flagged builtin: %s", body)
	}

	status, _, body = doReq(t, srv, http.MethodGet, "/api/v1/providers", testToken, nil)
	if status != http.StatusOK {
		t.Fatalf("list: status %d, body %s", status, body)
	}
	ids := map[string]bool{}
	for _, p := range decodeJSON(t, body)["providers"].([]any) {
		ids[p.(map[string]any)["id"].(string)] = true
	}
	if !ids["fixture"] {
		t.Fatalf("list missing fixture: %s", body)
	}

	updated := fixtureProviderPayload()
	updated["name"] = "Renamed Provider"
	status, _, body = doReq(t, srv, http.MethodPut, "/api/v1/providers/fixture", testToken, updated)
	if status != http.StatusOK {
		t.Fatalf("update: status %d, body %s", status, body)
	}
	_, _, body = doReq(t, srv, http.MethodGet, "/api/v1/providers/fixture", testToken, nil)
	if m := decodeJSON(t, body); m["name"] != "Renamed Provider" {
		t.Fatalf("update not applied: %s", body)
	}

	status, _, body = doReq(t, srv, http.MethodDelete, "/api/v1/providers/fixture", testToken, nil)
	if status != http.StatusOK {
		t.Fatalf("delete: status %d, body %s", status, body)
	}
	status, _, _ = doReq(t, srv, http.MethodGet, "/api/v1/providers/fixture", testToken, nil)
	if status != http.StatusNotFound {
		t.Fatalf("get after delete: status %d, want 404", status)
	}
}

func TestBuiltinProviderIDsReserved(t *testing.T) {
	srv, _ := newTestServer(t)

	status, _, body := doReq(t, srv, http.MethodGet, "/api/v1/providers/git", testToken, nil)
	if status != http.StatusOK {
		t.Fatalf("get builtin: status %d, body %s", status, body)
	}
	if m := decodeJSON(t, body); m["builtin"] != true {
		t.Fatalf("builtin flag missing: %s", body)
	}

	for _, id := range []string{"git", "symlink"} {
		payload := map[string]any{"id": id, "name": "evil", "install": "scripts/install.sh",
			"scripts": map[string]string{"scripts/install.sh": "true"}}
		status, _, body = doReq(t, srv, http.MethodPost, "/api/v1/providers", testToken, payload)
		if status != http.StatusConflict {
			t.Fatalf("create %s: status %d, want 409, body %s", id, status, body)
		}
		status, _, body = doReq(t, srv, http.MethodPut, "/api/v1/providers/"+id, testToken, payload)
		if status != http.StatusConflict {
			t.Fatalf("update %s: status %d, want 409, body %s", id, status, body)
		}
		status, _, body = doReq(t, srv, http.MethodDelete, "/api/v1/providers/"+id, testToken, nil)
		if status != http.StatusConflict {
			t.Fatalf("delete %s: status %d, want 409, body %s", id, status, body)
		}
	}
}

func TestSecretParamsMasked(t *testing.T) {
	srv, paths := newTestServer(t)
	createFixtureProvider(t, srv)

	status, _, body := doReq(t, srv, http.MethodPost, "/api/v1/projects", testToken, fixtureProjectPayload("demo"))
	if status != http.StatusCreated {
		t.Fatalf("create project: status %d, body %s", status, body)
	}

	for _, url := range []string{"/api/v1/projects/demo", "/api/v1/projects"} {
		status, hdr, body := doReq(t, srv, http.MethodGet, url, testToken, nil)
		if status != http.StatusOK {
			t.Fatalf("GET %s: status %d", url, status)
		}
		if bytes.Contains(body, []byte("hunter2")) {
			t.Fatalf("GET %s leaks secret param value: %s", url, body)
		}
		if !bytes.Contains(body, []byte(`"***"`)) {
			t.Fatalf("GET %s does not mask secret param: %s", url, body)
		}
		if hdr.Get("X-Request-Id") == "" {
			t.Fatalf("GET %s: missing X-Request-Id", url)
		}
	}

	// The real value stays on disk.
	raw, err := os.ReadFile(filepath.Join(paths.Projects, "demo.yaml"))
	if err != nil {
		t.Fatalf("read project file: %v", err)
	}
	if !strings.Contains(string(raw), "hunter2") {
		t.Fatalf("stored project lost the secret value:\n%s", raw)
	}
}

// A client that round-trips a masked GET body through PUT must not clobber
// the stored secret with the "***" placeholder.
func TestSecretPreservedOnPut(t *testing.T) {
	srv, paths := newTestServer(t)
	createFixtureProvider(t, srv)
	status, _, body := doReq(t, srv, http.MethodPost, "/api/v1/projects", testToken, fixtureProjectPayload("demo"))
	if status != http.StatusCreated {
		t.Fatalf("create project: status %d, body %s", status, body)
	}

	_, _, masked := doReq(t, srv, http.MethodGet, "/api/v1/projects/demo", testToken, nil)
	var proj map[string]any
	if err := json.Unmarshal(masked, &proj); err != nil {
		t.Fatalf("decode masked project: %v", err)
	}
	status, _, body = doReq(t, srv, http.MethodPut, "/api/v1/projects/demo", testToken, proj)
	if status != http.StatusOK {
		t.Fatalf("put masked project: status %d, body %s", status, body)
	}

	raw, err := os.ReadFile(filepath.Join(paths.Projects, "demo.yaml"))
	if err != nil {
		t.Fatalf("read project file: %v", err)
	}
	if !strings.Contains(string(raw), "hunter2") {
		t.Fatalf("PUT with masked value clobbered the secret:\n%s", raw)
	}
}

// Renaming or moving a mounted node changes its path, so a masked "***" can
// no longer be matched to the stored secret. The PUT must be rejected with
// 400 secret_params_orphaned — never silently drop the param, and never save
// the literal "***".
func TestSecretOrphanedOnNodeRename(t *testing.T) {
	srv, paths := newTestServer(t)
	createFixtureProvider(t, srv)
	status, _, body := doReq(t, srv, http.MethodPost, "/api/v1/projects", testToken, fixtureProjectPayload("demo"))
	if status != http.StatusCreated {
		t.Fatalf("create project: status %d, body %s", status, body)
	}

	raw := func() string {
		t.Helper()
		data, err := os.ReadFile(filepath.Join(paths.Projects, "demo.yaml"))
		if err != nil {
			t.Fatalf("read project file: %v", err)
		}
		return string(data)
	}

	// A plain round-trip of the masked body still restores the secret.
	_, _, masked := doReq(t, srv, http.MethodGet, "/api/v1/projects/demo", testToken, nil)
	var proj map[string]any
	if err := json.Unmarshal(masked, &proj); err != nil {
		t.Fatalf("decode masked project: %v", err)
	}
	status, _, body = doReq(t, srv, http.MethodPut, "/api/v1/projects/demo", testToken, proj)
	if status != http.StatusOK {
		t.Fatalf("put masked project unchanged: status %d, body %s", status, body)
	}
	if got := raw(); !strings.Contains(got, "hunter2") {
		t.Fatalf("plain masked PUT clobbered the secret:\n%s", got)
	}

	// Renaming the mounted node orphans the masked secret: 400, and the
	// stored file is untouched.
	tree := proj["tree"].([]any)
	node := tree[0].(map[string]any)
	node["path"] = "svc"
	status, _, body = doReq(t, srv, http.MethodPut, "/api/v1/projects/demo", testToken, proj)
	if status != http.StatusBadRequest {
		t.Fatalf("put renamed masked project: status %d, want 400, body %s", status, body)
	}
	code, message, _ := decodeErr(t, body)
	if code != "secret_params_orphaned" {
		t.Fatalf("code = %q, want secret_params_orphaned, body %s", code, body)
	}
	if !strings.Contains(message, "svc") || !strings.Contains(message, "token") {
		t.Fatalf("message %q must name the node path and param", message)
	}
	if got := raw(); !strings.Contains(got, "hunter2") || !strings.Contains(got, "path: app") {
		t.Fatalf("rejected PUT still modified the stored project:\n%s", got)
	}

	// Re-entering the real value on the renamed node saves fine.
	mount := node["mount"].(map[string]any)
	mount["params"].(map[string]any)["token"] = "hunter3"
	status, _, body = doReq(t, srv, http.MethodPut, "/api/v1/projects/demo", testToken, proj)
	if status != http.StatusOK {
		t.Fatalf("put renamed project with real secret: status %d, body %s", status, body)
	}
	if got := raw(); !strings.Contains(got, "hunter3") || !strings.Contains(got, "path: svc") {
		t.Fatalf("renamed project not saved with the re-entered secret:\n%s", got)
	}
}

func TestProviderDeleteReferenced(t *testing.T) {
	srv, _ := newTestServer(t)
	createFixtureProvider(t, srv)
	status, _, body := doReq(t, srv, http.MethodPost, "/api/v1/projects", testToken, fixtureProjectPayload("demo"))
	if status != http.StatusCreated {
		t.Fatalf("create project: status %d, body %s", status, body)
	}

	status, _, body = doReq(t, srv, http.MethodDelete, "/api/v1/providers/fixture", testToken, nil)
	if status != http.StatusConflict {
		t.Fatalf("delete referenced provider: status %d, want 409, body %s", status, body)
	}
	code, _, _ := decodeErr(t, body)
	if code != "conflict" {
		t.Fatalf("code = %q, want conflict", code)
	}
	m := decodeJSON(t, body)
	refs, ok := m["referenced_by"].([]any)
	if !ok || len(refs) != 1 || refs[0] != "demo" {
		t.Fatalf("referenced_by = %v, want [demo], body %s", m["referenced_by"], body)
	}

	// Once unreferenced, the delete goes through.
	status, _, _ = doReq(t, srv, http.MethodDelete, "/api/v1/projects/demo", testToken, nil)
	if status != http.StatusOK {
		t.Fatalf("delete project: status %d", status)
	}
	status, _, body = doReq(t, srv, http.MethodDelete, "/api/v1/providers/fixture", testToken, nil)
	if status != http.StatusOK {
		t.Fatalf("delete unreferenced provider: status %d, body %s", status, body)
	}
}

// Deleting a project never stops or removes its instances.
func TestProjectDeleteKeepsInstances(t *testing.T) {
	srv, paths := newTestServer(t)
	createFixtureProvider(t, srv)
	status, _, body := doReq(t, srv, http.MethodPost, "/api/v1/projects", testToken, fixtureProjectPayload("demo"))
	if status != http.StatusCreated {
		t.Fatalf("create project: status %d, body %s", status, body)
	}

	st := &instance.State{Project: "demo", Instance: "default", Status: instance.StatusRunning, Port: 25000}
	if err := instance.SaveState(paths, st); err != nil {
		t.Fatalf("seed instance state: %v", err)
	}

	status, _, body = doReq(t, srv, http.MethodDelete, "/api/v1/projects/demo", testToken, nil)
	if status != http.StatusOK {
		t.Fatalf("delete project: status %d, body %s", status, body)
	}

	got, err := instance.LoadState(paths, "demo", "default")
	if err != nil {
		t.Fatalf("instance state removed by project delete: %v", err)
	}
	if got.Status != instance.StatusRunning || got.Port != 25000 {
		t.Fatalf("instance state touched by project delete: %+v", got)
	}
}

func TestWarmupProvider(t *testing.T) {
	srv, paths := newTestServer(t)
	createFixtureProvider(t, srv)

	warm := func() (int, []byte) {
		status, _, body := doReq(t, srv, http.MethodPost, "/api/v1/providers/fixture/warmup", testToken,
			map[string]any{"params": map[string]string{"version": "1.0"}})
		return status, body
	}

	status, body := warm()
	if status != http.StatusOK {
		t.Fatalf("warmup: status %d, body %s", status, body)
	}
	if m := decodeJSON(t, body); m["status"] != "SUCCESS" {
		t.Fatalf("warmup status = %v, body %s", m["status"], body)
	}

	// The executor ran the script and left a ready marker.
	entries, err := os.ReadDir(paths.ProviderData)
	if err != nil {
		t.Fatalf("read provider-data: %v", err)
	}
	var cacheDir string
	for _, e := range entries {
		if e.IsDir() && strings.HasPrefix(e.Name(), "fixture-") {
			cacheDir = filepath.Join(paths.ProviderData, e.Name())
		}
	}
	if cacheDir == "" {
		t.Fatal("no fixture cache dir under provider-data")
	}
	if _, err := os.Stat(filepath.Join(cacheDir, ".pushrun-ready")); err != nil {
		t.Fatalf("ready marker missing: %v", err)
	}
	marker, _ := os.ReadFile(filepath.Join(cacheDir, "warmed.txt"))
	if got := strings.Count(string(marker), "warmed"); got != 1 {
		t.Fatalf("warmed.txt has %d warmup lines, want 1", got)
	}

	// A warm provider skips the script.
	status, body = warm()
	if status != http.StatusOK {
		t.Fatalf("second warmup: status %d, body %s", status, body)
	}
	marker, _ = os.ReadFile(filepath.Join(cacheDir, "warmed.txt"))
	if got := strings.Count(string(marker), "warmed"); got != 1 {
		t.Fatalf("warm provider re-ran the script: %d warmup lines", got)
	}
}

func TestWarmupProject(t *testing.T) {
	srv, _ := newTestServer(t)
	createFixtureProvider(t, srv)
	status, _, body := doReq(t, srv, http.MethodPost, "/api/v1/projects", testToken, fixtureProjectPayload("demo"))
	if status != http.StatusCreated {
		t.Fatalf("create project: status %d, body %s", status, body)
	}

	status, _, body = doReq(t, srv, http.MethodPost, "/api/v1/projects/demo/warmup", testToken, nil)
	if status != http.StatusOK {
		t.Fatalf("warmup: status %d, body %s", status, body)
	}
	m := decodeJSON(t, body)
	if m["status"] != "SUCCESS" {
		t.Fatalf("overall status = %v, body %s", m["status"], body)
	}
	results, ok := m["results"].([]any)
	if !ok || len(results) != 1 {
		t.Fatalf("results = %v, want 1 entry", m["results"])
	}
	r0 := results[0].(map[string]any)
	if r0["provider"] != "fixture" || r0["status"] != "SUCCESS" {
		t.Fatalf("unexpected per-provider status: %v", r0)
	}

	// A project referencing a missing provider reports FAILED per provider.
	broken := fixtureProjectPayload("broken")
	broken["tree"] = []any{map[string]any{
		"path":  "app",
		"mount": map[string]any{"provider": "nope", "params": map[string]string{}},
	}}
	status, _, body = doReq(t, srv, http.MethodPost, "/api/v1/projects", testToken, broken)
	if status != http.StatusCreated {
		t.Fatalf("create broken project: status %d, body %s", status, body)
	}
	status, _, body = doReq(t, srv, http.MethodPost, "/api/v1/projects/broken/warmup", testToken, nil)
	if status != http.StatusOK {
		t.Fatalf("warmup broken: status %d, body %s", status, body)
	}
	m = decodeJSON(t, body)
	if m["status"] != "FAILED" {
		t.Fatalf("overall status = %v, want FAILED", m["status"])
	}
	r0 = m["results"].([]any)[0].(map[string]any)
	if r0["provider"] != "nope" || r0["status"] != "FAILED" || r0["error"] == nil {
		t.Fatalf("unexpected per-provider failure: %v", r0)
	}
}

// Two mounts of one provider with distinct warmup params are distinct caches:
// the project warmup must warm each once, like the run engine does.
func TestWarmupProjectWarmsDistinctParamSets(t *testing.T) {
	srv, paths := newTestServer(t)
	createFixtureProvider(t, srv)
	proj := fixtureProjectPayload("demo")
	proj["tree"] = []any{
		map[string]any{
			"path":  "one",
			"mount": map[string]any{"provider": "fixture", "params": map[string]string{"version": "1.0", "token": "a"}},
		},
		map[string]any{
			"path":  "two",
			"mount": map[string]any{"provider": "fixture", "params": map[string]string{"version": "2.0", "token": "b"}},
		},
		map[string]any{
			// Same warmup params as "one" (install-scoped token differs only):
			// deduped away, warmed by the first mount already.
			"path":  "three",
			"mount": map[string]any{"provider": "fixture", "params": map[string]string{"version": "1.0", "token": "c"}},
		},
	}
	status, _, body := doReq(t, srv, http.MethodPost, "/api/v1/projects", testToken, proj)
	if status != http.StatusCreated {
		t.Fatalf("create project: status %d, body %s", status, body)
	}

	status, _, body = doReq(t, srv, http.MethodPost, "/api/v1/projects/demo/warmup", testToken, nil)
	if status != http.StatusOK {
		t.Fatalf("warmup: status %d, body %s", status, body)
	}
	m := decodeJSON(t, body)
	if m["status"] != "SUCCESS" {
		t.Fatalf("overall status = %v, body %s", m["status"], body)
	}
	if results, ok := m["results"].([]any); !ok || len(results) != 2 {
		t.Fatalf("results = %v, want 2 entries (one per distinct warmup param set)", m["results"])
	}
	entries, err := os.ReadDir(paths.ProviderData)
	if err != nil {
		t.Fatalf("read provider-data: %v", err)
	}
	cacheDirs := 0
	for _, e := range entries {
		if e.IsDir() && strings.HasPrefix(e.Name(), "fixture-") {
			cacheDirs++
			if _, err := os.Stat(filepath.Join(paths.ProviderData, e.Name(), ".pushrun-ready")); err != nil {
				t.Fatalf("ready marker missing in %s: %v", e.Name(), err)
			}
		}
	}
	if cacheDirs != 2 {
		t.Fatalf("%d fixture cache dirs, want 2 (one per warmup param set)", cacheDirs)
	}
}

func TestProjectWarmupStatus(t *testing.T) {
	srv, _ := newTestServer(t)

	src := t.TempDir()
	if err := os.WriteFile(filepath.Join(src, "index.txt"), []byte("hello"), 0o644); err != nil {
		t.Fatal(err)
	}
	proj := map[string]any{
		"name": "demo",
		"tree": []any{map[string]any{
			"path":  "app",
			"mount": map[string]any{"provider": "symlink", "params": map[string]string{"source": src}},
		}},
		"pipeline": []any{map[string]any{"name": "build", "run": "echo hi"}},
	}
	status, _, body := doReq(t, srv, http.MethodPost, "/api/v1/projects", testToken, proj)
	if status != http.StatusCreated {
		t.Fatalf("create project: status %d, body %s", status, body)
	}

	getMount := func() map[string]any {
		t.Helper()
		status, _, body := doReq(t, srv, http.MethodGet, "/api/v1/projects/demo/warmup", testToken, nil)
		if status != http.StatusOK {
			t.Fatalf("warmup status: status %d, body %s", status, body)
		}
		mounts, ok := decodeJSON(t, body)["mounts"].([]any)
		if !ok || len(mounts) != 1 {
			t.Fatalf("mounts = %v, want 1 entry, body %s", mounts, body)
		}
		return mounts[0].(map[string]any)
	}

	// Before any warmup the mount is cold and carries no warmed_at.
	m0 := getMount()
	if m0["path"] != "app" || m0["provider"] != "symlink" {
		t.Fatalf("unexpected mount entry: %v", m0)
	}
	if m0["status"] != "cold" {
		t.Fatalf("status = %v, want cold", m0["status"])
	}
	if _, ok := m0["warmed_at"]; ok {
		t.Fatalf("cold mount carries warmed_at: %v", m0)
	}

	// The POST trigger keeps its semantics.
	status, _, body = doReq(t, srv, http.MethodPost, "/api/v1/projects/demo/warmup", testToken, nil)
	if status != http.StatusOK {
		t.Fatalf("warmup: status %d, body %s", status, body)
	}
	if m := decodeJSON(t, body); m["status"] != "SUCCESS" {
		t.Fatalf("warmup status = %v, body %s", m["status"], body)
	}

	// After the warmup the mount is warm with a parseable warmed_at.
	m0 = getMount()
	if m0["status"] != "warm" {
		t.Fatalf("status = %v, want warm", m0["status"])
	}
	warmedAt, ok := m0["warmed_at"].(string)
	if !ok || warmedAt == "" {
		t.Fatalf("warm mount missing warmed_at: %v", m0)
	}
	if _, err := time.Parse(time.RFC3339, warmedAt); err != nil {
		t.Fatalf("warmed_at %q not RFC3339: %v", warmedAt, err)
	}
}

// The git builtin owns no ProviderData cache dir, so Warmups("git") is always
// empty; its per-mount status comes from the bare repo's snapshot ref
// refs/pushrun/for/<branch> instead.
func TestProjectWarmupStatusGitMount(t *testing.T) {
	srv, paths := newTestServer(t)
	createGitProject(t, srv, "demo", "git.example.com/team/demo", "echo hi")

	getStatus := func() string {
		t.Helper()
		status, _, body := doReq(t, srv, http.MethodGet, "/api/v1/projects/demo/warmup", testToken, nil)
		if status != http.StatusOK {
			t.Fatalf("warmup status: status %d, body %s", status, body)
		}
		mounts, ok := decodeJSON(t, body)["mounts"].([]any)
		if !ok || len(mounts) != 1 {
			t.Fatalf("mounts = %v, want 1 entry, body %s", mounts, body)
		}
		m0 := mounts[0].(map[string]any)
		if m0["path"] != "app" || m0["provider"] != "git" {
			t.Fatalf("unexpected mount entry: %v", m0)
		}
		s, _ := m0["status"].(string)
		return s
	}

	if got := getStatus(); got != "cold" {
		t.Fatalf("status = %q, want cold (no snapshot ref yet)", got)
	}

	// Seed the snapshot ref the way a warmup (or a push) would.
	src, _ := newPushFixtureRepo(t)
	repoDir := bareDir(t, paths, "git.example.com/team/demo")
	if err := gitx.InitBareRepo(repoDir); err != nil {
		t.Fatal(err)
	}
	if err := gitx.FetchSnapshot(repoDir, src, "main"); err != nil {
		t.Fatal(err)
	}

	if got := getStatus(); got != "warm" {
		t.Fatalf("status = %q, want warm (snapshot ref exists)", got)
	}
}

// readTarGzBundle decodes an exported bundle into name -> content.
func readTarGzBundle(t *testing.T, data []byte) map[string][]byte {
	t.Helper()
	gz, err := gzip.NewReader(bytes.NewReader(data))
	if err != nil {
		t.Fatalf("bundle is not gzip: %v", err)
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
			t.Fatalf("read tar: %v", err)
		}
		if hdr.Typeflag != tar.TypeReg {
			continue
		}
		content, err := io.ReadAll(tr)
		if err != nil {
			t.Fatalf("read %s: %v", hdr.Name, err)
		}
		files[hdr.Name] = content
	}
	return files
}

func TestProviderExportImportRoundTrip(t *testing.T) {
	srv, _ := newTestServer(t)
	createFixtureProvider(t, srv)

	status, hdr, bundle := doReq(t, srv, http.MethodGet, "/api/v1/providers/fixture/export", testToken, nil)
	if status != http.StatusOK {
		t.Fatalf("export: status %d, body %s", status, bundle)
	}
	if ct := hdr.Get("Content-Type"); !strings.Contains(ct, "gzip") {
		t.Fatalf("Content-Type = %q, want gzip", ct)
	}
	files := readTarGzBundle(t, bundle)
	for _, name := range []string{"provider.yaml", "scripts/warmup.sh", "scripts/install.sh"} {
		if _, ok := files[name]; !ok {
			t.Fatalf("bundle missing %s; entries: %v", name, files)
		}
	}

	// Import into a fresh root reproduces the definition.
	srv2, paths2 := newTestServer(t)
	status, _, body := doReq(t, srv2, http.MethodPost, "/api/v1/providers/import", testToken, bundle)
	if status != http.StatusCreated && status != http.StatusOK {
		t.Fatalf("import: status %d, body %s", status, body)
	}
	status, _, body = doReq(t, srv2, http.MethodGet, "/api/v1/providers/fixture", testToken, nil)
	if status != http.StatusOK {
		t.Fatalf("get imported: status %d, body %s", status, body)
	}
	m := decodeJSON(t, body)
	if m["id"] != "fixture" || m["name"] != "Fixture Provider" || m["warmup"] != "scripts/warmup.sh" {
		t.Fatalf("imported provider mismatch: %s", body)
	}
	params := m["parameters"].([]any)
	if len(params) != 2 || params[1].(map[string]any)["secret"] != true {
		t.Fatalf("imported params mismatch: %s", body)
	}
	raw, err := os.ReadFile(filepath.Join(paths2.Providers, "fixture", "scripts", "warmup.sh"))
	if err != nil || !strings.Contains(string(raw), "warmed.txt") {
		t.Fatalf("script not restored: %v %q", err, raw)
	}
}

// packTarGz writes name -> content entries as a .tar.gz bundle.
func packTarGz(t *testing.T, files map[string][]byte) []byte {
	t.Helper()
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gz)
	for name, data := range files {
		if err := tw.WriteHeader(&tar.Header{Name: name, Mode: 0o644, Size: int64(len(data))}); err != nil {
			t.Fatal(err)
		}
		if _, err := tw.Write(data); err != nil {
			t.Fatal(err)
		}
	}
	if err := tw.Close(); err != nil {
		t.Fatal(err)
	}
	if err := gz.Close(); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

// Importing over an existing provider swaps it in place and cleans up the
// aside: the provider stays valid throughout and no staging residue remains.
func TestImportProviderOverExisting(t *testing.T) {
	srv, paths := newTestServer(t)
	createFixtureProvider(t, srv)

	status, _, bundle := doReq(t, srv, http.MethodGet, "/api/v1/providers/fixture/export", testToken, nil)
	if status != http.StatusOK {
		t.Fatalf("export: status %d", status)
	}
	files := readTarGzBundle(t, bundle)
	files["provider.yaml"] = []byte(strings.Replace(
		string(files["provider.yaml"]), "Fixture Provider", "Fixture Provider v2", 1))

	status, _, body := doReq(t, srv, http.MethodPost, "/api/v1/providers/import", testToken, packTarGz(t, files))
	if status != http.StatusCreated && status != http.StatusOK {
		t.Fatalf("import over existing: status %d, body %s", status, body)
	}

	status, _, body = doReq(t, srv, http.MethodGet, "/api/v1/providers/fixture", testToken, nil)
	if status != http.StatusOK {
		t.Fatalf("get after re-import: status %d, body %s", status, body)
	}
	if m := decodeJSON(t, body); m["name"] != "Fixture Provider v2" {
		t.Fatalf("provider after re-import = %v, want v2", m["name"])
	}

	entries, err := os.ReadDir(paths.Providers)
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range entries {
		if strings.HasPrefix(e.Name(), ".import-aside-") || strings.HasPrefix(e.Name(), ".import-") {
			t.Fatalf("import residue left behind: %s", e.Name())
		}
	}
	if _, err := os.Stat(filepath.Join(paths.Providers, "fixture", "scripts", "install.sh")); err != nil {
		t.Fatalf("install script lost in swap: %v", err)
	}
}

func TestProjectExportImportRoundTrip(t *testing.T) {
	srv, _ := newTestServer(t)
	createFixtureProvider(t, srv)
	status, _, body := doReq(t, srv, http.MethodPost, "/api/v1/projects", testToken, fixtureProjectPayload("demo"))
	if status != http.StatusCreated {
		t.Fatalf("create project: status %d, body %s", status, body)
	}
	_, _, wantMasked := doReq(t, srv, http.MethodGet, "/api/v1/projects/demo", testToken, nil)

	status, _, bundle := doReq(t, srv, http.MethodGet, "/api/v1/projects/demo/export", testToken, nil)
	if status != http.StatusOK {
		t.Fatalf("export: status %d, body %s", status, bundle)
	}
	files := readTarGzBundle(t, bundle)
	if len(files) != 1 {
		t.Fatalf("project bundle entries = %v, want exactly one yaml", files)
	}
	if _, ok := files["demo.yaml"]; !ok {
		t.Fatalf("bundle entries = %v, want demo.yaml", files)
	}
	// The bundle carries the real definition (it is the migration path).
	if !bytes.Contains(files["demo.yaml"], []byte("hunter2")) {
		t.Fatal("project bundle lost the stored params")
	}

	// Fresh root: provider bundle first, then the project.
	srv2, _ := newTestServer(t)
	status, _, pBundle := doReq(t, srv, http.MethodGet, "/api/v1/providers/fixture/export", testToken, nil)
	if status != http.StatusOK {
		t.Fatalf("export provider: status %d", status)
	}
	status, _, body = doReq(t, srv2, http.MethodPost, "/api/v1/providers/import", testToken, pBundle)
	if status != http.StatusCreated && status != http.StatusOK {
		t.Fatalf("import provider: status %d, body %s", status, body)
	}
	status, _, body = doReq(t, srv2, http.MethodPost, "/api/v1/projects/import", testToken, bundle)
	if status != http.StatusCreated && status != http.StatusOK {
		t.Fatalf("import project: status %d, body %s", status, body)
	}

	status, _, gotMasked := doReq(t, srv2, http.MethodGet, "/api/v1/projects/demo", testToken, nil)
	if status != http.StatusOK {
		t.Fatalf("get imported project: status %d, body %s", status, body)
	}
	var want, got map[string]any
	_ = json.Unmarshal(wantMasked, &want)
	_ = json.Unmarshal(gotMasked, &got)
	delete(want, "schema")
	delete(got, "schema")
	wantJSON, _ := json.Marshal(want)
	gotJSON, _ := json.Marshal(got)
	if !bytes.Equal(wantJSON, gotJSON) {
		t.Fatalf("round-trip mismatch:\nwant %s\ngot  %s", wantJSON, gotJSON)
	}
}

func TestImportProjectMissingProviders(t *testing.T) {
	srv, _ := newTestServer(t)
	createFixtureProvider(t, srv)
	status, _, body := doReq(t, srv, http.MethodPost, "/api/v1/projects", testToken, fixtureProjectPayload("demo"))
	if status != http.StatusCreated {
		t.Fatalf("create project: status %d, body %s", status, body)
	}
	_, _, bundle := doReq(t, srv, http.MethodGet, "/api/v1/projects/demo/export", testToken, nil)

	// Fresh root without the provider: import must fail listing the missing
	// ids, and must not create anything.
	srv2, _ := newTestServer(t)
	status, _, body = doReq(t, srv2, http.MethodPost, "/api/v1/projects/import", testToken, bundle)
	if status != http.StatusBadRequest {
		t.Fatalf("import: status %d, want 400, body %s", status, body)
	}
	m := decodeJSON(t, body)
	missing, ok := m["missing_providers"].([]any)
	if !ok || len(missing) != 1 || missing[0] != "fixture" {
		t.Fatalf("missing_providers = %v, want [fixture], body %s", m["missing_providers"], body)
	}
	status, _, _ = doReq(t, srv2, http.MethodGet, "/api/v1/projects/demo", testToken, nil)
	if status != http.StatusNotFound {
		t.Fatalf("partial import happened: get status %d, want 404", status)
	}
}

func TestImportProviderRejectsBuiltinID(t *testing.T) {
	srv, _ := newTestServer(t)
	createFixtureProvider(t, srv)

	// Craft a bundle whose provider.yaml claims the builtin id "git".
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gz)
	yamlBody := []byte("schema: pushrun.provider/v1\nid: git\nname: Evil\ninstall: scripts/install.sh\n")
	_ = tw.WriteHeader(&tar.Header{Name: "provider.yaml", Mode: 0o644, Size: int64(len(yamlBody))})
	_, _ = tw.Write(yamlBody)
	script := []byte("true\n")
	_ = tw.WriteHeader(&tar.Header{Name: "scripts/install.sh", Mode: 0o755, Size: int64(len(script))})
	_, _ = tw.Write(script)
	_ = tw.Close()
	_ = gz.Close()

	status, _, body := doReq(t, srv, http.MethodPost, "/api/v1/providers/import", testToken, buf.Bytes())
	if status != http.StatusConflict {
		t.Fatalf("import builtin id: status %d, want 409, body %s", status, body)
	}
}

func TestRequestIDOnError(t *testing.T) {
	srv, _ := newTestServer(t)
	status, hdr, body := doReq(t, srv, http.MethodGet, "/api/v1/projects/nope", testToken, nil)
	if status != http.StatusNotFound {
		t.Fatalf("status %d, want 404", status)
	}
	rid := hdr.Get("X-Request-Id")
	if rid == "" {
		t.Fatal("missing X-Request-Id header")
	}
	code, _, requestID := decodeErr(t, body)
	if code != "not_found" {
		t.Fatalf("code = %q, want not_found", code)
	}
	if requestID != rid {
		t.Fatalf("request_id %q != header %q", requestID, rid)
	}
}

func TestRunEndpoints(t *testing.T) {
	srv, paths := newTestServer(t)

	// A project assembled from a local directory via the builtin symlink
	// provider, with one trivial step and no background process.
	src := t.TempDir()
	if err := os.WriteFile(filepath.Join(src, "index.txt"), []byte("hello"), 0o644); err != nil {
		t.Fatal(err)
	}
	proj := map[string]any{
		"name": "demo",
		"tree": []any{map[string]any{
			"path":  "app",
			"mount": map[string]any{"provider": "symlink", "params": map[string]string{"source": src}},
		}},
		"pipeline": []any{map[string]any{"name": "build", "run": "echo hi"}},
	}
	status, _, body := doReq(t, srv, http.MethodPost, "/api/v1/projects", testToken, proj)
	if status != http.StatusCreated {
		t.Fatalf("create project: status %d, body %s", status, body)
	}

	status, _, body = doReq(t, srv, http.MethodPost, "/api/v1/instances/demo/default/run", testToken,
		map[string]any{"user": "tester"})
	if status != http.StatusOK {
		t.Fatalf("run: status %d, body %s", status, body)
	}
	m := decodeJSON(t, body)
	if m["status"] != "SUCCESS" {
		t.Fatalf("run status = %v, body %s", m["status"], body)
	}
	runID, _ := m["run_id"].(string)
	if runID == "" {
		t.Fatalf("no run_id in %s", body)
	}
	port, _ := m["port"].(float64)
	if port < 25000 || port > 25100 {
		t.Fatalf("port %v outside pool range", m["port"])
	}

	status, _, body = doReq(t, srv, http.MethodGet, "/api/v1/instances", testToken, nil)
	if status != http.StatusOK {
		t.Fatalf("list instances: status %d, body %s", status, body)
	}
	found := false
	for _, inst := range decodeJSON(t, body)["instances"].([]any) {
		im := inst.(map[string]any)
		if im["project"] == "demo" && im["instance"] == "default" {
			found = true
		}
	}
	if !found {
		t.Fatalf("instances list missing demo/default: %s", body)
	}

	status, _, body = doReq(t, srv, http.MethodGet, "/api/v1/instances/demo/default", testToken, nil)
	if status != http.StatusOK {
		t.Fatalf("get instance: status %d, body %s", status, body)
	}
	if m := decodeJSON(t, body); m["status"] != "SUCCESS" {
		t.Fatalf("instance status = %v, body %s", m["status"], body)
	}

	status, _, body = doReq(t, srv, http.MethodGet, "/api/v1/instances/demo/default/runs", testToken, nil)
	if status != http.StatusOK {
		t.Fatalf("list runs: status %d, body %s", status, body)
	}
	runs := decodeJSON(t, body)["runs"].([]any)
	if len(runs) != 1 || runs[0].(map[string]any)["id"] != runID {
		t.Fatalf("runs = %s, want exactly run %s", body, runID)
	}

	status, _, body = doReq(t, srv, http.MethodGet, "/api/v1/runs/"+runID, testToken, nil)
	if status != http.StatusOK {
		t.Fatalf("get run: status %d, body %s", status, body)
	}
	m = decodeJSON(t, body)
	if m["id"] != runID || m["status"] != "SUCCESS" || m["project"] != "demo" || m["instance"] != "default" {
		t.Fatalf("run record mismatch: %s", body)
	}
	status, _, _ = doReq(t, srv, http.MethodGet, "/api/v1/runs/20990101-000000-deadbeef", testToken, nil)
	if status != http.StatusNotFound {
		t.Fatalf("get unknown run: status %d, want 404", status)
	}

	// Invalid action.
	status, _, _ = doReq(t, srv, http.MethodPost, "/api/v1/instances/demo/default/explode", testToken, nil)
	if status != http.StatusBadRequest {
		t.Fatalf("invalid action: status %d, want 400", status)
	}

	// Deleting the instance removes it without touching the project.
	status, _, body = doReq(t, srv, http.MethodDelete, "/api/v1/instances/demo/default", testToken, nil)
	if status != http.StatusOK {
		t.Fatalf("delete instance: status %d, body %s", status, body)
	}
	status, _, _ = doReq(t, srv, http.MethodGet, "/api/v1/instances/demo/default", testToken, nil)
	if status != http.StatusNotFound {
		t.Fatalf("get deleted instance: status %d, want 404", status)
	}
	if _, err := os.Stat(filepath.Join(paths.Instances, "demo", "default")); !os.IsNotExist(err) {
		t.Fatalf("instance dir still present: %v", err)
	}
	status, _, body = doReq(t, srv, http.MethodGet, "/api/v1/projects/demo", testToken, nil)
	if status != http.StatusOK {
		t.Fatalf("project gone after instance delete: status %d, body %s", status, body)
	}
}

// When a mount's provider no longer exists, masking fails closed: every
// param of the mount is masked, since no declaration remains to tell
// secrets from plain values.
func TestMaskProjectFailClosedOnMissingProvider(t *testing.T) {
	srv, paths := newTestServer(t)

	// Write a project file directly whose mount references a provider that
	// was never registered (possible only via on-disk edits).
	if err := os.MkdirAll(paths.Projects, 0o755); err != nil {
		t.Fatal(err)
	}
	yaml := `name: demo
tree:
  - path: app
    mount:
      provider: gone
      params:
        token: cleartext-secret
        flavor: vanilla
`
	if err := os.WriteFile(filepath.Join(paths.Projects, "demo.yaml"), []byte(yaml), 0o644); err != nil {
		t.Fatal(err)
	}

	status, _, body := doReq(t, srv, http.MethodGet, "/api/v1/projects/demo", testToken, nil)
	if status != http.StatusOK {
		t.Fatalf("GET project: status %d, body %s", status, body)
	}
	if bytes.Contains(body, []byte("cleartext-secret")) || bytes.Contains(body, []byte("vanilla")) {
		t.Fatalf("missing-provider mount leaks param values: %s", body)
	}
	if got := strings.Count(string(body), `"***"`); got != 2 {
		t.Fatalf("want both params masked, got %d masked values: %s", got, body)
	}
}

// A push-triggered run (via the loopback hook endpoint) yields a CI_URL
// built from the host the pusher connected to, forwarded as X-PushRun-Host,
// not the loopback fallback.
func TestHookRunUsesPushDisplayHost(t *testing.T) {
	srv, paths := newTestServer(t)
	secret, err := config.EnsureHookSecret(paths.Root)
	if err != nil {
		t.Fatalf("EnsureHookSecret: %v", err)
	}

	// The project mounts a git repo whose identity the hook call asserts.
	src, head := newPushFixtureRepo(t)
	proj := &project.Project{
		Name: "demo",
		Tree: []project.Node{
			{Path: "app", Mount: &project.Mount{Provider: "git", Primary: true,
				Params: map[string]string{"repo": src, "branch": "main"}}},
		},
		Pipeline: []project.Step{
			{Name: "serve", Run: "sleep 300", Background: true},
		},
	}
	if err := project.Save(paths, proj); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if st, err := instance.LoadState(paths, "demo", "default"); err == nil && st.PGID > 0 {
			_ = instance.KillGroup(st.PGID)
		}
	})
	identity, err := gitx.NormalizeRepo(src)
	if err != nil {
		t.Fatal(err)
	}
	// Seed the bare repo the way a push would, so the checkout of the
	// (fixture) commit finds its objects.
	bare, err := gitx.BareRepoDir(paths, src)
	if err != nil {
		t.Fatal(err)
	}
	if err := gitx.InitBareRepo(bare); err != nil {
		t.Fatal(err)
	}
	if err := gitx.FetchSnapshot(bare, src, "main"); err != nil {
		t.Fatal(err)
	}

	req, err := http.NewRequest(http.MethodPost, srv.URL+"/internal/v1/hooks/post-receive", nil)
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("X-PushRun-Hook-Secret", secret)
	req.Header.Set("X-PushRun-Repo", identity)
	req.Header.Set("X-PushRun-Branch", "main")
	req.Header.Set("X-PushRun-Commit", head)
	req.Header.Set("X-PushRun-Host", "ci.example.com:8000")
	resp, err := srv.Client().Do(req)
	if err != nil {
		t.Fatalf("hook call: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatal(err)
	}
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("hook call: status %d, body %s", resp.StatusCode, body)
	}
	out := string(body)
	if !strings.Contains(out, "CI_STATUS=SUCCESS") {
		t.Fatalf("hook run failed:\n%s", out)
	}
	m := regexp.MustCompile(`CI_URL=(\S+)`).FindStringSubmatch(out)
	if m == nil {
		t.Fatalf("no CI_URL trailer:\n%s", out)
	}
	if !strings.HasPrefix(m[1], "http://ci.example.com:") {
		t.Fatalf("CI_URL = %q, want ci.example.com host (port stripped)", m[1])
	}
}

func TestIndexPlaceholder(t *testing.T) {
	srv, _ := newTestServer(t)
	status, _, body := doReq(t, srv, http.MethodGet, "/", testToken, nil)
	if status != http.StatusOK {
		t.Fatalf("GET /: status %d, body %s", status, body)
	}
	if !bytes.Contains(body, []byte("pushrun")) {
		t.Fatalf("index page does not mention pushrun: %s", body)
	}
}

// Deleting an instance while a run holds the instance lock must fail with
// 409 and leave the instance's directories, state, and port lease intact.
func TestDeleteInstanceBusyOnRunInFlight(t *testing.T) {
	srv, paths := newTestServer(t)
	createFixtureProvider(t, srv)
	status, _, body := doReq(t, srv, http.MethodPost, "/api/v1/projects", testToken, fixtureProjectPayload("demo"))
	if status != http.StatusCreated {
		t.Fatalf("create project: status %d, body %s", status, body)
	}
	st := &instance.State{Project: "demo", Instance: "default", Status: instance.StatusRunning, Port: 25000}
	if err := instance.SaveState(paths, st); err != nil {
		t.Fatalf("seed instance state: %v", err)
	}
	instDir := filepath.Join(paths.Instances, "demo", "default")
	if err := os.MkdirAll(instDir, 0o755); err != nil {
		t.Fatal(err)
	}

	// Hold the instance lock the way an in-flight run would.
	if err := os.MkdirAll(paths.Locks, 0o755); err != nil {
		t.Fatal(err)
	}
	f, err := os.OpenFile(filepath.Join(paths.Locks, "4:demo-7:default"), os.O_CREATE|os.O_RDWR, 0o644)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = f.Close() }()
	if err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		t.Fatalf("hold instance lock: %v", err)
	}

	status, _, body = doReq(t, srv, http.MethodDelete, "/api/v1/instances/demo/default", testToken, nil)
	if status != http.StatusConflict {
		t.Fatalf("delete during run: status %d, want 409, body %s", status, body)
	}
	code, _, _ := decodeErr(t, body)
	if code != "busy" {
		t.Fatalf("code = %q, want busy", code)
	}

	// Nothing was torn down.
	if _, err := os.Stat(instDir); err != nil {
		t.Fatalf("instance dir removed despite busy: %v", err)
	}
	got, err := instance.LoadState(paths, "demo", "default")
	if err != nil {
		t.Fatalf("instance state removed despite busy: %v", err)
	}
	if got.Status != instance.StatusRunning || got.Port != 25000 {
		t.Fatalf("instance state modified despite busy: %+v", got)
	}

	// After the lock is released, the delete goes through.
	if err := syscall.Flock(int(f.Fd()), syscall.LOCK_UN); err != nil {
		t.Fatal(err)
	}
	status, _, body = doReq(t, srv, http.MethodDelete, "/api/v1/instances/demo/default", testToken, nil)
	if status != http.StatusOK {
		t.Fatalf("delete after run finished: status %d, body %s", status, body)
	}
	if _, err := os.Stat(instDir); !os.IsNotExist(err) {
		t.Fatalf("instance dir still present after delete: %v", err)
	}
}

// A client disconnect (cancelled request context) must not abort an
// in-flight engine action: runs legitimately take minutes and are cancelled
// only by server shutdown.
func TestInstanceActionSurvivesClientDisconnect(t *testing.T) {
	srv, paths := newTestServer(t)

	src := t.TempDir()
	if err := os.WriteFile(filepath.Join(src, "index.txt"), []byte("hello"), 0o644); err != nil {
		t.Fatal(err)
	}
	proj := map[string]any{
		"name": "demo",
		"tree": []any{map[string]any{
			"path":  "app",
			"mount": map[string]any{"provider": "symlink", "params": map[string]string{"source": src}},
		}},
		"pipeline": []any{map[string]any{"name": "slow", "run": "sleep 1"}},
	}
	status, _, body := doReq(t, srv, http.MethodPost, "/api/v1/projects", testToken, proj)
	if status != http.StatusCreated {
		t.Fatalf("create project: status %d, body %s", status, body)
	}

	ctx, cancel := context.WithCancel(context.Background())
	req, err := http.NewRequestWithContext(ctx, http.MethodPost,
		srv.URL+"/api/v1/instances/demo/default/run", nil)
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Authorization", "Bearer "+testToken)
	respCh := make(chan error, 1)
	go func() {
		resp, err := srv.Client().Do(req)
		if resp != nil {
			_ = resp.Body.Close()
		}
		respCh <- err
	}()

	// Disconnect mid-run.
	time.Sleep(200 * time.Millisecond)
	cancel()
	<-respCh // client-side error (context canceled); the server keeps going

	// The run still completes with SUCCESS.
	deadline := time.Now().Add(30 * time.Second)
	for {
		st, err := instance.LoadState(paths, "demo", "default")
		if err == nil && (st.Status == instance.StatusSuccess || st.Status == instance.StatusFailed) {
			if st.Status != instance.StatusSuccess {
				t.Fatalf("run was aborted by client disconnect: status %s", st.Status)
			}
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("run did not complete after client disconnect (state err: %v)", err)
		}
		time.Sleep(50 * time.Millisecond)
	}
}
