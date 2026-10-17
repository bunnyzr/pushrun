package project_test

import (
	"strings"
	"testing"

	"github.com/bunnyzr/pushrun/internal/config"
	"github.com/bunnyzr/pushrun/internal/project"
)

// saveGitProject registers a project with one git node mounting repo.
func saveGitProject(t *testing.T, paths config.Paths, name, repo string, primary bool) {
	t.Helper()
	p := &project.Project{
		Name: name,
		Tree: []project.Node{
			{Path: "app", Mount: &project.Mount{Provider: "git", Primary: primary,
				Params: map[string]string{"repo": repo, "branch": "main"}}},
		},
	}
	if err := project.Save(paths, p); err != nil {
		t.Fatal(err)
	}
}

func TestMatchRepo(t *testing.T) {
	tests := []struct {
		name     string
		projects []struct {
			name    string
			repo    string
			primary bool
		}
		repo     string
		explicit string
		want     string // project name, or "" when an error is expected
		wantCode string // substring of the error code when want == ""
	}{
		{
			name: "single primary matches without explicit project",
			projects: []struct {
				name, repo string
				primary    bool
			}{{"demo", "git.example.com/team/proto", true}},
			repo: "git.example.com/team/proto",
			want: "demo",
		},
		{
			name: "full URL and shorthand normalize to the same identity",
			projects: []struct {
				name, repo string
				primary    bool
			}{
				{"demo", "https://git.example.com/team/proto.git", true},
			},
			repo: "git.example.com/team/proto",
			want: "demo",
		},
		{
			name:     "unknown repo",
			repo:     "git.example.com/team/ghost",
			wantCode: "unknown_repo:git.example.com/team/ghost",
		},
		{
			name: "non-primary only requires an explicit project",
			projects: []struct {
				name, repo string
				primary    bool
			}{
				{"demo", "git.example.com/team/proto", false},
			},
			repo:     "git.example.com/team/proto",
			wantCode: "project_required:demo",
		},
		{
			name: "non-primary matches across projects list all candidates",
			projects: []struct {
				name, repo string
				primary    bool
			}{
				{"b-project", "git.example.com/team/proto", false},
				{"a-project", "git.example.com/team/proto", false},
			},
			repo:     "git.example.com/team/proto",
			wantCode: "project_required:",
		},
		{
			name: "primary in two projects is ambiguous",
			projects: []struct {
				name, repo string
				primary    bool
			}{
				{"demo", "git.example.com/team/proto", true},
				{"other", "git.example.com/team/proto", true},
			},
			repo:     "git.example.com/team/proto",
			wantCode: "ambiguous_project:demo,other",
		},
		{
			name: "primary in one project but mounted by another is ambiguous",
			projects: []struct {
				name, repo string
				primary    bool
			}{
				{"demo", "git.example.com/team/proto", true},
				{"other", "git.example.com/team/proto", false},
			},
			repo:     "git.example.com/team/proto",
			wantCode: "ambiguous_project:demo,other",
		},
		{
			name: "explicit project containing the repo triggers it",
			projects: []struct {
				name, repo string
				primary    bool
			}{
				{"demo", "git.example.com/team/proto", false},
			},
			repo:     "git.example.com/team/proto",
			explicit: "demo",
			want:     "demo",
		},
		{
			name: "explicit project without the repo is rejected",
			projects: []struct {
				name, repo string
				primary    bool
			}{
				{"demo", "git.example.com/team/proto", true},
				{"other", "git.example.com/team/unrelated", true},
			},
			repo:     "git.example.com/team/proto",
			explicit: "other",
			wantCode: "project_does_not_contain_repo:other",
		},
		{
			name:     "explicit unknown project",
			repo:     "git.example.com/team/proto",
			explicit: "ghost",
			wantCode: "unknown_project:ghost",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			paths := config.NewPaths(t.TempDir())
			for _, p := range tt.projects {
				saveGitProject(t, paths, p.name, p.repo, p.primary)
			}
			got, err := project.MatchRepo(paths, tt.repo, tt.explicit)
			if tt.want != "" {
				if err != nil {
					t.Fatalf("MatchRepo: %v", err)
				}
				if got != tt.want {
					t.Fatalf("MatchRepo = %q, want %q", got, tt.want)
				}
				return
			}
			if err == nil {
				t.Fatalf("MatchRepo = %q, want error %q", got, tt.wantCode)
			}
			if !strings.Contains(err.Error(), tt.wantCode) {
				t.Fatalf("error %q does not contain code %q", err, tt.wantCode)
			}
		})
	}
}

// Errors stream to the pusher, so they must name the candidate projects and
// the remedy.
func TestMatchRepoErrorMessagesActionable(t *testing.T) {
	paths := config.NewPaths(t.TempDir())
	saveGitProject(t, paths, "demo", "git.example.com/team/proto", false)
	saveGitProject(t, paths, "www", "git.example.com/team/proto", false)

	_, err := project.MatchRepo(paths, "git.example.com/team/proto", "")
	if err == nil {
		t.Fatal("want project_required error")
	}
	for _, want := range []string{"project_required:", "demo", "www", "X-PushRun-Project"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error missing %q: %v", want, err)
		}
	}
}
