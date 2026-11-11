package server

import (
	"fmt"
	"io"
	"net/http"
	"time"
)

// SSEHeartbeatInterval is how often an idle SSE stream emits a ": ping"
// comment so clients and proxies can detect a dead connection. It is a
// variable only so tests can shorten it.
var SSEHeartbeatInterval = 15 * time.Second

// followPollInterval is how often follow mode polls a log file for new
// bytes — deliberately simple polling, no fsnotify dependency.
const followPollInterval = 200 * time.Millisecond

// sseWriter writes Server-Sent Events with a flush per event.
type sseWriter struct {
	w  http.ResponseWriter
	fl http.Flusher
}

// newSSEWriter sets the SSE response headers, flushes them immediately so
// clients see the stream open even before the first event, and returns the
// event writer; ok is false when the response cannot stream.
func newSSEWriter(w http.ResponseWriter) (*sseWriter, bool) {
	fl, ok := w.(http.Flusher)
	if !ok {
		return nil, false
	}
	h := w.Header()
	h.Set("Content-Type", "text/event-stream")
	h.Set("Cache-Control", "no-cache")
	h.Set("Connection", "keep-alive")
	w.WriteHeader(http.StatusOK)
	fl.Flush()
	return &sseWriter{w: w, fl: fl}, true
}

// write sends one named event and flushes. Every element of lines becomes
// its own data: field (the SSE wire format for multi-line payloads).
func (s *sseWriter) write(event string, lines ...string) {
	_, _ = fmt.Fprintf(s.w, "event: %s\n", event)
	for _, l := range lines {
		_, _ = fmt.Fprintf(s.w, "data: %s\n", l)
	}
	_, _ = io.WriteString(s.w, "\n")
	s.fl.Flush()
}

// comment sends an SSE comment (": text") and flushes; used for heartbeats.
func (s *sseWriter) comment(text string) {
	_, _ = fmt.Fprintf(s.w, ": %s\n\n", text)
	s.fl.Flush()
}
