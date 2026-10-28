# whtlib

A lightweight, framework-independent Go library providing reusable core components and utilities, built for simplicity, reliability, and long-term maintainability.

最低 Go 版本：**1.27.3**。

## 目录结构

```text
whtlib/
├── adapter/      # 供多个包复用的通用适配契约
│   ├── logger.go # 与 logx 打印方法一致的 Logger 接口
│   └── log_acc.go # 累计字段、计时与结构化打印的 LogAcc 接口
├── toolkit/      # 独立、通用、框架无关的组件，包名使用 tk 前缀
│   ├── tkaes/    # AES 加解密与编码工具
│   ├── tkcache/  # 内存、Redis 与文件字节缓存、文本/JSON、回源合并
│   ├── tkhttp/   # 单次 HTTP 调用、标准库连接池、签名钩子与累计日志
│   ├── tkredis/  # Redis 命令、Lua、集合及租约锁
│   ├── tksign/   # 多格式公私钥解析、RSA/ECDSA/Ed25519 签名验签
│   ├── tktoken/  # 会话、旧 token 协议、标准 HTTP 中间件
│   ├── tkua/     # User-Agent 结构化解析与系统、终端、浏览器等枚举
│   └── tkkafka/  # Kafka 投递、消费、重试及分区提交
├── extension/    # Go 标准库功能扩展
│   ├── errx/     # 错误堆栈、字段、错误树、业务码与诊断快照
│   ├── filepathx/ # 本地路径查找、目录范围判断与文件名处理
│   ├── jsonx/    # encoding/json 扩展
│   ├── logx/     # slog 扩展及文件轮转
│   ├── pathx/    # 斜杠路径的分段前缀判断与裁剪
│   └── reflectx/ # reflect 扩展，合并 plreflect 与 lib/libreflect
└── platform/     # 第三方平台 API 封装
```


| 目录        | 职责                                                                                         |
| ------------- | ---------------------------------------------------------------------------------------------- |
| `adapter`   | 按需定义多个包共享的适配接口；仅组件自身使用的接口放在组件内部。                             |
| `toolkit`   | 封装可复用的通用功能和组件，包名使用 tk 前缀，例如 tkcache、tkredis。                        |
| `extension` | 扩展 Go 官方标准库功能，例如 `jsonx` 提供关闭 HTML 转义的 JSON 编码函数。                    |
| `platform`  | 封装第三方平台 API，按平台和服务组织目录，例如 `aliyun/oss/`。该路径仅为规划示例，暂未创建。 |

## 依赖原则

优先使用 Go 标准库，尽量减少第三方依赖。对于 Redis 客户端等确有需要的功能，可以引入必要的第三方库。

## 日志组件

[logx 使用说明](extension/logx/README.md)：基于标准库 slog，支持 ctx 字段、Skip、Clone 配置快照、SetConfig、全局单例、独立实例、私有全局文件管理和内置文件轮转。分割阈值与保留时间可配置，归档默认保留 24h。

## 通用日志接口

组件可选注入现有非泛型 `adapter.Logger`，包含 `Skip(n) Logger`、Debug/Info/Warn/Error 及四个格式化方法。普通参数按空格拼接正文；接口不包含 Log、SetConfig 或 Close。adapter 仅依赖标准库，日志配置与生命周期由调用方管理。

接入具体日志库时需满足完整方法签名。`logx.NewLoggerAdapter(logger)` 可将现有 `*logx.Logger` 转为 `adapter.Logger`；其他日志库可提供薄适配器，参见 [GoFrame 接入示例](examples/logacc_goframe/README.md)。Skip 返回借用视图，配置与关闭由后端创建方负责。

累计结构化字段使用独立的 `adapter.LogAcc`，包含 SetStartTime、字段更新和四种打印方法。`logx.NewLogAcc(...)` 创建的实例直接实现该接口，`logx.WithLogger(backend)` 注入 `adapter.Logger`，可使用 GoFrame 等后端。tkhttp 通过 `LogAccFactory` 在每次调用创建新累加器，LogAcc 不提供 Clone。

## HTTP 客户端

[tkhttp 使用说明](toolkit/tkhttp/README.md)：Config 管理基本参数，With 选项注入标准库连接池、客户端、调用层中间件、累计日志和签名钩子；中间件可观察参数校验、响应读取和解码的最终错误，通过 Response.StatusCode 获取状态码。请求配置可省略，支持 JSON/表单/字节正文、响应大小限制与可选解码。每次调用最多执行一次 `http.Client.Do` 并打印一条汇总日志，不提供重试能力。

## 签名验签

[tksign 使用说明](toolkit/tksign/README.md)：通过 `WithPrivateKey`、`WithPublicKey` 按需配置密钥，支持 PEM、Base64 和 DER，统一提供 RSA、RSA-PSS、ECDSA 和 Ed25519 签名验签。字符串入口默认使用 Base64，也可配置 Base64URL 或 Hex；仅传私钥时自动导出验签公钥。

## 路径扩展

- [filepathx](extension/filepathx/README.md)：向上查找标记目录、目录内拼接与相对路径、用户目录展开、文件名及扩展名处理。目录范围检查是词法判断，不解析符号链接。
- [pathx](extension/pathx/README.md)：基于标准库 path，按完整分段判断和裁剪路径前缀，避免 `/api/v1` 误匹配 `/api/v10`。

## 错误扩展

[errx 使用说明](extension/errx/README.md)：在标准错误链上附加堆栈和结构化字段，遍历多分支错误树，提取业务码并生成支持 slog、LogAcc 和 JSON 的诊断快照，兼容 errors.Is/AsType。

## User-Agent

[tkua 使用说明](toolkit/tkua/README.md)：复用 uap-go 规则，解析系统、平台、终端、浏览器、引擎、宿主应用、CPU 架构及机器人类别；提供默认 Parse 与可并发复用的 Parser，枚举集中在 consts.go。

## 迁移组件

- [tkcache](toolkit/tkcache/README.md)：内存、Redis 和文件 Store，文本、JSON、TTL、原子比较与回源合并。
- [tkredis](toolkit/tkredis/README.md)：原生命令、显式 JSON、pipeline、Lua 重载、集合与持有者校验锁。
- [tktoken](toolkit/tktoken/README.md)：显式协议和存储注入、CAS 会话续期/撤销、标准 net/http 中间件，复用 tkcache 存储。
- [tkkafka](toolkit/tkkafka/README.md)：保留原配置类型与分区约定，显式投递结果、失败不提交及有界关闭等待。
- [reflectx](extension/reflectx/README.md)：类型和值分析、nil 判断、标签字段查找及转换赋值。

迁移组件不依赖 GF 或源 go-gf-lib。旧 Redis key 和会话文件不自动导入；具体协议边界见各包说明。Kafka 沿用 confluent 驱动，保留其 CGO 与平台构建要求。
