package logx

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
)

func captureStdout(t *testing.T, call func()) (output string) {
	t.Helper()
	file, err := os.CreateTemp(t.TempDir(), "stdout-*")
	if err != nil {
		t.Fatal(err)
	}
	previous := os.Stdout
	os.Stdout = file
	defer func() { os.Stdout = previous; _ = file.Close() }()
	call()
	data, err := os.ReadFile(file.Name())
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}

func TestTerminalDefaultAndSkip(t *testing.T) {
	path := filepath.Join(t.TempDir(), "not-created")
	logger, err := New(Config{File: FileConfig{Path: path}})
	if err != nil {
		t.Fatal(err)
	}
	defer logger.Close()
	derived := logger.Skip(0)
	defer derived.Close()
	output := captureStdout(t, func() {
		logger.Info(t.Context(), "terminal")
		_ = logger.Close()
		derived.Info(t.Context(), "independent")
	})
	if !strings.Contains(output, "INFO terminal") || !strings.Contains(output, "INFO independent") {
		t.Fatal(output)
	}
	_, err = os.Stat(path)
	if !errors.Is(err, os.ErrNotExist) {
		t.Fatal("terminal created log directory")
	}
	if derived.core.handler.state.lease != nil {
		t.Fatal("terminal acquired file lease")
	}
}

func TestOutputSwitchAndSharedFile(t *testing.T) {
	path := t.TempDir()
	enabled := true
	a, err := New(Config{WriteFile: enabled, File: FileConfig{Path: path}})
	if err != nil {
		t.Fatal(err)
	}
	defer a.Close()
	b := testSkip(t, a, 0)
	enabled = false
	if !a.core.config.WriteFile {
		t.Fatal("caller mutated config")
	}
	writer := b.core.handler.state.lease.entry.writer
	a.SetConfig(Config{WriteFile: true})
	if a.core.handler.state.lease == nil {
		t.Fatal("explicit true disabled file")
	}
	output := captureStdout(t, func() {
		a.SetConfig(Config{WriteFile: false}).Info(t.Context(), "terminal only")
		b.Info(t.Context(), "file only")
	})
	if !strings.Contains(output, "terminal only") || strings.Contains(output, "file only") {
		t.Fatal(output)
	}
	data := logContents(t, path)
	if strings.Contains(data, "terminal only") || !strings.Contains(data, "file only") {
		t.Fatal(data)
	}
	_ = b.Close()
	select {
	case <-writer.done:
	default:
		t.Fatal("file not released")
	}
	a.SetConfig(Config{WriteFile: true}).Info(t.Context(), "file again")
	if !strings.Contains(logContents(t, path), "file again") {
		t.Fatal("could not reenable")
	}
	blocked := filepath.Join(path, "app.log")
	a.SetConfig(Config{File: FileConfig{Path: blocked}})
	if a.core.config.WriteFile || a.core.handler.state.lease != nil {
		t.Fatal("omitted bool did not select terminal")
	}
	diagnostic := captureStderr(t, func() { a.SetConfig(Config{WriteFile: true}) })
	if diagnostic == "" || a.core.config.WriteFile {
		t.Fatal("failed switch did not retain terminal")
	}
}

func TestConcurrentTerminalInstances(t *testing.T) {
	output := captureStdout(t, func() {
		var wg sync.WaitGroup
		for range 4 {
			wg.Go(func() {
				logger, err := New(Config{WriteFile: false})
				if err != nil {
					t.Error(err)
					return
				}
				defer logger.Close()
				for range 25 {
					logger.Info(t.Context(), "complete record")
				}
			})
		}
		wg.Wait()
	})
	if strings.Count(output, "INFO complete record\n") != 100 {
		t.Fatal("lost or split terminal records")
	}
}
