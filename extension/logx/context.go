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

func contextJSON(ctx context.Context, config Config) (data []byte, err error) {
	fields := make([]jsonField, 0, len(config.ContextFields))
	for _, field := range config.ContextFields {
		fields = appendContextField(ctx, fields, field.Name, field.Key)
	}
	if len(fields) == 0 {
		return nil, nil
	}
	return encodeFields(fields)
}

// contextTrace 将 traceId 输出为独立占位，空值省略，字符串去掉外围引号并保留转义。
func contextTrace(ctx context.Context, config Config) (data []byte, err error) {
	fields := appendContextField(ctx, nil, "traceId", config.TraceKey)
	if len(fields) == 0 {
		return nil, nil
	}
	value, err := json.Marshal(fields[0].value)
	if err != nil {
		return []byte("{[json encode error]}"), fmt.Errorf("logx: encode traceId: %w", err)
	}
	if len(value) >= 2 && value[0] == '"' && value[len(value)-1] == '"' {
		value = value[1 : len(value)-1]
	}
	data = append(data, '{')
	data = append(data, value...)
	data = append(data, '}')
	return
}
