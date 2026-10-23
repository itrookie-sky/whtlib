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

func TestLogAccCloneAndStartTime(t *testing.T) {
	var buffer bytes.Buffer
	logger := newBufferedLogger(t, &buffer, Config{})
	start := time.Now().Add(-time.Second)
	original := NewLogAcc(WithLogger(logger), WithCost(start)).SetValue("source", "original").Skip(2)
	clone := original.Clone()
	clone.SetValue("source", "clone").SetValue("onlyClone", true).SetStartTime(time.Time{}).Skip(1)
	fields, backend, originStart, skip := original.snapshot(nil)
	if fields["source"] != "original" || len(fields) != 1 || backend != logger || originStart != start || skip != 2 {
		t.Fatalf("original changed: %v, %v, %d", fields, originStart, skip)
	}
	fields, backend, cloneStart, skip := clone.snapshot(nil)
	if fields["source"] != "clone" || len(fields) != 2 || backend != logger || !cloneStart.IsZero() || skip != 3 {
		t.Fatalf("invalid clone: %v, %v, %d", fields, cloneStart, skip)
	}
	var zero LogAcc
	zero.Clone().SetValue("zero", true)
	if len(zero.fields) != 0 {
		t.Fatal("zero-value clone modified source")
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

func TestLogAccAdapterCloneCallerAndChain(t *testing.T) {
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
	for range 5 {
		acc = acc.Clone()
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

func TestLogAccConcurrentCloneStartAndOutput(t *testing.T) {
	var buffer bytes.Buffer
	logger := newBufferedLogger(t, &buffer, Config{})
	acc := NewLogAccAdapter(WithLogger(logger))
	var wg sync.WaitGroup
	for worker := range 4 {
		wg.Go(func() {
			for range 20 {
				acc.SetStartTime(time.Now()).SetValue("worker", worker)
				clone := acc.Clone()
				clone.SetStartTime(time.Time{}).Info(t.Context(), "clone")
				acc.Info(t.Context(), "source")
			}
		})
	}
	wg.Wait()
	if strings.Count(buffer.String(), "\n") != 160 {
		t.Fatal("lost concurrent records")
	}
}
