package tktoken

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

// legacyIV 是 GoFrame v2.9.0 gaes 默认 IV，只用于兼容旧协议。
const legacyIV = "I Love Go Frame!"
