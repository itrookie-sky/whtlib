package reflectx

import (
	"errors"
	"reflect"
	"testing"
	"unsafe"
)

func TestOrigins(t *testing.T) {
	type number int
	value := number(7)
	pointer := &value
	input := &pointer
	values := OriginValueAndKind(reflect.ValueOf(input))
	if values.InputKind != reflect.Pointer || values.OriginKind != reflect.Int || values.OriginValue.Interface() != value {
		t.Fatalf("多级指针取值错误: %+v", values)
	}
	for _, input := range []any{input, reflect.ValueOf(input), reflect.TypeOf(input)} {
		result := OriginTypeAndKind(input)
		if result.InputKind != reflect.Pointer || result.OriginType != reflect.TypeOf(value) {
			t.Fatalf("多级指针类型错误: %+v", result)
		}
	}
	var absent *number
	if OriginValueAndKind(absent).OriginValue.IsValid() || OriginTypeAndKind(absent).OriginType != reflect.TypeOf(value) {
		t.Fatal("typed nil 的值和类型应分别处理")
	}
	for _, input := range []any{nil, reflect.Value{}} {
		result := OriginTypeAndKind(input)
		if result.InputType != nil || result.OriginKind != reflect.Invalid || OriginValueAndKind(input).OriginValue.IsValid() {
			t.Fatalf("无效输入应返回零结果: %+v", result)
		}
	}
	var wrapped any = pointer
	if OriginValueAndKind(reflect.ValueOf(&wrapped).Elem()).OriginKind != reflect.Interface {
		t.Fatal("不应自动解开 interface")
	}
}

func TestNilAndSafeValue(t *testing.T) {
	var pointer *int
	var slice []int
	var object map[string]int
	var channel chan int
	var function func()
	var raw unsafe.Pointer
	for _, input := range []any{nil, pointer, slice, object, channel, function, raw, reflect.Value{}, reflect.ValueOf(pointer)} {
		if !IsNil(input) {
			t.Errorf("应识别 nil: %T", input)
		}
	}
	for _, input := range []any{"", 0, false, []int{}, map[string]int{}, struct{}{}, [0]int{}} {
		if IsNil(input) {
			t.Errorf("有效零值不应视为 nil: %T", input)
		}
	}
	type named int
	exported, ok := ValueToInterface(reflect.ValueOf(named(3)))
	if !ok || exported != named(3) {
		t.Fatal("可访问值应保留动态类型")
	}
	private := reflect.ValueOf(struct {
		number int16
		text   string
		data   []int
	}{4, "text", []int{1}})
	number, ok := ValueToInterface(private.Field(0))
	if !ok || number != int64(4) {
		t.Fatalf("不可直接访问标量应安全读取: %v %v", number, ok)
	}
	text, ok := ValueToInterface(private.Field(1))
	if !ok || text != "text" {
		t.Fatal("不可直接访问字符串读取失败")
	}
	for _, input := range []reflect.Value{{}, private.Field(2)} {
		_, ok = ValueToInterface(input)
		if ok {
			t.Fatal("无效值或不可访问复合值不应强行读取")
		}
	}
}

func TestFieldLookupAndAssignment(t *testing.T) {
	type record struct {
		ID      int64  `json:"id,omitempty" db:"record_id"`
		Hidden  string `json:"-"`
		Default bool
		Extra   []byte
		private int
	}
	value := record{Extra: []byte("value")}
	rv := reflect.ValueOf(&value)
	rt := reflect.TypeOf(&value)
	for _, test := range []struct{ name, tag, field string }{
		{"id", "", "ID"}, {"record_id", "db", "ID"}, {"Hidden", "json", "Hidden"}, {"Default", "", "Default"},
	} {
		field := GetFieldByTag(rv, rt, test.name, test.tag)
		if !field.IsValid() || field != rv.Elem().FieldByName(test.field) {
			t.Errorf("标签字段查找失败: %+v", test)
		}
	}
	for _, field := range []reflect.Value{
		GetFieldByTag(rv, rt, "missing", ""),
		GetFieldByTag(reflect.ValueOf(1), reflect.TypeOf(1), "id", ""),
		GetFieldByTag(reflect.Value{}, rt, "id", ""),
		GetFieldByTag(rv, reflect.TypeOf(struct{}{}), "id", ""),
		GetFieldByTag(rv, nil, "id", ""),
	} {
		if field.IsValid() {
			t.Fatal("不匹配或无效输入应返回无效字段")
		}
	}
	field := GetFieldByTag(rv, rt, "id", "")
	err := SetFieldValue(field, int32(42))
	if err != nil || value.ID != 42 || field.Type() != reflect.TypeFor[int64]() {
		t.Fatalf("转换赋值应保留字段类型: %v", err)
	}
	err = SetFieldValue(rv.Elem().FieldByName("Extra"), nil)
	if err != nil || value.Extra != nil {
		t.Fatalf("nil 赋值失败: %v", err)
	}
	for _, input := range []any{nil, []int{1}} {
		err = SetFieldValue(field, input)
		if !errors.Is(err, ErrNotConvertible) || value.ID != 42 {
			t.Fatalf("非法转换应返回错误并保留原值: %v", err)
		}
	}
	for _, input := range []reflect.Value{{}, reflect.ValueOf(value).Field(0), rv.Elem().FieldByName("private")} {
		err = SetFieldValue(input, 1)
		if !errors.Is(err, ErrNotSettable) {
			t.Fatalf("不可赋值应返回错误: %v", err)
		}
	}
	var array [2]int
	err = SetFieldValue(reflect.ValueOf(&array).Elem(), []int{1})
	if !errors.Is(err, ErrNotConvertible) {
		t.Fatalf("长度不足的 slice 转 array 不应 panic: %v", err)
	}
}
