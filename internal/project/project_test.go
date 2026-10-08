package project

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/bunnyzr/pushrun/internal/config"
)

// specExampleYAML is the §10.1 example, with the health check expressed via
// the type/target fields of the model.
const specExampleYAML = `schema: pushrun.project/v1
name: hello-web
display_name: Hello Web
tree:
  - path: app
    mount:
      provider: git
      primary: true
      params: { repo: github.com/acme/hello-web, branch: main }
  - path: runtime
    mount:
      provider: example-static-runtime
      params: { version: "1.2.3" }
pipeline:
  - name: build
    run: make -C app build
    timeout: 300
  - name: serve
    run: ./app/bin/hello --port "$CI_PORT"
    background: true
    health: { type: http, target: "http://127.0.0.1:$CI_PORT/healthz" }
`

func specExampleProject() *Project {
	return &Project{
		Schema:      SchemaV1,
		Name:        "hello-web",
		DisplayName: "Hello Web",
		Tree: []Node{
			{Path: "app", Mount: &Mount{
				Provider: "git",
				Primary:  true,
				Params:   map[string]string{"repo": "github.com/acme/hello-web", "branch": "main"},
			}},
			{Path: "runtime", Mount: &Mount{
				Provider: "example-static-runtime",
				Params:   map[string]string{"version": "1.2.3"},
			}},
		},
		Pipeline: []Step{
			{Name: "build", Run: "make -C app build", Timeout: 300},
			{
				Name:       "serve",
				Run:        `./app/bin/hello --port "$CI_PORT"`,
				Background: true,
				Health:     &Health{Type: "http", Target: "http://127.0.0.1:$CI_PORT/healthz"},
			},
		},
	}
}

func TestLoadSpecExample(t *testing.T) {
	root := t.TempDir()
	paths := config.NewPaths(root)
	if err := os.MkdirAll(paths.Projects, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(paths.Projects, "hello-web.yaml"), []byte(specExampleYAML), 0o644); err != nil {
		t.Fatal(err)
	}

	got, err := Load(paths, "hello-web")
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if !reflect.DeepEqual(got, specExampleProject()) {
		t.Fatalf("loaded project mismatch\ngot:  %+v\nwant: %+v", got, specExampleProject())
	}
}

func TestSaveLoadRoundTrip(t *testing.T) {
	root := t.TempDir()
	paths := config.NewPaths(root)

	if err := Save(paths, specExampleProject()); err != nil {
		t.Fatalf("Save: %v", err)
	}
	if _, err := os.Stat(filepath.Join(paths.Projects, "hello-web.yaml")); err != nil {
		t.Fatalf("saved file missing: %v", err)
	}

	got, err := Load(paths, "hello-web")
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if !reflect.DeepEqual(got, specExampleProject()) {
		t.Fatalf("round-trip mismatch\ngot:  %+v\nwant: %+v", got, specExampleProject())
	}
}

func TestValidate(t *testing.T) {
	valid := func() *Project { return specExampleProject() }

	tests := []struct {
		name    string
		mutate  func(*Project)
		wantErr bool
	}{
		{name: "spec example is valid", mutate: func(*Project) {}},
		{
			name: "mount node with child",
			mutate: func(p *Project) {
				p.Tree = append(p.Tree, Node{Path: "app/sub"})
			},
			wantErr: true,
		},
		{
			name: "two background steps",
			mutate: func(p *Project) {
				p.Pipeline = append(p.Pipeline, Step{Name: "b2", Run: "x", Background: true})
			},
			wantErr: true,
		},
		{
			name: "background step not last",
			mutate: func(p *Project) {
				p.Pipeline = append([]Step{{Name: "bg", Run: "x", Background: true}}, p.Pipeline...)
			},
			wantErr: true,
		},
		{
			name: "two primary mounts",
			mutate: func(p *Project) {
				p.Tree[1].Mount.Primary = true
			},
			wantErr: true,
		},
		{
			name: "absolute node path",
			mutate: func(p *Project) {
				p.Tree[0].Path = "/abs/path"
			},
			wantErr: true,
		},
		{
			name: "unclean node path",
			mutate: func(p *Project) {
				p.Tree[0].Path = "app//sub"
			},
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

func TestListAndDelete(t *testing.T) {
	root := t.TempDir()
	paths := config.NewPaths(root)

	names, err := List(paths)
	if err != nil {
		t.Fatalf("List on empty store: %v", err)
	}
	if len(names) != 0 {
		t.Fatalf("expected empty list, got %v", names)
	}

	for _, p := range []*Project{
		{Name: "b-proj", Tree: []Node{{Path: "app"}}},
		{Name: "a-proj", Tree: []Node{{Path: "app"}}},
	} {
		if err := Save(paths, p); err != nil {
			t.Fatalf("Save %s: %v", p.Name, err)
		}
	}

	names, err = List(paths)
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if !reflect.DeepEqual(names, []string{"a-proj", "b-proj"}) {
		t.Fatalf("List = %v, want [a-proj b-proj]", names)
	}

	if err := Delete(paths, "a-proj"); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	names, err = List(paths)
	if err != nil {
		t.Fatalf("List after delete: %v", err)
	}
	if !reflect.DeepEqual(names, []string{"b-proj"}) {
		t.Fatalf("List after delete = %v, want [b-proj]", names)
	}
	if err := Delete(paths, "a-proj"); err == nil {
		t.Fatal("expected error deleting missing project")
	}
}

func TestLoadMissing(t *testing.T) {
	paths := config.NewPaths(t.TempDir())
	if _, err := Load(paths, "nope"); err == nil {
		t.Fatal("expected error loading missing project")
	}
}

func TestSaveValidates(t *testing.T) {
	paths := config.NewPaths(t.TempDir())
	bad := &Project{Name: "bad", Tree: []Node{{Path: "/abs"}}}
	if err := Save(paths, bad); err == nil {
		t.Fatal("expected Save to reject invalid project")
	}
}
