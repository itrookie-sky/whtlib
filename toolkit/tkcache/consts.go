package tkcache

import (
	"time"

	"github.com/itrookie-sky/whtlib/toolkit/tkredis"
)

// StoreType 指定实例使用的 Store 类型。
type StoreType string

const (
	MemoryStore StoreType = "memory"
	RedisStore  StoreType = "redis"
	FileStore   StoreType = "file"
)

const (
	// NoExpiration 表示永久保存，写入和 TTL 查询均使用该原始哨兵值。
	NoExpiration time.Duration = tkredis.NoExpiration
	// TTLNotFound 表示键不存在，仅用于 TTL 查询结果，不能用于写入。
	TTLNotFound time.Duration = tkredis.TTLNotFound
)

type operation string

const (
	opSet       operation = "set"
	opSetNX     operation = "nx"
	opGetDelete operation = "getdel"
	opGetExpire operation = "getexpire"
	opGetSet    operation = "getset"
	opCAS       operation = "cas"
	opCAD       operation = "cad"
)
