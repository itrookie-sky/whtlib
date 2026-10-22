package tksign_test

import (
	"bytes"
	"crypto"
	"crypto/ecdh"
	"crypto/ecdsa"
	"crypto/ed25519"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/base64"
	"encoding/pem"
	"errors"
	"math/big"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/itrookie-sky/whtlib/toolkit/tksign"
)

type keyFixture struct {
	name       string
	key        crypto.Signer
	privateDER []byte
	publicDER  []byte
	hash       crypto.Hash
}

// loadFixtures 生成进程内的临时测试密钥，不输出或保存私钥。
var loadFixtures = sync.OnceValues(func() (fixtures []keyFixture, err error) {
	rsaKey, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		return
	}
	fixtures = append(fixtures, keyFixture{name: "RSA", key: rsaKey, hash: crypto.SHA256})
	for _, curve := range []elliptic.Curve{elliptic.P256(), elliptic.P384(), elliptic.P521()} {
		key, keyErr := ecdsa.GenerateKey(curve, rand.Reader)
		if keyErr != nil {
			return nil, keyErr
		}
		hash := crypto.SHA256
		switch curve {
		case elliptic.P384():
			hash = crypto.SHA384
		case elliptic.P521():
			hash = crypto.SHA512
		}
		fixtures = append(fixtures, keyFixture{name: curve.Params().Name, key: key, hash: hash})
	}
	_, edKey, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		return
	}
	fixtures = append(fixtures, keyFixture{name: "Ed25519", key: edKey})
	for i := range fixtures {
		fixtures[i].privateDER, err = x509.MarshalPKCS8PrivateKey(fixtures[i].key)
		if err != nil {
			return nil, err
		}
		fixtures[i].publicDER, err = x509.MarshalPKIXPublicKey(fixtures[i].key.Public())
		if err != nil {
			return nil, err
		}
	}
	return
})

func fixturesFor(t *testing.T) (fixtures []keyFixture) {
	t.Helper()
	fixtures, err := loadFixtures()
	if err != nil {
		t.Fatal(err)
	}
	return
}

func pemText(blockType string, der []byte) (text string) {
	return string(pem.EncodeToMemory(&pem.Block{Type: blockType, Bytes: der}))
}

func certificateDER(t *testing.T, key crypto.Signer) (der []byte) {
	t.Helper()
	template := &x509.Certificate{
		SerialNumber: big.NewInt(1),
		Subject:      pkix.Name{CommonName: "tksign test"},
		NotBefore:    time.Unix(0, 0),
		NotAfter:     time.Unix(1, 0),
		KeyUsage:     x509.KeyUsageDigitalSignature,
	}
	der, err := x509.CreateCertificate(rand.Reader, template, template, key.Public(), key)
	if err != nil {
		t.Fatal(err)
	}
	return
}

func TestKeyFormats(t *testing.T) {
	for _, fixture := range fixturesFor(t) {
		t.Run(fixture.name, func(t *testing.T) {
			certificate := certificateDER(t, fixture.key)
			privatePEM := pemText("PRIVATE KEY", fixture.privateDER)
			publicPEM := pemText("PUBLIC KEY", fixture.publicDER)
			privateOptions := []tksign.Option{
				tksign.WithPrivateKey(privatePEM),
				tksign.WithPrivateKey(strings.ReplaceAll(privatePEM, "\n", `\n`)),
				tksign.WithPrivateKey(" \n" + strings.ReplaceAll(privatePEM, "\n", "\r\n") + "\t"),
				tksign.WithPrivateKey(strings.ReplaceAll(privatePEM, "\n", `\r\n`)),
				tksign.WithPrivateKey(base64.StdEncoding.EncodeToString(fixture.privateDER)),
				tksign.WithPrivateKey(base64.RawStdEncoding.EncodeToString(fixture.privateDER)),
				tksign.WithPrivateKeyDER(fixture.privateDER),
			}
			publicOptions := []tksign.Option{
				tksign.WithPublicKey(publicPEM),
				tksign.WithPublicKey(strings.ReplaceAll(publicPEM, "\n", `\n`)),
				tksign.WithPublicKey(base64.StdEncoding.EncodeToString(fixture.publicDER)),
				tksign.WithPublicKey(base64.RawStdEncoding.EncodeToString(fixture.publicDER)),
				tksign.WithPublicKeyDER(fixture.publicDER),
				tksign.WithPublicKey(pemText("CERTIFICATE", certificate)),
				tksign.WithPublicKey(base64.StdEncoding.EncodeToString(certificate)),
				tksign.WithPublicKeyDER(certificate),
			}
			if key, ok := fixture.key.(*rsa.PrivateKey); ok {
				privateOptions = append(privateOptions,
					tksign.WithPrivateKey(pemText("RSA PRIVATE KEY", x509.MarshalPKCS1PrivateKey(key))),
					tksign.WithPrivateKeyDER(x509.MarshalPKCS1PrivateKey(key)))
				publicOptions = append(publicOptions,
					tksign.WithPublicKey(pemText("RSA PUBLIC KEY", x509.MarshalPKCS1PublicKey(&key.PublicKey))),
					tksign.WithPublicKeyDER(x509.MarshalPKCS1PublicKey(&key.PublicKey)))
			}
			if key, ok := fixture.key.(*ecdsa.PrivateKey); ok {
				der, err := x509.MarshalECPrivateKey(key)
				if err != nil {
					t.Fatal(err)
				}
				privateOptions = append(privateOptions,
					tksign.WithPrivateKey(pemText("EC PRIVATE KEY", der)), tksign.WithPrivateKeyDER(der))
			}
			message := []byte("格式兼容\x00data")
			for i, option := range privateOptions {
				signer, err := tksign.New(option)
				if err != nil {
					t.Fatalf("private format %d: %v", i, err)
				}
				signature, err := signer.Sign(message)
				if err != nil {
					t.Fatal(err)
				}
				err = signer.Verify(message, signature)
				if err != nil {
					t.Fatal(err)
				}
				for j, publicOption := range publicOptions {
					verifier, createErr := tksign.New(publicOption)
					if createErr != nil {
						t.Fatalf("public format %d: %v", j, createErr)
					}
					err = verifier.Verify(message, signature)
					if err != nil {
						t.Fatalf("private format %d, public format %d: %v", i, j, err)
					}
				}
			}
		})
	}
}

func TestInvalidInitialization(t *testing.T) {
	fixture := fixturesFor(t)[0]
	privatePEM := pemText("PRIVATE KEY", fixture.privateDER)
	publicPEM := pemText("PUBLIC KEY", fixture.publicDER)
	xKey, err := ecdh.X25519().GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	xPrivate, err := x509.MarshalPKCS8PrivateKey(xKey)
	if err != nil {
		t.Fatal(err)
	}
	xPublic, err := x509.MarshalPKIXPublicKey(xKey.PublicKey())
	if err != nil {
		t.Fatal(err)
	}
	malformedPublic := x509.MarshalPKCS1PublicKey(&rsa.PublicKey{N: big.NewInt(0), E: 65537})
	tests := []struct {
		name string
		opts []tksign.Option
		want error
	}{
		{name: "no keys", want: tksign.ErrInvalidKey},
		{name: "only nil options", opts: []tksign.Option{nil}, want: tksign.ErrInvalidKey},
		{name: "empty private", opts: []tksign.Option{tksign.WithPrivateKey("")}, want: tksign.ErrInvalidKey},
		{name: "empty public with private", opts: []tksign.Option{tksign.WithPrivateKey(privatePEM), tksign.WithPublicKey("")}, want: tksign.ErrInvalidKey},
		{name: "invalid private", opts: []tksign.Option{tksign.WithPrivateKey("not-a-private-key")}, want: tksign.ErrInvalidKey},
		{name: "invalid DER", opts: []tksign.Option{tksign.WithPrivateKeyDER([]byte{0, 1, 2})}, want: tksign.ErrInvalidKey},
		{name: "empty DER", opts: []tksign.Option{tksign.WithPublicKeyDER(nil)}, want: tksign.ErrInvalidKey},
		{name: "private as public", opts: []tksign.Option{tksign.WithPublicKey(privatePEM)}, want: tksign.ErrInvalidKey},
		{name: "public as private", opts: []tksign.Option{tksign.WithPrivateKey(publicPEM)}, want: tksign.ErrInvalidKey},
		{name: "garbage prefix", opts: []tksign.Option{tksign.WithPublicKey("garbage\n" + publicPEM)}, want: tksign.ErrInvalidKey},
		{name: "garbage suffix", opts: []tksign.Option{tksign.WithPublicKey(publicPEM + "garbage")}, want: tksign.ErrInvalidKey},
		{name: "multiple blocks", opts: []tksign.Option{tksign.WithPublicKey(publicPEM + publicPEM)}, want: tksign.ErrInvalidKey},
		{name: "malformed preceding block", opts: []tksign.Option{tksign.WithPublicKey("-----BEGIN PUBLIC KEY-----\ninvalid\n" + publicPEM)}, want: tksign.ErrInvalidKey},
		{name: "malformed PEM", opts: []tksign.Option{tksign.WithPrivateKey("-----BEGIN PRIVATE KEY-----\ninvalid")}, want: tksign.ErrInvalidKey},
		{name: "encrypted PKCS8", opts: []tksign.Option{tksign.WithPrivateKey(pemText("ENCRYPTED PRIVATE KEY", fixture.privateDER))}, want: tksign.ErrInvalidKey},
		{name: "encrypted legacy PEM", opts: []tksign.Option{tksign.WithPrivateKey(string(pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: fixture.privateDER, Headers: map[string]string{"Proc-Type": "4,ENCRYPTED"}})))}, want: tksign.ErrInvalidKey},
		{name: "unsupported private type", opts: []tksign.Option{tksign.WithPrivateKeyDER(xPrivate)}, want: tksign.ErrInvalidKey},
		{name: "unsupported public type", opts: []tksign.Option{tksign.WithPublicKeyDER(xPublic)}, want: tksign.ErrInvalidKey},
		{name: "invalid RSA modulus", opts: []tksign.Option{tksign.WithPublicKeyDER(malformedPublic)}, want: tksign.ErrInvalidKey},
		{name: "zero hash", opts: []tksign.Option{tksign.WithPublicKey(publicPEM), tksign.WithHash(0)}, want: tksign.ErrUnsupportedOption},
		{name: "SHA1", opts: []tksign.Option{tksign.WithPublicKey(publicPEM), tksign.WithHash(crypto.SHA1)}, want: tksign.ErrUnsupportedOption},
		{name: "unknown encoding", opts: []tksign.Option{tksign.WithPublicKey(publicPEM), tksign.WithEncoding("unknown")}, want: tksign.ErrUnsupportedOption},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			instance, createErr := tksign.New(test.opts...)
			if instance != nil || !errors.Is(createErr, test.want) {
				t.Fatalf("expected nil instance and %v, got %v", test.want, createErr)
			}
			if strings.Contains(createErr.Error(), privatePEM) || strings.Contains(createErr.Error(), "not-a-private-key") {
				t.Fatal("error contains key input")
			}
		})
	}
	for _, fixture := range fixturesFor(t)[1:] {
		_, createErr := tksign.New(tksign.WithPrivateKeyDER(fixture.privateDER), tksign.WithRSAPSS())
		if !errors.Is(createErr, tksign.ErrUnsupportedOption) {
			t.Fatalf("%s accepted RSA-PSS", fixture.name)
		}
		if fixture.name == "Ed25519" {
			_, createErr = tksign.New(tksign.WithPrivateKeyDER(fixture.privateDER), tksign.WithHash(crypto.SHA512))
			if !errors.Is(createErr, tksign.ErrUnsupportedOption) {
				t.Fatal("Ed25519 accepted external hash")
			}
		}
	}
}

func TestOptionsCopyAndOverride(t *testing.T) {
	fixture := fixturesFor(t)[0]
	private := bytes.Clone(fixture.privateDER)
	public := bytes.Clone(fixture.publicDER)
	privateOption := tksign.WithPrivateKeyDER(private)
	publicOption := tksign.WithPublicKeyDER(public)
	clear(private)
	clear(public)
	for range 2 {
		signer, err := tksign.New(nil, tksign.WithPrivateKey("invalid"), privateOption,
			tksign.WithPublicKey("invalid"), publicOption,
			tksign.WithHash(crypto.SHA1), tksign.WithHash(crypto.SHA256),
			tksign.WithEncoding("invalid"), tksign.WithEncoding(tksign.EncodingBase64))
		if err != nil {
			t.Fatal(err)
		}
		signature, err := signer.SignString("copied keys")
		if err != nil {
			t.Fatal(err)
		}
		err = signer.VerifyString("copied keys", signature)
		if err != nil {
			t.Fatal(err)
		}
	}
	_, err := tksign.New(privateOption, tksign.WithPrivateKey(""))
	if !errors.Is(err, tksign.ErrInvalidKey) {
		t.Fatal("last empty key must override an earlier valid key")
	}
}

func TestTrailingDERRejected(t *testing.T) {
	for _, fixture := range fixturesFor(t) {
		t.Run(fixture.name, func(t *testing.T) {
			private := append(bytes.Clone(fixture.privateDER), 0)
			public := append(bytes.Clone(fixture.publicDER), 0)
			for _, option := range []tksign.Option{
				tksign.WithPrivateKeyDER(private),
				tksign.WithPrivateKey(base64.StdEncoding.EncodeToString(private)),
				tksign.WithPrivateKey(pemText("PRIVATE KEY", private)),
				tksign.WithPublicKeyDER(public),
				tksign.WithPublicKey(base64.StdEncoding.EncodeToString(public)),
				tksign.WithPublicKey(pemText("PUBLIC KEY", public)),
			} {
				instance, err := tksign.New(option)
				if instance != nil || !errors.Is(err, tksign.ErrInvalidKey) {
					t.Fatal("DER with trailing garbage was accepted")
				}
			}
		})
	}
}
