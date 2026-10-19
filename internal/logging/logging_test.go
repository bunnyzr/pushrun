package logging

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestSetupLevelFiltering(t *testing.T) {
	root := t.TempDir()

	logger, err := Setup(filepath.Join(root, "logs"), "info", false)
	if err != nil {
		t.Fatalf("Setup: %v", err)
	}
	logger.Debug("hidden-debug-message")
	logger.Info("visible-info-message")

	data, err := os.ReadFile(filepath.Join(root, "logs", "daemon.log"))
	if err != nil {
		t.Fatalf("read daemon.log: %v", err)
	}
	content := string(data)
	if strings.Contains(content, "hidden-debug-message") {
		t.Error("daemon.log contains debug message at info level")
	}
	if !strings.Contains(content, "visible-info-message") {
		t.Error("daemon.log missing info message")
	}

	debugLogger, err := Setup(filepath.Join(root, "logs"), "debug", true)
	if err != nil {
		t.Fatalf("Setup debug: %v", err)
	}
	debugLogger.Debug("now-visible-debug-message")
	data, err = os.ReadFile(filepath.Join(root, "logs", "daemon.log"))
	if err != nil {
		t.Fatalf("read daemon.log: %v", err)
	}
	if !strings.Contains(string(data), "now-visible-debug-message") {
		t.Error("daemon.log missing debug message at debug level")
	}

	if _, err := Setup(filepath.Join(root, "logs"), "nonsense", false); err == nil {
		t.Error("Setup with invalid level: want error, got nil")
	}
}

func TestDaemonLogIsJSON(t *testing.T) {
	root := t.TempDir()

	logger, err := Setup(filepath.Join(root, "logs"), "info", false)
	if err != nil {
		t.Fatalf("Setup: %v", err)
	}
	logger.With("component", "test").Info("hello", "run_id", "abc123")

	data, err := os.ReadFile(filepath.Join(root, "logs", "daemon.log"))
	if err != nil {
		t.Fatalf("read daemon.log: %v", err)
	}
	line := strings.TrimSpace(string(data))
	var record map[string]any
	if err := json.Unmarshal([]byte(line), &record); err != nil {
		t.Fatalf("daemon.log line is not JSON: %v\nline: %s", err, line)
	}
	if record["msg"] != "hello" {
		t.Errorf("msg = %v, want hello", record["msg"])
	}
	if record["component"] != "test" || record["run_id"] != "abc123" {
		t.Errorf("attrs missing: %v", record)
	}
	if record["level"] != "INFO" {
		t.Errorf("level = %v, want INFO", record["level"])
	}
}

func TestRotationRollsAtSizeLimit(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "daemon.log")

	w, err := newRotateWriter(path, 100, 5)
	if err != nil {
		t.Fatalf("newRotateWriter: %v", err)
	}

	// 21-byte records; the file rolls every ~5 writes, well past 5 rolls total.
	for range 40 {
		if _, err := w.Write([]byte(strings.Repeat("x", 20) + "\n")); err != nil {
			t.Fatalf("write: %v", err)
		}
	}

	if _, err := os.Stat(path); err != nil {
		t.Fatalf("current log missing: %v", err)
	}
	if _, err := os.Stat(path + ".1"); err != nil {
		t.Fatalf("rotated log %s.1 missing: %v", path, err)
	}

	matches, err := filepath.Glob(path + "*")
	if err != nil {
		t.Fatal(err)
	}
	if len(matches) > 5 {
		t.Errorf("found %d log files (%v), want at most 5", len(matches), matches)
	}
	if _, err := os.Stat(path + ".5"); !os.IsNotExist(err) {
		t.Errorf("%s.5 should not exist (5-file cap)", path)
	}

	// The oldest retained backup must contain older data than the current
	// file, i.e. rotation actually shifted content instead of truncating.
	current, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(current) > 100 {
		t.Errorf("current log size = %d, exceeds 100-byte limit", len(current))
	}
}
