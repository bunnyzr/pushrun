package gitx_test

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/bunnyzr/pushrun/internal/config"
	"github.com/bunnyzr/pushrun/internal/gitx"
	"github.com/bunnyzr/pushrun/internal/hookclient"
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

// git runs the git CLI with a fixed author/committer identity and dates so
// fixture commits are deterministic.
func git(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(),
		"GIT_AUTHOR_NAME=PushRun Test",
		"GIT_AUTHOR_EMAIL=test@pushrun.dev",
		"GIT_COMMITTER_NAME=PushRun Test",
		"GIT_COMMITTER_EMAIL=test@pushrun.dev",
		"GIT_AUTHOR_DATE=2020-01-01T00:00:00Z",
		"GIT_COMMITTER_DATE=2020-01-01T00:00:00Z",
	)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %s: %v\n%s", strings.Join(args, " "), err, out)
	}
	return strings.TrimSpace(string(out))
}

// newFixtureRepo returns a non-bare repo with an initial commit containing
// the given files.
func newFixtureRepo(t *testing.T, files map[string]string) string {
	t.Helper()
	dir := t.TempDir()
	git(t, dir, "init", "-q")
	commitFiles(t, dir, files, "initial")
	return dir
}

// registerProject registers a project whose primary git node mounts repo, so
// the pre-receive hook's repo matching accepts pushes to that identity.
func registerProject(t *testing.T, paths config.Paths, name, repo string) {
	t.Helper()
	p := &project.Project{
		Name: name,
		Tree: []project.Node{
			{Path: "app", Mount: &project.Mount{Provider: "git", Primary: true,
				Params: map[string]string{"repo": repo, "branch": "main"}}},
		},
	}
	if err := project.Save(paths, p); err != nil {
		t.Fatal(err)
	}
}

// commitFiles writes files into dir, stages everything, and commits.
func commitFiles(t *testing.T, dir string, files map[string]string, msg string) string {
	t.Helper()
	for name, content := range files {
		path := filepath.Join(dir, name)
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	git(t, dir, "add", "-A")
	git(t, dir, "commit", "-qm", msg)
	return git(t, dir, "rev-parse", "HEAD")
}

// hookBin returns the test binary that doubles as the pushrun hook
// executable (see TestMain).
func hookBin(t *testing.T) string {
	t.Helper()
	bin, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	return bin
}

func TestEnsureBareRepo(t *testing.T) {
	root := t.TempDir()
	paths := config.NewPaths(root)

	repoDir, err := gitx.EnsureBareRepo(paths, "git.example.com/team/hello", hookBin(t), "s3cret", 8000)
	if err != nil {
		t.Fatalf("EnsureBareRepo: %v", err)
	}
	wantDir := filepath.Join(root, "repos", "git.example.com--team--hello.git")
	if repoDir != wantDir {
		t.Fatalf("repoDir = %q, want %q", repoDir, wantDir)
	}

	if out := git(t, root, "--git-dir="+repoDir, "rev-parse", "--is-bare-repository"); out != "true" {
		t.Fatalf("not a bare repo: %q", out)
	}
	if out := git(t, root, "--git-dir="+repoDir, "config", "http.receivepack"); out != "true" {
		t.Fatalf("http.receivepack = %q, want true", out)
	}

	hookPath := filepath.Join(repoDir, "hooks", "post-receive")
	info, err := os.Stat(hookPath)
	if err != nil {
		t.Fatalf("hook missing: %v", err)
	}
	if info.Mode().Perm() != 0o700 {
		t.Fatalf("hook mode = %o, want 700 (hook embeds the cleartext hook secret)", info.Mode().Perm())
	}
	hook, err := os.ReadFile(hookPath)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		`PUSHRUN_BIN="` + hookBin(t) + `"`,
		`PUSHRUN_ROOT="` + root + `"`,
		`PUSHRUN_HOOK_SECRET="s3cret"`,
		`PUSHRUN_REPO="git.example.com/team/hello"`,
		`PUSHRUN_PORT=8000`,
		`exec "$PUSHRUN_BIN" hook post-receive`,
	} {
		if !strings.Contains(string(hook), want) {
			t.Errorf("hook missing %q:\n%s", want, hook)
		}
	}
	if strings.Contains(string(hook), "PUSHRUN_PROJECT=") {
		t.Errorf("hook bakes PUSHRUN_PROJECT (the project is the pusher's explicit header now):\n%s", hook)
	}

	prePath := filepath.Join(repoDir, "hooks", "pre-receive")
	preInfo, err := os.Stat(prePath)
	if err != nil {
		t.Fatalf("pre-receive hook missing: %v", err)
	}
	if preInfo.Mode().Perm() != 0o700 {
		t.Fatalf("pre-receive hook mode = %o, want 700", preInfo.Mode().Perm())
	}
	preHook, err := os.ReadFile(prePath)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(preHook), `exec "$PUSHRUN_BIN" hook pre-receive`) {
		t.Errorf("pre-receive does not exec the hook entry point:\n%s", preHook)
	}

	// The repo must be pushable; the post-receive hook runs the test
	// binary, whose daemon call fails (nothing listens) — git ignores the
	// post-receive exit status, so the push itself still succeeds. The
	// pre-receive hook only allows the pushrun namespace and requires the
	// repo identity to match a registered project.
	registerProject(t, paths, "hello", "git.example.com/team/hello")
	src := newFixtureRepo(t, map[string]string{"a.txt": "v1\n"})
	git(t, src, "push", repoDir, "HEAD:refs/pushrun/for/main")

	// Idempotent: a second call refreshes the hooks without destroying refs.
	repoDir2, err := gitx.EnsureBareRepo(paths, "git.example.com/team/hello", hookBin(t), "new-secret", 9090)
	if err != nil {
		t.Fatalf("EnsureBareRepo (2nd): %v", err)
	}
	if repoDir2 != repoDir {
		t.Fatalf("2nd repoDir = %q, want %q", repoDir2, repoDir)
	}
	hook2, err := os.ReadFile(hookPath)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(hook2), `PUSHRUN_HOOK_SECRET="new-secret"`) {
		t.Errorf("hook secret not refreshed:\n%s", hook2)
	}
	if !strings.Contains(string(hook2), `PUSHRUN_PORT=9090`) {
		t.Errorf("hook port not refreshed:\n%s", hook2)
	}
	if got := git(t, root, "--git-dir="+repoDir, "rev-parse", "refs/pushrun/for/main"); len(got) != 40 {
		t.Errorf("ref lost after re-init: %q", got)
	}
}

func TestEnsureBareRepoPushPolicy(t *testing.T) {
	root := t.TempDir()
	paths := config.NewPaths(root)
	repoDir, err := gitx.EnsureBareRepo(paths, "git.example.com/team/hello", hookBin(t), "s3cret", 8000)
	if err != nil {
		t.Fatalf("EnsureBareRepo: %v", err)
	}
	registerProject(t, paths, "hello", "git.example.com/team/hello")
	src := newFixtureRepo(t, map[string]string{"a.txt": "v1\n"})

	// Refs outside refs/pushrun/for/* are rejected by the pre-receive hook.
	cmd := exec.Command("git", "push", repoDir, "HEAD:refs/heads/main")
	cmd.Dir = src
	out, err := cmd.CombinedOutput()
	if err == nil {
		t.Fatalf("push to refs/heads/main succeeded, want rejection:\n%s", out)
	}
	if !strings.Contains(string(out), "refs/pushrun/for/") {
		t.Fatalf("rejection does not name the allowed namespace:\n%s", out)
	}

	// A repo no registered project mounts rejects pushes with an actionable
	// message.
	repoDir2, err := gitx.EnsureBareRepo(paths, "git.example.com/team/ghost", hookBin(t), "s3cret", 8000)
	if err != nil {
		t.Fatalf("EnsureBareRepo ghost: %v", err)
	}
	cmd = exec.Command("git", "push", repoDir2, "HEAD:refs/pushrun/for/main")
	cmd.Dir = src
	out, err = cmd.CombinedOutput()
	if err == nil {
		t.Fatalf("push to unknown repo succeeded, want rejection:\n%s", out)
	}
	if !strings.Contains(string(out), "unknown_repo:git.example.com/team/ghost") {
		t.Fatalf("rejection is not actionable:\n%s", out)
	}
}

func TestFetchSnapshot(t *testing.T) {
	root := t.TempDir()
	paths := config.NewPaths(root)
	repoDir, err := gitx.EnsureBareRepo(paths, "git.example.com/team/hello", hookBin(t), "s3cret", 8000)
	if err != nil {
		t.Fatalf("EnsureBareRepo: %v", err)
	}

	src := newFixtureRepo(t, map[string]string{"a.txt": "v1\n"})
	head := git(t, src, "rev-parse", "HEAD")

	if err := gitx.FetchSnapshot(repoDir, src, "main"); err != nil {
		t.Fatalf("FetchSnapshot: %v", err)
	}
	commit, err := gitx.ResolveRef(repoDir, "refs/pushrun/for/main")
	if err != nil {
		t.Fatalf("ResolveRef: %v", err)
	}
	if commit != head {
		t.Fatalf("snapshot ref = %q, want source head %q", commit, head)
	}

	// A second fetch force-updates the snapshot to the advanced source head.
	head2 := commitFiles(t, src, map[string]string{"a.txt": "v2\n"}, "second")
	if err := gitx.FetchSnapshot(repoDir, src, "main"); err != nil {
		t.Fatalf("FetchSnapshot (2nd): %v", err)
	}
	commit, err = gitx.ResolveRef(repoDir, "refs/pushrun/for/main")
	if err != nil {
		t.Fatalf("ResolveRef (2nd): %v", err)
	}
	if commit != head2 {
		t.Fatalf("snapshot ref after re-fetch = %q, want %q", commit, head2)
	}
}

func TestResolveRef(t *testing.T) {
	root := t.TempDir()
	paths := config.NewPaths(root)
	repoDir, err := gitx.EnsureBareRepo(paths, "git.example.com/team/proj", hookBin(t), "s", 8000)
	if err != nil {
		t.Fatalf("EnsureBareRepo: %v", err)
	}
	registerProject(t, paths, "proj", "git.example.com/team/proj")

	src := newFixtureRepo(t, map[string]string{"a.txt": "v1\n"})
	head := git(t, src, "rev-parse", "HEAD")
	git(t, src, "push", repoDir, "HEAD:refs/pushrun/for/main")

	commit, err := gitx.ResolveRef(repoDir, "refs/pushrun/for/main")
	if err != nil {
		t.Fatalf("ResolveRef: %v", err)
	}
	if commit != head {
		t.Fatalf("ResolveRef(main) = %q, want %q", commit, head)
	}
	if _, err := gitx.ResolveRef(repoDir, "no-such-ref"); err == nil {
		t.Fatal("ResolveRef(no-such-ref) succeeded, want error")
	}
}

func TestCheckoutCommit(t *testing.T) {
	root := t.TempDir()
	paths := config.NewPaths(root)
	repoDir, err := gitx.EnsureBareRepo(paths, "git.example.com/team/proj", hookBin(t), "s", 8000)
	if err != nil {
		t.Fatalf("EnsureBareRepo: %v", err)
	}
	registerProject(t, paths, "proj", "git.example.com/team/proj")

	src := newFixtureRepo(t, map[string]string{"a.txt": "v1\n", "old.txt": "gone soon\n"})
	c1 := git(t, src, "rev-parse", "HEAD")
	if err := os.Remove(filepath.Join(src, "old.txt")); err != nil {
		t.Fatal(err)
	}
	c2 := commitFiles(t, src, map[string]string{"a.txt": "v2\n", "b.txt": "new\n"}, "second")
	git(t, src, "push", repoDir, "HEAD:refs/pushrun/for/main")

	worktree := filepath.Join(root, "worktrees", "proj")

	// First checkout materializes c1's tree into a fresh dir.
	if err := gitx.CheckoutCommit(repoDir, worktree, c1); err != nil {
		t.Fatalf("CheckoutCommit c1: %v", err)
	}
	assertFile(t, filepath.Join(worktree, "a.txt"), "v1\n")
	assertFile(t, filepath.Join(worktree, "old.txt"), "gone soon\n")

	// An untracked file must be cleaned away on the next checkout.
	if err := os.WriteFile(filepath.Join(worktree, "junk.txt"), []byte("junk"), 0o644); err != nil {
		t.Fatal(err)
	}

	// Second checkout of a different commit replaces content wholesale.
	if err := gitx.CheckoutCommit(repoDir, worktree, c2); err != nil {
		t.Fatalf("CheckoutCommit c2: %v", err)
	}
	assertFile(t, filepath.Join(worktree, "a.txt"), "v2\n")
	assertFile(t, filepath.Join(worktree, "b.txt"), "new\n")
	for _, gone := range []string{"old.txt", "junk.txt"} {
		if _, err := os.Stat(filepath.Join(worktree, gone)); !os.IsNotExist(err) {
			t.Errorf("%s still present after checkout of c2", gone)
		}
	}

	if err := gitx.CheckoutCommit(repoDir, worktree, "deadbeefdeadbeefdeadbeefdeadbeefdeadbeef"); err == nil {
		t.Fatal("CheckoutCommit with unknown commit succeeded, want error")
	}
}

func TestCheckoutCommitWithIndexConcurrent(t *testing.T) {
	root := t.TempDir()
	paths := config.NewPaths(root)
	repoDir, err := gitx.EnsureBareRepo(paths, "git.example.com/team/proj", hookBin(t), "s", 8000)
	if err != nil {
		t.Fatalf("EnsureBareRepo: %v", err)
	}
	registerProject(t, paths, "proj", "git.example.com/team/proj")
	files := map[string]string{}
	for i := range 20 {
		files[strings.Repeat("f", i+1)+".txt"] = "content\n"
	}
	src := newFixtureRepo(t, files)
	head := git(t, src, "rev-parse", "HEAD")
	git(t, src, "push", repoDir, "HEAD:refs/pushrun/for/main")

	// Concurrent checkouts from the same bare repo must not share index
	// state: each uses its own index file, like parallel instances of one
	// project.
	const n = 8
	errs := make(chan error, n)
	for i := range n {
		go func() {
			wt := filepath.Join(root, "wt", strings.Repeat("x", i+1))
			idx := filepath.Join(root, "idx", strings.Repeat("x", i+1))
			if err := os.MkdirAll(filepath.Dir(idx), 0o755); err != nil {
				errs <- err
				return
			}
			errs <- gitx.CheckoutCommitWithIndex(repoDir, wt, head, idx)
		}()
	}
	for range n {
		if err := <-errs; err != nil {
			t.Fatalf("concurrent checkout: %v", err)
		}
	}
	for i := range n {
		assertFile(t, filepath.Join(root, "wt", strings.Repeat("x", i+1), "f.txt"), "content\n")
	}
}

func assertFile(t *testing.T, path, want string) {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	if string(data) != want {
		t.Fatalf("%s = %q, want %q", path, data, want)
	}
}
