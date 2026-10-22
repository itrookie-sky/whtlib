# tkredis

基于 `go-redis/v9` 的 Redis 客户端、Lua、集合与分布式锁，完全不依赖 GoFrame。

```go
client, err := tkredis.New(&redis.Options{
    Addr: "127.0.0.1:6379",
    ContextTimeoutEnabled: true,
})
if err != nil {
    return err
}
defer client.Close()

_, err = client.Do(ctx, "SET", "profile", tkredis.JSON(profile))
if err != nil {
    return err
}
value, err := client.Do(ctx, "GET", "profile")
if errors.Is(err, redis.Nil) {
    // 缓存未命中
}
```

`New` 复制 `redis.Options` 配置值，创建并拥有连接池；构造过程不连接服务器、不预加载脚本，首次命令返回连接错误。`New(nil)` 使用 go-redis 默认配置。`NewWithOption` 提供配置函数入口。

`Wrap(redis.UniversalClient)` 借用已有单机、Sentinel 或 Cluster 客户端；`Raw()` 提供原生客户端访问。tkcache 通过本包公开方法复用能力，不绕过客户端状态检查。`Close()` 幂等，取消并等待锁维护任务后只关闭自有连接池。借用池仍由调用方管理。通过 `SetLogger(adapter.Logger)` 可选注入日志；内部诊断不会记录认证配置、锁 key、owner 或完整参数。

## 命令和编码

`Do` 返回原生 `any` 与原始错误，保留 `redis.Nil`；空字符串、空数组与缺失值不同。文本、`[]byte` 和标量直接传递。复杂对象通过 `JSON(value)` 标记，或明确调用 `DoJSON`/`EncodeArgs(start, args...)` 转换；不会修改调用方的参数切片。JSON 编解码由 `encoding/json` 完成，不经过浮点数中间转换。读取 JSON 时由调用方指定目标类型并解码。

`Pipeline(ctx, commands...)` 返回顺序一致的 `[]Result{Data, Err}`。执行错误同时通过整体 `err` 和对应结果暴露，后续成功项仍保留。空管道返回空结果。参数预处理失败时不会执行任何命令。管道不保证整组原子性，Cluster 跨 slot 时也只承诺逐条结果。

## Lua 和原子操作

`RegisterScript` 只注册本地脚本；`ScriptLoad` 同时加载到服务器；`ScriptLoadByPath` 以不带扩展名的文件名注册。`EvalSha` 按注册名称执行，遇到 `NOSCRIPT` 自动通过原始脚本执行并重建服务器缓存。注册和执行可以并发进行；一次执行使用取到的不可变脚本文本快照。`Eval` 直接执行脚本文本。全部入口保留原始 Redis 错误。

- `Cad`：比较值一致才删除。
- `CD`：原子检测冷却标记；兼容清理旧的永久标记，清理当次仍视为冷却中。
- `SetCD`：`SET NX PX` 原子占位，要求正 TTL，保留毫秒精度。
- `Once`：同样原子占位，默认七天；显式传入的 TTL 必须为正。仅适合幂等辅助，不承诺业务 exactly-once。
- `Polling`：返回 `[0, maximum)` 范围内的循环下标，`maximum` 必须为正。

小于一毫秒的正 TTL 向上取整为一毫秒，避免提前失效。

## 字符串和 TTL

`Get`、`Set` 提供原始 `[]byte` 入口；`SetIfAbsent`、`GetOrSet`、`GetAndExpire`、`GetAndDelete`、`CompareAndSwap` 提供通用原子操作，比较删除继续使用已有 `Cad`。这些方法统一通过本包的命令执行器和脚本执行器，脚本遇到 `NOSCRIPT` 自动重载。空内容与不存在通过 `found` 区分，错误原样返回，不将类型错误当作未命中。

字符串写入的 TTL 为 `time.Duration`：`NoExpiration`（原始 `-1`）永久，`0` 立即过期，正数设置期限并向上取整到毫秒；其他负数报 `ErrInvalidTTL`。`NormalizeTTL` 可供其他组件复用该参数校验与精度约定。

`PTTL` 返回毫秒精度的 Duration，原始 `-1` 表示永久、`TTLNotFound`（原始 `-2`）表示不存在，负数哨兵不乘以毫秒。正数传参仍使用 `time.Second`、`time.Minute` 等标准 Duration。

`GetOrSet` 命中不续期，`stored` 表示写入条件是否满足；未命中且 TTL 为 0 时返回候选值和 `stored=true`，不留下键。`GetAndExpire` 使用 `NoExpiration` 移除到期时间，使用 0 返回旧值后删除；CAS 和条件写入未匹配时保持已有键不变。

`DeletePrefix` 按字面量前缀执行 SCAN 与删除，空前缀报错，Cluster 遍历各主节点。它不承诺并发写入时的事务快照，也不关闭客户端。

上述 TTL 约定适用于本节新增字符串接口及 tkcache。`Do` 仍传递原生命令参数；下文集合可选 TTL 的“省略/0 保持原 TTL”以及锁、冷却标记必须使用正期限的约定保留。

## 集合

S 系列：`SAdd`、`SIsMember`、`SMembers`、`SPop`、`SUnion`、`SCard`、`SRem`。

Z 系列：`ZAdd`、`ZRange`、`ZPage`、`ZCard`、`ZCount`、`ZIncrBy`、`ZRangeByScore`、`ZRank`、`ZRem`、`ZRemRangeByRank`、`ZRemRangeByScore`、`ZScore`。

成员读取返回原生字符串。有序集合使用新的 `ScoredMember{Member, Score}`，`ZAdd` 接收其切片，范围与分页方法也返回其切片。`withScores=false` 时 `Score` 为零。缺失的成员分数和排名返回 `redis.Nil`，缺失集合返回空列表。`ZPage` 页码从一开始，单个 Lua 原子读取总数及该页。`ZRangeByScore` 始终传入最小、最大分数，内部按升降序组织命令；可传入 `[offset, count]` 分页。

集合方法最后可选传 TTL：省略或传零保持原有 TTL，正值在执行同一 Lua 内更新 TTL，负值报错。`SUnion` 仅在所有 key 存在时重置它们的 TTL。多 key Lua 和 Redis 集合运算在 Cluster 中要求 key 位于同一 slot，可由调用方使用相同 `{hash-tag}`。

## 分布式锁

```go
lock, err := client.NewLock("work:{job}", tkredis.WithExpiration(3*time.Second))
if err != nil {
    return err
}
ok, err := lock.TryLock(ctx)
if err != nil || !ok {
    return err
}
defer lock.Unlock(context.Background())
```

每次获取使用新的随机 owner，以 `SET NX PX` 原子加锁。默认租约十秒，每三分之一租期续期；续期与释放通过 Lua 原子比较 owner。其他持有者的租约不会被续期或删除。`SpinLock` 按指定次数及重试间隔尝试，等待响应 ctx 与 Close 取消。

同一锁对象同时只表示一个持有者；重复获取返回 `ErrLockHeld`，释放完成后可以重新获取。`Unlock` 停止并等待维护退出，报告比较删除结果。ctx 取消、续期失败、丢失 owner 或 `Client.Close` 都会停止 ticker 并尝试释放自己的租约。清理使用独立的一秒超时上下文；服务器不可达时仍依靠 Redis TTL 自动释放。借用客户端应配置有限的读写超时并启用 `ContextTimeoutEnabled`，实际网络等待受底层客户端配置约束。

锁不提供 fencing token。租约失效后，已经开始的外部数据库操作不会自动撤销；需要严格失效隔离的业务必须另外管理执行权。

## 从 go-gf-lib 迁移

| 原入口 | 新入口/变化 |
| --- | --- |
| `NewClientWithConfig(ctx, config)` | `New(config)`，延迟连接，错误返回，不 panic |
| `NewClientWithOption(ctx, options...)` | `NewWithOption(options...)` |
| `RedisClient` | `Client` |
| `Do`、`Eval`、`EvalSha` 的 `*gvar.Var` | 原生 `any`，保留 `redis.Nil`，显式 JSON |
| `DoRedisArgs` 原地转换 | `EncodeArgs` 返回复制后的切片 |
| `PipelineResult.Val` | 新的 `Result.Data`，逐条保留失败项 |
| `ScriptLoad`、`ScriptLoadByPath` | 同名入口，名称注册并发安全，缺失后自动重载 |
| `CD`、`SetCD`、`Cad`、`Polling` | 同名入口，原子执行，使用毫秒精度 |
| `RedisLock`、`RedisLockOption` | 新的 `Lock`、`LockOption`；保留 `NewRedisLock` 构造方法名，推荐 `NewLock` |
| `plcache.SAdd/SIsMember/SMembers/SPop/SUnion/SCard/SRem` | 移到 `Client` 的同名方法，集合读取返回 `[]string` |
| `plcache.SSAdd` | `ZAdd`，把旧 `map[any]float64` 显式转换为 `[]ScoredMember` |
| `SSRange`、`SSPage` | `ZRange`、`ZPage`，返回新的 `[]ScoredMember` |
| `SSCard`、`SSCount`、`SSIncrby` | `ZCard`、`ZCount`、`ZIncrBy` |
| `SSRangeByScore`、`SSRank`、`SSRem` | `ZRangeByScore`、`ZRank`、`ZRem` |
| `SSRemRangeByRank`、`SSRemRangeByScore`、`SSScore` | `ZRemRangeByRank`、`ZRemRangeByScore`、`ZScore` |
| `plcache.Once` | `Client.Once`，正 TTL 的原子占位 |

这些是明确的 API 迁移，不是只替换 import 即可的兼容层；原 GF 字段类型没有被偷偷改写。

## 验证

本包测试仅使用进程内 `miniredis`，不连接真实 Redis、MySQL 或 Kafka。覆盖命令 nil/空值、显式 JSON 与原始切片保护、管道部分失败、Lua 重载和并发注册、原子占位、集合 TTL 和分页、锁 owner 校验/续期/取消/重复获取以及客户端所有权。

```sh
go test -race ./toolkit/tkredis
go vet ./toolkit/tkredis
```

miniredis 不验证真实 Redis Cluster、网络分区或服务端持久化；这些不是本次单元测试的验收结果。
