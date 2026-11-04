package vrf

import (
	"bytes"
	"encoding/hex"
	"errors"
	"strings"
	"testing"

	"github.com/oasisprotocol/curve25519-voi/primitives/ed25519"
)

// rfc9381 holds the ECVRF-EDWARDS25519-SHA512-ELL2 vectors of RFC 9381
// Appendix B.4 (Examples 19-21).
var rfc9381 = []struct{ name, sk, pk, alpha, pi, beta string }{
	{"example 19",
		"9d61b19deffd5a60ba844af492ec2cc44449c5697b326919703bac031cae7f60",
		"d75a980182b10ab7d54bfed3c964073a0ee172f3daa62325af021a68f707511a",
		"",
		"7d9c633ffeee27349264cf5c667579fc583b4bda63ab71d001f89c10003ab46f14adf9a3cd8b8412d9038531e865c341cafa73589b023d14311c331a9ad15ff2fb37831e00f0acaa6d73bc9997b06501",
		"9d574bf9b8302ec0fc1e21c3ec5368269527b87b462ce36dab2d14ccf80c53cccf6758f058c5b1c856b116388152bbe509ee3b9ecfe63d93c3b4346c1fbc6c54"},
	{"example 20",
		"4ccd089b28ff96da9db6c346ec114e0f5b8a319f35aba624da8cf6ed4fb8a6fb",
		"3d4017c3e843895a92b70aa74d1b7ebc9c982ccf2ec4968cc0cd55f12af4660c",
		"72",
		"47b327393ff2dd81336f8a2ef10339112401253b3c714eeda879f12c509072ef055b48372bb82efbdce8e10c8cb9a2f9d60e93908f93df1623ad78a86a028d6bc064dbfc75a6a57379ef855dc6733801",
		"38561d6b77b71d30eb97a062168ae12b667ce5c28caccdf76bc88e093e4635987cd96814ce55b4689b3dd2947f80e59aac7b7675f8083865b46c89b2ce9cc735"},
	{"example 21",
		"c5aa8df43f9f837bedb7442f31dcb7b166d38535076f094b85ce3a2e0b4458f7",
		"fc51cd8e6218a1a38da47ed00230f0580816ed13ba3303ac5deb911548908025",
		"af82",
		"926e895d308f5e328e7aa159c06eddbe56d06846abf5d98c2512235eaa57fdce35b46edfc655bc828d44ad09d1150f31374e7ef73027e14760d42e77341fe05467bb286cc2c9d7fde29120a0b2320d04",
		"121b7f9b9aaaa29099fc04a94ba52784d44eac976dd1a3cca458733be5cd090a7b5fbd148444f17f8daf1fb55cb04b1ae85a626e30a54b4b0f8abf4a43314a58"},
}

const (
	// identityKey encodes the identity point (small order).
	identityKey = "01" + "00000000000000000000000000000000000000000000000000000000000000"
	// yEqualsPKey encodes y = 2^255 - 19 = p: non-canonical, and it also decodes
	// as y = 0, a point of small order, so it is refused even if either check is deleted.
	yEqualsPKey = "ed" + "ffffffffffffffffffffffffffffffffffffffffffffffffffffffffffff" + "7f"
	// nonCanonicalKey encodes y = p + 3 (not reduced mod p). It decodes to a
	// point of large order, so only the canonical-encoding check refuses it.
	nonCanonicalKey = "f0" + "ffffffffffffffffffffffffffffffffffffffffffffffffffffffffffff" + "7f"
)

func unhex(t testing.TB, s string) []byte {
	t.Helper()
	b, err := hex.DecodeString(s)
	if err != nil {
		t.Fatalf("bad hex %q: %v", s, err)
	}
	return b
}

func TestRFC9381Vectors(t *testing.T) {
	for _, v := range rfc9381 {
		t.Run(v.name, func(t *testing.T) {
			k, err := NewKeyFromSeed(unhex(t, v.sk))
			if err != nil {
				t.Fatal(err)
			}
			pk, alpha, pi := unhex(t, v.pk), unhex(t, v.alpha), unhex(t, v.pi)
			if !bytes.Equal(k.PublicKey(), pk) {
				t.Fatalf("public key %x, want %x", k.PublicKey(), pk)
			}
			if got := k.Prove(alpha); !bytes.Equal(got, pi) {
				t.Fatalf("proof %x, want %x", got, pi)
			}
			beta, err := Verify(pk, pi, alpha)
			if err != nil {
				t.Fatal(err)
			}
			if !bytes.Equal(beta, unhex(t, v.beta)) {
				t.Fatalf("output %x, want %s", beta, v.beta)
			}
		})
	}
}

func TestFixtureKeysAre32Bytes(t *testing.T) {
	for _, h := range []string{identityKey, yEqualsPKey, nonCanonicalKey} {
		if n := len(unhex(t, h)); n != PublicKeySize {
			t.Fatalf("fixture %s is %d bytes, want %d", h, n, PublicKeySize)
		}
	}
}

func TestVerifyRefuses(t *testing.T) {
	v := rfc9381[0]
	pk, pi := unhex(t, v.pk), unhex(t, v.pi)
	flip := func(i int) []byte {
		p := bytes.Clone(pi)
		p[i] ^= 0x01
		return p
	}
	// sNotReduced sets the top byte of s so that s >= q.
	sNotReduced := bytes.Clone(pi)
	sNotReduced[79] |= 0xf0
	// badGamma replaces Gamma with a non-canonical point encoding.
	badGamma := append(unhex(t, yEqualsPKey), pi[32:]...)
	cases := []struct {
		name            string
		pk, proof, alph []byte
		want            error
	}{
		{"proof byte 0 flipped", pk, flip(0), nil, ErrInvalidProof},
		{"proof byte 79 flipped", pk, flip(79), nil, ErrInvalidProof},
		{"other alpha", pk, pi, []byte{0x00}, ErrInvalidProof},
		{"79-byte proof", pk, pi[:79], nil, ErrInvalidProof},
		{"81-byte proof", pk, append(bytes.Clone(pi), 0x00), nil, ErrInvalidProof},
		{"nil proof", pk, nil, nil, ErrInvalidProof},
		{"s not reduced", pk, sNotReduced, nil, ErrInvalidProof},
		{"bad Gamma encoding", pk, badGamma, nil, ErrInvalidProof},
		{"identity key", unhex(t, identityKey), pi, nil, ErrInvalidPublicKey},
		{"y = p key", unhex(t, yEqualsPKey), pi, nil, ErrInvalidPublicKey},
		{"non-canonical key", unhex(t, nonCanonicalKey), pi, nil, ErrInvalidPublicKey},
		{"31-byte key", pk[:31], pi, nil, ErrInvalidPublicKey},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			out, err := Verify(c.pk, c.proof, c.alph)
			if !errors.Is(err, c.want) {
				t.Fatalf("err = %v, want %v", err, c.want)
			}
			if out != nil {
				t.Fatalf("output %x on error", out)
			}
		})
	}
}

func TestValidatePublicKey(t *testing.T) {
	for _, v := range rfc9381 {
		if err := ValidatePublicKey(unhex(t, v.pk)); err != nil {
			t.Errorf("%s: %v", v.name, err)
		}
	}
	for _, c := range []struct {
		name   string
		pk     []byte
		reason string
	}{
		{"identity", unhex(t, identityKey), "small-order"},
		{"y = p", unhex(t, yEqualsPKey), "non-canonical"},
		{"non-canonical", unhex(t, nonCanonicalKey), "non-canonical"},
		{"all zero", make([]byte, PublicKeySize), "small-order"},
		{"short", make([]byte, PublicKeySize-1), "31 bytes"},
		{"long", make([]byte, PublicKeySize+1), "33 bytes"},
		{"nil", nil, "0 bytes"},
	} {
		err := ValidatePublicKey(c.pk)
		if !errors.Is(err, ErrInvalidPublicKey) {
			t.Errorf("%s: err = %v, want ErrInvalidPublicKey", c.name, err)
			continue
		}
		if !strings.Contains(err.Error(), c.reason) {
			t.Errorf("%s: err = %q, want reason %q", c.name, err, c.reason)
		}
	}
}

func TestVerifyRecoversAPanic(t *testing.T) {
	orig := ecvrfVerify
	t.Cleanup(func() { ecvrfVerify = orig })
	ecvrfVerify = func(ed25519.PublicKey, []byte, []byte) (bool, []byte) { panic("hash to curve") }

	v := rfc9381[0]
	out, err := Verify(unhex(t, v.pk), unhex(t, v.pi), nil)
	if out != nil {
		t.Fatalf("output %x after a panic", out)
	}
	if !errors.Is(err, ErrInvalidProof) {
		t.Fatalf("err = %v, want ErrInvalidProof", err)
	}
	if !strings.Contains(err.Error(), "hash to curve") {
		t.Fatalf("err %q lost the panic value", err)
	}
}

func TestNewKeyFromSeedRefusesBadLength(t *testing.T) {
	for _, n := range []int{0, SeedSize - 1, SeedSize + 1, 64} {
		if _, err := NewKeyFromSeed(make([]byte, n)); err == nil {
			t.Errorf("%d-byte seed accepted", n)
		}
	}
}

func TestGenerateKeyRoundTrips(t *testing.T) {
	a, err := GenerateKey(nil)
	if err != nil {
		t.Fatal(err)
	}
	b, err := GenerateKey(nil)
	if err != nil {
		t.Fatal(err)
	}
	again, err := NewKeyFromSeed(a.Seed())
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(again.PublicKey(), a.PublicKey()) {
		t.Fatal("the seed does not reproduce the public key")
	}
	if bytes.Equal(a.PublicKey(), b.PublicKey()) {
		t.Fatal("two generated keys are equal")
	}
	alpha := []byte("round 1")
	proof := a.Prove(alpha)
	if _, err := Verify(a.PublicKey(), proof, alpha); err != nil {
		t.Fatal(err)
	}
	if _, err := Verify(b.PublicKey(), proof, alpha); !errors.Is(err, ErrInvalidProof) {
		t.Fatalf("a proof verified under another key: %v", err)
	}
}

func TestAccessorsReturnCopies(t *testing.T) {
	k, err := GenerateKey(nil)
	if err != nil {
		t.Fatal(err)
	}
	pk := bytes.Clone(k.PublicKey())
	k.PublicKey()[0] ^= 0xff
	k.Seed()[0] ^= 0xff
	if !bytes.Equal(k.PublicKey(), pk) {
		t.Fatal("PublicKey exposes the key's storage")
	}
	again, _ := NewKeyFromSeed(k.Seed())
	if !bytes.Equal(again.PublicKey(), pk) {
		t.Fatal("Seed exposes the key's storage")
	}
}
