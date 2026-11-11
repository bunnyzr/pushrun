package server_test

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"testing/fstest"
)

// newStaticTestServer boots the full handler stack with the given web
// console assets injected as Deps.Web.
func newStaticTestServer(t *testing.T, webFS fstest.MapFS) *httptest.Server {
	t.Helper()
	h, _ := newTestHandlerWith(t, webFS)
	srv := httptest.NewServer(h)
	t.Cleanup(srv.Close)
	return srv
}

var testWebFS = fstest.MapFS{
	"index.html":            &fstest.MapFile{Data: []byte("<html>pushrun console</html>")},
	"assets/app-abc123.js":  &fstest.MapFile{Data: []byte("console.log('hi')")},
	"assets/app-abc123.css": &fstest.MapFile{Data: []byte("body{}")},
}

func TestStaticIndexNoCache(t *testing.T) {
	srv := newStaticTestServer(t, testWebFS)

	status, hdr, body := doReq(t, srv, http.MethodGet, "/", "", nil)
	if status != http.StatusOK {
		t.Fatalf("GET /: status %d, body %s", status, body)
	}
	if cc := hdr.Get("Cache-Control"); cc != "no-cache" {
		t.Fatalf("GET /: Cache-Control = %q, want no-cache", cc)
	}
	if !bytes.Contains(body, []byte("pushrun console")) {
		t.Fatalf("GET /: body is not index.html: %s", body)
	}
}

// SPA deep links are extensionless misses: they serve index.html so the
// client-side router can take over.
func TestStaticSPAFallback(t *testing.T) {
	srv := newStaticTestServer(t, testWebFS)

	status, hdr, body := doReq(t, srv, http.MethodGet, "/projects/demo", "", nil)
	if status != http.StatusOK {
		t.Fatalf("GET /projects/demo: status %d, body %s", status, body)
	}
	if !bytes.Equal(body, testWebFS["index.html"].Data) {
		t.Fatalf("GET /projects/demo: body %q, want index.html", body)
	}
	if cc := hdr.Get("Cache-Control"); cc != "no-cache" {
		t.Fatalf("GET /projects/demo: Cache-Control = %q, want no-cache", cc)
	}
	if ct := hdr.Get("Content-Type"); !strings.Contains(ct, "text/html") {
		t.Fatalf("GET /projects/demo: Content-Type = %q, want text/html", ct)
	}
}

// Fingerprinted assets under /assets/ are cached immutably for a year.
func TestStaticAssetsImmutable(t *testing.T) {
	srv := newStaticTestServer(t, testWebFS)

	status, hdr, body := doReq(t, srv, http.MethodGet, "/assets/app-abc123.js", "", nil)
	if status != http.StatusOK {
		t.Fatalf("GET asset: status %d, body %s", status, body)
	}
	want := "public, max-age=31536000, immutable"
	if cc := hdr.Get("Cache-Control"); cc != want {
		t.Fatalf("GET asset: Cache-Control = %q, want %q", cc, want)
	}
	if !bytes.Equal(body, testWebFS["assets/app-abc123.js"].Data) {
		t.Fatalf("GET asset: body %q, want asset content", body)
	}
}

// A miss whose last path segment looks like a filename (contains a dot) is a
// genuine 404, not the SPA shell.
func TestStaticMissingFile404(t *testing.T) {
	srv := newStaticTestServer(t, testWebFS)

	status, _, _ := doReq(t, srv, http.MethodGet, "/assets/missing.js", "", nil)
	if status != http.StatusNotFound {
		t.Fatalf("GET /assets/missing.js: status %d, want 404", status)
	}
}

// Unknown API paths fall through to the GET / catch-all; they must answer
// with the standard JSON 404, never the SPA shell.
func TestStaticAPIFallbackIsJSON404(t *testing.T) {
	srv := newStaticTestServer(t, testWebFS)

	status, hdr, body := doReq(t, srv, http.MethodGet, "/api/v1/unknown", testToken, nil)
	if status != http.StatusNotFound {
		t.Fatalf("GET /api/v1/unknown: status %d, want 404", status)
	}
	if ct := hdr.Get("Content-Type"); ct != "application/json" {
		t.Fatalf("GET /api/v1/unknown: Content-Type = %q, want application/json", ct)
	}
	code, _, _ := decodeErr(t, body)
	if code != "not_found" {
		t.Fatalf("GET /api/v1/unknown: code = %q, want not_found", code)
	}
}
