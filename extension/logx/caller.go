package logx

import "runtime"

func callerPC(skip int) (pc uintptr) {
	// Callers、callerPC、output 和公开日志方法属于内部层。
	var pcs [1]uintptr
	if skip > int(^uint(0)>>1)-4 {
		return
	}
	if runtime.Callers(4+skip, pcs[:]) == 0 {
		return
	}
	return pcs[0]
}
