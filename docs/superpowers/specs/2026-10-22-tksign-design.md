# tksign 签名验签包设计

## 目标与约束

在 `toolkit/tksign` 提供框架无关的签名验签组件，初始化后重复使用，使用 Go 标准库，不增加第三方依赖。用户指定采用函数选项方式，按需传入公钥、私钥，不绑定平台或业务协议。

## 方案

- 采用 `New(opts ...Option)`，使用 `WithPrivateKey` 和 `WithPublicKey` 分别配置密钥。这是用户指定的方案，调用方只传实际需要的参数。
- 配置结构体虽然便于集中列出参数，但不符合本次指定的函数选项入口。
- 按 RSA、ECDSA、Ed25519 拆分公开构造函数，会让调用方先识别密钥类型；采用统一入口，在初始化时识别并固定算法。

以下算法范围、默认值、编码和错误行为已由用户确认并要求执行。

## 初始化与密钥角色

```go
signer, err := tksign.New(tksign.WithPrivateKey(privateKey))
verifier, err := tksign.New(tksign.WithPublicKey(publicKey))
both, err := tksign.New(
    tksign.WithPrivateKey(privateKey),
    tksign.WithPublicKey(publicKey),
)
```

- `WithPrivateKey` 和 `WithPublicKey` 接收字符串，适用于配置文件、环境变量中的密钥。
- 只有公钥时支持验签，签名返回缺少私钥的错误。
- 只有私钥时，从私钥导出公钥，同时支持签名和验签。
- 同时提供时，私钥用于签名，显式公钥用于验签；不要求两者成对，允许用自己的私钥签名、对方的公钥验签。验签失败时不回退到私钥导出的公钥。
- 未提供任何密钥、显式传入空密钥、密钥格式错误、算法与密钥类型不兼容时，初始化返回错误。
- 重复的同类选项以后一次为准；忽略 `nil` 选项。
- 密钥和配置只在初始化时处理，实例不提供密钥修改方法，可以并发使用。

## 密钥格式

自动识别 PEM、无头尾的 Base64 DER；PEM 支持实际换行和配置中的字面量 `\n`。通过 `WithPrivateKeyDER`、`WithPublicKeyDER` 接收二进制 DER。

| 密钥类型 | 支持的封装 |
| --- | --- |
| RSA 私钥 | PKCS#1、未加密 PKCS#8 |
| ECDSA 私钥 | SEC1、未加密 PKCS#8 |
| Ed25519 私钥 | 未加密 PKCS#8 |
| RSA 公钥 | PKCS#1、PKIX/SPKI、X.509 证书中的公钥 |
| ECDSA / Ed25519 公钥 | PKIX/SPKI、X.509 证书中的公钥 |

证书输入仅提取公钥，不执行证书链验证。加密私钥、OpenSSH、JWK、PKCS#12 和国密格式不在首版范围；不通过增加隐式格式猜测处理这些输入。

## 算法与签名编码

- 根据签名私钥、验签公钥分别识别算法，允许两个角色使用不同类型的密钥。
- RSA 默认使用 PKCS#1 v1.5 + SHA-256，提供 `WithRSAPSS()` 切换为 PSS；PSS 签名和验签均固定 salt 长度等于摘要长度。
- ECDSA 使用 ASN.1 DER 签名，P-256 默认 SHA-256、P-384 默认 SHA-384、P-521 默认 SHA-512。
- RSA / ECDSA 可通过 `WithHash` 显式指定 SHA-256、SHA-384、SHA-512；Ed25519 使用原始消息，不接受外部摘要选项。
- 不根据待验签数据自动切换算法，不自动拼接、排序、序列化业务数据。
- `Sign` / `Verify` 操作原始字节数据和签名字节；`SignString` / `VerifyString` 提供字符串便捷入口，默认使用标准 Base64 表示签名。
- `WithEncoding` 可选择标准 Base64、无填充 Base64URL、Hex，只影响字符串便捷入口；验签按配置解码，不试探其他编码。

```go
signature, err := signer.SignString("待签名数据")
err = verifier.VerifyString("待签名数据", signature)
```

## 错误与实现边界

- 通过可供 `errors.Is` 判断的错误区分无效密钥、缺少私钥、缺少公钥、不支持的选项和验签失败。
- 不在错误或日志中附带密钥、原始消息或签名内容。
- 不自动读取文件、生成密钥、保存密钥或接入业务请求流程。
- 不修改现有组件的接口和字段类型。
- 提供包内 README，说明初始化、仅签名、仅验签、格式兼容及算法编码的互操作约定。

## 验证建议

按仓库规则，新增测试须获得用户批准。建议批准后覆盖跨密钥格式的签名验签、标准库互操作、消息或签名篡改、显式公钥优先和无效初始化；仅运行 `tksign` 包相关检查，不运行整个项目测试或连接外部服务。

## 参考

- [Go crypto/x509：密钥与证书格式解析](https://pkg.go.dev/crypto/x509)
- [Go crypto/rsa：PKCS#1 v1.5、PSS 与 salt 长度](https://pkg.go.dev/crypto/rsa)
- [Go crypto/ecdsa：ASN.1 签名](https://pkg.go.dev/crypto/ecdsa)
- [Go crypto/ed25519：消息签名验签](https://pkg.go.dev/crypto/ed25519)
