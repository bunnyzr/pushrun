package server

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/bunnyzr/pushrun/internal/fsutil"
	"github.com/bunnyzr/pushrun/internal/instance"
	"github.com/bunnyzr/pushrun/internal/run"
)

// instanceLogRoot is the business-log root of one instance:
// <root>/instances/<project>/<instance>/logs (there is no per-project
// log_root override).
func (s *Server) instanceLogRoot(proj, inst string) (string, error) {
	if !fsutil.ValidName(proj) || !fsutil.ValidName(inst) {
		return "", fmt.Errorf("invalid instance id %q/%q", proj, inst)
	}
	return filepath.Join(s.paths.Instances, proj, inst, "logs"), nil
}

// runOutput streams a run's build.log over SSE: existing content replays
// first, appended content follows, and a terminal "status" event closes the
// stream once the run record shows a finished status.
func (s *Server) runOutput(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	proj, inst, err := s.findRunDir(id)
	if err != nil {
		s.fail(w, r, http.StatusNotFound, "not_found", err)
		return
	}
	sw, ok := newSSEWriter(w)
	if !ok {
		s.fail(w, r, http.StatusInternalServerError, "internal",
			fmt.Errorf("response writer cannot stream"))
		return
	}
	logPath := filepath.Join(s.paths.Runs, proj, inst, id, "build.log")
	streamLog(r.Context(), sw, logPath, 0, func() (string, bool) {
		// result.json is written once, at the very end of the run; until it
		// appears with a status, the run is still in flight.
		rec, err := run.GetRun(s.paths, proj, inst, id)
		if err != nil || rec.Status == "" {
			return "", false
		}
		payload, _ := json.Marshal(map[string]string{"status": rec.Status})
		return string(payload), true
	})
}

// findRunDir locates a run id across all instances by directory. Unlike
// findRun it does not require result.json: an in-flight run has a directory
// (and its build.log) but no record yet.
func (s *Server) findRunDir(id string) (proj, inst string, err error) {
	if !fsutil.ValidName(id) {
		return "", "", fmt.Errorf("run %q not found", id)
	}
	projs, err := os.ReadDir(s.paths.Runs)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return "", "", fmt.Errorf("run %q not found", id)
		}
		return "", "", fmt.Errorf("list runs: %w", err)
	}
	for _, pd := range projs {
		if !pd.IsDir() {
			continue
		}
		insts, err := os.ReadDir(filepath.Join(s.paths.Runs, pd.Name()))
		if err != nil {
			continue
		}
		for _, idir := range insts {
			if !idir.IsDir() {
				continue
			}
			st, err := os.Stat(filepath.Join(s.paths.Runs, pd.Name(), idir.Name(), id))
			if err == nil && st.IsDir() {
				return pd.Name(), idir.Name(), nil
			}
		}
	}
	return "", "", fmt.Errorf("run %q not found", id)
}

// logsTree lists the files under an instance's business-log root. Only
// relative slash paths cross the API — never absolute server paths.
func (s *Server) logsTree(w http.ResponseWriter, r *http.Request) {
	proj, inst := r.PathValue("project"), r.PathValue("instance")
	root, err := s.instanceLogRoot(proj, inst)
	if err != nil {
		s.fail(w, r, http.StatusBadRequest, "invalid", err)
		return
	}
	files := []string{}
	err = filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if !d.Type().IsRegular() {
			return nil
		}
		rel, err := filepath.Rel(root, p)
		if err != nil {
			return err
		}
		files = append(files, filepath.ToSlash(rel))
		return nil
	})
	if err != nil && !errors.Is(err, fs.ErrNotExist) {
		s.fail(w, r, http.StatusInternalServerError, "internal",
			fmt.Errorf("list instance logs %s/%s: %w", proj, inst, err))
		return
	}
	sort.Strings(files)
	writeJSON(w, http.StatusOK, map[string]any{"files": files})
}

// logsFile serves one business-log file. Without follow it returns JSON
// {"path", "content"}; tail_lines=N keeps only the last N lines. follow=1
// switches to SSE: current content (or its tail) replays first, then
// appended lines stream until the client disconnects.
func (s *Server) logsFile(w http.ResponseWriter, r *http.Request) {
	proj, inst := r.PathValue("project"), r.PathValue("instance")
	root, err := s.instanceLogRoot(proj, inst)
	if err != nil {
		s.fail(w, r, http.StatusBadRequest, "invalid", err)
		return
	}
	rel := r.URL.Query().Get("path")
	fp, err := instance.SafeJoin(root, rel)
	if err != nil {
		s.fail(w, r, http.StatusBadRequest, "invalid", err)
		return
	}
	f, err := os.Open(fp)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			s.fail(w, r, http.StatusNotFound, "not_found",
				fmt.Errorf("log file %q not found", rel))
			return
		}
		s.fail(w, r, http.StatusInternalServerError, "internal", err)
		return
	}
	defer func() { _ = f.Close() }()
	tail, err := tailLinesParam(r)
	if err != nil {
		s.fail(w, r, http.StatusBadRequest, "invalid", err)
		return
	}

	if r.URL.Query().Get("follow") == "1" {
		sw, ok := newSSEWriter(w)
		if !ok {
			s.fail(w, r, http.StatusInternalServerError, "internal",
				fmt.Errorf("response writer cannot stream"))
			return
		}
		offset := int64(0)
		if tail > 0 {
			data, err := io.ReadAll(f)
			if err != nil {
				s.fail(w, r, http.StatusInternalServerError, "internal", err)
				return
			}
			if lines := tailLineStrings(data, tail); len(lines) > 0 {
				sw.write("log", lines...)
			}
			offset, _ = f.Seek(0, io.SeekEnd)
		}
		streamLog(r.Context(), sw, fp, offset, nil)
		return
	}

	data, err := io.ReadAll(f)
	if err != nil {
		s.fail(w, r, http.StatusInternalServerError, "internal", err)
		return
	}
	if tail > 0 {
		data = tailBytes(data, tail)
	}
	writeJSON(w, http.StatusOK, map[string]any{"path": filepath.ToSlash(rel), "content": string(data)})
}

// tailLinesParam parses ?tail_lines=N; 0 means the parameter is absent.
func tailLinesParam(r *http.Request) (int, error) {
	raw := r.URL.Query().Get("tail_lines")
	if raw == "" {
		return 0, nil
	}
	n, err := strconv.Atoi(raw)
	if err != nil || n <= 0 {
		return 0, fmt.Errorf("tail_lines must be a positive integer, got %q", raw)
	}
	return n, nil
}

// tailLineStrings returns the last n lines of data, without line terminators.
func tailLineStrings(data []byte, n int) []string {
	body := bytes.TrimSuffix(data, []byte("\n"))
	if len(body) == 0 {
		return nil
	}
	lines := strings.Split(string(body), "\n")
	if len(lines) > n {
		lines = lines[len(lines)-n:]
	}
	return lines
}

// tailBytes returns the last n lines of data, preserving a trailing newline.
func tailBytes(data []byte, n int) []byte {
	lines := tailLineStrings(data, n)
	if lines == nil {
		return data
	}
	out := []byte(strings.Join(lines, "\n"))
	if bytes.HasSuffix(data, []byte("\n")) {
		out = append(out, '\n')
	}
	return out
}

// streamLog replays the file at path starting from offset, then follows
// appended bytes, emitting SSE "log" events (one data: line per log line)
// until the request context ends or terminal reports completion — at which
// point a final "status" event carries its payload and the stream ends. A
// nil terminal follows forever. The file is opened lazily so a stream can
// start before the log exists. New content is polled, not watched.
func streamLog(ctx context.Context, sw *sseWriter, path string, offset int64, terminal func() (string, bool)) {
	var f *os.File
	defer func() {
		if f != nil {
			_ = f.Close()
		}
	}()
	var pending []byte
	drain := func() {
		if f == nil {
			var err error
			if f, err = os.Open(path); err != nil {
				return
			}
			if _, err = f.Seek(offset, io.SeekStart); err != nil {
				return
			}
		}
		buf := make([]byte, 32<<10)
		for {
			n, err := f.Read(buf)
			if n > 0 {
				pending = append(pending, buf[:n]...)
				var lines []string
				lines, pending = splitCompleteLines(pending)
				if len(lines) > 0 {
					sw.write("log", lines...)
				}
			}
			if err != nil {
				return
			}
		}
	}
	tick := time.NewTicker(followPollInterval)
	defer tick.Stop()
	heart := time.NewTicker(SSEHeartbeatInterval)
	defer heart.Stop()
	for {
		drain()
		if terminal != nil {
			if payload, done := terminal(); done {
				drain()
				// A final partial line (no trailing newline) still ships.
				if len(pending) > 0 {
					sw.write("log", string(pending))
					pending = nil
				}
				sw.write("status", payload)
				return
			}
		}
		select {
		case <-ctx.Done():
			return
		case <-heart.C:
			sw.comment("ping")
		case <-tick.C:
		}
	}
}

// splitCompleteLines splits b at newlines, returning the complete lines and
// the unterminated remainder.
func splitCompleteLines(b []byte) (lines []string, rest []byte) {
	for {
		i := bytes.IndexByte(b, '\n')
		if i < 0 {
			return lines, b
		}
		lines = append(lines, string(b[:i]))
		b = b[i+1:]
	}
}
