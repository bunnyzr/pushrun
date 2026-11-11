// Package logging sets up the daemon's slog instrumentation: stderr plus
// a JSON daemon.log under the data root with size-based rotation.
package logging

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strconv"
	"sync"
)

const (
	// Daemon logs rotate at 64 MiB, keeping 5 files in total (the active
	// daemon.log plus four rotated backups).
	defaultMaxBytes int64 = 64 << 20
	defaultMaxFiles       = 5
)

// Setup builds the root daemon logger. Records go to stderr (text when dev,
// JSON otherwise) and to <logDir>/daemon.log in JSON with size-based
// rotation; logDir is the data root's logs directory (paths.Logs).
// Callers obtain component loggers via
// logger.With("component", "<pkg>").
func Setup(logDir, level string, dev bool) (*slog.Logger, error) {
	lvl, err := parseLevel(level)
	if err != nil {
		return nil, err
	}

	if err := os.MkdirAll(logDir, 0o755); err != nil {
		return nil, fmt.Errorf("create log dir: %w", err)
	}
	fw, err := newRotateWriter(filepath.Join(logDir, "daemon.log"), defaultMaxBytes, defaultMaxFiles)
	if err != nil {
		return nil, fmt.Errorf("open daemon log: %w", err)
	}

	opts := &slog.HandlerOptions{Level: lvl}
	var stderrHandler slog.Handler
	if dev {
		stderrHandler = slog.NewTextHandler(os.Stderr, opts)
	} else {
		stderrHandler = slog.NewJSONHandler(os.Stderr, opts)
	}
	fileHandler := slog.NewJSONHandler(fw, opts)

	return slog.New(newMultiHandler(stderrHandler, fileHandler)), nil
}

func parseLevel(level string) (slog.Level, error) {
	switch level {
	case "debug":
		return slog.LevelDebug, nil
	case "info":
		return slog.LevelInfo, nil
	case "warn":
		return slog.LevelWarn, nil
	case "error":
		return slog.LevelError, nil
	default:
		return 0, fmt.Errorf("invalid log level %q (want debug|info|warn|error)", level)
	}
}

// multiHandler fans records out to several handlers.
type multiHandler struct {
	handlers []slog.Handler
}

func newMultiHandler(handlers ...slog.Handler) *multiHandler {
	return &multiHandler{handlers: handlers}
}

func (h *multiHandler) Enabled(ctx context.Context, level slog.Level) bool {
	for _, sub := range h.handlers {
		if sub.Enabled(ctx, level) {
			return true
		}
	}
	return false
}

func (h *multiHandler) Handle(ctx context.Context, r slog.Record) error {
	var firstErr error
	for _, sub := range h.handlers {
		if !sub.Enabled(ctx, r.Level) {
			continue
		}
		if err := sub.Handle(ctx, r.Clone()); err != nil && firstErr == nil {
			firstErr = err
		}
	}
	return firstErr
}

func (h *multiHandler) WithAttrs(attrs []slog.Attr) slog.Handler {
	subs := make([]slog.Handler, len(h.handlers))
	for i, sub := range h.handlers {
		subs[i] = sub.WithAttrs(attrs)
	}
	return &multiHandler{handlers: subs}
}

func (h *multiHandler) WithGroup(name string) slog.Handler {
	subs := make([]slog.Handler, len(h.handlers))
	for i, sub := range h.handlers {
		subs[i] = sub.WithGroup(name)
	}
	return &multiHandler{handlers: subs}
}

// rotateWriter is an io.Writer that appends to a file and rolls it over to
// <path>.1, <path>.2, ... once it exceeds maxBytes, keeping at most
// maxFiles files including the active one.
type rotateWriter struct {
	path     string
	maxBytes int64
	maxFiles int

	mu   sync.Mutex
	f    *os.File
	size int64
}

func newRotateWriter(path string, maxBytes int64, maxFiles int) (*rotateWriter, error) {
	if maxBytes <= 0 || maxFiles < 1 {
		return nil, fmt.Errorf("rotateWriter: invalid limits maxBytes=%d maxFiles=%d", maxBytes, maxFiles)
	}
	f, err := os.OpenFile(path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		return nil, err
	}
	info, err := f.Stat()
	if err != nil {
		_ = f.Close()
		return nil, err
	}
	return &rotateWriter{path: path, maxBytes: maxBytes, maxFiles: maxFiles, f: f, size: info.Size()}, nil
}

func (w *rotateWriter) Write(p []byte) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()

	if w.size > 0 && w.size+int64(len(p)) > w.maxBytes {
		if err := w.rotate(); err != nil {
			return 0, err
		}
	}
	n, err := w.f.Write(p)
	w.size += int64(n)
	return n, err
}

// rotate shifts daemon.log.N-1 -> daemon.log.N and starts a fresh active
// file. The caller must hold w.mu.
func (w *rotateWriter) rotate() error {
	if err := w.f.Close(); err != nil {
		return err
	}

	// Drop the oldest backup, then shift the rest up by one.
	_ = os.Remove(w.path + "." + strconv.Itoa(w.maxFiles-1))
	for i := w.maxFiles - 1; i >= 2; i-- {
		_ = os.Rename(w.path+"."+strconv.Itoa(i-1), w.path+"."+strconv.Itoa(i))
	}
	_ = os.Rename(w.path, w.path+".1")

	f, err := os.OpenFile(w.path, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o644)
	if err != nil {
		return err
	}
	w.f = f
	w.size = 0
	return nil
}
