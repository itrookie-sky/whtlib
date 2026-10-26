# pathx

基于 `path` 扩展以 `/` 分隔的逻辑路径，适合对象键和已提取的 URL 路径。

```go
pathx.HasPathPrefix("/api/v1/users", "/api/v1")  // true
pathx.HasPathPrefix("/api/v10/users", "/api/v1") // false

rest, matched := pathx.TrimPathPrefix("/api/v1/users", "/api/v1")
// rest == "users", matched == true
```

- 两个接口先按 `path.Clean` 清理路径，再按完整分段匹配。
- `/` 表示绝对根，`.` 表示相对根；后者不匹配 `..` 或以 `../` 开头的路径。
- 绝对路径与相对路径不混合匹配；任意参数为空时不匹配。
- TrimPathPrefix 完全匹配时返回 `"", true`；匹配子路径时移除前缀及分隔符。
- 不匹配时返回原始输入和 false，保留重复斜杠等原始内容。
- 反斜杠是普通字符，不解析完整 URL，也不执行百分号解码。

例如 `/api/v1/../v2` 不匹配 `/api/v1`；`/api/v1/%2e%2e` 匹配后返回字面量 `%2e%2e`。
这些接口只判断路径关系，不应单独用作 URL 授权或文件访问安全边界。
