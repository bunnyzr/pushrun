// Package gitx wraps the git CLI for bare-repo management, hook
// installation (pre-receive push policy, post-receive daemon callback), and
// checkout of a commit into a plain directory.
package gitx

import (
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/bunnyzr/pushrun/internal/config"
	"github.com/bunnyzr/pushrun/internal/fsutil"
)

// EnsureBareRepo creates (or refreshes) the bare repo for a repo identity at
// <root>/repos/<host>--<path>.git, enables push-over-HTTP, and installs the
// hooks that re-enter the pushrun binary. It is idempotent: calling it on an
// existing repo keeps all refs and rewrites the hooks.
//
// The pre-receive hook execs `pushrun hook pre-receive`, which enforces the
// push policy where git can still reject the push (post-receive's exit
// status is ignored by git): only refs/pushrun/for/<branch> may be updated,
// and only when the repo identity matches a git node of a registered
// project. The post-receive hook execs `pushrun hook post-receive`. Both
// carry the daemon port, hook secret, and repo identity (PUSHRUN_REPO) baked
// into their environment; the explicit project, when the pusher asserts one,
// arrives as PUSHRUN_PROJECT via the CGI environment.
//
// Both hooks are written 0700: they embed the cleartext hook secret, and the
// daemon itself is the only user that ever executes them.
func EnsureBareRepo(paths config.Paths, repo, hookBin, hookSecret string, port int) (repoDir string, err error) {
	identity, err := NormalizeRepo(repo)
	if err != nil {
		return "", err
	}
	repoDir, err = BareRepoDir(paths, identity)
	if err != nil {
		return "", err
	}
	if err := InitBareRepo(repoDir); err != nil {
		return "", err
	}

	env := "# Managed by pushrun; rewritten whenever the daemon serves this repo.\n" +
		"export PUSHRUN_BIN=" + shellQuote(hookBin) + "\n" +
		"export PUSHRUN_ROOT=" + shellQuote(paths.Root) + "\n" +
		"export PUSHRUN_HOOK_SECRET=" + shellQuote(hookSecret) + "\n" +
		"export PUSHRUN_REPO=" + shellQuote(identity) + "\n" +
		fmt.Sprintf("export PUSHRUN_PORT=%d\n", port)

	postReceive := "#!/bin/sh\n" + env +
		`exec "$PUSHRUN_BIN" hook post-receive` + "\n"
	if err := fsutil.WriteFileAtomic(filepath.Join(repoDir, "hooks", "post-receive"), []byte(postReceive), 0o700); err != nil {
		return "", fmt.Errorf("write post-receive hook: %w", err)
	}

	preReceive := "#!/bin/sh\n" + env +
		`exec "$PUSHRUN_BIN" hook pre-receive` + "\n"
	if err := fsutil.WriteFileAtomic(filepath.Join(repoDir, "hooks", "pre-receive"), []byte(preReceive), 0o700); err != nil {
		return "", fmt.Errorf("write pre-receive hook: %w", err)
	}
	return repoDir, nil
}

// InitBareRepo initializes (or re-initializes) a bare repo at dir with
// push-over-HTTP enabled. Safe to call on an existing repo.
func InitBareRepo(dir string) error {
	if err := os.MkdirAll(filepath.Dir(dir), 0o755); err != nil {
		return fmt.Errorf("create repos dir: %w", err)
	}
	// git init on an existing repo is a safe re-initialization.
	if err := runGit("init", "--bare", dir); err != nil {
		return err
	}
	if err := runGit("--git-dir="+dir, "config", "http.receivepack", "true"); err != nil {
		return err
	}
	return nil
}

// FetchSnapshot fetches the head of branch from source into
// refs/pushrun/for/<branch> of the bare repo at repoDir, force-updating the
// ref. This is the one-time seed fetch for a repo nobody has pushed through
// pushrun yet; afterwards the ref only moves when a push lands.
func FetchSnapshot(repoDir, source, branch string) error {
	refspec := "+refs/heads/" + branch + ":refs/pushrun/for/" + branch
	if err := runGit("--git-dir="+repoDir, "fetch", source, refspec); err != nil {
		return fmt.Errorf("fetch %s from %s: %w", branch, source, err)
	}
	return nil
}

// shellQuote renders s as a double-quoted shell word.
func shellQuote(s string) string {
	var sb strings.Builder
	sb.WriteByte('"')
	for _, r := range s {
		switch r {
		case '"', '\\', '$', '`':
			sb.WriteByte('\\')
		}
		sb.WriteRune(r)
	}
	sb.WriteByte('"')
	return sb.String()
}

// runGit runs the git CLI and returns a descriptive error on failure.
func runGit(args ...string) error {
	return runGitEnv(nil, args...)
}

// runGitEnv runs the git CLI with extra environment variables.
func runGitEnv(env []string, args ...string) error {
	cmd := exec.Command("git", args...)
	if len(env) > 0 {
		cmd.Env = append(os.Environ(), env...)
	}
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("git %s: %w: %s", strings.Join(args, " "), err, strings.TrimSpace(stderr.String()))
	}
	return nil
}

// gitOutput runs the git CLI and returns its trimmed stdout.
func gitOutput(args ...string) (string, error) {
	cmd := exec.Command("git", args...)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return "", fmt.Errorf("git %s: %w: %s", strings.Join(args, " "), err, strings.TrimSpace(stderr.String()))
	}
	return strings.TrimSpace(stdout.String()), nil
}
