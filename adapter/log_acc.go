package adapter

import (
	"context"
	"time"
)

// LogAcc 定义累计字段和结构化打印的共享契约。
// 实现应支持并发操作；独立调用的累加器由使用方通过构造函数创建。
// 打印参数均为正文，结构化字段通过 SetValue、Update 或 SetErr 累计。
// 字段值和打印参数中的引用对象由调用方同步，日志后端的生命周期由注入方管理。
type LogAcc interface {
	// SetStartTime 设置打印耗时的起点；零时间关闭耗时前缀。
	SetStartTime(start time.Time)
	// SetValue 覆盖当前累加器的单个字段。
	SetValue(key string, value any)
	// Update 将字段合并到当前累加器。
	Update(values map[string]any)
	// Skip 累加需要跳过的调用层数。
	Skip(n int)
	// SetErr 读取当前错误并累计；不保留 error 指针。
	SetErr(value *error)
	// Debug 输出正文与累计字段，ctx 必须非 nil。
	Debug(ctx context.Context, args ...any)
	// Info 输出正文与累计字段，ctx 必须非 nil。
	Info(ctx context.Context, args ...any)
	// Warn 输出正文与累计字段，ctx 必须非 nil。
	Warn(ctx context.Context, args ...any)
	// Error 输出正文与累计字段，ctx 必须非 nil。
	Error(ctx context.Context, args ...any)
}
