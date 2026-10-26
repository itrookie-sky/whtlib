# filepathx

基于 `path/filepath` 扩展本地文件系统路径，沿用运行平台的分隔符和卷名语义。

| 接口 | 用途 |
| --- | --- |
| `FindUp(start, marker) (string, error)` | 从已有目录向上查找包含标记的最近目录，返回绝对路径，不改变 cwd |
| `JoinWithin(base string, elems ...string) (string, error)` | 把多个相对路径分段拼到 base 内，返回绝对路径 |
| `RelWithin(base, target) (string, error)` | 确认 target 位于 base 内后计算相对路径 |
| `ExpandHome(name) (string, error)` | 展开 `~`、`~/`；Windows 也支持反斜杠 |
| `Stem(name) string` | 取文件名并删除最后一个扩展名 |
| `ReplaceExt(name, ext) (string, error)` | 保留目录原样，替换最后一个扩展名 |

```go
root, err := filepathx.FindUp(".", "go.mod")
if err != nil {
    return err
}
configPath, err := filepathx.JoinWithin(root, "config", "app.json")
if err != nil {
    return err
}
relative, err := filepathx.RelWithin(root, configPath) // config/app.json
if err != nil {
    return err
}
_ = relative
```

`JoinWithin`、`RelWithin` 不要求路径存在。`.` 表示目录自身；base 必须非空。
`JoinWithin` 忽略空分段，不传分段或全部为空时返回规范化后的绝对 base。
`RelWithin` 的相对参数均相对于当前工作目录解释，例如 `RelWithin("data", "data/a")` 返回 `a`。
`RelWithin` 的 target 必须非空，并拒绝同前缀的兄弟目录。
`JoinWithin` 每个分段都拒绝绝对路径、根路径和卷名；拼接清理后的整体相对路径不能逃出 base。
例如 `JoinWithin(root, "config", "..", "app.json")` 合法，`JoinWithin(root, "..", "app.json")` 报错。
原有 `JoinWithin(base, relative)` 直接调用方式保持兼容；若将函数赋给固定二参数的函数变量，需同步调整变量类型。

以上限制只做词法判断，不解析符号链接。若需要实际限制文件访问范围，应使用标准库 `os.Root`。
Unix 上反斜杠是普通字符；处理外部平台的路径前应先确定其格式。

`FindUp` 的 marker 必须是单个名称，例如 `go.mod`、`.git`，可以指向文件或目录。
未找到返回可由 `errors.Is(err, fs.ErrNotExist)` 判断的错误；权限等错误直接保留错误链，
可由 `errors.AsType[*fs.PathError](err)` 获取底层路径信息，不将它们误判为未找到。

`ExpandHome` 不支持 `~user`，不展开 `$HOME` 等环境变量；非 tilde 路径原样返回。
展开后会清理路径，`~/..` 可以指向用户目录的父目录，不提供目录范围限制。

隐藏文件规则：`.env` 的 Stem 是 `.env`，`.env.local` 的 Stem 是 `.env`，
`archive.tar.gz` 的 Stem 是 `archive.tar`。以分隔符结尾、空路径、`.`、`..` 没有文件名，
Stem 返回空字符串，ReplaceExt 返回 `ErrInvalidPath`。

ReplaceExt 的 ext 可为 `json`、`.json` 或 `tar.gz`，空字符串表示移除扩展名；
分隔符、空字符和只有点的扩展名返回 `ErrInvalidExtension`。
包级错误集中在 `error.go`，目录范围错误使用 `ErrOutsideBase`，均支持 `errors.Is`。
