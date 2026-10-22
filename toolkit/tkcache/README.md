# tkcache

统一的字节缓存，支持内存、Redis 和文件三种 Store。文本直接存取，MySQL 等查询结果通过调用方提供的 loader 与泛型 JSON 函数缓存，不依赖数据库驱动或 ORM。

```go
cache, err := tkcache.New(tkcache.Config{
    Store: tkcache.MemoryStore,
    Namespace: "orders",
})
if err != nil {
    return err
}
defer cache.Close()

err = cache.SetString(ctx, "notice", "服务已就绪", 10*time.Minute)
if err != nil {
    return err
}
text, found, err := cache.GetString(ctx, "notice")

// loadUser 由业务实现，签名为 func(context.Context) (*User, error)。
user, err := tkcache.GetOrLoadJSON[*User](ctx, cache, "user:123", 5*time.Minute, loadUser)
```

Redis 模式先用 `tkredis.New(*redis.Options)` 或 `tkredis.Wrap(redis.UniversalClient)` 获得客户端，再传入 `Config{Store: tkcache.RedisStore, Namespace: "orders", RedisClient: client}`。Redis Store 只做接口适配，读写、TTL、原子操作、前缀清理和客户端状态检查均复用 `tkredis.Client`，不直接操作 go-redis 或维护 Lua。缓存始终借用客户端；关闭缓存不关闭客户端、不删除 Redis 数据。连接超时、认证和 Cluster/Sentinel 参数由原生客户端配置。

## 文件 Store

```go
cache, err := tkcache.New(tkcache.Config{
    Store: tkcache.FileStore,
    Namespace: "sessions",
    FilePath: "/var/lib/myapp/sessions.json",
})
if err != nil {
    return err
}
defer cache.Close()
```

`Config.Store` 的类型为 `StoreType`，支持 `MemoryStore`（默认）、`RedisStore` 和 `FileStore`。文件父目录必须存在；构造时拒绝符号链接、非普通文件及无版本的旧 `.dat` 文件。

文件 Store 复用内存 Store 的 TTL 和原子操作，JSON 快照保存版本及绝对到期时间。成功修改前同步写入同目录 0600 临时文件、同步内容并原子重命名；持久化失败不会改变有效内存数据。快照权限为 0600。加载时跳过过期项，重启不延长 TTL；读取及后续写入也会清理过期项。每次修改写入完整快照，适用于小规模本地持久化。

一个文件只能由一个活动 Cache 实例持有，不提供跨实例或跨进程文件锁。不同文件相互隔离；重新打开同一文件时，`Clear` 仅删除当前 Namespace 的业务数据和 Once 占位。`Close` 释放内存、取消回源并保留文件，不运行后台保存任务。原子替换保证快照完整，不额外承诺断电后的目录项持久性。

原 `tktoken.NewFileStore` 的创建入口已移除，改用上面的配置并注入 `tktoken`。原快照 key 没有 tkcache 命名空间前缀；复用已有会话时需显式映射为 `tkcache:<Namespace>:data:<原 key>`，保留值和绝对到期时间，本模块不会自动转换。

## 数据与接口

| 接口 | 行为 |
| --- | --- |
| `Get`、`GetString` | 返回值、found、error；普通读取不续期，空内容仍是命中。 |
| `Set`、`SetString`、`SetJSON` | 覆盖内容和 TTL；JSON 编码失败不改变原数据。 |
| `GetJSON[T]` | 按目标类型解码；无效 JSON 保留 found=true 并返回错误，动态数字为 json.Number。 |
| `GetOrLoad`、`GetOrLoadJSON[T]` | 未命中时回源，同实例同 key 合并查询。 |
| `SetIfAbsent` | 原子条件创建，已过期等同不存在。 |
| `GetOrSet` | 原子返回已有内容或写入候选值，命中不续期。 |
| `GetAndExpire`、`GetAndDelete` | 原子读后续期或删除。 |
| `CompareAndSwap`、`CompareAndDelete` | 比较原始字节，匹配后才更新/删除；缺失不能匹配成功。 |
| `TTL`、`Exists`、`Delete` | TTL 为 -1、true 表示永久，为 -2、false 表示不存在。 |
| `GetMap`、`SetMap` | 批量操作；缺失项不进入读取结果，跨 key 无事务，可能部分完成。 |
| `Once` | 独立区域的正 TTL 原子占位，不承诺业务 exactly-once。 |
| `Clear`、`Close` | 清当前命名空间；关闭幂等，停止维护、取消回源并唤醒等待者。 |

TTL 保持 `time.Duration` 类型，三种 Store 使用相同约定：

| 值 | 写入含义 | `TTL` 查询含义 |
| --- | --- | --- |
| `tkcache.NoExpiration`（原始 `-1`） | 永久保存或移除已有到期时间 | 键存在且永久，`found=true` |
| `0` | 立即过期，删除符合操作条件的键 | 剩余期限为零，不能用于判断永久或不存在 |
| 正数 | 保存期限，向上取整到毫秒 | 剩余有效期 |
| `tkcache.TTLNotFound`（原始 `-2`） | 无效参数 | 键不存在，`found=false` |

其他小于 `-1` 的写入参数返回 `ErrInvalidArgument`。哨兵直接使用常量或 `time.Duration(-1/-2)`，不要乘以 `time.Second` 或 `time.Millisecond`；正数期限仍按正常 Duration 使用。TTL 查询的 `found` 和错误返回值保留不变。

`0` 不再表示永久，原先用于永久保存的调用需要改为 `tkcache.NoExpiration`。`GetAndExpire(key, 0)` 返回旧内容后删除；`GetOrSet` 未命中且 TTL 为 0 时返回候选值但不留下缓存；`GetOrLoad` 的零 TTL 同样只返回回源结果、不保留缓存。条件写入或 CAS 未匹配时不会因为 TTL 为 0 而删除已有值。`Once` 仍要求正期限。

相同 key 的读写必须约定相同编码；nil 与零长度字节统一按空内容存储，不保留区别。写入和读取的切片均隔离，不共享可变底层数组。文件快照格式不变，已有零到期时间的永久记录仍按永久处理。

`WithCacheEmpty(false)` 使 nil 回源结果只返回、不回填；JSON typed nil 也按此处理。默认缓存成功的空结果，包括 JSON null。空字符串、0、false 和非 nil 空集合始终是有效数据。回源错误、JSON 错误和Store 故障都原样返回，不自动降级查询数据库。

## 命名空间与生命周期

Namespace 必填，只允许 ASCII 字母、数字、下划线和连字符；逻辑 key 必须非空。实际存储 key 为 `tkcache:<Namespace>:data:<key>`，Once 为 `tkcache:<Namespace>:once:<key>`。相同 Redis 数据库和 Namespace 的实例共享数据；相同 Namespace 的内存实例仍相互独立。

内存实例每个只有一个过期清理任务，默认每分钟扫描，可设置 `CleanupInterval`；读取仍立即识别过期项。首版没有容量限制或 LRU。`Clear` 同时删除当前 Namespace 的数据和 Once，占位不与业务数据碰撞。Redis 使用 SCAN 和单 key 删除，Cluster 遍历各主节点；不承诺跨进程并发写入时得到清空快照。

所有数据操作要求非 nil ctx。关闭后返回 `ErrClosed`；配置及参数错误分别通过 `errors.Is` 匹配 `ErrInvalidConfig`、`ErrInvalidArgument`。Redis 故障不会当作普通未命中；网络错误或取消也不保证已发送的命令未执行。

## 回源和 MySQL 一致性

同一次回源由首个调用者决定 loader、ctx、TTL 和选项。等待者可独立取消，首个调用者取消则终止本轮共享回源。loader 在缓存锁外执行，应将收到的 ctx 传给数据库，不能递归等待同实例同 key 的回源。

同实例更新、删除或 Clear 后，旧回源不能覆盖后续缓存，已有等待者仍可能拿到旧查询快照。后续调用不再加入该旧任务。此协调只限当前实例，不提供跨实例分布式 singleflight 或数据库强一致性。loader panic 转为 `ErrLoaderPanic`，不缓存；Close 不无限等待忽略 ctx 的业务回调。

MySQL 写入建议先提交事务，再删除相关详情和列表缓存。删除失败需由业务重试或补偿；TTL 只约束陈旧时间。缓存 key 应包含租户、筛选、排序、分页及必要的数据版本。模块不扫描或导入旧 plcache Redis key。

## 从 plcache 迁移

普通 Get 不再隐式续期，需要旧行为时使用 `GetAndExpire`；`GetOrSetFunc`/`GetOrSetFuncLock` 使用 `GetOrLoad` 和 `WithCacheEmpty`，真正合并回源；`SetIfNotExist` 使用 `SetIfAbsent`；`GetExpire`/`IsExist` 使用 `TTL`/`Exists`。集合和有序集合迁至 tkredis，未实现的 Hash/List TODO 不计入迁移。

## 离线验证

```sh
go test -race ./toolkit/tkcache
go vet ./toolkit/tkcache
```

测试使用内存、临时文件和 miniredis，覆盖 TTL、原子操作、JSON、切片隔离、Namespace、回源合并/取消/失效及关闭。tktoken 另有基于三种 Store 的会话续期和撤销联调。未连接真实 MySQL、Redis、Kafka，也未验证真实 Redis Cluster。
