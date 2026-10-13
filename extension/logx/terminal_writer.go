package logx

import (
	"errors"
	"io"
	"os"
	"sync"
)

// terminalWriter 串行输出完整日志记录，不拥有标准输出的生命周期。
type terminalWriter struct{ mu sync.Mutex }

var terminalOutput = &terminalWriter{}

func (w *terminalWriter) Write(p []byte) (n int, err error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	return os.Stdout.Write(p)
}

// dualWriter 始终尝试两个输出目标，一端失败不阻止另一端写入。
type dualWriter struct {
	terminal io.Writer
	file     io.Writer
}

func (w *dualWriter) Write(p []byte) (n int, err error) {
	terminalN, terminalErr := w.terminal.Write(p)
	if terminalN != len(p) && terminalErr == nil {
		terminalErr = io.ErrShortWrite
	}
	fileN, fileErr := w.file.Write(p)
	if fileN != len(p) && fileErr == nil {
		fileErr = io.ErrShortWrite
	}
	return min(terminalN, fileN), errors.Join(terminalErr, fileErr)
}
