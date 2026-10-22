package tkredis

import (
	"context"
	"errors"
	"math"
	"testing"
	"time"
)

func TestStringTTLAndScriptReload(t *testing.T) {
	client, server := newTestClient(t)
	ctx := t.Context()
	_, found, err := client.Get(ctx, "missing")
	if err != nil || found {
		t.Fatal("读取缺失键", err)
	}
	ttl, err := client.PTTL(ctx, "missing")
	if err != nil || ttl != TTLNotFound {
		t.Fatal("缺失哨兵错误", ttl, err)
	}
	err = client.Set(ctx, "key", nil, NoExpiration)
	if err != nil {
		t.Fatal(err)
	}
	value, found, err := client.Get(ctx, "key")
	if err != nil || !found || len(value) != 0 {
		t.Fatal("空内容必须与未命中区分", err)
	}
	ttl, err = client.PTTL(ctx, "key")
	if err != nil || ttl != NoExpiration {
		t.Fatal("永久哨兵错误", ttl, err)
	}
	value, stored, err := client.GetOrSet(ctx, "key", []byte("candidate"), 0)
	if err != nil || stored || len(value) != 0 {
		t.Fatal("GetOrSet 命中不应改写空值", err)
	}
	_, err = client.Do(ctx, "SCRIPT", "FLUSH")
	if err != nil {
		t.Fatal(err)
	}
	swapped, err := client.CompareAndSwap(ctx, "key", nil, []byte("reloaded"), NoExpiration)
	if err != nil || !swapped {
		t.Fatal("NOSCRIPT 后应重载通用字符串脚本", err)
	}
	value, found, err = client.GetAndDelete(ctx, "key")
	if err != nil || !found || string(value) != "reloaded" {
		t.Fatal("原子读后删除错误", err)
	}
	err = client.Set(ctx, "short", []byte("value"), time.Nanosecond)
	if err != nil {
		t.Fatal(err)
	}
	ttl, err = client.PTTL(ctx, "short")
	if err != nil || ttl != time.Millisecond {
		t.Fatal("正数期限应向上取整到毫秒", ttl, err)
	}
	server.FastForward(time.Millisecond)
	ttl, err = client.PTTL(ctx, "short")
	if err != nil || ttl != TTLNotFound {
		t.Fatal("过期后应返回缺失哨兵", ttl, err)
	}
}

func TestNormalizeTTLSentinelsAndOverflow(t *testing.T) {
	maximum := time.Duration(math.MaxInt64/int64(time.Millisecond)) * time.Millisecond
	for _, test := range []struct {
		input time.Duration
		want  time.Duration
	}{
		{input: NoExpiration, want: NoExpiration},
		{input: 0, want: 0},
		{input: time.Nanosecond, want: time.Millisecond},
		{input: time.Millisecond + 1, want: 2 * time.Millisecond},
		{input: maximum, want: maximum},
	} {
		got, err := NormalizeTTL(test.input)
		if err != nil || got != test.want {
			t.Fatalf("TTL %v: got %v, %v", test.input, got, err)
		}
	}
	for _, ttl := range []time.Duration{TTLNotFound, -time.Second, time.Duration(math.MaxInt64)} {
		_, err := NormalizeTTL(ttl)
		if !errors.Is(err, ErrInvalidTTL) {
			t.Fatal("无效或溢出期限必须被拒绝", ttl, err)
		}
	}
}

func TestStringWrongTypeAndClientClose(t *testing.T) {
	client, _ := newTestClient(t)
	ctx := t.Context()
	_, err := client.Do(ctx, "LPUSH", "list", "value")
	if err != nil {
		t.Fatal(err)
	}
	stored, err := client.SetIfAbsent(ctx, "list", nil, NoExpiration)
	if err != nil || stored {
		t.Fatal("NX 应保留任意类型的已有键", err)
	}
	_, _, err = client.Get(ctx, "list")
	if err == nil {
		t.Fatal("类型错误不能转成未命中")
	}
	err = client.Set(ctx, "list", nil, 0)
	if err != nil {
		t.Fatal("零 TTL 应删除已有键", err)
	}
	err = client.Close()
	if err != nil {
		t.Fatal(err)
	}
	for _, run := range []func() (err error){
		func() (err error) { return client.Set(ctx, "key", nil, NoExpiration) },
		func() (err error) { _, _, err = client.Get(ctx, "key"); return },
		func() (err error) { _, err = client.PTTL(ctx, "key"); return },
		func() (err error) { _, err = client.SetIfAbsent(ctx, "key", nil, NoExpiration); return },
		func() (err error) { _, err = client.CompareAndSwap(ctx, "key", nil, nil, NoExpiration); return },
		func() (err error) { return client.DeletePrefix(ctx, "prefix:") },
	} {
		err = run()
		if !errors.Is(err, ErrClosed) {
			t.Fatal("操作必须经过 tkredis 生命周期检查", err)
		}
	}
}

func TestDeleteLiteralPrefix(t *testing.T) {
	client, _ := newTestClient(t)
	ctx := t.Context()
	for _, key := range []string{"literal[1]*?:a", "literal[1]*?:b", "literal1:keep", "other:keep"} {
		err := client.Set(ctx, key, []byte("value"), NoExpiration)
		if err != nil {
			t.Fatal(err)
		}
	}
	err := client.DeletePrefix(ctx, "literal[1]*?:")
	if err != nil {
		t.Fatal(err)
	}
	for _, test := range []struct {
		key   string
		found bool
	}{
		{key: "literal[1]*?:a"}, {key: "literal[1]*?:b"},
		{key: "literal1:keep", found: true}, {key: "other:keep", found: true},
	} {
		_, found, getErr := client.Get(ctx, test.key)
		if getErr != nil || found != test.found {
			t.Fatal("字面量前缀删除范围错误", test.key, found, getErr)
		}
	}
	err = client.DeletePrefix(ctx, "")
	if err == nil {
		t.Fatal("空前缀不能清空整个数据库")
	}
	canceled, cancel := context.WithCancel(ctx)
	cancel()
	err = client.DeletePrefix(canceled, "other:")
	if !errors.Is(err, context.Canceled) {
		t.Fatal("前缀删除应响应上下文取消", err)
	}
}
