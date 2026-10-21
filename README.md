# whtlib

A lightweight, framework-independent Go library providing reusable core components and utilities, built for simplicity, reliability, and long-term maintainability.

## 目录结构

```text
whtlib/
├── adapter/      # 供多个包复用的通用适配契约
│   └── logger.go # 与 logx 打印方法一致的 Logger 接口
├── toolkit/      # 独立、通用、框架无关的组件，包名使用 tk 前缀
│   ├── tkaes/    # AES 加解密与编码工具
│   ├── tkcache/  # 内存与 Redis 字节缓存、文本/JSON、回源合并
│   ├── tkredis/  # Redis 命令、Lua、集合及租约锁
│   ├── tktoken/  # 会话、旧 token 协议、标准 HTTP 中间件
│   └── tkkafka/  # Kafka 投递、消费、重试及分区提交
├── extension/    # Go 标准库功能扩展
│   ├── jsonx/    # encoding/json 扩展
│   ├── logx/     # slog 扩展及文件轮转
│   └── reflectx/ # reflect 扩展，合并 plreflect 与 lib/libreflect
└── platform/     # 第三方平台 API 封装
```

| 目录 | 职责 |
| --- | --- |
| `adapter` | 按需定义多个包共享的适配接口；仅组件自身使用的接口放在组件内部。 |
| `toolkit` | 封装可复用的通用功能和组件，包名使用 tk 前缀，例如 tkcache、tkredis。 |
| `extension` | 扩展 Go 官方标准库功能，例如 `jsonx` 提供关闭 HTML 转义的 JSON 编码函数。 |
| `platform` | 封装第三方平台 API，按平台和服务组织目录，例如 `aliyun/oss/`。该路径仅为规划示例，暂未创建。 |

## 依赖原则

优先使用 Go 标准库，尽量减少第三方依赖。对于 Redis 客户端等确有需要的功能，可以引入必要的第三方库。

## 日志组件

[logx 使用说明](extension/logx/README.md)：基于标准库 slog，支持 ctx 字段、Skip、SetConfig、全局单例、独立实例、私有全局文件管理和内置文件轮转。分割阈值与保留时间可配置，归档默认保留 24h。


## 通用日志接口

组件可选注入现有非泛型 `adapter.Logger`，包含 `Skip(n) Logger`、Debug/Info/Warn/Error 及四个格式化方法。普通参数按空格拼接正文；接口不包含 Log、SetConfig 或 Close。adapter 仅依赖标准库，日志配置与生命周期由调用方管理。

接入具体日志库时需满足完整方法签名。当前 `logx.Logger.Skip` 返回 `*logx.Logger`，需要调用方提供包装器转换为 `adapter.Logger` 的返回类型；Go 接口不支持返回类型协变。

## 迁移组件

- [tkcache](toolkit/tkcache/README.md)：内存和 Redis 后端，文本、JSON、TTL、原子比较与回源合并。
- [tkredis](toolkit/tkredis/README.md)：原生命令、显式 JSON、pipeline、Lua 重载、集合与持有者校验锁。
- [tktoken](toolkit/tktoken/README.md)：显式协议和存储注入、CAS 会话续期/撤销、标准 net/http 中间件及文件存储。
- [tkkafka](toolkit/tkkafka/README.md)：保留原配置类型与分区约定，显式投递结果、失败不提交及有界关闭等待。
- [reflectx](extension/reflectx/README.md)：类型和值分析、nil 判断、标签字段查找及转换赋值。

迁移组件不依赖 GF 或源 go-gf-lib。旧 Redis key 和会话文件不自动导入；具体协议边界见各包说明。Kafka 沿用 confluent 驱动，保留其 CGO 与平台构建要求。
