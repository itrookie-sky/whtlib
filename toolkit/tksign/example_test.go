package tksign_test

import (
	"crypto/ed25519"
	"crypto/rand"
	"crypto/x509"
	"encoding/pem"
	"fmt"

	"github.com/itrookie-sky/whtlib/toolkit/tksign"
)

func ExampleNew() {
	// 示例临时生成密钥；实际使用时传入已有密钥字符串即可。
	publicKey, privateKey, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		panic(err)
	}
	privateDER, err := x509.MarshalPKCS8PrivateKey(privateKey)
	if err != nil {
		panic(err)
	}
	publicDER, err := x509.MarshalPKIXPublicKey(publicKey)
	if err != nil {
		panic(err)
	}
	privatePEM := string(pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: privateDER}))
	publicPEM := string(pem.EncodeToMemory(&pem.Block{Type: "PUBLIC KEY", Bytes: publicDER}))

	signer, err := tksign.New(tksign.WithPrivateKey(privatePEM))
	if err != nil {
		panic(err)
	}
	verifier, err := tksign.New(tksign.WithPublicKey(publicPEM))
	if err != nil {
		panic(err)
	}
	signature, err := signer.SignString("hello")
	if err != nil {
		panic(err)
	}
	err = verifier.VerifyString("hello", signature)
	fmt.Println(err == nil)
	// Output: true
}
