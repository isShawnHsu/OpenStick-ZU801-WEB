package svc

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"uz801d/internal/config"
)

func newLogTestManager() *Manager {
	return &Manager{cfg: config.Default()}
}

func writeTestLog(t *testing.T, lines int) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "frpc.log")
	var b strings.Builder
	for i := 0; i < lines; i++ {
		fmt.Fprintf(&b, "line-%03d\n", i)
	}
	if err := os.WriteFile(path, []byte(b.String()), 0o600); err != nil {
		t.Fatalf("write log: %v", err)
	}
	return path
}

func TestReadLogFileTailNewestFirst(t *testing.T) {
	m := newLogTestManager()
	path := writeTestLog(t, 50)
	text, err := m.ReadLogFile(path, 10)
	if err != nil {
		t.Fatalf("ReadLogFile: %v", err)
	}
	lines := strings.Split(text, "\n")
	if len(lines) != 10 {
		t.Fatalf("got %d lines, want 10: %q", len(lines), text)
	}
	if lines[0] != "line-049" || lines[len(lines)-1] != "line-040" {
		t.Fatalf("unexpected order: %q", text)
	}
}

func TestReadLogFileDefaultLines(t *testing.T) {
	m := newLogTestManager()
	path := writeTestLog(t, 250)
	// lines < 10 falls back to cfg.System.LogLines (200).
	text, err := m.ReadLogFile(path, 0)
	if err != nil {
		t.Fatalf("ReadLogFile: %v", err)
	}
	lines := strings.Split(text, "\n")
	if len(lines) != 200 {
		t.Fatalf("got %d lines, want 200", len(lines))
	}
	if lines[0] != "line-249" || lines[len(lines)-1] != "line-050" {
		t.Fatalf("unexpected order: first=%q last=%q", lines[0], lines[len(lines)-1])
	}
}

func TestReadLogFileRejectsInvalidPaths(t *testing.T) {
	m := newLogTestManager()
	if _, err := m.ReadLogFile("", 100); err == nil {
		t.Fatal("empty path accepted")
	}
	if _, err := m.ReadLogFile("relative/frpc.log", 100); err == nil {
		t.Fatal("relative path accepted")
	}
	if _, err := m.ReadLogFile(t.TempDir(), 100); err == nil {
		t.Fatal("directory accepted")
	}
	if _, err := m.ReadLogFile(filepath.Join(t.TempDir(), "missing.log"), 100); err == nil {
		t.Fatal("missing file accepted")
	}
}
