package logx

import (
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
