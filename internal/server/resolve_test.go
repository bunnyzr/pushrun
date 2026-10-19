package server_test

import (
	"net/http"
	"strings"
	"testing"
)

// The resolve endpoint answers with the same project the pre-receive hook
// would match for a push of the repo.
func TestResolveProject(t *testing.T) {
	srv, _ := newTestServer(t)
	createGitProject(t, srv, "demo", "git.example.com/team/demo", "echo hi")

	status, _, body := doReq(t, srv, http.MethodGet,
		"/api/v1/resolve?repo=git.example.com/team/demo", testToken, nil)
	if status != http.StatusOK {
		t.Fatalf("resolve: status %d, body %s", status, body)
	}
	if !strings.Contains(string(body), `"project":"demo"`) {
		t.Fatalf("resolve body missing the project: %s", body)
	}

	// URL forms of the same identity resolve too.
	status, _, body = doReq(t, srv, http.MethodGet,
		"/api/v1/resolve?repo=https%3A%2F%2Fgit.example.com%2Fteam%2Fdemo.git", testToken, nil)
	if status != http.StatusOK || !strings.Contains(string(body), `"project":"demo"`) {
		t.Fatalf("resolve URL form: status %d, body %s", status, body)
	}

	// An explicit project that mounts the repo resolves to it.
	status, _, body = doReq(t, srv, http.MethodGet,
		"/api/v1/resolve?repo=git.example.com/team/demo&project=demo", testToken, nil)
	if status != http.StatusOK || !strings.Contains(string(body), `"project":"demo"`) {
		t.Fatalf("resolve with explicit project: status %d, body %s", status, body)
	}
}

// Matching failures carry the MatchError code so clients can act on them.
func TestResolveProjectErrors(t *testing.T) {
	srv, _ := newTestServer(t)
	createGitProjectWithNodes(t, srv, "demo", "echo hi",
		gitNode("app", "git.example.com/team/app", true),
		gitNode("lib", "git.example.com/team/lib", false),
	)

	// Satellite-only repo: project_required with the candidates.
	status, _, body := doReq(t, srv, http.MethodGet,
		"/api/v1/resolve?repo=git.example.com/team/lib", testToken, nil)
	if status != http.StatusConflict || !strings.Contains(string(body), "project_required:demo") {
		t.Fatalf("resolve satellite repo: status %d, body %s", status, body)
	}

	// Unknown repo.
	status, _, body = doReq(t, srv, http.MethodGet,
		"/api/v1/resolve?repo=git.example.com/team/nope", testToken, nil)
	if status != http.StatusConflict || !strings.Contains(string(body), "unknown_repo:git.example.com/team/nope") {
		t.Fatalf("resolve unknown repo: status %d, body %s", status, body)
	}

	// Explicit project that does not mount the repo.
	status, _, body = doReq(t, srv, http.MethodGet,
		"/api/v1/resolve?repo=git.example.com/team/lib&project=other", testToken, nil)
	if status != http.StatusConflict || !strings.Contains(string(body), "unknown_project:other") {
		t.Fatalf("resolve mismatched explicit project: status %d, body %s", status, body)
	}

	// A repo reference that is not a host/path identity is a 400.
	status, _, _ = doReq(t, srv, http.MethodGet, "/api/v1/resolve?repo=demo", testToken, nil)
	if status != http.StatusBadRequest {
		t.Fatalf("resolve invalid repo: status %d, want 400", status)
	}

	// The endpoint is token-authenticated like the rest of /api/v1.
	status, _, _ = doReq(t, srv, http.MethodGet,
		"/api/v1/resolve?repo=git.example.com/team/app", "", nil)
	if status != http.StatusUnauthorized {
		t.Fatalf("resolve without token: status %d, want 401", status)
	}
}
