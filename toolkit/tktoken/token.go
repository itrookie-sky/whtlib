// Package tktoken 提供与框架无关的有状态令牌及会话管理。
package tktoken

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"time"
)

const (
	CacheModeCache int8 = 1
	CacheModeRedis int8 = 2
	CacheModeFile  int8 = 3

	MiddlewareTypeGroup  uint = 1
	MiddlewareTypeBind   uint = 2
	MiddlewareTypeGlobal uint = 3

	DefaultTimeout        = 10 * 24 * 60 * 60 * 1000
	DefaultCacheKey       = "tktoken:"
	DefaultTokenDelimiter = "_"
	maxAttempts           = 32
)

var (
	ErrUnauthorized   = errors.New("tktoken: unauthorized")
	ErrNotFound       = errors.New("tktoken: session not found")
	ErrInvalidConfig  = errors.New("tktoken: invalid configuration")
	ErrInvalidSession = errors.New("tktoken: invalid session")
	ErrConflict       = errors.New("tktoken: concurrent session update")
)

// Store 是会话消费方所需的最小存储接口；比较操作必须由 Store 原子执行。
//
// 读取、写入的字节不得由调用方与存储共享可变底层数组。
// ttl 为零表示永久保存，负值无效；不存在的键不能通过比较操作创建。
type Store interface {
	Get(ctx context.Context, key string) (value []byte, found bool, err error)
	Set(ctx context.Context, key string, value []byte, ttl time.Duration) (err error)
	SetIfAbsent(ctx context.Context, key string, value []byte, ttl time.Duration) (stored bool, err error)
	CompareAndSwap(ctx context.Context, key string, expected, value []byte, ttl time.Duration) (swapped bool, err error)
	CompareAndDelete(ctx context.Context, key string, expected []byte) (deleted bool, err error)
}

// Identity 是令牌中编码的用户和会话身份。
type Identity struct {
	UserKey string `json:"userKey"`
	UUID    string `json:"uuid"`
}

// TokenCodec 隔离令牌字符串协议，不负责会话存储或到期处理。
//
// 实现必须支持并发调用，Encode 对同一身份返回相同字符串，确保多端登录复用令牌。
type TokenCodec interface {
	Encode(userKey, uuid string) (value string, err error)
	Decode(value string) (identity Identity, err error)
}

// Config 保留原基础字段的类型；Timeout、MaxRefresh 的单位均为毫秒。
//
// CacheMode 仅保留配置映射，实际存储由传入的 Store 决定。
// Codec 必须显式选择；NewLegacy 使用 EncryptKey、TokenDelimiter 构建旧协议。
// MiddlewareType 仅保留路由注册方式的兼容值，HTTP 路由由调用方注册。
type Config struct {
	CacheMode      int8
	CacheKey       string
	Timeout        int
	MaxRefresh     int
	TokenDelimiter string
	EncryptKey     []byte
	MultiLogin     bool
	MiddlewareType uint
	Codec          TokenCodec
}

// Session 保存会话；Data 保留原始 JSON，避免默认转换为 float64。
//
// 时间字段均为 Unix 毫秒。RefreshTime 是下一次允许续期的时间；
// LastRefreshTime 单独记录最后续期时间，续期不会修改 CreateTime。
type Session struct {
	UserKey         string          `json:"userKey"`
	UUID            string          `json:"uuid"`
	Data            json.RawMessage `json:"data"`
	CreateTime      int64           `json:"createTime"`
	RefreshTime     int64           `json:"refreshTime"`
	LastRefreshTime int64           `json:"lastRefreshTime"`
	ExpiresAt       int64           `json:"expiresAt"`
}

// DecodeData 将业务数据显式解码到目标对象；interface 目标中的数字使用 json.Number。
func (s Session) DecodeData(target any) (err error) {
	decoder := json.NewDecoder(bytes.NewReader(s.Data))
	decoder.UseNumber()
	err = decoder.Decode(target)
	return
}

// IssuedToken 包含令牌字符串及其对应的会话快照。
type IssuedToken struct {
	Token   string  `json:"token"`
	Session Session `json:"session"`
}

// Token 管理会话，允许并发使用；借入的 Store 和 Codec 由调用方管理生命周期。
type Token struct {
	store      Store
	codec      TokenCodec
	cacheKey   string
	timeout    time.Duration
	maxRefresh time.Duration
	multiLogin bool
	now        func() time.Time
}

// New 使用显式协议和存储创建令牌管理器，不自动创建或关闭存储。
func New(config Config, store Store) (token *Token, err error) {
	if store == nil || config.Codec == nil {
		err = fmt.Errorf("%w: store and codec are required", ErrInvalidConfig)
		return
	}
	if config.CacheMode < 0 || config.CacheMode > CacheModeFile {
		err = fmt.Errorf("%w: unsupported cache mode", ErrInvalidConfig)
		return
	}
	if config.Timeout == 0 {
		config.Timeout = DefaultTimeout
	}
	if config.MaxRefresh == 0 {
		config.MaxRefresh = max(1, config.Timeout/2)
	}
	if config.Timeout < 1 || int64(config.Timeout) > math.MaxInt64/int64(time.Millisecond) {
		err = fmt.Errorf("%w: timeout is outside duration range", ErrInvalidConfig)
		return
	}
	if config.MaxRefresh < 1 || config.MaxRefresh > config.Timeout {
		err = fmt.Errorf("%w: refresh interval must be within timeout", ErrInvalidConfig)
		return
	}
	if config.CacheKey == "" {
		config.CacheKey = DefaultCacheKey
	}
	token = &Token{
		store: store, codec: config.Codec, cacheKey: config.CacheKey,
		timeout:    time.Duration(config.Timeout) * time.Millisecond,
		maxRefresh: time.Duration(config.MaxRefresh) * time.Millisecond,
		multiLogin: config.MultiLogin, now: time.Now,
	}
	return
}

// NewLegacy 显式选择旧 CBC 协议，并使用配置中的密钥和分隔符创建管理器。
func NewLegacy(config Config, store Store) (token *Token, err error) {
	codec, err := NewLegacyCodec(config.EncryptKey, config.TokenDelimiter)
	if err != nil {
		return
	}
	config.Codec = codec
	token, err = New(config, store)
	return
}

// GenToken 生成令牌；MultiLogin 为 true 时复用现存会话及业务数据。
func (t *Token) GenToken(ctx context.Context, userKey string, data any) (issued IssuedToken, err error) {
	if userKey == "" {
		err = fmt.Errorf("%w: user key is empty", ErrInvalidConfig)
		return
	}
	var identity Identity
	var value string
	var encodedData []byte
	key := t.cacheKey + userKey
	for range maxAttempts {
		err = ctx.Err()
		if err != nil {
			return
		}
		raw, found, getErr := t.store.Get(ctx, key)
		if getErr != nil {
			err = getErr
			return
		}
		if found && t.multiLogin {
			session, decodeErr := decodeSession(raw, userKey)
			if decodeErr != nil {
				err = decodeErr
				return
			}
			if session.ExpiresAt == 0 || session.ExpiresAt > t.now().UnixMilli() {
				value, err = t.codec.Encode(session.UserKey, session.UUID)
				if err != nil {
					return
				}
				// 校验和续期仍经过身份比较与 CAS，不能回写被替换的会话。
				issued.Session, err = t.ValidToken(ctx, value)
				if errors.Is(err, ErrNotFound) {
					continue
				}
				if err != nil {
					return
				}
				issued.Token = value
				return
			}
		}
		if identity.UUID == "" {
			identity, err = randomIdentity(userKey)
			if err != nil {
				return
			}
			value, err = t.codec.Encode(identity.UserKey, identity.UUID)
			if err != nil {
				return
			}
			encodedData, err = json.Marshal(data)
			if err != nil {
				return
			}
		}
		now := t.now()
		session := Session{
			UserKey: userKey, UUID: identity.UUID, Data: encodedData,
			CreateTime: now.UnixMilli(), LastRefreshTime: now.UnixMilli(),
			RefreshTime: now.Add(t.maxRefresh).UnixMilli(), ExpiresAt: now.Add(t.timeout).UnixMilli(),
		}
		next, encodeErr := json.Marshal(session)
		if encodeErr != nil {
			err = encodeErr
			return
		}
		var stored bool
		if found {
			stored, err = t.store.CompareAndSwap(ctx, key, raw, next, t.timeout)
		}
		if !found {
			stored, err = t.store.SetIfAbsent(ctx, key, next, t.timeout)
		}
		if err != nil {
			return
		}
		if stored {
			issued = IssuedToken{Token: value, Session: session}
			return
		}
	}
	err = ErrConflict
	return
}

// ValidToken 先校验会话身份，再按刷新阈值原子续期。
func (t *Token) ValidToken(ctx context.Context, value string) (session Session, err error) {
	identity, err := t.decode(value)
	if err != nil {
		return
	}
	key := t.cacheKey + identity.UserKey
	for range maxAttempts {
		err = ctx.Err()
		if err != nil {
			return
		}
		raw, current, loadErr := t.load(ctx, key, identity)
		if loadErr != nil {
			err = loadErr
			return
		}
		now := t.now()
		if current.RefreshTime > now.UnixMilli() {
			session = current
			return
		}
		current.RefreshTime = now.Add(t.maxRefresh).UnixMilli()
		current.LastRefreshTime = now.UnixMilli()
		current.ExpiresAt = now.Add(t.timeout).UnixMilli()
		next, encodeErr := json.Marshal(current)
		if encodeErr != nil {
			err = encodeErr
			return
		}
		swapped, swapErr := t.store.CompareAndSwap(ctx, key, raw, next, t.timeout)
		if swapErr != nil {
			err = swapErr
			return
		}
		if swapped {
			session = current
			return
		}
	}
	err = ErrConflict
	return
}

// RemoveToken 仅撤销令牌所代表的当前会话，旧令牌不能删除新登录会话。
func (t *Token) RemoveToken(ctx context.Context, value string) (err error) {
	identity, err := t.decode(value)
	if err != nil {
		return
	}
	key := t.cacheKey + identity.UserKey
	for range maxAttempts {
		err = ctx.Err()
		if err != nil {
			return
		}
		raw, _, loadErr := t.load(ctx, key, identity)
		if loadErr != nil {
			err = loadErr
			return
		}
		deleted, deleteErr := t.store.CompareAndDelete(ctx, key, raw)
		if deleteErr != nil {
			err = deleteErr
			return
		}
		if deleted {
			return
		}
	}
	err = ErrConflict
	return
}

func (t *Token) decode(value string) (identity Identity, err error) {
	identity, err = t.codec.Decode(value)
	if err != nil || identity.UserKey == "" || identity.UUID == "" {
		// 不回传底层解码错误，避免自定义 Codec 将敏感令牌拼入错误。
		err = ErrUnauthorized
	}
	return
}

func (t *Token) load(ctx context.Context, key string, identity Identity) (raw []byte, session Session, err error) {
	raw, found, err := t.store.Get(ctx, key)
	if err != nil {
		return
	}
	if !found {
		err = errors.Join(ErrUnauthorized, ErrNotFound)
		return
	}
	session, err = decodeSession(raw, identity.UserKey)
	if err != nil {
		return
	}
	if session.UUID != identity.UUID {
		err = ErrUnauthorized
		return
	}
	if session.ExpiresAt > 0 && session.ExpiresAt <= t.now().UnixMilli() {
		err = errors.Join(ErrUnauthorized, ErrNotFound)
		return
	}
	return
}

func decodeSession(raw []byte, userKey string) (session Session, err error) {
	err = json.Unmarshal(raw, &session)
	if err != nil {
		err = ErrInvalidSession
		return
	}
	if session.UserKey != userKey || session.UUID == "" {
		err = ErrInvalidSession
	}
	return
}

func randomIdentity(userKey string) (identity Identity, err error) {
	var random [16]byte
	_, err = rand.Read(random[:])
	if err != nil {
		return
	}
	identity = Identity{UserKey: userKey, UUID: hex.EncodeToString(random[:])}
	return
}
