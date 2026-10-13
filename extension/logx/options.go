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

// Options 配置日志实例，零值使用默认文件输出和 Info 级别。
type Options struct {
	Writer        io.Writer
	File          FileOptions
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

func normalizeOptions(options Options) (result Options, err error) {
	result = options
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
