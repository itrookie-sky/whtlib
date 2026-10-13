package logx

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestFilePatternExpansion(t *testing.T) {
	now := time.Date(2026, 1, 2, 23, 0, 0, 0, time.Local)
	for _, tc := range []struct{ pattern, expected string }{
		{"sql-{y-m-d}.log", "sql-2026-01-02.log"},
		{"ymd-{ymd}.log", "ymd-20260102.log"},
		{"system-y-m-d.log", "system-y-m-d.log"},
		{"sql-{y}_{m}.{d}.log", "sql-2026_01.02.log"},
		{"{y-m-d}.log", "2026-01-02.log"},
	} {
		t.Run(tc.pattern, func(t *testing.T) {
			pattern, err := compileFilePattern(tc.pattern)
			if err != nil {
				t.Fatal(err)
			}
			if actual := pattern.expand(now); actual != tc.expected {
				t.Fatalf("want %q, got %q", tc.expected, actual)
			}
			if !pattern.matcher.MatchString(tc.expected) {
				t.Fatal("expanded name not recognized")
			}
		})
	}
	for _, name := range []string{"{}.log", "{h}.log", "{Y}.log", "{y.log", "y}.log", "{{y}}.log"} {
		_, err := normalizeFileConfig(FileConfig{File: name})
		if err == nil {
			t.Fatalf("invalid pattern %q accepted", name)
		}
	}
	if DefaultFileConfig().File != "{y-m-d}.log" {
		t.Fatal("wrong default pattern")
	}
}

func TestFilePatternRolloverAndReopen(t *testing.T) {
	path := t.TempDir()
	writer := testfileWriter(t, FileConfig{Path: path, File: "sql-{y-m-d}.log", RotateInterval: -1, RotateCheckInterval: time.Hour})
	before := time.Date(2026, 12, 31, 23, 59, 59, 0, time.Local)
	after := before.Add(time.Second)
	for _, record := range []struct {
		at      time.Time
		message string
	}{{before, "before\n"}, {after, "after\n"}} {
		_, err := writer.writeAt([]byte(record.message), record.at)
		if err != nil {
			t.Fatal(err)
		}
	}
	files := readLogFiles(t, path)
	if files["sql-2026-12-31.log"] != "before\n" || files["sql-2027-01-01.log"] != "after\n" {
		t.Fatal(files)
	}
	_, err := writer.writeAt([]byte("append\n"), before)
	if err != nil {
		t.Fatal(err)
	}
	if readLogFiles(t, path)["sql-2026-12-31.log"] != "before\nappend\n" {
		t.Fatal("existing date file truncated")
	}
}

func TestFilePatternCleanupAndRotation(t *testing.T) {
	path := t.TempDir()
	now := time.Now()
	writer := testfileWriter(t, FileConfig{Path: path, File: "sql-{y-m-d}.log", RotateSize: 5, RotateInterval: -1, RotateCheckInterval: time.Hour})
	for range 2 {
		_, err := writer.writeAt([]byte("line\n"), now)
		if err != nil {
			t.Fatal(err)
		}
	}
	files := readLogFiles(t, path)
	if len(files) != 2 {
		t.Fatalf("expected active and archive: %v", files)
	}
	writer.mu.Lock()
	for name := range files {
		if name == writer.activeName {
			continue
		}
		if _, ok := writer.parseArchive(name); !ok {
			t.Errorf("archive not recognized: %s", name)
		}
	}
	writer.mu.Unlock()
	oldName := "sql-2000-01-02.log"
	unrelated := "other-2000-01-02.log"
	for _, name := range []string{oldName, unrelated, "sql-invalid.log"} {
		file := filepath.Join(path, name)
		err := os.WriteFile(file, []byte("old"), 0o600)
		if err != nil {
			t.Fatal(err)
		}
		err = os.Chtimes(file, now.Add(-48*time.Hour), now.Add(-48*time.Hour))
		if err != nil {
			t.Fatal(err)
		}
	}
	err := writer.Cleanup()
	if err != nil {
		t.Fatal(err)
	}
	files = readLogFiles(t, path)
	if _, ok := files[oldName]; ok {
		t.Fatal("expired date file retained")
	}
	if files[unrelated] != "old" || files["sql-invalid.log"] != "old" {
		t.Fatal("unrelated file removed")
	}
	if files[writer.pattern.expand(now)] != "line\n" {
		t.Fatal("active file removed")
	}
}

func TestFilePatternManagerSharingAndConflicts(t *testing.T) {
	path := t.TempDir()
	config := Config{WriteFile: true, File: FileConfig{Path: path, File: "sql-{y-m-d}.log"}}
	a := newTestLogger(t, config)
	b := newTestLogger(t, config)
	if a.core.handler.state.lease.entry != b.core.handler.state.lease.entry {
		t.Fatal("expression not shared")
	}
	for _, file := range []string{"sql-2027-01-02.log", "sql-{y}-01-02.log"} {
		_, err := New(Config{WriteFile: true, File: FileConfig{Path: path, File: file}})
		if err == nil || !strings.Contains(err.Error(), "overlapping") {
			t.Fatalf("conflicting expression accepted: %s, %v", file, err)
		}
	}
	c := newTestLogger(t, Config{WriteFile: true, File: FileConfig{Path: path, File: "other-{ymd}.log"}})
	if c.core.handler.state.lease.entry == a.core.handler.state.lease.entry {
		t.Fatal("different expressions shared")
	}
}

func TestFilePatternSwitchFailureKeepsOldFile(t *testing.T) {
	path := t.TempDir()
	writer := testfileWriter(t, FileConfig{Path: path, File: "{y-m-d}.log", RotateInterval: -1, RotateCheckInterval: time.Hour})
	now := time.Now()
	_, err := writer.writeAt([]byte("before\n"), now)
	if err != nil {
		t.Fatal(err)
	}
	next := now.AddDate(0, 0, 1)
	err = os.Mkdir(filepath.Join(path, writer.pattern.expand(next)), 0o700)
	if err != nil {
		t.Fatal(err)
	}
	_, err = writer.writeAt([]byte("must fail\n"), next)
	if err == nil {
		t.Fatal("directory accepted as file")
	}
	_, err = writer.writeAt([]byte("retained\n"), now)
	if err != nil {
		t.Fatal(err)
	}
	if readLogFiles(t, path)[writer.pattern.expand(now)] != "before\nretained\n" {
		t.Fatal("old file lost")
	}
}

func TestFilePatternHistoryLimit(t *testing.T) {
	path := t.TempDir()
	writer := testfileWriter(t, FileConfig{Path: path, File: "{y-m-d}.log", RotateBackupExpire: -1, RotateBackupLimit: 1, RotateInterval: -1, RotateCheckInterval: time.Hour})
	now := time.Now()
	for i, name := range []string{"2000-01-01.log", "2000-01-02.log"} {
		file := filepath.Join(path, name)
		err := os.WriteFile(file, []byte("history"), 0o600)
		if err != nil {
			t.Fatal(err)
		}
		modified := now.Add(time.Duration(i-3) * time.Hour)
		err = os.Chtimes(file, modified, modified)
		if err != nil {
			t.Fatal(err)
		}
	}
	err := writer.Cleanup()
	if err != nil {
		t.Fatal(err)
	}
	files := readLogFiles(t, path)
	if len(files) != 2 || files["2000-01-02.log"] != "history" {
		t.Fatal(files)
	}
	if _, ok := files[writer.pattern.expand(now)]; !ok {
		t.Fatal("active file deleted")
	}
}
