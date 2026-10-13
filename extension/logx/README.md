# logx

基于标准库 `log/slog` 的日志扩展。所有日志调用必须传入 ctx，支持有序上下文 JSON、源码行号、Skip，以及可配置的文件轮转和归档清理。无第三方依赖。

## 输出目标

`WriteFile` 为 bool，默认 false：false 仅输出到终端（os.Stdout），不创建文件、目录或维护任务；true 同时输出到终端和文件。SetConfig 始终应用 WriteFile，false 关闭文件输出；文件模式下更新其他配置也需显式传入 WriteFile: true。切换只影响当前实例，最后一个文件引用释放时才关闭文件。

```go
logger, err := logx.New(logx.Config{WriteFile: false})
// 处理 err，结束时 logger.Close()。
logger.SetConfig(logx.Config{WriteFile: true})
logger.SetConfig(logx.Config{WriteFile: false})
```

## 文件输出示例

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

    logger, err := logx.New(logx.Config{
        WriteFile: true,
        TraceKey: traceKey,
        ContextFields: []logx.ContextField{
            {Name: "userId", Key: userKey},
        },
        Level: slog.LevelInfo,
        File: logx.FileConfig{
            Path: "./logs",
            File: "{y-m-d}.log",
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
    logger.Info(ctx, "创建订单成功", "orderId", "o123")
}
```

输出形如：

```text
2026-10-13 14:30:00.000 +08:00 {"traceId":"abc123","userId":1001} app/main.go:42 INFO 创建订单成功 {"orderId":"o123"}
```

上下文 JSON 固定在日期后，traceId 第一，自定义字段按配置顺序排列。缺失字段省略，全缺失输出 `{}`。nil、typed nil 和空字符串省略；0 与 false 保留。JSON 编码失败会输出错误标记，其他日志内容仍尝试写入。所有打印方法及内部 output 均无返回值，编码和写入错误由 output 连同当前 goroutine 的调用堆栈直接写入 stderr，不经过配置的 Writer，避免递归；初始化与 Close 仍返回错误。

普通方法的可变参数为 slog 结构化属性；文本格式化使用 Infof 等方法。ctx 取消仍可记录日志，nil ctx 会 panic。默认 Info 级别，Config.Level 在 New/SetConfig 时读取级别快照，之后通过 SetConfig 调整，外部 LevelVar 的变化不会传播到实例。

## 全局单例

```go
logx.SetConfig(logx.Config{WriteFile: true, File: logx.FileConfig{Path: "./logs", File: "{y-m-d}.log"}})
logx.Info(ctx, "创建订单成功", "orderId", "o123")
logx.Default().Infof(ctx, "订单：%s", "o123")
```

`Default()` 始终返回同一个实例。仅导入包或调用 Default 不创建文件；第一次打印或 SetConfig 才初始化输出资源，默认使用终端。包级 Debug、Info、Warn、Error、Log 及带 f 的方法代理同一实例，并保持正确的业务调用行号。`logx.Skip(n)` 创建单例的派生实例，`logx.Close()` 关闭单例；关闭后不自动重建。

单例属于 logx，不修改 `slog.Default()`。如需设置文件路径，请在第一次打印之前调用 SetConfig，避免先初始化默认文件。

## 修改当前实例配置

```go
logger.SetConfig(logx.Config{WriteFile: true, Level: slog.LevelDebug}).Debug(ctx, "启用调试")
logger.SetConfig(logx.Config{
    WriteFile: true,
    File: logx.FileConfig{RotateSize: 20 * 1024 * 1024, RotateBackupExpire: 48 * time.Hour},
})
```

SetConfig 返回原来的 `*Logger`，WriteFile 始终生效（含默认 false）；其他字段合并规则：nil、空字符串、0、false 均表示沿用旧值；File 内部字段也逐项合并。ContextFields 为 nil 时保留，非 nil 空切片清空列表，非空切片整体替换并复制。Level 是接口，显式传 `slog.LevelInfo` 可以恢复 Info 级别。

因此，SetConfig 不能用零值重置 Skip、FullPath、RotateBackupLimit；需要这些重置行为时创建新实例。轮转的 -1 禁用值可正常传入。

配置变更与当前实例的打印、Close 同步，每条日志使用完整的一版配置。New 和 Skip 创建的实例各自持有独立的 loggerCore，SetConfig 和 Close 不影响其他实例。验证、新文件初始化或共享策略检查失败时，输出错误和堆栈并保留旧配置；成功切换后释放旧文件引用，旧资源关闭失败仅输出诊断。

## 私有全局文件管理器

Writer 封闭在包内部，不再提供 Config.Writer、FileWriter 或 NewFileWriter。通过 File.Path 和 File.File 配置输出目标。

所有实例共用一个私有文件管理器，同一规范化绝对路径共用一个文件句柄、写入锁、轮转计数及维护任务。不同文件分别管理，目录符号链接会解析到真实路径。活动文件符号链接不接受；不支持通过硬链接或其他文件系统别名指向同一文件。

同一文件的轮转参数必须一致：New 遇到冲突返回错误；SetConfig 遇到冲突打印堆栈并保留旧配置。只有一个实例引用该文件时，允许修改其轮转参数。路径切换只影响当前实例，原文件继续供其他实例使用。

启用文件输出的实例独立持有文件引用；Close 释放当前引用，最后一个引用释放后停止维护并关闭文件。关闭中的文件完成释放前，新实例会等待，不会同时打开第二个 Writer。共享限定在同一进程，多进程必须使用不同文件。

OnError 按实例登记，共享文件的后台错误通知当前登记的各实例回调；修改回调不影响其他实例。回调可能已被本轮维护快照捕获。回调应快速返回，不应重入 logx API。字段格式化函数中也不要调用当前实例的 SetConfig/Close。

## 文件默认配置


| 字段                | 默认值         | 覆盖与禁用                          |
| --------------------- | ---------------- | ------------------------------------- |
| Path                | ./logs         | 自定义目录                          |
| File                | {y-m-d}.log        | 单个 .log 文件名，支持花括号日期表达式，不允许路径分隔符  |
| RotateSize          | 100 MiB        | 字节数；0 默认，-1 禁用             |
| RotateInterval      | 24h            | time.Duration；0 默认，-1 禁用      |
| RotateBackupExpire  | 24h            | time.Duration；0 默认，-1 禁用      |
| RotateBackupLimit   | 0              | 正数限制归档数量；0 不限数量        |
| RotateCheckInterval | 1m             | time.Duration；0 默认               |
| OnError             | 向 stderr 输出 | 可传入 func(error) 接收后台维护错误 |

使用 `DefaultFileConfig()` 获取默认配置再覆盖字段，也可直接传入部分配置。-1 是精确的哨兵值，其他负数无效；时长禁用时使用 `-1`，不是 `-time.Second`。

分割在写入前检查，单条日志不会跨文件；单条自身大于阈值时允许该文件超限。归档默认保留 24h，从归档产生时间计算，不是逐条日志的存活时间。过期清理存在检查周期延迟，程序停止时不运行，重启时补做。首版不提供压缩。

只清理当前目录下精确匹配当前文件归档格式的普通文件，忽略活动文件、其他前缀、目录和符号链接。同一进程由全局文件管理器统一协调；不同进程不得操作同一个活动文件。

后台错误通过 OnError 报告，默认写入 stderr。普通写入不逐条 fsync，当前不对外提供 Writer 或 Sync 接口。

## 调用位置和关闭

默认显示日志调用处的 `目录/文件:行号`，`FullPath: true` 输出完整路径。Skip 追加层数，复制当前配置快照并创建独立实例；负数或溢出 panic，超出栈时省略位置。关闭实例上调用 Skip 得到的实例同样处于关闭状态。

封装打印方法时，独立派生实例需要关闭，建议在封装组件初始化时创建并复用：

```go
wrapped := logger.Skip(1)
defer wrapped.Close()
// 在封装方法中调用 wrapped.Info(ctx, "消息")。
```

不要在高频打印中持续创建不关闭的 Skip 实例。关闭父实例不会关闭派生实例，反之亦然。每个实例都应在不再使用时 Close，应用退出时也应关闭全局单例。重复 Close 安全；关闭后打印向 stderr 输出关闭错误与堆栈。

## 文件名日期表达式

默认 `FileConfig.File` 为 `{y-m-d}.log`。仅花括号内部识别小写 `y`（四位年份）、`m`（两位月份）、`d`（两位日期），花括号外保持原样：

| 配置 | 2026 年 1 月 2 日的文件名 |
| --- | --- |
| `sql-{y-m-d}.log` | `sql-2026-01-02.log` |
| `ymd-{ymd}.log` | `ymd-20260102.log` |
| `{y-m-d}.log` | `2026-01-02.log` |
| `system-y-m-d.log` | `system-y-m-d.log` |

花括号内可使用 `-`、`_`、`.` 分隔符，暂不支持其他占位符；空表达式、嵌套或未配对花括号返回配置错误。文件路径不能放在表达式内。

按本地时间在每次写入前检查目标名称，跨日、跨月或跨年后自动切换，已有日期文件以追加模式打开。后台检查也会切换日期，因此空闲期间可能创建当天的空文件。文件切换失败时保留旧句柄，本条文件写入报告错误，终端输出不受影响。

同一目录、同一表达式共享 Writer。不同表达式（包括固定文件名）若可能生成相同名称，保守拒绝同时启用，即使当前日期尚未冲突；修改路径或使用不同业务前缀可避免冲突。

按大小或时间轮转的归档，以展开后的日期文件名作为前缀。清理同时匹配当前表达式的历史日期文件和归档：历史日期文件从最后修改时间计算保留期，归档从归档名称中的时间计算；两者合计应用保留数量。当前打开文件和当天目标文件不删除。默认保留 24h。
