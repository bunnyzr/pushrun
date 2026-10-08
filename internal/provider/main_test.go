package provider

import (
	"os"
	"testing"

	"github.com/bunnyzr/pushrun/internal/testutil"
)

func TestMain(m *testing.M) {
	testutil.IsolateGitEnv()
	os.Exit(m.Run())
}
