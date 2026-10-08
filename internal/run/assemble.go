package run

import (
	"context"
	"fmt"
	"io"
	"maps"
	"os"
	"path/filepath"

	"github.com/bunnyzr/pushrun/internal/gitx"
	"github.com/bunnyzr/pushrun/internal/instance"
	"github.com/bunnyzr/pushrun/internal/project"
	"github.com/bunnyzr/pushrun/internal/provider"
)

// triggerNode returns the tree index of the git node that receives the
// triggering commit: the node mounting triggerRepo, or — when triggerRepo is
// empty (API-triggered runs) — the primary git node. It returns -1 when the
// project has no git node at all, and an error when a non-empty triggerRepo
// matches no node of the project.
func triggerNode(proj *project.Project, triggerRepo string) (int, error) {
	primary := -1
	for i, n := range proj.Tree {
		if n.Mount == nil || n.Mount.Provider != "git" {
			continue
		}
		if n.Mount.Primary && primary == -1 {
			primary = i
		}
		if triggerRepo == "" {
			continue
		}
		id, err := gitx.NormalizeRepo(n.Mount.Params["repo"])
		if err == nil && id == triggerRepo {
			return i, nil
		}
	}
	if triggerRepo != "" {
		return -1, fmt.Errorf("project %q has no git node for repo %q", proj.Name, triggerRepo)
	}
	return primary, nil
}

// gitNodeEnv builds the workspace and git-node environment injected into
// pipeline steps: CI_WORKSPACE is the instance root, the CI_TRIGGER_* pair
// describes the node whose repo triggered the run, and the CI_PROJECT_* pair
// describes the primary git node. Pairs with no corresponding git node are
// omitted.
func gitNodeEnv(proj *project.Project, instDir string, triggerIdx int) []string {
	env := []string{"CI_WORKSPACE=" + instDir}
	if triggerIdx >= 0 {
		n := proj.Tree[triggerIdx]
		env = append(env,
			"CI_TRIGGER_PROJECT_DIR="+filepath.Join(instDir, n.Path),
			"CI_TRIGGER_PROJECT_NAME="+repoName(n.Mount.Params["repo"]))
	}
	if primaryIdx, err := triggerNode(proj, ""); err == nil && primaryIdx >= 0 {
		n := proj.Tree[primaryIdx]
		env = append(env,
			"CI_PROJECT_DIR="+filepath.Join(instDir, n.Path),
			"CI_PROJECT_NAME="+repoName(n.Mount.Params["repo"]))
	}
	return env
}

// repoName returns the path part of a repo reference's normalized identity
// ("team/proto" for "git.example.com/team/proto"), or "" when the reference
// does not normalize.
func repoName(repo string) string {
	id, err := gitx.NormalizeRepo(repo)
	if err != nil {
		return ""
	}
	return gitx.RepoPath(id)
}

// assemble builds the instance directory from the project tree and returns
// its path. Plain nodes become directories; mount nodes are exactly replaced
// by their provider's install output. The triggering commit is injected into
// the trigger node's params; every other git node installs its snapshot ref.
// Nodes pushrun does not own are never touched.
func (m *Manager) assemble(ctx context.Context, proj *project.Project, req Request, providers map[int]*provider.Provider, triggerIdx int, log io.Writer) (string, error) {
	instDir := filepath.Join(m.paths.Instances, proj.Name, req.Instance)
	if err := os.MkdirAll(instDir, 0o755); err != nil {
		return "", fmt.Errorf("create instance dir: %w", err)
	}
	for i, n := range proj.Tree {
		target, err := instance.SafeJoin(instDir, n.Path)
		if err != nil {
			return "", fmt.Errorf("node %q: %w", n.Path, err)
		}
		if n.Mount == nil {
			if err := os.MkdirAll(target, 0o755); err != nil {
				return "", fmt.Errorf("create node %q: %w", n.Path, err)
			}
			continue
		}
		// Exact replacement of the mount node.
		if err := os.RemoveAll(target); err != nil {
			return "", fmt.Errorf("replace node %q: %w", n.Path, err)
		}
		if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
			return "", fmt.Errorf("create node %q parent: %w", n.Path, err)
		}
		params := maps.Clone(n.Mount.Params)
		if n.Mount.Provider == "git" && i == triggerIdx && req.Commit != "" {
			if params == nil {
				params = map[string]string{}
			}
			params["commit"] = req.Commit
		}
		if err := m.exec.Install(ctx, providers[i], params, target, log); err != nil {
			return "", err
		}
	}
	return instDir, nil
}
