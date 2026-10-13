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
	Path                string
	File                string
	RotateSize          int64
	RotateInterval      time.Duration
	RotateBackupExpire  time.Duration
	RotateBackupLimit   int
	RotateCheckInterval time.Duration
	// OnError 接收后台维护错误，回调内不能关闭当前 Writer/Logger 或调用 Logger.SetConfig。
	OnError func(error)
}

// DefaultFileConfig 返回独立的默认配置，归档保留 24 小时。
func DefaultFileConfig() (config FileConfig) {
	return FileConfig{
		Path: "./logs", File: "app.log", RotateSize: 100 * 1024 * 1024,
		RotateInterval: 24 * time.Hour, RotateBackupExpire: 24 * time.Hour,
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
		return result, fmt.Errorf("logx: invalid rotation configuration")
	}
	if result.File == ".log" || !strings.HasSuffix(result.File, ".log") || strings.ContainsAny(result.File, `/\`) || filepath.Base(result.File) != result.File {
		return result, fmt.Errorf("logx: invalid log filename %q", result.File)
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

// FileWriter 提供单进程内并发安全的日志写入、轮转和清理。
// 同一活动文件只能由一个 FileWriter 管理。
type FileWriter struct {
	mu       sync.Mutex
	config   FileConfig
	file     *os.File
	size     int64
	started  time.Time
	closed   bool
	stop     chan struct{}
	done     chan struct{}
	wake     chan struct{}
	pending  error
	close    sync.Once
	closeErr error
}

// NewFileWriter 创建文件 Writer 并启动后台维护，调用方需要 Close。
func NewFileWriter(config FileConfig) (writer *FileWriter, err error) {
	config, err = normalizeFileConfig(config)
	if err != nil {
		return
	}
	err = os.MkdirAll(config.Path, 0o750)
	if err != nil {
		return
	}
	writer = &FileWriter{config: config, stop: make(chan struct{}), done: make(chan struct{}), wake: make(chan struct{}, 1)}
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

func (w *FileWriter) openLocked(now time.Time) (err error) {
	path := filepath.Join(w.config.Path, w.config.File)
	info, statErr := os.Lstat(path)
	if statErr == nil && !info.Mode().IsRegular() {
		return fmt.Errorf("logx: active file is not a regular file")
	}
	if statErr != nil && !errors.Is(statErr, os.ErrNotExist) {
		return statErr
	}
	w.file, err = os.OpenFile(path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o640)
	if err != nil {
		return
	}
	info, err = w.file.Stat()
	if err != nil {
		_ = w.file.Close()
		w.file = nil
		return
	}
	w.size = info.Size()
	w.started = now
	if w.size > 0 {
		w.started = info.ModTime()
	}
	return
}

func (w *FileWriter) notifyLocked(err error) {
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
func (w *FileWriter) Write(p []byte) (n int, err error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.closed {
		return 0, os.ErrClosed
	}
	if len(p) == 0 {
		return
	}
	now := time.Now()
	if w.file == nil {
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
func (w *FileWriter) Sync() (err error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.closed {
		return os.ErrClosed
	}
	if w.file == nil {
		return fmt.Errorf("logx: active file unavailable")
	}
	return w.file.Sync()
}

// Close 停止维护任务并关闭文件，重复调用返回相同结果。
func (w *FileWriter) Close() (err error) {
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
func (w *FileWriter) reconfigure(config FileConfig) (err error) {
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

func (w *FileWriter) maintain() {
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
