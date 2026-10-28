# errx

在 Go 标准错误链之上提供调用堆栈、结构化字段、错误树遍历、业务码和诊断快照。
只依赖标准库，测试也不引用其他组件，适用于 Go 1.27.3 及以上版本。与日志、HTTP 等组件的联动验证和示例放在消费方。

| 接口 | 返回值 | 用途 |
| --- | --- | --- |
| `WithStack(err)` | `error` | 在调用处采集堆栈，已有堆栈时复用 |
| `Stack(err)` | `[]Frame` | 提取首个堆栈的独立副本 |
| `WithFields(err, fields)` | `error` | 附加字段 map 的浅复制 |
| `Fields(err)` | `map[string]any` | 汇总全部分支字段，外层及先访问的分支优先 |
| `Walk(err)` | `iter.Seq[error]` | 深度优先先序遍历整个错误树 |
| `Leaves(err)` | `iter.Seq[error]` | 按上述顺序遍历全部叶子原因 |
| `CodeOf(err)` | `string` | 提取首个非空业务码 |
| `Describe(err)` | `Description` | 生成结构化诊断快照，支持 JSON 与 slog |

`CodeError`、私有包装错误及其方法、包装函数统一在 [error.go](error.go)，常量集中在 [consts.go](consts.go)。
错误创建、类别判断、类型提取和聚合仍直接使用 `errors.New`、`errors.Is`、`errors.AsType`、`errors.Join`、`fmt.Errorf("...: %w", err)`。

## 包装与提取

```go
func loadOrder(id string) (err error) {
    err = queryOrder(id)
    if err != nil {
        err = errx.WithStack(err)
        err = errx.WithFields(err, map[string]any{
            "operation": "loadOrder",
            "orderId":   id,
        })
        return
    }
    return
}
```

WithStack、WithFields 不改变原错误文本，并通过 Unwrap 保留 `errors.Is/AsType` 的行为。
元数据不会自动打印，也不会自动附加到 Error 文本。

```go
if errors.Is(err, context.DeadlineExceeded) {
    // 原始类别仍可判断。
}
fields := errx.Fields(err)
frames := errx.Stack(err)
```

Frame 包含 Function、File、Line，首帧指向调用 WithStack 的位置，最多采集 64 帧。
错误树的任何分支已有非空堆栈时，WithStack 返回原错误；Stack 返回先序遍历的首个堆栈。
各分支的独立堆栈可通过 Describe 的 Nodes 查看。

WithFields 复制传入 map；Fields 返回新的汇总 map。同名字段外层优先，分支之间按 Walk 顺序先到优先。
字段值为 nil 也会覆盖内层同名字段。复制为浅复制：字段值中的指针、map、slice 仍由调用方管理和同步。
修改提取出的字段 map 或堆栈 slice 不会影响包装器。

## 错误树

```go
combined := errors.Join(errA, fmt.Errorf("加载 B: %w", errB))
for node := range errx.Walk(combined) {
    // 包括 combined 和中间包装节点。
    _ = node
}
for cause := range errx.Leaves(combined) {
    // 依次得到 errA、errB。
    _ = cause
}
```

两个迭代器均支持 break 和重复迭代。遍历同时识别 `Unwrap() error` 与 `Unwrap() []error`，
忽略 nil 子节点；共享错误在每条分支路径分别出现，不按值去重，因此也支持非可比较的错误类型。
错误树须像标准 errors 的输入一样无环，且遍历期间不得修改其结构。
本包不提供单一 RootCause，因为多分支可能有多个末端原因。

## 业务码

```go
err := &errx.CodeError{
    Code:    "ORDER_NOT_FOUND",
    Message: "订单不存在",
    Err:     original,
}
code := errx.CodeOf(fmt.Errorf("load order: %w", err))
// code == "ORDER_NOT_FOUND"
```

CodeError.Error 优先返回 Message，缺失时依次返回 Code、原始错误文本、默认说明。
业务码由各包或业务层集中定义，errx 不绑定 HTTP 状态码或业务枚举。
同码的两个 CodeError 不自动满足 errors.Is；errors.Is 用于原有哨兵或实例判断，CodeOf 用于取业务码。
外层非空业务码优先；空码会继续搜索内层和后续分支。

## 诊断与日志

```go
detail := errx.Describe(err)
logger.ErrorContext(ctx, "操作失败", "error", detail) // 标准 slog
```

Description 的 Message、Code、Fields、Stack 分别为根错误文本、首个业务码、合并字段、首个堆栈。
Nodes 保留全部节点的 Type、Message、Code、Fields、Stack；Parent 是父节点在 Nodes 中的索引，根节点为 -1。
例如 Join 的两个叶子会形成 Parent 为 `[-1, 0, 0]` 的三个节点，不会丢失分支关系。
结构使用平铺节点列表，不依赖递归序列化。

Description 实现 `slog.LogValuer`，也能直接交给 JSON 编码器。
字段值的 JSON 可编码性由调用方保证；快照与提取方法一样只浅复制字段值。
Describe 会显式读取每层原始 Error 文本和源码路径，供内部诊断使用；它不等同于可直接返回给客户端的错误响应。
例如外部包装错误的 Error 隐藏底层文本时，显式 Describe 仍会遍历并读取其底层原因。

外部错误可通过以下方法提供元数据，无需依赖 errx 的具体包装类型：

```go
ErrorCode() string
Fields() map[string]any
StackTrace() []errx.Frame
```

这是按方法签名匹配的约定；其他堆栈库若返回不同类型，需要调用方提供适配。

## 空值

- WithStack(nil)、WithFields(nil, ...) 返回 nil；空字段不增加包装。
- Stack(nil)、Fields(nil) 返回 nil；CodeOf(nil) 返回空字符串。
- Walk(nil)、Leaves(nil) 不产生元素；Describe(nil) 返回零值。
- typed nil 仍遵循 Go 的 error 接口语义，不会被自动转换为 nil。

包装完成后可并发读取；CodeError 的公开字段、外部错误方法及引用类型字段值需要调用方保证并发安全。
