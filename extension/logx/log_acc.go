package logx

import (
	"context"
	"fmt"
	"log/slog"
	"maps"
	"os"
	"runtime/debug"
	"slices"
	"sync"
	"time"

	"github.com/itrookie-sky/whtlib/adapter"
	"github.com/itrookie-sky/whtlib/extension/reflectx"
)

// LogAcc 并发安全地累积日志字段，使用后不能按值复制。
// 仅保护字段映射；值中的指针、map 和切片仍由调用方同步。
type LogAcc struct {
	mu     sync.RWMutex
	fields map[string]any
	logger adapter.Logger
	start  time.Time
	skip   int
}

// LogAccOptionFunc 配置日志累积器，仅在构造期间使用。
type LogAccOptionFunc func(acc *LogAcc)

// NewLogAcc 创建日志累积器，默认通过适配器使用全局 Logger。
func NewLogAcc(ops ...LogAccOptionFunc) (acc *LogAcc) {
	acc = &LogAcc{fields: make(map[string]any)}
	for _, op := range ops {
		if op != nil {
			op(acc)
		}
	}
	return
}

// WithLogger 注入日志后端，支持任意 adapter.Logger 实现。
// nil 或 typed nil 使用全局 Logger 的适配器；不接管实例生命周期。
func WithLogger(logger adapter.Logger) (op LogAccOptionFunc) {
	return func(acc *LogAcc) { acc.logger = logger }
}

// WithCost 设置计时起点，打印时在消息前附加耗时；零时间不启用计时。
func WithCost(start time.Time) (op LogAccOptionFunc) {
	return func(acc *LogAcc) { acc.SetStartTime(start) }
}

// SetStartTime 修改打印耗时的起点；零时间关闭耗时前缀。
func (a *LogAcc) SetStartTime(start time.Time) (acc *LogAcc) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.start = start
	return a
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
func (a *LogAcc) snapshot(args []any) (fields map[string]any, logger adapter.Logger, start time.Time, skip int) {
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
	if reflectx.IsNil(ctx) {
		panic("logx: nil context")
	}
	fields, logger, start, skip := a.snapshot(args)
	if reflectx.IsNil(logger) {
		logger = NewLoggerAdapter(nil)
	}
	// 公开累加器方法和本方法各占一层，后端适配器补偿其自身封装。
	maxSkip := int(^uint(0) >> 1)
	totalSkip := maxSkip
	if skip <= maxSkip-2 {
		totalSkip = skip + 2
	}
	view := logger.Skip(totalSkip)
	if reflectx.IsNil(view) {
		panic("logx: Logger.Skip returned nil")
	}
	// 惰性编码保留后端级别过滤能力，不向普通日志接口传入结构化键值对。
	data := &logAccMessage{message: message, fields: fields, start: start}
	switch level {
	case slog.LevelDebug:
		view.Debug(ctx, "", data)
	case slog.LevelWarn:
		view.Warn(ctx, "", data)
	case slog.LevelError:
		view.Error(ctx, "", data)
	default:
		view.Info(ctx, "", data)
	}
}

// logAccMessage 由后端在实际格式化正文时编码字段，持有独立快照。
type logAccMessage struct {
	message string
	fields  map[string]any
	start   time.Time
}

func (m *logAccMessage) String() (message string) {
	message = m.message
	if !m.start.IsZero() {
		message = fmt.Sprintf("[%s] %s", time.Since(m.start), message)
	}
	attrs := make([]slog.Attr, 0, len(m.fields))
	for _, key := range slices.Sorted(maps.Keys(m.fields)) {
		attrs = append(attrs, slog.Any(key, m.fields[key]))
	}
	fields, _ := attributeFields(attrs)
	if len(fields) == 0 {
		return
	}
	data, err := encodeFields(fields)
	if err != nil {
		_, _ = fmt.Fprintf(os.Stderr, "logx: %v\n%s", err, debug.Stack())
	}
	if message == "" {
		return string(data)
	}
	return message + " " + string(data)
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
	if value == nil || reflectx.IsNil(*value) {
		return a
	}
	return a.SetValue("err", (*value).Error())
}
