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
	fields := make([]jsonField, 0, 1+len(config.ContextFields))
	fields = appendContextField(ctx, fields, "traceId", config.TraceKey)
	for _, field := range config.ContextFields {
		fields = appendContextField(ctx, fields, field.Name, field.Key)
	}
	return encodeFields(fields)
}
