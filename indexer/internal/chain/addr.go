package chain

import (
	"crypto/sha256"
	"encoding/base64"
	"fmt"
	"strings"
)

const bech32Charset = "qpzry9x8gf2tvdw0s3jn54khce6mua7l"

// AddrKey reduces a bech32 address to the bytes underneath it, as lower-case hex.
//
// It exists because the same validator wears two addresses: x/staking reports
// `saiconvaloper1…` and the registry's own list reports `saicon1…`. They decode to
// identical bytes and differ only in the prefix — and in the checksum that depends on
// it — so comparing the strings is a join that never matches. Key on this instead.
//
// The checksum is not verified: these strings come from the chain's own responses, and
// the only question being asked is which validator they name.
func AddrKey(addr string) (string, error) {
	i := strings.LastIndex(addr, "1")
	if i < 1 || i+7 > len(addr) {
		return "", fmt.Errorf("not a bech32 address: %q", addr)
	}
	data := addr[i+1:]
	data = data[:len(data)-6] // drop the 6-character checksum; i+7<=len(addr) guarantees len(data)>=6

	var acc, bits uint
	out := make([]byte, 0, len(data)*5/8)
	for _, c := range data {
		v := strings.IndexRune(bech32Charset, c)
		if v < 0 {
			return "", fmt.Errorf("bech32 %q: bad character %q", addr, c)
		}
		acc = acc<<5 | uint(v)
		bits += 5
		for bits >= 8 {
			bits -= 8
			out = append(out, byte(acc>>bits))
		}
	}
	return fmt.Sprintf("%x", out), nil
}

// ConsAddrFromPubKey turns a validator's base64 ed25519 consensus pubkey into the
// consensus address a block header carries: the first 20 bytes of its SHA-256, in
// upper-case hex. Verified against the localnet at height 50.
func ConsAddrFromPubKey(b64 string) (string, error) {
	raw, err := base64.StdEncoding.DecodeString(b64)
	if err != nil {
		return "", fmt.Errorf("consensus pubkey %q: %w", b64, err)
	}
	sum := sha256.Sum256(raw)
	return fmt.Sprintf("%X", sum[:20]), nil
}
