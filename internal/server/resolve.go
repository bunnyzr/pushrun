package server

import (
	"errors"
	"net/http"

	"github.com/bunnyzr/pushrun/internal/gitx"
	"github.com/bunnyzr/pushrun/internal/project"
)

// resolveProject implements GET /api/v1/resolve?repo=<host/path>[&project=<name>]:
// it answers which project a push of the given repo identity would trigger,
// running the exact same matching as the pre-receive hook, so non-push client
// commands (status, logs, start/stop/rerun, ...) address the same project a
// push would. The optional project parameter asserts an explicit project,
// mirroring the X-PushRun-Project push header.
//
// Success: 200 {"project": "<name>"}. Matching failures: 409 with the
// MatchError code (unknown_repo:<id>, project_required:<names>,
// ambiguous_project:<names>, project_does_not_contain_repo:<p>,
// unknown_project:<p>) so callers can act on it; an unparseable repo
// reference is a 400.
func (s *Server) resolveProject(w http.ResponseWriter, r *http.Request) {
	repo := r.URL.Query().Get("repo")
	if repo == "" {
		s.fail(w, r, http.StatusBadRequest, "invalid",
			errors.New("missing repo query parameter (want ?repo=<host/path>)"))
		return
	}
	if _, err := gitx.NormalizeRepo(repo); err != nil {
		s.fail(w, r, http.StatusBadRequest, "invalid", err)
		return
	}
	name, err := project.MatchRepo(s.paths, repo, r.URL.Query().Get("project"))
	if err != nil {
		var me *project.MatchError
		if errors.As(err, &me) {
			s.fail(w, r, http.StatusConflict, me.Code, err)
			return
		}
		s.fail(w, r, http.StatusInternalServerError, "internal", err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"project": name})
}
