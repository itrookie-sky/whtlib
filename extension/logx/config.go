// Package logx 提供基于 slog 的上下文日志和可轮转文件输出。
package logx

import (
	"fmt"
	"io"
	"log/slog"
	"reflect"
	"regexp"
	"slices"
)

// ContextField 指定日志字段名称及其 context key。
type ContextField struct {
	Name string
	Key  any
}

// Config 配置日志实例，零值使用默认文件输出和 Info 级别。
type Config struct {
	Writer        io.Writer
	File          FileConfig
	Level         slog.Leveler
	TraceKey      any
	ContextFields []ContextField
	Skip          int
	FullPath      bool
}

var fieldNamePattern = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)

func isNil(v any) (result bool) {
	if v == nil {
		return true
	}
	rv := reflect.ValueOf(v)
	switch rv.Kind() {
	case reflect.Chan, reflect.Func, reflect.Interface, reflect.Map, reflect.Pointer, reflect.Slice, reflect.UnsafePointer:
		return rv.IsNil()
	}
	return false
}

func validKey(key any) (valid bool) {
	return !isNil(key) && reflect.ValueOf(key).Comparable()
}

// BuildDefaultConfig 构建默认的 Config。
func BuildDefaultConfig(config Config) (result Config, err error) {
	result = config
	if result.Skip < 0 {
		return result, fmt.Errorf("logx: negative skip")
	}
	if result.Writer != nil && isNil(result.Writer) {
		return result, fmt.Errorf("logx: nil writer")
	}
	if result.Level == nil {
		result.Level = slog.LevelInfo
	} else if isNil(result.Level) {
		return result, fmt.Errorf("logx: nil level")
	}
	if result.TraceKey == nil {
		result.TraceKey = "traceId"
	}
	if !validKey(result.TraceKey) {
		return result, fmt.Errorf("logx: invalid trace key")
	}
	result.ContextFields = slices.Clone(result.ContextFields)
	seen := map[string]bool{"traceId": true}
	for _, field := range result.ContextFields {
		if !fieldNamePattern.MatchString(field.Name) || seen[field.Name] || !validKey(field.Key) {
			return result, fmt.Errorf("logx: invalid context field %q", field.Name)
		}
		seen[field.Name] = true
	}
	return
}

// mergeConfig 将显式非零字段合并到旧配置，nil 切片表示保留。
func mergeConfig(previous Config, update Config) (result Config) {
	result = previous
	if update.Writer != nil {
		result.Writer = update.Writer
	}
	if update.Level != nil {
		result.Level = update.Level
	}
	if update.TraceKey != nil {
		result.TraceKey = update.TraceKey
	}
	if update.ContextFields != nil {
		result.ContextFields = update.ContextFields
	}
	if update.Skip != 0 {
		result.Skip = update.Skip
	}
	if update.FullPath {
		result.FullPath = true
	}
	result.File = mergeFileConfig(previous.File, update.File)
	return
}

func mergeFileConfig(previous FileConfig, update FileConfig) (result FileConfig) {
	result = previous
	if update.Path != "" {
		result.Path = update.Path
	}
	if update.File != "" {
		result.File = update.File
	}
	if update.RotateSize != 0 {
		result.RotateSize = update.RotateSize
	}
	if update.RotateInterval != 0 {
		result.RotateInterval = update.RotateInterval
	}
	if update.RotateBackupExpire != 0 {
		result.RotateBackupExpire = update.RotateBackupExpire
	}
	if update.RotateBackupLimit != 0 {
		result.RotateBackupLimit = update.RotateBackupLimit
	}
	if update.RotateCheckInterval != 0 {
		result.RotateCheckInterval = update.RotateCheckInterval
	}
	if update.OnError != nil {
		result.OnError = update.OnError
	}
	return
}
