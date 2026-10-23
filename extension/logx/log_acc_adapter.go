package logx

import (
	"context"
	"time"

	"github.com/itrookie-sky/whtlib/adapter"
)

var _ adapter.LogAcc = (*logAccAdapter)(nil)

// logAccAdapter 保留具体 LogAcc 的链式返回类型，适配共享契约。
type logAccAdapter struct {
	acc *LogAcc
}

// NewLogAccAdapter 创建共享日志累加器，支持 WithLogger 和 WithCost。
// 未传 Logger 时使用全局实例；包装器不接管日志后端生命周期。
func NewLogAccAdapter(opts ...LogAccOptionFunc) (acc adapter.LogAcc) {
	return &logAccAdapter{acc: NewLogAcc(opts...).Skip(1)}
}

func (a *logAccAdapter) SetStartTime(start time.Time) (acc adapter.LogAcc) {
	a.acc.SetStartTime(start)
	return a
}

func (a *logAccAdapter) SetValue(key string, value any) (acc adapter.LogAcc) {
	a.acc.SetValue(key, value)
	return a
}

func (a *logAccAdapter) Update(values map[string]any) (acc adapter.LogAcc) {
	a.acc.Update(values)
	return a
}

func (a *logAccAdapter) Skip(n int) (acc adapter.LogAcc) {
	a.acc.Skip(n)
	return a
}

func (a *logAccAdapter) SetErr(value *error) (acc adapter.LogAcc) {
	a.acc.SetErr(value)
	return a
}

func (a *logAccAdapter) Debug(ctx context.Context, message string, args ...any) {
	a.acc.Debug(ctx, message, args...)
}

func (a *logAccAdapter) Info(ctx context.Context, message string, args ...any) {
	a.acc.Info(ctx, message, args...)
}

func (a *logAccAdapter) Warn(ctx context.Context, message string, args ...any) {
	a.acc.Warn(ctx, message, args...)
}

func (a *logAccAdapter) Error(ctx context.Context, message string, args ...any) {
	a.acc.Error(ctx, message, args...)
}
