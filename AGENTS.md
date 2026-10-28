# 项目规则

## 文件组织

- 按职责组织文件，同一功能的类型、构造函数、方法和私有助手集中放在对应主文件中，不按开发批次或单个方法拆成多个文件。
- 不为一两个相关函数新增文件，不使用 `feat_1.go`、`feat_n.go` 等缺乏职责含义的命名；新增前先检查是否适合放入已有文件。
- 职责独立的模块、不同存储实现、平台或构建约束文件可以分别维护，不机械地把整个包合成一个大文件。
- 错误定义及其相关实现集中在 `error.go`，包内常量集中在 `consts.go`；测试按对应职责归并，不因新增一个方法就新增测试文件。

## Git 工作流

- 直接在 `main` 分支开发，不创建功能分支或临时开发分支。
- 不使用 Git worktree，所有开发在当前仓库目录完成。
- 本节约定覆盖全局规则与技能中关于开发分支和 worktree 的默认要求。

## 错误处理

- 对具有稳定判断或处理价值的错误，优先使用当前包在 `error.go` 中集中声明的哨兵错误或自定义错误类型；新增前先检查并复用已有定义，避免在业务代码中零散构造同义错误。
- 包级错误、自定义错误类型及其 `Error`、`Unwrap`、`Is` 方法和构造、包装助手集中维护在 `error.go`。
- 错误应优先支持通过 `errors.Is` 判断类别、通过 `errors.AsType` 获取具体类型和字段，避免依赖错误文本匹配。
- 附加错误上下文时使用 `%w` 或 `errors.Join`，自定义包装类型实现 `Unwrap`，需要类别匹配时实现 `Is`，保留原始错误链。
- 已有明确语义的标准库错误直接复用，例如 `context.Canceled`、`context.DeadlineExceeded`，不重复声明同义错误。
- 一次性的参数校验细节作为已有错误类别的上下文说明，不为每条报错文案新增导出错误变量。

## adapter 职责

- `adapter/` 用于定义多个组件实际共享的适配接口，例如 `Logger`。
- 仅供单个组件使用的接口和适配实现放在组件内部。

## toolkit 职责与命名规范

- `toolkit/` 用于独立、通用、可复用的工具和组件，保持框架无关，例如 cache、Redis、token、Kafka。
- `toolkit/` 下的工具包目录名和 Go 包名统一采用 `tk<功能名>` 格式，全部使用小写字母，例如目录 `toolkit/tkcache` 对应包名 `tkcache`。
- 类型、函数和方法遵循常规 Go 命名，不重复添加 `tk` 前缀，例如 `tkcache.Cache`、`tkcache.Config`、`tkcache.New(...)`。

- 缓存存储统一使用 `Store` 命名，例如 `Config.Store`、`MemoryStore`、`RedisStore`、`FileStore`；通用存储实现集中在 `tkcache`，消费组件通过接口复用。

## extension 职责与命名规范

- `extension/` 用于基于 Go 标准库提供功能扩展，例如 `jsonx` 扩展 `encoding/json`，`logx` 扩展 `log/slog`，`reflectx` 扩展 `reflect`。
- `extension/` 下的扩展包目录名和 Go 包名统一采用 `<功能名>x` 格式，全部使用小写字母，例如目录 `extension/jsonx`、`extension/logx` 分别对应包名 `jsonx`、`logx`。
- 类型、函数和方法遵循常规 Go 命名，不重复添加 `x` 后缀，例如 `logx.Logger`、`logx.Config`、`logx.New(...)`。
- `errx` 是基础错误包，生产代码仅依赖标准库，测试也不引用 logx、toolkit 等其他组件；跨组件联动测试和示例放在消费方，避免反向依赖。

## platform 职责

- `platform/` 用于封装第三方平台 API，按平台和服务组织目录，例如 `platform/aliyun/oss`。
