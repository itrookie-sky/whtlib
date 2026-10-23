package logx

import (
	"context"
	"log/slog"

	"github.com/itrookie-sky/whtlib/adapter"
)

var _ adapter.Logger = (*loggerAdapter)(nil)

// loggerAdapter 借用日志后端，Skip 仅保存打印视图，不派生文件引用。
type loggerAdapter struct {
	logger *Logger
	skip   int
}

// NewLoggerAdapter 将 logx.Logger 适配为共享日志接口；nil 使用 Default。
// 适配器和 Skip 视图不接管后端生命周期，创建方仍负责关闭原 Logger。
func NewLoggerAdapter(logger *Logger) (result adapter.Logger) {
	if logger == nil {
		logger = Default()
	}
	return &loggerAdapter{logger: logger}
}

func (a *loggerAdapter) Skip(n int) (logger adapter.Logger) {
	if n < 0 || n > int(^uint(0)>>1)-a.skip {
		panic("logx: invalid skip")
	}
	return &loggerAdapter{logger: a.logger, skip: a.skip + n}
}

func (a *loggerAdapter) output(ctx context.Context, level slog.Level, message string, args []any, mode outputMode) {
	// 仅构造借用视图；额外补偿本方法一层，饱和时不记录源码位置。
	maxSkip := int(^uint(0) >> 1)
	totalSkip := maxSkip
	if a.skip < maxSkip && a.logger.skip <= maxSkip-a.skip-1 {
		totalSkip = a.logger.skip + a.skip + 1
	}
	view := Logger{core: a.logger.core, skip: totalSkip}
	view.output(ctx, level, message, args, mode)
}

func (a *loggerAdapter) Debug(ctx context.Context, message string, args ...any) {
	a.output(ctx, slog.LevelDebug, message, args, outputPlain)
}

func (a *loggerAdapter) Info(ctx context.Context, message string, args ...any) {
	a.output(ctx, slog.LevelInfo, message, args, outputPlain)
}

func (a *loggerAdapter) Warn(ctx context.Context, message string, args ...any) {
	a.output(ctx, slog.LevelWarn, message, args, outputPlain)
}

func (a *loggerAdapter) Error(ctx context.Context, message string, args ...any) {
	a.output(ctx, slog.LevelError, message, args, outputPlain)
}

func (a *loggerAdapter) Debugf(ctx context.Context, format string, args ...any) {
	a.output(ctx, slog.LevelDebug, format, args, outputFormatted)
}

func (a *loggerAdapter) Infof(ctx context.Context, format string, args ...any) {
	a.output(ctx, slog.LevelInfo, format, args, outputFormatted)
}

func (a *loggerAdapter) Warnf(ctx context.Context, format string, args ...any) {
	a.output(ctx, slog.LevelWarn, format, args, outputFormatted)
}

func (a *loggerAdapter) Errorf(ctx context.Context, format string, args ...any) {
	a.output(ctx, slog.LevelError, format, args, outputFormatted)
}
