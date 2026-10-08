package gitx

import (
	"fmt"
	"os"
	"path/filepath"
)

// CheckoutCommit materializes the tree of commit from the bare repo at
// repoDir into worktreeDir, replacing whatever was there: tracked files are
// reset to the commit's content and untracked/ignored files are removed.
// worktreeDir is created if missing. The bare repo's index file is used as
// checkout state, so checkouts are not concurrent-safe per repo; use
// CheckoutCommitWithIndex when checkouts may run in parallel.
func CheckoutCommit(repoDir, worktreeDir, commit string) error {
	return checkout(repoDir, worktreeDir, commit, "")
}

// CheckoutCommitWithIndex behaves like CheckoutCommit but uses indexFile as
// the git index for this checkout, so concurrent checkouts from the same
// bare repo do not share index state. indexFile need not exist; it is left
// behind for the caller to remove.
func CheckoutCommitWithIndex(repoDir, worktreeDir, commit, indexFile string) error {
	if indexFile == "" {
		return fmt.Errorf("gitx: index file path required")
	}
	abs, err := filepath.Abs(indexFile)
	if err != nil {
		return fmt.Errorf("gitx: resolve index file path: %w", err)
	}
	return checkout(repoDir, worktreeDir, commit, abs)
}

func checkout(repoDir, worktreeDir, commit, indexFile string) error {
	if err := os.MkdirAll(worktreeDir, 0o755); err != nil {
		return fmt.Errorf("create worktree dir: %w", err)
	}
	base := []string{"--git-dir=" + repoDir, "--work-tree=" + worktreeDir}
	var env []string
	if indexFile != "" {
		env = append(env, "GIT_INDEX_FILE="+indexFile)
	}
	if err := runGitEnv(env, append(base, "read-tree", "--reset", "-u", commit)...); err != nil {
		return fmt.Errorf("checkout %s: %w", commit, err)
	}
	if err := runGitEnv(env, append(base, "clean", "-fdx")...); err != nil {
		return fmt.Errorf("clean worktree: %w", err)
	}
	return nil
}

// ResolveRef resolves ref (branch, tag, or commit-ish) in the bare repo at
// repoDir to a full commit SHA.
func ResolveRef(repoDir, ref string) (commit string, err error) {
	commit, err = gitOutput("--git-dir="+repoDir, "rev-parse", ref)
	if err != nil {
		return "", fmt.Errorf("resolve ref %q: %w", ref, err)
	}
	return commit, nil
}
