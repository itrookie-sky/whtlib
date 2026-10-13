package logx

import (
	"context"
	"errors"
	"log/slog"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func logContents(t *testing.T, path string) (contents string) {
	t.Helper()
	for _, data := range readLogFiles(t, path) {
		contents += data
	}
	return
}

func TestSetConfigRetainsValuesAndIdentity(t *testing.T) {
	path := t.TempDir()
	logger := newTestLogger(t, Config{WriteFile: true, File: FileConfig{File: "app.log", Path: path}, TraceKey: testKey(1), ContextFields: []ContextField{{Name: "userId", Key: testKey(2)}}})
	derived := testSkip(t, logger, 0)
	if logger.SetConfig(Config{WriteFile: true, Level: slog.LevelDebug}) != logger {
		t.Fatal("identity changed")
	}
	ctx := context.WithValue(t.Context(), testKey(1), "trace")
	ctx = context.WithValue(ctx, testKey(2), 42)
	derived.Debug(ctx, "filtered")
	logger.SetConfig(Config{WriteFile: true}).Debug(ctx, "retained")
	output := logContents(t, path)
	if strings.Contains(output, "filtered") || !strings.Contains(output, `{"traceId":"trace","userId":42}`) {
		t.Fatal(output)
	}
	logger.SetConfig(Config{WriteFile: true, ContextFields: []ContextField{}}).Info(ctx, "cleared")
	derived.Info(ctx, "unchanged")
	output = logContents(t, path)
	if !strings.Contains(output, `{"traceId":"trace"}`) || !strings.Contains(output, "INFO unchanged") {
		t.Fatal(output)
	}
	fields := []ContextField{{Name: "other", Key: testKey(2)}}
	logger.SetConfig(Config{WriteFile: true, ContextFields: fields})
	fields[0].Name = "mutated"
	logger.Info(ctx, "copied")
	if strings.Contains(logContents(t, path), "mutated") {
		t.Fatal("slice alias")
	}
}

func TestSetConfigFailureKeepsPrevious(t *testing.T) {
	path := t.TempDir()
	logger := newTestLogger(t, Config{WriteFile: true, File: FileConfig{File: "app.log", Path: path}})
	old := logger.core.handler
	blocked := filepath.Join(path, "blocked")
	err := os.WriteFile(blocked, []byte("occupied"), 0o600)
	if err != nil {
		t.Fatal(err)
	}
	for _, config := range []Config{{TraceKey: []string{"invalid"}}, {WriteFile: true, File: FileConfig{File: "app.log", Path: blocked}}} {
		diagnostic := captureStderr(t, func() { logger.SetConfig(config) })
		if diagnostic == "" || logger.core.handler != old {
			t.Fatal("failed update changed state")
		}
	}
	logger.Info(t.Context(), "still usable")
	if !strings.Contains(logContents(t, path), "still usable") {
		t.Fatal("lost previous file")
	}
}

func TestSharedFileIsolationAndRelease(t *testing.T) {
	path := t.TempDir()
	config := Config{WriteFile: true, File: FileConfig{File: "app.log", Path: path}}
	a := newTestLogger(t, config)
	b := newTestLogger(t, config)
	c := testSkip(t, a, 0)
	writer := a.core.handler.state.lease.entry.writer
	if a.core == b.core || a.core == c.core || b.core.handler.state.lease.entry.writer != writer {
		t.Fatal("incorrect sharing boundary")
	}
	a.SetConfig(Config{WriteFile: true, Level: slog.LevelError})
	b.Info(t.Context(), "b before")
	_ = a.Close()
	b.Info(t.Context(), "b after")
	c.Info(t.Context(), "c after")
	_ = b.Close()
	select {
	case <-writer.done:
		t.Fatal("closed too early")
	default:
	}
	_ = c.Close()
	select {
	case <-writer.done:
	default:
		t.Fatal("last reference did not close writer")
	}
	if strings.Count(logContents(t, path), "INFO") != 3 {
		t.Fatal("close or config affected other instances")
	}
	reopened := newTestLogger(t, config)
	reopened.Info(t.Context(), "reopened")
	if !strings.Contains(logContents(t, path), "reopened") {
		t.Fatal("could not reopen")
	}
}

func TestSetConfigPathSwitchAndPolicy(t *testing.T) {
	path := t.TempDir()
	a := newTestLogger(t, Config{WriteFile: true, File: FileConfig{File: "app.log", Path: path, RotateInterval: -1}})
	b := newTestLogger(t, Config{WriteFile: true, File: FileConfig{File: "app.log", Path: path, RotateInterval: -1}})
	original := a.core.handler
	diagnostic := captureStderr(t, func() { a.SetConfig(Config{WriteFile: true, File: FileConfig{RotateSize: 1}}) })
	if !strings.Contains(diagnostic, "conflicting rotation policy") || a.core.handler != original {
		t.Fatal("shared policy mutated")
	}
	_, err := New(Config{WriteFile: true, File: FileConfig{File: "app.log", Path: path, RotateSize: 1}})
	if err == nil {
		t.Fatal("conflicting new instance accepted")
	}
	newPath := t.TempDir()
	a.SetConfig(Config{WriteFile: true, File: FileConfig{Path: newPath, File: "other.log"}}).Info(t.Context(), "new path")
	b.Info(t.Context(), "old path")
	b.SetConfig(Config{WriteFile: true, File: FileConfig{RotateSize: 1, RotateBackupExpire: time.Hour}})
	b.Info(t.Context(), "rotated")
	if len(readLogFiles(t, path)) != 2 || !strings.Contains(readLogFiles(t, newPath)["other.log"], "new path") {
		t.Fatal("path switch or exclusive policy update failed")
	}
}

func TestSetConfigRefreshesMaintenanceInterval(t *testing.T) {
	path := t.TempDir()
	logger := newTestLogger(t, Config{WriteFile: true, File: FileConfig{File: "app.log", Path: path, RotateInterval: -1, RotateCheckInterval: time.Hour}})
	logger.Info(t.Context(), "idle")
	logger.SetConfig(Config{WriteFile: true, File: FileConfig{RotateInterval: 10 * time.Millisecond, RotateCheckInterval: 5 * time.Millisecond}})
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if len(readLogFiles(t, path)) == 2 {
			return
		}
		time.Sleep(2 * time.Millisecond)
	}
	t.Fatal("maintenance not updated")
}

func TestSharedFileConcurrentRotation(t *testing.T) {
	path := t.TempDir()
	config := Config{WriteFile: true, File: FileConfig{File: "app.log", Path: path, RotateSize: 512, RotateInterval: -1}}
	var wg sync.WaitGroup
	for i := range 8 {
		wg.Go(func() {
			logger, err := New(config)
			if err != nil {
				t.Error(err)
				return
			}
			defer logger.Close()
			for j := range 50 {
				logger.Infof(t.Context(), "record-%d-%d", i, j)
			}
			logger.SetConfig(Config{WriteFile: true, Level: slog.LevelDebug})
		})
	}
	wg.Wait()
	output := logContents(t, path)
	if strings.Count(output, "\n") != 400 {
		t.Fatal("lost or split records")
	}
	for i := range 8 {
		for j := range 50 {
			if strings.Count(output, "record-"+strconv.Itoa(i)+"-"+strconv.Itoa(j)+"\n") != 1 {
				t.Fatal("record duplicated or missing")
			}
		}
	}
}

func TestDefaultSingletonAndCallers(t *testing.T) {
	previous := defaultLogger
	defaultLogger = &Logger{core: &loggerCore{}}
	t.Cleanup(func() { _ = Close(); defaultLogger = previous })
	if Default() != Default() || Default().core.handler != nil {
		t.Fatal("not lazy singleton")
	}
	path := t.TempDir()
	if SetConfig(Config{WriteFile: true, File: FileConfig{File: "app.log", Path: path}, Level: slog.LevelDebug}) != Default() {
		t.Fatal("identity changed")
	}
	_, file, line, _ := runtime.Caller(0)
	Info(t.Context(), "global")
	if !strings.Contains(logContents(t, path), filepath.Base(file)+":"+strconv.Itoa(line+1)+" INFO global") {
		t.Fatal("wrong caller")
	}
	for _, output := range []func(context.Context, string, ...any){Debug, Info, Warn, Error, Debugf, Infof, Warnf, Errorf} {
		output(t.Context(), "method")
	}
	Log(t.Context(), slog.LevelInfo, "custom")
	derived := Skip(0)
	defer derived.Close()
	_ = Close()
	derived.Info(t.Context(), "independent")
	if strings.Count(logContents(t, path), "\n") != 11 {
		t.Fatal("missing output")
	}
}

func TestSetConfigConcurrentClose(t *testing.T) {
	logger := newTestLogger(t, Config{})
	captureStderr(t, func() {
		var wg sync.WaitGroup
		for range 4 {
			wg.Go(func() {
				for range 10 {
					logger.SetConfig(Config{WriteFile: true, Level: slog.LevelDebug})
					logger.Info(t.Context(), "concurrent")
				}
			})
		}
		for range 2 {
			wg.Go(func() {
				err := logger.Close()
				if err != nil {
					t.Error(err)
				}
			})
		}
		wg.Wait()
	})
	if !logger.core.closed {
		t.Fatal("reopened closed logger")
	}
}

func TestLevelSnapshotAndDirectoryAlias(t *testing.T) {
	path := t.TempDir()
	var level slog.LevelVar
	a := newTestLogger(t, Config{WriteFile: true, File: FileConfig{File: "app.log", Path: path}, Level: &level})
	level.Set(slog.LevelError)
	if a.core.config.Level.Level() != slog.LevelInfo {
		t.Fatal("mutable level leaked")
	}
	alias := filepath.Join(t.TempDir(), "alias")
	err := os.Symlink(path, alias)
	if err != nil {
		t.Fatal(err)
	}
	b := newTestLogger(t, Config{WriteFile: true, File: FileConfig{File: "app.log", Path: alias}})
	if a.core.handler.state.lease.entry != b.core.handler.state.lease.entry {
		t.Fatal("directory alias opened duplicate writer")
	}
}

func TestSharedCallbacksAndHardLink(t *testing.T) {
	path := t.TempDir()
	var first, second, updated atomic.Int32
	a := newTestLogger(t, Config{WriteFile: true, File: FileConfig{File: "app.log", Path: path, OnError: func(error) { first.Add(1) }}})
	b := newTestLogger(t, Config{WriteFile: true, File: FileConfig{File: "app.log", Path: path, OnError: func(error) { second.Add(1) }}})
	a.SetConfig(Config{WriteFile: true, File: FileConfig{OnError: func(error) { updated.Add(1) }}})
	sharedFiles.report(b.core.handler.state.lease.entry, errors.New("test maintenance"))
	if first.Load() != 0 || second.Load() != 1 || updated.Load() != 1 {
		t.Fatal("callbacks not isolated")
	}
	alias := filepath.Join(path, "alias.log")
	err := os.Link(filepath.Join(path, "app.log"), alias)
	if err != nil {
		t.Fatal(err)
	}
	_, err = New(Config{WriteFile: true, File: FileConfig{Path: path, File: "alias.log"}})
	if err == nil {
		t.Fatal("hard link bypassed manager")
	}
}

func TestConcurrentReopenAndRelease(t *testing.T) {
	path := t.TempDir()
	var wg sync.WaitGroup
	for range 4 {
		wg.Go(func() {
			for range 40 {
				logger, err := New(Config{WriteFile: true, File: FileConfig{File: "app.log", Path: path}})
				if err != nil {
					t.Error(err)
					return
				}
				logger.Info(t.Context(), "reopen")
				err = logger.Close()
				if err != nil {
					t.Error(err)
					return
				}
			}
		})
	}
	wg.Wait()
	if strings.Count(logContents(t, path), "INFO reopen\n") != 160 {
		t.Fatal("reopen lost records")
	}
	sharedFiles.mu.Lock()
	remaining := len(sharedFiles.files)
	sharedFiles.mu.Unlock()
	if remaining != 0 {
		t.Fatalf("leaked %d files", remaining)
	}
}
