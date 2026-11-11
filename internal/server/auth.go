package server

import (
	"crypto/subtle"
	"errors"
	"net/http"
	"strings"
)

// authMiddleware enforces Bearer-token auth on /api/v1/* when auth is
// enabled (see docs/design.md). The version endpoint is exempt so the web
// console can learn whether auth is enabled before it has a token. The
// token value is never logged; failures return standard 401 error JSON.
func (s *Server) authMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !s.cfg.Auth.Enabled || !strings.HasPrefix(r.URL.Path, "/api/v1/") ||
			r.URL.Path == "/api/v1/version" {
			next.ServeHTTP(w, r)
			return
		}
		token, ok := strings.CutPrefix(r.Header.Get("Authorization"), "Bearer ")
		if !ok || subtle.ConstantTimeCompare([]byte(token), []byte(s.deps.Token)) != 1 {
			s.fail(w, r, http.StatusUnauthorized, "unauthorized",
				errors.New("missing or invalid bearer token"))
			return
		}
		next.ServeHTTP(w, r)
	})
}
