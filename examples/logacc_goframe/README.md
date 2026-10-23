# GoFrame Logger 注入 LogAcc

`logx.WithLogger` 接收 `adapter.Logger`。此示例借用 GoFrame 的 `*glog.Logger` 作为日志后端，LogAcc 仅负责累计字段、耗时和正文 JSON。

```go
native := glog.New()
native.SetStack(false)
backend := NewGoFrameLogger(native)

acc := logx.NewLogAcc(logx.WithLogger(backend), logx.WithCost(time.Now()))
acc.SetValue("orderId", "demo-001").Info(ctx, "订单处理完成")
```

输出正文类似：

```text
[35µs] 订单处理完成 {"orderId":"demo-001"}
```

时间、级别、context 字段和源码位置由 GoFrame 配置控制。[logger.go](logger.go) 实现完整接口：普通方法按空格拼接正文，格式化方法使用 GoFrame 的 `*f` 方法，Warn 映射为 Warning。字段采用惰性格式化，GoFrame 过滤级别后才编码 JSON。

`Skip` 返回独立的借用视图，并补偿适配器自身调用层；GoFrame 的 Skip 是绝对值，此处保留原 StSkip 后设置新值。每次通过 GoFrame Logger 的 Clone 创建配置视图，避免修改注入实例。应先配置后端，再开始并发使用。后端资源由创建方管理，LogAcc 无需 Close。

注入 tkhttp 时，后端共享，每次调用创建新的累计器：

```go
client, err := tkhttp.New(tkhttp.Config{BaseURL: "https://api.example.com"},
    tkhttp.WithLogAcc(func() (acc adapter.LogAcc) {
        return logx.NewLogAccAdapter(logx.WithLogger(backend))
    }),
)
if err != nil {
    return
}
defer client.CloseIdleConnections()
// tkhttp 自动通过 SetStartTime 设置每次调用的入口时间。
response, err := client.Get(ctx, "/orders")
```

完整构造函数见 [main.go](main.go)，本地 HTTP 测试见 [logger_test.go](logger_test.go)。

此目录为独立 module，使用 GoFrame v2.9.0，通过 replace 引用仓库根模块，根 go.mod/go.sum 不引入 GoFrame。运行：

```sh
cd examples/logacc_goframe
go run .
go test -race ./...
```

GoFrame API 参考：[日志组件](https://goframe.org/en/docs/core/glog)、[链式操作](https://goframe.org/en/docs/core/glog-chaining)。
