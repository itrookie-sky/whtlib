# 项目规则

## adapter 职责

- `adapter/` 用于定义多个组件实际共享的适配接口，例如 `Logger`。
- 仅供单个组件使用的接口和适配实现放在组件内部。

## toolkit 职责与命名规范

- `toolkit/` 用于独立、通用、可复用的工具和组件，保持框架无关，例如 cache、Redis、token、Kafka。
- `toolkit/` 下的工具包目录名和 Go 包名统一采用 `tk<功能名>` 格式，全部使用小写字母，例如目录 `toolkit/tkcache` 对应包名 `tkcache`。
- 类型、函数和方法遵循常规 Go 命名，不重复添加 `tk` 前缀，例如 `tkcache.Cache`、`tkcache.Config`、`tkcache.New(...)`。

## extension 职责与命名规范

- `extension/` 用于基于 Go 标准库提供功能扩展，例如 `jsonx` 扩展 `encoding/json`，`logx` 扩展 `log/slog`，`reflectx` 扩展 `reflect`。
- `extension/` 下的扩展包目录名和 Go 包名统一采用 `<功能名>x` 格式，全部使用小写字母，例如目录 `extension/jsonx`、`extension/logx` 分别对应包名 `jsonx`、`logx`。
- 类型、函数和方法遵循常规 Go 命名，不重复添加 `x` 后缀，例如 `logx.Logger`、`logx.Config`、`logx.New(...)`。

## platform 职责

- `platform/` 用于封装第三方平台 API，按平台和服务组织目录，例如 `platform/aliyun/oss`。
