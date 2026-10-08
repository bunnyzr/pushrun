package provider

import (
	"context"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

func TestBuiltinsDescriptors(t *testing.T) {
	builtins := Builtins()
	if len(builtins) != 2 {
		t.Fatalf("Builtins() returned %d providers, want 2", len(builtins))
	}

	git := builtins[0]
	if git.ID != "git" {
		t.Fatalf("builtins[0].ID = %q, want git", git.ID)
	}
	if git.Schema != SchemaV1 {
		t.Errorf("git.Schema = %q, want %q", git.Schema, SchemaV1)
	}
	if err := git.Validate(); err != nil {
		t.Errorf("git.Validate() = %v, want nil", err)
	}
	wantGitParams := []Param{
		{ID: "repo", Label: "Repository URL", Type: ParamTypeString, Scope: ScopeWarmup, Required: true},
		{ID: "branch", Label: "Branch", Type: ParamTypeString, Scope: ScopeWarmup, Required: true},
		{ID: "commit", Label: "Commit", Type: ParamTypeString, Scope: ScopeInstall},
	}
	assertParams(t, "git", git.Params, wantGitParams)

	symlink := builtins[1]
	if symlink.ID != "symlink" {
		t.Fatalf("builtins[1].ID = %q, want symlink", symlink.ID)
	}
	if symlink.Schema != SchemaV1 {
		t.Errorf("symlink.Schema = %q, want %q", symlink.Schema, SchemaV1)
	}
	if symlink.Warmup != "" {
		t.Errorf("symlink.Warmup = %q, want empty (install-only provider)", symlink.Warmup)
	}
	if err := symlink.Validate(); err != nil {
		t.Errorf("symlink.Validate() = %v, want nil", err)
	}
	wantSymlinkParams := []Param{
		{ID: "source", Label: "Source path", Type: ParamTypeString, Scope: ScopeInstall, Required: true},
	}
	assertParams(t, "symlink", symlink.Params, wantSymlinkParams)
}

func assertParams(t *testing.T, id string, got, want []Param) {
	t.Helper()
	if len(got) != len(want) {
		t.Fatalf("%s has %d params, want %d: %+v", id, len(got), len(want), got)
	}
	for i, wp := range want {
		if !reflect.DeepEqual(got[i], wp) {
			t.Errorf("%s params[%d] = %+v, want %+v", id, i, got[i], wp)
		}
	}
}

func symlinkBuiltin(t *testing.T) *Provider {
	t.Helper()
	for i := range Builtins() {
		if Builtins()[i].ID == "symlink" {
			return &Builtins()[i]
		}
	}
	t.Fatal("symlink builtin not found")
	return nil
}

func TestSymlinkBuiltinInstallCreatesSymlink(t *testing.T) {
	ex, paths := newTestExecutor(t)
	p := symlinkBuiltin(t)

	source := filepath.Join(paths.Root, "source")
	if err := os.MkdirAll(source, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(source, "hello.txt"), []byte("hi"), 0o644); err != nil {
		t.Fatal(err)
	}

	// The target dir already holds stale content; install must replace it.
	target := filepath.Join(paths.Root, "nodes", "node-1")
	if err := os.MkdirAll(target, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(target, "stale.txt"), []byte("old"), 0o644); err != nil {
		t.Fatal(err)
	}

	err := ex.Install(context.Background(), p, map[string]string{"source": source}, target, io.Discard)
	if err != nil {
		t.Fatalf("Install: %v", err)
	}

	dest, err := os.Readlink(target)
	if err != nil {
		t.Fatalf("target is not a symlink: %v", err)
	}
	if dest != source {
		t.Fatalf("symlink points at %q, want %q", dest, source)
	}
	data, err := os.ReadFile(filepath.Join(target, "hello.txt"))
	if err != nil {
		t.Fatalf("source not reachable through symlink: %v", err)
	}
	if string(data) != "hi" {
		t.Fatalf("hello.txt through symlink = %q, want hi", data)
	}
	if _, err := os.Lstat(filepath.Join(target, "stale.txt")); !os.IsNotExist(err) {
		t.Fatalf("stale content survived install: %v", err)
	}
}

func TestSymlinkBuiltinInstallMissingSourceFails(t *testing.T) {
	ex, paths := newTestExecutor(t)
	p := symlinkBuiltin(t)

	target := filepath.Join(paths.Root, "target")
	err := ex.Install(context.Background(), p, map[string]string{}, target, io.Discard)
	if err == nil {
		t.Fatal("Install without source param: want error, got nil")
	}
	if _, err := os.Lstat(target); !os.IsNotExist(err) {
		t.Fatalf("target created despite missing source param: %v", err)
	}
}
