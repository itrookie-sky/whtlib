package logx

import (
	"bytes"
	"context"
	"errors"
	"log/slog"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"testing"
)

func testClone(t *testing.T, source *Logger) (cloned *Logger) {
	t.Helper()
	cloned, err := source.Clone()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		closeErr := cloned.Close()
		if closeErr != nil {
			t.Error(closeErr)
		}
	})
	return
}

func TestCloneConfigIsolation(t *testing.T) {
	var level slog.LevelVar
	path := t.TempDir()
	source := newTestLogger(t, Config{File: FileConfig{Path: path, File: "clone.log"}, Level: &level, TraceKey: testKey(1), ContextFields: []ContextField{{Name: "userId", Key: testKey(2)}}})
	level.Set(slog.LevelError)
	cloned := testClone(t, source)
	if cloned == source || cloned.core == source.core || cloned.core.handler == source.core.handler || cloned.core.handler.state == source.core.handler.state {
		t.Fatal("实例状态未独立")
	}
	if cloned.core.config.Level.Level() != slog.LevelInfo {
		t.Fatal("没有继承级别快照")
	}
	if &cloned.core.config.ContextFields[0] == &source.core.config.ContextFields[0] {
		t.Fatal("ContextFields 切片共享")
	}
	cloned.SetConfig(Config{WriteFile: true, Level: slog.LevelDebug, TraceKey: testKey(3), ContextFields: []ContextField{{Name: "childUser", Key: testKey(2)}}})
	ctx := context.WithValue(t.Context(), testKey(1), "parent")
	ctx = context.WithValue(ctx, testKey(3), "child")
	ctx = context.WithValue(ctx, testKey(2), 42)
	source.Debug(ctx, "parent filtered")
	source.Info(ctx, "parent info")
	cloned.Debug(ctx, "child debug")
	source.SetConfig(Config{WriteFile: true, Level: slog.LevelError})
	cloned.Info(ctx, "child still info")
	output := logContents(t, path)
	if strings.Contains(output, "parent filtered") || !strings.Contains(output, `{parent} {"userId":42}`) || !strings.Contains(output, `{child} {"childUser":42}`) || !strings.Contains(output, "child still info") {
		t.Fatal(output)
	}
}

func TestCloneSharedFileLifetime(t *testing.T) {
	path := t.TempDir()
	source := newTestLogger(t, Config{File: FileConfig{Path: path, File: "clone.log"}})
	cloned := testClone(t, source)
	sourceLease := source.core.handler.state.lease
	cloneLease := cloned.core.handler.state.lease
	if sourceLease == cloneLease || sourceLease.entry != cloneLease.entry {
		t.Fatal("文件应共享 Writer 并持有独立引用")
	}
	writer := sourceLease.entry.writer
	err := source.Close()
	if err != nil {
		t.Fatal(err)
	}
	select {
	case <-writer.done:
		t.Fatal("源实例关闭时错误释放 Writer")
	default:
	}
	cloned.Info(t.Context(), "clone after parent close")
	if !strings.Contains(logContents(t, path), "clone after parent close") {
		t.Fatal("源实例关闭影响克隆")
	}
	err = cloned.Close()
	if err != nil {
		t.Fatal(err)
	}
	select {
	case <-writer.done:
	default:
		t.Fatal("最后一个引用未释放 Writer")
	}
	if got, cloneErr := source.Clone(); got != nil || !errors.Is(cloneErr, os.ErrClosed) {
		t.Fatalf("关闭态 Clone = %v, %v", got, cloneErr)
	}
	// 保留既有 Skip 对关闭态实例的行为。
	derived := testSkip(t, source, 0)
	if !derived.core.closed || derived.core.handler != nil {
		t.Fatal("Skip 的关闭态行为改变")
	}
}

func TestCloneCallerAndInheritedSkip(t *testing.T) {
	var buffer bytes.Buffer
	source := newBufferedLogger(t, &buffer, Config{})
	cloned := testClone(t, source)
	_, file, line, _ := runtime.Caller(0)
	cloned.Info(t.Context(), "clone caller")
	if !strings.Contains(buffer.String(), filepath.Base(file)+":"+strconv.Itoa(line+1)+" clone caller") {
		t.Fatal(buffer.String())
	}
	buffer.Reset()
	skipped := testSkip(t, source, 1)
	clonedSkip := testClone(t, skipped)
	_, file, line, _ = runtime.Caller(0)
	cloneLogHelper(clonedSkip, t.Context())
	if !strings.Contains(buffer.String(), filepath.Base(file)+":"+strconv.Itoa(line+1)+" inherited skip") {
		t.Fatal(buffer.String())
	}
}

func cloneLogHelper(logger *Logger, ctx context.Context) { logger.Info(ctx, "inherited skip") }

func TestCloneDefaultAndInvalidSource(t *testing.T) {
	var buffer bytes.Buffer
	previous := defaultLogger
	defaultLogger = newBufferedLogger(t, &buffer, Config{})
	t.Cleanup(func() { defaultLogger = previous })
	cloned, err := Clone()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = cloned.Close() })
	if cloned == Default() || cloned.core == Default().core {
		t.Fatal("全局 Clone 未派生独立实例")
	}
	err = Close()
	if err != nil {
		t.Fatal(err)
	}
	cloned.Info(t.Context(), "global clone")
	if !strings.Contains(buffer.String(), "global clone") {
		t.Fatal(buffer.String())
	}
	if result, cloneErr := Clone(); result != nil || !errors.Is(cloneErr, os.ErrClosed) {
		t.Fatalf("关闭的全局 Clone = %v, %v", result, cloneErr)
	}
	for _, source := range []*Logger{nil, {}} {
		result, cloneErr := source.Clone()
		if result != nil || !errors.Is(cloneErr, ErrInvalidConfig) {
			t.Fatalf("无效源 Clone = %v, %v", result, cloneErr)
		}
	}
}

func TestCloneConcurrentConfig(t *testing.T) {
	path := t.TempDir()
	source := newTestLogger(t, Config{File: FileConfig{Path: path, File: "clone.log"}})
	var group sync.WaitGroup
	group.Go(func() {
		for i := range 40 {
			source.SetConfig(Config{WriteFile: true, Level: slog.Level(i % 2), ContextFields: []ContextField{{Name: "worker", Key: testKey(1)}}})
		}
	})
	for range 4 {
		group.Go(func() {
			for range 20 {
				cloned, err := source.Clone()
				if err != nil {
					t.Error(err)
					return
				}
				cloned.Error(t.Context(), "concurrent clone")
				err = cloned.Close()
				if err != nil {
					t.Error(err)
					return
				}
			}
		})
	}
	group.Wait()
	if count := strings.Count(logContents(t, path), "concurrent clone"); count != 80 {
		t.Fatalf("日志数量 = %d", count)
	}
}

func TestCloneConcurrentClose(t *testing.T) {
	for range 20 {
		source := newTestLogger(t, Config{})
		writer := source.core.handler.state.lease.entry.writer
		start := make(chan struct{})
		var group sync.WaitGroup
		group.Go(func() {
			<-start
			cloned, err := source.Clone()
			if errors.Is(err, os.ErrClosed) {
				return
			}
			if err != nil {
				t.Error(err)
				return
			}
			err = cloned.Close()
			if err != nil {
				t.Error(err)
			}
		})
		group.Go(func() {
			<-start
			err := source.Close()
			if err != nil {
				t.Error(err)
			}
		})
		close(start)
		group.Wait()
		select {
		case <-writer.done:
		default:
			t.Fatal("并发 Clone/Close 泄漏文件引用")
		}
	}
}
