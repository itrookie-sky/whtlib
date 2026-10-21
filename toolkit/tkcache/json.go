package tkcache

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"time"

	"github.com/itrookie-sky/whtlib/extension/reflectx"
)

// GetString 读取文本，空文本仍为有效命中。
func (c *Cache) GetString(ctx context.Context, key string) (value string, found bool, err error) {
	data, found, err := c.Get(ctx, key)
	return string(data), found, err
}

// SetString 保存原始文本，不添加 JSON 引号。
func (c *Cache) SetString(ctx context.Context, key, value string, ttl time.Duration) (err error) {
	return c.Set(ctx, key, []byte(value), ttl)
}

// SetJSON 编码成功后才替换缓存。
func (c *Cache) SetJSON(ctx context.Context, key string, value any, ttl time.Duration) (err error) {
	data, err := json.Marshal(value)
	if err != nil {
		return
	}
	return c.Set(ctx, key, data, ttl)
}

func decodeJSON[T any](data []byte) (value T, err error) {
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.UseNumber()
	err = decoder.Decode(&value)
	if err != nil {
		return
	}
	var extra any
	trailingErr := decoder.Decode(&extra)
	if !errors.Is(trailingErr, io.EOF) {
		if trailingErr == nil {
			err = fmt.Errorf("tkcache: multiple JSON values")
		} else {
			err = trailingErr
		}
	}
	return
}

// GetJSON 按指定类型解码；命中损坏 JSON 时 found 仍为 true。
func GetJSON[T any](ctx context.Context, cache *Cache, key string) (value T, found bool, err error) {
	if cache == nil {
		return value, false, ErrInvalidArgument
	}
	data, found, err := cache.Get(ctx, key)
	if err != nil || !found {
		return
	}
	value, err = decodeJSON[T](data)
	return
}

// GetOrLoadJSON 通过显式目标类型统一两种后端的对象语义。
func GetOrLoadJSON[T any](ctx context.Context, cache *Cache, key string, ttl time.Duration, loader func(ctx context.Context) (value T, err error), options ...LoadOption) (value T, err error) {
	if cache == nil || loader == nil {
		return value, ErrInvalidArgument
	}
	config := buildLoadOptions(options)
	data, err := cache.GetOrLoad(ctx, key, ttl, func(loadCtx context.Context) (data []byte, err error) {
		result, err := loader(loadCtx)
		if err != nil {
			return
		}
		if !config.cacheEmpty && reflectx.IsNil(result) {
			return
		}
		return json.Marshal(result)
	}, options...)
	if err != nil {
		return
	}
	// nil 表示本轮发起者跳过空值回填，等待者沿用该结果，不用自身选项重新解释。
	if data == nil {
		return
	}
	return decodeJSON[T](data)
}
