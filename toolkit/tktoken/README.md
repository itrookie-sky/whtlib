# tktoken

与框架无关的有状态令牌管理。核心只依赖标准库，负责生成、校验、按阈值续期和撤销；存储及 HTTP 路由由调用方显式提供。

```go
import (
    "encoding/base64"
    "net/http"
    "os"

    "github.com/itrookie-sky/whtlib/toolkit/tktoken"
)

func buildToken() (manager *tktoken.Token, err error) {
    // 密钥由部署配置提供，解码后必须为 16、24 或 32 字节。
    key, err := base64.StdEncoding.DecodeString(os.Getenv("TOKEN_KEY_BASE64"))
    if err != nil {
        return
    }
    store, err := tktoken.NewFileStore("/var/lib/myapp/token-sessions.json")
    if err != nil {
        return
    }
    manager, err = tktoken.NewLegacy(tktoken.Config{
        CacheMode: tktoken.CacheModeFile,
        CacheKey: "myapp:sessions:",
        EncryptKey: key,
        Timeout: 60 * 60 * 1000,
        MaxRefresh: 30 * 60 * 1000,
        TokenDelimiter: "_",
        MultiLogin: false,
    }, store)
    return
}

func protect(manager *tktoken.Token, next http.Handler) (handler http.Handler) {
    return manager.Middleware(tktoken.HTTPOptions{
        Paths: []string{"/api/*"},
        ExcludePaths: []string{"/api/login"},
    })(next)
}
```

文件父目录必须已存在。`tkcache.Cache` 可直接作为 `Store` 注入；它的内存、Redis 后端选择及 Namespace 由调用方完成。核心不会创建、关闭或扫描借入的存储。

## 核心接口

| 接口 | 行为 |
| --- | --- |
| `New(config, store)` | 要求显式提供 `Config.Codec` 和 `Store`。 |
| `NewLegacy(config, store)` | 显式选择旧协议，使用 `EncryptKey`、`TokenDelimiter` 创建 `LegacyCodec`。 |
| `GenToken(ctx, userKey, data)` | 返回 `IssuedToken`，业务数据经 JSON 编码保存。 |
| `ValidToken(ctx, value)` | 返回 `Session`，身份匹配后才按阈值原子续期。 |
| `RemoveToken(ctx, value)` | 比较当前会话再原子撤销。已失效或不存在的身份返回错误。 |
| `Session.DecodeData(target)` | 显式解码业务数据；动态对象的数字使用 `json.Number`。 |

`MultiLogin=true` 保留源模块的实际行为：同一用户复用现有会话、业务数据和相同 token，到达刷新阈值时续期。`false` 每次登录创建新 UUID 并替换旧会话；它不表示按设备保存多份会话。并发首次登录通过条件创建决定保留的会话。

`Session.Data` 为 `json.RawMessage`，保留 JSON 数字文本和 `null`。`CreateTime` 为创建时间，`LastRefreshTime` 为最后续期时间，`RefreshTime` 为下一次允许续期的时间，`ExpiresAt` 为绝对到期时间，均使用 Unix 毫秒。续期保持创建时间。

存储必须提供原子的 `SetIfAbsent`、`CompareAndSwap`、`CompareAndDelete`。先比较用户和 UUID，再比较读取的完整字节进行修改；竞争失败后重新读取，禁止旧令牌覆盖或撤销新会话。同一操作连续竞争 32 次仍未成功返回 `ErrConflict`，调用方可按业务策略重试。

使用 `errors.Is` 判断 `ErrUnauthorized`、`ErrNotFound`、`ErrInvalidConfig`、`ErrInvalidSession`、`ErrConflict`。缺失或已过期的会话同时匹配 `ErrUnauthorized` 和 `ErrNotFound`；被其他登录替换的令牌匹配 `ErrUnauthorized`。存储错误及上下文取消向上传递。

## 配置与旧协议

| 配置 | 类型及单位 | 默认与迁移说明 |
| --- | --- | --- |
| `CacheMode` | `int8` | 保留 Cache=1、Redis=2、File=3；实际后端完全由传入的 Store 决定，0 也可接受。 |
| `CacheKey` | `string` | 默认 `tktoken:`，实际键为前缀与 userKey 拼接。 |
| `Timeout` | `int`，毫秒 | 0 使用 10 天；构造时检查正数及 duration 范围。 |
| `MaxRefresh` | `int`，毫秒 | 0 使用超时的一半，最小 1 毫秒；不得大于 Timeout。 |
| `TokenDelimiter` | `string` | 仅 `NewLegacy` 使用，空值默认 `_`。 |
| `EncryptKey` | `[]byte` | 仅 `NewLegacy` 使用，必须显式提供；无固定默认密钥。 |
| `MultiLogin` | `bool` | 默认 false，每次登录替换会话。 |
| `MiddlewareType` | `uint` | 保留 Group=1、Bind=2、Global=3 的配置值；新 HTTP 层由调用方注册。 |
| `Codec` | `TokenCodec` | `New` 必填；实现须支持并发，并对相同身份产生相同字符串。 |

`LegacyCodec` 使用标准库复现已核对的 GoFrame v2.9.0 `gaes`：CBC、严格 PKCS7、默认 IV 和标准 Base64，明文为 `userKey + delimiter + uuid`。新 UUID 使用 `crypto/rand` 的 16 字节转 32 位十六进制。用户标识或 UUID 包含分隔符时明确报错，避免旧解析规则的歧义。固定 IV 和无独立认证标签是旧协议限制；该入口只用于已有协议的兼容，不替换为 JWT，也不复用 `toolkit/tkaes` 的 ECB 实现。

```go
codec, err := tktoken.NewLegacyCodec(key, "_")
// 检查 err 后可独立调用 codec.Encode(userKey, uuid)、codec.Decode(value)。
// 自定义协议则实现 TokenCodec，并传入 Config.Codec。
```

令牌字符串兼容与会话存储兼容分开处理。包不会自动读取、改写或导入旧 Redis key、旧 `.dat` 文件。旧部署原有 `pltoken:` 前缀如需使用，必须显式配置 `CacheKey`，并自行验收会话 JSON、TTL 与 tkcache Namespace 的组合映射；仅设置前缀不能保证旧会话无缝复用。

## HTTP 迁移

`manager.Middleware(options)` 返回 `func(http.Handler) http.Handler`。默认只读取 `Authorization: Bearer ...`；只有 `AllowQueryToken=true` 才读取查询参数，参数名默认 `token`，可通过 `QueryParameter` 更换。已有无效认证头不会降级使用查询参数。

`Paths` 为空时检查全部路径。路径支持精确匹配及 `/*` 结尾的目录前缀，`/api/*` 匹配 `/api` 和 `/api/item`，不会匹配 `/api2`。`ExcludePaths` 优先，可独立调用 `MatchPath`、`RequiresAuthentication`。`ShouldAuthenticate` 是路径匹配后的可选过滤回调。

通过 `SessionFromContext(request.Context())` 获取已验证会话。默认认证失败返回 401，存储等内部错误返回 500；`OnFailure` 可自定义业务响应。核心不从 ctx 反查任何框架请求。

| 源能力 | 新入口 |
| --- | --- |
| `GenToken`、`ValidToken`、`RemoveToken` | 同名核心方法，具名结果和 error 替代 `response.Resp`。 |
| `EncryptToken`、`DecryptToken` | 显式选择 `TokenCodec.Encode`、`TokenCodec.Decode`。 |
| `GetTokenData` | `ExtractToken` 后调用 `ValidToken`，或中间件验证后读取上下文会话。 |
| `RemoveRequestToken` | `ExtractToken` 后调用 `RemoveToken`。 |
| `AuthPath` | 独立 `MatchPath`、`RequiresAuthentication`。 |
| `InitConfig` | `New`、`NewLegacy` 构造时校验和默认配置。 |
| Group、Bind、Global | 由调用方将标准中间件注册到对应路由范围。 |
| 源 HTTP 回调、响应结构、日志 | 使用 `HTTPOptions` 和应用层响应；本包不记录 token、密钥或完整会话。 |

## 文件存储

`FileStore` 仅持久化本实例条目，JSON 快照带版本及绝对到期时间。每次写操作使用同目录 0600 临时文件、同步文件内容、原子重命名；失败写入不会改变有效内存会话。快照本身权限也是 0600。加载时跳过已过期条目，重启不会重新授予完整 TTL。

每个快照文件只允许一个 FileStore 实例持有；互斥锁仅保护本实例线程，不提供多进程文件锁。文件不保持打开，也无后台保存任务，无需 Close。原子替换保证完整快照可见；不额外承诺断电后的目录项持久性。

`Store` 的零 TTL 表示永久保存，负 TTL 无效；Token 创建的会话始终使用正 TTL。文件存储拒绝直接打开符号链接或非普通文件，不接受旧 `.dat` 的无版本 JSON。导入旧会话需由调用方单独实现明确的到期策略。

## 离线验证

```sh
go test -race ./toolkit/tktoken
go vet ./toolkit/tktoken
```

测试不连接真实 MySQL、Redis 或 Kafka；覆盖独立 OpenSSL 协议向量、错误填充、配置、数字精度、MultiLogin、并发 CAS、旧令牌失效、HTTP 提取与路径规则，以及文件绝对到期、权限、原子比较和失败写入。
