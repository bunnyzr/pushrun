package server

import (
	"fmt"
	"io/fs"
	"net/http"
	"path"
	"strings"
)

// staticHandler serves the web console from webFS. Existing files are
// served as-is: hashed assets under assets/ are cached immutably for a
// year, index.html is never cached. Extensionless misses are SPA deep links
// and serve index.html with no-cache so the client-side router can take
// over; a miss whose last path segment looks like a filename (contains a
// dot) is a genuine 404. Paths under the reserved /api/, /git/ and
// /internal/ prefixes that fall through to this catch-all answer with the
// standard JSON 404, never the SPA shell.
func (s *Server) staticHandler(webFS fs.FS) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		name := strings.TrimPrefix(path.Clean("/"+r.URL.Path), "/")
		if name == "" {
			name = "index.html"
		}

		if name == "api" || strings.HasPrefix(name, "api/") ||
			name == "git" || strings.HasPrefix(name, "git/") ||
			name == "internal" || strings.HasPrefix(name, "internal/") {
			s.fail(w, r, http.StatusNotFound, "not_found",
				fmt.Errorf("unknown endpoint %q", r.URL.Path))
			return
		}

		if info, err := fs.Stat(webFS, name); err == nil && !info.IsDir() {
			switch {
			case name == "index.html":
				w.Header().Set("Cache-Control", "no-cache")
			case strings.HasPrefix(name, "assets/"):
				w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
			}
			http.ServeFileFS(w, r, webFS, name)
			return
		}

		// A miss that looks like a filename is a genuine 404; anything else
		// is an SPA deep link and gets the shell.
		if strings.Contains(path.Base(name), ".") {
			s.fail(w, r, http.StatusNotFound, "not_found",
				fmt.Errorf("no such file %q", r.URL.Path))
			return
		}
		w.Header().Set("Cache-Control", "no-cache")
		http.ServeFileFS(w, r, webFS, "index.html")
	})
}
