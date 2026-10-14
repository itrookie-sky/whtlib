package logx

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"runtime/debug"
	"strings"
	"time"
)

type CtxKey string

// Logger 提供必须传入 ctx 的日志方法，各实例独立保存配置与关闭状态。
// 打印方法不返回错误，编码和写入错误连同调用堆栈直接输出到 stderr。
type Logger struct {
	core *loggerCore
	skip int
}

// New 创建日志实例，默认输出到终端，WriteFile 为 true 时获取共享文件资源。
func New(config Config) (logger *Logger, err error) {
	logger = &Logger{core: &loggerCore{config: config}}
	err = logger.core.initialize()
	if err != nil {
		return nil, err
	}
	return
}

// SetConfig 更新当前实例并返回原实例；WriteFile 始终生效，其他未填写项沿用旧值。
// 更新失败时保留旧配置，并向 stderr 输出错误和调用堆栈。
func (l *Logger) SetConfig(config Config) (logger *Logger) {
	err := l.core.setConfig(config)
	if err != nil {
		_, _ = fmt.Fprintf(os.Stderr, "logx: %v\n%s", err, debug.Stack())
	}
	return l
}

// Skip 返回额外跳过 n 层业务调用的独立实例，不改变原实例，调用方需单独 Close。
func (l *Logger) Skip(n int) (logger *Logger) {
	l.core.mu.RLock()
	defer l.core.mu.RUnlock()
	baseSkip := l.core.config.Skip
	if n < 0 || n > int(^uint(0)>>1)-l.skip || baseSkip > int(^uint(0)>>1)-l.skip-n {
		panic("logx: invalid skip")
	}
	config, err := BuildDefaultConfig(l.core.config)
	if err != nil {
		panic(err)
	}
	clone := &Logger{core: &loggerCore{config: config, closed: l.core.closed}, skip: l.skip + n}
	if l.core.handler != nil && !l.core.closed {
		var lease *fileLease
		if l.core.handler.state.lease != nil {
			lease = sharedFiles.retain(l.core.handler.state.lease)
		}
		clone.core.handler = &handler{config: config, state: &outputState{writer: l.core.handler.state.writer, lease: lease}}
	}
	return clone
}

// Close 仅关闭当前实例，最后一个引用释放时关闭对应文件。
func (l *Logger) Close() (err error) {
	return l.core.close()
}

type outputMode uint8

const (
	outputPlain outputMode = iota
	outputFormatted
	outputStructured
)

func (l *Logger) output(ctx context.Context, level slog.Level, message string, args []any, mode outputMode) {
	if isNil(ctx) {
		panic("logx: nil context")
	}
	err := l.core.initialize()
	if err != nil {
		_, _ = fmt.Fprintf(os.Stderr, "logx: %v\n%s", err, debug.Stack())
		return
	}
	l.core.mu.RLock()
	defer l.core.mu.RUnlock()
	if l.core.closed {
		_, _ = fmt.Fprintf(os.Stderr, "logx: %v\n%s", os.ErrClosed, debug.Stack())
		return
	}
	h := l.core.handler
	if !h.Enabled(ctx, level) {
		return
	}
	var pc uintptr
	if l.skip <= int(^uint(0)>>1)-l.core.config.Skip {
		pc = callerPC(l.skip + l.core.config.Skip)
	}
	switch mode {
	case outputFormatted:
		message = fmt.Sprintf(message, args...)
	case outputPlain:
		if len(args) > 0 {
			values := make([]any, 0, len(args)+1)
			if message != "" {
				values = append(values, message)
			}
			values = append(values, args...)
			message = strings.TrimSuffix(fmt.Sprintln(values...), "\n")
		}
	}
	record := slog.NewRecord(time.Now(), level, message, pc)
	if mode == outputStructured {
		record.Add(args...)
	}
	err = h.Handle(ctx, record)
	if err != nil {
		// 直接写 stderr，避免故障 Writer 导致递归记录。
		_, _ = fmt.Fprintf(os.Stderr, "logx: %v\n%s", err, debug.Stack())
	}
}

// Log 按指定级别输出消息，可变参数以空格拼接到正文。
func (l *Logger) Log(ctx context.Context, level slog.Level, message string, args ...any) {
	l.output(ctx, level, message, args, outputPlain)
}

// Debug 输出调试日志。
func (l *Logger) Debug(ctx context.Context, message string, args ...any) {
	l.output(ctx, slog.LevelDebug, message, args, outputPlain)
}

// Info 输出普通日志。
func (l *Logger) Info(ctx context.Context, message string, args ...any) {
	l.output(ctx, slog.LevelInfo, message, args, outputPlain)
}

// Warn 输出警告日志。
func (l *Logger) Warn(ctx context.Context, message string, args ...any) {
	l.output(ctx, slog.LevelWarn, message, args, outputPlain)
}

// Error 输出错误日志。
func (l *Logger) Error(ctx context.Context, message string, args ...any) {
	l.output(ctx, slog.LevelError, message, args, outputPlain)
}

// Debugf 输出格式化调试日志。
func (l *Logger) Debugf(ctx context.Context, format string, args ...any) {
	l.output(ctx, slog.LevelDebug, format, args, outputFormatted)
}

// Infof 输出格式化普通日志。
func (l *Logger) Infof(ctx context.Context, format string, args ...any) {
	l.output(ctx, slog.LevelInfo, format, args, outputFormatted)
}

// Warnf 输出格式化警告日志。
func (l *Logger) Warnf(ctx context.Context, format string, args ...any) {
	l.output(ctx, slog.LevelWarn, format, args, outputFormatted)
}

// Errorf 输出格式化错误日志。
func (l *Logger) Errorf(ctx context.Context, format string, args ...any) {
	l.output(ctx, slog.LevelError, format, args, outputFormatted)
}
