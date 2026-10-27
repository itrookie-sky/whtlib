# Logger.Clone 实施计划

目标：实现用户已确认的配置快照式 Clone。当前仓库在 main 开发，验证后直接 push，不使用 worktree 或功能分支。

接口：`(*Logger).Clone() (*Logger, error)` 与包级 `Clone() (*Logger, error)`；包级入口克隆 Default。
复制当前完整配置与 ContextFields 切片，保留上下文 key 的身份；继承 Level 快照和 Skip。
各实例使用独立 loggerCore、handler/outputState 与文件引用；文件 Writer 及其轮转策略继续共享。
已关闭源返回 os.ErrClosed；nil 或未初始化源返回现有 ErrInvalidConfig，不新增同义错误。
复用 Skip 的派生实现，保留原 Skip 签名和关闭态行为；Clone 不增加调用层数。

- [x] 提取内部配置快照逻辑，新增两个 Clone 入口。
- [x] 补充已获准的配置/字段隔离、文件引用、关闭态、全局入口、行号和并发测试。
- [x] 更新 README；运行 logx 及使用组件相关测试、race、vet、build 和 Windows 交叉编译。
- [x] 同步 codegraph，核对提交范围。

验证：logx、errx、tkhttp 的 race 测试及 GoFrame 示例的 race 测试通过；logx 的 vet、本机构建、Windows amd64 交叉编译通过。未执行 Windows 运行测试。

发布步骤：中文提交并普通 push main，核对远端 SHA。

已有未提交的 extension/logx/default.go 初始化错误包装属于用户改动，保留且不纳入本次提交。
