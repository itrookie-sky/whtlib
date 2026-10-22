package tksign_test

import (
	"bytes"
	"crypto"
	"crypto/ecdsa"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"sync"
	"testing"

	"github.com/itrookie-sky/whtlib/toolkit/tksign"
)

func standardDigest(data []byte, hash crypto.Hash) (digest []byte) {
	if hash == 0 {
		return data
	}
	hasher := hash.New()
	hasher.Write(data)
	return hasher.Sum(nil)
}

func standardSignature(t *testing.T, key crypto.Signer, data []byte, hash crypto.Hash, pss bool) (signature []byte) {
	t.Helper()
	var opts crypto.SignerOpts = hash
	if _, isRSA := key.(*rsa.PrivateKey); isRSA && pss {
		opts = &rsa.PSSOptions{SaltLength: rsa.PSSSaltLengthEqualsHash, Hash: hash}
	}
	signature, err := key.Sign(rand.Reader, standardDigest(data, hash), opts)
	if err != nil {
		t.Fatal(err)
	}
	return
}

func verifyStandard(t *testing.T, key crypto.PublicKey, data, signature []byte, hash crypto.Hash, pss bool) {
	t.Helper()
	digest := standardDigest(data, hash)
	var err error
	switch publicKey := key.(type) {
	case *rsa.PublicKey:
		if pss {
			err = rsa.VerifyPSS(publicKey, hash, digest, signature, &rsa.PSSOptions{SaltLength: rsa.PSSSaltLengthEqualsHash})
			break
		}
		err = rsa.VerifyPKCS1v15(publicKey, hash, digest, signature)
	case *ecdsa.PublicKey:
		if !ecdsa.VerifyASN1(publicKey, digest, signature) {
			t.Fatal("standard library rejected ECDSA signature")
		}
	case ed25519.PublicKey:
		if !ed25519.Verify(publicKey, data, signature) {
			t.Fatal("standard library rejected Ed25519 signature")
		}
	default:
		t.Fatal("unsupported fixture")
	}
	if err != nil {
		t.Fatal(err)
	}
}

func TestStandardLibraryInteroperability(t *testing.T) {
	for _, fixture := range fixturesFor(t) {
		t.Run(fixture.name, func(t *testing.T) {
			type interopCase struct {
				name string
				hash crypto.Hash
				pss  bool
				opts []tksign.Option
			}
			tests := []interopCase{{name: "default", hash: fixture.hash}}
			if fixture.hash != 0 {
				for _, hash := range []crypto.Hash{crypto.SHA256, crypto.SHA384, crypto.SHA512} {
					tests = append(tests, interopCase{name: hash.String(), hash: hash, opts: []tksign.Option{tksign.WithHash(hash)}})
				}
			}
			if fixture.name == "RSA" {
				for _, hash := range []crypto.Hash{crypto.SHA256, crypto.SHA384, crypto.SHA512} {
					tests = append(tests, interopCase{name: "PSS-" + hash.String(), hash: hash, pss: true, opts: []tksign.Option{tksign.WithRSAPSS(), tksign.WithHash(hash)}})
				}
			}
			for _, test := range tests {
				t.Run(test.name, func(t *testing.T) {
					opts := append([]tksign.Option{tksign.WithPrivateKeyDER(fixture.privateDER)}, test.opts...)
					signer, err := tksign.New(opts...)
					if err != nil {
						t.Fatal(err)
					}
					for _, data := range [][]byte{nil, {}, []byte("hello\x00签名\xff")} {
						signature, signErr := signer.Sign(data)
						if signErr != nil {
							t.Fatal(signErr)
						}
						verifyStandard(t, fixture.key.Public(), data, signature, test.hash, test.pss)
						external := standardSignature(t, fixture.key, data, test.hash, test.pss)
						err = signer.Verify(data, external)
						if err != nil {
							t.Fatal(err)
						}
						err = signer.Verify(append(bytes.Clone(data), 'x'), signature)
						if !errors.Is(err, tksign.ErrVerification) {
							t.Fatal("tampered message was accepted")
						}
						changed := bytes.Clone(signature)
						changed[len(changed)-1] ^= 1
						err = signer.Verify(data, changed)
						if !errors.Is(err, tksign.ErrVerification) {
							t.Fatal("tampered signature was accepted")
						}
						for _, invalid := range [][]byte{nil, {0}, append(bytes.Clone(signature), 0)} {
							err = signer.Verify(data, invalid)
							if !errors.Is(err, tksign.ErrVerification) {
								t.Fatal("malformed signature was accepted")
							}
						}
					}
				})
			}
		})
	}
}

func TestExplicitPublicKeyTakesPrecedence(t *testing.T) {
	fixtures := fixturesFor(t)
	otherRSA, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	otherPublic, err := x509.MarshalPKIXPublicKey(otherRSA.Public())
	if err != nil {
		t.Fatal(err)
	}
	other := keyFixture{name: "other RSA", key: otherRSA, publicDER: otherPublic, hash: crypto.SHA256}
	tests := []struct {
		name    string
		private keyFixture
		public  keyFixture
		pss     bool
	}{
		{name: "different RSA pairs", private: fixtures[0], public: other},
		{name: "RSA to Ed25519", private: fixtures[0], public: fixtures[4]},
		{name: "Ed25519 to ECDSA", private: fixtures[4], public: fixtures[1]},
		{name: "PSS signing and ECDSA verification", private: fixtures[0], public: fixtures[1], pss: true},
		{name: "ECDSA signing and PSS verification", private: fixtures[1], public: fixtures[0], pss: true},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			opts := []tksign.Option{tksign.WithPrivateKeyDER(test.private.privateDER), tksign.WithPublicKeyDER(test.public.publicDER)}
			if test.pss {
				opts = append(opts, tksign.WithRSAPSS())
			}
			instance, createErr := tksign.New(opts...)
			if createErr != nil {
				t.Fatal(createErr)
			}
			data := []byte("independent roles")
			own, signErr := instance.Sign(data)
			if signErr != nil {
				t.Fatal(signErr)
			}
			verifyStandard(t, test.private.key.Public(), data, own, test.private.hash, test.pss)
			verifyErr := instance.Verify(data, own)
			if !errors.Is(verifyErr, tksign.ErrVerification) {
				t.Fatal("verification fell back to the signing key")
			}
			external := standardSignature(t, test.public.key, data, test.public.hash, test.pss)
			verifyErr = instance.Verify(data, external)
			if verifyErr != nil {
				t.Fatal(verifyErr)
			}
		})
	}
}

func TestPSSDoesNotDowngrade(t *testing.T) {
	fixture := fixturesFor(t)[0]
	signer, err := tksign.New(tksign.WithPrivateKeyDER(fixture.privateDER), tksign.WithRSAPSS())
	if err != nil {
		t.Fatal(err)
	}
	data := []byte("fixed salt length")
	pkcs1 := standardSignature(t, fixture.key, data, fixture.hash, false)
	err = signer.Verify(data, pkcs1)
	if !errors.Is(err, tksign.ErrVerification) {
		t.Fatal("PSS verifier accepted PKCS#1 v1.5 signature")
	}
	shortSalt, err := rsa.SignPSS(rand.Reader, fixture.key.(*rsa.PrivateKey), fixture.hash,
		standardDigest(data, fixture.hash), &rsa.PSSOptions{SaltLength: 1})
	if err != nil {
		t.Fatal(err)
	}
	err = signer.Verify(data, shortSalt)
	if !errors.Is(err, tksign.ErrVerification) {
		t.Fatal("PSS verifier accepted an unexpected salt length")
	}
	pkcs1Verifier, err := tksign.New(tksign.WithPublicKeyDER(fixture.publicDER))
	if err != nil {
		t.Fatal(err)
	}
	pss, err := signer.Sign(data)
	if err != nil {
		t.Fatal(err)
	}
	err = pkcs1Verifier.Verify(data, pss)
	if !errors.Is(err, tksign.ErrVerification) {
		t.Fatal("PKCS#1 v1.5 verifier accepted PSS signature")
	}
}

func TestStringEncodings(t *testing.T) {
	fixture := fixturesFor(t)[4]
	data := "编码测试\x00message"
	for _, encoding := range []tksign.Encoding{tksign.EncodingBase64, tksign.EncodingBase64URL, tksign.EncodingHex} {
		t.Run(string(encoding), func(t *testing.T) {
			signer, err := tksign.New(tksign.WithPrivateKeyDER(fixture.privateDER), tksign.WithEncoding(encoding))
			if err != nil {
				t.Fatal(err)
			}
			signature, err := signer.SignString(data)
			if err != nil {
				t.Fatal(err)
			}
			err = signer.VerifyString(data, signature)
			if err != nil {
				t.Fatal(err)
			}
			raw := ed25519.Sign(fixture.key.(ed25519.PrivateKey), []byte(data))
			encodings := map[tksign.Encoding]string{
				tksign.EncodingBase64:    base64.StdEncoding.EncodeToString(raw),
				tksign.EncodingBase64URL: base64.RawURLEncoding.EncodeToString(raw),
				tksign.EncodingHex:       hex.EncodeToString(raw),
			}
			if signature != encodings[encoding] {
				t.Fatal("unexpected signature encoding")
			}
			for other, value := range encodings {
				if other == encoding {
					continue
				}
				err = signer.VerifyString(data, value)
				if !errors.Is(err, tksign.ErrVerification) {
					t.Fatal("verifier tried another encoding")
				}
			}
			for _, invalid := range []string{"", "!invalid!", "a"} {
				err = signer.VerifyString(data, invalid)
				if !errors.Is(err, tksign.ErrVerification) {
					t.Fatal("invalid encoded signature was accepted")
				}
			}
		})
	}
	defaultSigner, err := tksign.New(tksign.WithPrivateKeyDER(fixture.privateDER))
	if err != nil {
		t.Fatal(err)
	}
	signature, err := defaultSigner.SignString("")
	if err != nil {
		t.Fatal(err)
	}
	raw, err := base64.StdEncoding.DecodeString(signature)
	if err != nil || !ed25519.Verify(fixture.key.Public().(ed25519.PublicKey), nil, raw) {
		t.Fatal("default encoding or empty string signing is incorrect")
	}
}

func TestMissingKeysAndZeroValues(t *testing.T) {
	fixture := fixturesFor(t)[0]
	publicOnly, err := tksign.New(tksign.WithPublicKeyDER(fixture.publicDER))
	if err != nil {
		t.Fatal(err)
	}
	_, err = publicOnly.Sign(nil)
	if !errors.Is(err, tksign.ErrMissingPrivateKey) {
		t.Fatal("public-only instance accepted signing")
	}
	_, err = publicOnly.SignString("message")
	if !errors.Is(err, tksign.ErrMissingPrivateKey) {
		t.Fatal("public-only instance accepted string signing")
	}
	for _, instance := range []*tksign.Signer{nil, {}} {
		_, err = instance.Sign(nil)
		if !errors.Is(err, tksign.ErrMissingPrivateKey) {
			t.Fatal("zero instance accepted signing")
		}
		_, err = instance.SignString("")
		if !errors.Is(err, tksign.ErrMissingPrivateKey) {
			t.Fatal("zero instance accepted string signing")
		}
		err = instance.Verify(nil, nil)
		if !errors.Is(err, tksign.ErrMissingPublicKey) {
			t.Fatal("zero instance accepted verification")
		}
		err = instance.VerifyString("", "")
		if !errors.Is(err, tksign.ErrMissingPublicKey) {
			t.Fatal("zero instance accepted string verification")
		}
	}
}

func TestConcurrentReuse(t *testing.T) {
	for _, fixture := range fixturesFor(t) {
		t.Run(fixture.name, func(t *testing.T) {
			option := tksign.WithPrivateKeyDER(fixture.privateDER)
			signer, err := tksign.New(option)
			if err != nil {
				t.Fatal(err)
			}
			var workers sync.WaitGroup
			for range 12 {
				workers.Go(func() {
					signature, signErr := signer.SignString("shared instance")
					if signErr != nil {
						t.Error(signErr)
						return
					}
					verifyErr := signer.VerifyString("shared instance", signature)
					if verifyErr != nil {
						t.Error(verifyErr)
					}
					other, createErr := tksign.New(option)
					if createErr != nil {
						t.Error(createErr)
						return
					}
					verifyErr = other.VerifyString("shared instance", signature)
					if verifyErr != nil {
						t.Error(verifyErr)
					}
				})
			}
			workers.Wait()
		})
	}
}
