# tkcache 缓存模块设计

`tkcache` 位于 `toolkit/tkcache`，参考 `go-gf-lib/plugins/plcache` 迁移通用缓存能力，提供内存和 Redis 两种后端，用于缓存文本数据与 MySQL 查询结果。业务通过统一的 `Cache` 使用 TTL、原子写入、删除和按需回源；后端统一保存字节数据，文本与 JSON 编解码由上层便捷接口处理。Redis 客户端与脚本能力由同步迁移的 `tkredis` 提供。

状态：已实现，已通过内存与 miniredis 模块测试、race 检查。支持文本及 MySQL 查询结果缓存，同时提供内存和 Redis 后端。本模块属于 [go-gf-lib 通用模块迁移设计](./2026-10-21-toolkit-migration-design.md) 的一部分，同批迁移还包括 plredis、pltoken、plkafka 与反射扩展。

## 目标与范围


| 项目           | 设计                                                             |
| ---------------- | ------------------------------------------------------------------ |
| 模块位置       | `toolkit/tkcache`，Go 包名为 `tkcache`。                         |
| 内存后端       | 数据保存在当前实例内，支持并发读写、TTL 和后台清理。             |
| Redis 后端     | 多个服务实例可以通过同一个 Redis 数据库和 Namespace 共享缓存。   |
| 文本缓存       | 使用 String 便捷方法读写字符串，也可以直接使用字节接口。         |
| MySQL 数据缓存 | 缓存查询后的结构体、列表与统计结果，通过业务提供的 loader 回源。 |
| 依赖           | 内存实现和 JSON 编解码使用标准库；Redis 使用必要的客户端依赖。   |

`extension` 用于基于 Go 标准库的功能扩展；`tkcache` 属于独立的通用缓存组件，按项目规则放入 `toolkit`，目录名和包名使用 `tk` 前缀。类型与函数使用 `Cache`、`Config`、`New` 等常规名称。

首版每个实例选择一个后端，可以在同一进程中分别创建内存实例和 Redis 实例。暂不包含自动组合的二级缓存、Redis 故障时自动切换内存、分布式回源锁、数据库变更订阅、ORM 集成、LRU 淘汰和缓存持久化管理。

## 组件结构

```text
业务代码
    → 文本方法或 JSON 辅助函数
    → Cache：参数校验、Namespace、TTL、回源合并、生命周期
    → 私有 store 接口
        ├── memoryStore：map、互斥锁、过期清理
        └── redisStore：tkredis → go-redis、Redis String、服务端 TTL
```

两种后端使用同一套字节存储契约，不将任意 Go 对象引用直接保存在内存后端。这样，文本、JSON 格式与对象解码规则不随后端变化，修改调用方对象也不会隐式改变已缓存的数据。

`store` 接口放在 `tkcache` 内部，覆盖字节读写、原子条件写入、删除和范围清理；首版不增加公开插件注册机制，也不将此接口放入 `adapter`。

## 配置与构造

通过 `New` 创建独立实例，构造后配置保持不变。切换后端或 Namespace 时新建实例。

```go
type Backend string

const (
    BackendMemory Backend = "memory"
    BackendRedis  Backend = "redis"
)

type Config struct {
    Backend         Backend
    Namespace       string
    CleanupInterval time.Duration
    RedisClient     *tkredis.Client
}

func New(config Config) (cache *Cache, err error)
```

代码中的 `tkredis` 为 `whtlib/toolkit/tkredis`，该模块迁移自 `go-gf-lib/plugins/plredis`，底层沿用 `github.com/redis/go-redis/v9`。具体版本在实施时选择并固定，不使用浮动版本。[go-redis 官方指南](https://redis.io/docs/latest/develop/clients/go/)


| 字段            | 默认值          | 规则                                              |
| ----------------- | ----------------- | --------------------------------------------------- |
| Backend         | `BackendMemory` | 空值使用内存后端；未知值返回配置错误。            |
| Namespace       | 无              | 必填，仅允许 ASCII 字母、数字、下划线与连字符。   |
| CleanupInterval | `1m`            | 仅内存后端使用；0 使用默认值，负数返回配置错误。  |
| RedisClient     | 无              | Redis 后端必须提供非 nil 客户端；内存后端不使用。 |

Redis 客户端由调用方通过 tkredis 创建或包装，配置连接池、认证、TLS 与超时，并负责最终关闭。`tkcache.New` 仅校验和借用客户端，不执行网络探测；如需启动时确认连接，由调用方执行 `Ping`。

tkredis 的原生客户端访问入口可包装 `UniversalClient`，适配单机、Sentinel 与 Cluster；Cluster 的范围清理必须遍历各主节点，不能只对任意一个节点执行一次扫描。新包装入口不改变源 plredis 基于 `redis.Options` 的配置字段类型。[go-redis UniversalClient 源码](https://github.com/redis/go-redis/blob/master/universal.go)

## 对外接口

### 字节接口

```go
type Loader func(ctx context.Context) (value []byte, err error)

func (c *Cache) Get(ctx context.Context, key string) (value []byte, found bool, err error)

func (c *Cache) Set(ctx context.Context, key string, value []byte, ttl time.Duration) (err error)

func (c *Cache) SetIfAbsent(ctx context.Context, key string, value []byte, ttl time.Duration) (stored bool, err error)

func (c *Cache) GetOrLoad(ctx context.Context, key string, ttl time.Duration, loader Loader, options ...LoadOption) (value []byte, err error)

func (c *Cache) GetAndExpire(ctx context.Context, key string, ttl time.Duration) (value []byte, found bool, err error)

func (c *Cache) GetAndDelete(ctx context.Context, key string) (value []byte, found bool, err error)

func (c *Cache) GetOrSet(ctx context.Context, key string, value []byte, ttl time.Duration) (result []byte, err error)

func (c *Cache) GetMap(ctx context.Context, keys []string) (values map[string][]byte, err error)

func (c *Cache) SetMap(ctx context.Context, values map[string][]byte, ttl time.Duration) (err error)

func (c *Cache) Exists(ctx context.Context, key string) (found bool, err error)

func (c *Cache) TTL(ctx context.Context, key string) (ttl time.Duration, found bool, err error)

func (c *Cache) CompareAndSwap(ctx context.Context, key string, expected, value []byte, ttl time.Duration) (swapped bool, err error)

func (c *Cache) CompareAndDelete(ctx context.Context, key string, expected []byte) (deleted bool, err error)

func (c *Cache) Once(ctx context.Context, key string, ttl time.Duration) (acquired bool, err error)

func (c *Cache) Delete(ctx context.Context, keys ...string) (err error)

func (c *Cache) Clear(ctx context.Context) (err error)

func (c *Cache) Close() (err error)
```

`Get` 用 `found` 区分未命中和已缓存的空内容。内存后端在写入与返回时复制字节切片；回源合并的每个等待者也获得独立切片。调用方可以修改原始切片或返回切片，不会改变缓存内容或其他等待者的结果。字节接口将 nil 切片与空切片统一视为零长度内容，不承诺保留二者差别。

### 文本与 JSON 接口

```go
func (c *Cache) GetString(ctx context.Context, key string) (value string, found bool, err error)

func (c *Cache) SetString(ctx context.Context, key string, value string, ttl time.Duration) (err error)

func (c *Cache) SetJSON(ctx context.Context, key string, value any, ttl time.Duration) (err error)

func GetJSON[T any](ctx context.Context, cache *Cache, key string) (value T, found bool, err error)

func GetOrLoadJSON[T any](ctx context.Context, cache *Cache, key string, ttl time.Duration, loader func(ctx context.Context) (value T, err error), options ...LoadOption) (value T, err error)
```

文本方法直接转换字符串与字节，不添加 JSON 引号。JSON 方法使用 `encoding/json`，由 `GetJSON[T]` 和 `GetOrLoadJSON[T]` 指定返回类型，例如 `User`、`*User`、`[]User`。泛型辅助函数使用包级函数形式。

同一个逻辑 key 的读写双方必须约定相同的格式与数据结构。JSON 方法不自动推断原始 Go 类型；包含 interface 字段时遵循标准库 JSON 的解码规则。JSON 不可编码的数据返回错误，缓存原值不受影响。

`GetJSON` 未命中时返回 `found=false`，不执行解码；命中但解码失败时返回 `found=true` 和解码错误。`GetOrLoadJSON` 遇到已有内容无法解码时直接返回错误，不自动删除内容或执行数据库回源。

`LoadOption` 首版提供 `WithCacheEmpty(bool)`，用于迁移原 plcache 的 force 参数，默认缓存成功返回的空结果。关闭该选项时，nil 字节结果或 JSON nil 结果只返回、不回填；空字符串、0、false 和非 nil 的空切片仍是有效内容。JSON 层在编码前使用 `extension/reflectx` 的 IsNil 判断 typed nil，不把非空对象误判为空。reflectx 迁移自源 plreflect，定位见 [reflectx 标准库扩展设计](./2026-10-21-reflectx-design.md)。回源方法的回调错误始终不缓存。

## plcache 能力迁移

| 源行为 | 目标行为 |
| --- | --- |
| `Get(ctx, key, timeout...)` 读取并续期 | 普通 Get 保持无续期语义；需要旧行为时使用原子 GetAndExpire。 |
| GetMap、SetMap | 迁移为批量字节读写；GetMap 不包含缺失项，存在但内容为空的 key 保留。 |
| GetOrSet | 原子读取已有内容或写入候选值，命中时不延长 TTL；旧续期需求通过显式续期操作迁移。 |
| GetOrSetFunc、GetOrSetFuncLock | 使用 GetOrLoad 和 WithCacheEmpty；新实现真正合并同 key 回源。 |
| SetIfNotExist | 映射为 SetIfAbsent；Func 变体通过显式回源组合，不复制名义上的 Lock 方法。 |
| GetAndDelete | 两种后端都在原子操作中读取并删除。 |
| IsExist、GetExpire | 映射为 Exists、TTL；TTL 未命中返回 found=false，永久缓存返回 ttl=0、found=true。 |
| Once | 正数 TTL 的原子占位，期间只允许一个成功获取者；原默认七天通过迁移调用方显式传入。 |
| CustomCache | 使用原生类型的自定义加载入口；依赖多 key Lua 的具体逻辑迁移到 tkredis。 |
| Set（集合）、SortedSet | 放入 tkredis，内存后端不模拟 Redis 的全部数据结构。 |

GetMap 和 SetMap 不承诺跨 key 原子性；Redis Cluster 采用单 key 命令或 pipeline，失败时可能部分完成，不改变旧多 key Lua 的同 slot 限制。已完成的批量读取结果与错误一起返回。需要整组原子读取、写入或续期的调用方使用 tkredis 专用接口并满足同 slot 条件。

GetAndExpire 仅在命中时按 ttl 重设到期时间，0 表示移除到期时间；GetAndDelete、GetOrSet、成功的比较操作和批量写入均纳入回源任务的写回协调。比较操作只有当前原始字节与 expected 完全相同时才执行，未命中不成功；JSON 会话比较使用读取到的原始字节，不重新编码后猜测等价。

tkcache 使用独立的 `tkcache:<Namespace>:once:<key>` 区域保存 Once 占位，普通缓存数据使用 `tkcache:<Namespace>:data:<key>`。两类操作互不碰撞，Clear 同时清理当前 Namespace 的数据与占位，不影响其他 Namespace。

## key 与 Namespace

调用方提供非空逻辑 key，模块按如下形式构造实际 key：

```text
tkcache:<Namespace>:data:<key>

tkcache:order_service:data:config:notice
tkcache:order_service:data:user:123
tkcache:order_service:data:user_list:status=enabled:page=1:size=20
```

Namespace 不允许 Redis glob 通配符，保证范围清理可以精确构造前缀。逻辑 key 按原字符串匹配，不自动去空格或改变大小写。

业务 key 必须包含影响结果的租户、筛选、排序、分页等条件；复杂条件可以先确定性编码，再计算摘要。JSON 结构出现不兼容变更时，由业务在 key 中加入版本。

相同 Redis 数据库和 Namespace 下的实例共享数据，`Clear` 会影响该 Namespace 的所有调用方。内存实例始终各自持有数据，相同 Namespace 不会使两个内存实例自动共享状态。

## TTL 与基础行为


| 场景           | 行为                                                               |
| ---------------- | -------------------------------------------------------------------- |
| `ttl == 0`     | 不设置到期时间；仍可被显式删除，Redis 也可能按服务端淘汰策略移除。 |
| `ttl > 0`      | 从本次成功写入开始计时；统一向上取整到毫秒，两种后端使用相同精度。 |
| `ttl < 0`      | 返回参数错误，不解释为删除或沿用旧 TTL。                           |
| 读取过期项     | 返回未命中，不返回过期内容。                                       |
| `Get` 命中     | 不延长 TTL。                                                       |
| `Set` 已存在项 | 替换值，重新设置 TTL；0 会移除旧的到期时间。                       |
| `SetIfAbsent`  | 仅在 key 不存在或已过期时写入；判断与写入必须原子完成。            |
| 删除不存在项   | 正常返回；无参数 `Delete` 正常返回。                               |

Redis 使用单条 `SET` 同时设置值与到期时间，条件写入通过 `NX` 完成，不拆成先判断再写入或写入后再设置 TTL。[Redis SET 文档](https://redis.io/docs/latest/commands/set/)

源 plcache 的 Redis 实现先将 duration 截断为整秒，小于一秒的正数可能变成永久缓存。迁移后的毫秒精度是明确修正；旧调用中的负数与省略 TTL 需要显式映射，不能直接按新规则解释。

## 内存后端

使用 `map[string]entry` 保存字节数据与到期时间，以 `sync.Mutex` 保护并发访问，读取过期条目可在同一临界区内删除。到期时间保留 `time.Time` 的单调时钟信息，不转换为秒级整数。

过期处理结合读取时检查和后台定期扫描：读取保证不会命中过期数据，后台扫描回收无人访问的数据。每个实例只启动一个维护 goroutine，不为每个 key 创建 timer。

过期检查和删除在同一临界区内完成，避免误删其他调用更新的新值。loader 不在数据锁内执行。

首版不设置容量上限，也不实现 LRU；无 TTL 的条目持续保留到删除、清理或关闭。调用方需要控制条目数量和单条数据大小，不能将 TTL 清理视为容量限制。

## Redis 后端

所有业务数据保存为 Redis String，内容为原始字节或 JSON 文本。`GET` 返回 key 不存在时转换为 `found=false, err=nil`，其他 Redis 错误保留原因并返回。

TTL 由 Redis 服务端管理，不启动本地过期清理任务。不在本地保留 Redis 数据副本，不隐式启用客户端缓存。

`Delete` 只删除指定逻辑 key。多个 key 在 Cluster 下按单 key 命令发送，可以通过 pipeline 减少往返，不发送跨 slot 的多 key 删除；批量删除可能部分成功，失败不回滚。

`Clear` 只处理当前 Namespace，通过 `SCAN MATCH` 逐批遍历并删除；扫描允许重复结果，删除需要保持幂等。Cluster 遍历所有主节点，逐批检查 ctx 是否取消。该方案遵循 `SCAN` 的迭代契约。[Redis SCAN 文档](https://redis.io/docs/latest/commands/scan/)

范围清理不使用 `KEYS`、`FLUSHDB` 或 `FLUSHALL`。首版删除使用 `DEL`；大对象异步回收可在后续增加 `UNLINK` 选项。

内存 `Clear` 可以在锁内清空当前数据；Redis `Clear` 是范围遍历，可能部分完成，并不保证清理期间其他实例新写入的数据全部被删除。需要稳定清空结果时，调用方应先停止该 Namespace 的写入。

## 按需回源与并发

`GetOrLoad` 按以下顺序工作，JSON 辅助函数复用同一流程：

1. 校验参数并查询缓存；命中时直接返回。
2. 未命中时，在当前 `Cache` 实例内查找相同 key 的进行中任务。
3. 没有任务时登记任务，由第一个调用者提供 loader、ttl、加载选项和共享回源 ctx；其余调用者等待同一结果。
4. 任务登记后再次查询缓存，避免覆盖在首次读取与任务登记之间写入的数据。
5. 仍未命中时，在缓存锁外执行 loader；成功后写入缓存，再通知等待者。

相同 key 使用不同 loader、ttl 或加载选项并发调用时，以建立该次回源任务的调用为准。因此业务必须让同一个 key 表达同一类数据。

每个等待者可以用自己的 ctx 取消等待，不取消其他等待者。首个发起者的 ctx 取消会取消共享回源，使本轮等待者得到取消错误；后续调用可以重新发起。loader 必须使用传入的 ctx 进行数据库访问，且不得递归等待当前实例同一 key 的回源任务。

`Set`、成功的 `SetIfAbsent`、`Delete`、`Clear` 以及其他成功变更数据或到期时间的操作，会使受影响的旧回源任务失去写回资格；后续读取不再加入该旧任务。实现必须将同 key 的变更与回源写回按顺序协调，不能仅在 Redis 写入前检查一次标记后就释放协调锁。任务身份只跟随进行中的任务，不永久保留已删除 key 的版本记录。

失去写回资格的任务仍可向已有等待者返回本次查询结果，但不得覆盖后续缓存状态。该保证仅覆盖同一个 `Cache` 实例；其他实例或进程的写入不受本地任务协调保护。

回源合并不提供跨进程互斥。多个服务实例同时未命中 Redis 时，仍可能各自访问 MySQL。跨进程的旧数据回填与强一致性要求，需要另行设计版本校验或分布式协调。

## MySQL 使用方式与一致性

MySQL 数据使用 Cache-Aside，由业务提供查询函数，模块不直接依赖 SQL 驱动或 ORM。


| 操作           | 业务流程                                                          |
| ---------------- | ------------------------------------------------------------------- |
| 查询单条记录   | 用主键构造 key，调用 `GetOrLoadJSON[*User]`；未命中时查询 MySQL。 |
| 查询列表       | key 包含完整查询条件，调用 `GetOrLoadJSON[[]User]`。              |
| 修改数据库     | 先完成数据库事务，再删除受影响的详情缓存与列表缓存。              |
| 未找到记录     | 业务决定是否映射为成功的空结果；缓存沿用调用指定的 TTL。          |
| 数据库查询失败 | 返回错误，不写入缓存。                                            |

事务提交前不执行缓存失效，避免其他请求将未提交前的数据重新写入缓存。提交成功后的缓存删除失败必须向业务暴露，由业务决定重试或补偿；TTL 是额外的过期机制，不能保证数据库与缓存实时一致。

JSON 的 `null` 是有效缓存内容，可以与未命中区分。数据库返回的“记录不存在”错误默认仍是错误；只有业务显式将它转换为空结果时，才进行空结果缓存。

示例中的 `loadUser` 由业务实现，返回 `(*User, error)`：

```go
cache, err := tkcache.New(tkcache.Config{
    Backend:   tkcache.BackendMemory,
    Namespace: "order_service",
})
if err != nil {
    return
}
defer cache.Close()

err = cache.SetString(ctx, "config:notice", "service ready", 10*time.Minute)
if err != nil {
    return
}

user, err := tkcache.GetOrLoadJSON[*User](ctx, cache, "user:123", 5*time.Minute, loadUser)
if err != nil {
    return
}
```

使用 Redis 时，构造配置改为 `BackendRedis` 并传入调用方创建的 `RedisClient`，后续文本与 JSON 调用方式保持一致。示例 TTL 仅用于说明调用方式，实际值按业务允许的陈旧时间确定。

## 错误与生命周期

公开可识别错误至少包括 `ErrClosed`、`ErrInvalidArgument` 和 `ErrInvalidConfig`，通过 `errors.Is` 判断。Redis、JSON、ctx 和 loader 错误保留原始原因，不将后端故障当作普通未命中。

所有数据操作要求非 nil ctx；nil ctx 视为调用错误并 panic。已取消的 ctx 返回 `ctx.Err()`，不会开始新的回源任务。请求取消不能保证已经发送给 Redis 的写入未生效，网络写入错误也不代表服务端一定没有执行。

`GetOrLoad` 遇到缓存读取错误时直接返回，不自动访问数据库；loader 失败不缓存；回填失败也返回错误，不将该次调用报告为完全成功。业务如需在缓存故障时继续查询数据库，应显式提供降级逻辑。

`Close` 幂等，并发调用安全。关闭后新操作返回 `ErrClosed`，尚未交付的回源任务停止写回，并唤醒等待者。关闭会取消模块派生的回源 ctx，但不等待不响应取消的业务 loader 无限结束。

内存实例关闭时停止并等待维护 goroutine，释放当前数据。Redis 实例关闭时只释放自身状态，不删除服务端缓存，也不关闭调用方注入的共享客户端。关闭前已发送的 Redis 命令可能已经生效。

首版不提供全局默认实例、不在导入包时启动后台任务，也不强依赖 `logx`。错误由调用方统一记录和处理。

## 文件职责

```text
toolkit/tkcache/
├── cache.go       # 配置、Cache、公共操作、原子比较、批量与生命周期
├── memory.go      # 内存存储、TTL 与定时清理
├── redis.go       # 基于 tkredis 的缓存后端和 Namespace 清理
├── json.go        # 文本、JSON 编解码与泛型辅助函数
├── loader.go      # 回源合并、取消和写回协调
├── cache_test.go  # 两种后端的数据与 TTL 行为
├── loader_test.go # 回源并发、取消、失效和关闭
└── README.md      # 两种后端的用法与行为边界
```

## 验收行为


| 场景                               | 预期                                                       |
| ------------------------------------ | ------------------------------------------------------------ |
| 两种后端读写文本、空文本与字节数据 | 内容一致，命中与未命中可区分。                             |
| 结构体、列表和 JSON null           | 按调用方指定的类型解码，null 不等于未命中。                |
| 修改写入或返回的切片               | 不改变内存中已经缓存的数据。                               |
| TTL、覆盖与条件写入                | 过期不命中，覆盖重置 TTL，条件写入具有原子性。             |
| 相同实例、相同 key 并发未命中      | 合并回源，loader 不阻塞其他 key 的数据锁。                 |
| 回源中发生更新、删除或清理         | 旧任务不能覆盖该实例后续的缓存状态。                       |
| 等待取消、回源失败或实例关闭       | 等待者可以退出，不留下可写回的失效任务。                   |
| Redis key 不存在与 Redis 故障      | 前者为未命中，后者保留并返回错误。                         |
| Redis Namespace 清理               | 仅删除本 Namespace；Cluster 遍历主节点，跨 slot 删除正确。 |
| Redis 客户端复用与关闭             | 关闭缓存包装不关闭共享客户端，不删除服务端数据。           |
| MySQL 事务提交与缓存删除失败       | 提交后执行失效，失败可由业务识别和补偿。                   |

上述本地行为已通过内存、miniredis 及 tktoken 两种后端联调验证。未连接真实 MySQL、Redis、Kafka，也未验证真实 Cluster；数据库事务与缓存删除补偿仍由业务实现。使用示例见模块 README，执行记录见 [迁移实施计划](../plans/2026-10-21-toolkit-migration.md)。
