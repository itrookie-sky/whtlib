package logx

import (
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

// FileConfig 配置文件输出；零值使用默认值，部分阈值以 -1 禁用。
type FileConfig struct {
	Path string
	// File 支持花括号中的 y/m/d 日期占位符，默认 {y-m-d}.log。
	File                string
	RotateSize          int64
	RotateInterval      time.Duration
	RotateBackupExpire  time.Duration
	RotateBackupLimit   int
	RotateCheckInterval time.Duration
	// OnError 接收后台维护错误，回调内不要重入 logx API。
	OnError func(error)
}

// DefaultFileConfig 返回独立的默认配置，归档保留 24 小时。
func DefaultFileConfig() (config FileConfig) {
	return FileConfig{
		Path:                "./logs",
		File:                "{y-m-d}.log",
		RotateSize:          100 * 1024 * 1024,
		RotateInterval:      24 * time.Hour,
		RotateBackupExpire:  24 * time.Hour,
		RotateCheckInterval: time.Minute,
	}
}

func normalizeFileConfig(config FileConfig) (result FileConfig, err error) {
	result = config
	defaults := DefaultFileConfig()
	if result.Path == "" {
		result.Path = defaults.Path
	}
	if result.File == "" {
		result.File = defaults.File
	}
	if result.RotateSize == 0 {
		result.RotateSize = defaults.RotateSize
	}
	if result.RotateInterval == 0 {
		result.RotateInterval = defaults.RotateInterval
	}
	if result.RotateBackupExpire == 0 {
		result.RotateBackupExpire = defaults.RotateBackupExpire
	}
	if result.RotateCheckInterval == 0 {
		result.RotateCheckInterval = defaults.RotateCheckInterval
	}
	if result.RotateSize < -1 || result.RotateInterval < -1 || result.RotateBackupExpire < -1 || result.RotateBackupLimit < 0 || result.RotateCheckInterval < 0 {
		return result, invalidConfig("invalid rotation configuration")
	}
	if result.File == ".log" || !strings.HasSuffix(result.File, ".log") || strings.ContainsAny(result.File, `/\`) || filepath.Base(result.File) != result.File {
		return result, invalidConfig("invalid log filename %q", result.File)
	}
	_, err = compileFilePattern(result.File)
	if err != nil {
		return
	}
	result.Path, err = filepath.Abs(result.Path)
	if err != nil {
		return
	}
	if result.OnError == nil {
		result.OnError = func(err error) { _, _ = fmt.Fprintln(os.Stderr, "logx:", err) }
	}
	return
}

// fileWriter 提供单进程内并发安全的日志写入、轮转和清理。
// 同一活动文件只能由一个 fileWriter 管理。
type fileWriter struct {
	mu         sync.Mutex
	config     FileConfig
	file       *os.File
	pattern    *filePattern
	activeName string
	size       int64
	started    time.Time
	closed     bool
	stop       chan struct{}
	done       chan struct{}
	wake       chan struct{}
	pending    error
	close      sync.Once
	closeErr   error
}

// newFileWriter 创建文件 Writer 并启动后台维护，调用方需要 Close。
func newFileWriter(config FileConfig) (writer *fileWriter, err error) {
	config, err = normalizeFileConfig(config)
	if err != nil {
		return
	}
	err = os.MkdirAll(config.Path, 0o750)
	if err != nil {
		return
	}
	writer = &fileWriter{config: config, stop: make(chan struct{}), done: make(chan struct{}), wake: make(chan struct{}, 1)}
	writer.pattern, err = compileFilePattern(config.File)
	if err != nil {
		return nil, err
	}
	err = writer.openLocked(time.Now())
	if err != nil {
		return nil, err
	}
	writer.pending = writer.cleanupLocked(time.Now())
	if writer.pending != nil {
		writer.wake <- struct{}{}
	}
	go writer.maintain()
	return
}

func (w *fileWriter) openLocked(now time.Time) (err error) {
	name := w.pattern.expand(now)
	path := filepath.Join(w.config.Path, name)
	info, statErr := os.Lstat(path)
	if statErr == nil && !info.Mode().IsRegular() {
		return invalidConfig("active file is not a regular file")
	}
	if statErr != nil && !errors.Is(statErr, os.ErrNotExist) {
		return statErr
	}
	file, err := os.OpenFile(path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o640)
	if err != nil {
		return
	}
	info, err = file.Stat()
	if err != nil {
		_ = file.Close()
		return
	}
	if w.file != nil {
		err = w.file.Close()
	}
	w.file = file
	w.activeName = name
	w.size = info.Size()
	w.started = now
	if w.size > 0 {
		w.started = info.ModTime()
	}
	return
}

func (w *fileWriter) notifyLocked(err error) {
	if err == nil {
		return
	}
	// 合并尚未消费的维护错误，唤醒后台回调而不阻塞写入。
	w.pending = errors.Join(w.pending, err)
	select {
	case w.wake <- struct{}{}:
	default:
	}
}

// Write 写入一个完整记录，大小超限时在写入前轮转，不拆分 p。
func (w *fileWriter) Write(p []byte) (n int, err error) {
	return w.writeAt(p, time.Now())
}

func (w *fileWriter) writeAt(p []byte, now time.Time) (n int, err error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.closed {
		return 0, os.ErrClosed
	}
	if len(p) == 0 {
		return
	}
	if w.file == nil || w.activeName != w.pattern.expand(now) {
		err = w.openLocked(now)
		if err != nil {
			return
		}
	}
	sizeDue := w.config.RotateSize > 0 && (w.size >= w.config.RotateSize || int64(len(p)) > w.config.RotateSize-w.size)
	timeDue := w.config.RotateInterval > 0 && now.Sub(w.started) >= w.config.RotateInterval
	if w.size > 0 && (sizeDue || timeDue) {
		err = w.rotateLocked(now)
		if err != nil {
			return
		}
		w.notifyLocked(w.cleanupLocked(now))
	}
	n, err = w.file.Write(p)
	w.size += int64(n)
	if n != len(p) && err == nil {
		err = io.ErrShortWrite
	}
	return
}

// Sync 将活动文件数据提交到文件系统。
func (w *fileWriter) Sync() (err error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.closed {
		return os.ErrClosed
	}
	if w.file == nil {
		return ErrFileUnavailable
	}
	return w.file.Sync()
}

// Close 停止维护任务并关闭文件，重复调用返回相同结果。
func (w *fileWriter) Close() (err error) {
	w.close.Do(func() {
		w.mu.Lock()
		w.closed = true
		close(w.stop)
		w.mu.Unlock()
		<-w.done
		w.mu.Lock()
		defer w.mu.Unlock()
		w.closeErr = w.pending
		w.pending = nil
		if w.file != nil {
			w.closeErr = errors.Join(w.closeErr, w.file.Close())
			w.file = nil
		}
	})
	return w.closeErr
}

// reconfigure 仅用于同一路径的策略更新，调用方已完成校验。
func (w *fileWriter) reconfigure(config FileConfig) (err error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.closed {
		return os.ErrClosed
	}
	w.config = config
	select {
	case w.wake <- struct{}{}:
	default:
	}
	return
}

func (w *fileWriter) maintain() {
	w.mu.Lock()
	interval := w.config.RotateCheckInterval
	w.mu.Unlock()
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	defer close(w.done)
	for {
		select {
		case <-w.stop:
			return
		case <-ticker.C:
		case <-w.wake:
		}
		w.mu.Lock()
		if w.closed {
			w.mu.Unlock()
			return
		}
		err := w.pending
		w.pending = nil
		now := time.Now()
		if w.activeName != w.pattern.expand(now) {
			err = errors.Join(err, w.openLocked(now))
		}
		if w.file != nil && w.size > 0 && w.config.RotateInterval > 0 && now.Sub(w.started) >= w.config.RotateInterval {
			err = errors.Join(err, w.rotateLocked(now))
		}
		err = errors.Join(err, w.cleanupLocked(now))
		interval = w.config.RotateCheckInterval
		onError := w.config.OnError
		w.mu.Unlock()
		ticker.Reset(interval)
		if err != nil {
			onError(err)
		}
	}
}
