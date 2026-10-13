package logx

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

func testfileWriter(t *testing.T, config FileConfig) (writer *fileWriter) {
	t.Helper()
	writer, err := newFileWriter(config)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		err := writer.Close()
		if err != nil {
			t.Error(err)
		}
	})
	return
}

func readLogFiles(t *testing.T, path string) (files map[string]string) {
	t.Helper()
	entries, err := os.ReadDir(path)
	if err != nil {
		t.Fatal(err)
	}
	files = make(map[string]string)
	for _, entry := range entries {
		if entry.IsDir() || entry.Type()&os.ModeSymlink != 0 {
			continue
		}
		data, readErr := os.ReadFile(filepath.Join(path, entry.Name()))
		if readErr != nil {
			// 后台清理可能刚刚移除了此归档。
			if errors.Is(readErr, os.ErrNotExist) {
				continue
			}
			t.Fatal(readErr)
		}
		files[entry.Name()] = string(data)
	}
	return
}

func TestFileSizeRotationAndOversize(t *testing.T) {
	path := t.TempDir()
	writer := testfileWriter(t, FileConfig{Path: path, RotateSize: 8, RotateInterval: -1})
	for _, data := range []string{"aaaa\n", "bbbb\n", "long-record\n", "end\n"} {
		n, err := writer.Write([]byte(data))
		if err != nil || n != len(data) {
			t.Fatalf("write: %d, %v", n, err)
		}
	}
	files := readLogFiles(t, path)
	if len(files) != 4 || files["app.log"] != "end\n" {
		t.Fatalf("unexpected split: %#v", files)
	}
	for _, expected := range []string{"aaaa\n", "bbbb\n", "long-record\n"} {
		found := false
		for _, data := range files {
			found = found || data == expected
		}
		if !found {
			t.Fatalf("record split or lost: %q", expected)
		}
	}
}

func TestFileReopenAndClose(t *testing.T) {
	path := t.TempDir()
	writer := testfileWriter(t, FileConfig{Path: path})
	_, err := writer.Write([]byte("before\n"))
	if err != nil {
		t.Fatal(err)
	}
	if writer.config.RotateBackupExpire != 24*time.Hour {
		t.Fatal("retention default must be 24h")
	}
	err = writer.Sync()
	if err != nil {
		t.Fatal(err)
	}
	_ = writer.Close()
	_, err = writer.Write([]byte("closed"))
	if !errors.Is(err, os.ErrClosed) || !errors.Is(writer.Rotate(), os.ErrClosed) || !errors.Is(writer.Cleanup(), os.ErrClosed) || !errors.Is(writer.Sync(), os.ErrClosed) {
		t.Fatal("operation after close accepted")
	}
	reopened := testfileWriter(t, FileConfig{Path: path})
	_, err = reopened.Write([]byte("after\n"))
	if err != nil {
		t.Fatal(err)
	}
	if readLogFiles(t, path)["app.log"] != "before\nafter\n" {
		t.Fatal("reopen truncated content")
	}
}

func TestCleanupRetentionAndIsolation(t *testing.T) {
	path := t.TempDir()
	now := time.Now()
	archiveName := func(prefix string, age time.Duration, id int) string {
		return fmt.Sprintf("%s.%s.%016x.log", prefix, now.Add(-age).UTC().Format("20060102T150405.000000000Z"), id)
	}
	expired := archiveName("app", 25*time.Hour, 1)
	old := archiveName("app", 2*time.Hour, 2)
	recent := archiveName("app", time.Hour, 3)
	other := archiveName("other", 48*time.Hour, 4)
	symbolic := archiveName("app", 48*time.Hour, 5)
	for _, name := range []string{expired, old, recent, other, "app.invalid.log", "app.log"} {
		err := os.WriteFile(filepath.Join(path, name), []byte(name), 0o600)
		if err != nil {
			t.Fatal(err)
		}
	}
	err := os.Symlink(filepath.Join(path, other), filepath.Join(path, symbolic))
	if err != nil {
		t.Fatal(err)
	}
	writer := testfileWriter(t, FileConfig{Path: path, RotateBackupLimit: 1, RotateInterval: -1})
	err = writer.Cleanup()
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{expired, old} {
		_, err = os.Lstat(filepath.Join(path, name))
		if !errors.Is(err, os.ErrNotExist) {
			t.Fatalf("archive not removed: %s, %v", name, err)
		}
	}
	for _, name := range []string{recent, other, symbolic, "app.invalid.log", "app.log"} {
		_, err = os.Lstat(filepath.Join(path, name))
		if err != nil {
			t.Fatalf("unrelated file removed: %s, %v", name, err)
		}
	}
}

func TestIdleRotationAndCleanup(t *testing.T) {
	path := t.TempDir()
	writer := testfileWriter(t, FileConfig{
		Path: path, RotateSize: -1, RotateInterval: 20 * time.Millisecond,
		RotateBackupExpire: 100 * time.Millisecond, RotateCheckInterval: 5 * time.Millisecond,
	})
	_, err := writer.Write([]byte("idle\n"))
	if err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(3 * time.Second)
	seenArchive := false
	for time.Now().Before(deadline) {
		files := readLogFiles(t, path)
		if len(files) > 1 {
			seenArchive = true
		}
		if seenArchive && len(files) == 1 && files["app.log"] == "" {
			return
		}
		time.Sleep(2 * time.Millisecond)
	}
	t.Fatalf("idle rotation/cleanup failed: %#v", readLogFiles(t, path))
}

func TestFileConcurrentRotationAndClose(t *testing.T) {
	path := t.TempDir()
	writer := testfileWriter(t, FileConfig{Path: path, RotateSize: 128, RotateInterval: -1})
	var wg sync.WaitGroup
	for range 4 {
		wg.Go(func() {
			for range 40 {
				_, err := writer.Write([]byte("record\n"))
				if err != nil {
					t.Error(err)
				}
			}
		})
	}
	wg.Wait()
	count := 0
	for _, data := range readLogFiles(t, path) {
		count += strings.Count(data, "record\n")
	}
	if count != 160 {
		t.Fatalf("lost records: %d", count)
	}
	for range 4 {
		wg.Go(func() {
			for range 10 {
				_, err := writer.Write([]byte("closing\n"))
				if err != nil && !errors.Is(err, os.ErrClosed) {
					t.Error(err)
				}
			}
		})
	}
	for range 3 {
		wg.Go(func() {
			err := writer.Close()
			if err != nil {
				t.Error(err)
			}
		})
	}
	wg.Wait()
}

func TestFileConfigAndNoEmptyRotation(t *testing.T) {
	for _, config := range []FileConfig{
		{Path: t.TempDir(), File: "../app.log"}, {Path: t.TempDir(), RotateSize: -2},
		{Path: t.TempDir(), RotateBackupExpire: -2}, {Path: t.TempDir(), RotateBackupLimit: -1},
	} {
		_, err := newFileWriter(config)
		if err == nil {
			t.Fatal("invalid file config accepted")
		}
	}
	path := t.TempDir()
	writer := testfileWriter(t, FileConfig{Path: path, RotateSize: 1})
	_, err := writer.Write(nil)
	if err != nil {
		t.Fatal(err)
	}
	for range 3 {
		err = writer.Rotate()
		if err != nil {
			t.Fatal(err)
		}
	}
	if len(readLogFiles(t, path)) != 1 {
		t.Fatal("empty files rotated")
	}
}

func TestLoggerOwnsDefaultWriter(t *testing.T) {
	logger := newTestLogger(t, Config{File: FileConfig{Path: t.TempDir()}})
	logger.Info(t.Context(), "owned")
	_ = logger.Close()
	select {
	case <-logger.core.handler.state.lease.entry.writer.done:
	default:
		t.Fatal("maintenance leaked after logger close")
	}
	_, err := logger.core.handler.state.lease.entry.writer.Write([]byte("closed"))
	if !errors.Is(err, os.ErrClosed) {
		t.Fatal("owned file not closed")
	}
}

func TestBackgroundErrorCallback(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "logs")
	reported := make(chan error, 1)
	writer := testfileWriter(t, FileConfig{
		Path: path, RotateInterval: -1, RotateCheckInterval: 5 * time.Millisecond,
		OnError: func(err error) {
			select {
			case reported <- err:
			default:
			}
		},
	})
	err := os.Rename(path, filepath.Join(root, "moved"))
	if err != nil {
		t.Fatal(err)
	}
	select {
	case err = <-reported:
		if !errors.Is(err, os.ErrNotExist) {
			t.Fatalf("unexpected maintenance error: %v", err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("background error was hidden")
	}
	_ = writer.Close()
}
