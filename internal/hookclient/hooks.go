// Package hookclient implements the pushrun binary side of the git hooks:
// pre-receive enforces the push policy (ref namespace + repo-to-project
// matching) where git can still reject the push, and post-receive relays the
// pushed refs to the daemon's loopback hook endpoint and streams the run
// output back to stdout, where git receive-pack picks it up as sideband
// "remote:" lines for the pusher.
package hookclient

import (
	"bufio"
	"bytes"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"

	"github.com/bunnyzr/pushrun/internal/config"
	"github.com/bunnyzr/pushrun/internal/project"
)

// Env carries the hook configuration, normally sourced from the environment
// baked into the hook script by gitx.EnsureBareRepo.
type Env struct {
	Port     string // daemon HTTP port (PUSHRUN_PORT)
	Secret   string // hook shared secret (PUSHRUN_HOOK_SECRET)
	Root     string // daemon data root (PUSHRUN_ROOT), used by pre-receive matching
	Repo     string // repo identity host/path (PUSHRUN_REPO)
	Project  string // explicit project asserted by the pusher (PUSHRUN_PROJECT, optional)
	User     string // self-asserted pusher identity (REMOTE_USER)
	Action   string // run action, default "run" (PUSHRUN_ACTION)
	Instance string // instance name, default "default" (PUSHRUN_INSTANCE)
	Host     string // host the pusher connected to (PUSHRUN_HOST, forwarded by the git endpoint)
}

const zeroSHA = "0000000000000000000000000000000000000000"

// branchRefPrefix is the only ref namespace the daemon accepts pushes into.
const branchRefPrefix = "refs/pushrun/for/"

// staleHook hints at the fix when a hook script predates the current
// installation contract.
const staleHook = "(stale hook script? push again or hit any /git/ URL to refresh hooks)"

// PreReceive enforces the push policy on the refs listed on stdin
// ("<old> <new> <ref>" lines): every updated ref must live under
// refs/pushrun/for/, and the repo identity baked into the hook environment
// must resolve to a project (directly, or via the pusher's explicit
// PUSHRUN_PROJECT). Rejections are explained on stderr, which git relays to
// the pusher as "remote:" lines; a non-nil return rejects the whole push.
func PreReceive(stdin io.Reader, stderr io.Writer, env Env) error {
	if env.Root == "" {
		return errors.New("PUSHRUN_ROOT is not set " + staleHook)
	}
	if env.Repo == "" {
		return errors.New("PUSHRUN_REPO is not set " + staleHook)
	}

	sc := bufio.NewScanner(stdin)
	sc.Buffer(make([]byte, 0, 64<<10), 1<<20)
	for sc.Scan() {
		fields := strings.Fields(sc.Text())
		if len(fields) != 3 {
			return fmt.Errorf("malformed pre-receive line %q (want \"<old> <new> <ref>\")", sc.Text())
		}
		if ref := fields[2]; !strings.HasPrefix(ref, branchRefPrefix) {
			_, _ = fmt.Fprintf(stderr, "pushrun: refusing to update %s — push to refs/pushrun/for/<branch> instead\n", ref)
			return fmt.Errorf("ref %s outside the %s* namespace", ref, branchRefPrefix)
		}
	}
	if err := sc.Err(); err != nil {
		return fmt.Errorf("read pre-receive stdin: %w", err)
	}

	if _, err := project.MatchRepo(config.NewPaths(env.Root), env.Repo, env.Project); err != nil {
		_, _ = fmt.Fprintf(stderr, "pushrun: %s\n", err)
		return err
	}
	return nil
}

// PostReceive reads post-receive stdin lines ("<old> <new> <ref>"), POSTs
// each updated refs/pushrun/for/<branch> to the daemon, and relays the
// daemon's streamed response to stdout. It returns a non-nil error unless
// every triggered run reported CI_STATUS=SUCCESS; main maps that to the
// hook's exit code.
func PostReceive(stdin io.Reader, stdout io.Writer, env Env) error {
	if env.Port == "" {
		return errors.New("PUSHRUN_PORT is not set " + staleHook)
	}
	if env.Secret == "" {
		return errors.New("PUSHRUN_HOOK_SECRET is not set " + staleHook)
	}
	if env.Repo == "" {
		return errors.New("PUSHRUN_REPO is not set " + staleHook)
	}
	if env.Action == "" {
		env.Action = "run"
	}
	if env.Instance == "" {
		env.Instance = "default"
	}

	type push struct{ branch, commit string }
	var pushes []push
	sc := bufio.NewScanner(stdin)
	sc.Buffer(make([]byte, 0, 64<<10), 1<<20)
	for sc.Scan() {
		fields := strings.Fields(sc.Text())
		if len(fields) != 3 {
			return fmt.Errorf("malformed post-receive line %q (want \"<old> <new> <ref>\")", sc.Text())
		}
		newSHA, ref := fields[1], fields[2]
		branch, ok := strings.CutPrefix(ref, branchRefPrefix)
		if !ok {
			continue
		}
		if newSHA == zeroSHA {
			_, _ = fmt.Fprintf(stdout, "pushrun: %s deleted; no run triggered\n", ref)
			continue
		}
		pushes = append(pushes, push{branch, newSHA})
	}
	if err := sc.Err(); err != nil {
		return fmt.Errorf("read post-receive stdin: %w", err)
	}
	if len(pushes) == 0 {
		_, _ = fmt.Fprintf(stdout, "pushrun: no %s* refs updated; nothing to run\n", branchRefPrefix)
		return nil
	}

	var failed []string
	for _, p := range pushes {
		status, err := env.trigger(stdout, p.branch, p.commit)
		if err != nil {
			return err
		}
		if status != "SUCCESS" {
			failed = append(failed, p.branch+" (CI_STATUS="+status+")")
		}
	}
	if len(failed) > 0 {
		return fmt.Errorf("pushrun: run did not succeed for %s", strings.Join(failed, ", "))
	}
	return nil
}

// trigger POSTs one pushed branch to the daemon and streams the response
// body to stdout as it arrives. It returns the trailing CI_STATUS value.
func (env Env) trigger(stdout io.Writer, branch, commit string) (string, error) {
	url := fmt.Sprintf("http://127.0.0.1:%s/internal/v1/hooks/post-receive", env.Port)
	req, err := http.NewRequest(http.MethodPost, url, nil)
	if err != nil {
		return "", err
	}
	req.Header.Set("X-PushRun-Hook-Secret", env.Secret)
	req.Header.Set("X-PushRun-Repo", env.Repo)
	if env.Project != "" {
		req.Header.Set("X-PushRun-Project", env.Project)
	}
	req.Header.Set("X-PushRun-Branch", branch)
	req.Header.Set("X-PushRun-Commit", commit)
	req.Header.Set("X-PushRun-User", env.User)
	req.Header.Set("X-PushRun-Action", env.Action)
	req.Header.Set("X-PushRun-Instance", env.Instance)
	if env.Host != "" {
		req.Header.Set("X-PushRun-Host", env.Host)
	}

	// No client timeout: runs legitimately take minutes and the daemon
	// streams output continuously.
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return "", fmt.Errorf("reach the pushrun daemon at %s: %w (is 'pushrun serve' running?)", url, err)
	}
	defer func() { _ = resp.Body.Close() }()

	tw := &tailWriter{w: stdout}
	if _, err := io.Copy(tw, resp.Body); err != nil {
		return "", fmt.Errorf("stream daemon output: %w", err)
	}
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("daemon rejected the hook call: %s", resp.Status)
	}
	status := tw.lastStatus()
	if status == "" {
		return "", errors.New("daemon output ended without a CI_STATUS= trailer")
	}
	return status, nil
}

// tailWriter passes writes through while keeping the trailing 64 KiB, so
// the final CI_STATUS= trailer can be parsed out of the stream.
type tailWriter struct {
	w    io.Writer
	tail []byte
}

func (t *tailWriter) Write(p []byte) (int, error) {
	t.tail = append(t.tail, p...)
	if len(t.tail) > 64<<10 {
		t.tail = append([]byte(nil), t.tail[len(t.tail)-(64<<10):]...)
	}
	return t.w.Write(p)
}

// lastStatus returns the value of the last CI_STATUS= line in the tail.
func (t *tailWriter) lastStatus() string {
	lines := bytes.Split(t.tail, []byte("\n"))
	for i := len(lines) - 1; i >= 0; i-- {
		if v, ok := bytes.CutPrefix(lines[i], []byte("CI_STATUS=")); ok {
			return string(bytes.TrimSpace(v))
		}
	}
	return ""
}
