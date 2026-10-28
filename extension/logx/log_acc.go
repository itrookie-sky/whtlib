package logx

import (
	"context"
	"fmt"
	"log/slog"
	"maps"
	"os"
	"runtime/debug"
	"slices"
	"strings"
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

var _ adapter.LogAcc = (*LogAcc)(nil)

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
func (a *LogAcc) SetStartTime(start time.Time) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.start = start
}

// SetValue 覆盖当前累积器的单个字段。
func (a *LogAcc) SetValue(key string, value any) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.fields == nil {
		a.fields = make(map[string]any)
	}
	a.fields[key] = value
}

// Update 将多个字段原子地合并到内部 map，重复字段使用新值。
func (a *LogAcc) Update(values map[string]any) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.fields == nil {
		a.fields = make(map[string]any, len(values))
	}
	maps.Copy(a.fields, values)
}

// Skip 累加当前累积器的业务调用跳过层数；负数和溢出会 panic。
func (a *LogAcc) Skip(n int) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if n < 0 || n > int(^uint(0)>>1)-a.skip {
		panic("logx: invalid skip")
	}
	a.skip += n
}

// snapshot 读取配置和字段快照，打印不修改已累计的字段。
func (a *LogAcc) snapshot() (fields map[string]any, logger adapter.Logger, start time.Time, skip int) {
	a.mu.RLock()
	defer a.mu.RUnlock()
	return maps.Clone(a.fields), a.logger, a.start, a.skip
}

func (a *LogAcc) output(ctx context.Context, level slog.Level, args []any) {
	if reflectx.IsNil(ctx) {
		panic("logx: nil context")
	}
	fields, logger, start, skip := a.snapshot()
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
	data := &logAccMessage{args: slices.Clone(args), fields: fields, start: start}
	switch level {
	case slog.LevelDebug:
		view.Debug(ctx, data)
	case slog.LevelWarn:
		view.Warn(ctx, data)
	case slog.LevelError:
		view.Error(ctx, data)
	default:
		view.Info(ctx, data)
	}
}

// logAccMessage 由后端在实际格式化正文时编码字段，持有独立快照。
type logAccMessage struct {
	args   []any
	fields map[string]any
	start  time.Time
}

func (m *logAccMessage) String() (message string) {
	message = strings.TrimSuffix(fmt.Sprintln(m.args...), "\n")
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

// Debug 按空格拼接全部正文参数并打印当前字段快照。
func (a *LogAcc) Debug(ctx context.Context, args ...any) {
	a.output(ctx, slog.LevelDebug, args)
}

// Info 按空格拼接全部正文参数并打印当前字段快照。
func (a *LogAcc) Info(ctx context.Context, args ...any) {
	a.output(ctx, slog.LevelInfo, args)
}

// Warn 按空格拼接全部正文参数并打印当前字段快照。
func (a *LogAcc) Warn(ctx context.Context, args ...any) {
	a.output(ctx, slog.LevelWarn, args)
}

// Error 按空格拼接全部正文参数并打印当前字段快照。
func (a *LogAcc) Error(ctx context.Context, args ...any) {
	a.output(ctx, slog.LevelError, args)
}

// SetErr 读取当前错误并覆盖 err 字段；nil 指针或 nil 错误保持已有字段。
// 可使用 defer acc.SetErr(&err)，调用方负责同步 error 变量的读写。
func (a *LogAcc) SetErr(value *error) {
	if value == nil || reflectx.IsNil(*value) {
		return
	}
	a.SetValue("err", (*value).Error())
}
