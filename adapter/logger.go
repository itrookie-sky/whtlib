// Package adapter 定义供多个包复用的通用适配契约。
// 仅单个组件使用的接口和适配实现应放在该组件内部。
package adapter

import (
	"context"
	"log/slog"
)

// Logger 定义与 logx 一致的日志打印接口。
//
// 打印方法必须传入非 nil 的 ctx，且不返回错误。
// 普通方法将 message 与 args 按空格拼接为正文，不将 args 解析为键值对；
// 空 message 不产生前导空格，没有 args 时保留 message。
// 带 f 的方法遵循 fmt.Sprintf 格式化语义。
// 实现应支持并发打印；配置与关闭由实例创建方管理。
type Logger interface {
	// Skip 返回额外跳过 n 层业务调用的实例，资源生命周期遵循具体实现约定。
	Skip(n int) Logger
	// Log 按指定级别输出日志。
	Log(ctx context.Context, level slog.Level, message string, args ...any)
	// Debug 输出调试日志。
	Debug(ctx context.Context, message string, args ...any)
	// Info 输出普通日志。
	Info(ctx context.Context, message string, args ...any)
	// Warn 输出警告日志。
	Warn(ctx context.Context, message string, args ...any)
	// Error 输出错误日志。
	Error(ctx context.Context, message string, args ...any)
	// Debugf 输出格式化调试日志。
	Debugf(ctx context.Context, format string, args ...any)
	// Infof 输出格式化普通日志。
	Infof(ctx context.Context, format string, args ...any)
	// Warnf 输出格式化警告日志。
	Warnf(ctx context.Context, format string, args ...any)
	// Errorf 输出格式化错误日志。
	Errorf(ctx context.Context, format string, args ...any)
}
