package config

import (
	"os"
	"path/filepath"
	"testing"
)

// A relative data root is absolutized: hooks and CGI subprocesses run with
// a different working directory, so Paths built from a relative root must
// still resolve.
func TestNewPathsAbsolutizesRoot(t *testing.T) {
	wd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	p := NewPaths("./local/data")
	if !filepath.IsAbs(p.Root) {
		t.Fatalf("Root = %q, want absolute", p.Root)
	}
	want := filepath.Join(wd, "local", "data")
	if p.Root != want {
		t.Fatalf("Root = %q, want %q", p.Root, want)
	}
	if p.Projects != filepath.Join(want, "projects") {
		t.Fatalf("Projects = %q, want %q", p.Projects, filepath.Join(want, "projects"))
	}
}

// An absolute root passes through unchanged.
func TestNewPathsAbsoluteRootUnchanged(t *testing.T) {
	root := t.TempDir()
	p := NewPaths(root)
	if p.Root != root {
		t.Fatalf("Root = %q, want %q", p.Root, root)
	}
}
