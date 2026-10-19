// Command pushrun is the pushrun daemon and CLI entry point.
package main

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"net/http"
	"os"
	"os/exec"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/bunnyzr/pushrun/internal/config"
	"github.com/bunnyzr/pushrun/internal/hookclient"
	"github.com/bunnyzr/pushrun/internal/instance"
	"github.com/bunnyzr/pushrun/internal/logging"
	"github.com/bunnyzr/pushrun/internal/provider"
	runengine "github.com/bunnyzr/pushrun/internal/run"
	"github.com/bunnyzr/pushrun/internal/server"
	"github.com/bunnyzr/pushrun/web"
)

// version is set via -ldflags "-X main.version=<ver>".
var version = "dev"

func main() {
	if err := run(os.Args[1:]); err != nil {
		fmt.Fprintln(os.Stderr, "pushrun:", err)
		os.Exit(1)
	}
}

const usage = `pushrun — receive git pushes, build and run projects on this machine

Usage:
  pushrun serve [--root DIR] [--dev]   start the daemon (--dev: human-readable
                                       stderr logs; also PUSHRUN_DEV=1)
  pushrun hook pre-receive        git pre-receive hook entry point
  pushrun hook post-receive       git post-receive hook entry point
  pushrun token show [--root DIR] print the daemon auth token
  pushrun version                 print the version

The data root defaults to $PUSHRUN_ROOT, then $XDG_DATA_HOME/pushrun,
then ~/.local/share/pushrun.
`

func run(args []string) error {
	if len(args) == 0 {
		fmt.Fprint(os.Stderr, usage)
		return errors.New("missing subcommand")
	}

	root, args := extractRootFlag(args)

	switch args[0] {
	case "serve":
		dev := os.Getenv("PUSHRUN_DEV") != ""
		rest := args[1:]
		for i := 0; i < len(rest); i++ {
			if rest[i] == "--dev" {
				dev = true
				rest = append(rest[:i], rest[i+1:]...)
				i--
			}
		}
		if len(rest) != 0 {
			return fmt.Errorf("unknown serve flags: %s", strings.Join(rest, " "))
		}
		return serve(root, dev)
	case "hook":
		if len(args) < 2 {
			return errors.New("usage: pushrun hook pre-receive|post-receive")
		}
		env := hookclient.Env{
			Port:     os.Getenv("PUSHRUN_PORT"),
			Secret:   os.Getenv("PUSHRUN_HOOK_SECRET"),
			Root:     root,
			Repo:     os.Getenv("PUSHRUN_REPO"),
			Project:  os.Getenv("PUSHRUN_PROJECT"),
			User:     os.Getenv("REMOTE_USER"),
			Action:   os.Getenv("PUSHRUN_ACTION"),
			Instance: os.Getenv("PUSHRUN_INSTANCE"),
			Host:     os.Getenv("PUSHRUN_HOST"),
		}
		switch args[1] {
		case "pre-receive":
			return hookclient.PreReceive(os.Stdin, os.Stderr, env)
		case "post-receive":
			return hookclient.PostReceive(os.Stdin, os.Stdout, env)
		default:
			return errors.New("usage: pushrun hook pre-receive|post-receive")
		}
	case "token":
		if len(args) < 2 || args[1] != "show" {
			return errors.New("usage: pushrun token show")
		}
		return tokenShow(root)
	case "version":
		fmt.Println(version)
		return nil
	case "help", "-h", "--help":
		fmt.Print(usage)
		return nil
	default:
		fmt.Fprint(os.Stderr, usage)
		return fmt.Errorf("unknown subcommand %q", args[0])
	}
}

// extractRootFlag pulls a global --root DIR (or --root=DIR) out of args.
func extractRootFlag(args []string) (string, []string) {
	root := resolveRoot()
	rest := args[:0]
	for i := 0; i < len(args); i++ {
		switch {
		case args[i] == "--root" && i+1 < len(args):
			root = args[i+1]
			i++
		case strings.HasPrefix(args[i], "--root="):
			root = strings.TrimPrefix(args[i], "--root=")
		default:
			rest = append(rest, args[i])
		}
	}
	return root, rest
}

func resolveRoot() string {
	if root := os.Getenv("PUSHRUN_ROOT"); root != "" {
		return root
	}
	return config.DefaultRoot()
}

// checkDeps verifies the runtime dependencies (git and bash) are in PATH.
func checkDeps() error {
	var missing []string
	for _, dep := range []string{"git", "bash"} {
		if _, err := exec.LookPath(dep); err != nil {
			missing = append(missing, dep)
		}
	}
	if len(missing) > 0 {
		return fmt.Errorf("missing required dependencies in PATH: %s (pushrun needs git and bash on the host)", strings.Join(missing, ", "))
	}
	return nil
}

func tokenShow(root string) error {
	token, generated, err := config.EnsureToken(root)
	if err != nil {
		return err
	}
	fmt.Println(token)
	if generated {
		fmt.Fprintf(os.Stderr, "generated a new token, stored in %s/token (0600)\n", root)
	}
	return nil
}

// serve boots the daemon: config, logging, token, dependency check, crash
// recovery, then the HTTP server on the configured bind address. dev
// switches stderr logging to human-readable text (the --dev flag or
// PUSHRUN_DEV).
func serve(root string, dev bool) error {
	if err := checkDeps(); err != nil {
		return err
	}
	if err := os.MkdirAll(root, 0o755); err != nil {
		return fmt.Errorf("create data root: %w", err)
	}
	cfg, err := config.Load(root)
	if err != nil {
		return err
	}
	paths := config.NewPaths(root)
	logger, err := logging.Setup(paths.Logs, cfg.Log.Level, dev)
	if err != nil {
		return err
	}
	log := logger.With("component", "main")

	// The token file always exists after first boot (see docs/design.md); a
	// token set in config.yaml overrides it. The generated token is printed to the
	// console exactly once — it is never logged.
	token, generated, err := config.EnsureToken(root)
	if err != nil {
		return err
	}
	if cfg.Auth.Token != "" {
		token = cfg.Auth.Token
	} else if generated {
		fmt.Fprintf(os.Stderr, "pushrun: generated a new auth token (stored in %s/token, 0600):\n%s\n", root, token)
	}

	if err := instance.Recover(paths, logger.With("component", "instance")); err != nil {
		log.Warn("crash recovery incomplete", "error", err)
	}
	exec := provider.NewExecutor(paths, cfg)
	pool := instance.NewPortPool(paths, cfg.PortPool.From, cfg.PortPool.To)
	mgr := runengine.NewManager(paths, cfg, exec, pool, logger)
	webFS, err := fs.Sub(web.Dist, "dist")
	if err != nil {
		return fmt.Errorf("web assets: %w", err)
	}
	handler := server.New(paths, cfg, server.Deps{
		Manager:  mgr,
		Executor: exec,
		Pool:     pool,
		Token:    token,
		Version:  version,
		Logger:   logger,
		Web:      webFS,
	})

	addr := fmt.Sprintf("%s:%d", cfg.HTTP.Bind, cfg.HTTP.Port)
	srv := &http.Server{Addr: addr, Handler: handler, ReadHeaderTimeout: 10 * time.Second}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	errCh := make(chan error, 1)
	go func() { errCh <- srv.ListenAndServe() }()
	log.Info("pushrun listening", "addr", addr, "root", root)
	select {
	case <-ctx.Done():
		log.Info("shutting down")
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		return srv.Shutdown(shutdownCtx)
	case err := <-errCh:
		if errors.Is(err, http.ErrServerClosed) {
			return nil
		}
		return err
	}
}
