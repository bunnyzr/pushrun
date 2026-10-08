package server_test

import (
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/bunnyzr/pushrun/internal/config"
	"github.com/bunnyzr/pushrun/internal/gitx"
	"github.com/bunnyzr/pushrun/internal/hookclient"
	"github.com/bunnyzr/pushrun/internal/instance"
	"github.com/bunnyzr/pushrun/internal/project"
	"github.com/bunnyzr/pushrun/internal/testutil"
)

// TestMain doubles the test binary as the pushrun hook executable: the hook
// scripts installed into test bare repos exec os.Executable() (this test
// binary) as "hook pre-receive" / "hook post-receive".
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

// mustExecutable returns the test binary, which doubles as the pushrun hook
// executable (see TestMain).
func mustExecutable(t *testing.T) string {
	t.Helper()
	bin, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	return bin
}

// gitCmd runs the git CLI with a fixed identity and returns its combined
// output; unlike gitOK it does not fail the test, so callers can assert on
// failing pushes.
func gitCmd(dir string, args ...string) (string, error) {
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(),
		"GIT_TERMINAL_PROMPT=0",
		"GIT_AUTHOR_NAME=PushRun Test",
		"GIT_AUTHOR_EMAIL=test@pushrun.dev",
		"GIT_COMMITTER_NAME=PushRun Test",
		"GIT_COMMITTER_EMAIL=test@pushrun.dev",
	)
	out, err := cmd.CombinedOutput()
	return string(out), err
}

func gitOK(t *testing.T, dir string, args ...string) string {
	t.Helper()
	out, err := gitCmd(dir, args...)
	if err != nil {
		t.Fatalf("git %s: %v\n%s", strings.Join(args, " "), err, out)
	}
	return strings.TrimSpace(out)
}

// newPushFixtureRepo returns a non-bare repo on branch main with one commit
// holding main.go.
func newPushFixtureRepo(t *testing.T) (dir, head string) {
	t.Helper()
	return newPushFixtureRepoWith(t, map[string]string{"main.go": "package main\n"})
}

// newPushFixtureRepoWith returns a non-bare repo on branch main with one
// commit holding the given files.
func newPushFixtureRepoWith(t *testing.T, files map[string]string) (dir, head string) {
	t.Helper()
	dir = t.TempDir()
	gitOK(t, dir, "init", "-q", "-b", "main")
	for name, content := range files {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	gitOK(t, dir, "add", "-A")
	gitOK(t, dir, "commit", "-qm", "initial")
	return dir, gitOK(t, dir, "rev-parse", "HEAD")
}

// commitPushFixture adds a commit with the given files and returns its SHA.
func commitPushFixture(t *testing.T, dir string, files map[string]string) string {
	t.Helper()
	for name, content := range files {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	gitOK(t, dir, "add", "-A")
	gitOK(t, dir, "commit", "-qm", "update")
	return gitOK(t, dir, "rev-parse", "HEAD")
}

// createGitProject registers a project whose primary git node (at path
// "app") mounts repo, running one pipeline step.
func createGitProject(t *testing.T, srv *httptest.Server, name, repo, stepRun string) {
	t.Helper()
	createGitProjectWithNodes(t, srv, name, stepRun,
		map[string]any{"path": "app", "mount": map[string]any{
			"provider": "git", "primary": true,
			"params": map[string]string{"repo": repo, "branch": "main"},
		}})
}

// createGitProjectWithNodes registers a project with the given tree nodes
// and one pipeline step.
func createGitProjectWithNodes(t *testing.T, srv *httptest.Server, name, stepRun string, nodes ...map[string]any) {
	t.Helper()
	proj := map[string]any{
		"name":     name,
		"tree":     nodes,
		"pipeline": []any{map[string]any{"name": "build", "run": stepRun}},
	}
	status, _, body := doReq(t, srv, http.MethodPost, "/api/v1/projects", testToken, proj)
	if status != http.StatusCreated {
		t.Fatalf("create project %s: status %d, body %s", name, status, body)
	}
}

// gitNode builds a tree node mounting repo (branch main) at path.
func gitNode(path, repo string, primary bool) map[string]any {
	return map[string]any{"path": path, "mount": map[string]any{
		"provider": "git", "primary": primary,
		"params": map[string]string{"repo": repo, "branch": "main"},
	}}
}

// gitURL returns the push URL for a repo identity with HTTP Basic
// credentials (password is the daemon token).
func gitURL(srv *httptest.Server, repoIdentity string) string {
	return strings.Replace(srv.URL, "http://", "http://ci:"+testToken+"@", 1) + "/git/" + repoIdentity + ".git"
}

// pushHead pushes the fixture's HEAD to refs/pushrun/for/main of the given
// repo identity, with optional pushrun metadata headers ("Key: Value").
func pushHead(t *testing.T, src string, srv *httptest.Server, repoIdentity string, headers ...string) (string, error) {
	t.Helper()
	args := []string{}
	for _, h := range headers {
		args = append(args, "-c", "http.extraHeader="+h)
	}
	args = append(args, "push", gitURL(srv, repoIdentity), "HEAD:refs/pushrun/for/main")
	return gitCmd(src, args...)
}

// bareDir returns the on-disk bare repo dir for a repo identity.
func bareDir(t *testing.T, paths config.Paths, repoIdentity string) string {
	t.Helper()
	dir, err := gitx.BareRepoDir(paths, repoIdentity)
	if err != nil {
		t.Fatalf("BareRepoDir(%q): %v", repoIdentity, err)
	}
	return dir
}

func TestGitPushEndToEnd(t *testing.T) {
	srv, paths := newTestServer(t)
	createGitProject(t, srv, "demo", "git.example.com/team/demo", "echo push-build-ran")
	src, head := newPushFixtureRepo(t)

	out, err := pushHead(t, src, srv, "git.example.com/team/demo")
	if err != nil {
		t.Fatalf("push failed: %v\n%s", err, out)
	}
	// Build output streamed back over the sideband channel.
	if !strings.Contains(out, "push-build-ran") {
		t.Fatalf("sideband output missing build log:\n%s", out)
	}
	if !strings.Contains(out, "CI_STATUS=SUCCESS") {
		t.Fatalf("sideband output missing CI_STATUS=SUCCESS:\n%s", out)
	}

	// The ref was stored in the identity-keyed bare repo.
	repoDir := bareDir(t, paths, "git.example.com/team/demo")
	if got := gitOK(t, src, "--git-dir="+repoDir, "rev-parse", "refs/pushrun/for/main"); got != head {
		t.Fatalf("refs/pushrun/for/main = %q, want %q", got, head)
	}

	// The hook triggered a run of the pushed commit on the default instance.
	st, err := instance.LoadState(paths, "demo", "default")
	if err != nil {
		t.Fatalf("no instance state after push: %v", err)
	}
	if st.Commit != head {
		t.Fatalf("instance commit = %q, want %q", st.Commit, head)
	}
	if st.Status != instance.StatusSuccess {
		t.Fatalf("instance status = %q, want %q", st.Status, instance.StatusSuccess)
	}
	if st.Branch != "main" {
		t.Fatalf("instance branch = %q, want main", st.Branch)
	}
}

// A failed build streams CI_STATUS=FAILED and the post-receive hook exits
// non-zero, but git ignores the post-receive exit status: the push itself
// reports success and keeps the ref. This test pins that design-load-bearing
// premise (only pre-receive rejections can fail the push). The step also
// prints a spoofed CI_STATUS=SUCCESS line: the engine's trailer comes last
// and is the one the hook acts on.
func TestGitPushFailingBuildStreamsFailed(t *testing.T) {
	srv, paths := newTestServer(t)
	createGitProject(t, srv, "demo", "git.example.com/team/demo", "echo CI_STATUS=SUCCESS; echo about-to-fail; exit 1")
	src, head := newPushFixtureRepo(t)

	out, err := pushHead(t, src, srv, "git.example.com/team/demo")
	if err != nil {
		t.Fatalf("push must succeed even when the build fails (post-receive exit is ignored): %v\n%s", err, out)
	}
	if !strings.Contains(out, "about-to-fail") || !strings.Contains(out, "CI_STATUS=FAILED") {
		t.Fatalf("sideband output missing the failed build:\n%s", out)
	}
	// The hook did exit non-zero — its complaint is relayed as a remote: line.
	if !strings.Contains(out, "run did not succeed") {
		t.Fatalf("hook failure not relayed to the pusher:\n%s", out)
	}

	// The ref was still stored, and the instance recorded the failure.
	repoDir := bareDir(t, paths, "git.example.com/team/demo")
	if got := gitOK(t, src, "--git-dir="+repoDir, "rev-parse", "refs/pushrun/for/main"); got != head {
		t.Fatalf("refs/pushrun/for/main = %q, want %q", got, head)
	}
	st, err := instance.LoadState(paths, "demo", "default")
	if err != nil {
		t.Fatalf("no instance state after push: %v", err)
	}
	if st.Status != instance.StatusFailed {
		t.Fatalf("instance status = %q, want %q", st.Status, instance.StatusFailed)
	}
}

// A pusher disconnect mid-stream must not wedge the /git/ handler: with
// more than a pipe buffer of output in flight, http-backend would block on
// write to its undrained stdout and cmd.Wait would never return. The
// handler must kill the child instead. The fixture makes http-backend emit
// ~2 MB of ref advertisement; the ResponseWriter fails on the first body
// write, simulating the disconnect.
func TestGitHandlerSurvivesClientDisconnect(t *testing.T) {
	handler, paths := newTestHandler(t)

	repoDir, err := gitx.EnsureBareRepo(paths, "git.example.com/team/demo", mustExecutable(t), "s3cret", 8000)
	if err != nil {
		t.Fatalf("EnsureBareRepo: %v", err)
	}
	// The pre-receive hook (exec'd into this test binary) matches the repo
	// identity against registered projects.
	if err := project.Save(paths, &project.Project{
		Name: "demo",
		Tree: []project.Node{{Path: "app", Mount: &project.Mount{Provider: "git", Primary: true,
			Params: map[string]string{"repo": "git.example.com/team/demo", "branch": "main"}}}},
	}); err != nil {
		t.Fatal(err)
	}
	// One real object to hang the tags on.
	src, head := newPushFixtureRepo(t)
	gitOK(t, src, "push", repoDir, "HEAD:refs/pushrun/for/main")

	// ~2 MB of advertisement: 30000 tags × ~65 bytes each. That exceeds even
	// the largest pipe buffer any supported platform grows to (macOS big
	// pipes cap at 512 KB), so an undrained http-backend would block on
	// write and hang cmd.Wait.
	var sb strings.Builder
	for i := range 30000 {
		fmt.Fprintf(&sb, "create refs/tags/t%05d %s\n", i, head)
	}
	mkrefs := exec.Command("git", "--git-dir="+repoDir, "update-ref", "--stdin")
	mkrefs.Stdin = strings.NewReader(sb.String())
	if out, err := mkrefs.CombinedOutput(); err != nil {
		t.Fatalf("seed tags: %v\n%s", err, out)
	}

	req := httptest.NewRequest(http.MethodGet, "/git/git.example.com/team/demo.git/info/refs?service=git-upload-pack", nil)
	req.RemoteAddr = "127.0.0.1:1"
	req.Host = "127.0.0.1:8000" // the handler derives the hook callback port from Host
	req.Header.Set("Authorization", "Bearer "+testToken)

	done := make(chan struct{})
	go func() {
		handler.ServeHTTP(&disconnectWriter{header: http.Header{}}, req)
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(30 * time.Second):
		t.Fatal("/git/ handler hung after client disconnect (http-backend left blocked on its undrained stdout pipe)")
	}
}

// disconnectWriter simulates a client that vanished: headers are accepted,
// the first body write fails.
type disconnectWriter struct {
	header http.Header
	code   int
}

func (d *disconnectWriter) Header() http.Header  { return d.header }
func (d *disconnectWriter) WriteHeader(code int) { d.code = code }
func (d *disconnectWriter) Write([]byte) (int, error) {
	return 0, errors.New("client disconnected")
}

func TestGitPushRejectsNonPushrunRef(t *testing.T) {
	srv, _ := newTestServer(t)
	createGitProject(t, srv, "demo", "git.example.com/team/demo", "echo hi")
	src, _ := newPushFixtureRepo(t)

	out, err := gitCmd(src, "push", gitURL(srv, "git.example.com/team/demo"), "HEAD:refs/heads/main")
	if err == nil {
		t.Fatalf("push to refs/heads/main succeeded, want rejection:\n%s", out)
	}
	if !strings.Contains(out, "refs/pushrun/for/") {
		t.Fatalf("rejection does not name the allowed namespace:\n%s", out)
	}
}

func TestGitPushUnknownRepo(t *testing.T) {
	srv, _ := newTestServer(t)
	src, _ := newPushFixtureRepo(t)

	out, err := pushHead(t, src, srv, "git.example.com/team/nope")
	if err == nil {
		t.Fatalf("push to unknown repo succeeded, want failure:\n%s", out)
	}
	if !strings.Contains(out, "unknown_repo:git.example.com/team/nope") {
		t.Fatalf("error is not actionable (want 'unknown_repo:<identity>'):\n%s", out)
	}
}

// The old project-named git path shape (/git/<project>.git) no longer
// resolves: repo identities are host/path.
func TestGitPathRequiresRepoIdentity(t *testing.T) {
	srv, _ := newTestServer(t)
	createGitProject(t, srv, "demo", "git.example.com/team/demo", "echo hi")

	status, _, _ := doReq(t, srv, http.MethodGet,
		"/git/demo.git/info/refs?service=git-receive-pack", testToken, nil)
	if status != http.StatusNotFound {
		t.Fatalf("project-named git path: status %d, want 404", status)
	}
	// A host/path identity parses (auth runs next, so a good token reaches
	// the backend).
	status, _, _ = doReq(t, srv, http.MethodGet,
		"/git/git.example.com/team/demo.git/info/refs?service=git-receive-pack", testToken, nil)
	if status != http.StatusOK {
		t.Fatalf("repo-identity git path: status %d, want 200", status)
	}
}

func TestGitPushRequiresAuth(t *testing.T) {
	srv, _ := newTestServer(t)
	createGitProject(t, srv, "demo", "git.example.com/team/demo", "echo hi")
	src, _ := newPushFixtureRepo(t)

	// The unauthenticated info/refs probe is challenged with a Basic realm
	// (this is what makes git prompt for credentials interactively).
	status, hdr, _ := doReq(t, srv, http.MethodGet,
		"/git/git.example.com/team/demo.git/info/refs?service=git-receive-pack", "", nil)
	if status != http.StatusUnauthorized {
		t.Fatalf("unauthenticated info/refs: status %d, want 401", status)
	}
	if !strings.Contains(hdr.Get("WWW-Authenticate"), "Basic") {
		t.Fatalf("missing Basic challenge, WWW-Authenticate = %q", hdr.Get("WWW-Authenticate"))
	}

	out, err := gitCmd(src, "push", srv.URL+"/git/git.example.com/team/demo.git", "HEAD:refs/pushrun/for/main")
	if err == nil {
		t.Fatalf("unauthenticated push succeeded:\n%s", out)
	}

	// A wrong password is rejected too ("Authentication failed" follows the
	// daemon's 401 challenge).
	badURL := strings.Replace(srv.URL, "http://", "http://ci:wrong@", 1) + "/git/git.example.com/team/demo.git"
	out, err = gitCmd(src, "push", badURL, "HEAD:refs/pushrun/for/main")
	if err == nil {
		t.Fatalf("push with wrong token succeeded:\n%s", out)
	}
	if !strings.Contains(out, "Authentication failed") {
		t.Fatalf("expected an authentication failure:\n%s", out)
	}
}

// A push from a repo mounted as a non-primary (satellite) git node, carrying
// the explicit X-PushRun-Project header, triggers that project: the matching
// node gets the pushed commit, the primary node keeps its snapshot content,
// and the CI_TRIGGER_*/CI_PROJECT_* env pairs name the two nodes.
func TestGitPushFromNonPrimaryNode(t *testing.T) {
	srv, paths := newTestServer(t)
	priSrc, _ := newPushFixtureRepoWith(t, map[string]string{"hello.txt": "app-v1\n"})
	satSrc, _ := newPushFixtureRepoWith(t, map[string]string{"hello.txt": "lib-v1\n"})
	satHead2 := commitPushFixture(t, satSrc, map[string]string{"hello.txt": "lib-v2\n"})

	createGitProjectWithNodes(t, srv, "demo",
		"cat app/hello.txt > seen-app.txt; cat lib/hello.txt > seen-lib.txt; "+
			`env | grep -E '^CI_(WORKSPACE|TRIGGER_PROJECT_|PROJECT_)' | sort > ci-env.txt`,
		gitNode("app", priSrc, true),
		gitNode("lib", "git.example.com/team/lib", false),
	)

	out, err := pushHead(t, satSrc, srv, "git.example.com/team/lib", "X-PushRun-Project: demo")
	if err != nil {
		t.Fatalf("push from non-primary node failed: %v\n%s", err, out)
	}
	if !strings.Contains(out, "CI_STATUS=SUCCESS") {
		t.Fatalf("run not successful:\n%s", out)
	}

	instDir := filepath.Join(paths.Instances, "demo", "default")
	assertFileContent(t, filepath.Join(instDir, "seen-lib.txt"), "lib-v2\n") // trigger node: pushed commit
	assertFileContent(t, filepath.Join(instDir, "seen-app.txt"), "app-v1\n") // primary node: snapshot

	// The pushed commit is the run's commit.
	st, err := instance.LoadState(paths, "demo", "default")
	if err != nil {
		t.Fatalf("no instance state: %v", err)
	}
	if st.Commit != satHead2 {
		t.Fatalf("instance commit = %q, want pushed %q", st.Commit, satHead2)
	}

	// Env parity: trigger pair names the satellite node, project pair the
	// primary node.
	priIdentity, err := gitx.NormalizeRepo(priSrc)
	if err != nil {
		t.Fatal(err)
	}
	env := readFile(t, filepath.Join(instDir, "ci-env.txt"))
	for _, want := range []string{
		"CI_WORKSPACE=" + instDir,
		"CI_TRIGGER_PROJECT_DIR=" + filepath.Join(instDir, "lib"),
		"CI_TRIGGER_PROJECT_NAME=team/lib",
		"CI_PROJECT_DIR=" + filepath.Join(instDir, "app"),
		"CI_PROJECT_NAME=" + gitx.RepoPath(priIdentity),
	} {
		if !strings.Contains(env, want+"\n") && !strings.HasSuffix(env, want) {
			t.Errorf("ci-env.txt missing %q:\n%s", want, env)
		}
	}
}

// A push from a repo mounted only as a non-primary node, without an explicit
// project, is rejected at pre-receive with the candidate project names.
func TestGitPushFromNonPrimaryNodeRequiresProject(t *testing.T) {
	srv, _ := newTestServer(t)
	createGitProjectWithNodes(t, srv, "demo", "echo hi",
		gitNode("app", "git.example.com/team/app", true),
		gitNode("lib", "git.example.com/team/lib", false),
	)
	src, _ := newPushFixtureRepo(t)

	out, err := pushHead(t, src, srv, "git.example.com/team/lib")
	if err == nil {
		t.Fatalf("push from non-primary node without explicit project succeeded:\n%s", out)
	}
	if !strings.Contains(out, "project_required:demo") {
		t.Fatalf("rejection does not name the candidate project:\n%s", out)
	}
}

// A repo mounted by two projects cannot be auto-matched; the rejection lists
// both candidates.
func TestGitPushAmbiguousProject(t *testing.T) {
	srv, _ := newTestServer(t)
	createGitProject(t, srv, "demo-a", "git.example.com/team/shared", "echo hi")
	createGitProject(t, srv, "demo-b", "git.example.com/team/shared", "echo hi")
	src, _ := newPushFixtureRepo(t)

	out, err := pushHead(t, src, srv, "git.example.com/team/shared")
	if err == nil {
		t.Fatalf("push to a shared repo succeeded, want rejection:\n%s", out)
	}
	if !strings.Contains(out, "ambiguous_project:demo-a,demo-b") {
		t.Fatalf("rejection does not list both candidates:\n%s", out)
	}
}

// An explicit project that does not mount the pushed repo is rejected at
// pre-receive.
func TestGitPushExplicitProjectMismatch(t *testing.T) {
	srv, _ := newTestServer(t)
	createGitProject(t, srv, "demo", "git.example.com/team/demo", "echo hi")
	createGitProject(t, srv, "other", "git.example.com/team/other", "echo hi")
	src, _ := newPushFixtureRepo(t)

	out, err := pushHead(t, src, srv, "git.example.com/team/demo", "X-PushRun-Project: other")
	if err == nil {
		t.Fatalf("push with a mismatched explicit project succeeded:\n%s", out)
	}
	if !strings.Contains(out, "project_does_not_contain_repo:other") {
		t.Fatalf("rejection does not name the mismatched project:\n%s", out)
	}
}

// X-PushRun-Instance flows through the push into the run engine.
func TestGitPushCustomInstance(t *testing.T) {
	srv, paths := newTestServer(t)
	createGitProject(t, srv, "demo", "git.example.com/team/demo", "echo hi")
	src, _ := newPushFixtureRepo(t)

	out, err := pushHead(t, src, srv, "git.example.com/team/demo", "X-PushRun-Instance: feature-x")
	if err != nil {
		t.Fatalf("push failed: %v\n%s", err, out)
	}
	st, err := instance.LoadState(paths, "demo", "feature-x")
	if err != nil {
		t.Fatalf("no state for instance feature-x: %v", err)
	}
	if st.Status != instance.StatusSuccess {
		t.Fatalf("instance status = %q, want SUCCESS", st.Status)
	}
}

// The internal hook endpoint is guarded by the hook secret (never the user
// token) and by loopback-only access.
func TestHookEndpointAuth(t *testing.T) {
	handler, paths := newTestHandler(t)

	secret, err := config.EnsureHookSecret(paths.Root)
	if err != nil {
		t.Fatalf("read hook secret: %v", err)
	}
	if secret == "" {
		t.Fatal("empty hook secret")
	}

	post := func(remoteAddr, secretHeader string, hdrs map[string]string) (int, []byte) {
		t.Helper()
		req := httptest.NewRequest(http.MethodPost, "/internal/v1/hooks/post-receive", nil)
		req.RemoteAddr = remoteAddr
		if secretHeader != "" {
			req.Header.Set("X-PushRun-Hook-Secret", secretHeader)
		}
		for k, v := range hdrs {
			req.Header.Set(k, v)
		}
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, req)
		return rec.Code, rec.Body.Bytes()
	}

	meta := map[string]string{
		"X-PushRun-Repo":   "git.example.com/team/ghost",
		"X-PushRun-Branch": "main",
		"X-PushRun-Commit": strings.Repeat("a", 40),
	}

	// Wrong secret → 403, even from loopback.
	status, _ := post("127.0.0.1:1", "wrong-secret", meta)
	if status != http.StatusForbidden {
		t.Fatalf("wrong secret: status %d, want 403", status)
	}
	// Missing secret → 403.
	status, _ = post("127.0.0.1:1", "", meta)
	if status != http.StatusForbidden {
		t.Fatalf("missing secret: status %d, want 403", status)
	}
	// The user token is NOT a valid hook secret.
	status, _ = post("127.0.0.1:1", testToken, meta)
	if status != http.StatusForbidden {
		t.Fatalf("user token accepted as hook secret: status %d, want 403", status)
	}
	// Non-loopback peer → 403 even with the right secret.
	status, _ = post("10.1.2.3:4444", secret, meta)
	if status != http.StatusForbidden {
		t.Fatalf("non-loopback: status %d, want 403", status)
	}
	// Right secret from loopback, unknown repo → 200 with an actionable
	// streamed error and a FAILED trailer (the hook relays this and exits
	// non-zero).
	status, body := post("127.0.0.1:1", secret, meta)
	if status != http.StatusOK {
		t.Fatalf("hook call: status %d, body %s", status, body)
	}
	if !strings.Contains(string(body), "unknown_repo:git.example.com/team/ghost") ||
		!strings.Contains(string(body), "CI_STATUS=FAILED") {
		t.Fatalf("unknown repo body not actionable: %s", body)
	}
	// Missing repo metadata → 400 (a stale pre-identity hook sends only
	// X-PushRun-Project).
	status, _ = post("127.0.0.1:1", secret, map[string]string{
		"X-PushRun-Project": "demo",
		"X-PushRun-Branch":  "main",
	})
	if status != http.StatusBadRequest {
		t.Fatalf("missing repo metadata: status %d, want 400", status)
	}
}

func assertFileContent(t *testing.T, path, want string) {
	t.Helper()
	if got := readFile(t, path); got != want {
		t.Fatalf("%s = %q, want %q", path, got, want)
	}
}

func readFile(t *testing.T, path string) string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	return string(data)
}
