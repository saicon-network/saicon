package bech32

import (
	"bytes"
	"strings"
	"testing"
)

func mk(hrp string, n int) string {
	raw := bytes.Repeat([]byte{0xAB}, n)
	// regroup bytes to 5-bit
	var data []byte
	var acc, bits uint32
	for _, b := range raw {
		acc = acc<<8 | uint32(b)
		bits += 8
		for bits >= 5 {
			bits -= 5
			data = append(data, byte(acc>>bits&31))
		}
	}
	if bits > 0 {
		data = append(data, byte(acc<<(5-bits)&31))
	}
	return Encode(hrp, data)
}

func TestBIP173Vector(t *testing.T) {
	// Valid vector from BIP-173.
	if _, _, err := Decode("A12UEL5L"); err != nil {
		t.Fatal(err)
	}
	if _, _, err := Decode("A12UEL5M"); err == nil {
		t.Fatal("bad checksum accepted")
	}
}

func TestValidateAccount(t *testing.T) {
	good := mk("saicon", 20)
	if got, err := ValidateAccount(good, "saicon"); err != nil || got != good {
		t.Fatalf("good rejected: %v", err)
	}
	if _, err := ValidateAccount(strings.ToUpper(good), "saicon"); err != nil {
		t.Fatalf("uppercase should canonicalise: %v", err)
	}
	if _, err := ValidateAccount(mk("saicon", 32), "saicon"); err != nil {
		t.Fatalf("32-byte rejected: %v", err)
	}
	cases := map[string]string{
		"wrong prefix":      mk("cosmos", 20),
		"validator address": mk("saiconvaloper", 20),
		"bad length":        mk("saicon", 7),
		"empty":             "",
		"garbage":           "hello",
		"mixed case":        good[:10] + strings.ToUpper(good[10:12]) + good[12:],
	}
	for name, a := range cases {
		if _, err := ValidateAccount(a, "saicon"); err == nil {
			t.Errorf("%s: accepted %q", name, a)
		}
	}
	// bad checksum: flip last char
	bad := good[:len(good)-1] + "q"
	if good[len(good)-1] == 'q' {
		bad = good[:len(good)-1] + "p"
	}
	if _, err := ValidateAccount(bad, "saicon"); err == nil {
		t.Error("bad checksum accepted")
	}
}
