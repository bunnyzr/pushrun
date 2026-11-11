package server_test

import (
	"bytes"
	"net/http"
	"testing"
	"time"
)

// secretWarmProviderPayload is a script provider whose only param is a
// secret warmup-scoped one, so the warmup record must mask it.
func secretWarmProviderPayload() map[string]any {
	return map[string]any{
		"id":      "secwarm",
		"name":    "Secret Warm",
		"warmup":  "scripts/warmup.sh",
		"install": "scripts/install.sh",
		"parameters": []any{
			map[string]any{"id": "token", "type": "string", "scope": "warmup", "required": true, "secret": true},
		},
		"scripts": map[string]string{
			"scripts/warmup.sh": "#!/bin/bash\nset -euo pipefail\necho warm > \"$PUSHRUN_PROVIDER_CACHE_DIR/warmed.txt\"\n",
			"scripts/install.sh": "#!/bin/bash\nset -euo pipefail\nmkdir -p \"$PUSHRUN_PROVIDER_TARGET_DIR\"\n" +
				"echo installed > \"$PUSHRUN_PROVIDER_TARGET_DIR/installed.txt\"\n",
		},
	}
}

func TestProvidersListIncludesWarmups(t *testing.T) {
	srv, _ := newTestServer(t)

	status, _, body := doReq(t, srv, http.MethodPost, "/api/v1/providers", testToken, secretWarmProviderPayload())
	if status != http.StatusCreated {
		t.Fatalf("create provider: status %d, body %s", status, body)
	}
	status, _, body = doReq(t, srv, http.MethodPost, "/api/v1/providers/secwarm/warmup", testToken,
		map[string]any{"params": map[string]string{"token": "s3cr3t"}})
	if status != http.StatusOK {
		t.Fatalf("warmup: status %d, body %s", status, body)
	}

	// Every provider object — builtin or stored — carries a warmups array,
	// empty when the provider was never warmed (the git builtin always is:
	// its cache is the bare repo, not a ProviderData dir).
	check := func(url string) map[string]any {
		t.Helper()
		status, _, body := doReq(t, srv, http.MethodGet, url, testToken, nil)
		if status != http.StatusOK {
			t.Fatalf("GET %s: status %d, body %s", url, status, body)
		}
		if bytes.Contains(body, []byte("s3cr3t")) {
			t.Fatalf("GET %s leaks the secret warmup param: %s", url, body)
		}
		var providers []any
		m := decodeJSON(t, body)
		if list, ok := m["providers"].([]any); ok {
			providers = list
		} else {
			providers = []any{m}
		}
		var found map[string]any
		for _, p := range providers {
			pm := p.(map[string]any)
			warmups, ok := pm["warmups"].([]any)
			if !ok {
				t.Fatalf("GET %s: provider %v has no warmups array: %v", url, pm["id"], pm)
			}
			if pm["id"] == "git" && len(warmups) != 0 {
				t.Fatalf("GET %s: git builtin has %d warmups, want 0", url, len(warmups))
			}
			if pm["id"] == "secwarm" {
				found = pm
			}
		}
		if found == nil {
			t.Fatalf("GET %s: secwarm missing from %s", url, body)
		}
		return found
	}

	for _, url := range []string{"/api/v1/providers", "/api/v1/providers/secwarm"} {
		pm := check(url)
		warmups := pm["warmups"].([]any)
		if len(warmups) != 1 {
			t.Fatalf("GET %s: warmups = %v, want 1 entry", url, warmups)
		}
		w0 := warmups[0].(map[string]any)
		if w0["fingerprint"] == "" {
			t.Fatalf("GET %s: warmup entry has no fingerprint: %v", url, w0)
		}
		if w0["params"].(map[string]any)["token"] != "***" {
			t.Fatalf("GET %s: secret warmup param not masked: %v", url, w0)
		}
		warmedAt, ok := w0["warmed_at"].(string)
		if !ok || warmedAt == "" {
			t.Fatalf("GET %s: warmup entry missing warmed_at: %v", url, w0)
		}
		if _, err := time.Parse(time.RFC3339, warmedAt); err != nil {
			t.Fatalf("GET %s: warmed_at %q not RFC3339: %v", url, warmedAt, err)
		}
	}
}

// The single-provider GET exposes the on-disk script files so the console
// can preload the editor; the builtin providers own no dir and carry none.
func TestGetProviderIncludesScripts(t *testing.T) {
	srv, _ := newTestServer(t)
	createFixtureProvider(t, srv)

	status, _, body := doReq(t, srv, http.MethodGet, "/api/v1/providers/fixture", testToken, nil)
	if status != http.StatusOK {
		t.Fatalf("get: status %d, body %s", status, body)
	}
	m := decodeJSON(t, body)
	scripts, ok := m["scripts"].(map[string]any)
	if !ok {
		t.Fatalf("get: no scripts map: %s", body)
	}
	want := fixtureProviderPayload()["scripts"].(map[string]string)
	for path, content := range want {
		if scripts[path] != content {
			t.Fatalf("scripts[%q] = %q, want %q", path, scripts[path], content)
		}
	}

	// The list endpoint stays light: no script contents there.
	status, _, body = doReq(t, srv, http.MethodGet, "/api/v1/providers", testToken, nil)
	if status != http.StatusOK {
		t.Fatalf("list: status %d, body %s", status, body)
	}
	for _, p := range decodeJSON(t, body)["providers"].([]any) {
		if pm := p.(map[string]any); pm["scripts"] != nil {
			t.Fatalf("list carries scripts for %v: %s", pm["id"], body)
		}
	}

	// The git builtin owns no on-disk dir: no scripts content.
	status, _, body = doReq(t, srv, http.MethodGet, "/api/v1/providers/git", testToken, nil)
	if status != http.StatusOK {
		t.Fatalf("get builtin: status %d, body %s", status, body)
	}
	if m := decodeJSON(t, body); m["scripts"] != nil {
		t.Fatalf("builtin carries scripts: %s", body)
	}
}
