package server_test

import (
	"bufio"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/bunnyzr/pushrun/internal/config"
	"github.com/bunnyzr/pushrun/internal/run"
	"github.com/bunnyzr/pushrun/internal/server"
)

// sseEvent is one parsed Server-Sent Events block.
type sseEvent struct {
	event   string
	data    []string
	comment string
}

func hasData(ev sseEvent, want ...string) bool {
	for _, w := range want {
		found := false
		for _, d := range ev.data {
			if d == w {
				found = true
			}
		}
		if !found {
			return false
		}
	}
	return true
}

// sseGet opens an SSE request with the test token and returns a buffered
// reader over the stream. The stream (and its 10s safety timeout) is cleaned
// up with the test.
func sseGet(t *testing.T, srv *httptest.Server, path string) *bufio.Reader {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	t.Cleanup(cancel)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, srv.URL+path, nil)
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Authorization", "Bearer "+testToken)
	resp, err := srv.Client().Do(req)
	if err != nil {
		t.Fatalf("GET %s: %v", path, err)
	}
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		_ = resp.Body.Close()
		t.Fatalf("GET %s: status %d, body %s", path, resp.StatusCode, body)
	}
	if ct := resp.Header.Get("Content-Type"); !strings.Contains(ct, "text/event-stream") {
		_ = resp.Body.Close()
		t.Fatalf("GET %s: Content-Type %q, want text/event-stream", path, ct)
	}
	t.Cleanup(func() { _ = resp.Body.Close() })
	return bufio.NewReader(resp.Body)
}

// readEventUntil reads SSE events until one matches, failing the test when
// the stream ends first.
func readEventUntil(t *testing.T, r *bufio.Reader, match func(sseEvent) bool) sseEvent {
	t.Helper()
	var cur sseEvent
	var seen []sseEvent
	for {
		line, err := r.ReadString('\n')
		if err != nil {
			t.Fatalf("stream ended before expected event: %v (seen: %+v)", err, seen)
		}
		line = strings.TrimRight(line, "\r\n")
		if line == "" {
			seen = append(seen, cur)
			if match(cur) {
				return cur
			}
			cur = sseEvent{}
			continue
		}
		switch {
		case strings.HasPrefix(line, "event: "):
			cur.event = strings.TrimPrefix(line, "event: ")
		case strings.HasPrefix(line, "data: "):
			cur.data = append(cur.data, strings.TrimPrefix(line, "data: "))
		case strings.HasPrefix(line, ":"):
			cur.comment = strings.TrimSpace(strings.TrimPrefix(line, ":"))
		}
	}
}

// seedRun creates a run directory with the given build.log content and, when
// rec is non-nil, a result.json (a run in flight has no record yet).
func seedRun(t *testing.T, paths config.Paths, proj, inst, runID string, rec *run.Record, logContent string) string {
	t.Helper()
	dir := filepath.Join(paths.Runs, proj, inst, runID)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if logContent != "" {
		if err := os.WriteFile(filepath.Join(dir, "build.log"), []byte(logContent), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	if rec != nil {
		data, err := json.Marshal(rec)
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, "result.json"), data, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return dir
}

// The output stream replays existing build.log content, picks up lines
// appended mid-stream, and terminates with a status event once the run
// record shows a finished status.
func TestRunOutputReplaysFollowsAndTerminates(t *testing.T) {
	srv, paths := newTestServer(t)
	runID := "20261004-000000-abcdef01"
	dir := seedRun(t, paths, "demo", "default", runID, nil, "first\nsecond\n")

	r := sseGet(t, srv, "/api/v1/runs/"+runID+"/output")

	// Replay of existing content.
	readEventUntil(t, r, func(ev sseEvent) bool {
		return ev.event == "log" && hasData(ev, "first", "second")
	})

	// Live append mid-stream.
	f, err := os.OpenFile(filepath.Join(dir, "build.log"), os.O_APPEND|os.O_WRONLY, 0)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.WriteString("third\n"); err != nil {
		t.Fatal(err)
	}
	_ = f.Close()
	readEventUntil(t, r, func(ev sseEvent) bool {
		return ev.event == "log" && hasData(ev, "third")
	})

	// Finishing the run (result.json appears) terminates the stream.
	rec := &run.Record{ID: runID, Status: run.StatusSuccess,
		StartedAt: time.Now().UTC(), FinishedAt: time.Now().UTC()}
	data, err := json.Marshal(rec)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "result.json"), data, 0o644); err != nil {
		t.Fatal(err)
	}
	ev := readEventUntil(t, r, func(ev sseEvent) bool { return ev.event == "status" })
	if !hasData(ev, `{"status":"SUCCESS"}`) {
		t.Fatalf("terminal event data = %v, want status SUCCESS", ev.data)
	}
	if _, err := r.ReadString('\n'); err != io.EOF {
		t.Fatalf("stream did not close after terminal event: %v", err)
	}
}

// A run that already finished before the client connects replays its log
// and ends immediately.
func TestRunOutputFinishedRunReplaysAndEnds(t *testing.T) {
	srv, paths := newTestServer(t)
	runID := "20261004-000001-abcdef01"
	rec := &run.Record{ID: runID, Status: run.StatusFailed,
		StartedAt: time.Now().UTC(), FinishedAt: time.Now().UTC()}
	seedRun(t, paths, "demo", "default", runID, rec, "boom\n")

	r := sseGet(t, srv, "/api/v1/runs/"+runID+"/output")
	readEventUntil(t, r, func(ev sseEvent) bool { return ev.event == "log" && hasData(ev, "boom") })
	ev := readEventUntil(t, r, func(ev sseEvent) bool { return ev.event == "status" })
	if !hasData(ev, `{"status":"FAILED"}`) {
		t.Fatalf("terminal event data = %v, want status FAILED", ev.data)
	}
	if _, err := r.ReadString('\n'); err != io.EOF {
		t.Fatalf("stream did not close after terminal event: %v", err)
	}
}

func TestRunOutputUnknownRun(t *testing.T) {
	srv, _ := newTestServer(t)
	status, _, body := doReq(t, srv, http.MethodGet, "/api/v1/runs/20261004-000000-00000000/output", testToken, nil)
	if status != http.StatusNotFound {
		t.Fatalf("status %d, want 404, body %s", status, body)
	}
	if code, _, _ := decodeErr(t, body); code != "not_found" {
		t.Fatalf("code = %q, want not_found", code)
	}
}

func TestLogsTree(t *testing.T) {
	srv, paths := newTestServer(t)
	root := filepath.Join(paths.Instances, "demo", "default", "logs")
	if err := os.MkdirAll(filepath.Join(root, "sub"), 0o755); err != nil {
		t.Fatal(err)
	}
	for name, content := range map[string]string{"a.log": "a\n", "sub/b.log": "b\n"} {
		if err := os.WriteFile(filepath.Join(root, name), []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	status, _, body := doReq(t, srv, http.MethodGet, "/api/v1/instances/demo/default/logs/tree", testToken, nil)
	if status != http.StatusOK {
		t.Fatalf("status %d, body %s", status, body)
	}
	var resp struct {
		Files []string `json:"files"`
	}
	if err := json.Unmarshal(body, &resp); err != nil {
		t.Fatalf("decode %s: %v", body, err)
	}
	if len(resp.Files) != 2 || resp.Files[0] != "a.log" || resp.Files[1] != "sub/b.log" {
		t.Fatalf("files = %v, want [a.log sub/b.log]", resp.Files)
	}
	// Relative paths only: nothing absolute may cross the API.
	if strings.Contains(string(body), root) {
		t.Fatalf("tree leaks absolute server path: %s", body)
	}
}

func TestLogsTreeEmptyWhenAbsent(t *testing.T) {
	srv, _ := newTestServer(t)
	status, _, body := doReq(t, srv, http.MethodGet, "/api/v1/instances/demo/default/logs/tree", testToken, nil)
	if status != http.StatusOK {
		t.Fatalf("status %d, body %s", status, body)
	}
	var resp struct {
		Files []string `json:"files"`
	}
	if err := json.Unmarshal(body, &resp); err != nil {
		t.Fatalf("decode %s: %v", body, err)
	}
	if resp.Files == nil || len(resp.Files) != 0 {
		t.Fatalf("files = %v, want empty list", resp.Files)
	}
}

func TestLogsFileContentAndTail(t *testing.T) {
	srv, paths := newTestServer(t)
	root := filepath.Join(paths.Instances, "demo", "default", "logs")
	if err := os.MkdirAll(root, 0o755); err != nil {
		t.Fatal(err)
	}
	content := "l1\nl2\nl3\nl4\nl5\n"
	if err := os.WriteFile(filepath.Join(root, "app.log"), []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	base := "/api/v1/instances/demo/default/logs/file?path="

	status, _, body := doReq(t, srv, http.MethodGet, base+"app.log", testToken, nil)
	if status != http.StatusOK {
		t.Fatalf("status %d, body %s", status, body)
	}
	m := decodeJSON(t, body)
	if m["path"] != "app.log" || m["content"] != content {
		t.Fatalf("unexpected file response: %s", body)
	}

	status, _, body = doReq(t, srv, http.MethodGet, base+"app.log&tail_lines=2", testToken, nil)
	if status != http.StatusOK {
		t.Fatalf("tail: status %d, body %s", status, body)
	}
	if m := decodeJSON(t, body); m["content"] != "l4\nl5\n" {
		t.Fatalf("tail content = %q, want %q", m["content"], "l4\nl5\n")
	}

	for _, q := range []string{"app.log&tail_lines=0", "app.log&tail_lines=abc", "app.log&tail_lines=-3"} {
		status, _, body = doReq(t, srv, http.MethodGet, base+q, testToken, nil)
		if status != http.StatusBadRequest {
			t.Fatalf("%s: status %d, want 400, body %s", q, status, body)
		}
	}

	status, _, _ = doReq(t, srv, http.MethodGet, "/api/v1/instances/demo/default/logs/file", testToken, nil)
	if status != http.StatusBadRequest {
		t.Fatalf("missing path: status %d, want 400", status)
	}

	status, _, _ = doReq(t, srv, http.MethodGet, base+"nope.log", testToken, nil)
	if status != http.StatusNotFound {
		t.Fatalf("missing file: status %d, want 404", status)
	}
}

// Path traversal against the business-log root is rejected.
func TestLogsFileRejectsTraversal(t *testing.T) {
	srv, paths := newTestServer(t)
	root := filepath.Join(paths.Instances, "demo", "default", "logs")
	if err := os.MkdirAll(root, 0o755); err != nil {
		t.Fatal(err)
	}
	for _, p := range []string{"../../etc/passwd", "/etc/passwd", "../secret", ".."} {
		q := url.Values{"path": {p}}
		status, _, body := doReq(t, srv, http.MethodGet,
			"/api/v1/instances/demo/default/logs/file?"+q.Encode(), testToken, nil)
		if status != http.StatusBadRequest {
			t.Fatalf("path %q: status %d, want 400, body %s", p, status, body)
		}
		if code, _, _ := decodeErr(t, body); code != "invalid" {
			t.Fatalf("path %q: code = %q, want invalid", p, code)
		}
	}
}

// follow=1 switches the file endpoint to SSE: it replays current content,
// streams appends, and emits heartbeat comments while idle.
func TestLogsFileFollow(t *testing.T) {
	srv, paths := newTestServer(t)
	root := filepath.Join(paths.Instances, "demo", "default", "logs")
	if err := os.MkdirAll(root, 0o755); err != nil {
		t.Fatal(err)
	}
	logPath := filepath.Join(root, "app.log")
	if err := os.WriteFile(logPath, []byte("old\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	old := server.SSEHeartbeatInterval
	server.SSEHeartbeatInterval = 50 * time.Millisecond
	defer func() { server.SSEHeartbeatInterval = old }()

	r := sseGet(t, srv, "/api/v1/instances/demo/default/logs/file?path=app.log&follow=1")

	readEventUntil(t, r, func(ev sseEvent) bool { return ev.event == "log" && hasData(ev, "old") })

	f, err := os.OpenFile(logPath, os.O_APPEND|os.O_WRONLY, 0)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.WriteString("new\n"); err != nil {
		t.Fatal(err)
	}
	_ = f.Close()
	readEventUntil(t, r, func(ev sseEvent) bool { return ev.event == "log" && hasData(ev, "new") })

	// Heartbeat comments keep the stream liveness-detectable while idle.
	readEventUntil(t, r, func(ev sseEvent) bool { return ev.comment == "ping" })
}

// tail_lines composes with follow: the last N lines are sent first, then
// appends stream.
func TestLogsFileFollowWithTail(t *testing.T) {
	srv, paths := newTestServer(t)
	root := filepath.Join(paths.Instances, "demo", "default", "logs")
	if err := os.MkdirAll(root, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "app.log"), []byte("l1\nl2\nl3\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	r := sseGet(t, srv, "/api/v1/instances/demo/default/logs/file?path=app.log&tail_lines=2&follow=1")
	ev := readEventUntil(t, r, func(ev sseEvent) bool { return ev.event == "log" })
	if !hasData(ev, "l2", "l3") || hasData(ev, "l1") {
		t.Fatalf("initial follow data = %v, want [l2 l3]", ev.data)
	}
}
