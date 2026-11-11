package gitx_test

import (
	"path/filepath"
	"testing"

	"github.com/bunnyzr/pushrun/internal/config"
	"github.com/bunnyzr/pushrun/internal/gitx"
)

func TestNormalizeRepo(t *testing.T) {
	for repo, want := range map[string]string{
		"git.example.com/team/proto":             "git.example.com/team/proto",
		"git.example.com/team/proto.git":         "git.example.com/team/proto",
		"https://git.example.com/team/proto":     "git.example.com/team/proto",
		"https://git.example.com/team/proto.git": "git.example.com/team/proto",
		"ssh://git.example.com:2222/team/proto":  "git.example.com:2222/team/proto",
		"https://user@git.example.com/proto":     "git.example.com/proto",
		"git@git.example.com:team/proto":         "git.example.com/team/proto",
		"git@git.example.com:team/proto.git":     "git.example.com/team/proto",
		"git.example.com:team/proto":             "git.example.com/team/proto",
		"git.example.com:2222/team/proto":        "git.example.com:2222/team/proto",
		"/srv/repos/proto":                       "srv/repos/proto",
		"/srv/repos/proto.git":                   "srv/repos/proto",
		"/Users/me/a+b & c/proto":                "Users/me/a+b & c/proto",
	} {
		got, err := gitx.NormalizeRepo(repo)
		if err != nil {
			t.Errorf("NormalizeRepo(%q): %v", repo, err)
			continue
		}
		if got != want {
			t.Errorf("NormalizeRepo(%q) = %q, want %q", repo, got, want)
		}
		// Identities are fixed points: re-normalizing a canonical identity
		// (notably host:port/path) must not change it.
		if again, err := gitx.NormalizeRepo(got); err != nil || again != got {
			t.Errorf("NormalizeRepo(%q) not idempotent: %q, %v", got, again, err)
		}
	}
}

func TestNormalizeRepoRejects(t *testing.T) {
	for _, repo := range []string{
		"",
		"   ",
		"demo",                     // no host/path structure
		"demo.git",                 // still no host/path structure
		"git.example.com",          // host only
		"git.example.com//proto",   // empty segment
		"git.example.com/../proto", // traversal
		"../proto/x",               // traversal
		"git.example.com/./proto",  // dot segment
		`git.example.com\proto\x`,  // backslash
		"file:///srv/repos/proto",  // hostless URL
		"https:///team/proto",      // empty host
	} {
		if got, err := gitx.NormalizeRepo(repo); err == nil {
			t.Errorf("NormalizeRepo(%q) = %q, want error", repo, got)
		}
	}
}

func TestBareRepoDir(t *testing.T) {
	paths := config.NewPaths(t.TempDir())
	dir, err := gitx.BareRepoDir(paths, "git.example.com/team/proto")
	if err != nil {
		t.Fatalf("BareRepoDir: %v", err)
	}
	want := filepath.Join(paths.Repos, "git.example.com--team--proto.git")
	if dir != want {
		t.Fatalf("BareRepoDir = %q, want %q", dir, want)
	}
	// A full URL and the shorthand resolve to the same repo.
	dir2, err := gitx.BareRepoDir(paths, "https://git.example.com/team/proto.git")
	if err != nil {
		t.Fatalf("BareRepoDir(URL): %v", err)
	}
	if dir2 != dir {
		t.Fatalf("BareRepoDir(URL) = %q, want %q", dir2, dir)
	}
	if _, err := gitx.BareRepoDir(paths, "../escape/x"); err == nil {
		t.Fatal("BareRepoDir with traversal succeeded, want error")
	}
}

func TestRepoPath(t *testing.T) {
	if got := gitx.RepoPath("git.example.com/team/proto"); got != "team/proto" {
		t.Fatalf("RepoPath = %q, want team/proto", got)
	}
}

func TestSourceURL(t *testing.T) {
	for repo, want := range map[string]string{
		"git.example.com/team/proto":         "https://git.example.com/team/proto",
		"https://git.example.com/team/proto": "https://git.example.com/team/proto",
		"ssh://git.example.com/team/proto":   "ssh://git.example.com/team/proto",
		"git@git.example.com:team/proto":     "ssh://git@git.example.com/team/proto",
		"git.example.com:2222/team/proto":    "https://git.example.com:2222/team/proto",
		"/srv/repos/proto":                   "/srv/repos/proto",
	} {
		if got := gitx.SourceURL(repo, "https"); got != want {
			t.Errorf("SourceURL(%q) = %q, want %q", repo, got, want)
		}
	}
	if got := gitx.SourceURL("git.example.com/team/proto", "ssh"); got != "ssh://git.example.com/team/proto" {
		t.Errorf("SourceURL with ssh scheme = %q", got)
	}
}
