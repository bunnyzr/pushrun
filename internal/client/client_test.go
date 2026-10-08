// Package client_test drives the server-rendered shell scripts (install.sh,
// client.sh) with bash against a real pushrun server on httptest, with HOME
// overridden to a temp dir so the host's git config and pushrun config are
// never touched.
package client_test

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/bunnyzr/pushrun/internal/client"
	"github.com/bunnyzr/pushrun/internal/config"
	"github.com/bunnyzr/pushrun/internal/gitx"
	"github.com/bunnyzr/pushrun/internal/hookclient"
	"github.com/bunnyzr/pushrun/internal/instance"
	"github.com/bunnyzr/pushrun/internal/provider"
	"github.com/bunnyzr/pushrun/internal/run"
	"github.com/bunnyzr/pushrun/internal/server"
	"github.com/bunnyzr/pushrun/internal/testutil"
)

const testToken = "test-token-0123456789abcdef"
const testVersion = "test-version"

// TestMain doubles the test binary as the pushrun hook executable: the
// hooks installed into test bare repos exec os.Executable() (this test
// binary) as "hook pre-receive" / "hook post-receive". Same trick as the
// server package's git tests.
func TestMain(m *testing.M) {
	if len(os.Args) >= 3 && os.Args[1] == "hook" {
		env := hookclient.Env{
			Port:     os.Getenv("PUSHRUN_PORT"),
			Secret:   os.Getenv("PUSHRUN_HOOK_SECRET"),
			Root:     os.Getenv("PUSHRUN_ROOT"),
			Repo:     os.Getenv("PUSHRUN_REPO"),
			Project:  os.Getenv("PUSHRUN_PROJECT"),
			User:     os.Getenv("REMOTE_USER"),
			Action:   os.Getenv("PUSHRUN_ACTION"),
			Instance: os.Getenv("PUSHRUN_INSTANCE"),
		}
		var err error
		switch os.Args[2] {
		case "pre-receive":
			err = hookclient.PreReceive(os.Stdin, os.Stderr, env)
		case "post-receive":
			err = hookclient.PostReceive(os.Stdin, os.Stdout, env)
		default:
			err = fmt.Errorf("unknown hook %q", os.Args[2])
		}
		if err != nil {
			fmt.Fprintln(os.Stderr, "pushrun hook:", err)
			os.Exit(1)
		}
		os.Exit(0)
	}
	testutil.IsolateGitEnv()
	os.Exit(m.Run())
}

// requestLog records method+path+auth of every request, so tests can assert
// which endpoints the shell client hit.
type requestLog struct {
	mu      sync.Mutex
	entries []string
}

func (l *requestLog) wrap(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		l.mu.Lock()
		l.entries = append(l.entries, r.Method+" "+r.URL.Path+" auth="+r.Header.Get("Authorization"))
		l.mu.Unlock()
		next.ServeHTTP(w, r)
	})
}

func (l *requestLog) clear() {
	l.mu.Lock()
	l.entries = nil
	l.mu.Unlock()
}

func (l *requestLog) contains(sub string) bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	for _, e := range l.entries {
		if strings.Contains(e, sub) {
			return true
		}
	}
	return false
}

func (l *requestLog) dump() string {
	l.mu.Lock()
	defer l.mu.Unlock()
	return strings.Join(l.entries, "\n")
}

// newTestServer boots the full server stack over a fresh temp data root,
// wrapped in the request recorder.
func newTestServer(t *testing.T) (*httptest.Server, config.Paths, *requestLog) {
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
	h := server.New(paths, cfg, server.Deps{
		Manager:  mgr,
		Executor: exec,
		Pool:     pool,
		Token:    testToken,
		Version:  testVersion,
		Logger:   logger,
	})
	rec := &requestLog{}
	srv := httptest.NewServer(rec.wrap(h))
	t.Cleanup(srv.Close)
	return srv, paths, rec
}

// doReq performs one API call against the test server.
func doReq(t *testing.T, srv *httptest.Server, method, path string, body any) (int, []byte) {
	t.Helper()
	var rdr io.Reader
	if body != nil {
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
	req.Header.Set("Authorization", "Bearer "+testToken)
	resp, err := srv.Client().Do(req)
	if err != nil {
		t.Fatalf("%s %s: %v", method, path, err)
	}
	defer func() { _ = resp.Body.Close() }()
	data, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("read body: %v", err)
	}
	return resp.StatusCode, data
}

// gitProjectPayload builds a project definition whose primary git node (at
// path "app") mounts repoIdentity, with one pipeline step running stepRun.
// Extra tree nodes (e.g. a non-primary git node) can be appended.
func gitProjectPayload(repoIdentity, name, stepRun string, extraNodes ...map[string]any) map[string]any {
	tree := []any{map[string]any{
		"path": "app",
		"mount": map[string]any{"provider": "git", "primary": true,
			"params": map[string]string{"repo": repoIdentity, "branch": "main"}},
	}}
	for _, n := range extraNodes {
		tree = append(tree, n)
	}
	return map[string]any{
		"name":     name,
		"tree":     tree,
		"pipeline": []any{map[string]any{"name": "build", "run": stepRun}},
	}
}

// gitNode builds a tree node mounting repoIdentity (branch main) at path.
func gitNode(path, repoIdentity string, primary bool) map[string]any {
	return map[string]any{"path": path, "mount": map[string]any{
		"provider": "git", "primary": primary,
		"params": map[string]string{"repo": repoIdentity, "branch": "main"},
	}}
}

// createGitProject registers a project whose primary git node mounts
// repoIdentity, with one pipeline step running stepRun.
func createGitProject(t *testing.T, srv *httptest.Server, name, repoIdentity, stepRun string, extraNodes ...map[string]any) {
	t.Helper()
	status, body := doReq(t, srv, http.MethodPost, "/api/v1/projects",
		gitProjectPayload(repoIdentity, name, stepRun, extraNodes...))
	if status != http.StatusCreated {
		t.Fatalf("create project %s: status %d, body %s", name, status, body)
	}
}

// putGitProject replaces an existing project definition.
func putGitProject(t *testing.T, srv *httptest.Server, repoIdentity, name, stepRun string) {
	t.Helper()
	status, body := doReq(t, srv, http.MethodPut, "/api/v1/projects/"+name,
		gitProjectPayload(repoIdentity, name, stepRun))
	if status != http.StatusOK {
		t.Fatalf("update project %s: status %d, body %s", name, status, body)
	}
}

// gitOK runs the git CLI in dir with a fixed identity and fails on error.
func gitOK(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(),
		"GIT_TERMINAL_PROMPT=0",
		"GIT_AUTHOR_NAME=PushRun Test",
		"GIT_AUTHOR_EMAIL=tester@pushrun.dev",
		"GIT_COMMITTER_NAME=PushRun Test",
		"GIT_COMMITTER_EMAIL=tester@pushrun.dev",
	)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %s: %v\n%s", strings.Join(args, " "), err, out)
	}
	return strings.TrimSpace(string(out))
}

// newFixtureRepo returns a non-bare repo on branch main with one commit and
// an origin whose repo identity is "github.com/acme/demo" (the client derives
// the push URL and the server-side project match from that identity).
func newFixtureRepo(t *testing.T) (dir, head string) {
	t.Helper()
	dir = t.TempDir()
	gitOK(t, dir, "init", "-q", "-b", "main")
	if err := os.WriteFile(filepath.Join(dir, "main.go"), []byte("package main\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	gitOK(t, dir, "add", "-A")
	gitOK(t, dir, "commit", "-qm", "initial")
	gitOK(t, dir, "remote", "add", "origin", "https://github.com/acme/demo.git")
	return dir, gitOK(t, dir, "rev-parse", "HEAD")
}

// writeScript renders data to an executable file in dir and returns its path.
func writeScript(t *testing.T, dir, name string, data []byte) string {
	t.Helper()
	p := filepath.Join(dir, name)
	if err := os.WriteFile(p, data, 0o755); err != nil {
		t.Fatal(err)
	}
	return p
}

// writeClientConfig seeds ~/.config/pushrun/config the way install.sh would.
func writeClientConfig(t *testing.T, home, serverURL string) {
	t.Helper()
	dir := filepath.Join(home, ".config", "pushrun")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	cfg := "server=" + serverURL + "\ntoken=" + testToken + "\n"
	if err := os.WriteFile(filepath.Join(dir, "config"), []byte(cfg), 0o600); err != nil {
		t.Fatal(err)
	}
}

// runScript executes script with bash. home overrides HOME; dir is the
// working directory. It returns the combined output and the error (nil on
// exit 0).
func runScript(t *testing.T, script, dir, home string, args ...string) (string, error) {
	t.Helper()
	argv := append([]string{script}, args...)
	cmd := exec.Command("bash", argv...)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(),
		"HOME="+home,
		"GIT_TERMINAL_PROMPT=0",
		"GIT_AUTHOR_NAME=PushRun Test",
		"GIT_AUTHOR_EMAIL=tester@pushrun.dev",
		"GIT_COMMITTER_NAME=PushRun Test",
		"GIT_COMMITTER_EMAIL=tester@pushrun.dev",
	)
	out, err := cmd.CombinedOutput()
	return string(out), err
}

func TestRenderScriptsEmbedServerAndVersion(t *testing.T) {
	for name, data := range map[string][]byte{
		"install.sh": client.RenderInstall("http://example.test:8000", "v1.2.3"),
		"client.sh":  client.RenderClient("http://example.test:8000", "v1.2.3"),
	} {
		s := string(data)
		if !strings.HasPrefix(s, "#!") {
			t.Fatalf("%s: missing shebang", name)
		}
		if !strings.Contains(s, "http://example.test:8000") {
			t.Fatalf("%s: server URL not baked in", name)
		}
		if !strings.Contains(s, "v1.2.3") {
			t.Fatalf("%s: version not baked in", name)
		}
		if strings.Contains(s, "{{") {
			t.Fatalf("%s: unrendered template placeholder", name)
		}
		// The token must ride in a 0600 curl config file (-K), never in
		// curl's argv where ps would show it. The git push path
		// uses env-scoped GIT_CONFIG_* instead, which is exempt.
		if strings.Contains(s, `-H "Authorization: Bearer $TOKEN"`) {
			t.Fatalf("%s: token passed via curl argv (visible in ps)", name)
		}
	}
}

func TestInstallWritesConfigCachesClientAndSetsAlias(t *testing.T) {
	srv, _, _ := newTestServer(t)
	home := t.TempDir()
	script := writeScript(t, t.TempDir(), "install.sh", client.RenderInstall(srv.URL, testVersion))

	out, err := runScript(t, script, home, home, "--token", testToken, "--server", srv.URL)
	if err != nil {
		t.Fatalf("install.sh failed: %v\n%s", err, out)
	}

	// Config written with the token, mode 0600.
	cfgPath := filepath.Join(home, ".config", "pushrun", "config")
	info, err := os.Stat(cfgPath)
	if err != nil {
		t.Fatalf("config not written: %v\noutput:\n%s", err, out)
	}
	if info.Mode().Perm() != 0o600 {
		t.Fatalf("config mode = %o, want 600", info.Mode().Perm())
	}
	cfgData, _ := os.ReadFile(cfgPath)
	if !strings.Contains(string(cfgData), "server="+srv.URL) ||
		!strings.Contains(string(cfgData), "token="+testToken) {
		t.Fatalf("config content wrong:\n%s", cfgData)
	}

	// Client script cached and executable.
	cached := filepath.Join(home, ".local", "share", "pushrun", "client.sh")
	cinfo, err := os.Stat(cached)
	if err != nil {
		t.Fatalf("client.sh not cached: %v", err)
	}
	if cinfo.Mode().Perm()&0o100 == 0 {
		t.Fatalf("cached client.sh not executable: %o", cinfo.Mode().Perm())
	}

	// Git alias installed.
	cmd := exec.Command("git", "config", "--global", "alias.pushrun")
	cmd.Env = append(os.Environ(), "HOME="+home)
	alias, err := cmd.Output()
	if err != nil {
		t.Fatalf("alias.pushrun not set: %v", err)
	}
	if got := strings.TrimSpace(string(alias)); got != "!~/.local/share/pushrun/client.sh" {
		t.Fatalf("alias.pushrun = %q, want %q", got, "!~/.local/share/pushrun/client.sh")
	}

	// The installer reports exactly what it changed.
	for _, want := range []string{".config/pushrun/config", ".local/share/pushrun/client.sh", "alias.pushrun"} {
		if !strings.Contains(out, want) {
			t.Fatalf("install output does not mention %q:\n%s", want, out)
		}
	}
}

func TestInstallRejectsBadTokenWithClearMessage(t *testing.T) {
	srv, _, _ := newTestServer(t)
	home := t.TempDir()
	script := writeScript(t, t.TempDir(), "install.sh", client.RenderInstall(srv.URL, testVersion))

	out, err := runScript(t, script, home, home, "--token", "wrong-token", "--server", srv.URL)
	if err == nil {
		t.Fatalf("install with a wrong token succeeded:\n%s", out)
	}
	if !strings.Contains(out, "401") && !strings.Contains(strings.ToLower(out), "unauthorized") {
		t.Fatalf("wrong-token failure is not actionable:\n%s", out)
	}
	if _, err := os.Stat(filepath.Join(home, ".config", "pushrun", "config")); err == nil {
		t.Fatal("config written even though the token was rejected")
	}
}

func TestStatusSendsBearerToken(t *testing.T) {
	srv, paths, rec := newTestServer(t)
	// status resolves the project server-side from the origin repo identity,
	// so the project must mount that repo.
	createGitProject(t, srv, "demo", "github.com/acme/demo", "echo hi")
	st := &instance.State{Project: "demo", Instance: "default", Status: instance.StatusSuccess, Branch: "main"}
	if err := instance.SaveState(paths, st); err != nil {
		t.Fatal(err)
	}
	home := t.TempDir()
	writeClientConfig(t, home, srv.URL)
	repo, _ := newFixtureRepo(t)
	script := writeScript(t, t.TempDir(), "client.sh", client.RenderClient(srv.URL, testVersion))

	out, err := runScript(t, script, repo, home, "status")
	if err != nil {
		t.Fatalf("status failed: %v\n%s", err, out)
	}
	if !strings.Contains(out, "SUCCESS") {
		t.Fatalf("status output missing instance status:\n%s", out)
	}
	if !rec.contains("GET /api/v1/instances/demo/default auth=Bearer " + testToken) {
		t.Fatalf("instance status request missing or unauthenticated:\n%s", rec.dump())
	}
}

// A failing pipeline step streams CI_STATUS=FAILED, and git push still exits
// 0 (post-receive exit codes are ignored) — the client must decide its exit
// code from the API. The step also prints a spoofed CI_STATUS=SUCCESS line:
// the streamed trailer is display only, the API is the truth.
func TestRunFailedBuildExitsNonZero(t *testing.T) {
	srv, paths, _ := newTestServer(t)
	createGitProject(t, srv, "demo", "github.com/acme/demo", "echo CI_STATUS=SUCCESS; exit 1")
	home := t.TempDir()
	writeClientConfig(t, home, srv.URL)
	repo, head := newFixtureRepo(t)
	script := writeScript(t, t.TempDir(), "client.sh", client.RenderClient(srv.URL, testVersion))

	out, err := runScript(t, script, repo, home, "run")
	if err == nil {
		t.Fatalf("run against a failing pipeline exited 0:\n%s", out)
	}
	if !strings.Contains(out, "CI_STATUS=FAILED") {
		t.Fatalf("sideband output missing CI_STATUS=FAILED:\n%s", out)
	}

	// The push stored the ref in the identity-keyed bare repo, and the
	// instance recorded the failure.
	repoDir, err := gitx.BareRepoDir(paths, "github.com/acme/demo")
	if err != nil {
		t.Fatal(err)
	}
	if got := gitOK(t, repo, "--git-dir="+repoDir, "rev-parse", "refs/pushrun/for/main"); got != head {
		t.Fatalf("refs/pushrun/for/main = %q, want %q", got, head)
	}
	st, err := instance.LoadState(paths, "demo", "default")
	if err != nil {
		t.Fatalf("no instance state after push: %v", err)
	}
	if st.Status != instance.StatusFailed {
		t.Fatalf("instance status = %q, want FAILED", st.Status)
	}
}

// Regression: the runs list is newest-first and the server emits compact
// one-line JSON, so the API-truth check must read the FIRST "status" on the
// line. A previous greedy sed pattern captured the oldest run's status,
// making every run after a failure exit non-zero forever.
func TestRunAfterFailureReadsLatestRunStatus(t *testing.T) {
	srv, _, _ := newTestServer(t)
	createGitProject(t, srv, "demo", "github.com/acme/demo", "exit 1")
	home := t.TempDir()
	writeClientConfig(t, home, srv.URL)
	repo, _ := newFixtureRepo(t)
	script := writeScript(t, t.TempDir(), "client.sh", client.RenderClient(srv.URL, testVersion))

	out, err := runScript(t, script, repo, home, "run")
	if err == nil {
		t.Fatalf("first run (failing pipeline) exited 0:\n%s", out)
	}

	// Fix the pipeline and push a new commit: the client must exit 0 based
	// on the NEWEST run's status, with the older FAILED run in the list.
	putGitProject(t, srv, "github.com/acme/demo", "demo", "echo recovered")
	if err := os.WriteFile(filepath.Join(repo, "second.txt"), []byte("v2"), 0o644); err != nil {
		t.Fatal(err)
	}
	gitOK(t, repo, "add", "-A")
	gitOK(t, repo, "commit", "-qm", "second")

	out, err = runScript(t, script, repo, home, "run")
	if err != nil {
		t.Fatalf("run after recovery failed: %v\n%s", err, out)
	}
	if !strings.Contains(out, "CI_STATUS=SUCCESS") {
		t.Fatalf("recovery run missing CI_STATUS=SUCCESS:\n%s", out)
	}

	// Sanity: the runs list really holds [SUCCESS, FAILED] in that order.
	status, body := doReq(t, srv, http.MethodGet, "/api/v1/instances/demo/default/runs", nil)
	if status != http.StatusOK {
		t.Fatalf("list runs: status %d, body %s", status, body)
	}
	succ := strings.Index(string(body), `"status":"SUCCESS"`)
	fail := strings.Index(string(body), `"status":"FAILED"`)
	if succ < 0 || fail < 0 || succ > fail {
		t.Fatalf("runs list not newest-first [SUCCESS, FAILED]: %s", body)
	}
}

// When the remote pushrun ref already points at HEAD, the client must not
// re-upload; it triggers the rerun API instead.
func TestRunUnchangedCommitTriggersRerun(t *testing.T) {
	srv, _, rec := newTestServer(t)
	createGitProject(t, srv, "demo", "github.com/acme/demo", "echo push-build-ran")
	home := t.TempDir()
	writeClientConfig(t, home, srv.URL)
	repo, _ := newFixtureRepo(t)
	script := writeScript(t, t.TempDir(), "client.sh", client.RenderClient(srv.URL, testVersion))

	out, err := runScript(t, script, repo, home, "run")
	if err != nil {
		t.Fatalf("first run failed: %v\n%s", err, out)
	}
	if !strings.Contains(out, "CI_STATUS=SUCCESS") {
		t.Fatalf("first run missing CI_STATUS=SUCCESS:\n%s", out)
	}

	rec.clear()
	out, err = runScript(t, script, repo, home, "run")
	if err != nil {
		t.Fatalf("second run (unchanged commit) failed: %v\n%s", err, out)
	}
	if !rec.contains("POST /api/v1/instances/demo/default/rerun") {
		t.Fatalf("unchanged commit did not route to the rerun API:\n%s", rec.dump())
	}
	if rec.contains("git-receive-pack") {
		t.Fatalf("unchanged commit still pushed:\n%s", rec.dump())
	}
}

// --instance flows through the push headers (X-PushRun-Instance) into the
// hook and the run engine.
func TestRunCustomInstance(t *testing.T) {
	srv, paths, _ := newTestServer(t)
	createGitProject(t, srv, "demo", "github.com/acme/demo", "echo hi")
	home := t.TempDir()
	writeClientConfig(t, home, srv.URL)
	repo, _ := newFixtureRepo(t)
	script := writeScript(t, t.TempDir(), "client.sh", client.RenderClient(srv.URL, testVersion))

	out, err := runScript(t, script, repo, home, "run", "--instance", "feature-x")
	if err != nil {
		t.Fatalf("run --instance feature-x failed: %v\n%s", err, out)
	}
	st, err := instance.LoadState(paths, "demo", "feature-x")
	if err != nil {
		t.Fatalf("no state for instance feature-x: %v", err)
	}
	if st.Status != instance.StatusSuccess {
		t.Fatalf("instance status = %q, want SUCCESS", st.Status)
	}
}

// A wrong stored token must fail with an actionable 401 message.
func TestStatusWrongTokenIsActionable(t *testing.T) {
	srv, _, _ := newTestServer(t)
	home := t.TempDir()
	dir := filepath.Join(home, ".config", "pushrun")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "config"),
		[]byte("server="+srv.URL+"\ntoken=wrong-token\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	repo, _ := newFixtureRepo(t)
	script := writeScript(t, t.TempDir(), "client.sh", client.RenderClient(srv.URL, testVersion))

	out, err := runScript(t, script, repo, home, "status")
	if err == nil {
		t.Fatalf("status with a wrong token succeeded:\n%s", out)
	}
	if !strings.Contains(out, "401") {
		t.Fatalf("wrong-token failure does not mention 401:\n%s", out)
	}
}

// logs subcommands must propagate HTTP failures to the exit code instead of
// masking them behind the formatting pipeline.
func TestLogsFailuresExitNonZero(t *testing.T) {
	srv, _, _ := newTestServer(t)
	home := t.TempDir()
	dir := filepath.Join(home, ".config", "pushrun")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "config"),
		[]byte("server="+srv.URL+"\ntoken=wrong-token\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	repo, _ := newFixtureRepo(t)
	script := writeScript(t, t.TempDir(), "client.sh", client.RenderClient(srv.URL, testVersion))

	for _, args := range [][]string{
		{"logs", "ls"},
		{"logs", "tree"},
		{"logs", "fetch", "app.log"},
		{"logs", "fetch", "app.log", "-f"},
	} {
		out, err := runScript(t, script, repo, home, args...)
		if err == nil {
			t.Fatalf("%v with a wrong token exited 0:\n%s", args, out)
		}
		if !strings.Contains(out, "401") {
			t.Fatalf("%v: failure does not mention 401:\n%s", args, out)
		}
	}
}

// The client's project resolution goes through the server's resolve endpoint:
// a project whose name shares nothing with the origin repo's basename must
// still be addressed correctly by status and by the unchanged-commit rerun
// shortcut.
func TestResolveEndpointDrivesStatusAndRerun(t *testing.T) {
	srv, paths, rec := newTestServer(t)
	createGitProject(t, srv, "web-frontend", "github.com/acme/demo", "echo ok")
	st := &instance.State{Project: "web-frontend", Instance: "default", Status: instance.StatusSuccess, Branch: "main"}
	if err := instance.SaveState(paths, st); err != nil {
		t.Fatal(err)
	}
	home := t.TempDir()
	writeClientConfig(t, home, srv.URL)
	repo, _ := newFixtureRepo(t)
	script := writeScript(t, t.TempDir(), "client.sh", client.RenderClient(srv.URL, testVersion))

	out, err := runScript(t, script, repo, home, "status")
	if err != nil {
		t.Fatalf("status failed: %v\n%s", err, out)
	}
	if !strings.Contains(out, "SUCCESS") {
		t.Fatalf("status output missing instance status:\n%s", out)
	}
	if !rec.contains("GET /api/v1/resolve") {
		t.Fatalf("status did not resolve the project via the server:\n%s", rec.dump())
	}
	if !rec.contains("GET /api/v1/instances/web-frontend/default") {
		t.Fatalf("status did not address the resolved project:\n%s", rec.dump())
	}

	// A push, then an unchanged re-push: the rerun shortcut must also target
	// the resolved project (origin basename "demo" != project "web-frontend").
	out, err = runScript(t, script, repo, home, "run")
	if err != nil {
		t.Fatalf("first run failed: %v\n%s", err, out)
	}
	rec.clear()
	out, err = runScript(t, script, repo, home, "run")
	if err != nil {
		t.Fatalf("second run (unchanged commit) failed: %v\n%s", err, out)
	}
	if !rec.contains("POST /api/v1/instances/web-frontend/default/rerun") {
		t.Fatalf("unchanged commit did not rerun the resolved project:\n%s", rec.dump())
	}
}

// A repo mounted only as a non-primary (satellite) git node cannot be
// auto-matched: the client relays the server's project_required error, prints
// an actionable --project hint, and exits non-zero — before any push happens.
func TestRunSatelliteRepoRequiresProject(t *testing.T) {
	srv, _, rec := newTestServer(t)
	priSrc, _ := newFixtureRepo(t) // local path; seeded offline by the git warmup
	createGitProject(t, srv, "demo", priSrc, "echo hi",
		gitNode("lib", "github.com/acme/demo", false))
	home := t.TempDir()
	writeClientConfig(t, home, srv.URL)
	repo, _ := newFixtureRepo(t)
	script := writeScript(t, t.TempDir(), "client.sh", client.RenderClient(srv.URL, testVersion))

	out, err := runScript(t, script, repo, home, "run")
	if err == nil {
		t.Fatalf("run from a satellite repo without --project exited 0:\n%s", out)
	}
	if !strings.Contains(out, "project_required:demo") {
		t.Fatalf("output missing the project_required code:\n%s", out)
	}
	if !strings.Contains(out, "--project") {
		t.Fatalf("output missing the actionable --project hint:\n%s", out)
	}
	if !rec.contains("GET /api/v1/resolve") {
		t.Fatalf("resolution did not go through the resolve endpoint:\n%s", rec.dump())
	}
	if rec.contains("git-receive-pack") {
		t.Fatalf("a push happened even though resolution failed:\n%s", rec.dump())
	}
}

// With --project the push of a satellite repo succeeds: the flag rides as the
// X-PushRun-Project header, the satellite node gets the pushed commit, and
// the primary node keeps its snapshot content.
func TestRunSatelliteRepoWithProjectFlag(t *testing.T) {
	srv, paths, rec := newTestServer(t)
	priSrc, _ := newFixtureRepo(t)
	createGitProject(t, srv, "demo", priSrc, "echo satellite-run-ok",
		gitNode("lib", "github.com/acme/demo", false))
	home := t.TempDir()
	writeClientConfig(t, home, srv.URL)
	repo, _ := newFixtureRepo(t)
	// Marker file distinguishing the pushed (satellite) content from the
	// seeded primary content — both fixtures carry the same main.go.
	if err := os.WriteFile(filepath.Join(repo, "sat.txt"), []byte("from-the-push\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	gitOK(t, repo, "add", "-A")
	gitOK(t, repo, "commit", "-qm", "satellite commit")
	head := gitOK(t, repo, "rev-parse", "HEAD")
	script := writeScript(t, t.TempDir(), "client.sh", client.RenderClient(srv.URL, testVersion))

	out, err := runScript(t, script, repo, home, "run", "--project", "demo")
	if err != nil {
		t.Fatalf("run --project demo from a satellite repo failed: %v\n%s", err, out)
	}
	if !strings.Contains(out, "CI_STATUS=SUCCESS") {
		t.Fatalf("run missing CI_STATUS=SUCCESS:\n%s", out)
	}
	// An explicit project is asserted, not resolved: no resolve call.
	if rec.contains("GET /api/v1/resolve") {
		t.Fatalf("--project still hit the resolve endpoint:\n%s", rec.dump())
	}

	st, err := instance.LoadState(paths, "demo", "default")
	if err != nil {
		t.Fatalf("no instance state after push: %v", err)
	}
	if st.Commit != head {
		t.Fatalf("instance commit = %q, want pushed %q", st.Commit, head)
	}
	instDir := filepath.Join(paths.Instances, "demo", "default")
	if data, err := os.ReadFile(filepath.Join(instDir, "lib", "sat.txt")); err != nil || string(data) != "from-the-push\n" {
		t.Fatalf("trigger node did not get the pushed commit: %v %q", err, data)
	}
	if _, err := os.Stat(filepath.Join(instDir, "app", "sat.txt")); err == nil {
		t.Fatal("primary node got the pushed commit; want its snapshot content")
	}
}

// A repo identity containing query-significant characters ("+", "&", "=")
// must survive url-encoding on the resolve call; a broken encoding would make
// the server answer unknown_repo and fail the command.
func TestResolveEncodesPathSpecials(t *testing.T) {
	srv, paths, _ := newTestServer(t)
	createGitProject(t, srv, "specials", "github.com/a+b&c=d/demo", "echo ok")
	st := &instance.State{Project: "specials", Instance: "default", Status: instance.StatusSuccess, Branch: "main"}
	if err := instance.SaveState(paths, st); err != nil {
		t.Fatal(err)
	}
	home := t.TempDir()
	writeClientConfig(t, home, srv.URL)
	repo, _ := newFixtureRepo(t)
	gitOK(t, repo, "remote", "set-url", "origin", "https://github.com/a+b&c=d/demo.git")
	script := writeScript(t, t.TempDir(), "client.sh", client.RenderClient(srv.URL, testVersion))

	out, err := runScript(t, script, repo, home, "status")
	if err != nil {
		t.Fatalf("status with special characters in the identity failed: %v\n%s", err, out)
	}
	if !strings.Contains(out, "SUCCESS") {
		t.Fatalf("status output missing instance status:\n%s", out)
	}
}

// The push failure branch: a rejected push (unknown_project from pre-receive)
// must exit non-zero with the server's rejection visible, and an empty
// --project= value is a usage error.
func TestRunUnknownProjectExitsNonZero(t *testing.T) {
	srv, _, _ := newTestServer(t)
	repo, _ := newFixtureRepo(t)
	home := t.TempDir()
	writeClientConfig(t, home, srv.URL)
	script := writeScript(t, t.TempDir(), "client.sh", client.RenderClient(srv.URL, testVersion))

	out, err := runScript(t, script, repo, home, "run", "--project", "nonexistent")
	if err == nil {
		t.Fatalf("run --project nonexistent exited 0:\n%s", out)
	}
	if !strings.Contains(out, "unknown_project") {
		t.Fatalf("rejection message missing unknown_project:\n%s", out)
	}

	out, err = runScript(t, script, repo, home, "run", "--project=")
	if err == nil {
		t.Fatalf("run --project= (empty) exited 0:\n%s", out)
	}
	if !strings.Contains(out, "--project needs a value") {
		t.Fatalf("empty --project did not produce a usage error:\n%s", out)
	}
}
