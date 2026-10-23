# tkaes 错误处理

可识别的包级错误集中声明在 [error.go](error.go)，使用 `errors.Is` 判断类别：

| 错误 | 含义 |
| --- | --- |
| ErrInvalidCiphertext | Base64/Hex 编码或 AES 分组长度无效 |
| ErrInvalidPadding | 解密后的填充数据无效 |

密钥长度错误直接复用标准库 `aes.KeySizeError`，可通过 `errors.AsType[aes.KeySizeError](err)` 获取。密文解码错误保留原始 Base64/Hex 原因，可继续通过 errors.AsType 检查具体类型。

密文和填充错误保留原有文本以兼容旧调用方；新代码使用错误类别或类型判断，不依赖文本。
