package provider

import (
	"context"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/bunnyzr/pushrun/internal/config"
	"github.com/bunnyzr/pushrun/internal/gitx"
)

func gitBuiltin(t *testing.T) *Provider {
	t.Helper()
	for i := range Builtins() {
		if Builtins()[i].ID == "git" {
			return &Builtins()[i]
		}
	}
	t.Fatal("git builtin not found")
	return nil
}

// git runs the git CLI with a fixed identity.
func git(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(),
		"GIT_AUTHOR_NAME=PushRun Test", "GIT_AUTHOR_EMAIL=test@pushrun.dev",
		"GIT_COMMITTER_NAME=PushRun Test", "GIT_COMMITTER_EMAIL=test@pushrun.dev")
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %s: %v\n%s", strings.Join(args, " "), err, out)
	}
	return strings.TrimSpace(string(out))
}

// newSourceRepo returns a non-bare repo on branch main holding one file with
// the given content.
func newSourceRepo(t *testing.T, content string) (dir, head string) {
	t.Helper()
	dir = t.TempDir()
	git(t, dir, "init", "-q", "-b", "main")
	return dir, commitSource(t, dir, content)
}

func commitSource(t *testing.T, dir, content string) string {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, "hello.txt"), []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	git(t, dir, "add", "-A")
	git(t, dir, "commit", "-qm", "set "+content)
	return git(t, dir, "rev-parse", "HEAD")
}

func bareRepoDir(t *testing.T, ex *Executor, repo string) string {
	t.Helper()
	dir, err := gitx.BareRepoDir(ex.paths, repo)
	if err != nil {
		t.Fatalf("BareRepoDir: %v", err)
	}
	return dir
}

// Warmup seeds the snapshot ref from the source remote exactly once: a warm
// repo is never re-fetched, so advancing the source remote does not leak
// into later runs.
func TestGitWarmupSeedsOnceAndNeverRefetches(t *testing.T) {
	ex, _ := newTestExecutor(t)
	p := gitBuiltin(t)
	src, head1 := newSourceRepo(t, "v1\n")
	params := map[string]string{"repo": src, "branch": "main"}
	ctx := context.Background()

	dir1, err := ex.EnsureWarmed(ctx, p, params, io.Discard)
	if err != nil {
		t.Fatalf("first EnsureWarmed: %v", err)
	}
	if dir1 != bareRepoDir(t, ex, src) {
		t.Fatalf("cache dir = %q, want the bare repo %q", dir1, bareRepoDir(t, ex, src))
	}
	ref, err := gitx.ResolveRef(dir1, "refs/pushrun/for/main")
	if err != nil {
		t.Fatalf("snapshot ref missing after warmup: %v", err)
	}
	if ref != head1 {
		t.Fatalf("snapshot ref = %q, want source head %q", ref, head1)
	}
	if !ex.Warm(p, params) {
		t.Fatal("Warm = false after seeding, want true")
	}

	// Advance the source remote: a warm repo must not follow it.
	head2 := commitSource(t, src, "v2\n")
	dir2, err := ex.EnsureWarmed(ctx, p, params, io.Discard)
	if err != nil {
		t.Fatalf("second EnsureWarmed: %v", err)
	}
	if dir2 != dir1 {
		t.Fatalf("cache dir changed between calls: %q vs %q", dir2, dir1)
	}
	ref, err = gitx.ResolveRef(dir1, "refs/pushrun/for/main")
	if err != nil {
		t.Fatal(err)
	}
	if ref != head1 {
		t.Fatalf("warm repo re-fetched: snapshot ref = %q, want pinned %q (source head %q)", ref, head1, head2)
	}
}

// Install checks out the explicit commit param (trigger node) when given,
// and the snapshot ref otherwise.
func TestGitInstallTriggerCommitVsSnapshot(t *testing.T) {
	ex, _ := newTestExecutor(t)
	p := gitBuiltin(t)
	src, _ := newSourceRepo(t, "v1\n")
	params := map[string]string{"repo": src, "branch": "main"}
	ctx := context.Background()

	if _, err := ex.EnsureWarmed(ctx, p, params, io.Discard); err != nil {
		t.Fatalf("EnsureWarmed: %v", err)
	}
	// The snapshot ref points at head1 (seeded before head2 existed).
	head2 := commitSource(t, src, "v2\n")

	// Non-trigger node: snapshot content.
	target := filepath.Join(t.TempDir(), "node")
	if err := ex.Install(ctx, p, params, target, io.Discard); err != nil {
		t.Fatalf("Install (snapshot): %v", err)
	}
	data, err := os.ReadFile(filepath.Join(target, "hello.txt"))
	if err != nil || string(data) != "v1\n" {
		t.Fatalf("snapshot install hello.txt = %q, %v; want v1", data, err)
	}

	// Trigger node: the explicit commit wins even ahead of the snapshot. Its
	// objects arrive via the push itself; emulate that by moving the
	// snapshot ref the way a push would.
	trigger := map[string]string{"repo": src, "branch": "main", "commit": head2}
	bare := bareRepoDir(t, ex, src)
	git(t, bare, "--git-dir="+bare, "fetch", src, "+refs/heads/main:refs/pushrun/for/main")
	target2 := filepath.Join(t.TempDir(), "node2")
	if err := ex.Install(ctx, p, trigger, target2, io.Discard); err != nil {
		t.Fatalf("Install (trigger): %v", err)
	}
	data, err = os.ReadFile(filepath.Join(target2, "hello.txt"))
	if err != nil || string(data) != "v2\n" {
		t.Fatalf("trigger install hello.txt = %q, %v; want v2", data, err)
	}
}

// A cold install (no snapshot ref, never warmed) fails instead of silently
// falling back to a source branch head.
func TestGitInstallColdRepoFails(t *testing.T) {
	ex, _ := newTestExecutor(t)
	p := gitBuiltin(t)
	src, _ := newSourceRepo(t, "v1\n")

	target := filepath.Join(t.TempDir(), "node")
	err := ex.Install(context.Background(), p, map[string]string{"repo": src, "branch": "main"}, target, io.Discard)
	if err == nil {
		t.Fatal("Install on a cold repo succeeded, want snapshot ref error")
	}
	if !strings.Contains(err.Error(), "refs/pushrun/for/main") {
		t.Fatalf("error does not name the missing snapshot ref: %v", err)
	}
}

// The executor defaults the git source scheme to https when no config is
// supplied.
func TestGitSchemeDefault(t *testing.T) {
	ex := NewExecutor(config.NewPaths(t.TempDir()), nil)
	if ex.gitScheme != "https" {
		t.Fatalf("default git scheme = %q, want https", ex.gitScheme)
	}
}
