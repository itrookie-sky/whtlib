# logx 模块设计

`logx` 位于 `extension/logx`，基于标准库 `log/slog`，强制传入 `context.Context`。traceId 在级别后独立输出为 {值}，其余上下文字段组成有序 JSON；支持源码位置回溯、私有全局文件管理，以及内置文件 Writer 的分割、轮转和清理。全部使用标准库，不引入 GoFrame 依赖。

已确定：使用 slog、ctx 必传、traceId 首位、有序自定义字段、Skip(n)、封闭 Writer、同路径共享文件资源、分割阈值和保留时长可配置、默认保留 24h。本设计已在 extension/logx 实现；具体调用示例见该包 README。

## 组件职责

```text
业务调用 Logger（ctx 必传）
    → slog.Record（时间、级别、正文、业务属性、调用位置）
    → 自定义 slog.Handler（上下文提取与日志格式化）
    → 私有 terminalWriter（默认终端）或全局 fileManager → 私有 fileWriter（按文件路径复用）
```

Logger 负责入口、级别判断和调用位置；Handler 负责格式与同步输出；私有 fileWriter 负责文件生命周期，不解析 ctx 或正文。不修改 slog 全局实例，不保存请求 ctx。首版不包含异步日志队列、进程退出接口或自动链路追踪集成。

## 输出格式

```text
日期时间 [级别] {traceId值} [其他上下文 JSON] 文件:行号 正文 [业务属性 JSON]
```

末尾业务属性 JSON 仅在传入结构化属性时输出，方括号表示可选部分，不是实际字符。

```text
2026-10-13 14:30:00 +0800 CST [INFO] {abc123} {"userId":1001} order/service.go:42 创建订单成功
2026-10-13 14:30:01.123 +0800 CST [INFO] {"userId":1001} order/service.go:42 创建订单成功 {"orderId":"o123"}
2026-10-13 14:30:02.456 +0800 CST [ERROR] order/service.go:42 创建订单失败
```

日期使用本地时区的 Time.String() 格式，小数位按实际精度输出。级别紧随日期；traceId 有值时以 {值} 独立输出，缺失、nil 或空字符串时省略。其他上下文字段按配置顺序输出 JSON，无有效字段时省略。可选区块省略时不保留分隔空格。

键和值使用 encoding/json 编码并保留 JSON 类型，正文换行与回车转义，每条记录以一个真实换行结束。整条日志为文本与 JSON 块的混合格式，不是完整 JSON 文档。

JSON 对象语义上无顺序，但本模块保证顶层 context 成员的输出顺序。使用有序切片逐项编码键和值，不把全部字段放入 map；嵌套对象不额外保证业务顺序。

## Logger 配置和接口

New 接收 Config，返回 Logger 和初始化错误。构造时复制字段切片并校验配置，每版配置保持只读，通过 SetConfig 同步切换；级别读取为快照，通过 SetConfig 调整。

| 配置项 | 默认值 | 含义 |
| --- | --- | --- |
| WriteFile | false | bool；false 仅终端，true 同时输出终端和文件；SetConfig 始终应用此值，未填写也视为 false。 |
| File | 默认文件配置 | FileConfig，设置路径、文件名和轮转策略。 |
| Level | slog.LevelInfo | 接收 slog.Leveler，过滤低于阈值的日志。 |
| TraceKey | 字符串 traceId | 从 ctx 读取 traceId 的 key，输出名称固定为 traceId。 |
| ContextFields | 空切片 | 每项包含输出名称 Name 与读取键 Key，按切片顺序输出。 |
| Skip | 0 | 在业务调用位置基础上额外跳过的层数。 |
| FullPath | false | 默认显示最后一级目录及文件名；true 输出完整路径。 |

context key 支持可比较的自定义类型。TraceKey 未设置时使用默认值；字段 Key 不允许 nil；不可比较或 typed nil 的 key 返回错误。字段名限定 ASCII 字母、下划线或连字符（-）开头，后续为字母、数字、下划线或连字符，拒绝重复名称及保留名称 traceId。

| 方法 | 行为 |
| --- | --- |
| Debug、Info、Warn、Error | 接收 ctx、消息及可变正文参数，无返回值。 |
| Log | 接收 ctx、slog.Level、消息及可变正文参数，无返回值。 |
| Debugf、Infof、Warnf、Errorf | 接收 ctx、格式串及参数，以 fmt.Sprintf 生成正文，无返回值。 |
| SetConfig | 合并非零字段，更新当前实例并返回原指针，失败打印堆栈。 |
| Default（包级） | 返回延迟初始化的全局 Logger，包级打印函数代理此实例。 |
| Skip(n) | 返回额外跳过 n 层调用的派生 Logger。 |
| Close | 关闭当前实例，释放文件引用，最后一个引用负责停止维护并关闭文件。 |

普通方法将消息与可变参数以空格拼接为正文，空消息不产生前导空格；不按 slog 属性解析。格式化文本使用带 f 方法。LogAcc 单独按键值对累积业务属性，在行尾输出 JSON，不能覆盖级别后的 context JSON。

所有输出方法要求非 nil ctx，nil 直接 panic；无请求上下文时显式传 context.Background()。ctx 取消不阻止日志输出。Writer.Write 和资源关闭接口不要求 ctx。

logx 先调用 Handler.Enabled，过滤后的日志不提取字段、不解析栈、不格式化正文。使用 slog.NewRecord 构造 Record，再调用 Handler.Handle。公开打印方法和内部 output 均无返回值。output 遇到编码、写入或关闭状态错误时，直接向 os.Stderr 输出错误及 runtime/debug.Stack 提供的当前调用堆栈，不经故障 Writer 或 logger 递归记录；构造和 Close 仍保留错误返回。

Handler 完整实现 Enabled、Handle、WithAttrs、WithGroup，遵循值解析、空属性、空组和嵌套分组约定。派生 Handler 共享输出锁，属性配置只读。Record 时间或 PC 为零时，分别省略时间或源码位置。

## 动态配置和全局单例

Config 替代原 Options，FileConfig 替代 FileOptions，默认配置工具相应改为 BuildDefaultConfig 与 DefaultFileConfig。SetConfig 的 WriteFile 始终应用（默认 false），其他字段的 nil、空字符串、0、false 表示沿用旧值；File 逐字段合并，非 nil 的 ContextFields 整体替换，空切片清空。零值重置需要创建新实例。

Logger 和 Skip 派生实例各自持有独立 loggerCore，配置切片复制、Level 读取为快照。SetConfig 和 Close 只作用于当前实例。文件通过私有全局 fileManager 按规范化绝对路径共享，目录符号链接解析到真实路径；硬链接别名不支持。相同文件的轮转策略必须一致，多实例持有时拒绝策略变更；只有一个引用时允许重新配置。路径切换先获取新资源，成功后释放旧引用，失败保留原配置。每个 Skip 实例必须独立 Close，最后一个引用释放后关闭文件。

Default 返回包内固定单例，导入包不触发文件系统操作。第一次打印或设置配置才初始化；初始化失败输出堆栈，后续调用允许重试。包级 SetConfig、Close、Skip 以及全部打印方法代理同一单例，不修改 slog 全局状态。包级打印入口直接进入 output，保持 Skip 和源码位置语义。关闭单例不会自动重建。

输出持有配置读锁；字段格式化函数不得重入当前 Logger 的 SetConfig/Close，OnError 回调不得重入 logx API，避免锁重入或等待维护任务自身退出。

## 上下文字段

每次先读取 TraceKey，再读取 ContextFields，不遍历或猜测其他 key，不生成 traceId、不回写 ctx。业务可将外部追踪系统的 traceId 放入约定 key。

nil、typed nil 和空字符串省略；0 与 false 保留，其余值按标准 JSON 规则编码。调用方不得在输出期间并发修改字段引用的对象。

某字段编码失败时，替换成 JSON 字符串 `"[json encode error]"`，继续输出；Handler 内部保留带字段名的错误，同时写入失败时通过 errors.Join 合并，output 将合并错误与当前调用堆栈一次性打印到 stderr。用户自定义 MarshalJSON 内部的 panic 不主动恢复。

## 调用位置与 Skip

默认配置下 Skip(0) 指向业务直接调用行，内部封装层自动跳过。初始化 Skip 与链式 Skip(n) 累加，Skip(0) 不重置已有数值。封装函数使用 Skip(1) 定位其上层调用者。

初始化负数 Skip 返回错误；链式负数或累加溢出 panic。超出调用栈时 PC 为零，省略位置。派生 Logger 不修改原实例，复制配置并独立持有文件引用。

通过 runtime.Callers 和 runtime.CallersFrames 正确处理内联帧，分别校准普通、格式化及 Log 方法的内部深度。调用位置在 Logger 内写入 Record.PC，Handler 不重新猜测调用方。

## 私有全局文件管理与生命周期

公开 API 移除 Config.Writer、FileWriter、NewFileWriter。文件路径和名称通过 FileConfig 设置。所有实例共用一个 fileManager，同文件只持有一个 fileWriter；不同文件独立维护。

文件模式的 Logger 持有一个 fileLease；终端模式共用私有 terminalWriter，串行写入 os.Stdout，不创建文件资源。Skip 保留文件引用并复制配置，配置与关闭状态不共享。Close 仅释放当前引用，最后一个引用负责停止维护并关闭文件；关闭期间新的同路径申请等待释放完成，避免重复管理。全局单例也遵循相同规则。

文件策略冲突时 New 返回错误，SetConfig 输出错误堆栈并保留旧配置。OnError 回调按实例登记，维护错误向当前登记的回调分别通知，更新回调不会影响其他实例；执行前取得快照，快照中的旧回调可能仍会执行。普通写入不逐条 fsync，暂不公开 Sync。

## 文件配置与默认值

所有分割阈值、轮转周期、保留时长和清理周期均可传入。提供 DefaultFileConfig 返回完整默认值，零值按下表处理。

| 字段 | 类型 | 默认值 | 语义 |
| --- | --- | --- | --- |
| Path | string | ./logs | 日志目录，构造时解析为绝对路径并创建。 |
| File | string | {y-m-d}.log | 活动文件名表达式，要求 .log 后缀，不允许路径分隔符。 |
| RotateSize | int64 | 100 × 1024 × 1024 | 字节，正数为分割阈值，0 使用默认值，-1 禁用大小分割。 |
| RotateInterval | time.Duration | 24h | 自本轮开始的最大写入时长，0 使用默认值，-1 禁用时间轮转。 |
| RotateBackupExpire | time.Duration | 24h | 归档保留时长，0 使用默认值，-1 禁用按时间清理。 |
| RotateBackupLimit | int | 0 | 正数限制归档数量，0 不按数量限制。 |
| RotateCheckInterval | time.Duration | 1m | 后台轮转与清理检查间隔，0 使用默认值。 |
| OnError | func(error) | 向 os.Stderr 输出诊断 | 接收后台轮转和清理错误。 |

除明确允许的 -1 哨兵值外，负数配置返回错误。大小使用字节数，时长使用 time.Duration，首版不实现单位字符串解析或配置文件读取。

默认保留 24h 指归档产生后保留 24 小时，并非逐条日志的存活时间。轮转周期与归档保留时间相互独立；较早写入的记录会额外经历活动文件阶段。过期清理存在最多约一个检查周期的调度延迟，不承诺到期瞬间删除。

## 分割与轮转

例如显式设置活动文件为 app.log 时，归档格式为 `app.<UTC轮转时间>.<唯一标识>.log`。时间精确到纳秒，唯一标识由标准库生成，发生名称冲突时重新生成，不覆盖归档。首版不压缩。

Write 在锁内检查非空活动文件：若写入后会超过 RotateSize，或距本轮开始达到 RotateInterval，先轮转再写入。一次 Write 不拆到两个文件；单条日志自身超过阈值时允许单文件暂时超限，下一条写入再轮转。零长度 Write 不触发轮转。

后台任务即使没有新日志也检查时间轮转，但不轮转空文件。轮转周期不因持续写入而重置。重启时根据已有活动文件修改时间保守恢复本轮起点，继续追加，不截断已有内容。

轮转持有 Writer 锁，关闭活动文件、改名归档、再以追加模式打开活动文件。关闭或改名失败时尝试恢复活动文件并返回错误；新文件打开失败时保留归档，后续 Write 可重试打开。按实际写入字节数更新大小，短写返回 io.ErrShortWrite，不自动重试整条记录。

Write、Rotate、Cleanup、后台维护和 Close 使用统一同步状态。同一进程由 fileManager 统一共享文件；不支持多个进程操作同一活动文件，不同进程使用不同目录或文件名。

## 自动清理

初始化、轮转后及后台周期检查时执行清理。只扫描当前 Path 的直接子文件，精确匹配当前 File 的归档命名规则；跳过目录、符号链接、活动文件、不合法名称及其他日志前缀，不递归清理。

先按归档时间删除过期文件，再按 RotateBackupLimit 从旧到新删除多余归档。活动文件不计入数量。历史归档清理不依赖当前是否启用轮转。

清理失败不回滚成功的写入或轮转。后台错误交给 OnError，回调在文件锁外执行，不通过当前 logger 递归记录，也不允许在维护任务回调内调用 Close 或 SetConfig 等待自身退出。手动 Cleanup 直接返回错误。

进程停止期间不执行维护，下次初始化补做清理。Close 停止 ticker 并等待 goroutine 退出，避免资源泄漏。

## GoFrame 参考与差异

参考 glog 的 Writer 扩展点，以及按大小分割、归档数量、保留时长和检查周期的配置划分。GoFrame 的 RotateExpire 依据文件未修改时长；本模块使用 RotateInterval 表达固定轮转周期，避免同名不同义。

本模块 RotateBackupExpire 默认 24h，零值使用默认值；RotateBackupLimit 为 0 表示不按数量限制。这是 logx 自身定义，不承诺兼容 GoFrame 零值语义。首版不包含归档压缩、Cat、自动追踪接入或 GoFrame 配置系统。

参考资料：[GoFrame Rotate 特性](https://goframe.org/docs/core/glog-rotate)、[GoFrame Writer 接口](https://goframe.org/docs/core/glog-writer)、[Go slog](https://pkg.go.dev/log/slog)。

## 文件职责

```text
extension/logx/
├── logx.go          # Logger 入口、Record、派生实例和关闭
├── config.go        # Logger 配置、合并与校验
├── instance.go      # 实例私有状态、配置切换与资源释放
├── writer_manager.go # 全局文件资源注册、引用计数及策略冲突检查
├── default.go       # 全局单例和包级打印入口
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
| traceId 与自定义字段齐全或部分缺失 | 级别后 traceId 独立占位，缺失时省略；其余按配置顺序输出 JSON，全缺失时省略。 |
| nil、typed nil、空字符串、0、false | 前三者省略，后两者保留 JSON 类型。 |
| 编码失败 | 输出标记并保留其他日志内容，打印方法不返回错误。 |
| ctx 取消或为 nil | 取消仍输出，nil panic。 |
| 级别过滤 | 不进行字段提取、格式化或写入。 |
| 普通、格式化及 Log 方法的 Skip | 业务行号正确，覆盖内联与叠加 skip，越界省略位置。 |
| 私有 Writer | 无注入和直接访问入口，同路径共享唯一写入资源。 |
| 默认或自定义文件配置 | 默认保留 24h，传入阈值、周期、保留时间均生效。 |
| 大小分割、时间轮转及空闲维护 | 不拆分记录、不反复归档空文件。 |
| 重启、归档名冲突及超大单条日志 | 不截断旧数据、不覆盖归档、不拆分单条记录。 |
| 过期及数量超限 | 只删除当前日志归档，不影响活动文件和其他文件。 |
| 并发写入、轮转、清理和关闭 | 记录不交错，不访问已关闭句柄，派生实例独立关闭。 |
| 写入、后台维护或短写错误 | output 将同步错误及堆栈写入 stderr；后台错误调用 OnError，不递归记录。 |
| 重复 Close | 资源已释放，调用安全。 |
| SetConfig | 返回原实例，未填字段保留，失败保留旧配置，派生实例同步生效。 |
| 同路径修改轮转参数 | 独占文件时复用并更新；共享文件时拒绝策略冲突。 |
| 全局单例和包级打印 | 延迟初始化、相同实例、业务调用行号正确。 |
| SetConfig 与打印和 Close 并发 | 无数据竞争、无混合配置、无重复维护资源。 |

用户已授权添加并运行模块测试。实现验证覆盖模块构建、静态检查及 race 测试；测试范围包括上下文顺序、调用位置、轮转与清理、后台错误和并发关闭。

## 文件名表达式补充

File 默认 `{y-m-d}.log`，仅在花括号内展开 y/m/d，分别为四位年、两位月和两位日。支持 `sql-{y-m-d}.log`、`ymd-{ymd}.log`；括号外内容不解析，括号内只允许这些占位符及 `-_.` 分隔符。配置校验拒绝空、嵌套、未闭合表达式和其他占位符。

fileWriter 保留原表达式，单独记录 activeName，写入及维护时按本地日期重新展开，先打开新文件再释放旧句柄。大小轮转基于 activeName，历史归档跨日期识别。历史日期文件按 mtime 清理，归档按名称时间清理，合计应用数量上限，保护活动文件与当前日期文件。

fileManager 以规范化目录和原表达式登记，同表达式共享资源，可能输出同名文件的不同表达式保守拒绝同时启用。
