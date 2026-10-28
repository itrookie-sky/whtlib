package logx

import (
	"bytes"
	"context"
	"fmt"
	"log/slog"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/itrookie-sky/whtlib/adapter"
)

// recordingBackend 只实现通用契约，不依赖 logx.Logger。
type recordingBackend struct {
	adapter.Logger
	records *[]backendRecord
	skip    int
}

type backendRecord struct {
	ctx     context.Context
	level   string
	message string
	skip    int
}

func (b *recordingBackend) Skip(n int) (logger adapter.Logger) {
	return &recordingBackend{records: b.records, skip: b.skip + n}
}

func (b *recordingBackend) record(ctx context.Context, level string, args []any) {
	*b.records = append(*b.records, backendRecord{ctx, level, strings.TrimSuffix(fmt.Sprintln(args...), "\n"), b.skip})
}

func (b *recordingBackend) Debug(ctx context.Context, args ...any) {
	b.record(ctx, "debug", args)
}

func (b *recordingBackend) Info(ctx context.Context, args ...any) {
	b.record(ctx, "info", args)
}

func (b *recordingBackend) Warn(ctx context.Context, args ...any) {
	b.record(ctx, "warn", args)
}

func (b *recordingBackend) Error(ctx context.Context, args ...any) {
	b.record(ctx, "error", args)
}

func TestLogAccUsesInjectedInterface(t *testing.T) {
	var records []backendRecord
	backend := &recordingBackend{records: &records}
	acc := NewLogAcc(WithLogger(backend), WithCost(time.Now()))
	acc.Skip(3)
	acc.SetValue("z", 2)
	acc.SetValue("a", 1)
	ctx := context.WithValue(t.Context(), testKey(100), "trace")
	acc.Debug(ctx, "done")
	acc.Info(ctx, "done")
	acc.Warn(ctx, "done")
	acc.Error(ctx, "done")
	for i, level := range []string{"debug", "info", "warn", "error"} {
		record := records[i]
		if record.ctx != ctx || record.level != level || record.skip != 5 || backend.skip != 0 {
			t.Fatalf("backend contract lost: %#v", record)
		}
		if !strings.HasPrefix(record.message, "[") || !strings.HasSuffix(record.message, `] done {"a":1,"z":2}`) {
			t.Fatal(record.message)
		}
	}
	acc.SetStartTime(time.Time{})
	acc.SetValue("z", 3)
	acc.Info(ctx, "next")
	if records[4].message != `next {"a":1,"z":3}` || !strings.Contains(records[0].message, `"z":2`) {
		t.Fatal("field snapshots or start time changed")
	}
}

func TestLoggerAdapterOutputAndCaller(t *testing.T) {
	var buffer bytes.Buffer
	native := newBufferedLogger(t, &buffer, Config{Level: slog.LevelDebug})
	backend := NewLoggerAdapter(native)
	for _, print := range []func(context.Context, ...any){backend.Debug, backend.Info, backend.Warn, backend.Error} {
		print(t.Context(), "hello", 1, "world")
	}
	for _, print := range []func(context.Context, string, ...any){backend.Debugf, backend.Infof, backend.Warnf, backend.Errorf} {
		print(t.Context(), "hello %d %s", 1, "world")
	}
	if strings.Count(buffer.String(), "hello 1 world\n") != 8 {
		t.Fatal(buffer.String())
	}
	buffer.Reset()
	_, file, line, _ := runtime.Caller(0)
	backend.Info(t.Context(), "direct")
	if !strings.Contains(buffer.String(), filepath.Base(file)+":"+strconv.Itoa(line+1)+" direct") {
		t.Fatal(buffer.String())
	}
	buffer.Reset()
	print := func() { backend.Skip(1).Info(t.Context(), "wrapped") }
	_, _, line, _ = runtime.Caller(0)
	print()
	if !strings.Contains(buffer.String(), filepath.Base(file)+":"+strconv.Itoa(line+1)+" wrapped") {
		t.Fatal(buffer.String())
	}
	buffer.Reset()
	_, _, line, _ = runtime.Caller(0)
	backend.Info(t.Context(), "original")
	if !strings.Contains(buffer.String(), filepath.Base(file)+":"+strconv.Itoa(line+1)+" original") {
		t.Fatal("Skip mutated original adapter: ", buffer.String())
	}
}

func TestLogAccBackendFilteringAndTypedNil(t *testing.T) {
	var buffer bytes.Buffer
	native := newBufferedLogger(t, &buffer, Config{Level: slog.LevelInfo})
	diagnostic := captureStderr(t, func() {
		acc := NewLogAcc(WithLogger(NewLoggerAdapter(native)))
		acc.SetValue("bad", make(chan int))
		acc.Debug(t.Context(), "filtered")
	})
	if buffer.Len() != 0 || diagnostic != "" {
		t.Fatalf("filtered fields were encoded: %q %q", buffer.String(), diagnostic)
	}
	previous := defaultLogger
	defaultLogger = native
	t.Cleanup(func() { defaultLogger = previous })
	var typedNil *recordingBackend
	NewLogAcc(WithLogger(typedNil)).Info(t.Context(), "fallback")
	NewLoggerAdapter(nil).Info(t.Context(), "default adapter")
	if !strings.Contains(buffer.String(), "fallback\n") || !strings.Contains(buffer.String(), "default adapter\n") {
		t.Fatal(buffer.String())
	}
}
