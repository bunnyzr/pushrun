package run

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/bunnyzr/pushrun/internal/instance"
	"github.com/bunnyzr/pushrun/internal/project"
)

// defaultStepTimeout is the per-step timeout in seconds when the step does
// not declare one.
const defaultStepTimeout = 300

// defaultHealthInterval / defaultHealthRetries are the health check
// defaults (seconds / attempts) when the step does not declare them.
const (
	defaultHealthInterval = 1
	defaultHealthRetries  = 30
)

// backgroundStep returns the pipeline's background step, if any.
func backgroundStep(proj *project.Project) *project.Step {
	for i := range proj.Pipeline {
		if proj.Pipeline[i].Background {
			return &proj.Pipeline[i]
		}
	}
	return nil
}

// stepEnv builds the CI_* environment injected into pipeline steps. extra
// carries the workspace/git-node variables (see gitNodeEnv).
func stepEnv(req Request, runID string, port int, extra []string) []string {
	env := []string{
		"CI_PROJECT=" + req.Project,
		"CI_INSTANCE=" + req.Instance,
		"CI_USER=" + req.User,
		"CI_BRANCH=" + req.Branch,
		"CI_COMMIT=" + req.Commit,
		"CI_RUN_ID=" + runID,
	}
	if port > 0 {
		env = append(env, "CI_PORT="+strconv.Itoa(port))
	}
	return append(env, extra...)
}

// runSteps executes the non-background pipeline steps in order, streaming
// output to log with framing headers. The first failing step stops the
// chain and is reported.
func (m *Manager) runSteps(ctx context.Context, proj *project.Project, req Request, instDir string, port int, rec *Record, envExtra []string, log io.Writer) error {
	env := append(os.Environ(), stepEnv(req, rec.ID, port, envExtra)...)
	for _, s := range proj.Pipeline {
		if s.Background {
			continue
		}
		sr := StepResult{Name: s.Name, StartedAt: time.Now().UTC()}
		_, _ = fmt.Fprintf(log, "# ==> step: %s (%s)\n", s.Name, time.Now().Format(time.RFC3339))

		timeout := s.Timeout
		if timeout <= 0 {
			timeout = defaultStepTimeout
		}
		sctx, cancel := context.WithTimeout(ctx, time.Duration(timeout)*time.Second)
		cmd := exec.CommandContext(sctx, "/bin/bash", "-c", s.Run)
		// Run the step in its own process group so cancellation kills the
		// whole group — otherwise step-spawned children survive a timeout,
		// and by holding the output pipes open they would also block Wait.
		cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
		cmd.Cancel = func() error {
			err := syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
			if errors.Is(err, syscall.ESRCH) {
				return os.ErrProcessDone
			}
			return err
		}
		cmd.WaitDelay = 2 * time.Second
		cmd.Dir = instDir
		cmd.Env = env
		cmd.Stdout = log
		cmd.Stderr = log
		runErr := cmd.Run()
		cancel()

		sr.FinishedAt = time.Now().UTC()
		if runErr != nil {
			sr.Status = StatusFailed
			sr.ExitCode = exitCode(cmd)
			rec.Steps = append(rec.Steps, sr)
			if sctx.Err() == context.DeadlineExceeded {
				return fmt.Errorf("step %q timed out after %ds", s.Name, timeout)
			}
			return fmt.Errorf("step %q failed: %w", s.Name, runErr)
		}
		sr.Status = StatusSuccess
		rec.Steps = append(rec.Steps, sr)
	}
	return nil
}

// exitCode extracts the exit code of a finished command.
func exitCode(cmd *exec.Cmd) int {
	if cmd.ProcessState != nil {
		return cmd.ProcessState.ExitCode()
	}
	return -1
}

// startBackground stops the previously supervised process, starts the
// pipeline's background step as a new supervised process group, and runs
// its health check. It is a no-op when the pipeline has no background step.
func (m *Manager) startBackground(ctx context.Context, proj *project.Project, req Request, instDir string, port int, rec *Record, st *instance.State, envExtra []string, log io.Writer) error {
	bg := backgroundStep(proj)
	if bg == nil {
		return nil
	}
	// pushrun only manages processes it started and recorded: stop the
	// previous one before starting its replacement.
	if prev, err := instance.LoadState(m.paths, req.Project, req.Instance); err == nil &&
		prev.PGID > 0 && instance.Alive(prev.PGID) {
		_, _ = fmt.Fprintf(log, "# ==> stopping previous process (pgid %d)\n", prev.PGID)
		if err := instance.KillGroup(prev.PGID); err != nil {
			return fmt.Errorf("stop previous process: %w", err)
		}
		m.log.Info("instance stopped", "project", req.Project, "instance", req.Instance, "pgid", prev.PGID)
	}
	started := time.Now().UTC()
	if err := m.superviseBackground(ctx, bg, req, instDir, port, rec.ID, st, envExtra, log); err != nil {
		rec.Steps = append(rec.Steps, StepResult{
			Name: bg.Name, Status: StatusFailed,
			StartedAt: started, FinishedAt: time.Now().UTC(),
		})
		return err
	}
	rec.Steps = append(rec.Steps, StepResult{
		Name: bg.Name, Status: StatusSuccess,
		StartedAt: started, FinishedAt: time.Now().UTC(),
	})
	return nil
}

// superviseBackground launches bg as a supervised process group with its
// output appended to the instance's business log, runs the health check,
// and fills st with the live process details. On health failure the
// process is killed and the instance is left without a supervised process.
func (m *Manager) superviseBackground(ctx context.Context, bg *project.Step, req Request, instDir string, port int, runID string, st *instance.State, envExtra []string, log io.Writer) error {
	env := append(os.Environ(), stepEnv(req, runID, port, envExtra)...)

	// The supervised process's output is a business log: it belongs in the
	// instance's own logs/ dir, the root the logs API serves
	// (<root>/instances/<project>/<instance>/logs), not the daemon's
	// top-level logs/ dir.
	logDir := filepath.Join(m.paths.Instances, req.Project, req.Instance, "logs")
	if err := os.MkdirAll(logDir, 0o755); err != nil {
		return fmt.Errorf("create business log dir: %w", err)
	}
	blog, err := os.OpenFile(filepath.Join(logDir, "service.log"), os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		return fmt.Errorf("open business log: %w", err)
	}

	cmd := exec.Command("/bin/bash", "-c", bg.Run)
	cmd.Dir = instDir
	cmd.Env = env
	cmd.Stdout = blog
	cmd.Stderr = blog
	pid, pgid, err := instance.Supervise(cmd)
	// The child holds its own dup of the file; our copy is no longer needed.
	_ = blog.Close()
	if err != nil {
		return fmt.Errorf("start background step %q: %w", bg.Name, err)
	}
	_, _ = fmt.Fprintf(log, "# ==> step: %s (%s) background pid %d\n", bg.Name, time.Now().Format(time.RFC3339), pid)

	st.PID, st.PGID = pid, pgid
	if bg.Health != nil {
		if err := checkHealth(ctx, bg.Health, env, instDir, log); err != nil {
			_ = instance.KillGroup(pgid)
			st.PID, st.PGID = 0, 0
			return fmt.Errorf("health check failed: %w", err)
		}
	}
	st.Status = instance.StatusRunning
	// The display host comes from the push connection's Host header so the
	// URL is reachable for a pusher on another machine; API-triggered runs
	// fall back to loopback.
	host := req.DisplayHost
	if host == "" {
		host = "127.0.0.1"
	}
	st.URL = "http://" + net.JoinHostPort(host, strconv.Itoa(port))
	m.log.Info("instance started",
		"project", req.Project, "instance", req.Instance, "pid", pid, "pgid", pgid, "port", port, "url", st.URL)
	return nil
}

// checkHealth probes the target until it passes or the retries are
// exhausted. CI_* variables in the target are expanded for tcp/http checks.
func checkHealth(ctx context.Context, h *project.Health, env []string, dir string, log io.Writer) error {
	interval := h.Interval
	if interval <= 0 {
		interval = defaultHealthInterval
	}
	retries := h.Retries
	if retries <= 0 {
		retries = defaultHealthRetries
	}
	_, _ = fmt.Fprintf(log, "# ==> health: %s %s (%s)\n", h.Type, h.Target, time.Now().Format(time.RFC3339))
	var lastErr error
	for i := range retries {
		lastErr = probe(ctx, h.Type, h.Target, env, dir)
		if lastErr == nil {
			_, _ = fmt.Fprintf(log, "health check passed (%s %s)\n", h.Type, h.Target)
			return nil
		}
		if i+1 < retries {
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-time.After(time.Duration(interval) * time.Second):
			}
		}
	}
	return fmt.Errorf("%s %s not healthy after %d tries: %w", h.Type, h.Target, retries, lastErr)
}

// probe runs one health check attempt.
func probe(ctx context.Context, typ, target string, env []string, dir string) error {
	switch typ {
	case "tcp":
		d := net.Dialer{Timeout: 2 * time.Second}
		conn, err := d.DialContext(ctx, "tcp", expandEnv(target, env))
		if err != nil {
			return err
		}
		_ = conn.Close()
		return nil
	case "http":
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, expandEnv(target, env), nil)
		if err != nil {
			return err
		}
		client := &http.Client{Timeout: 5 * time.Second}
		resp, err := client.Do(req)
		if err != nil {
			return err
		}
		defer func() { _ = resp.Body.Close() }()
		_, _ = io.Copy(io.Discard, resp.Body)
		if resp.StatusCode >= 400 {
			return fmt.Errorf("status %d", resp.StatusCode)
		}
		return nil
	case "command":
		cmd := exec.CommandContext(ctx, "/bin/bash", "-c", target)
		cmd.Dir = dir
		cmd.Env = env
		return cmd.Run()
	default:
		return fmt.Errorf("unknown health check type %q (want tcp|http|command)", typ)
	}
}

// expandEnv expands $VAR references in s from env entries of the form
// KEY=VALUE.
func expandEnv(s string, env []string) string {
	return os.Expand(s, func(k string) string {
		prefix := k + "="
		for _, e := range env {
			if v, ok := strings.CutPrefix(e, prefix); ok {
				return v
			}
		}
		return ""
	})
}
