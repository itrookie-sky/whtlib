# tkhttp 调用层中间件实施计划

> 使用 executing-plans 在当前 main 按任务实施；用户已批准执行，沿用相关测试授权，不采用 TDD、不新增审核和 worktree。

**目标：** WithMiddleware 注入完整调用层中间件，观察参数校验、读取、解码最终错误，并统一错误分类和 HTTP 状态码获取。

**架构：** Handler 沿用 Do 的参数，Middleware 包装 Handler。配置列表在 New 复制冻结，每次请求使用独立状态构建处理链，日志在外层统一输出。错误声明和包装集中在 error.go，保留原始错误链。

**技术栈：** Go 1.26.3、标准库 net/http、现有 adapter.LogAcc；无新增依赖。

**方案：** [tkhttp 中间件设计](../specs/2026-10-23-tkhttp-middleware-design.md)。

## 全局约束

- main 开发，单个中文提交直接推送；不改已有共享接口字段类型。
- 核心执行最多一次，不恢复 retry。中间件 next 同步且最多一次；检测无效结果与重复调用。
- 收到 HTTP 响应后保留 StatusCode，即使读取或解码出错；未收到响应时返回 nil。
- 参数校验在链内，LogAcc 创建和唯一日志出口在链外；初始化错误和无效日志工厂仍先返回。
- 不连接外部平台和数据库，只执行受影响模块与本地示例验证。

## 验证重点

- 配置数量超限、nil/typed nil ctx 仍可被中间件观察。
- 前后置顺序、重复 WithMiddleware、配置列表复制及并发调用互不串扰。
- 中间件提前拒绝、合成响应、nil Handler、nil/nil 结果和重复 next 不额外发送请求。
- 中间件包装、替换或消除核心错误后，最终日志反映返回结果并只输出一次，源码位置保持正确。
- 每类哨兵错误、底层原因和 StatusError 的 errors.Is/AsType 均可用。

## 任务 1：错误统一与处理链

**文件：** error.go、middleware.go、options.go、client.go、request.go、hook.go、response.go、log.go。

**接口：** `Handler func(context.Context, string, string, ...RequestConfig) (*Response, error)`；`Middleware func(Handler) Handler`；`WithMiddleware(...Middleware) Option`，均使用命名返回值。

- [x] error.go 补充 ErrRequestEncode、ErrTransport、ErrResponseRead、ErrHTTPStatus、ErrResponseDecode、ErrMiddleware，集中包装助手和 StatusError.Is。
- [x] 新增中间件类型与调用包装，New 校验 nil 并复制列表，重复选项追加。
- [x] 分离日志外壳与核心 execute，完整核心错误流经 next，保持状态码及 Body 关闭语义。
- [x] 汇总最终结果与错误阶段，保持日志单次输出、调用位置、脱敏和超时约定。

## 任务 2：相关验证与文档

**文件：** middleware_test.go、error_test.go、现有相关测试及 README/设计稿。

- [x] 补充上述验证重点的测试，执行 `go test -race -count=1 -cover ./toolkit/tkhttp ./extension/logx ./adapter`。
- [x] 更新用法、完整错误清单、执行顺序和状态码说明；既有 HTTP 设计与方案同步。
- [x] 执行相关 vet/build；独立 GoFrame 示例 race/vet/build；检查 gofmt、diff、文档链接并同步 codegraph。
- [x] 核对 origin/main 基线、保护状态及本次变更范围。

交付动作：完成中文单提交，推送后回读远端 SHA 和工作区状态，并在最终回复报告结果。

## 执行记录

- 基线 `3cc13046bf1fa38733df5668bc9fbaba7f3fc1cd`，工作区干净。
- 接口依赖核对：任务 2 使用任务 1 的 Handler、Middleware、错误类别和完整结果约定，无冲突。测试授权沿用 tkhttp/LogAcc 授权；不额外请求审批。
- 完成完整错误透传、状态码保留、注册顺序、列表复制、无效结果、提前拦截、签名顺序、next 生命周期与并发隔离验证。同步等待已启动的异步 next，防止后台执行与最终日志竞争；日志采用中间件最终返回结果。
- 相关包 race/coverage 通过：tkhttp 96.2%，logx 92.1%；补充最终日志分类和调用位置用例后，tkhttp race/coverage 与 vet 再次通过。
- 相关包 vet/build 通过，独立 GoFrame 示例 race/vet/build 通过；无外部 API/数据库访问。
- gofmt、diff 空白、Markdown 代码块/本地链接和根依赖检查通过，codegraph 同步完成且 WithMiddleware 可检索。方案稿同期排版调整已保留。
