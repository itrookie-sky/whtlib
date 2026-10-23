package logx

import (
	"errors"
	"fmt"
)

var (
	// ErrInvalidConfig 表示日志配置或文件名表达式无效。
	ErrInvalidConfig = errors.New("logx: invalid config")
	// ErrFileConflict 表示共享文件、轮转策略或归档名称冲突。
	ErrFileConflict = errors.New("logx: file conflict")
	// ErrFileUnavailable 表示活动日志文件不可用。
	ErrFileUnavailable = errors.New("logx: active file unavailable")
)

func invalidConfig(format string, args ...any) (err error) {
	return fmt.Errorf("%w: %s", ErrInvalidConfig, fmt.Sprintf(format, args...))
}

func fileConflict(format string, args ...any) (err error) {
	return fmt.Errorf("%w: %s", ErrFileConflict, fmt.Sprintf(format, args...))
}

func fieldEncodingError(name string, cause error) (err error) {
	return fmt.Errorf("logx: encode %q: %w", name, cause)
}

func traceEncodingError(cause error) (err error) {
	return fmt.Errorf("logx: encode traceId: %w", cause)
}

func archiveRemovalError(name string, cause error) (err error) {
	return fmt.Errorf("logx: remove archive %q: %w", name, cause)
}
