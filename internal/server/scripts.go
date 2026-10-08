package server

import (
	"crypto/subtle"
	"errors"
	"net/http"
	"strings"

	"github.com/bunnyzr/pushrun/internal/client"
)

// serveInstallSh and serveClientSh render the client scripts with the server
// URL (derived from the request) and the daemon version baked in (see
// docs/design.md). Both are token-authenticated like the API: the scripts are served by
// the daemon and a leaked script pair is harmless, but an open client.sh
// download would let install.sh "succeed" without a valid token.
func (s *Server) serveInstallSh(w http.ResponseWriter, r *http.Request) {
	s.serveScript(w, r, true)
}

func (s *Server) serveClientSh(w http.ResponseWriter, r *http.Request) {
	s.serveScript(w, r, false)
}

func (s *Server) serveScript(w http.ResponseWriter, r *http.Request, install bool) {
	if s.cfg.Auth.Enabled {
		token, ok := strings.CutPrefix(r.Header.Get("Authorization"), "Bearer ")
		if !ok || subtle.ConstantTimeCompare([]byte(token), []byte(s.deps.Token)) != 1 {
			s.fail(w, r, http.StatusUnauthorized, "unauthorized",
				errors.New("missing or invalid bearer token"))
			return
		}
	}
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	if install {
		_, _ = w.Write(client.RenderInstall(serverURL(r), s.deps.Version))
	} else {
		_, _ = w.Write(client.RenderClient(serverURL(r), s.deps.Version))
	}
}

// serverURL derives the externally visible base URL from the request.
func serverURL(r *http.Request) string {
	scheme := "http"
	if r.TLS != nil {
		scheme = "https"
	}
	if p := r.Header.Get("X-Forwarded-Proto"); p == "http" || p == "https" {
		scheme = p
	}
	return scheme + "://" + r.Host
}
