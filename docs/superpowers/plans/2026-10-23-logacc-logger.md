# LogAcc 日志后端接口化实施计划

> 使用 executing-plans 在当前 main 完成；用户已指定改造方向，直接实施，不采用 TDD、不自动审核，验证后直接推送。

**目标：** LogAcc 通过 adapter.Logger 注入日志后端，能够使用 GoFrame Logger 等其他实现。

**架构：** WithLogger 和累加器字段改用 adapter.Logger，LogAcc 独立组织字段 JSON 与耗时正文，接口负责输出与调用位置。logx 提供借用原实例的 Logger 适配器；GoFrame 接入作为独立示例 module，根模块保持框架无关。

**约束：** 保留 Logger/LogAcc 共享接口的方法类型、原有字段累计及 SetStartTime API；不恢复 Clone。tkhttp.LogAccFactory 每次构造新累加器。仅修改本功能文件，所有验证采用本地后端或 httptest。

## 任务 1：LogAcc 后端解耦

- [x] `extension/logx/log_acc.go` 的 logger、WithLogger 和 snapshot 改用 adapter.Logger；nil/typed nil 保留默认后端约定。
- [x] 通过惰性正文格式化复用字段编码，保持排序、slog 值/分组、编码失败标记和诊断；被后端过滤时不提前编码。
- [x] `extension/logx/logger_adapter.go` 提供 `NewLoggerAdapter(logger *Logger) (result adapter.Logger)`；Skip 仅生成借用视图，不增加文件引用或关闭后端。
- [x] 调整已有调用及测试，验证其他 adapter.Logger 后端、ctx/级别、字段快照、耗时、Skip 和后端所有权。

## 任务 2：GoFrame 示例

- [x] `examples/logacc_goframe` 独立 module 使用 GoFrame v2.9.0，实现 Logger 薄适配，包含普通/格式化方法及 Skip。
- [x] 示例展示 GoFrame 后端注入 NewLogAcc 和 HTTP LogAccFactory；根 go.mod/go.sum 不增加 GoFrame。
- [x] 本地测试验证真实 GoFrame 输出、业务调用行号、过滤、并发借用和单次 HTTP 日志，无外部平台或数据库依赖。

## 任务 3：文档与交付

- [x] 更新 README、logx/tkhttp 用法及 HTTP 设计中的日志后端说明。
- [x] 执行受影响包和示例的 race 测试、vet、构建，格式与文档链接检查。
- [x] 同步 codegraph，核对 main 远端基线、保护状态及本次变更范围。

交付动作：以中文单提交推送 main，回读远端 SHA 和工作区状态，在最终回复报告结果。

## 执行记录

- 基线：`3d8ea9bd84a62366a75a7fa95b849ee8b70bf08b`，工作区干净。
- 已用 codegraph 阅读 LogAcc 和共享接口；已加载全局与 Go 规范、Modern Go Guidelines。GoFrame 示例按其 v2 API 核对，库本体不采用框架。
- 本次相关测试沿用用户对 tkhttp/LogAcc 测试的授权；不新增审批或审核步骤。
- `go test -race -count=1 -cover ./adapter ./extension/logx ./toolkit/tkhttp` 通过，logx 覆盖率 92.1%，tkhttp 95.8%；相关包 vet/build 通过。
- 独立示例 `go test -race -count=1 -cover ./...`、vet、build、`go run .` 通过；测试覆盖真实 GoFrame 后端及本地 HTTP，无外部 API 请求。
- gofmt、diff 空白、Markdown 代码块与本地链接检查通过；根 go.mod/go.sum 无改动。codegraph sync 完成，两个新增适配器构造函数均可检索。
