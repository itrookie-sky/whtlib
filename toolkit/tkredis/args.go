package tkredis

import (
	"encoding"
	"encoding/json"
	"reflect"
	"slices"

	"github.com/itrookie-sky/whtlib/extension/reflectx"
)

// JSONValue 标记应使用 JSON 编码的命令参数。
type JSONValue struct {
	Value any
}

// JSON 显式标记一个 JSON 参数。
func JSON(value any) (argument JSONValue) {
	return JSONValue{Value: value}
}

func explicitArgs(args []any) (converted []any, err error) {
	converted = slices.Clone(args)
	for i, arg := range converted {
		value, ok := arg.(JSONValue)
		if !ok {
			continue
		}
		converted[i], err = json.Marshal(value.Value)
		if err != nil {
			err = argumentEncodingError(i, err)
			return
		}
	}
	return
}

// EncodeArgs 复制参数切片，将 start 后的结构体、map、非字节切片和数组转为 JSON。
// 支持 encoding.BinaryMarshaler 的参数保持原生编码；不会修改调用方切片。
func EncodeArgs(start int, args ...any) (converted []any, err error) {
	if start < 0 || start > len(args) {
		err = invalidArgument("argument start index out of range")
		return
	}
	converted, err = explicitArgs(args)
	if err != nil {
		return
	}
	for i := start; i < len(converted); i++ {
		arg := converted[i]
		_, isBytes := arg.([]byte)
		_, isMarshaler := arg.(encoding.BinaryMarshaler)
		if isBytes || isMarshaler {
			continue
		}
		info := reflectx.OriginTypeAndKind(arg)
		switch info.OriginKind {
		case reflect.Struct, reflect.Map, reflect.Slice, reflect.Array:
			converted[i], err = json.Marshal(arg)
			if err != nil {
				err = argumentEncodingError(i, err)
				return
			}
		}
	}
	return
}
