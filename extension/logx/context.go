package logx

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
)

type jsonField struct {
	name  string
	value any
}

type orderedObject []jsonField

func encodeFields(fields []jsonField) (data []byte, err error) {
	data = append(data, '{')
	for i, field := range fields {
		if i > 0 {
			data = append(data, ',')
		}
		name, _ := json.Marshal(field.name)
		var value []byte
		var encodeErr error
		object, isObject := field.value.(orderedObject)
		if isObject {
			value, encodeErr = encodeFields(object)
		} else {
			value, encodeErr = json.Marshal(field.value)
		}
		if encodeErr != nil {
			err = errors.Join(err, fmt.Errorf("logx: encode %q: %w", field.name, encodeErr))
			if !isObject {
				value = []byte(`"[json encode error]"`)
			}
		}
		data = append(data, name...)
		data = append(data, ':')
		data = append(data, value...)
	}
	data = append(data, '}')
	return
}

// appendContextField 读取上下文字段，忽略 nil 和空字符串。
func appendContextField(ctx context.Context, fields []jsonField, name string, key any) (result []jsonField) {
	value := ctx.Value(key)
	if isNil(value) {
		return fields
	}
	rv := reflect.ValueOf(value)
	if rv.Kind() == reflect.String && rv.Len() == 0 {
		return fields
	}
	return append(fields, jsonField{name: name, value: value})
}

func contextText(ctx context.Context, config Config) (data []byte, err error) {
	fields := make([]jsonField, 0, 1+len(config.ContextFields))
	fields = appendContextField(ctx, fields, "traceId", config.TraceKey)
	for _, field := range config.ContextFields {
		fields = appendContextField(ctx, fields, field.Name, field.Key)
	}
	data = append(data, '{')
	for i, field := range fields {
		if i > 0 {
			data = append(data, ',')
		}
		data = append(data, field.name...)
		data = append(data, ':')
		value, encodeErr := json.Marshal(field.value)
		if encodeErr != nil {
			err = errors.Join(err, fmt.Errorf("logx: encode %q: %w", field.name, encodeErr))
			value = []byte("[json encode error]")
		} else if len(value) >= 2 && value[0] == '"' && value[len(value)-1] == '"' {
			// 仅移除字符串外围引号，保留换行等转义，避免一条日志跨行。
			value = value[1 : len(value)-1]
		}
		data = append(data, value...)
	}
	data = append(data, '}')
	return
}
