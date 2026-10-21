# 项目规则

## toolkit 命名规范

- `toolkit/` 下的工具包目录名和 Go 包名统一采用 `tk<功能名>` 格式，全部使用小写字母，例如目录 `toolkit/tkcache` 对应包名 `tkcache`。
- 类型、函数和方法遵循常规 Go 命名，不重复添加 `tk` 前缀，例如 `tkcache.Cache`、`tkcache.Config`、`tkcache.New(...)`。

## extension 职责与命名规范

- `extension/` 用于基于 Go 标准库提供功能扩展，例如 `jsonx` 扩展 `encoding/json`，`logx` 扩展 `log/slog`。
- `extension/` 下的扩展包目录名和 Go 包名统一采用 `<功能名>x` 格式，全部使用小写字母，例如目录 `extension/jsonx`、`extension/logx` 分别对应包名 `jsonx`、`logx`。
- 类型、函数和方法遵循常规 Go 命名，不重复添加 `x` 后缀，例如 `logx.Logger`、`logx.Config`、`logx.New(...)`。
