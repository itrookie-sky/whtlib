# tksign

框架无关的签名验签工具，只依赖 Go 标准库。通过函数选项按需传入公钥、私钥，在初始化时解析并确定算法，实例可并发复用。

## 快速使用

```go
signer, err := tksign.New(tksign.WithPrivateKey(privateKey))
if err != nil {
    return
}
signature, err := signer.SignString("待签名数据")
if err != nil {
    return
}

verifier, err := tksign.New(tksign.WithPublicKey(publicKey))
if err != nil {
    return
}
err = verifier.VerifyString("待签名数据", signature)
if err != nil {
    return
}
```

导入路径：`github.com/itrookie-sky/whtlib/toolkit/tksign`。上面的代码片段用于具有命名错误返回值的业务函数；完整可运行示例见 `example_test.go`。

## 按需配置密钥

```go
// 只有私钥：可签名，自动导出公钥用于验签。
signer, err := tksign.New(tksign.WithPrivateKey(privateKey))

// 只有公钥：可验签，调用签名方法返回 ErrMissingPrivateKey。
verifier, err := tksign.New(tksign.WithPublicKey(publicKey))

// 同时传入：私钥用于签名，显式公钥用于验签。
client, err := tksign.New(
    tksign.WithPrivateKey(myPrivateKey),
    tksign.WithPublicKey(peerPublicKey),
)
```

三个示例分别使用，初始化后先检查 `err`。签名私钥和显式验签公钥可以来自不同密钥对，甚至使用不同算法；验签失败不会回退到私钥导出的公钥。

`WithPrivateKey` / `WithPublicKey` 接收密钥内容字符串，支持 PEM、无头尾的标准 Base64 DER（有无填充均可）。PEM 支持首尾空白、CRLF 和环境配置中的字面量 `\n` / `\r\n`。

二进制 DER 使用独立选项，传入的切片会立即复制；选项可以复用：

```go
signer, err := tksign.New(tksign.WithPrivateKeyDER(privateDER))
verifier, err := tksign.New(tksign.WithPublicKeyDER(publicDER))
```

所有选项在初始化时生效，同类选项以后一次为准，`nil` 选项忽略。没有密钥、显式空密钥、格式错误或不兼容选项会导致初始化失败，返回 `nil` 实例和错误。

## 支持的格式

| 内容 | 支持的封装 |
| --- | --- |
| RSA 私钥 | PKCS#1、未加密 PKCS#8 |
| ECDSA 私钥 | SEC1、未加密 PKCS#8 |
| Ed25519 私钥 | 未加密 PKCS#8 |
| RSA 公钥 | PKCS#1、PKIX/SPKI |
| ECDSA / Ed25519 公钥 | PKIX/SPKI |
| X.509 证书 | 提取 RSA、ECDSA、Ed25519 公钥 |

PEM 标签支持 `RSA PRIVATE KEY`、`PRIVATE KEY`、`EC PRIVATE KEY`、`RSA PUBLIC KEY`、`PUBLIC KEY` 和 `CERTIFICATE`，公私钥选项须使用对应类型。

每个密钥参数接受单个 PEM 块或单个 DER 序列，拒绝垃圾前后缀和拼接的多个密钥或证书。证书仅作为公钥容器，不校验证书链、有效期或用途；调用方负责建立公钥信任关系。

首版不处理带密码的私钥、OpenSSH、JWK、PKCS#12、国密或 X25519 密钥，不读取文件或自动生成密钥。

## 算法与摘要

| 密钥 | 默认签名方式 |
| --- | --- |
| RSA | PKCS#1 v1.5 + SHA-256 |
| ECDSA P-256 | SHA-256，ASN.1 DER 签名 |
| ECDSA P-384 | SHA-384，ASN.1 DER 签名 |
| ECDSA P-521 | SHA-512，ASN.1 DER 签名 |
| Ed25519 | 对原始消息签名 |

RSA 接受至少 1024 位的密钥，遵守标准库的最低长度限制；ECDSA 支持上表的三种曲线。

使用 `WithHash(crypto.SHA256)`、`WithHash(crypto.SHA384)` 或 `WithHash(crypto.SHA512)` 覆盖 RSA / ECDSA 默认摘要。Ed25519 使用原始消息，与显式 `WithHash` 选项不兼容。

RSA-PSS 通过函数选项启用：

```go
signer, err := tksign.New(
    tksign.WithPrivateKey(privateKey),
    tksign.WithRSAPSS(),
    tksign.WithHash(crypto.SHA256),
)
```

签名和验签均固定 PSS 盐长度等于摘要长度。验签方须使用相同的 PSS 和摘要配置；不会自动尝试 PKCS#1 v1.5、其他盐长度或其他摘要。混合算法实例中 `WithRSAPSS()` 只影响 RSA 角色，没有 RSA 密钥时返回 `ErrUnsupportedOption`。

## 原始字节与字符串

```go
signature, err := signer.Sign(data)
err = verifier.Verify(data, signature)

encoded, err := signer.SignString("待签名数据")
err = verifier.VerifyString("待签名数据", encoded)
```

`Sign` / `Verify` 直接接收消息和签名字节。字符串方法使用字符串的原始字节，签名编码默认为标准 Base64；空消息可以签名。上述每一步调用后都应先检查错误，再使用结果。

`WithEncoding` 只影响字符串方法：

| 选项值 | 编码 |
| --- | --- |
| `EncodingBase64` | 标准 Base64，有填充 |
| `EncodingBase64URL` | URL 安全 Base64，无填充 |
| `EncodingHex` | 十六进制，输出小写 |

双方应使用相同编码。验签严格按已配置编码解码，不自动尝试其他编码。

调用方传入原始消息即可，包内部完成算法需要的摘要计算；不要先自行计算摘要再传入。HTTP 参数排序、JSON 序列化、字段拼接等业务约定由调用方在签名前完成，签名和验签必须使用完全相同的字节。

## 错误处理

使用 `errors.Is` 判断错误：

| 错误 | 含义 |
| --- | --- |
| `ErrInvalidKey` | 未提供密钥、格式错误、不支持的类型或无效密钥 |
| `ErrMissingPrivateKey` | 公钥实例或未初始化实例尝试签名 |
| `ErrMissingPublicKey` | 未初始化实例尝试验签 |
| `ErrUnsupportedOption` | 编码、摘要或 PSS 配置不适用于密钥 |
| `ErrVerification` | 签名解码失败、签名畸形或消息/公钥不匹配 |

nil 实例和零值实例的方法返回缺少密钥错误。错误不包含传入的密钥、消息或签名内容。

## 验证

```sh
go test -race -cover ./toolkit/tksign
go vet ./toolkit/tksign
```
