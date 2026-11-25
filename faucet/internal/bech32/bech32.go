// Package bech32 validates Cosmos account addresses without pulling in the
// Cosmos SDK. Only decoding and checksum verification are implemented.
package bech32

import (
	"errors"
	"fmt"
	"strings"
)

const charset = "qpzry9x8gf2tvdw0s3jn54khce6mua7l"

var gen = [5]uint32{0x3b6a57b2, 0x26508e6d, 0x1ea119fa, 0x3d4233dd, 0x2a1462b3}

func polymod(values []byte) uint32 {
	chk := uint32(1)
	for _, v := range values {
		top := chk >> 25
		chk = (chk&0x1ffffff)<<5 ^ uint32(v)
		for i := 0; i < 5; i++ {
			if (top>>uint(i))&1 == 1 {
				chk ^= gen[i]
			}
		}
	}
	return chk
}

func hrpExpand(hrp string) []byte {
	out := make([]byte, 0, len(hrp)*2+1)
	for i := 0; i < len(hrp); i++ {
		out = append(out, hrp[i]>>5)
	}
	out = append(out, 0)
	for i := 0; i < len(hrp); i++ {
		out = append(out, hrp[i]&31)
	}
	return out
}

// Decode parses a classic bech32 string (BIP-173, not bech32m, which the SDK
// does not use) and returns its human-readable part and 5-bit data groups
// without the checksum.
func Decode(s string) (string, []byte, error) {
	if len(s) < 8 || len(s) > 1023 {
		return "", nil, errors.New("bad length")
	}
	if s != strings.ToLower(s) && s != strings.ToUpper(s) {
		return "", nil, errors.New("mixed case")
	}
	s = strings.ToLower(s)
	pos := strings.LastIndexByte(s, '1')
	if pos < 1 || pos+7 > len(s) {
		return "", nil, errors.New("bad separator position")
	}
	hrp := s[:pos]
	for i := 0; i < len(hrp); i++ {
		if hrp[i] < 33 || hrp[i] > 126 {
			return "", nil, errors.New("bad character in prefix")
		}
	}
	data := make([]byte, 0, len(s)-pos-1)
	for i := pos + 1; i < len(s); i++ {
		c := strings.IndexByte(charset, s[i])
		if c < 0 {
			return "", nil, fmt.Errorf("invalid character %q", s[i])
		}
		data = append(data, byte(c))
	}
	if polymod(append(hrpExpand(hrp), data...)) != 1 {
		return "", nil, errors.New("bad checksum")
	}
	return hrp, data[:len(data)-6], nil
}

// Encode builds a bech32 string; used by tests and by nothing at runtime.
func Encode(hrp string, data []byte) string {
	values := append(hrpExpand(hrp), data...)
	values = append(values, 0, 0, 0, 0, 0, 0)
	mod := polymod(values) ^ 1
	var b strings.Builder
	b.WriteString(hrp)
	b.WriteByte('1')
	for _, d := range data {
		b.WriteByte(charset[d])
	}
	for i := 0; i < 6; i++ {
		b.WriteByte(charset[(mod>>uint(5*(5-i)))&31])
	}
	return b.String()
}

// convertBits regroups 5-bit values into bytes, rejecting non-zero padding.
func convertBits(data []byte) ([]byte, error) {
	var acc, bits uint32
	var out []byte
	for _, v := range data {
		acc = acc<<5 | uint32(v)
		bits += 5
		for bits >= 8 {
			bits -= 8
			out = append(out, byte(acc>>bits&0xff))
		}
	}
	if bits >= 5 || (acc<<(8-bits))&0xff != 0 {
		return nil, errors.New("bad padding")
	}
	return out, nil
}

// ErrWrongPrefix reports a well-formed bech32 address with another prefix
// (a validator operator address, another chain's address).
var ErrWrongPrefix = errors.New("wrong address prefix")

// ValidateAccount checks that addr is a bech32 account address with the given
// prefix and a 20- or 32-byte payload (the two sizes the SDK produces for
// accounts). It returns the canonical lower-case form. A validator operator
// address (saiconvaloper...) fails on the prefix.
func ValidateAccount(addr, prefix string) (string, error) {
	hrp, data, err := Decode(strings.TrimSpace(addr))
	if err != nil {
		return "", fmt.Errorf("not a valid bech32 address: %w", err)
	}
	if hrp != prefix {
		return "", fmt.Errorf("%w: want %q, got %q", ErrWrongPrefix, prefix+"1", hrp+"1")
	}
	raw, err := convertBits(data)
	if err != nil {
		return "", fmt.Errorf("not a valid bech32 address: %w", err)
	}
	if len(raw) != 20 && len(raw) != 32 {
		return "", fmt.Errorf("unexpected address length %d bytes", len(raw))
	}
	return strings.ToLower(strings.TrimSpace(addr)), nil
}
