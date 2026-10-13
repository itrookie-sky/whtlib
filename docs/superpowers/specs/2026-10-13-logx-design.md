# logx 模块设计

`logx` 位于 `extension/logx`，基于标准库 `log/slog`，强制传入 `context.Context`。上下文特殊字段组成有序 JSON，紧跟日期时间输出；支持源码位置回溯、Writer 注入，以及内置文件 Writer 的分割、轮转和清理。全部使用标准库，不引入 GoFrame 依赖。

已确定：使用 slog、ctx 必传、traceId 首位、有序自定义字段、Skip(n)、接收 io.Writer、提供默认文件 Writer、分割阈值和保留时长可配置、默认保留 24h。本设计已在 extension/logx 实现；具体调用示例见该包 README。

## 组件职责

```text
业务调用 Logger（ctx 必传）
    → slog.Record（时间、级别、正文、业务属性、调用位置）
    → 自定义 slog.Handler（上下文提取与日志格式化）
    → io.Writer（外部 Writer 或内置 FileWriter）
```

Logger 负责入口、级别判断和调用位置；Handler 负责格式与同步输出；FileWriter 负责文件生命周期，不解析 ctx 或正文。不修改 slog 全局实例，不保存请求 ctx。首版不包含异步日志队列、进程退出接口或自动链路追踪集成。

## 输出格式

```text
日期时间 {上下文 JSON} 文件:行号 级别 正文 [业务属性 JSON]
```

末尾业务属性 JSON 仅在传入结构化属性时输出，方括号表示可选部分，不是实际字符。

```text
2026/10/13 14:30:00 {"traceId":"abc123","userId":1001} order/service.go:42 INFO 创建订单成功
2026/10/13 14:30:01 {"userId":1001} order/service.go:42 INFO 创建订单成功 {"orderId":"o123"}
2026/10/13 14:30:02 {} order/service.go:42 ERROR 创建订单失败
```

日期默认使用本地时区，格式为 YYYY/MM/DD HH:mm:ss。上下文 JSON 固定处于日期之后首位，traceId 存在时必须是对象第一个成员，其余字段按配置顺序输出。无有效字段时输出 `{}`。

键和值使用 encoding/json 编码并保留 JSON 类型，正文换行与回车转义，每条记录以一个真实换行结束。整条日志为文本与 JSON 块的混合格式，不是完整 JSON 文档。

JSON 对象语义上无顺序，但本模块保证顶层 context 成员的输出顺序。使用有序切片逐项编码键和值，不把全部字段放入 map；嵌套对象不额外保证业务顺序。

## Logger 配置和接口

New 接收 Options，返回 Logger 和初始化错误。构造时复制字段切片并校验配置，之后保持只读；动态级别可使用 slog.LevelVar。

| 配置项 | 默认值 | 含义 |
| --- | --- | --- |
| Writer | nil | 接收 io.Writer；非 nil 使用外部 Writer，nil 创建内置 FileWriter，typed nil 返回错误。 |
| File | 默认文件配置 | FileOptions，仅未传 Writer 时使用。 |
| Level | slog.LevelInfo | 接收 slog.Leveler，过滤低于阈值的日志。 |
| TraceKey | 字符串 traceId | 从 ctx 读取 traceId 的 key，输出名称固定为 traceId。 |
| ContextFields | 空切片 | 每项包含输出名称 Name 与读取键 Key，按切片顺序输出。 |
| Skip | 0 | 在业务调用位置基础上额外跳过的层数。 |
| FullPath | false | 默认显示最后一级目录及文件名；true 输出完整路径。 |

context key 支持可比较的自定义类型。TraceKey 未设置时使用默认值；字段 Key 不允许 nil；不可比较或 typed nil 的 key 返回错误。字段名限定 ASCII 字母或下划线开头，后续为字母、数字或下划线，拒绝重复名称及保留名称 traceId。

| 方法 | 行为 |
| --- | --- |
| Debug、Info、Warn、Error | 接收 ctx、消息及可变结构化属性，返回错误。 |
| Log | 接收 ctx、slog.Level、消息及结构化属性，返回错误。 |
| Debugf、Infof、Warnf、Errorf | 接收 ctx、格式串及参数，以 fmt.Sprintf 生成正文，返回错误。 |
| Skip(n) | 返回额外跳过 n 层调用的派生 Logger。 |
| Close | 关闭共享实例体系，仅释放 Logger 自己创建的内置 Writer。 |

普通方法遵循 slog 的消息加属性模型，可变参数不再以 fmt.Sprint 拼接正文；格式化文本使用带 f 方法。业务属性在行尾独立输出，不能覆盖日期后的 context JSON。

所有输出方法要求非 nil ctx，nil 直接 panic；无请求上下文时显式传 context.Background()。ctx 取消不阻止日志输出。Writer.Write 和资源关闭接口不要求 ctx。

logx 先调用 Handler.Enabled，过滤后的日志不提取字段、不解析栈、不格式化正文。使用 slog.NewRecord 构造 Record，再调用 Handler.Handle 并返回其错误，不经会丢弃 Handler 错误的 slog.Logger 常用输出方法转发。

Handler 完整实现 Enabled、Handle、WithAttrs、WithGroup，遵循值解析、空属性、空组和嵌套分组约定。派生 Handler 共享输出锁，属性配置只读。Record 时间或 PC 为零时，分别省略时间或源码位置。

## 上下文字段

每次先读取 TraceKey，再读取 ContextFields，不遍历或猜测其他 key，不生成 traceId、不回写 ctx。业务可将外部追踪系统的 traceId 放入约定 key。

nil、typed nil 和空字符串省略；0 与 false 保留，其余值按标准 JSON 规则编码。调用方不得在输出期间并发修改字段引用的对象。

某字段编码失败时，替换成 JSON 字符串 `"[json encode error]"`，继续输出并返回带字段名的错误；同时写入失败时通过 errors.Join 合并。用户自定义 MarshalJSON 内部的 panic 不主动恢复。

## 调用位置与 Skip

默认配置下 Skip(0) 指向业务直接调用行，内部封装层自动跳过。初始化 Skip 与链式 Skip(n) 累加，Skip(0) 不重置已有数值。封装函数使用 Skip(1) 定位其上层调用者。

初始化负数 Skip 返回错误；链式负数或累加溢出 panic。超出调用栈时 PC 为零，省略位置。派生 Logger 不修改原实例，复用 Handler 与资源状态。

通过 runtime.Callers 和 runtime.CallersFrames 正确处理内联帧，分别校准普通、格式化及 Log 方法的内部深度。调用位置在 Logger 内写入 Record.PC，Handler 不重新猜测调用方。

## Writer 注入与生命周期

传入外部 Writer 时，不创建日志目录、不启动轮转或清理，也不关闭外部 Writer。需要同时写文件和终端时，调用方可创建 FileWriter，使用 io.MultiWriter 组合后传入，并自行管理生命周期。

未传 Writer 时，New 创建默认 FileWriter，目录或文件不可写立即返回错误。Logger.Close 停止维护任务并关闭自有文件，重复调用安全；关闭后所有派生 Logger 的输出返回 os.ErrClosed。

FileWriter 可通过 NewFileWriter 独立创建，实现 io.WriteCloser，另提供 Rotate、Cleanup、Sync。Close 等待维护任务退出，释放文件，不额外归档。普通写入不承诺每条日志 fsync，调用方可显式 Sync。

## 文件配置与默认值

所有分割阈值、轮转周期、保留时长和清理周期均可传入。提供 DefaultFileOptions 返回完整默认值，零值按下表处理。

| 字段 | 类型 | 默认值 | 语义 |
| --- | --- | --- | --- |
| Path | string | ./logs | 日志目录，构造时解析为绝对路径并创建。 |
| File | string | app.log | 活动文件名，要求 .log 后缀，不允许路径分隔符。 |
| RotateSize | int64 | 100 × 1024 × 1024 | 字节，正数为分割阈值，0 使用默认值，-1 禁用大小分割。 |
| RotateInterval | time.Duration | 24h | 自本轮开始的最大写入时长，0 使用默认值，-1 禁用时间轮转。 |
| RotateBackupExpire | time.Duration | 24h | 归档保留时长，0 使用默认值，-1 禁用按时间清理。 |
| RotateBackupLimit | int | 0 | 正数限制归档数量，0 不按数量限制。 |
| RotateCheckInterval | time.Duration | 1m | 后台轮转与清理检查间隔，0 使用默认值。 |
| OnError | func(error) | 向 os.Stderr 输出诊断 | 接收后台轮转和清理错误。 |

除明确允许的 -1 哨兵值外，负数配置返回错误。大小使用字节数，时长使用 time.Duration，首版不实现单位字符串解析或配置文件读取。

默认保留 24h 指归档产生后保留 24 小时，并非逐条日志的存活时间。轮转周期与归档保留时间相互独立；较早写入的记录会额外经历活动文件阶段。过期清理存在最多约一个检查周期的调度延迟，不承诺到期瞬间删除。

## 分割与轮转

活动文件为 app.log，归档格式为 `app.<UTC轮转时间>.<唯一标识>.log`。时间精确到纳秒，唯一标识由标准库生成，发生名称冲突时重新生成，不覆盖归档。首版不压缩。

Write 在锁内检查非空活动文件：若写入后会超过 RotateSize，或距本轮开始达到 RotateInterval，先轮转再写入。一次 Write 不拆到两个文件；单条日志自身超过阈值时允许单文件暂时超限，下一条写入再轮转。零长度 Write 不触发轮转。

后台任务即使没有新日志也检查时间轮转，但不轮转空文件。轮转周期不因持续写入而重置。重启时根据已有活动文件修改时间保守恢复本轮起点，继续追加，不截断已有内容。

轮转持有 Writer 锁，关闭活动文件、改名归档、再以追加模式打开活动文件。关闭或改名失败时尝试恢复活动文件并返回错误；新文件打开失败时保留归档，后续 Write 可重试打开。按实际写入字节数更新大小，短写返回 io.ErrShortWrite，不自动重试整条记录。

Write、Rotate、Cleanup、后台维护和 Close 使用统一同步状态。首版不支持多个进程或多个独立 FileWriter 操作同一活动文件；不同进程使用不同目录或文件名。同一进程内可显式共享 FileWriter。

## 自动清理

初始化、轮转后及后台周期检查时执行清理。只扫描当前 Path 的直接子文件，精确匹配当前 File 的归档命名规则；跳过目录、符号链接、活动文件、不合法名称及其他日志前缀，不递归清理。

先按归档时间删除过期文件，再按 RotateBackupLimit 从旧到新删除多余归档。活动文件不计入数量。历史归档清理不依赖当前是否启用轮转。

清理失败不回滚成功的写入或轮转。后台错误交给 OnError，回调在文件锁外执行，不通过当前 logger 递归记录，也不允许在维护任务回调内调用 Close 等待自身退出。手动 Cleanup 直接返回错误。

进程停止期间不执行维护，下次初始化补做清理。Close 停止 ticker 并等待 goroutine 退出，避免资源泄漏。

## GoFrame 参考与差异

参考 glog 的 Writer 扩展点，以及按大小分割、归档数量、保留时长和检查周期的配置划分。GoFrame 的 RotateExpire 依据文件未修改时长；本模块使用 RotateInterval 表达固定轮转周期，避免同名不同义。

本模块 RotateBackupExpire 默认 24h，零值使用默认值；RotateBackupLimit 为 0 表示不按数量限制。这是 logx 自身定义，不承诺兼容 GoFrame 零值语义。首版不包含归档压缩、Cat、自动追踪接入或 GoFrame 配置系统。

参考资料：[GoFrame Rotate 特性](https://goframe.org/docs/core/glog-rotate)、[GoFrame Writer 接口](https://goframe.org/docs/core/glog-writer)、[Go slog](https://pkg.go.dev/log/slog)。

## 文件职责

```text
extension/logx/
├── logx.go          # Logger 入口、Record、派生实例和关闭
├── options.go       # Logger 配置与校验
├── handler.go       # slog.Handler 与固定输出格式
├── context.go       # ctx 提取与有序 JSON
├── caller.go        # 调用位置解析
├── file_writer.go   # 文件配置、写入与生命周期
└── rotate.go        # 轮转、归档识别与清理
```

上述实现文件已创建，另有模块 README 和经用户授权新增的模块测试。

## 验收标准

| 场景 | 预期 |
| --- | --- |
| traceId 与自定义字段齐全或部分缺失 | 日期后 JSON 有效，traceId 第一，其余按配置顺序；全缺失输出 {}。 |
| nil、typed nil、空字符串、0、false | 前三者省略，后两者保留 JSON 类型。 |
| 编码失败 | 输出标记并保留日志，返回编码错误。 |
| ctx 取消或为 nil | 取消仍输出，nil panic。 |
| 级别过滤 | 不进行字段提取、格式化或写入。 |
| 普通、格式化及 Log 方法的 Skip | 业务行号正确，覆盖内联与叠加 skip，越界省略位置。 |
| 外部 Writer | 不创建文件或维护任务，不关闭外部资源。 |
| 默认或自定义文件配置 | 默认保留 24h，传入阈值、周期、保留时间均生效。 |
| 大小分割、时间轮转及空闲维护 | 不拆分记录、不反复归档空文件。 |
| 重启、归档名冲突及超大单条日志 | 不截断旧数据、不覆盖归档、不拆分单条记录。 |
| 过期及数量超限 | 只删除当前日志归档，不影响活动文件和其他文件。 |
| 并发写入、轮转、清理和关闭 | 记录不交错，不访问已关闭句柄，派生实例共享关闭状态。 |
| 写入、后台维护或短写错误 | 返回错误或调用 OnError，不递归记录，不误报成功。 |
| 重复 Close | 资源已释放，调用安全。 |

用户已授权添加并运行模块测试。实现验证覆盖模块构建、静态检查及 race 测试；测试范围包括上下文顺序、调用位置、轮转与清理、后台错误和并发关闭。
