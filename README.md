# whtlib

A lightweight, framework-independent Go library providing reusable core components and utilities, built for simplicity, reliability, and long-term maintainability.

## 目录结构

```text
whtlib/
├── adapter/      # 供多个包复用的通用适配契约
│   └── logger.go # 与 logx 打印方法一致的 Logger 接口
├── lib/          # 通用功能封装
├── extension/    # Go 标准库功能扩展
│   ├── jsonx/    # encoding/json 扩展
│   └── logx/     # slog 扩展及文件轮转
└── platform/     # 第三方平台 API 封装
```

| 目录 | 职责 |
| --- | --- |
| `adapter` | 按需定义多个包共享的适配接口；仅组件自身使用的接口放在组件内部。 |
| `lib` | 封装可复用的通用功能和组件，例如 Redis 客户端封装。 |
| `extension` | 扩展 Go 官方标准库功能，例如 `jsonx` 提供关闭 HTML 转义的 JSON 编码函数。 |
| `platform` | 封装第三方平台 API，按平台和服务组织目录，例如 `aliyun/oss/`。该路径仅为规划示例，暂未创建。 |

## 依赖原则

优先使用 Go 标准库，尽量减少第三方依赖。对于 Redis 客户端等确有需要的功能，可以引入必要的第三方库。

## 日志组件

[logx 使用说明](extension/logx/README.md)：基于标准库 slog，支持 ctx 字段、Skip、SetConfig、全局单例、独立实例、私有全局文件管理和内置文件轮转。分割阈值与保留时间可配置，归档默认保留 24h。


## 通用日志接口

其他组件可依赖 `adapter.Logger`，由调用方注入 `*logx.Logger` 或 `logx.Default()`：

```go
var logger adapter.Logger = logx.Default()
logger.Info(ctx, "订单", 123, "完成")
logger.Infof(ctx, "订单 %d 完成", 123)
```

接口包含 Log、Debug、Info、Warn、Error 以及四个带 f 的格式化方法。普通参数按空格拼接正文，不按 slog 键值对解析。adapter 只依赖标准库，不导入 logx；logx 包包含编译期接口检查。SetConfig、Skip 和 Close 由创建实例的调用方管理，不属于通用打印接口。
