package logx

import (
	"context"
	"fmt"
	"log/slog"
	"maps"
	"slices"
	"sync"
	"time"
)

// LogAcc 并发安全地累积日志字段，使用后不能按值复制。
// 仅保护字段映射；值中的指针、map 和切片仍由调用方同步。
type LogAcc struct {
	mu     sync.RWMutex
	fields map[string]any
	logger *Logger
	start  time.Time
	skip   int
}

// LogAccOptionFunc 配置日志累积器，仅在构造期间使用。
type LogAccOptionFunc func(acc *LogAcc)

// NewLogAcc 创建日志累积器，默认使用全局 Logger。
func NewLogAcc(ops ...LogAccOptionFunc) (acc *LogAcc) {
	acc = &LogAcc{fields: make(map[string]any)}
	for _, op := range ops {
		if op != nil {
			op(acc)
		}
	}
	return
}

// WithLogger 指定日志实例，nil 表示使用全局 Logger；不接管实例生命周期。
func WithLogger(logger *Logger) (op LogAccOptionFunc) {
	return func(acc *LogAcc) { acc.logger = logger }
}

// WithCost 设置计时起点，打印时在消息前附加耗时；零时间不启用计时。
func WithCost(start time.Time) (op LogAccOptionFunc) {
	return func(acc *LogAcc) { acc.start = start }
}

// SetValue 覆盖单个字段，返回原累积器。
func (a *LogAcc) SetValue(key string, value any) (acc *LogAcc) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.fields == nil {
		a.fields = make(map[string]any)
	}
	a.fields[key] = value
	return a
}

// Update 将多个字段原子地合并到内部 map，重复字段使用新值。
func (a *LogAcc) Update(values map[string]any) (acc *LogAcc) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.fields == nil {
		a.fields = make(map[string]any, len(values))
	}
	maps.Copy(a.fields, values)
	return a
}

// Skip 累加业务调用跳过层数并返回原累积器；负数和溢出会 panic。
func (a *LogAcc) Skip(n int) (acc *LogAcc) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if n < 0 || n > int(^uint(0)>>1)-a.skip {
		panic("logx: invalid skip")
	}
	a.skip += n
	return a
}

// snapshot 先校验完整键值对，再一次性更新并复制字段，避免部分写入。
func (a *LogAcc) snapshot(args []any) (fields map[string]any, logger *Logger, start time.Time, skip int) {
	if len(args)%2 != 0 {
		panic("logx: LogAcc requires key/value pairs")
	}
	for i := 0; i < len(args); i += 2 {
		if _, ok := args[i].(string); !ok {
			panic("logx: LogAcc field key must be a string")
		}
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.fields == nil {
		a.fields = make(map[string]any)
	}
	for i := 0; i < len(args); i += 2 {
		a.fields[args[i].(string)] = args[i+1]
	}
	return maps.Clone(a.fields), a.logger, a.start, a.skip
}

func (a *LogAcc) output(ctx context.Context, level slog.Level, message string, args []any) {
	if isNil(ctx) {
		panic("logx: nil context")
	}
	fields, logger, start, skip := a.snapshot(args)
	if logger == nil {
		logger = Default()
	}
	keys := slices.Sorted(maps.Keys(fields))
	attrs := make([]any, 0, len(keys)*2)
	for _, key := range keys {
		attrs = append(attrs, key, fields[key])
	}
	if !start.IsZero() {
		message = fmt.Sprintf("[%s] %s", time.Since(start), message)
	}
	// 临时调用视图仅调整栈深度，不派生文件引用，也不关闭借用的 Logger。
	maxSkip := int(^uint(0) >> 1)
	totalSkip := maxSkip
	if skip < maxSkip && logger.skip <= maxSkip-skip-1 {
		totalSkip = logger.skip + skip + 1
	}
	view := Logger{core: logger.core, skip: totalSkip}
	view.output(ctx, level, message, attrs, outputStructured)
}

// Debug 合并可选键值对并打印当前字段快照。
func (a *LogAcc) Debug(ctx context.Context, message string, args ...any) {
	a.output(ctx, slog.LevelDebug, message, args)
}

// Info 合并可选键值对并打印当前字段快照。
func (a *LogAcc) Info(ctx context.Context, message string, args ...any) {
	a.output(ctx, slog.LevelInfo, message, args)
}

// Warn 合并可选键值对并打印当前字段快照。
func (a *LogAcc) Warn(ctx context.Context, message string, args ...any) {
	a.output(ctx, slog.LevelWarn, message, args)
}

// Error 合并可选键值对并打印当前字段快照。
func (a *LogAcc) Error(ctx context.Context, message string, args ...any) {
	a.output(ctx, slog.LevelError, message, args)
}

// SetErr 读取当前错误并覆盖 err 字段；nil 指针或 nil 错误保持已有字段。
// 可使用 defer acc.SetErr(&err)，调用方负责同步 error 变量的读写。
func (a *LogAcc) SetErr(value *error) (acc *LogAcc) {
	if value == nil || isNil(*value) {
		return a
	}
	return a.SetValue("err", (*value).Error())
}
