# reflectx 标准库扩展设计

将 `go-gf-lib/plugins/plreflect` 与 `go-gf-lib/lib/libreflect` 合并迁入 `whtlib/extension/reflectx`，作为 Go 标准库 `reflect` 的扩展，提供指针解引用、原始类型与值分析、安全取值、nil 判断及字段操作。

状态：已实现，已通过模块测试和 race 检查。模块来源、目标路径和标准库扩展定位已确认。属于 [go-gf-lib 通用模块迁移设计](./2026-10-21-toolkit-migration-design.md) 的一部分。

## 来源与定位

核对源码为本地 `go-gf-lib` 提交 `1d80d96` 的 `plugins/plreflect/reflect.go` 与 `lib/libreflect/libreflect.go`。前者仅导入标准库 reflect，后者的 GF 错误类型已替换为标准库 errors、fmt。迁移重点是路径和包名、现有 API 保留、边界行为及调用方复用。

| 项目 | 约定 |
| --- | --- |
| 目标目录 | `extension/reflectx`。 |
| Go 包名 | `reflectx`，使用标准库扩展包的 x 后缀。 |
| 职责 | 对标准库 reflect 提供小范围、可复用的扩展。 |
| 依赖 | 仅 Go 标准库，不依赖 GF、源 go-gf-lib、toolkit 或业务平台。 |
| 使用方 | tkredis 的复杂参数类型判断、tkcache 的 typed nil 判断，以及其他需要相同反射能力的组件。 |

按用户补充要求，`lib/libreflect` 的 `GetFieldByTag`、`SetFieldValue` 一并迁入，本次共六个函数、两个输出结构体。

## 接口与字段类型

保留现有函数名和输出结构体字段，不给函数、类型再加 reflectx 前后缀。所有已有结果字段的类型保持不变。

```go
type OriginValueAndKindOutput struct {
    InputValue  reflect.Value
    InputKind   reflect.Kind
    OriginValue reflect.Value
    OriginKind  reflect.Kind
}

type OriginTypeAndKindOutput struct {
    InputType  reflect.Type
    InputKind  reflect.Kind
    OriginType reflect.Type
    OriginKind reflect.Kind
}

func OriginValueAndKind(value any) (out OriginValueAndKindOutput)

func OriginTypeAndKind(value any) (out OriginTypeAndKindOutput)

func ValueToInterface(value reflect.Value) (result any, ok bool)

func IsNil(value any) (isNil bool)

func GetFieldByTag(structVal reflect.Value, structTyp reflect.Type, tagName string, tag string) (field reflect.Value)

func SetFieldValue(fieldVal reflect.Value, value any) (err error)
```

这些函数不涉及 I/O，不接收 ctx，不启动后台任务，也不持有全局可变状态。

## 类型与值解引用

`OriginValueAndKind` 接收普通值或 reflect.Value，保留输入的 Value 与 Kind，然后逐层解引用指针，返回最终 Value 与 Kind。输入为无效值或遇到 nil 指针时，OriginValue 可以是无效值、OriginKind 为 reflect.Invalid；调用方通过 IsValid 判断，不创建虚假的零值对象。

`OriginTypeAndKind` 接收普通值、reflect.Type 或有效 reflect.Value，保留输入类型与 Kind，逐层移除指针类型。typed nil 指针仍有类型，可以获取其最终元素类型；它与运行时值解引用后得到无效值的结果不同。

两个函数只沿指针层级解引用，不自动打开 interface 类型，也不展开 slice、map 或 struct。传入反射值表示的 interface 时，保留该层的 Kind，不擅自改变源函数的解引用范围。

nil 或无效 reflect.Value 无法提供 Type 时，类型结果返回零值，其中 reflect.Type 字段为 nil、Kind 为 reflect.Invalid。源实现直接对无效 reflect.Value 调用 Type 会 panic；迁移后先检查 IsValid，这是一项明确的边界修正。

## 安全取值

`ValueToInterface` 优先使用合法且 CanInterface 的值，返回原有动态类型。值不可直接 Interface 时，保留源实现通过对应 Kind 的读取方法获取基础标量，以及递归处理指针和 interface 的能力。

不可直接 Interface 时，数值读取遵循标准库反射访问方法的返回类型，例如有符号整数返回 int64，无符号整数返回 uint64，浮点数返回 float64，复数返回 complex128；不承诺在该分支保留具名类型。

无效值、nil 指针解引用后的无效值，以及无法安全读取的复合值返回 `nil, false`。实现不使用 unsafe 绕过反射访问限制，不修改输入对象。

## nil 判断

| 输入 | IsNil 结果 |
| --- | --- |
| nil interface | true。 |
| typed nil 指针、map、slice、chan、func、interface 或 unsafe.Pointer | true。 |
| 无效 reflect.Value | true，作为明确的边界定义。 |
| 有效 reflect.Value 包含 nil 值 | 按其 Kind 判断。 |
| 非 nil 的零长度 slice 或 map | false。 |
| 空字符串、0、false、零值 struct 或 array | false。 |

先处理无效反射值，再仅对允许的 Kind 调用 IsNil，避免对普通标量或无效值触发 panic。该函数判断 nil，不等同于 IsZero、内容为空或业务上的未命中。

源 IsNil 对无效 reflect.Value 返回 false；迁移后统一视为空并返回 true，需在迁移说明中明确记录。普通有效输入和 typed nil 的既有行为保持。

## 字段查找与赋值

`GetFieldByTag` 查找直接字段，默认使用 json 标签，并移除 json 的逗号选项；没有标签或标签恰为 `-` 时沿用 Go 字段名，保持源函数约定，不作为 JSON 可见性判定。支持指针输入，不递归展开嵌入字段。输入无效、实际类型不匹配或找不到字段时返回无效 reflect.Value。

`SetFieldValue` 优先按可赋值规则赋值，否则按 Go 可转换规则转换，字段声明类型不变。nil 仅可赋给允许 nil 的字段；不可写字段返回 `ErrNotSettable`，不合法转换返回 `ErrNotConvertible`，可通过 errors.Is 匹配。采用 Value.CanConvert 避免短切片转数组的动态 panic；数值截断仍遵循 Go 转换规则。

## 调用关系

```text
tkredis：判断复杂参数的原始类型
    → reflectx.OriginTypeAndKind

tkcache：在 JSON 编码前判断 typed nil
    → reflectx.IsNil

reflectx
    → reflect
```

组件内仅涉及已有具体类型的简单判断可以保留局部实现。与上述通用语义相同的判断统一复用 reflectx，避免各包对 nil 和指针层级做出不同解释。

## 文件与验收

```text
extension/reflectx/
├── reflect.go  # 类型和值分析、安全取值、nil 判断、标签查找与赋值
├── reflect_test.go # 边界行为验证
└── README.md   # 输入约定、使用示例及源实现边界修正
```

验收覆盖普通值、具名类型、多级指针、typed nil、无效 reflect.Value、不可直接 Interface 的基础标量、无法读取的复合值，以及非 nil 空集合与真正 nil 的区别。两种输出结构体的字段和类型与源定义逐项核对。

已新增用户授权的模块测试，覆盖上述边界及字段查找、赋值、错误识别。依赖仅为标准库；实施与验证记录见 [迁移实施计划](../plans/2026-10-21-toolkit-migration.md)。
