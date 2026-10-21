# 通用模块迁移实施计划

目标：实现已确认的 reflectx、tkredis、tkcache、tktoken、tkkafka，移除 GF 与源 go-gf-lib 依赖。

设计依据：[整体迁移设计](../specs/2026-10-21-toolkit-migration-design.md)、[缓存设计](../specs/2026-10-21-tkcache-design.md)、[反射扩展设计](../specs/2026-10-21-reflectx-design.md)。

## 执行约束

- 用户已确认在当前 main 工作区实现，完成后提交并 push。
- Go 1.26.3；中文注释，命名返回值，错误判断分行；保留迁移配置的已有类型及单位。
- 遵循全局 workflow：禁用 TDD；已获准新增不依赖真实 MySQL、Redis、Kafka 的模块测试。
- Redis、token、Kafka 按独立文件边界分工；根 go.mod、go.sum 和集成文档仅由主执行者维护。
- 测试仅限此次新增模块，Redis 使用内存模拟服务；Kafka 验证配置、分区、编码、提交进度和重试，不连接真实 broker。
- 不自动执行代码审核；用户另行同意后才进行正式审核。
- 实施中不迁移源项目和业务服务的调用，不导入 GF，不复制源固定默认密钥。

## 共享接口

| 提供者 | 消费者 | 约定 |
| --- | --- | --- |
| reflectx | tkredis、tkcache | 保留 OriginTypeAndKind、IsNil 及两个输出结构体字段类型。 |
| tkredis | tkcache | `New(*redis.Options) (*Client,error)`、`Wrap(redis.UniversalClient) (*Client,error)`、`Raw() redis.UniversalClient`；借用客户端不关闭。 |
| tkcache | tktoken | Get 返回字节、found、error；Set、SetIfAbsent、CompareAndSwap、CompareAndDelete 支持 ctx 和 TTL。token 内定义消费方最小 Store 接口。 |
| adapter | tkredis、tkkafka | 可选 adapter.Logger，采用现有非泛型接口，不绑定 logx。 |

## Task 1：reflectx

- [x] 实现 extension/reflectx/reflect.go，保留四个函数、两个结果类型，修正无效反射值处理；按用户补充合并 lib/libreflect 的两个字段操作函数。
- [x] 新增模块测试与 README；运行模块测试及 race 检查。

## Task 2：tkredis

- [x] 实现 toolkit/tkredis 的客户端所有权、原生值命令、pipeline、脚本重载、集合与有序集合，以及持有者校验锁。
- [x] 保留 redis.Options 配置类型，复杂参数显式编码，独立结果类型代替 gvar。
- [x] 编写不依赖真实 Redis 的测试及 README，执行构建、vet 与模块测试。

## Task 3：tkcache

- [x] 实现 toolkit/tkcache 的内存、Redis 字节后端与 Namespace、毫秒 TTL、批量操作、原子比较、Once。
- [x] 实现文本和泛型 JSON 辅助函数、同实例回源合并、取消及旧回源写回失效。
- [x] 编写内存和 miniredis 的行为测试、并发测试和 README；执行模块 race 测试与 vet。

## Task 4：tktoken

- [x] 基于 Store 实现生成、校验、原子刷新、比较撤销及 MultiLogin 原有行为。
- [x] 显式 LegacyCodec，标准库 CBC/PKCS7/Base64，配置密钥必填；不复用 ECB 工具。
- [x] 标准 net/http 中间件与独立 token 文件存储，不从 ctx 反查框架 request。
- [x] 验证内存存储、旧格式编解码、无效旧 token 不能续期或撤销新会话、HTTP 和文件到期；补充 tkcache 内存/Redis 两种后端联调。

## Task 5：tkkafka

- [x] 保留配置字段类型、环境 topic/group、FNV-1a 分区和 producer/consumer 能力。
- [x] 显式消息编码及投递结果、可取消重试、逐 partition 提交连续成功进度，失败不提交。
- [x] 有界关闭等待，响应取消的后台任务正常结束；无法强停不响应 ctx 的业务回调。使用 fake 驱动测试，不连接真实 broker。

## Task 6：集成、文档与交付

- [x] 固定必要依赖，gofmt、模块 go test -race、go vet、构建及 GF 依赖检查。
- [x] 同步设计实施状态和 README，记录不承诺的旧 key/文件自动导入与协议边界。

交付步骤：检查完整变更后创建一个中文提交，普通快进 push 到 origin/main，核对远端 SHA 和干净工作区；结果以 Git 状态和最终回报为准。

## 执行记录

- 预检：工作区干净，源仓库基线 1d80d96，当前工具链 Go 1.26.3，CGO 已开启。
- 用户已授权新增无需真实基础设施的模块单元测试。
- 决策：旧 token 兼容由显式 LegacyCodec 和配置密钥提供，不自动扫描或重写旧 Redis key、旧会话文件。
- 决策：依赖保留源模块的客户端家族，首轮以源已使用版本建立兼容基线，升级与协议切换不混入迁移。
- 实现：plreflect 与用户新增的 lib/libreflect 合并到 reflectx，共六个函数、两个结果类型及可识别字段赋值错误。
- 测试同步修正：Redis 连接池会提前读取 ctx.Done，测试使用后端上下文包装隔离该信号，确保取消发生在等待者加入共享回源后。
- 回源边界修正：共享空结果沿用首个调用者选项；交付结果前检查共享 ctx，避免取消回调调度延迟使本轮错误被忽略。
- 验证范围：内存/miniredis、fake Kafka、HTTP 测试服务及临时文件；无真实基础设施连接。未执行全仓测试或正式代码审核。
- 通过：`go test -race ./extension/reflectx ./toolkit/tkredis ./toolkit/tkcache ./toolkit/tktoken ./toolkit/tkkafka`；相同五包的 `go vet`、`go build`。
- 依赖：`go mod tidy` 已完成；实际包依赖与模块清单均无 GF/go-gf-lib，reflectx 与 tktoken 的生产依赖仅标准库。源仓库保持 1d80d96 且工作区干净。
