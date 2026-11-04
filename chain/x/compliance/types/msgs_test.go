package types_test

import (
	"strings"
	"testing"

	"cosmossdk.io/math"
	"github.com/stretchr/testify/require"

	"github.com/saicon-network/saicon/chain/x/compliance/types"
)

func validFire() *types.MsgFireCoordinatedSanction {
	return &types.MsgFireCoordinatedSanction{
		Authority:      "saicon10d07y265gmmuvt4z0w9aw880jnsr700j6zn9kn",
		EntityId:       "acme",
		EvidenceHashes: []string{hashOf("v1"), hashOf("v2")},
		Fraction:       math.LegacyNewDecWithPrec(30, 2),
		Reason:         "coordinated double-sign",
	}
}

func TestMsgFireCoordinatedSanctionValidateBasic(t *testing.T) {
	require.NoError(t, validFire().ValidateBasic())

	// The bounds are inclusive and the reason may be exactly 256 bytes.
	m := validFire()
	m.Fraction = math.LegacyNewDecWithPrec(10, 2)
	m.Reason = strings.Repeat("r", 256)
	require.NoError(t, m.ValidateBasic())
	m.Fraction = math.LegacyOneDec()
	m.Reason = ""
	require.NoError(t, m.ValidateBasic())

	cases := []struct {
		name  string
		edit  func(m *types.MsgFireCoordinatedSanction)
		wantE error
	}{
		{"empty authority", func(m *types.MsgFireCoordinatedSanction) { m.Authority = "" }, types.ErrInvalidSanction},
		{"entity id too short", func(m *types.MsgFireCoordinatedSanction) { m.EntityId = "A" }, types.ErrInvalidSanction},
		{"no hashes", func(m *types.MsgFireCoordinatedSanction) { m.EvidenceHashes = nil }, types.ErrInvalidSanction},
		{"17 hashes", func(m *types.MsgFireCoordinatedSanction) {
			m.EvidenceHashes = nil
			for i := 0; i < 17; i++ {
				m.EvidenceHashes = append(m.EvidenceHashes, hashOf(string(rune('a'+i))))
			}
		}, types.ErrInvalidSanction},
		{"lowercase hash", func(m *types.MsgFireCoordinatedSanction) {
			m.EvidenceHashes[1] = strings.ToLower(m.EvidenceHashes[1])
		}, types.ErrInvalidEvidenceHash},
		{"short hash", func(m *types.MsgFireCoordinatedSanction) { m.EvidenceHashes[0] = "ABCD" }, types.ErrInvalidEvidenceHash},
		{"duplicate hash", func(m *types.MsgFireCoordinatedSanction) {
			m.EvidenceHashes[1] = m.EvidenceHashes[0]
		}, types.ErrInvalidEvidenceHash},
		{"nil fraction", func(m *types.MsgFireCoordinatedSanction) { m.Fraction = math.LegacyDec{} }, types.ErrInvalidFraction},
		{"fraction 0.09", func(m *types.MsgFireCoordinatedSanction) { m.Fraction = math.LegacyNewDecWithPrec(9, 2) }, types.ErrInvalidFraction},
		{"fraction 1.01", func(m *types.MsgFireCoordinatedSanction) { m.Fraction = math.LegacyNewDecWithPrec(101, 2) }, types.ErrInvalidFraction},
		{"reason of 257 bytes", func(m *types.MsgFireCoordinatedSanction) { m.Reason = strings.Repeat("r", 257) }, types.ErrInvalidSanction},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			m := validFire()
			tc.edit(m)
			require.ErrorIs(t, m.ValidateBasic(), tc.wantE)
		})
	}
}
