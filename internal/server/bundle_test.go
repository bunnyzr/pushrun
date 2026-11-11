package server_test

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"errors"
	"io/fs"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

func makeBundle(t *testing.T, files map[string]string) []byte {
	t.Helper()
	names := make([]string, 0, len(files))
	for name := range files {
		names = append(names, name)
	}
	sort.Strings(names)
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gz)
	for _, name := range names {
		data := []byte(files[name])
		if err := tw.WriteHeader(&tar.Header{
			Name: name, Mode: 0o644, Size: int64(len(data)), Typeflag: tar.TypeReg,
		}); err != nil {
			t.Fatalf("write header %s: %v", name, err)
		}
		if _, err := tw.Write(data); err != nil {
			t.Fatalf("write %s: %v", name, err)
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

func TestBundleExportImportRoundTrip(t *testing.T) {
	srv, _ := newTestServer(t)
	createFixtureProvider(t, srv)
	status, _, body := doReq(t, srv, http.MethodPost, "/api/v1/projects", testToken, fixtureProjectPayload("demo"))
	if status != http.StatusCreated {
		t.Fatalf("create project: status %d, body %s", status, body)
	}

	status, hdr, bundle := doReq(t, srv, http.MethodGet, "/api/v1/bundle/export", testToken, nil)
	if status != http.StatusOK {
		t.Fatalf("export: status %d, body %s", status, bundle)
	}
	if ct := hdr.Get("Content-Type"); !strings.Contains(ct, "gzip") {
		t.Fatalf("Content-Type = %q, want gzip", ct)
	}
	files := readTarGzBundle(t, bundle)
	for _, want := range []string{
		"bundle.yaml",
		"providers/fixture/provider.yaml",
		"providers/fixture/scripts/warmup.sh",
		"providers/fixture/scripts/install.sh",
		"projects/demo.yaml",
	} {
		if _, ok := files[want]; !ok {
			t.Fatalf("bundle missing %q; entries: %v", want, files)
		}
	}
	if _, ok := files["providers/git/provider.yaml"]; ok {
		t.Fatal("builtin git provider must not be exported")
	}

	// A fresh deployment imports providers and projects from one request.
	srv2, paths2 := newTestServer(t)
	status, _, body = doReq(t, srv2, http.MethodPost, "/api/v1/bundle/import", testToken, bundle)
	if status != http.StatusCreated {
		t.Fatalf("import: status %d, body %s", status, body)
	}
	m := decodeJSON(t, body)
	if m["status"] != "imported" {
		t.Fatalf("status = %v, body %s", m["status"], body)
	}
	if _, _, body = doReq(t, srv2, http.MethodGet, "/api/v1/providers/fixture", testToken, nil); !strings.Contains(string(body), "Fixture Provider") {
		t.Fatalf("provider not imported: %s", body)
	}
	if _, _, body = doReq(t, srv2, http.MethodGet, "/api/v1/projects/demo", testToken, nil); !strings.Contains(string(body), `"name":"demo"`) {
		t.Fatalf("project not imported: %s", body)
	}
	// Project YAML is copied verbatim, so the secret mount param survives even
	// though the API view masks it.
	raw, err := os.ReadFile(filepath.Join(paths2.Projects, "demo.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(raw), "hunter2") {
		t.Fatalf("imported project lost its secret:\n%s", raw)
	}
}

func TestBundleImportRejectsMissingProvidersBeforeWriting(t *testing.T) {
	srv, paths := newTestServer(t)
	payload := fixtureProjectPayload("demo")
	payload["schema"] = "pushrun.project/v1"
	projectYAML, err := yaml.Marshal(payload)
	if err != nil {
		t.Fatal(err)
	}
	bundle := makeBundle(t, map[string]string{
		"bundle.yaml":        "schema: pushrun.bundle/v1\n",
		"projects/demo.yaml": string(projectYAML),
	})

	status, _, body := doReq(t, srv, http.MethodPost, "/api/v1/bundle/import", testToken, bundle)
	if status != http.StatusBadRequest {
		t.Fatalf("status %d, want 400, body %s", status, body)
	}
	if code, msg, _ := decodeErr(t, body); code != "missing_providers" || !strings.Contains(msg, "fixture") {
		t.Fatalf("error = %q %q, body %s", code, msg, body)
	}
	if _, err := os.Stat(filepath.Join(paths.Projects, "demo.yaml")); !errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("failed import wrote the project: %v", err)
	}
}

func TestBundleImportRejectsBuiltinProvider(t *testing.T) {
	srv, _ := newTestServer(t)
	bundle := makeBundle(t, map[string]string{
		"bundle.yaml": "schema: pushrun.bundle/v1\n",
		"providers/git/provider.yaml": "schema: pushrun.provider/v1\n" +
			"id: git\nname: Git\ninstall: scripts/install.sh\n",
		"providers/git/scripts/install.sh": "#!/bin/sh\n",
	})
	status, _, body := doReq(t, srv, http.MethodPost, "/api/v1/bundle/import", testToken, bundle)
	if status != http.StatusBadRequest {
		t.Fatalf("status %d, want 400, body %s", status, body)
	}
	if code, msg, _ := decodeErr(t, body); code != "invalid" || !strings.Contains(msg, "builtin") {
		t.Fatalf("error = %q %q, body %s", code, msg, body)
	}
}

func TestBundleImportRequiresManifest(t *testing.T) {
	srv, _ := newTestServer(t)
	bundle := makeBundle(t, map[string]string{
		"projects/demo.yaml": "schema: pushrun.project/v1\nname: demo\n",
	})
	status, _, body := doReq(t, srv, http.MethodPost, "/api/v1/bundle/import", testToken, bundle)
	if status != http.StatusBadRequest {
		t.Fatalf("status %d, want 400, body %s", status, body)
	}
	if !strings.Contains(string(body), "bundle.yaml") {
		t.Fatalf("error does not mention the missing manifest: %s", body)
	}
}
