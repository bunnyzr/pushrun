// Package e2e drives the full push-to-serve loop against real processes:
// a built pushrun binary serves a temp data root, the example provider and
// project are imported over the API, the client is installed via
// /install.sh into a temp HOME, and a `git pushrun` push of the example
// service triggers the run end to end. No mocks.
//
// Requires git, bash, curl, and go in PATH (the pipeline builds the example
// service with go).
package e2e_test

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"encoding/json"
	"fmt"
	"io"
	"io/fs"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"testing"
	"time"
)

func TestPushToServeLoop(t *testing.T) {
	for _, tool := range []string{"git", "bash", "curl", "go"} {
		if _, err := exec.LookPath(tool); err != nil {
			t.Fatalf("e2e requires %s in PATH: %v", tool, err)
		}
	}
	repoRoot, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatal(err)
	}

	// Build the binary under test.
	bin := filepath.Join(t.TempDir(), "pushrun")
	build := exec.Command("go", "build",
		"-ldflags", "-X main.version=e2e",
		"-o", bin, "./cmd/pushrun")
	build.Dir = repoRoot
	if out, err := build.CombinedOutput(); err != nil {
		t.Fatalf("build pushrun: %v\n%s", err, out)
	}

	// Data root with non-default ports everywhere.
	root := t.TempDir()
	daemonPort := freePort(t)
	poolFrom := freePort(t)
	for poolFrom == daemonPort {
		poolFrom = freePort(t)
	}
	// Room for a small range above the base port.
	if poolFrom > 65535-21 {
		poolFrom = 65535 - 21
	}
	poolTo := poolFrom + 20
	configYAML := fmt.Sprintf(`http:
  bind: 127.0.0.1
  port: %d
port_pool:
  from: %d
  to: %d
auth:
  enabled: true
log:
  level: info
`, daemonPort, poolFrom, poolTo)
	if err := os.WriteFile(filepath.Join(root, "config.yaml"), []byte(configYAML), 0o600); err != nil {
		t.Fatal(err)
	}

	server := fmt.Sprintf("http://127.0.0.1:%d", daemonPort)

	// Start the daemon; its stderr goes to a file dumped on failure.
	daemonLog := filepath.Join(t.TempDir(), "daemon.log")
	dlf, err := os.Create(daemonLog)
	if err != nil {
		t.Fatal(err)
	}
	daemon := exec.Command(bin, "serve", "--root", root)
	daemon.Stdout = dlf
	daemon.Stderr = dlf
	if err := daemon.Start(); err != nil {
		t.Fatalf("start daemon: %v", err)
	}
	t.Cleanup(func() {
		_ = daemon.Process.Kill()
		_ = daemon.Wait()
		_ = dlf.Close()
		if t.Failed() {
			if data, err := os.ReadFile(daemonLog); err == nil {
				t.Logf("daemon log:\n%s", tailString(string(data), 8000))
			}
		}
	})

	token := waitForDaemon(t, root, server)
	t.Cleanup(func() {
		// Best effort: stop the supervised service before the daemon dies,
		// so no test process leaks past the test.
		apiRequest(t, http.MethodPost, server+"/api/v1/instances/hello-web/default/stop", token, nil)
	})

	// Import the example provider and the hello-web project (its git mount
	// declares the repo identity localhost/hello-web, which the local repo's
	// origin URL below normalizes to).
	providerBundle := tarGzFromDir(t, filepath.Join(repoRoot, "examples", "providers", "example-static-runtime"))
	resp := apiRequest(t, http.MethodPost, server+"/api/v1/providers/import", token, providerBundle)
	if resp.status != http.StatusCreated {
		t.Fatalf("import provider: HTTP %d: %s", resp.status, resp.body)
	}

	projectYAML, err := os.ReadFile(filepath.Join(repoRoot, "examples", "hello-web", "project.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	resp = apiRequest(t, http.MethodPost, server+"/api/v1/projects/import", token,
		tarGz(t, map[string][]byte{"hello-web.yaml": projectYAML}))
	if resp.status != http.StatusCreated {
		t.Fatalf("import project: HTTP %d: %s", resp.status, resp.body)
	}

	// Install the client via /install.sh into a throwaway HOME.
	home := t.TempDir()
	resp = apiRequest(t, http.MethodGet, server+"/install.sh", token, nil)
	if resp.status != http.StatusOK {
		t.Fatalf("GET /install.sh: HTTP %d: %s", resp.status, resp.body)
	}
	installSh := filepath.Join(t.TempDir(), "install.sh")
	if err := os.WriteFile(installSh, resp.body, 0o755); err != nil {
		t.Fatal(err)
	}
	out, err := shCmd(home, home, "sh", installSh, "--server", server, "--token", token)
	if err != nil {
		t.Fatalf("install.sh: %v\n%s", err, out)
	}
	cfgFile := filepath.Join(home, ".config", "pushrun", "config")
	fi, err := os.Stat(cfgFile)
	if err != nil {
		t.Fatalf("client config missing: %v", err)
	}
	if fi.Mode().Perm() != 0o600 {
		t.Fatalf("client config mode = %o, want 0600", fi.Mode().Perm())
	}

	// Local repo from the example service.
	src := filepath.Join(t.TempDir(), "hello-web")
	if err := os.MkdirAll(src, 0o755); err != nil {
		t.Fatal(err)
	}
	for _, f := range []string{"go.mod", "main.go"} {
		data, err := os.ReadFile(filepath.Join(repoRoot, "examples", "hello-web", f))
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(src, f), data, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	git(t, home, src, "init", "-b", "main")
	git(t, home, src, "config", "user.email", "e2e@example.com")
	git(t, home, src, "config", "user.name", "e2e")
	git(t, home, src, "add", "-A")
	git(t, home, src, "commit", "-m", "hello pushrun")
	// The client derives the repo identity (localhost/hello-web) from the
	// origin URL; the push itself goes to the daemon's /git/<identity>.git
	// endpoint, so origin is never contacted.
	git(t, home, src, "remote", "add", "origin", "https://localhost/hello-web.git")

	// The push runs through the installed client: exit 0, CI_STATUS=SUCCESS
	// streamed back over the sideband, CI_URL live. The token rides as an
	// env-scoped Authorization extraHeader (never written to .git/config).
	push, err := shCmd(home, src, "git", "pushrun")
	if err != nil {
		t.Fatalf("git pushrun: %v\n%s", err, push)
	}
	if !strings.Contains(push, "CI_STATUS=SUCCESS") {
		t.Fatalf("push output missing CI_STATUS=SUCCESS:\n%s", push)
	}
	m := regexp.MustCompile(`CI_URL=(\S+)`).FindStringSubmatch(push)
	if m == nil {
		t.Fatalf("push output missing CI_URL:\n%s", push)
	}
	ciURL := m[1]
	httpReq(t, http.MethodGet, ciURL, "", http.StatusOK)

	// status reports the running instance.
	status, err := shCmd(home, src, "git", "pushrun", "status")
	if err != nil {
		t.Fatalf("git pushrun status: %v\n%s", err, status)
	}
	if !strings.Contains(status, `"status":"RUNNING"`) {
		t.Fatalf("status output does not report RUNNING:\n%s", status)
	}

	// Business logs are visible through the logs API: the supervised
	// process's stdout/stderr (service.log) and the pipeline's own file.
	resp = apiRequest(t, http.MethodGet, server+"/api/v1/instances/hello-web/default/logs/tree", token, nil)
	if resp.status != http.StatusOK {
		t.Fatalf("logs tree: HTTP %d: %s", resp.status, resp.body)
	}
	var tree struct {
		Files []string `json:"files"`
	}
	if err := json.Unmarshal(resp.body, &tree); err != nil {
		t.Fatalf("parse logs tree: %v", err)
	}
	for _, want := range []string{"service.log", "hello-web.log"} {
		if !slices.Contains(tree.Files, want) {
			t.Fatalf("logs tree %v missing %q", tree.Files, want)
		}
	}
	resp = apiRequest(t, http.MethodGet,
		server+"/api/v1/instances/hello-web/default/logs/file?path=service.log", token, nil)
	if resp.status != http.StatusOK {
		t.Fatalf("logs file: HTTP %d: %s", resp.status, resp.body)
	}
	var logFile struct {
		Content string `json:"content"`
	}
	if err := json.Unmarshal(resp.body, &logFile); err != nil {
		t.Fatalf("parse logs file: %v", err)
	}
	if !strings.Contains(logFile.Content, "hello-web listening") {
		t.Fatalf("service.log missing service output:\n%s", logFile.Content)
	}

	// A rerun with no code change succeeds via the API.
	resp = apiRequest(t, http.MethodPost, server+"/api/v1/instances/hello-web/default/rerun", token,
		[]byte(`{"branch":"main","user":"e2e@example.com"}`))
	if resp.status != http.StatusOK {
		t.Fatalf("rerun: HTTP %d: %s", resp.status, resp.body)
	}
	if !strings.Contains(string(resp.body), `"status":"SUCCESS"`) {
		t.Fatalf("rerun did not succeed: %s", resp.body)
	}
	httpReq(t, http.MethodGet, ciURL, "", http.StatusOK)

	// stop kills the supervised process group: the port goes closed.
	stop, err := shCmd(home, src, "git", "pushrun", "stop")
	if err != nil {
		t.Fatalf("git pushrun stop: %v\n%s", err, stop)
	}
	if !strings.Contains(stop, "stop hello-web/default: SUCCESS") {
		t.Fatalf("stop output:\n%s", stop)
	}
	waitPortClosed(t, ciURL)
}

// --- helpers ---

func freePort(t *testing.T) int {
	t.Helper()
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = l.Close() }()
	return l.Addr().(*net.TCPAddr).Port
}

// waitForDaemon polls the API until the daemon answers, then returns the
// auth token from the data root.
func waitForDaemon(t *testing.T, root, server string) string {
	t.Helper()
	deadline := time.Now().Add(15 * time.Second)
	for {
		data, err := os.ReadFile(filepath.Join(root, "token"))
		if err == nil {
			token := strings.TrimSpace(string(data))
			if token != "" {
				resp, err := http.Get(server + "/api/v1/version")
				if err == nil {
					_ = resp.Body.Close()
					return token
				}
			}
		}
		if time.Now().After(deadline) {
			t.Fatal("daemon did not come up within 15s")
		}
		time.Sleep(100 * time.Millisecond)
	}
}

// waitPortClosed polls until nothing listens on the URL's port anymore.
func waitPortClosed(t *testing.T, rawURL string) {
	t.Helper()
	addr := strings.TrimPrefix(rawURL, "http://")
	deadline := time.Now().Add(10 * time.Second)
	for {
		conn, err := net.DialTimeout("tcp", addr, 500*time.Millisecond)
		if err != nil {
			return
		}
		_ = conn.Close()
		if time.Now().After(deadline) {
			t.Fatalf("port %s still open after stop", addr)
		}
		time.Sleep(100 * time.Millisecond)
	}
}

type apiResp struct {
	status int
	body   []byte
}

func apiRequest(t *testing.T, method, url, token string, body []byte) apiResp {
	t.Helper()
	var rdr io.Reader
	if body != nil {
		rdr = bytes.NewReader(body)
	}
	req, err := http.NewRequest(method, url, rdr)
	if err != nil {
		t.Fatal(err)
	}
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("%s %s: %v", method, url, err)
	}
	defer func() { _ = resp.Body.Close() }()
	data, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatal(err)
	}
	return apiResp{status: resp.StatusCode, body: data}
}

func httpReq(t *testing.T, method, url, token string, wantStatus int) {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for {
		resp := apiRequest(t, method, url, token, nil)
		if resp.status == wantStatus {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("%s %s: HTTP %d, want %d; body: %s", method, url, resp.status, wantStatus, resp.body)
		}
		time.Sleep(200 * time.Millisecond)
	}
}

// shCmd runs a command with HOME pointed at the throwaway client home.
func shCmd(home, dir, name string, args ...string) (string, error) {
	cmd := exec.Command(name, args...)
	cmd.Dir = dir
	cmd.Env = cleanEnv(home)
	out, err := cmd.CombinedOutput()
	return string(out), err
}

func git(t *testing.T, home, dir string, args ...string) string {
	t.Helper()
	out, err := shCmd(home, dir, "git", args...)
	if err != nil {
		t.Fatalf("git %s: %v\n%s", strings.Join(args, " "), err, out)
	}
	return out
}

// cleanEnv is the inherited environment with HOME replaced, so git never
// touches the real user config.
func cleanEnv(home string) []string {
	env := []string{"HOME=" + home}
	for _, e := range os.Environ() {
		if strings.HasPrefix(e, "HOME=") {
			continue
		}
		env = append(env, e)
	}
	return env
}

// tarGz packs files (slash-separated name -> content) into a .tar.gz bundle.
func tarGz(t *testing.T, files map[string][]byte) []byte {
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

// tarGzFromDir packs every regular file under dir into a .tar.gz bundle,
// keyed by slash-separated relative path. .sh files keep mode 0755.
func tarGzFromDir(t *testing.T, dir string) []byte {
	t.Helper()
	files := map[string][]byte{}
	err := filepath.WalkDir(dir, func(p string, d fs.DirEntry, err error) error {
		if err != nil || !d.Type().IsRegular() {
			return err
		}
		rel, err := filepath.Rel(dir, p)
		if err != nil {
			return err
		}
		data, err := os.ReadFile(p)
		if err != nil {
			return err
		}
		files[filepath.ToSlash(rel)] = data
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gz)
	for name, data := range files {
		mode := int64(0o644)
		if strings.HasSuffix(name, ".sh") {
			mode = 0o755
		}
		if err := tw.WriteHeader(&tar.Header{Name: name, Mode: mode, Size: int64(len(data))}); err != nil {
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

func tailString(s string, max int) string {
	if len(s) > max {
		return s[len(s)-max:]
	}
	return s
}
