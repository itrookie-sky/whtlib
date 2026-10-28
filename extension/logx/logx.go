package logx

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"runtime"
	"runtime/debug"
	"strings"
	"sync"
	"time"

	"github.com/itrookie-sky/whtlib/adapter"
	"github.com/itrookie-sky/whtlib/extension/reflectx"
)

type CtxKey string

// Logger 提供必须传入 ctx 的日志方法，各实例独立保存配置与关闭状态。
// 打印方法不返回错误，编码和写入错误连同调用堆栈直接输出到 stderr。
type Logger struct {
	core *loggerCore
	skip int
}

// New 创建日志实例，默认输出到终端，WriteFile 为 true 时获取共享文件资源。
func New(config Config) (logger *Logger, err error) {
	logger = &Logger{core: &loggerCore{config: config}}
	err = logger.core.initialize()
	if err != nil {
		return nil, err
	}
	return
}

// Clone 创建当前配置的独立快照，继承调用跳过层数并共享文件写入资源。
// 后续 SetConfig、Close 各自独立；已关闭实例返回 os.ErrClosed。
// nil 或未初始化的 Logger 返回 ErrInvalidConfig。
func (l *Logger) Clone() (logger *Logger, err error) {
	if l == nil || l.core == nil {
		return nil, invalidConfig("cannot clone nil or uninitialized logger")
	}
	l.core.mu.RLock()
	defer l.core.mu.RUnlock()
	if l.core.closed {
		return nil, os.ErrClosed
	}
	return l.cloneLocked(l.skip)
}

// cloneLocked 复制配置、实例状态和文件引用；调用方必须持有 core 的读锁。
// 保留 closed 状态供原有 Skip 使用，Clone 自行拒绝已关闭实例。
func (l *Logger) cloneLocked(skip int) (logger *Logger, err error) {
	config, err := BuildDefaultConfig(l.core.config)
	if err != nil {
		return
	}
	logger = &Logger{core: &loggerCore{config: config, closed: l.core.closed}, skip: skip}
	if l.core.handler == nil || l.core.closed {
		return
	}
	var lease *fileLease
	if l.core.handler.state.lease != nil {
		lease = sharedFiles.retain(l.core.handler.state.lease)
	}
	logger.core.handler = &handler{config: config, state: &outputState{writer: l.core.handler.state.writer, lease: lease}}
	return
}

// SetConfig 更新当前实例并返回原实例；WriteFile 始终生效，其他未填写项沿用旧值。
// 更新失败时保留旧配置，并向 stderr 输出错误和调用堆栈。
func (l *Logger) SetConfig(config Config) (logger *Logger) {
	err := l.core.setConfig(config)
	if err != nil {
		_, _ = fmt.Fprintf(os.Stderr, "logx: %v\n%s", err, debug.Stack())
	}
	return l
}

// Skip 返回额外跳过 n 层业务调用的独立实例，不改变原实例，调用方需单独 Close。
func (l *Logger) Skip(n int) (logger *Logger) {
	l.core.mu.RLock()
	defer l.core.mu.RUnlock()
	baseSkip := l.core.config.Skip
	if n < 0 || n > int(^uint(0)>>1)-l.skip || baseSkip > int(^uint(0)>>1)-l.skip-n {
		panic("logx: invalid skip")
	}
	clone, err := l.cloneLocked(l.skip + n)
	if err != nil {
		panic(err)
	}
	return clone
}

// Close 仅关闭当前实例，最后一个引用释放时关闭对应文件。
func (l *Logger) Close() (err error) {
	return l.core.close()
}

func (l *Logger) output(ctx context.Context, level slog.Level, message string, args []any, mode outputMode) {
	if reflectx.IsNil(ctx) {
		panic("logx: nil context")
	}
	err := l.core.initialize()
	if err != nil {
		_, _ = fmt.Fprintf(os.Stderr, "logx: %v\n%s", err, debug.Stack())
		return
	}
	l.core.mu.RLock()
	defer l.core.mu.RUnlock()
	if l.core.closed {
		_, _ = fmt.Fprintf(os.Stderr, "logx: %v\n%s", os.ErrClosed, debug.Stack())
		return
	}
	h := l.core.handler
	if !h.Enabled(ctx, level) {
		return
	}
	var pc uintptr
	if l.skip <= int(^uint(0)>>1)-l.core.config.Skip {
		pc = callerPC(l.skip + l.core.config.Skip)
	}
	switch mode {
	case outputFormatted:
		message = fmt.Sprintf(message, args...)
	case outputPlain:
		message = strings.TrimSuffix(fmt.Sprintln(args...), "\n")
	}
	record := slog.NewRecord(time.Now(), level, message, pc)
	if mode == outputStructured {
		record.Add(args...)
	}
	err = h.Handle(ctx, record)
	if err != nil {
		// 直接写 stderr，避免故障 Writer 导致递归记录。
		_, _ = fmt.Fprintf(os.Stderr, "logx: %v\n%s", err, debug.Stack())
	}
}

// Log 按指定级别输出消息，可变参数以空格拼接到正文。
func (l *Logger) Log(ctx context.Context, level slog.Level, args ...any) {
	l.output(ctx, level, "", args, outputPlain)
}

// Debug 输出调试日志。
func (l *Logger) Debug(ctx context.Context, args ...any) {
	l.output(ctx, slog.LevelDebug, "", args, outputPlain)
}

// Info 输出普通日志。
func (l *Logger) Info(ctx context.Context, args ...any) {
	l.output(ctx, slog.LevelInfo, "", args, outputPlain)
}

// Warn 输出警告日志。
func (l *Logger) Warn(ctx context.Context, args ...any) {
	l.output(ctx, slog.LevelWarn, "", args, outputPlain)
}

// Error 输出错误日志。
func (l *Logger) Error(ctx context.Context, args ...any) {
	l.output(ctx, slog.LevelError, "", args, outputPlain)
}

// Debugf 输出格式化调试日志。
func (l *Logger) Debugf(ctx context.Context, format string, args ...any) {
	l.output(ctx, slog.LevelDebug, format, args, outputFormatted)
}

// Infof 输出格式化普通日志。
func (l *Logger) Infof(ctx context.Context, format string, args ...any) {
	l.output(ctx, slog.LevelInfo, format, args, outputFormatted)
}

// Warnf 输出格式化警告日志。
func (l *Logger) Warnf(ctx context.Context, format string, args ...any) {
	l.output(ctx, slog.LevelWarn, format, args, outputFormatted)
}

// Errorf 输出格式化错误日志。
func (l *Logger) Errorf(ctx context.Context, format string, args ...any) {
	l.output(ctx, slog.LevelError, format, args, outputFormatted)
}

// loggerCore 保存单个实例私有的配置和生命周期。
type loggerCore struct {
	mu       sync.RWMutex
	changeMu sync.Mutex
	config   Config
	handler  *handler
	closed   bool
	closeErr error
}

func buildHandler(config Config, previous *handler) (next *handler, resolved Config, retired *fileLease, err error) {
	resolved, err = BuildDefaultConfig(config)
	if err != nil {
		return
	}
	var old *fileLease
	if previous != nil {
		old = previous.state.lease
	}
	state := &outputState{writer: terminalOutput}
	var lease *fileLease
	if resolved.WriteFile {
		var fileConfig FileConfig
		lease, fileConfig, err = sharedFiles.acquire(resolved.File, old)
		if err != nil {
			return
		}
		resolved.File = fileConfig
		state.writer = &dualWriter{terminal: terminalOutput, file: lease.entry.writer}
		state.lease = lease
	}
	next = &handler{state: state, config: resolved}
	if previous != nil {
		previous.state.mu.Lock()
		previous.state.closed = true
		previous.state.mu.Unlock()
		if old != lease {
			retired = old
		}
	}
	return
}

func (c *loggerCore) initialize() (err error) {
	c.mu.RLock()
	ready := c.handler != nil
	closed := c.closed
	c.mu.RUnlock()
	if closed {
		return os.ErrClosed
	}
	if ready {
		return
	}
	c.changeMu.Lock()
	defer c.changeMu.Unlock()
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.closed {
		return os.ErrClosed
	}
	if c.handler != nil {
		return
	}
	next, config, _, err := buildHandler(c.config, nil)
	if err != nil {
		return
	}
	c.handler = next
	c.config = config
	return
}

func (c *loggerCore) setConfig(update Config) (err error) {
	// 串行完成切换与旧资源释放，避免连续更新重复打开同一文件。
	c.changeMu.Lock()
	defer c.changeMu.Unlock()
	c.mu.Lock()
	if c.closed {
		c.mu.Unlock()
		return os.ErrClosed
	}
	next, config, retired, err := buildHandler(mergeConfig(c.config, update), c.handler)
	if err == nil {
		c.handler = next
		c.config = config
	}
	c.mu.Unlock()
	if err != nil {
		return
	}
	// 释放配置锁后再等待旧维护任务退出，避免阻塞新配置的打印。
	if retired != nil {
		err = sharedFiles.release(retired)
	}
	return
}

func (c *loggerCore) close() (err error) {
	c.changeMu.Lock()
	defer c.changeMu.Unlock()
	c.mu.Lock()
	if c.closed {
		c.mu.Unlock()
		return c.closeErr
	}
	c.closed = true
	var lease *fileLease
	if c.handler != nil {
		c.handler.state.mu.Lock()
		c.handler.state.closed = true
		lease = c.handler.state.lease
		c.handler.state.mu.Unlock()
	}
	c.mu.Unlock()
	if lease != nil {
		c.closeErr = sharedFiles.release(lease)
	}
	return c.closeErr
}

func callerPC(skip int) (pc uintptr) {
	// Callers、callerPC、output 和公开日志方法属于内部层。
	var pcs [1]uintptr
	if skip > int(^uint(0)>>1)-4 {
		return
	}
	if runtime.Callers(4+skip, pcs[:]) == 0 {
		return
	}
	return pcs[0]
}

var _ adapter.Logger = (*loggerAdapter)(nil)

// loggerAdapter 借用日志后端，Skip 仅保存打印视图，不派生文件引用。
type loggerAdapter struct {
	logger *Logger
	skip   int
}

// NewLoggerAdapter 将 logx.Logger 适配为共享日志接口；nil 使用 Default。
// 适配器和 Skip 视图不接管后端生命周期，创建方仍负责关闭原 Logger。
func NewLoggerAdapter(logger *Logger) (result adapter.Logger) {
	if logger == nil {
		logger = Default()
	}
	return &loggerAdapter{logger: logger}
}

func (a *loggerAdapter) Skip(n int) (logger adapter.Logger) {
	if n < 0 || n > int(^uint(0)>>1)-a.skip {
		panic("logx: invalid skip")
	}
	return &loggerAdapter{logger: a.logger, skip: a.skip + n}
}

func (a *loggerAdapter) output(ctx context.Context, level slog.Level, message string, args []any, mode outputMode) {
	// 仅构造借用视图；额外补偿本方法一层，饱和时不记录源码位置。
	maxSkip := int(^uint(0) >> 1)
	totalSkip := maxSkip
	if a.skip < maxSkip && a.logger.skip <= maxSkip-a.skip-1 {
		totalSkip = a.logger.skip + a.skip + 1
	}
	view := Logger{core: a.logger.core, skip: totalSkip}
	view.output(ctx, level, message, args, mode)
}

func (a *loggerAdapter) Debug(ctx context.Context, args ...any) {
	a.output(ctx, slog.LevelDebug, "", args, outputPlain)
}

func (a *loggerAdapter) Info(ctx context.Context, args ...any) {
	a.output(ctx, slog.LevelInfo, "", args, outputPlain)
}

func (a *loggerAdapter) Warn(ctx context.Context, args ...any) {
	a.output(ctx, slog.LevelWarn, "", args, outputPlain)
}

func (a *loggerAdapter) Error(ctx context.Context, args ...any) {
	a.output(ctx, slog.LevelError, "", args, outputPlain)
}

func (a *loggerAdapter) Debugf(ctx context.Context, format string, args ...any) {
	a.output(ctx, slog.LevelDebug, format, args, outputFormatted)
}

func (a *loggerAdapter) Infof(ctx context.Context, format string, args ...any) {
	a.output(ctx, slog.LevelInfo, format, args, outputFormatted)
}

func (a *loggerAdapter) Warnf(ctx context.Context, format string, args ...any) {
	a.output(ctx, slog.LevelWarn, format, args, outputFormatted)
}

func (a *loggerAdapter) Errorf(ctx context.Context, format string, args ...any) {
	a.output(ctx, slog.LevelError, format, args, outputFormatted)
}
