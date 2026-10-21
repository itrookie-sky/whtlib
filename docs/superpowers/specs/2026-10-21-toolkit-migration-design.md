# go-gf-lib 通用模块迁移设计

将 `go-gf-lib` 中的 `plcache`、`plredis`、`pltoken`、`plkafka` 沉淀到 `whtlib/toolkit`，移除 GoFrame 及源仓库内部依赖，保留通用能力，并明确配置、数据协议和运行行为的迁移关系。

状态：设计草案。已确认四个模块的迁移范围、`tk` 包名前缀，以及缓存同时支持内存和 Redis。本文记录源码核对结果与建议设计，当前不包含代码迁移。

## 源码基线与模块映射

本次核对的源仓库为 `/Users/wuhaotian/work/gopath/src/go-gf-lib`，HEAD 为 `1d80d96`，工作区无未提交改动。源模块使用 GoFrame v2.9.0，Redis 驱动为 `go-redis/v9`，Kafka 驱动为 `confluent-kafka-go/v2`。实施前需要再次核对源版本及工作区差异。

| 源模块 | 目标包 | 职责 |
| --- | --- | --- |
| `plugins/plredis` | `toolkit/tkredis` | Redis 客户端、命令、pipeline、Lua 脚本、锁和 Redis 特有数据结构。 |
| `plugins/plcache` | `toolkit/tkcache` | 统一缓存接口、内存与 Redis 后端、TTL、批量操作和回源。 |
| `plugins/pltoken` | `toolkit/tktoken` | token 编解码、会话存储、校验、续期、撤销及 HTTP 适配。 |
| `plugins/plkafka` | `toolkit/tkkafka` | Kafka 生产、消费、批量消费、重试、offset 与资源关闭。 |

`extension` 继续用于 Go 标准库的功能扩展。上述四个模块均属于独立组件，使用 `tk` 前缀；导出类型和函数不重复增加该前缀。

源码核对入口为 `plcache/interface.go`、`plcache/redis_cache.go`、`plredis/redis.go`、`plredis/utils.go`、`pltoken/token.go`、`pltoken/token_cache.go`、`pltoken/token_middleware.go`、`plkafka/kafka.go`。

## 当前实现的关键行为

| 模块 | 已核对的源码行为 | 迁移处理 |
| --- | --- | --- |
| plredis | 包装 `redis.Client`，命令结果使用 `gvar.Var`；复杂参数会转换为 JSON；构造时预加载 Lua。 | 保留必要客户端能力，使用原生返回值和显式编解码，初始化失败返回错误。 |
| plredis | 包含 pipeline、冷却、条件删除和带续期的分布式锁。 | 保留能力，锁续期与解锁都校验持有者，修正维护任务生命周期。 |
| plcache | `Get` 可附带续期，支持批量读写、读后删除、回源、空值缓存和 Once。 | 迁移行为，普通读取与续期读取拆成明确接口。 |
| plcache | `GetOrSetFuncLock` 直接转调普通回源方法，当前没有额外互斥保护。 | 新实现真正合并同实例同 key 的回源，不沿用名称与实现不一致的行为。 |
| plcache | Set（集合）与 SortedSet 方法也放在 RedisCache 中，Hash 与 List 在接口中仍为 TODO。 | 已有集合能力进入 tkredis；未实现的 TODO 不计为已迁移功能。 |
| pltoken | 内存模式使用全局 gcache；Redis 模式保存 JSON；文件模式导出全局缓存为 JSON。 | 统一会话存储接口，文件快照限定当前 token 实例的数据。 |
| pltoken | `MultiLogin=true` 复用已有会话 uuid，返回相同 token；false 重新生成会话。 | 保留实际含义，不将其解释为每台设备拥有独立 token。 |
| pltoken | token 由用户 key、分隔符和 uuid 组成，经 AES-CBC、PKCS7 和 Base64 编码。 | 将旧协议隔离为显式兼容 Codec，使用标准库复现，单独验证协议兼容性。 |
| plkafka | topic 为 `<Env>_<topic>`，group 为 `<ConsumerEnv>_group_<name>`；非空 key 用 FNV-1a 分区。 | 默认保留命名及分区约定，避免迁移导致消费组和消息路由变化。 |
| plkafka | 关闭自动提交，包含单条与批量消费、失败重试、全局生产者和 topic 排除配置。 | 保留能力，明确成功后提交与失败后的处理策略。 |

## 依赖方向

```text
tktoken
    → 会话存储接口
        → tkcache：内存或 Redis
        → token 专用文件存储

tkcache
    ├── memoryStore：标准库
    └── redisStore → tkredis → go-redis/v9

tkkafka → confluent-kafka-go/v2

需要内部诊断日志的模块 → adapter.Logger（可选注入）
```

`tkredis` 不导入 `tkcache`，`tkcache` 不导入 `tktoken`。Kafka 不依赖缓存或 token。仅供某个模块使用的接口放在模块内，多个模块实际共享时再考虑进入 `adapter`。

`whtlib` 不导入源仓库，不通过源仓库的 logger、response、tools 或 plreflect 间接引入 GoFrame。日志使用现有 `adapter.Logger`，未注入时不创建全局日志实例。

## GoFrame 依赖替换

| 现有依赖 | 替代方式 |
| --- | --- |
| `gvar.Var` | 字节、字符串、原生标量、结构体和显式命中标记；JSON 读取指定目标类型。 |
| `gconv`、`g.Map` | 具名结构体、`map[string]any`、`strconv` 与显式转换。 |
| `gjson` | `encoding/json`；需要动态数字兼容时使用 `json.Decoder.UseNumber`，不默默转为 float64。 |
| `gerror` | `errors`、`fmt.Errorf`、`errors.Join`，保留原始错误供 `errors.Is/As` 判断。 |
| `gtime` | `time.Time`、`time.Duration`、`UnixMilli`。 |
| `gcache` | tkcache 内存后端及模块实例拥有的状态。 |
| `gfile` | `os`、`path/filepath`、`io`、必要时 `embed`。 |
| `gstr` | `strings`，涉及路径匹配时单独定义规则。 |
| `grand`、`gmd5` | token 随机标识使用 `crypto/rand`；旧标识的外形和解析规则单独保持兼容。 |
| `gaes`、`gbase64` | `crypto/aes`、`crypto/cipher`、严格 PKCS7 校验和 `encoding/base64`，通过 Codec 隔离协议。 |
| `ghttp` | token 核心只接收 ctx 和 token 字符串；HTTP 适配使用 `net/http`。 |
| 源仓库 `response.Resp` | token 结果结构体与 error；HTTP 状态码和业务响应交给调用方。 |
| 源仓库 `plreflect`、`tools` | 只保留当前模块需要的小范围标准库实现，不迁移整套工具链。 |

剔除依赖以实际 Go import 和模块依赖图为准，仅替换包名不算完成。标准库替代实现需要匹配原有数据和协议约定，不能用 JSON 往返代替任意对象转换后宣称完全等价。

## tkredis 设计

沿用 `go-redis/v9` 客户端家族，首轮不同时更换 Redis 驱动。保留 `redis.Options` 的字段类型；若增加 UniversalClient 包装入口，使用独立构造函数，不把旧配置类型改成另一种 Options。

| 能力 | 设计 |
| --- | --- |
| 客户端构造 | `New` 创建并拥有底层客户端；`Wrap` 包装调用方的客户端，生命周期由调用方管理。 |
| 原生访问 | 提供底层客户端访问入口，支持 tkcache 复用连接池，不重复创建连接。 |
| 通用命令 | `Do` 返回原生值与错误；Redis nil 在客户端层保留，缓存层再映射为未命中。 |
| 复杂参数 | 文本、字节与标量直接传递；JSON 编码显式可见，兼容参数转换时复制参数切片，不修改调用方入参。 |
| Pipeline | 保留命令顺序及每条命令的结果、错误；部分失败时仍返回已得到的结果。 |
| Lua | 按名称管理脚本，使用 `embed` 或不可变脚本内容，支持并发注册和执行；遇到 NOSCRIPT 能重新加载。 |
| 条件操作 | 承接比较删除、冷却检测与必要原子脚本；原子性由 Redis 命令或脚本提供。 |
| 集合与有序集合 | 承接原 plcache 的 S 系列和 SS 系列能力，统一为 Redis 标准命名及具名结果。 |
| 关闭 | 关闭自有连接，停止并等待模块维护任务；借用客户端不由包装层关闭。 |

原 `PipelineResult.Val` 是 `*gvar.Var`，不能在原结构上静默改为 `any`。目标定义新的 `Result`，使用 `Data any` 和 `Err error` 等字段，并在调用迁移时显式替换。空字符串、空数组与 Redis nil 保持不同含义。

SortedSet 结果定义为 `ScoredMember` 列表，包含 member 与 score，不沿用 `[]map[*gvar.Var]float64`。源码中的方法名、参数与返回值在迁移清单中逐项映射，不能将未迁移的高阶方法宣称已被通用 Do 等价覆盖。

### 锁与租约

保留尝试加锁、自旋重试、续期和解锁能力。每次成功获取生成独立的随机持有者标识，使用带 TTL 的原子条件写入。

续期和解锁都必须在脚本中原子比较持有者。源码续期只执行 `EXPIRE`，可能为其他持有者续期，不能直接沿用。续期周期根据实际租约计算，明确时间单位；退出时停止 ticker，ctx 取消、续期失败或丢失持有权都结束维护任务。

同一锁对象不并发代表多个持有者；重新加锁建立新的持有状态。分布式锁本身不保证外部数据库写入已被撤销，严格互斥的业务需要自行处理失效后的执行权。

## tkcache 设计调整

详细接口及基本行为见 [tkcache 缓存模块设计](./2026-10-21-tkcache-design.md)。Redis 后端统一通过 tkredis 获取客户端与脚本能力，内存后端由标准库实现。

| 原 plcache 能力 | 目标处理 |
| --- | --- |
| Get | 普通 Get；原读取时续期行为通过 GetAndExpire 显式表达。 |
| GetMap、SetMap | 保留批量能力，定义缺失项和部分失败的返回规则。 |
| GetAndDelete | 两种后端都提供原子读后删除。 |
| GetOrSet | 保留原子“已存在则读取，否则写入”的能力。 |
| GetOrSetFunc、GetOrSetFuncLock | 统一到 GetOrLoad；同实例同 key 合并回源。 |
| force 空值策略 | 以显式加载选项保留；文本空字符串与 JSON null 不等于未命中。 |
| SetIfNotExist 及 Func 版本 | 映射为条件写入与受控回源，不通过先 Exists 后 Set 实现。 |
| IsExist、GetExpire | 提供存在判断与 TTL 查询；区分未命中、永久缓存及剩余 TTL。 |
| CustomCache、CustomGetOrSetFunc | 通过新的原生类型加载接口迁移；多 key Lua 原子性属于 tkredis 专用能力。 |
| Once | 保留有期限的原子占位能力，属于幂等辅助；不能解释为业务 exactly-once。 |
| S、SS 系列 | 移入 tkredis，不扩大内存缓存的接口要求。 |

token 续期和撤销需要原子比较更新与比较删除。两种缓存后端增加基于原始字节比较的 `CompareAndSwap`、`CompareAndDelete`，避免旧会话的续期或退出请求覆盖、删除新登录会话。

Redis 多 key 操作必须说明单机或同 slot 原子性限制。Cluster 中跨 slot 的 pipeline 只承诺逐条结果，不能伪装成一笔原子事务。

## tktoken 设计

### 核心与存储

核心提供生成、校验、续期和撤销，返回 `IssuedToken`、`Session` 与可识别错误，不包含 GoFrame 请求对象或源仓库响应类型。

| 原接口或能力 | 目标处理 |
| --- | --- |
| GenToken | 接收 ctx、userKey 和业务数据，返回 token 结果和 error。 |
| ValidToken | 接收 token 字符串，先验证 token 与会话一致，再按刷新阈值续期。 |
| RemoveToken | 校验当前会话身份后比较删除，不允许旧 token 删除新会话。 |
| EncryptToken、DecryptToken | 进入 TokenCodec，旧协议作为 LegacyCodec 显式提供。 |
| CacheModeCache、CacheModeRedis | 分别使用 tkcache 内存或 Redis 后端适配会话存储。 |
| CacheModeFile | 提供 token 专用文件存储，保持能力可用，不扩展为通用缓存的默认持久化。 |
| GetTokenData、RemoveRequestToken | HTTP 层先提取 token，再调用核心；不从 ctx 反查 GoFrame request。 |

会话保留 `userKey`、`uuid`、`data`、`createTime`、`refreshTime` 等协议字段。核心使用具名结构体，业务数据通过显式 JSON 编解码处理，数字精度与空值语义保持可控。

`MultiLogin=true` 保留复用已有会话、返回相同 token 的行为；false 创建新会话并替换旧会话。它不表示每设备独立会话；按设备管理属于后续新功能。

校验必须先比较 token 身份再刷新，避免无效旧 token 为当前会话续期。续期通过 CompareAndSwap，撤销通过 CompareAndDelete；失败时重新检查当前状态，不能将旧数据直接写回。

原实现刷新时会重写 createTime。新会话将创建时间与最后刷新时间分开保存；兼容导入时保留原字段值，后续刷新不再改写创建时间。

### 配置与协议兼容

| 原字段 | 原类型与单位 | 迁移约束 |
| --- | --- | --- |
| CacheMode | `int8` | 兼容配置保留枚举含义，构造时映射到存储适配器。 |
| CacheKey | `string` | 保留业务前缀含义；与 tkcache Namespace 的组合必须显式映射。 |
| Timeout、MaxRefresh | `int`，毫秒 | 保留原类型与单位，转换为 duration 时检查范围。 |
| TokenDelimiter | `string` | 旧协议解析保留；不能与用户标识的合法内容发生未处理的歧义。 |
| EncryptKey | `[]byte` | 调用方显式提供并校验长度，不使用源码中的固定默认密钥。 |
| MultiLogin | `bool` | 保留源码实际行为。 |
| MiddlewareType | `uint` | 兼容配置保留旧值映射，新 HTTP 入口不再绑定 GoFrame 的注册方式。 |

原 `RedisCache` 字段和 HTTP 回调类型绑定旧包，无法跨模块原样保留。使用新名称的存储注入及 HTTP 回调接口显式替代；源项目如需过渡兼容，可在源项目提供薄适配层，目标包不携带 GF 类型。

已核对本地 GoFrame v2.9.0 的 gaes：旧协议使用 CBC、PKCS7 和框架默认 IV。LegacyCodec 只服务于旧协议兼容，使用标准库复现；现有 `toolkit/aes` 使用 ECB，不能直接替换。迁移不同时改成 JWT，也不默认宣称更换密码模式后仍兼容旧 token。

LegacyCodec 应显式选择并注入已有部署使用的密钥；token、密钥、解码明文与完整会话不写入日志。旧 token 的字符串兼容与旧会话 Redis key 的兼容是两个问题，必须分别验收。

### 文件与 HTTP 适配

文件模式只保存当前实例的会话及到期时间，不导出进程中的全部缓存。使用标准库 JSON、临时文件与原子替换，限制文件权限；加载时跳过过期会话，不把重启解释为所有会话重新获得完整 TTL。

旧 `.dat` 实际保存 JSON，缺少可靠的逐条到期元数据。需要导入时提供显式工具和失效策略，不在普通启动流程中把旧内容无期限恢复，也不改写用户现存文件。

HTTP 适配采用 `func(http.Handler) http.Handler` 形式；路径规则和错误响应回调独立配置。默认读取 `Authorization: Bearer ...`，旧查询参数 token 提取作为显式兼容选项。GoFrame 的 Group、Bind、Global 注册入口由调用方路由层适配。

## tkkafka 设计

保留 `confluent-kafka-go/v2`，本轮专注移除 GF 及源仓库内部依赖，不顺便替换 Kafka 驱动。该客户端的原生构建依赖需要纳入实施验证，Kafka 包与其他 toolkit 包保持独立。

| 能力 | 迁移设计 |
| --- | --- |
| 配置构造 | 合并 NewWithOption 与 NewWithConfig 的重复默认值逻辑，构造时复制 map 和 slice。 |
| Topic 与 group | 保留 Env、ConsumerEnv、Groups、Mode、ExcludeTopics 和 GlobalProducer 的含义。 |
| 分区 | 保留非空 key 的 FNV-1a 路由，空 key 使用驱动自动选择；PartitionNum 必须大于 0。 |
| 生产者 | 保留普通及共享生产者，明确入队、投递回报与 broker 确认的区别。 |
| 发送 | 保留 SendMessage 入口；最终投递失败通过结果或回调暴露，不只打印日志。 |
| 单条消费 | 处理成功后提交，失败进入受 ctx 控制的重试。 |
| 批量消费 | 按 BatchSize 或 BatchInterval 触发；按 partition 维护可提交进度。 |
| 停止与关闭 | 取消读取、结束消费任务、处理已接收批次、限时等待投递并关闭底层资源。 |
| 诊断 | 使用可选 adapter.Logger；不输出完整认证配置、密码或默认全量消息正文。 |

### 保留字段类型与单位

以下字段迁移时保持原类型，不因剔除 GF 改为其他类型；JSON 字段名也保持原约定。

| 字段 | 类型与单位 |
| --- | --- |
| Servers、Protocol、Mechanism、UserName、Password | `string`。 |
| Retries、RetryMaxCount、BatchSize、MessageMaxBytes、BufferMemory | `int`；数量或字节含义按原配置映射。 |
| RetryBackoff、LingerMs | `int`，毫秒。 |
| WaitTimeout、RetryDelay、BatchInterval | `time.Duration`。 |
| OffsetReset、CompressionType、Env、ConsumerEnv、GlobalProducer | `string`。 |
| IsClose | `bool`。 |
| TopicConfig | `map[string]TopicConfig`。 |
| ExcludeTopics | `[]string`。 |
| TopicConfig.PartitionNum | `uint32`。 |
| TopicConfig.Mode | 保留 `ProducerConsumerStartMode` 及其原始底层类型。 |
| TopicConfig.Groups | `[]string`。 |
| ProducerMessage.Key、ProducerMessage.Data | 分别保留 `string`、`any`。 |

源码配置中的 BufferMemory 是否真正下传、BatchSize 同时参与生产与消费参数等，需要在迁移配置映射时明确处理，不根据字段名猜测含义或静默改变数值单位。

### 消息编码与确认

源 SendMessage 会将对象转成 map 并删除数据库时间字段。通用消息层使用显式 Encoder，不内置业务字段删除。旧消费者依赖该格式时，在调用方提供兼容 Encoder，先核对消息样本后替换；不能直接用 json.Marshal 任意对象后声称与旧消息等价。

源码在 Produce 后执行固定时长 Flush，但没有将每条消息的最终投递结果返回给调用方。目标接口明确结果边界；如增加同步发送，以新方法或选项表达，并由 ctx 约束等待，不能把排队成功解释为已经持久化。

### 重试与 offset

源码在有限重试耗尽后仍会提交消息或批次末尾 offset，存在跳过失败消息的路径。目标默认不提交失败记录；重试耗尽后停止该消费任务并暴露错误，显式跳过或死信策略另行配置。

批次可能包含多个 partition，不能只提交整批最后一条消息。按各 partition 已成功处理的连续进度提交，不越过失败记录。批处理返回错误时，默认整批未确认，业务需能承受重放。

不承诺 exactly-once；业务处理与 Kafka offset 不在同一笔数据库事务内。ctx 取消或关闭期间，未成功处理的数据不提前确认。

`WaitTimeout=-1` 的长期等待不能阻止退出。内部采用可周期检查 ctx 的读取方式，重试等待使用可取消 timer；用户 handler 若不响应取消，不承诺强行终止其执行。

## 兼容边界与待确认项

| 项目 | 已确定约束或后续确认内容 |
| --- | --- |
| GoFrame 类型 | 目标包完全移除；依赖 GF 的返回值与回调用明确迁移映射替代。 |
| 配置字段 | 已存在字段的类型和单位保持，必要新模型使用新名称并提供适配。 |
| 客户端驱动 | 保留 go-redis/v9 与 confluent-kafka-go/v2 家族，实施时固定兼容版本。 |
| 线上旧 token | 是否要求滚动发布期间新旧 token 双向互认，需要确认；方案预留显式 LegacyCodec。 |
| 旧缓存 key | 是否要求复用旧 Redis key，需要确认；默认 Namespace 不能自动等同于旧前缀。 |
| 文件会话 | 保留文件模式能力；是否导入旧 `.dat` 及其到期策略，需要确认。 |
| Kafka 故障策略 | 默认失败不提交；跳过、死信、限次后人工处理属于显式业务策略。 |

这些确认点不改变四个模块均需迁移的范围。未完成协议与业务验证前，不宣称是只换 import 即可的无缝替换。

## 落地顺序与验证

1. 实施 tkredis，剔除 gvar、gerror、gfile 与 plreflect，保留客户端和原子操作能力。
2. 实施 tkcache 的两种后端，补齐从 plcache 迁移的能力与 token 所需比较操作。
3. 实施 tktoken 核心、会话存储、LegacyCodec 与独立 HTTP 适配。
4. 实施 tkkafka，保留配置和消息协议，落实关闭、重试与 offset 规则。
5. 按实际调用点更新使用说明和迁移示例；源仓库及业务服务的调用替换作为明确范围单独执行。

实现后验证四个模块不直接或间接导入 GF，也不依赖源 go-gf-lib。按模块进行构建、静态检查和已有验证；不自动运行需要真实 MySQL、Redis 或 Kafka 的集成测试，不运行整个仓库测试。新增测试遵循用户授权。

验收至少覆盖缓存两种后端的等价行为、原子比较更新与删除、Redis 锁持有者校验、旧 token 编解码及会话 key 映射、文件会话到期、Kafka 多分区提交和失败不提交。设计中的验收要求不代表上述实现或测试已经完成。

当前交付包含迁移设计及更新后的 tkcache 设计，不修改源仓库，不提交或推送代码。
