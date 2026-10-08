package provider

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/bunnyzr/pushrun/internal/config"
)

// specExampleYAML is the §10.2 example.
const specExampleYAML = `schema: pushrun.provider/v1
id: example-static-runtime
name: Example Static Runtime
description: Downloads a runtime tarball and installs it into instances.
warmup: scripts/warmup.sh
install: scripts/install.sh
parameters:
  - id: version
    label: Runtime version
    type: string
    scope: warmup
    required: true
`

func specExampleProvider() *Provider {
	return &Provider{
		Schema:      SchemaV1,
		ID:          "example-static-runtime",
		Name:        "Example Static Runtime",
		Description: "Downloads a runtime tarball and installs it into instances.",
		Warmup:      "scripts/warmup.sh",
		Install:     "scripts/install.sh",
		Params: []Param{
			{ID: "version", Label: "Runtime version", Type: "string", Scope: "warmup", Required: true},
		},
	}
}

// writeScripts creates empty warmup/install scripts referenced by p under
// the provider dir.
func writeScripts(t *testing.T, paths config.Paths, p *Provider) {
	t.Helper()
	dir := filepath.Join(paths.Providers, p.ID)
	for _, script := range []string{p.Warmup, p.Install} {
		if script == "" {
			continue
		}
		fp := filepath.Join(dir, filepath.FromSlash(script))
		if err := os.MkdirAll(filepath.Dir(fp), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(fp, []byte("#!/bin/sh\n"), 0o755); err != nil {
			t.Fatal(err)
		}
	}
}

func TestLoadSpecExample(t *testing.T) {
	root := t.TempDir()
	paths := config.NewPaths(root)
	p := specExampleProvider()
	writeScripts(t, paths, p)
	dir := filepath.Join(paths.Providers, p.ID)
	if err := os.WriteFile(filepath.Join(dir, "provider.yaml"), []byte(specExampleYAML), 0o644); err != nil {
		t.Fatal(err)
	}

	got, err := Load(paths, p.ID)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if !reflect.DeepEqual(got, p) {
		t.Fatalf("loaded provider mismatch\ngot:  %+v\nwant: %+v", got, p)
	}
}

func TestSaveLoadRoundTrip(t *testing.T) {
	root := t.TempDir()
	paths := config.NewPaths(root)
	p := specExampleProvider()

	if err := Save(paths, p); err != nil {
		t.Fatalf("Save: %v", err)
	}
	if _, err := os.Stat(filepath.Join(paths.Providers, p.ID, "provider.yaml")); err != nil {
		t.Fatalf("saved file missing: %v", err)
	}
	writeScripts(t, paths, p)

	got, err := Load(paths, p.ID)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if !reflect.DeepEqual(got, p) {
		t.Fatalf("round-trip mismatch\ngot:  %+v\nwant: %+v", got, p)
	}
}

func TestValidate(t *testing.T) {
	valid := func() *Provider { return specExampleProvider() }

	tests := []struct {
		name    string
		mutate  func(*Provider)
		wantErr bool
	}{
		{name: "spec example is valid", mutate: func(*Provider) {}},
		{
			name:    "missing install",
			mutate:  func(p *Provider) { p.Install = "" },
			wantErr: true,
		},
		{
			name:    "absolute install path",
			mutate:  func(p *Provider) { p.Install = "/abs/install.sh" },
			wantErr: true,
		},
		{
			name:    "install path escapes provider dir",
			mutate:  func(p *Provider) { p.Install = "../install.sh" },
			wantErr: true,
		},
		{
			name:    "unclean warmup path",
			mutate:  func(p *Provider) { p.Warmup = "scripts//warmup.sh" },
			wantErr: true,
		},
		{
			name:    "bad param type",
			mutate:  func(p *Provider) { p.Params[0].Type = "file" },
			wantErr: true,
		},
		{
			name:    "bad param scope",
			mutate:  func(p *Provider) { p.Params[0].Scope = "build" },
			wantErr: true,
		},
		{
			name:    "secret param with default",
			mutate:  func(p *Provider) { p.Params[0].Secret = true; p.Params[0].Default = "x" },
			wantErr: true,
		},
		{
			name:    "duplicate param id",
			mutate:  func(p *Provider) { p.Params = append(p.Params, p.Params[0]) },
			wantErr: true,
		},
		{
			name:    "missing param id",
			mutate:  func(p *Provider) { p.Params[0].ID = "" },
			wantErr: true,
		},
		{
			name:    "missing id",
			mutate:  func(p *Provider) { p.ID = "" },
			wantErr: true,
		},
		{
			name: "select with options is valid",
			mutate: func(p *Provider) {
				p.Params[0].Type = ParamTypeSelect
				p.Params[0].Options = []string{"a", "b"}
			},
		},
		{
			name: "select without options",
			mutate: func(p *Provider) {
				p.Params[0].Type = ParamTypeSelect
			},
			wantErr: true,
		},
		{
			name:    "options on non-select type",
			mutate:  func(p *Provider) { p.Params[0].Options = []string{"a"} },
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			p := valid()
			tt.mutate(p)
			err := p.Validate()
			if tt.wantErr && err == nil {
				t.Fatal("expected error, got nil")
			}
			if !tt.wantErr && err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
		})
	}
}

func TestLoadMissingScript(t *testing.T) {
	root := t.TempDir()
	paths := config.NewPaths(root)
	p := specExampleProvider()
	if err := Save(paths, p); err != nil {
		t.Fatalf("Save: %v", err)
	}
	if _, err := Load(paths, p.ID); err == nil {
		t.Fatal("expected error loading provider whose scripts are missing")
	}
}

func TestListAndDelete(t *testing.T) {
	root := t.TempDir()
	paths := config.NewPaths(root)

	ids, err := List(paths)
	if err != nil {
		t.Fatalf("List on empty store: %v", err)
	}
	if len(ids) != 0 {
		t.Fatalf("expected empty list, got %v", ids)
	}

	for _, id := range []string{"b-prov", "a-prov"} {
		p := &Provider{ID: id, Name: id, Install: "install.sh"}
		if err := Save(paths, p); err != nil {
			t.Fatalf("Save %s: %v", id, err)
		}
		writeScripts(t, paths, p)
	}

	ids, err = List(paths)
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if !reflect.DeepEqual(ids, []string{"a-prov", "b-prov"}) {
		t.Fatalf("List = %v, want [a-prov b-prov]", ids)
	}

	if err := Delete(paths, "a-prov"); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	ids, err = List(paths)
	if err != nil {
		t.Fatalf("List after delete: %v", err)
	}
	if !reflect.DeepEqual(ids, []string{"b-prov"}) {
		t.Fatalf("List after delete = %v, want [b-prov]", ids)
	}
	if _, err := os.Stat(filepath.Join(paths.Providers, "a-prov")); !os.IsNotExist(err) {
		t.Fatalf("provider dir still present after delete: %v", err)
	}
	if err := Delete(paths, "a-prov"); err == nil {
		t.Fatal("expected error deleting missing provider")
	}
}

func TestLoadMissing(t *testing.T) {
	paths := config.NewPaths(t.TempDir())
	if _, err := Load(paths, "nope"); err == nil {
		t.Fatal("expected error loading missing provider")
	}
}

func TestSaveValidates(t *testing.T) {
	paths := config.NewPaths(t.TempDir())
	bad := &Provider{ID: "bad", Name: "Bad"}
	if err := Save(paths, bad); err == nil {
		t.Fatal("expected Save to reject invalid provider")
	}
}

func TestBuiltins(t *testing.T) {
	builtins := Builtins()
	if len(builtins) != 2 {
		t.Fatalf("Builtins returned %d providers, want 2", len(builtins))
	}
	byID := map[string]Provider{}
	for _, b := range builtins {
		if err := b.Validate(); err != nil {
			t.Fatalf("builtin %q invalid: %v", b.ID, err)
		}
		byID[b.ID] = b
	}

	git, ok := byID["git"]
	if !ok {
		t.Fatal("builtins missing git")
	}
	wantGitParams := []Param{
		{ID: "repo", Label: "Repository URL", Type: "string", Scope: "warmup", Required: true},
		{ID: "branch", Label: "Branch", Type: "string", Scope: "warmup", Required: true},
		{ID: "commit", Label: "Commit", Type: "string", Scope: "install"},
	}
	if !reflect.DeepEqual(git.Params, wantGitParams) {
		t.Fatalf("git params mismatch\ngot:  %+v\nwant: %+v", git.Params, wantGitParams)
	}

	symlink, ok := byID["symlink"]
	if !ok {
		t.Fatal("builtins missing symlink")
	}
	wantSymlinkParams := []Param{
		{ID: "source", Label: "Source path", Type: "string", Scope: "install", Required: true},
	}
	if !reflect.DeepEqual(symlink.Params, wantSymlinkParams) {
		t.Fatalf("symlink params mismatch\ngot:  %+v\nwant: %+v", symlink.Params, wantSymlinkParams)
	}
}
