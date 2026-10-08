// Package testutil holds helpers shared by tests across packages.
package testutil

import (
	"os"
	"path/filepath"
)

// IsolateGitEnv makes every git invocation in this test process hermetic to
// the host's git configuration: the system config is disabled and the global
// config is pointed at an empty file. Without this a contributor's
// ~/.gitconfig leaks into tests — credential helpers are the worst offender
// (an osxkeychain entry for 127.0.0.1 lets an "unauthenticated" test push
// succeed), but commit signing and default-branch settings break tests too.
// Call from TestMain before m.Run.
func IsolateGitEnv() {
	dir, err := os.MkdirTemp("", "pushrun-githome-")
	if err != nil {
		panic(err)
	}
	empty := filepath.Join(dir, "gitconfig")
	// An explicit defaultBranch keeps tests deterministic regardless of the
	// git build's factory default (master vs main).
	if err := os.WriteFile(empty, []byte("[init]\n\tdefaultBranch = main\n"), 0o644); err != nil {
		panic(err)
	}
	if err := os.Setenv("GIT_CONFIG_NOSYSTEM", "1"); err != nil {
		panic(err)
	}
	if err := os.Setenv("GIT_CONFIG_GLOBAL", empty); err != nil {
		panic(err)
	}
}
