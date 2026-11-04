package types_test

import (
	"bytes"
	"encoding/hex"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/saicon-network/saicon/chain/x/entityregistry/types"
)

const (
	// rfcKey19 is the public key of RFC 9381 Appendix B.4 example 19.
	rfcKey19 = "d75a980182b10ab7d54bfed3c964073a0ee172f3daa62325af021a68f707511a"
	// rfcKey20 and rfcKey21 are the keys of examples 20 and 21.
	rfcKey20 = "3d4017c3e843895a92b70aa74d1b7ebc9c982ccf2ec4968cc0cd55f12af4660c"
	rfcKey21 = "fc51cd8e6218a1a38da47ed00230f0580816ed13ba3303ac5deb911548908025"
	// identityKeyHex encodes the identity point (small order).
	identityKeyHex = "01" + "00000000000000000000000000000000000000000000000000000000000000"
)

func TestDecodeVrfPublicKey(t *testing.T) {
	pk, err := types.DecodeVrfPublicKey(rfcKey19)
	require.NoError(t, err)
	want, err := hex.DecodeString(rfcKey19)
	require.NoError(t, err)
	require.Equal(t, want, pk)

	for name, in := range map[string]string{
		"uppercase":    strings.ToUpper(rfcKey19),
		"62 chars":     rfcKey19[:62],
		"66 chars":     rfcKey19 + "00",
		"empty":        "",
		"not hex":      strings.Repeat("zz", 32),
		"identity":     identityKeyHex,
		"0x prefix":    "0x" + rfcKey19[2:],
		"whitespace":   " " + rfcKey19[1:],
		"mixed case":   "D7" + rfcKey19[2:],
		"all zero key": strings.Repeat("00", 32),
	} {
		t.Run(name, func(t *testing.T) {
			_, err := types.DecodeVrfPublicKey(in)
			require.ErrorIs(t, err, types.ErrInvalidVrfKey)
		})
	}
}

func TestDecodeProofOfPossession(t *testing.T) {
	good := strings.Repeat("ab", 80)
	pop, err := types.DecodeProofOfPossession(good)
	require.NoError(t, err)
	require.Len(t, pop, 80)

	for name, in := range map[string]string{
		"158 chars": good[:158],
		"162 chars": good + "ab",
		"uppercase": strings.ToUpper(good),
		"not hex":   strings.Repeat("zz", 80),
		"empty":     "",
	} {
		t.Run(name, func(t *testing.T) {
			_, err := types.DecodeProofOfPossession(in)
			require.ErrorIs(t, err, types.ErrInvalidProofOfPossession)
		})
	}
}

func TestPopAlphaIsBoundToChainValidatorAndKey(t *testing.T) {
	pk, err := types.DecodeVrfPublicKey(rfcKey19)
	require.NoError(t, err)
	pk2, err := types.DecodeVrfPublicKey(rfcKey20)
	require.NoError(t, err)

	base := types.PopAlpha("saicon-devnet-1", valoper(1), pk)
	require.True(t, bytes.HasPrefix(base, []byte(types.PopDomain)))
	require.Equal(t, "saicon/entityregistry/vrf-pop/v1", types.PopDomain)

	var want []byte
	want = append(want, types.PopDomain...)
	want = append(want, 0x00, 0x0f)
	want = append(want, "saicon-devnet-1"...)
	want = append(want, 0x00, 0x14)
	want = append(want, valoper(1)...)
	want = append(want, pk...)
	require.Equal(t, want, base)

	require.NotEqual(t, base, types.PopAlpha("saicon-devnet-2", valoper(1), pk))
	require.NotEqual(t, base, types.PopAlpha("saicon-devnet-1", valoper(2), pk))
	require.NotEqual(t, base, types.PopAlpha("saicon-devnet-1", valoper(1), pk2))
}
