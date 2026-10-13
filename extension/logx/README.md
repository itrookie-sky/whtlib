# logx

基于标准库 `log/slog` 的日志扩展。所有日志调用必须传入 ctx，支持有序上下文 JSON、源码行号、Skip，以及可配置的文件轮转和归档清理。无第三方依赖。

## 默认文件输出

```go
package main

import (
    "context"
    "log/slog"
    "time"

    "github.com/itrookie-sky/whtlib/extension/logx"
)

func main() {
    type contextKey string
    const traceKey contextKey = "traceId"
    const userKey contextKey = "userId"

    logger, err := logx.New(logx.Options{
        TraceKey: traceKey,
        ContextFields: []logx.ContextField{
            {Name: "userId", Key: userKey},
        },
        Level: slog.LevelInfo,
        File: logx.FileOptions{
            Path: "./logs",
            File: "app.log",
            RotateSize: 100 * 1024 * 1024,
            RotateInterval: 24 * time.Hour,
            RotateBackupExpire: 24 * time.Hour,
        },
    })
    if err != nil {
        panic(err)
    }
    defer logger.Close()

    ctx := context.WithValue(context.Background(), traceKey, "abc123")
    ctx = context.WithValue(ctx, userKey, 1001)
    err = logger.Info(ctx, "创建订单成功", "orderId", "o123")
    if err != nil {
        panic(err)
    }
}
```

输出形如：

```text
2026/10/13 14:30:00 {"traceId":"abc123","userId":1001} app/main.go:42 INFO 创建订单成功 {"orderId":"o123"}
```

上下文 JSON 固定在日期后，traceId 第一，自定义字段按配置顺序排列。缺失字段省略，全缺失输出 `{}`。nil、typed nil 和空字符串省略；0 与 false 保留。JSON 编码失败会输出错误标记并返回错误，其他日志内容仍写入。

普通方法的可变参数为 slog 结构化属性；文本格式化使用 Infof 等方法。ctx 取消仍可记录日志，nil ctx 会 panic。默认 Info 级别，可通过 Options.Level 传入 slog.LevelVar 动态调整。

## 自定义 Writer

将 `Options.Writer` 设为 `os.Stdout`、`bytes.Buffer` 或其他 `io.Writer`。传入 Writer 后不会创建文件或启动清理任务，File 配置不生效，Logger.Close 不关闭外部 Writer。

FileWriter 也可独立通过 `logx.NewFileWriter(logx.FileOptions{...})` 创建，支持 Write、Rotate、Cleanup、Sync 和 Close。需要文件加终端双写时，使用 `io.MultiWriter(fileWriter, os.Stdout)`，并自行关闭 fileWriter。

## 文件默认配置

| 字段 | 默认值 | 覆盖与禁用 |
| --- | --- | --- |
| Path | ./logs | 自定义目录 |
| File | app.log | 单个 .log 文件名，不允许路径分隔符 |
| RotateSize | 100 MiB | 字节数；0 默认，-1 禁用 |
| RotateInterval | 24h | time.Duration；0 默认，-1 禁用 |
| RotateBackupExpire | 24h | time.Duration；0 默认，-1 禁用 |
| RotateBackupLimit | 0 | 正数限制归档数量；0 不限数量 |
| RotateCheckInterval | 1m | time.Duration；0 默认 |
| OnError | 向 stderr 输出 | 可传入 func(error) 接收后台维护错误 |

使用 `DefaultFileOptions()` 获取默认配置再覆盖字段，也可直接传入部分配置。-1 是精确的哨兵值，其他负数无效；时长禁用时使用 `-1`，不是 `-time.Second`。

分割在写入前检查，单条日志不会跨文件；单条自身大于阈值时允许该文件超限。归档默认保留 24h，从归档产生时间计算，不是逐条日志的存活时间。过期清理存在检查周期延迟，程序停止时不运行，重启时补做。首版不提供压缩。

只清理当前目录下精确匹配当前文件归档格式的普通文件，忽略活动文件、其他前缀、目录和符号链接。不能让多个进程或多个独立 FileWriter 操作同一个活动文件；同一进程可显式共享 FileWriter。

后台错误通过 OnError 报告，回调内不要调用当前 Logger/FileWriter 的 Close，也不要用当前 logger 递归记录。普通写入不逐条 fsync，需要持久化检查点时调用 FileWriter.Sync。

## 调用位置和关闭

默认显示日志调用处的 `目录/文件:行号`，`FullPath: true` 输出完整路径。在业务日志封装内调用 `logger.Skip(1).Info(ctx, "消息")` 可以定位上层调用者。Skip 追加层数，派生实例不修改原实例；负数或溢出 panic，超出栈时省略位置。

所有派生实例共享关闭状态，任一实例 Close 后，所有实例输出均返回 os.ErrClosed。内部创建的 FileWriter 随 Logger 关闭；外部 Writer 始终由调用方管理。请在应用退出时调用 Close 停止维护任务。
