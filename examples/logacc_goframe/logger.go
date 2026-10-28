package main

import (
	"context"
	"fmt"
	"strings"

	"github.com/itrookie-sky/whtlib/adapter"

	"github.com/gogf/gf/v2/os/glog"
)

var _ adapter.Logger = (*goFrameLogger)(nil)

// goFrameLogger 借用已配置的 GoFrame 日志实例，配置与关闭由创建方负责。
type goFrameLogger struct {
	logger *glog.Logger
	skip   int
}

// NewGoFrameLogger 将 GoFrame Logger 转为 adapter.Logger；logger 必须非 nil。
// 应在注入前完成后端配置，避免并发打印期间修改 GoFrame 的配置。
func NewGoFrameLogger(logger *glog.Logger) (backend adapter.Logger) {
	if logger == nil {
		panic("example: nil GoFrame logger")
	}
	return &goFrameLogger{logger: logger}
}

func (l *goFrameLogger) Skip(n int) (backend adapter.Logger) {
	if n < 0 || n > int(^uint(0)>>1)-l.skip {
		panic("example: invalid skip")
	}
	return &goFrameLogger{logger: l.logger, skip: l.skip + n}
}

func (l *goFrameLogger) view() (logger *glog.Logger) {
	baseSkip := l.logger.GetConfig().StSkip
	maxSkip := int(^uint(0) >> 1)
	if baseSkip < 0 || l.skip == maxSkip || baseSkip > maxSkip-l.skip-1 {
		panic("example: invalid GoFrame stack skip")
	}
	// GoFrame 的 Skip 为绝对值且可能修改派生实例，先复制后再设置。
	// 本方法在打印前返回，只需补偿下面各公开打印方法自身的一层。
	return l.logger.Clone().Skip(baseSkip + l.skip + 1)
}

// plainMessage 延迟拼接正文，让 GoFrame 先执行日志级别过滤。
type plainMessage struct {
	args []any
}

func (m plainMessage) String() (message string) {
	return strings.TrimSuffix(fmt.Sprintln(m.args...), "\n")
}

func (l *goFrameLogger) Debug(ctx context.Context, args ...any) {
	l.view().Debug(ctx, plainMessage{args: args})
}

func (l *goFrameLogger) Info(ctx context.Context, args ...any) {
	l.view().Info(ctx, plainMessage{args: args})
}

func (l *goFrameLogger) Warn(ctx context.Context, args ...any) {
	l.view().Warning(ctx, plainMessage{args: args})
}

func (l *goFrameLogger) Error(ctx context.Context, args ...any) {
	l.view().Error(ctx, plainMessage{args: args})
}

func (l *goFrameLogger) Debugf(ctx context.Context, format string, args ...any) {
	l.view().Debugf(ctx, format, args...)
}

func (l *goFrameLogger) Infof(ctx context.Context, format string, args ...any) {
	l.view().Infof(ctx, format, args...)
}

func (l *goFrameLogger) Warnf(ctx context.Context, format string, args ...any) {
	l.view().Warningf(ctx, format, args...)
}

func (l *goFrameLogger) Errorf(ctx context.Context, format string, args ...any) {
	l.view().Errorf(ctx, format, args...)
}
