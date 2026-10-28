package logx

import (
	"context"
	"fmt"
	"log/slog"
)

// 包加载仅建立实例，不打开文件或启动维护任务。
var defaultLogger *Logger

func init() {
	var err error
	defaultLogger, err = New(Config{})
	if err != nil {
		panic(fmt.Errorf("logx: defaultLogger init failed, %w", err))
	}
}

// Default 返回全局单例，首次打印或设置配置时初始化输出资源。
func Default() (logger *Logger) {
	return defaultLogger
}

// Clone 克隆全局 Logger 的当前配置，返回需要独立 Close 的实例。
func Clone() (logger *Logger, err error) {
	return Default().Clone()
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
func Log(ctx context.Context, level slog.Level, args ...any) {
	Default().output(ctx, level, "", args, outputPlain)
}

// Debug 使用全局单例打印调试日志。
func Debug(ctx context.Context, args ...any) {
	Default().output(ctx, slog.LevelDebug, "", args, outputPlain)
}

// Info 使用全局单例打印普通日志。
func Info(ctx context.Context, args ...any) {
	Default().output(ctx, slog.LevelInfo, "", args, outputPlain)
}

// Warn 使用全局单例打印警告日志。
func Warn(ctx context.Context, args ...any) {
	Default().output(ctx, slog.LevelWarn, "", args, outputPlain)
}

// Error 使用全局单例打印错误日志。
func Error(ctx context.Context, args ...any) {
	Default().output(ctx, slog.LevelError, "", args, outputPlain)
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
