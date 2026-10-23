// Package reflectx 提供标准库 reflect 的类型、取值和字段操作扩展。
package reflectx

import (
	"reflect"
	"strings"
)

// OriginValueAndKindOutput 保留输入与指针解引用后的反射值。
type OriginValueAndKindOutput struct {
	InputValue  reflect.Value
	InputKind   reflect.Kind
	OriginValue reflect.Value
	OriginKind  reflect.Kind
}

// OriginTypeAndKindOutput 保留输入与指针解引用后的反射类型。
type OriginTypeAndKindOutput struct {
	InputType  reflect.Type
	InputKind  reflect.Kind
	OriginType reflect.Type
	OriginKind reflect.Kind
}

// OriginValueAndKind 逐层解引用指针，nil 指针产生无效的原始值。
func OriginValueAndKind(value any) (out OriginValueAndKindOutput) {
	out.InputValue, _ = value.(reflect.Value)
	_, reflected := value.(reflect.Value)
	if !reflected {
		out.InputValue = reflect.ValueOf(value)
	}
	out.InputKind = out.InputValue.Kind()
	out.OriginValue = out.InputValue
	for out.OriginValue.Kind() == reflect.Pointer {
		out.OriginValue = out.OriginValue.Elem()
	}
	out.OriginKind = out.OriginValue.Kind()
	return
}

// OriginTypeAndKind 逐层解引用指针类型，安全处理无效反射值。
func OriginTypeAndKind(value any) (out OriginTypeAndKindOutput) {
	if value == nil {
		return
	}
	switch v := value.(type) {
	case reflect.Type:
		out.InputType = v
	case reflect.Value:
		if !v.IsValid() {
			return
		}
		out.InputType = v.Type()
	default:
		out.InputType = reflect.TypeOf(value)
	}
	if out.InputType == nil {
		return
	}
	out.InputKind = out.InputType.Kind()
	out.OriginType = out.InputType
	for out.OriginType.Kind() == reflect.Pointer {
		out.OriginType = out.OriginType.Elem()
	}
	out.OriginKind = out.OriginType.Kind()
	return
}

// ValueToInterface 安全取得反射值；不可直接访问的基础标量按标准库类型返回。
func ValueToInterface(v reflect.Value) (value any, ok bool) {
	if !v.IsValid() {
		return
	}
	if v.CanInterface() {
		return v.Interface(), true
	}
	switch v.Kind() {
	case reflect.Bool:
		return v.Bool(), true
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		return v.Int(), true
	case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64, reflect.Uintptr:
		return v.Uint(), true
	case reflect.Float32, reflect.Float64:
		return v.Float(), true
	case reflect.Complex64, reflect.Complex128:
		return v.Complex(), true
	case reflect.String:
		return v.String(), true
	case reflect.Pointer, reflect.Interface:
		return ValueToInterface(v.Elem())
	}
	return
}

// IsNil 判断真正的 nil 和无效反射值，不将零值或非 nil 空集合视为 nil。
func IsNil(value any) (isNil bool) {
	if value == nil {
		return true
	}
	rv, reflected := value.(reflect.Value)
	if !reflected {
		rv = reflect.ValueOf(value)
	}
	if !rv.IsValid() {
		return true
	}
	switch rv.Kind() {
	case reflect.Chan, reflect.Func, reflect.Interface, reflect.Map, reflect.Pointer, reflect.Slice, reflect.UnsafePointer:
		return rv.IsNil()
	}
	return false
}

// GetFieldByTag 按标签查找直接字段；空标签类别使用 json，空标签或 - 沿用字段名。
func GetFieldByTag(structVal reflect.Value, structTyp reflect.Type, tagName string, tag string) (field reflect.Value) {
	if structTyp == nil {
		return
	}
	for structTyp.Kind() == reflect.Pointer {
		structTyp = structTyp.Elem()
	}
	for structVal.Kind() == reflect.Pointer {
		structVal = structVal.Elem()
	}
	if structTyp.Kind() != reflect.Struct || structVal.Kind() != reflect.Struct || structVal.Type() != structTyp {
		return
	}
	if tag == "" {
		tag = "json"
	}
	for i := range structTyp.NumField() {
		definition := structTyp.Field(i)
		name := definition.Tag.Get(tag)
		if name == "" || name == "-" {
			name = definition.Name
		} else if tag == "json" {
			name, _, _ = strings.Cut(name, ",")
		}
		if name == tagName {
			return structVal.Field(i)
		}
	}
	return
}

// SetFieldValue 按 Go 可赋值或可转换规则更新字段，不改变字段声明类型。
func SetFieldValue(fieldVal reflect.Value, value any) (err error) {
	if !fieldVal.IsValid() || !fieldVal.CanSet() {
		return ErrNotSettable
	}
	if value == nil {
		switch fieldVal.Kind() {
		case reflect.Pointer, reflect.Map, reflect.Slice, reflect.Interface, reflect.Func, reflect.Chan, reflect.UnsafePointer:
			fieldVal.SetZero()
			return
		}
		return conversionError("nil to %s", fieldVal.Type())
	}
	input := reflect.ValueOf(value)
	if input.Type().AssignableTo(fieldVal.Type()) {
		fieldVal.Set(input)
		return
	}
	if !input.CanConvert(fieldVal.Type()) {
		return conversionError("%s to %s", input.Type(), fieldVal.Type())
	}
	fieldVal.Set(input.Convert(fieldVal.Type()))
	return
}
