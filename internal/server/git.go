package server

import (
	"bufio"
	"bytes"
	"crypto/subtle"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/textproto"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/bunnyzr/pushrun/internal/fsutil"
	"github.com/bunnyzr/pushrun/internal/gitx"
	"github.com/bunnyzr/pushrun/internal/run"
)

// serveGit handles the git smart-HTTP endpoint /git/<host>/<path>.git/... by
// wrapping `git http-backend` as a CGI subprocess. The repo identity
// (host/path) keys the bare repo on disk; auth is HTTP Basic (password is
// the daemon token, the username is recorded as the pusher) or a Bearer
// token. The repo's bare repo is ensured on every request so the hooks
// always carry the current hook secret and port.
func (s *Server) serveGit(w http.ResponseWriter, r *http.Request) {
	identity, pathInfo, ok := parseGitPath(r.PathValue("rest"))
	if !ok {
		s.fail(w, r, http.StatusNotFound, "not_found",
			fmt.Errorf("invalid git path %q (want /git/<host>/<path>.git/...)", r.URL.Path))
		return
	}
	user, ok := s.gitAuth(w, r)
	if !ok {
		return
	}
	// Push metadata headers (see docs/design.md): the client rides action,
	// instance, an optional explicit project, and a self-asserted display
	// user along with the push. They are forwarded into the CGI environment,
	// where the hooks pick them up. X-PushRun-User overrides the
	// transport-level identity (Bearer pushes otherwise record the
	// meaningless user "token").
	if a := r.Header.Get("X-PushRun-Action"); a != "" {
		switch a {
		case run.ActionRun, run.ActionSync, run.ActionBuild, run.ActionTest,
			run.ActionStart, run.ActionStop, run.ActionRestart, run.ActionRerun:
		default:
			s.fail(w, r, http.StatusBadRequest, "invalid",
				fmt.Errorf("unknown X-PushRun-Action %q", a))
			return
		}
	}
	if inst := r.Header.Get("X-PushRun-Instance"); inst != "" && !fsutil.ValidName(inst) {
		s.fail(w, r, http.StatusBadRequest, "invalid",
			fmt.Errorf("invalid X-PushRun-Instance %q", inst))
		return
	}
	if p := r.Header.Get("X-PushRun-Project"); p != "" && !fsutil.ValidName(p) {
		s.fail(w, r, http.StatusBadRequest, "invalid",
			fmt.Errorf("invalid X-PushRun-Project %q", p))
		return
	}
	if u := strings.TrimSpace(r.Header.Get("X-PushRun-User")); u != "" {
		user = u
	}

	secret, err := s.hookSecret()
	if err != nil {
		s.fail(w, r, http.StatusInternalServerError, "internal", err)
		return
	}
	hookBin, err := os.Executable()
	if err != nil {
		s.fail(w, r, http.StatusInternalServerError, "internal",
			fmt.Errorf("locate pushrun binary for hook installation: %w", err))
		return
	}
	port, err := s.daemonPort(r)
	if err != nil {
		s.fail(w, r, http.StatusInternalServerError, "internal", err)
		return
	}
	if _, err := gitx.EnsureBareRepo(s.paths, identity, hookBin, secret, port); err != nil {
		s.fail(w, r, http.StatusInternalServerError, "internal",
			fmt.Errorf("ensure bare repo for %q: %w", identity, err))
		return
	}

	s.runGitHTTPBackend(w, r, pathInfo, user)
}

// parseGitPath splits a /git/ tail like "git.example.com/team/proto.git/
// info/refs" into the repo identity ("git.example.com/team/proto") and the
// CGI PATH_INFO pointing at the on-disk bare repo ("/git.example.com--team--
// proto.git/info/refs"). It returns ok=false for anything that is not a
// valid "<host>/<path>.git" prefix followed by a git endpoint.
func parseGitPath(rest string) (identity, pathInfo string, ok bool) {
	i := strings.Index(rest, "/")
	if i <= 0 {
		return "", "", false
	}
	remainder := rest[i+1:]
	j := strings.Index(remainder, ".git/")
	if j <= 0 {
		return "", "", false
	}
	identity = rest[:i] + "/" + remainder[:j]
	if _, err := gitx.NormalizeRepo(identity); err != nil {
		return "", "", false
	}
	tail := remainder[j+len(".git/"):]
	for _, seg := range strings.Split(tail, "/") {
		if seg == "" || seg == ".." {
			return "", "", false
		}
	}
	dir := strings.ReplaceAll(identity, "/", "--")
	return identity, "/" + dir + ".git/" + tail, true
}

// gitAuth enforces token auth on the git endpoint: HTTP Basic with the
// token as password, or a Bearer token. It returns the pusher's display
// name (the Basic username, self-asserted metadata only).
func (s *Server) gitAuth(w http.ResponseWriter, r *http.Request) (string, bool) {
	if !s.cfg.Auth.Enabled {
		return "anonymous", true
	}
	if user, pass, ok := r.BasicAuth(); ok &&
		subtle.ConstantTimeCompare([]byte(pass), []byte(s.deps.Token)) == 1 {
		if user == "" {
			user = "git"
		}
		return user, true
	}
	if bearer, ok := strings.CutPrefix(r.Header.Get("Authorization"), "Bearer "); ok &&
		subtle.ConstantTimeCompare([]byte(bearer), []byte(s.deps.Token)) == 1 {
		return "token", true
	}
	// The Basic challenge makes git prompt for credentials interactively.
	w.Header().Set("WWW-Authenticate", `Basic realm="pushrun"`)
	s.fail(w, r, http.StatusUnauthorized, "unauthorized",
		errors.New("git endpoint requires the daemon token as HTTP Basic password or Bearer token"))
	return "", false
}

// daemonPort determines the port the hook scripts should call back on. The
// port the pusher used (from the Host header) is authoritative — it works
// for both the configured bind port and ephemeral test servers; the
// configured port is the fallback.
func (s *Server) daemonPort(r *http.Request) (int, error) {
	if _, p, err := net.SplitHostPort(r.Host); err == nil {
		if n, err := strconv.Atoi(p); err == nil && n > 0 {
			return n, nil
		}
	}
	if s.cfg.HTTP.Port > 0 {
		return s.cfg.HTTP.Port, nil
	}
	return 0, errors.New("cannot determine the daemon port for hook callbacks (no port in the request Host header and http.port is 0)")
}

// runGitHTTPBackend execs `git http-backend` as a CGI subprocess and relays
// its response, streaming the body so sideband progress and hook output
// reach the pusher as they happen.
func (s *Server) runGitHTTPBackend(w http.ResponseWriter, r *http.Request, pathInfo, user string) {
	env := []string{
		"GIT_PROJECT_ROOT=" + s.paths.Repos,
		"GIT_HTTP_EXPORT_ALL=1",
		"PATH_INFO=" + pathInfo,
		"QUERY_STRING=" + r.URL.RawQuery,
		"REQUEST_METHOD=" + r.Method,
		"CONTENT_TYPE=" + r.Header.Get("Content-Type"),
		"REMOTE_USER=" + user,
		"REMOTE_ADDR=" + r.RemoteAddr,
	}
	// net/http promotes Content-Length out of the header map; source it
	// from the parsed field instead (chunked push bodies leave it unset,
	// which http-backend handles by reading to EOF).
	if r.ContentLength >= 0 {
		env = append(env, "CONTENT_LENGTH="+strconv.FormatInt(r.ContentLength, 10))
	}
	// Required for protocol v2: http-backend only sees the negotiation
	// version via this CGI variable.
	if gp := r.Header.Get("Git-Protocol"); gp != "" {
		env = append(env, "GIT_PROTOCOL="+gp)
	}
	// Forward the validated pushrun metadata headers into the hook
	// environment (validated in serveGit).
	if a := r.Header.Get("X-PushRun-Action"); a != "" {
		env = append(env, "PUSHRUN_ACTION="+a)
	}
	if inst := r.Header.Get("X-PushRun-Instance"); inst != "" {
		env = append(env, "PUSHRUN_INSTANCE="+inst)
	}
	// The explicit project the pusher asserted, if any; the hooks use it to
	// disambiguate a repo mounted by multiple projects.
	if p := r.Header.Get("X-PushRun-Project"); p != "" {
		env = append(env, "PUSHRUN_PROJECT="+p)
	}
	// The host the pusher connected to (host part of the Host header) lets
	// the run engine build a CI_URL that is reachable from the pusher's
	// machine; the hook forwards it to the daemon.
	if host := displayHost(r.Host); host != "" {
		env = append(env, "PUSHRUN_HOST="+host)
	}

	cmd := exec.Command("git", "http-backend")
	// http-backend spawns receive-pack/upload-pack, which spawn the hooks:
	// run the whole tree in its own process group so a client disconnect can
	// kill all of it. Killing only http-backend would leave the children
	// holding the stderr pipe open, and cmd.Wait would hang forever waiting
	// for the stderr copier.
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	// Duplicate keys are deduplicated keeping the last value, so appending
	// overrides any inherited GIT_* variables.
	cmd.Env = append(os.Environ(), env...)
	cmd.Stdin = r.Body
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		s.fail(w, r, http.StatusInternalServerError, "internal", fmt.Errorf("git http-backend: %w", err))
		return
	}
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	if err := cmd.Start(); err != nil {
		s.fail(w, r, http.StatusInternalServerError, "internal", fmt.Errorf("start git http-backend: %w", err))
		return
	}

	// CGI response: a MIME header block (possibly with a Status header),
	// then the body.
	br := bufio.NewReader(stdout)
	mime, err := textproto.NewReader(br).ReadMIMEHeader()
	if err != nil {
		killBackendTree(cmd)
		_ = cmd.Wait()
		s.fail(w, r, http.StatusBadGateway, "bad_gateway",
			fmt.Errorf("read git http-backend response: %w: %s", err, strings.TrimSpace(stderr.String())))
		return
	}
	status := http.StatusOK
	if st := mime.Get("Status"); st != "" {
		if n, err := strconv.Atoi(strings.Fields(st)[0]); err == nil {
			status = n
		}
		mime.Del("Status")
	}
	for k, vs := range mime {
		for _, v := range vs {
			w.Header().Add(k, v)
		}
	}
	w.WriteHeader(status)

	buf := make([]byte, 32<<10)
	for {
		n, rerr := br.Read(buf)
		if n > 0 {
			if _, werr := w.Write(buf[:n]); werr != nil {
				// Pusher went away mid-stream. Kill the whole http-backend
				// process tree instead of blocking in Wait on its
				// now-undrained pipes: with more than a pipe buffer of
				// output pending the child would block on write and Wait
				// would never return, leaking the goroutine and the process
				// chain. The hook's daemon call outlives us either way.
				killBackendTree(cmd)
				break
			}
			if f, ok := w.(http.Flusher); ok {
				f.Flush()
			}
		}
		if rerr != nil {
			break
		}
	}
	if err := cmd.Wait(); err != nil {
		s.requestLogger(r).Debug("git http-backend exited with error",
			"error", err, "stderr", strings.TrimSpace(stderr.String()))
	}
}

// killBackendTree SIGKILLs http-backend's whole process group, retrying in
// the background until the group is gone (or a 5s cap is hit). A single
// group kill can miss a grandchild that sits in uninterruptible sleep when
// the signal lands (observed on macOS: an upload-pack stuck in state U
// survived kill(-pgid, SIGKILL) and kept the inherited stderr pipe open,
// which then hung cmd.Wait's stderr copier forever); the retry lands once
// the process returns to an interruptible state. The group id equals the
// http-backend pid (Setpgid above). A zombie group leader keeps the group
// non-empty until Wait reaps it, so the loop runs concurrently with Wait.
func killBackendTree(cmd *exec.Cmd) {
	if cmd.Process == nil {
		return
	}
	pgid := cmd.Process.Pid
	go func() {
		for range 100 {
			if err := syscall.Kill(-pgid, syscall.SIGKILL); err != nil {
				return // ESRCH: the process group is gone
			}
			time.Sleep(50 * time.Millisecond)
		}
	}()
}
