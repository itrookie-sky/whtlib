package logx

import (
	"context"
	"log/slog"
)

// 包加载仅建立实例，不打开文件或启动维护任务。
var defaultLogger, _ = New(Config{})

// Default 返回全局单例，首次打印或设置配置时初始化输出资源。
func Default() (logger *Logger) {
	return defaultLogger
}

// SetConfig 更新全局单例配置，返回同一个实例。
func SetConfig(config Config) (logger *Logger) {
	return Default().SetConfig(config)
}

// Close 关闭全局单例，关闭后不能重新配置或打印。
func Close() (err error) {
	return Default().Close()
}

// Skip 返回全局单例的派生实例，额外跳过 n 层调用。
func Skip(n int) (logger *Logger) {
	return Default().Skip(n)
}

// Log 使用全局单例按指定级别打印。
func Log(ctx context.Context, level slog.Level, message string, args ...any) {
	Default().output(ctx, level, message, args, outputPlain)
}

// Debug 使用全局单例打印调试日志。
func Debug(ctx context.Context, message string, args ...any) {
	Default().output(ctx, slog.LevelDebug, message, args, outputPlain)
}

// Info 使用全局单例打印普通日志。
func Info(ctx context.Context, message string, args ...any) {
	Default().output(ctx, slog.LevelInfo, message, args, outputPlain)
}

// Warn 使用全局单例打印警告日志。
func Warn(ctx context.Context, message string, args ...any) {
	Default().output(ctx, slog.LevelWarn, message, args, outputPlain)
}

// Error 使用全局单例打印错误日志。
func Error(ctx context.Context, message string, args ...any) {
	Default().output(ctx, slog.LevelError, message, args, outputPlain)
}

// Debugf 使用全局单例打印格式化调试日志。
func Debugf(ctx context.Context, format string, args ...any) {
	Default().output(ctx, slog.LevelDebug, format, args, outputFormatted)
}

// Infof 使用全局单例打印格式化普通日志。
func Infof(ctx context.Context, format string, args ...any) {
	Default().output(ctx, slog.LevelInfo, format, args, outputFormatted)
}

// Warnf 使用全局单例打印格式化警告日志。
func Warnf(ctx context.Context, format string, args ...any) {
	Default().output(ctx, slog.LevelWarn, format, args, outputFormatted)
}

// Errorf 使用全局单例打印格式化错误日志。
func Errorf(ctx context.Context, format string, args ...any) {
	Default().output(ctx, slog.LevelError, format, args, outputFormatted)
}
