package logx

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"time"
)

// Logger 提供必须传入 ctx 的日志方法，派生实例共享输出与关闭状态。
// 打印方法与 slog 一致，不返回或传播编码和写入错误。
type Logger struct {
	handler *handler
	skip    int
}

// New 创建日志实例，未指定 Writer 时创建内置文件 Writer。
func New(options Options) (logger *Logger, err error) {
	options, err = BuildDefaultOptions(options)
	if err != nil {
		return
	}
	state := &outputState{writer: options.Writer}
	if state.writer == nil {
		state.owned, err = NewFileWriter(options.File)
		if err != nil {
			return
		}
		state.writer = state.owned
	}
	logger = &Logger{handler: &handler{state: state, options: options}, skip: options.Skip}
	return
}

// Skip 返回额外跳过 n 层业务调用的实例，不改变原实例。
func (l *Logger) Skip(n int) (logger *Logger) {
	if n < 0 || n > int(^uint(0)>>1)-l.skip {
		panic("logx: invalid skip")
	}
	clone := *l
	clone.skip += n
	return &clone
}

// Close 关闭共享实例体系，不关闭调用方传入的 Writer。
func (l *Logger) Close() (err error) {
	state := l.handler.state
	state.close.Do(func() {
		state.mu.Lock()
		state.closed = true
		state.mu.Unlock()
		if state.owned != nil {
			state.closeErr = state.owned.Close()
		}
	})
	return state.closeErr
}

func (l *Logger) output(ctx context.Context, level slog.Level, message string, args []any, formatted bool) (err error) {
	if isNil(ctx) {
		panic("logx: nil context")
	}
	l.handler.state.mu.Lock()
	closed := l.handler.state.closed
	l.handler.state.mu.Unlock()
	if closed {
		return os.ErrClosed
	}
	if !l.handler.Enabled(ctx, level) {
		return
	}
	pc := callerPC(l.skip)
	if formatted {
		message = fmt.Sprintf(message, args...)
	}
	record := slog.NewRecord(time.Now(), level, message, pc)
	if !formatted {
		record.Add(args...)
	}
	return l.handler.Handle(ctx, record)
}

// Log 按指定级别输出消息和结构化属性。
func (l *Logger) Log(ctx context.Context, level slog.Level, message string, args ...any) {
	_ = l.output(ctx, level, message, args, false)
}

// Debug 输出调试日志。
func (l *Logger) Debug(ctx context.Context, message string, args ...any) {
	_ = l.output(ctx, slog.LevelDebug, message, args, false)
}

// Info 输出普通日志。
func (l *Logger) Info(ctx context.Context, message string, args ...any) {
	_ = l.output(ctx, slog.LevelInfo, message, args, false)
}

// Warn 输出警告日志。
func (l *Logger) Warn(ctx context.Context, message string, args ...any) {
	_ = l.output(ctx, slog.LevelWarn, message, args, false)
}

// Error 输出错误日志。
func (l *Logger) Error(ctx context.Context, message string, args ...any) {
	_ = l.output(ctx, slog.LevelError, message, args, false)
}

// Debugf 输出格式化调试日志。
func (l *Logger) Debugf(ctx context.Context, format string, args ...any) {
	_ = l.output(ctx, slog.LevelDebug, format, args, true)
}

// Infof 输出格式化普通日志。
func (l *Logger) Infof(ctx context.Context, format string, args ...any) {
	_ = l.output(ctx, slog.LevelInfo, format, args, true)
}

// Warnf 输出格式化警告日志。
func (l *Logger) Warnf(ctx context.Context, format string, args ...any) {
	_ = l.output(ctx, slog.LevelWarn, format, args, true)
}

// Errorf 输出格式化错误日志。
func (l *Logger) Errorf(ctx context.Context, format string, args ...any) {
	_ = l.output(ctx, slog.LevelError, format, args, true)
}
