package project

import (
	"fmt"
	"sort"
	"strings"

	"github.com/bunnyzr/pushrun/internal/config"
	"github.com/bunnyzr/pushrun/internal/gitx"
)

// MatchError is a failed push-to-project resolution. Code is a stable,
// machine-readable token (e.g. "project_required:demo,www"); Error renders
// it together with an actionable message for the pusher.
type MatchError struct {
	Code string
	Msg  string
}

func (e *MatchError) Error() string { return e.Code + ": " + e.Msg }

// MatchRepo resolves which project a push to repo triggers, returning the
// project name. repo is the pushed repo identity (any form accepted by
// gitx.NormalizeRepo); explicitProject is the project the pusher asserted
// (empty when the push carried no explicit project).
//
// Resolution:
//   - explicitProject set: the project must mount the repo in one of its git
//     nodes, else project_does_not_contain_repo.
//   - unset: every project with a git node for the repo is a candidate. The
//     push triggers only when the repo is the primary git node of exactly
//     one project and no other project mounts it. Candidates that are all
//     non-primary yield project_required; multiple candidates with a primary
//     among them yield ambiguous_project; no candidates yields unknown_repo.
func MatchRepo(paths config.Paths, repo, explicitProject string) (string, error) {
	identity, err := gitx.NormalizeRepo(repo)
	if err != nil {
		return "", err
	}

	if explicitProject != "" {
		p, err := Load(paths, explicitProject)
		if err != nil {
			return "", &MatchError{
				Code: "unknown_project:" + explicitProject,
				Msg: fmt.Sprintf("no project matches %q; create it via POST /api/v1/projects (%v)",
					explicitProject, err),
			}
		}
		if hasGitNode(p, identity) {
			return explicitProject, nil
		}
		return "", &MatchError{
			Code: "project_does_not_contain_repo:" + explicitProject,
			Msg: fmt.Sprintf("project %q has no git node for repo %q; check the project definition or drop the explicit project flag",
				explicitProject, identity),
		}
	}

	names, err := List(paths)
	if err != nil {
		return "", fmt.Errorf("match repo %q: %w", identity, err)
	}
	var primary, satellite []string
	for _, name := range names {
		p, err := Load(paths, name)
		if err != nil {
			continue // unloadable project: not a candidate, but never blocks matching
		}
		if isPrimary, ok := mountsRepo(p, identity); ok {
			if isPrimary {
				primary = append(primary, name)
			} else {
				satellite = append(satellite, name)
			}
		}
	}

	candidates := append(append([]string(nil), primary...), satellite...)
	switch {
	case len(candidates) == 0:
		return "", &MatchError{
			Code: "unknown_repo:" + identity,
			Msg: fmt.Sprintf("no project has a git node for repo %q; create a project mounting it via POST /api/v1/projects",
				identity),
		}
	case len(candidates) == 1 && len(primary) == 1:
		return primary[0], nil
	case len(primary) == 0:
		return "", &MatchError{
			Code: "project_required:" + strings.Join(candidates, ","),
			Msg: fmt.Sprintf("repo %q is a non-primary git node of %s; re-push with an explicit project (X-PushRun-Project header) naming one of: %s",
				identity, projectList(candidates), strings.Join(candidates, ", ")),
		}
	default:
		sort.Strings(candidates)
		return "", &MatchError{
			Code: "ambiguous_project:" + strings.Join(candidates, ","),
			Msg: fmt.Sprintf("repo %q is mounted by multiple projects (%s); re-push with an explicit project (X-PushRun-Project header) naming one of them",
				identity, strings.Join(candidates, ", ")),
		}
	}
}

// hasGitNode reports whether any git node of p mounts the repo identity.
func hasGitNode(p *Project, identity string) bool {
	_, ok := mountsRepo(p, identity)
	return ok
}

// mountsRepo reports whether p mounts the repo identity in a git node, and
// whether that node is primary.
func mountsRepo(p *Project, identity string) (primary, ok bool) {
	for _, n := range p.Tree {
		if n.Mount == nil || n.Mount.Provider != "git" {
			continue
		}
		id, err := gitx.NormalizeRepo(n.Mount.Params["repo"])
		if err != nil || id != identity {
			continue
		}
		return n.Mount.Primary, true
	}
	return false, false
}

// projectList renders ["a", "b"] as "projects a and b".
func projectList(names []string) string {
	if len(names) == 1 {
		return "project " + names[0]
	}
	return "projects " + strings.Join(names, ", ")
}
