// Package adapter 定义供多个包复用的通用适配契约。
// 仅单个组件使用的接口和适配实现应放在该组件内部。
package adapter

import (
	"context"
)

// Logger 定义多个组件共享的日志打印与调用位置契约。
//
// 打印方法必须传入非 nil 的 ctx，且不返回错误。
// 普通方法将全部 args 按空格拼接为正文，不将 args 解析为键值对；
// 可以不传参数，首个参数无需为字符串。
// 带 f 的方法遵循 fmt.Sprintf 格式化语义。
// 实现应支持并发打印；配置与关闭由实例创建方管理。
type Logger interface {
	// Skip 返回额外跳过 n 层调用的借用视图，不修改原实例或接管后端资源。
	Skip(n int) Logger
	// Debug 输出调试日志。
	Debug(ctx context.Context, args ...any)
	// Info 输出普通日志。
	Info(ctx context.Context, args ...any)
	// Warn 输出警告日志。
	Warn(ctx context.Context, args ...any)
	// Error 输出错误日志。
	Error(ctx context.Context, args ...any)
	// Debugf 输出格式化调试日志。
	Debugf(ctx context.Context, format string, args ...any)
	// Infof 输出格式化普通日志。
	Infof(ctx context.Context, format string, args ...any)
	// Warnf 输出格式化警告日志。
	Warnf(ctx context.Context, format string, args ...any)
	// Errorf 输出格式化错误日志。
	Errorf(ctx context.Context, format string, args ...any)
}
