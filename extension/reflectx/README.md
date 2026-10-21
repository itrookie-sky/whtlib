# reflectx

Go 标准库 `reflect` 的扩展，合并迁移自 go-gf-lib 的 `plugins/plreflect` 和 `lib/libreflect`，仅依赖标准库。

| 接口 | 行为 |
| --- | --- |
| `OriginValueAndKind` | 保留输入反射值，并逐层解开指针；nil 指针的最终值无效。 |
| `OriginTypeAndKind` | 接收普通值、`reflect.Type` 或 `reflect.Value`，逐层解开指针类型。 |
| `ValueToInterface` | 安全取值；不可直接 Interface 的基础标量通过反射访问方法读取。 |
| `IsNil` | 识别 typed nil 与无效反射值；空字符串、0、false、非 nil 空集合均返回 false。 |
| `GetFieldByTag` | 按标签查找直接字段，默认 json，移除 json 标签选项；无标签或 `-` 沿用字段名。 |
| `SetFieldValue` | 按可赋值或可转换规则更新字段，保留字段声明类型。 |

```go
type User struct {
    ID int64 `json:"id,omitempty"`
}
user := User{}
field := reflectx.GetFieldByTag(reflect.ValueOf(&user), reflect.TypeOf(user), "id", "")
err := reflectx.SetFieldValue(field, int32(123))
```

`GetFieldByTag` 不递归查找嵌入字段，找不到、类型不匹配或输入无效时返回无效 `reflect.Value`；`json:"-"` 按源函数约定可通过 Go 字段名查找，并非序列化可见性判断。取得的字段能否修改仍由 `CanSet` 决定。

`SetFieldValue` 的错误可通过 `errors.Is` 匹配 `ErrNotSettable`、`ErrNotConvertible`。nil 只可赋给允许 nil 的字段；长度不足的切片转数组返回错误。数值转换遵循 Go 转换规则，不额外检查截断或溢出。

迁移修正：`OriginTypeAndKind` 对无效值返回零结果；`IsNil(reflect.Value{})` 返回 true；字段赋值对 nil、不可写字段和不合法动态转换返回错误。可直接 Interface 的值保留原类型；不可直接访问的数值按反射访问方法返回 int64、uint64、float64 或 complex128，不使用 unsafe 绕过访问限制。
