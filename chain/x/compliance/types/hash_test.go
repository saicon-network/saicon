package types_test

import (
	"crypto/sha256"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/saicon-network/saicon/chain/x/compliance/types"
)

func TestFormatEvidenceHashIsUppercaseHex(t *testing.T) {
	sum := sha256.Sum256([]byte("an equivocation"))
	s := types.FormatEvidenceHash(sum[:])
	require.Len(t, s, 64)
	require.Equal(t, strings.ToUpper(s), s)
	require.Regexp(t, "^[0-9A-F]{64}$", s)
}

func TestParseEvidenceHashRoundTrips(t *testing.T) {
	sum := sha256.Sum256([]byte("another equivocation"))
	b, err := types.ParseEvidenceHash(types.FormatEvidenceHash(sum[:]))
	require.NoError(t, err)
	require.Equal(t, sum[:], b)
}

func TestParseEvidenceHashRefusesMalformedInput(t *testing.T) {
	sum := sha256.Sum256([]byte("x"))
	good := types.FormatEvidenceHash(sum[:])
	for name, s := range map[string]string{
		"lowercase": strings.ToLower(good),
		"63 chars":  good[:63],
		"65 chars":  good + "A",
		"non-hex":   good[:63] + "G",
		"empty":     "",
		"0x prefix": "0X" + good[2:],
		"spaces":    " " + good[1:],
	} {
		_, err := types.ParseEvidenceHash(s)
		require.ErrorIs(t, err, types.ErrInvalidEvidenceHash, name)
	}
}
