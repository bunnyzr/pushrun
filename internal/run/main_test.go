package run_test

import (
	"os"
	"testing"
	"time"

	"github.com/bunnyzr/pushrun/internal/run"
	"github.com/bunnyzr/pushrun/internal/testutil"
)

func TestMain(m *testing.M) {
	testutil.IsolateGitEnv()
	// The zero-config readiness probe's grace period is seconds in
	// production; tests pay it per background step, so shrink it.
	run.AutoReadyGrace = 300 * time.Millisecond
	os.Exit(m.Run())
}
