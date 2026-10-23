package adapter

import (
	"context"
	"time"
)

// LogAcc 定义累计字段和结构化打印的共享契约。
// 实现应支持并发操作；独立调用的累加器由使用方通过构造函数创建。
// 字段值中的引用对象由调用方同步，日志后端的生命周期由注入方管理。
type LogAcc interface {
	// SetStartTime 设置打印耗时的起点；零时间关闭耗时前缀。
	SetStartTime(start time.Time) (acc LogAcc)
	// SetValue 覆盖单个字段并返回同一累加器。
	SetValue(key string, value any) (acc LogAcc)
	// Update 合并字段并返回同一累加器。
	Update(values map[string]any) (acc LogAcc)
	// Skip 累加需要跳过的调用层数。
	Skip(n int) (acc LogAcc)
	// SetErr 读取当前错误并累计；不保留 error 指针。
	SetErr(value *error) (acc LogAcc)
	// Debug 合并键值对并输出调试日志，ctx 必须非 nil。
	Debug(ctx context.Context, message string, args ...any)
	// Info 合并键值对并输出普通日志，ctx 必须非 nil。
	Info(ctx context.Context, message string, args ...any)
	// Warn 合并键值对并输出警告日志，ctx 必须非 nil。
	Warn(ctx context.Context, message string, args ...any)
	// Error 合并键值对并输出错误日志，ctx 必须非 nil。
	Error(ctx context.Context, message string, args ...any)
}
