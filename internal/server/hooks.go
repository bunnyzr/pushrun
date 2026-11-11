package server

import (
	"bytes"
	"context"
	"crypto/subtle"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"strings"

	"github.com/bunnyzr/pushrun/internal/config"
	"github.com/bunnyzr/pushrun/internal/gitx"
	"github.com/bunnyzr/pushrun/internal/project"
	"github.com/bunnyzr/pushrun/internal/run"
)

// hookSecretHeader carries the shared secret between the local git hook and
// the daemon. It is deliberately independent of the user-facing auth token:
// the hook runs on the daemon host and the git transport already
// authenticated the pusher.
const hookSecretHeader = "X-PushRun-Hook-Secret"

// hookSecret returns the persistent hook shared secret from
// <root>/hooks-secret, generating it on first use.
func (s *Server) hookSecret() (string, error) {
	s.hookOnce.Do(func() {
		s.hookSecretVal, s.hookSecretErr = config.EnsureHookSecret(s.paths.Root)
	})
	return s.hookSecretVal, s.hookSecretErr
}

// handleHookPostReceive is the daemon side of the post-receive hook. It is
// loopback-only and guarded by the hook secret — never the user token. The
// response body is the run output, streamed as it happens; the hook relays
// it into the push sideband and reads the trailing CI_STATUS= line.
func (s *Server) handleHookPostReceive(w http.ResponseWriter, r *http.Request) {
	if !isLoopbackPeer(r.RemoteAddr) {
		s.fail(w, r, http.StatusForbidden, "forbidden",
			errors.New("the hook endpoint is loopback-only"))
		return
	}
	secret, err := s.hookSecret()
	if err != nil {
		s.fail(w, r, http.StatusInternalServerError, "internal", err)
		return
	}
	if subtle.ConstantTimeCompare([]byte(r.Header.Get(hookSecretHeader)), []byte(secret)) != 1 {
		s.fail(w, r, http.StatusForbidden, "forbidden",
			errors.New("missing or invalid hook secret"))
		return
	}

	repo := r.Header.Get("X-PushRun-Repo")
	if repo == "" {
		s.fail(w, r, http.StatusBadRequest, "invalid_argument",
			errors.New("X-PushRun-Repo and X-PushRun-Branch headers are required (stale hook? hit any /git/ URL to refresh hooks)"))
		return
	}
	identity, err := gitx.NormalizeRepo(repo)
	if err != nil {
		s.fail(w, r, http.StatusBadRequest, "invalid_argument", err)
		return
	}
	branch := r.Header.Get("X-PushRun-Branch")
	if branch == "" {
		s.fail(w, r, http.StatusBadRequest, "invalid_argument",
			errors.New("X-PushRun-Branch header is required"))
		return
	}

	req := run.Request{
		Branch:      branch,
		Commit:      r.Header.Get("X-PushRun-Commit"),
		User:        r.Header.Get("X-PushRun-User"),
		Action:      r.Header.Get("X-PushRun-Action"),
		Instance:    r.Header.Get("X-PushRun-Instance"),
		TriggerRepo: identity,
		// The host the pusher connected to, forwarded from the push
		// connection's Host header via the hook environment; it becomes the
		// host of the CI_URL trailer. Host part only, no port.
		DisplayHost: displayHost(r.Header.Get("X-PushRun-Host")),
	}
	if req.Action == "" {
		req.Action = run.ActionRun
	}
	if req.Instance == "" {
		req.Instance = "default"
	}

	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	w.WriteHeader(http.StatusOK)
	fw := &flushWriter{w: w}

	// Resolve the pushed repo to the project to trigger. A failed resolution
	// must be actionable in the push output, and the hook (which sees
	// CI_STATUS=FAILED) exits non-zero.
	projectName, err := project.MatchRepo(s.paths, identity, r.Header.Get("X-PushRun-Project"))
	if err != nil {
		_, _ = fmt.Fprintf(fw, "pushrun: %v\n", err)
		_, _ = fmt.Fprint(fw, "CI_STATUS=FAILED\n")
		return
	}
	req.Project = projectName

	if req.Commit == "" {
		// Fall back to the ref stored by the push.
		repoDir, err := gitx.BareRepoDir(s.paths, identity)
		if err != nil {
			_, _ = fmt.Fprintf(fw, "pushrun: %v\n", err)
			_, _ = fmt.Fprint(fw, "CI_STATUS=FAILED\n")
			return
		}
		commit, err := gitx.ResolveRef(repoDir, "refs/pushrun/for/"+req.Branch)
		if err != nil {
			_, _ = fmt.Fprintf(fw, "pushrun: no commit given and refs/pushrun/for/%s does not resolve: %v\n", req.Branch, err)
			_, _ = fmt.Fprint(fw, "CI_STATUS=FAILED\n")
			return
		}
		req.Commit = commit
	}

	_, _ = fmt.Fprintf(fw, "pushrun: running %s for %s/%s at %s\n",
		req.Action, req.Project, req.Instance, shortSHA(req.Commit))
	tw := &trailerWriter{w: fw}
	// Runs take minutes; a pusher disconnect must not abort them (the
	// instance state and run record are persisted regardless).
	_, runErr := s.deps.Manager.Run(context.WithoutCancel(r.Context()), req, tw)
	if runErr != nil {
		_, _ = fmt.Fprintf(tw, "pushrun: run failed: %v\n", runErr)
	}
	if !tw.sawStatus {
		// The engine writes its own CI_STATUS= trailer on every path that
		// reaches it; this covers early request-level failures.
		status := run.StatusSuccess
		if runErr != nil {
			status = run.StatusFailed
		}
		_, _ = fmt.Fprintf(tw, "CI_STATUS=%s\n", status)
	}
}

// isLoopbackPeer reports whether remoteAddr ("host:port") is a loopback IP.
func isLoopbackPeer(remoteAddr string) bool {
	host, _, err := net.SplitHostPort(remoteAddr)
	if err != nil {
		return false
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}

// displayHost extracts the host part of an HTTP Host header value
// ("example.com:8000" -> "example.com") and rejects anything containing
// characters outside a valid host name or IP literal, so it is always safe
// to embed in a URL or environment variable. Returns "" for empty or
// invalid input.
func displayHost(hostport string) string {
	host, _, err := net.SplitHostPort(hostport)
	if err != nil {
		host = hostport // no port (or bracketless IPv6)
	}
	host = strings.TrimPrefix(strings.TrimSuffix(host, "]"), "[")
	for _, r := range host {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9':
		case r == '.' || r == '-' || r == '_' || r == ':' || r == '%':
		default:
			return ""
		}
	}
	return host
}

// shortSHA abbreviates a commit SHA for log lines.
func shortSHA(sha string) string {
	if len(sha) > 12 {
		return sha[:12]
	}
	return sha
}

// flushWriter flushes after every write so run output streams to the hook
// (and through the push sideband to the pusher's terminal) as it happens.
type flushWriter struct {
	w io.Writer
}

func (f *flushWriter) Write(p []byte) (int, error) {
	n, err := f.w.Write(p)
	if fl, ok := f.w.(http.Flusher); ok {
		fl.Flush()
	}
	return n, err
}

// trailerWriter passes writes through while watching for the engine's
// CI_STATUS= trailer (tracked across chunk boundaries).
type trailerWriter struct {
	w         io.Writer
	tail      []byte
	sawStatus bool
}

func (t *trailerWriter) Write(p []byte) (int, error) {
	if !t.sawStatus {
		window := append(t.tail, p...)
		t.sawStatus = bytes.Contains(window, []byte("CI_STATUS="))
		if len(window) > 16 {
			window = window[len(window)-16:]
		}
		t.tail = append([]byte(nil), window...)
	}
	return t.w.Write(p)
}
