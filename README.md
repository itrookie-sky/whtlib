# whtlib

A lightweight, framework-independent Go library providing reusable core components and utilities, built for simplicity, reliability, and long-term maintainability.

## 目录结构

```text
whtlib/
├── lib/          # 通用功能封装
├── extension/    # Go 标准库功能扩展
│   ├── jsonx/    # encoding/json 扩展
│   └── logx/     # slog 扩展及文件轮转
└── platform/     # 第三方平台 API 封装
```

| 目录 | 职责 |
| --- | --- |
| `lib` | 封装可复用的通用功能和组件，例如 Redis 客户端封装。 |
| `extension` | 扩展 Go 官方标准库功能，例如 `jsonx` 提供关闭 HTML 转义的 JSON 编码函数。 |
| `platform` | 封装第三方平台 API，按平台和服务组织目录，例如 `aliyun/oss/`。该路径仅为规划示例，暂未创建。 |

## 依赖原则

优先使用 Go 标准库，尽量减少第三方依赖。对于 Redis 客户端等确有需要的功能，可以引入必要的第三方库。

## 日志组件

[logx 使用说明](extension/logx/README.md)：基于标准库 slog，支持 ctx 字段、Skip、SetConfig、全局单例、自定义 Writer 和内置文件轮转。分割阈值与保留时间可配置，归档默认保留 24h。
