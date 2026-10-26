package errx

import "runtime"

// Frame 表示符号化调用帧，可用于日志和 JSON。
type Frame struct {
	Function string `json:"function"` // 完整函数名。
	File     string `json:"file"`     // 源文件路径。
	Line     int    `json:"line"`     // 源文件行号。
}

type stackSource interface{ StackTrace() []Frame }

// Stack 返回错误树中首个非空堆栈的副本，查找顺序与 Walk 一致。
// 支持实现 StackTrace() []Frame 的外部错误，未找到时返回 nil。
func Stack(err error) (frames []Frame) {
	for current := range Walk(err) {
		frames = stackOf(current)
		if len(frames) > 0 {
			return
		}
	}
	return nil
}

func captureStack() (pcs []uintptr) {
	pcs = make([]uintptr, maxStackDepth)
	n := runtime.Callers(stackCallerSkip, pcs)
	return pcs[:n:n]
}

func stackFrames(pcs []uintptr) (frames []Frame) {
	if len(pcs) == 0 {
		return
	}
	iterator := runtime.CallersFrames(pcs)
	for {
		frame, more := iterator.Next()
		frames = append(frames, Frame{Function: frame.Function, File: frame.File, Line: frame.Line})
		if !more {
			return
		}
	}
}
