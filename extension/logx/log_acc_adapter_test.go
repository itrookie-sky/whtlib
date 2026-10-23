package logx

import (
	"bytes"
	"errors"
	"log/slog"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestLogAccNewInstanceAndStartTime(t *testing.T) {
	var buffer bytes.Buffer
	logger := newBufferedLogger(t, &buffer, Config{})
	start := time.Now().Add(-time.Second)
	first := NewLogAcc(WithLogger(logger), WithCost(start)).SetValue("source", "first").Skip(2)
	second := NewLogAcc(WithLogger(logger)).SetValue("source", "second").Skip(1)
	fields, backend, firstStart, skip := first.snapshot(nil)
	if fields["source"] != "first" || len(fields) != 1 || backend != logger || firstStart != start || skip != 2 {
		t.Fatalf("first instance changed: %v, %v, %d", fields, firstStart, skip)
	}
	fields, backend, secondStart, skip := second.snapshot(nil)
	if fields["source"] != "second" || len(fields) != 1 || backend != logger || !secondStart.IsZero() || skip != 1 {
		t.Fatalf("second instance shared state: %v, %v, %d", fields, secondStart, skip)
	}
	acc := NewLogAcc(WithLogger(logger))
	if acc.SetStartTime(start) != acc {
		t.Fatal("SetStartTime must return receiver")
	}
	acc.Info(t.Context(), "timed")
	if !strings.Contains(buffer.String(), "] timed") {
		t.Fatal(buffer.String())
	}
	buffer.Reset()
	acc.SetStartTime(time.Time{}).Info(t.Context(), "untimed")
	if !strings.Contains(buffer.String(), " untimed\n") || strings.Contains(buffer.String(), "] untimed") {
		t.Fatal(buffer.String())
	}
}

func TestLogAccAdapterCallerAndChain(t *testing.T) {
	var buffer bytes.Buffer
	logger := newBufferedLogger(t, &buffer, Config{Level: slog.LevelDebug})
	acc := NewLogAccAdapter(WithLogger(logger))
	if acc.SetValue("a", 1) != acc || acc.Update(map[string]any{"b": 2}) != acc || acc.SetStartTime(time.Time{}) != acc || acc.Skip(0) != acc {
		t.Fatal("adapter methods must retain chain identity")
	}
	err := errors.New("example")
	if acc.SetErr(&err) != acc {
		t.Fatal("SetErr must return receiver")
	}
	_, file, line, _ := runtime.Caller(0)
	acc.Info(t.Context(), "adapted")
	if !strings.Contains(buffer.String(), filepath.Base(file)+":"+strconv.Itoa(line+1)+" adapted") {
		t.Fatal(buffer.String())
	}
	buffer.Reset()
	acc.Debug(t.Context(), "debug")
	acc.Warn(t.Context(), "warn")
	acc.Error(t.Context(), "error")
	if strings.Count(buffer.String(), "\n") != 3 || !strings.Contains(buffer.String(), `"err":"example"`) {
		t.Fatal(buffer.String())
	}
}

func TestLogAccConcurrentStartAndOutput(t *testing.T) {
	var buffer bytes.Buffer
	logger := newBufferedLogger(t, &buffer, Config{})
	acc := NewLogAcc(WithLogger(logger))
	var wg sync.WaitGroup
	for worker := range 4 {
		wg.Go(func() {
			for range 20 {
				acc.SetStartTime(time.Now()).SetValue("worker", worker)
				requestAcc := NewLogAcc(WithLogger(logger))
				requestAcc.SetStartTime(time.Time{}).Info(t.Context(), "request")
				acc.Info(t.Context(), "source")
			}
		})
	}
	wg.Wait()
	if strings.Count(buffer.String(), "\n") != 160 {
		t.Fatal("lost concurrent records")
	}
}
