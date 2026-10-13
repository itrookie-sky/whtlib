package logx

import (
	"bytes"
	"context"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestSetConfigRetainsValuesAndIdentity(t *testing.T) {
	var buffer bytes.Buffer
	logger := newTestLogger(t, Config{
		Writer: &buffer, TraceKey: testKey(1),
		ContextFields: []ContextField{{Name: "userId", Key: testKey(2)}},
	})
	derived := logger.Skip(0)
	if logger.SetConfig(Config{Level: slog.LevelDebug}) != logger {
		t.Fatal("SetConfig returned a different instance")
	}
	ctx := context.WithValue(t.Context(), testKey(1), "trace")
	ctx = context.WithValue(ctx, testKey(2), 42)
	derived.Debug(ctx, "updated")
	if !strings.Contains(buffer.String(), `{"traceId":"trace","userId":42}`) || !strings.Contains(buffer.String(), "DEBUG updated") {
		t.Fatalf("omitted config lost or derived instance stale: %s", buffer.String())
	}
	buffer.Reset()
	logger.SetConfig(Config{}).Debug(ctx, "retained")
	if !strings.Contains(buffer.String(), `"userId":42`) {
		t.Fatal("empty config reset fields")
	}
	buffer.Reset()
	logger.SetConfig(Config{ContextFields: []ContextField{}}).Info(ctx, "cleared")
	if !strings.Contains(buffer.String(), `{"traceId":"trace"}`) || strings.Contains(buffer.String(), "userId") {
		t.Fatalf("explicit empty fields not cleared: %s", buffer.String())
	}
	fields := []ContextField{{Name: "userId", Key: testKey(2)}}
	logger.SetConfig(Config{ContextFields: fields})
	fields[0].Name = "mutated"
	buffer.Reset()
	logger.Info(ctx, "copied")
	if strings.Contains(buffer.String(), "mutated") {
		t.Fatal("config fields not copied")
	}
}

func TestSetConfigFailureKeepsPrevious(t *testing.T) {
	var buffer bytes.Buffer
	logger := newTestLogger(t, Config{Writer: &buffer})
	diagnostic := captureStderr(t, func() {
		if logger.SetConfig(Config{TraceKey: []string{"invalid"}}) != logger {
			t.Fatal("failure changed identity")
		}
	})
	if !strings.Contains(diagnostic, "invalid trace key") || !strings.Contains(diagnostic, "goroutine ") {
		t.Fatalf("missing config error diagnostic: %s", diagnostic)
	}
	logger.Info(t.Context(), "still usable")
	if !strings.Contains(buffer.String(), "still usable") {
		t.Fatal("previous writer lost")
	}
	path := t.TempDir()
	fileLogger := newTestLogger(t, Config{File: FileConfig{Path: path}})
	oldHandler := fileLogger.core.handler
	blocked := filepath.Join(path, "not-a-directory")
	err := os.WriteFile(blocked, []byte("occupied"), 0o600)
	if err != nil {
		t.Fatal(err)
	}
	diagnostic = captureStderr(t, func() { fileLogger.SetConfig(Config{File: FileConfig{Path: blocked}}) })
	if diagnostic == "" || fileLogger.core.handler != oldHandler {
		t.Fatal("failed file initialization replaced active config")
	}
	fileLogger.Info(t.Context(), "old file")
	if !strings.Contains(readLogFiles(t, path)["app.log"], "old file") {
		t.Fatal("old file no longer usable")
	}
}

func TestSetConfigWriterSwitchAndClose(t *testing.T) {
	oldPath := t.TempDir()
	newPath := t.TempDir()
	logger := newTestLogger(t, Config{File: FileConfig{Path: oldPath}})
	oldWriter := logger.core.handler.state.owned
	logger.Info(t.Context(), "old")
	logger.SetConfig(Config{File: FileConfig{Path: newPath}}).Info(t.Context(), "new")
	select {
	case <-oldWriter.done:
	default:
		t.Fatal("retired maintenance did not stop")
	}
	if !strings.Contains(readLogFiles(t, oldPath)["app.log"], "INFO old") || !strings.Contains(readLogFiles(t, newPath)["app.log"], "INFO new") {
		t.Fatal("file switch lost records")
	}
	owned := logger.core.handler.state.owned
	var first, second closingBuffer
	logger.SetConfig(Config{Writer: &first}).Info(t.Context(), "external one")
	select {
	case <-owned.done:
	default:
		t.Fatal("owned writer not closed when replaced")
	}
	logger.SetConfig(Config{Writer: &second}).Info(t.Context(), "external two")
	if first.closed || second.closed || !strings.Contains(first.String(), "external one") || !strings.Contains(second.String(), "external two") {
		t.Fatal("external writer ownership or routing changed")
	}
	_ = logger.Close()
	diagnostic := captureStderr(t, func() { logger.SetConfig(Config{Writer: io.Discard}) })
	if !strings.Contains(diagnostic, os.ErrClosed.Error()) || second.closed {
		t.Fatal("closed logger reopened or external writer closed")
	}
}

func TestSetConfigUpdatesSameFileStrategy(t *testing.T) {
	path := t.TempDir()
	logger := newTestLogger(t, Config{File: FileConfig{
		Path: path, RotateSize: 1 << 20, RotateInterval: -1, RotateCheckInterval: time.Hour,
	}})
	writer := logger.core.handler.state.owned
	logger.Info(t.Context(), "before")
	logger.SetConfig(Config{File: FileConfig{RotateSize: 1, RotateBackupExpire: time.Hour}})
	if logger.core.handler.state.owned != writer {
		t.Fatal("same path opened a second writer")
	}
	logger.Info(t.Context(), "after")
	if len(readLogFiles(t, path)) != 2 {
		t.Fatal("updated size threshold did not rotate")
	}
	writer.mu.Lock()
	config := writer.config
	writer.mu.Unlock()
	if config.Path != path || config.RotateInterval != -1 || config.RotateBackupExpire != time.Hour {
		t.Fatalf("nested file config not merged: %#v", config)
	}
}

func TestSetConfigRefreshesMaintenanceInterval(t *testing.T) {
	path := t.TempDir()
	logger := newTestLogger(t, Config{File: FileConfig{Path: path, RotateInterval: -1, RotateCheckInterval: time.Hour}})
	logger.Info(t.Context(), "idle")
	logger.SetConfig(Config{File: FileConfig{RotateInterval: 10 * time.Millisecond, RotateCheckInterval: 5 * time.Millisecond}})
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if len(readLogFiles(t, path)) == 2 {
			return
		}
		time.Sleep(2 * time.Millisecond)
	}
	t.Fatal("maintenance interval did not update")
}

func TestSetConfigConcurrentPrinting(t *testing.T) {
	var buffer bytes.Buffer
	logger := newTestLogger(t, Config{Writer: &buffer})
	var wg sync.WaitGroup
	for range 4 {
		wg.Go(func() {
			for range 100 {
				logger.Info(t.Context(), "concurrent")
			}
		})
	}
	for range 2 {
		wg.Go(func() {
			for range 30 {
				logger.SetConfig(Config{Level: slog.LevelDebug})
			}
		})
	}
	wg.Wait()
	if strings.Count(buffer.String(), "INFO concurrent\n") != 400 {
		t.Fatal("reconfiguration lost or split records")
	}
}

func TestDefaultSingletonAndCallers(t *testing.T) {
	// 本测试独占包级单例，结束后恢复，不与其他测试并行执行。
	previous := defaultLogger
	defaultLogger = &Logger{core: &loggerCore{}}
	t.Cleanup(func() { _ = Close(); defaultLogger = previous })
	if Default() != Default() || Default().core.handler != nil {
		t.Fatal("default is not a lazy singleton")
	}
	var buffer bytes.Buffer
	if SetConfig(Config{Writer: &buffer, Level: slog.LevelDebug}) != Default() {
		t.Fatal("global config returned another logger")
	}
	_, file, line, _ := runtime.Caller(0)
	Info(t.Context(), "global")
	if !strings.Contains(buffer.String(), filepath.Base(file)+":"+strconv.Itoa(line+1)+" INFO global") {
		t.Fatalf("global wrapper moved caller: %s", buffer.String())
	}
	buffer.Reset()
	_, _, line, _ = runtime.Caller(0)
	Default().Info(t.Context(), "singleton")
	if !strings.Contains(buffer.String(), filepath.Base(file)+":"+strconv.Itoa(line+1)+" INFO singleton") {
		t.Fatalf("singleton caller incorrect: %s", buffer.String())
	}
	buffer.Reset()
	for _, output := range []func(context.Context, string, ...any){Debug, Info, Warn, Error, Debugf, Infof, Warnf, Errorf} {
		output(t.Context(), "global method")
	}
	Log(t.Context(), slog.LevelInfo, "custom level")
	Skip(0).Info(t.Context(), "skip")
	if strings.Count(buffer.String(), "\n") != 10 {
		t.Fatal("missing global output wrapper")
	}
}

func TestSetConfigConcurrentClose(t *testing.T) {
	logger := newTestLogger(t, Config{Writer: io.Discard})
	captureStderr(t, func() {
		var wg sync.WaitGroup
		for range 4 {
			wg.Go(func() {
				for range 10 {
					logger.SetConfig(Config{Level: slog.LevelDebug})
					logger.Info(t.Context(), "concurrent close")
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
		t.Fatal("reconfiguration reopened closed instance")
	}
}
