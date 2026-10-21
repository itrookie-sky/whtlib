package tkkafka

import (
	"encoding/json"
	"fmt"
)

// Encoder 显式定义生产消息的编码协议；实现必须支持并发调用。
type Encoder interface {
	Encode(value any) (data []byte, err error)
}

// EncoderFunc 将函数适配为 Encoder。
type EncoderFunc func(value any) (data []byte, err error)

// Encode 调用编码函数。
func (f EncoderFunc) Encode(value any) (data []byte, err error) {
	return f(value)
}

// JSONEncoder 使用标准库 JSON，不删除或改写业务字段。
type JSONEncoder struct{}

// Encode 将原始数据编码为 JSON。
func (JSONEncoder) Encode(value any) (data []byte, err error) {
	return json.Marshal(value)
}

// LegacyEncoder 显式过滤旧协议的六个顶层数据库时间字段。
// 输入需可编码为 JSON 对象，字段名遵循 encoding/json；这不复现 gconv 的任意对象转换。
type LegacyEncoder struct{}

// Encode 在独立 JSON 对象中删除旧时间字段，不修改调用方的 map 或结构体。
func (LegacyEncoder) Encode(value any) (data []byte, err error) {
	data, err = json.Marshal(value)
	if err != nil {
		return
	}
	var object map[string]json.RawMessage
	err = json.Unmarshal(data, &object)
	if err != nil {
		err = fmt.Errorf("tkkafka: legacy encoder requires a JSON object: %w", err)
		return
	}
	for _, field := range []string{"created_at", "updated_at", "deleted_at", "create_time", "update_time", "delete_time"} {
		delete(object, field)
	}
	data, err = json.Marshal(object)
	return
}
