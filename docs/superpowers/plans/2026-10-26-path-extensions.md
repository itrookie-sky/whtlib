# 路径扩展实施计划

依据：用户确认实现全部八个建议接口，允许补充相关测试。
在 main 直接实施，不使用 worktree，不增加审核环节，验证后直接 push。

## 接口与边界

- `filepathx.FindUp(start, marker string) (dir string, err error)`：从已有目录向上寻找单个标记名，返回绝对目录，不改变 cwd；未找到使用 `fs.ErrNotExist`，其他文件系统错误保留错误链。
- `filepathx.JoinWithin(base string, elems ...string) (joined string, err error)`：返回 base 内的绝对路径，拒绝空 base、各分段中的绝对路径或卷名，以及拼接后的词法越界；空分段忽略，无分段时返回绝对 base。
- `filepathx.RelWithin(base, target string) (relative string, err error)`：两个相对参数均相对于 cwd 解释，相同目录返回 `.`，越界报错。
- `filepathx.ExpandHome(name string) (expanded string, err error)`：展开 `~` 和 `~/`，不展开环境变量，不支持 `~user`。
- `filepathx.Stem(name string) (stem string)`：去掉文件名最后的扩展名；`.env` 保留，`.env.local` 返回 `.env`；没有文件名时返回空字符串。
- `filepathx.ReplaceExt(name, ext string) (result string, err error)`：保留目录原样，扩展名允许带点或不带点，空值表示删除；无文件名、扩展名含分隔符或只有点时失败。
- `pathx.HasPathPrefix(name, prefix string) (matched bool)`：先 Clean，再按 `/` 分段判断；空参数不匹配，`.` 表示相对根，`/` 表示绝对根。
- `pathx.TrimPathPrefix(name, prefix string) (rest string, matched bool)`：匹配后返回规范化的剩余路径，完全匹配返回空；不匹配保留原始输入。

目录限制仅做词法检查，不解析符号链接，不构成文件访问安全沙箱。pathx 不解析 URL、不做百分号解码。
错误集中在 filepathx/error.go，以 errors.Is 判断，保留原始文件系统错误。

## 执行清单

- [x] 实现 filepathx 六个接口、集中错误和 README。
- [x] 实现 pathx 两个接口和 README。
- [x] testx 复用 FindUp；tkredis 脚本名提取复用 Stem。
- [x] 补充边界测试，执行相关包测试、race、vet、build；不执行全仓测试或外部数据库测试。
- [x] 同步 codegraph，核对实现差异。

## 验证记录

- `go test -race ./extension/filepathx ./extension/pathx ./extension/testx ./toolkit/tkredis` 通过；Redis 使用现有 miniredis 内存服务。
- 上述四个包的 `go vet`、`go build` 通过。
- `GOOS=windows GOARCH=amd64 go build` 上述四个包通过；未执行 Windows 运行测试。
- `git diff --check` 通过，codegraph 已同步。

发布步骤：使用中文提交说明，在 main 上普通快进 push；发布后核对远端 SHA 与本地 HEAD 一致。
