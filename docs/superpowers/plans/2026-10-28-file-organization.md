# 文件职责归并与 LogAcc 接口整理

**目标：** 扫描整个项目，按用户指定的职责归并相关定义，取消 LogAcc 的链式返回及转发包装。

**约束：** 在当前 main 开发，不使用 worktree，不新增测试，不改变已有公开字段类型；保留 Logger.Skip 的独立视图语义。完成必要验证后直接提交并 push，不执行审核流程。

## 执行步骤

- [x] 扫描所有 Go 文件及包级规则，记录归并映射和变更前的声明快照。
- [x] 将 logx 实例、调用位置和 Logger 适配放入 logx.go；输出编码放入 handler.go；LogAcc 定义集中在 log_acc.go。
- [x] 将 errx 遍历和信息提取归并为 errx.go；tkua 分类规则归并为 classification.go，解析结果放入 parser.go。
- [x] 将 tkhttp 配置选项归并到 config.go、请求钩子归并到 request.go；tksign 构造选项归并到 sign.go；tkredis 通用命令参数和键操作归并到 client.go。
- [x] 把 tkcache 回源配置放回 loader.go，Kafka 消息类型放回 producer.go，各包常量集中到 consts.go，保留 error.go 和职责独立的后端实现。
- [x] LogAcc 的 SetStartTime、SetValue、Update、SetErr、Skip 全部无返回值，直接实现 adapter.LogAcc，移除适配包装并迁移调用、示例和现有测试。
- [x] 核对声明快照及引用；执行受影响模块的现有测试、vet 和构建，避免外部数据库和全项目集成测试；同步 codegraph。
- [x] 更新项目规则及本计划完成状态，核对变更、当前 main 与远端基线。

**交付：** 以一个中文提交 push 到 main，随后确认远端精确 SHA。

**验证结果：** 声明快照确认除 LogAcc 接口及其迁移外实现不变；十个受影响包及 GoFrame 示例的现有竞态测试通过，相关 vet 与根模块构建通过，codegraph 已同步。未新增测试，未连接外部数据库。
