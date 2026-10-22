# Store TTL 语义实施计划

> 用户已确认写入和查询统一采用负数哨兵，并批准调整、补充相关测试。本会话直接实施，不使用 TDD、功能分支或 worktree。

**目标：** tkcache 的三种 Store 与 tktoken.Store 使用统一 TTL 约定。

**已确认约定：** 写入 `-1` 永久、`0` 立即过期、正数设置期限；查询 `-1` 永久、`-2` 不存在、非负数为剩余期限。保留 `time.Duration` 和 `found` 返回值，哨兵为原始 `time.Duration(-1/-2)`，不是负秒或负毫秒；写入小于 `-1` 的参数仍报错。

**结构：** tkredis 定义并统一实现 `NoExpiration`、`TTLNotFound`、TTL 校验、字符串原子操作和前缀清理，tkcache 复用常量和参数校验，Redis Store 只做接口适配。内存负责即时删除及查询结果，文件复用内存行为。tktoken 会话仍使用原有正 TTL，不改变 Config.Timeout 及其他字段类型。

**用户追加约束：** tkcache 不重复实现 Redis 能力；复用 tkredis.Client。已有 `Do`、`Cad` 和脚本执行器继续复用，缺少的通用字符串操作补充在 tkredis，所有 Redis 访问经过其生命周期检查。

## 实施步骤

- [x] 调整 `toolkit/tkcache/cache.go`：常量、参数标准化、API 注释。
- [x] 调整 `memory_store.go`、`redis_store.go`：永久/即时过期/查询哨兵；GetOrSet 即时过期仍返回候选值，条件操作未匹配时不删除数据。
- [x] 在 `toolkit/tkredis/string.go`、`key.go` 提供通用字符串原子操作、PTTL 和前缀清理，抽取统一脚本执行器复用于 EvalSha 和通用字符串接口；Redis Store 移除 go-redis 和 Lua。
- [x] 更新 `toolkit/tktoken/token.go` 的 Store 契约与离线测试 Store，保持实现符合接口约定。
- [x] 将原有测试中表达永久的 `0` 改为 `NoExpiration`，补充三种 Store、原子操作、回源和文件重启的边界测试。
- [x] 更新 tkcache、tktoken README，明确 `0` 写入行为发生变化、哨兵单位及 Once 仍仅接受正期限。
- [x] 运行 tkcache、tkredis、tktoken 三个相关包的竞态测试和 go vet、检查差异，并同步 codegraph。

## 核心边界

- Redis Lua 参数不能将 `-1` 除以毫秒后截断为 `0`。
- TTL 为 0 时，Set 删除旧值；SetIfAbsent / CompareAndSwap 只在条件满足时生效；GetAndExpire 返回旧值后删除；GetOrSet 返回已有值或候选值，未命中时不留下缓存。
- 文件快照依然以零到期时间表达永久；不更改快照版本，不改写既有永久项。
- GetOrLoad 的零 TTL 返回回源结果但不留下缓存；不改变旧回源不得覆盖后续写入的协调规则。
- 测试仅使用内存、临时文件和 miniredis，不连接真实数据库。

## 执行记录

- 已检查 normalizeTTL 影响范围和 Store 实现，已获取用户的测试授权。

- 完成 tkredis 通用字符串与 TTL 接口，Redis Store 仅保留 tkredis.Client 适配；通过现有统一命令/脚本执行器，并复用 Cad。
- 验证：`go test -race ./toolkit/tkcache ./toolkit/tkredis ./toolkit/tktoken` 全部通过；`go vet ./toolkit/tkcache ./toolkit/tkredis ./toolkit/tktoken` 通过；`git diff --check` 通过。
- 新增测试覆盖三种 Store 的 TTL 哨兵、立即过期、原子条件、零 TTL 回源、文件重启、Lua NOSCRIPT 重载、字面量前缀清理和客户端关闭状态。
