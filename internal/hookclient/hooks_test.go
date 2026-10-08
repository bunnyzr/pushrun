package hookclient_test

import (
	"bytes"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/bunnyzr/pushrun/internal/config"
	"github.com/bunnyzr/pushrun/internal/hookclient"
	"github.com/bunnyzr/pushrun/internal/project"
)

const (
	testOldSHA = "1111111111111111111111111111111111111111"
	testNewSHA = "2222222222222222222222222222222222222222"
	zeroSHA    = "0000000000000000000000000000000000000000"
	testRepo   = "git.example.com/team/demo"
)

func testEnv(port string) hookclient.Env {
	return hookclient.Env{
		Port:   port,
		Secret: "s3cret",
		Repo:   testRepo,
		User:   "alice",
		Host:   "ci.example.com",
	}
}

// fakeDaemon serves the internal hook endpoint, echoing the received
// metadata into a channel and responding with the given body/status.
func fakeDaemon(t *testing.T, status int, body string, got chan<- http.Header) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/internal/v1/hooks/post-receive" {
			http.Error(w, "bad path", http.StatusNotFound)
			return
		}
		if got != nil {
			got <- r.Header.Clone()
		}
		w.WriteHeader(status)
		_, _ = io.WriteString(w, body)
	}))
	t.Cleanup(srv.Close)
	return srv
}

// serverPort extracts the port from an httptest server URL.
func serverPort(srv *httptest.Server) string {
	return srv.URL[strings.LastIndex(srv.URL, ":")+1:]
}

func pushStdin(refs ...string) io.Reader {
	var lines []string
	for _, ref := range refs {
		lines = append(lines, testOldSHA+" "+testNewSHA+" "+ref)
	}
	return strings.NewReader(strings.Join(lines, "\n") + "\n")
}

func TestPostReceiveSuccess(t *testing.T) {
	got := make(chan http.Header, 1)
	srv := fakeDaemon(t, http.StatusOK, "building...\nCI_STATUS=SUCCESS\n", got)

	var out bytes.Buffer
	env := testEnv(serverPort(srv))
	err := hookclient.PostReceive(pushStdin("refs/pushrun/for/main"), &out, env)
	if err != nil {
		t.Fatalf("PostReceive: %v", err)
	}
	if !strings.Contains(out.String(), "building...") || !strings.Contains(out.String(), "CI_STATUS=SUCCESS") {
		t.Fatalf("daemon output not relayed to stdout: %q", out.String())
	}

	h := <-got
	if h.Get("X-PushRun-Hook-Secret") != "s3cret" {
		t.Fatalf("secret header = %q", h.Get("X-PushRun-Hook-Secret"))
	}
	if h.Get("X-PushRun-Repo") != testRepo {
		t.Fatalf("repo header = %q, want %q", h.Get("X-PushRun-Repo"), testRepo)
	}
	if h.Get("X-PushRun-Project") != "" {
		t.Fatalf("project header sent without an explicit project: %q", h.Get("X-PushRun-Project"))
	}
	if h.Get("X-PushRun-Branch") != "main" ||
		h.Get("X-PushRun-Commit") != testNewSHA || h.Get("X-PushRun-User") != "alice" {
		t.Fatalf("metadata headers wrong: %v", h)
	}
	if h.Get("X-PushRun-Action") != "run" || h.Get("X-PushRun-Instance") != "default" {
		t.Fatalf("defaults not applied: action=%q instance=%q",
			h.Get("X-PushRun-Action"), h.Get("X-PushRun-Instance"))
	}
	if h.Get("X-PushRun-Host") != "ci.example.com" {
		t.Fatalf("display host header = %q, want ci.example.com", h.Get("X-PushRun-Host"))
	}
}

// An explicit project asserted by the pusher rides as X-PushRun-Project.
func TestPostReceiveForwardsExplicitProject(t *testing.T) {
	got := make(chan http.Header, 1)
	srv := fakeDaemon(t, http.StatusOK, "CI_STATUS=SUCCESS\n", got)

	var out bytes.Buffer
	env := testEnv(serverPort(srv))
	env.Project = "demo"
	if err := hookclient.PostReceive(pushStdin("refs/pushrun/for/main"), &out, env); err != nil {
		t.Fatalf("PostReceive: %v", err)
	}
	h := <-got
	if h.Get("X-PushRun-Project") != "demo" {
		t.Fatalf("project header = %q, want demo", h.Get("X-PushRun-Project"))
	}
	if h.Get("X-PushRun-Repo") != testRepo {
		t.Fatalf("repo header = %q, want %q", h.Get("X-PushRun-Repo"), testRepo)
	}
}

func TestPostReceiveFailedRun(t *testing.T) {
	for _, status := range []string{"FAILED", "BUSY"} {
		srv := fakeDaemon(t, http.StatusOK, "oops\nCI_STATUS="+status+"\n", nil)
		var out bytes.Buffer
		err := hookclient.PostReceive(pushStdin("refs/pushrun/for/main"), &out, testEnv(serverPort(srv)))
		if err == nil {
			t.Fatalf("CI_STATUS=%s: PostReceive succeeded, want error", status)
		}
		if !strings.Contains(out.String(), "oops") {
			t.Fatalf("CI_STATUS=%s: daemon output not relayed: %q", status, out.String())
		}
	}
}

func TestPostReceiveDaemonError(t *testing.T) {
	srv := fakeDaemon(t, http.StatusForbidden, "bad secret\n", nil)
	var out bytes.Buffer
	err := hookclient.PostReceive(pushStdin("refs/pushrun/for/main"), &out, testEnv(serverPort(srv)))
	if err == nil {
		t.Fatal("PostReceive succeeded against a 403, want error")
	}
	if !strings.Contains(out.String(), "bad secret") {
		t.Fatalf("error body not relayed: %q", out.String())
	}
}

func TestPostReceiveNoPushrunRefs(t *testing.T) {
	var out bytes.Buffer
	err := hookclient.PostReceive(pushStdin("refs/heads/main"), &out, testEnv("1"))
	if err != nil {
		t.Fatalf("PostReceive: %v", err)
	}
	if !strings.Contains(out.String(), "nothing to run") {
		t.Fatalf("expected a no-op note, got %q", out.String())
	}
}

func TestPostReceiveSkipsBranchDeletion(t *testing.T) {
	srv := fakeDaemon(t, http.StatusOK, "CI_STATUS=SUCCESS\n", nil)
	var out bytes.Buffer
	stdin := strings.NewReader(testOldSHA + " " + zeroSHA + " refs/pushrun/for/old-branch\n" +
		testOldSHA + " " + testNewSHA + " refs/pushrun/for/main\n")
	if err := hookclient.PostReceive(stdin, &out, testEnv(serverPort(srv))); err != nil {
		t.Fatalf("PostReceive: %v", err)
	}
	if !strings.Contains(out.String(), "deleted") {
		t.Fatalf("deletion not reported: %q", out.String())
	}
}

func TestPostReceiveMissingEnv(t *testing.T) {
	var out bytes.Buffer
	stdin := pushStdin("refs/pushrun/for/main")
	if err := hookclient.PostReceive(stdin, &out, hookclient.Env{Secret: "s", Repo: "r/x"}); err == nil {
		t.Fatal("missing port: want error")
	}
	if err := hookclient.PostReceive(stdin, &out, hookclient.Env{Port: "1", Repo: "r/x"}); err == nil {
		t.Fatal("missing secret: want error")
	}
	if err := hookclient.PostReceive(stdin, &out, hookclient.Env{Port: "1", Secret: "s"}); err == nil {
		t.Fatal("missing repo: want error")
	}
}

func TestPostReceiveDaemonUnreachable(t *testing.T) {
	// Port 1 is not listening.
	var out bytes.Buffer
	err := hookclient.PostReceive(pushStdin("refs/pushrun/for/main"), &out, testEnv("1"))
	if err == nil {
		t.Fatal("PostReceive succeeded with the daemon down, want error")
	}
	if !strings.Contains(err.Error(), "pushrun serve") {
		t.Fatalf("error does not hint at the daemon: %v", err)
	}
}

// --- pre-receive ---

// preReceiveEnv returns a hook environment over a fresh data root holding
// the given projects.
func preReceiveEnv(t *testing.T, projects ...*project.Project) hookclient.Env {
	t.Helper()
	root := t.TempDir()
	paths := config.NewPaths(root)
	for _, p := range projects {
		if err := project.Save(paths, p); err != nil {
			t.Fatal(err)
		}
	}
	return hookclient.Env{Root: root, Repo: testRepo}
}

func gitProject(name, repo string, primary bool) *project.Project {
	return &project.Project{
		Name: name,
		Tree: []project.Node{
			{Path: "app", Mount: &project.Mount{Provider: "git", Primary: primary,
				Params: map[string]string{"repo": repo, "branch": "main"}}},
		},
	}
}

func TestPreReceiveAcceptsPushrunRefs(t *testing.T) {
	env := preReceiveEnv(t, gitProject("demo", testRepo, true))
	var stderr bytes.Buffer
	if err := hookclient.PreReceive(pushStdin("refs/pushrun/for/main"), &stderr, env); err != nil {
		t.Fatalf("PreReceive: %v\nstderr: %s", err, stderr.String())
	}
}

func TestPreReceiveRejectsForeignRefs(t *testing.T) {
	env := preReceiveEnv(t, gitProject("demo", testRepo, true))
	var stderr bytes.Buffer
	err := hookclient.PreReceive(pushStdin("refs/heads/main"), &stderr, env)
	if err == nil {
		t.Fatal("PreReceive accepted refs/heads/main, want rejection")
	}
	if !strings.Contains(stderr.String(), "refs/pushrun/for/") {
		t.Fatalf("rejection does not name the allowed namespace: %q", stderr.String())
	}
}

func TestPreReceiveRejectsUnknownRepo(t *testing.T) {
	env := preReceiveEnv(t) // no projects at all
	var stderr bytes.Buffer
	err := hookclient.PreReceive(pushStdin("refs/pushrun/for/main"), &stderr, env)
	if err == nil {
		t.Fatal("PreReceive accepted an unknown repo, want rejection")
	}
	if !strings.Contains(stderr.String(), "unknown_repo:"+testRepo) {
		t.Fatalf("rejection not actionable: %q", stderr.String())
	}
}

func TestPreReceiveRejectsAmbiguousRepo(t *testing.T) {
	env := preReceiveEnv(t,
		gitProject("demo", testRepo, true),
		gitProject("other", testRepo, true),
	)
	var stderr bytes.Buffer
	err := hookclient.PreReceive(pushStdin("refs/pushrun/for/main"), &stderr, env)
	if err == nil {
		t.Fatal("PreReceive accepted an ambiguous repo, want rejection")
	}
	if !strings.Contains(stderr.String(), "ambiguous_project:demo,other") {
		t.Fatalf("rejection not actionable: %q", stderr.String())
	}

	// The pusher's explicit project disambiguates.
	env.Project = "other"
	stderr.Reset()
	if err := hookclient.PreReceive(pushStdin("refs/pushrun/for/main"), &stderr, env); err != nil {
		t.Fatalf("PreReceive with explicit project: %v\nstderr: %s", err, stderr.String())
	}
}

func TestPreReceiveRejectsExplicitProjectMismatch(t *testing.T) {
	env := preReceiveEnv(t,
		gitProject("demo", testRepo, true),
		gitProject("other", "git.example.com/team/unrelated", true),
	)
	env.Project = "other"
	var stderr bytes.Buffer
	err := hookclient.PreReceive(pushStdin("refs/pushrun/for/main"), &stderr, env)
	if err == nil {
		t.Fatal("PreReceive accepted a repo the explicit project does not mount, want rejection")
	}
	if !strings.Contains(stderr.String(), "project_does_not_contain_repo:other") {
		t.Fatalf("rejection not actionable: %q", stderr.String())
	}
}

func TestPreReceiveMissingEnv(t *testing.T) {
	stdin := pushStdin("refs/pushrun/for/main")
	if err := hookclient.PreReceive(stdin, io.Discard, hookclient.Env{Repo: testRepo}); err == nil {
		t.Fatal("missing root: want error")
	}
	if err := hookclient.PreReceive(stdin, io.Discard, hookclient.Env{Root: t.TempDir()}); err == nil {
		t.Fatal("missing repo: want error")
	}
}
