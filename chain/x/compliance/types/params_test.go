package types_test

import (
	"testing"

	"cosmossdk.io/math"
	"github.com/stretchr/testify/require"

	"github.com/saicon-network/saicon/chain/x/compliance/types"
)

func TestParamsValidate(t *testing.T) {
	require.NoError(t, types.DefaultParams().Validate())
	require.Equal(t, types.DefaultUnjailCooldownBlocks, types.DefaultParams().UnjailCooldownBlocks)

	require.NoError(t, types.NewParams(0).Validate())

	bad := types.DefaultParams()
	bad.UnjailCooldownBlocks = -1
	require.Error(t, bad.Validate())
}

func TestDefaultParams(t *testing.T) {
	p := types.DefaultParams()
	require.Equal(t, int64(43200), p.UnjailCooldownBlocks)
	require.Equal(t, "0.300000000000000000", p.CoordinatedAttackFraction.String())
	require.Equal(t, uint64(604800), p.AppealWindowSeconds)
	require.NoError(t, p.Validate())
}

func TestCoordinatedFractionBounds(t *testing.T) {
	require.Equal(t, "0.100000000000000000", types.CoordinatedFractionFloor().String())
	require.Equal(t, "1.000000000000000000", types.CoordinatedFractionCeiling().String())

	for _, ok := range []string{"0.10", "0.30", "1.00", "0.100000000000000001", "0.999999999999999999"} {
		require.NoError(t, types.ValidateCoordinatedFraction(math.LegacyMustNewDecFromStr(ok)), ok)
	}
	for _, bad := range []string{"0.099999999999999999", "1.000000000000000001", "0", "-0.30", "-1"} {
		err := types.ValidateCoordinatedFraction(math.LegacyMustNewDecFromStr(bad))
		require.ErrorIs(t, err, types.ErrInvalidFraction, bad)
	}
	require.ErrorIs(t, types.ValidateCoordinatedFraction(math.LegacyDec{}), types.ErrInvalidFraction, "nil")

	// Params.Validate applies the same bounds.
	p := types.DefaultParams()
	p.CoordinatedAttackFraction = math.LegacyMustNewDecFromStr("0.099999999999999999")
	require.ErrorIs(t, p.Validate(), types.ErrInvalidFraction)
	p.CoordinatedAttackFraction = math.LegacyMustNewDecFromStr("1.000000000000000001")
	require.ErrorIs(t, p.Validate(), types.ErrInvalidFraction)
	p.CoordinatedAttackFraction = math.LegacyMustNewDecFromStr("0.10")
	require.NoError(t, p.Validate())
	p.CoordinatedAttackFraction = math.LegacyOneDec()
	require.NoError(t, p.Validate())
}

func TestAppealWindowBounds(t *testing.T) {
	require.Equal(t, uint64(86_400), types.MinAppealWindowSeconds)
	require.Equal(t, uint64(1_209_600), types.MaxAppealWindowSeconds)
	require.Equal(t, uint64(604_800), types.DefaultAppealWindowSeconds)

	p := types.DefaultParams()
	for _, ok := range []uint64{86_400, 604_800, 1_209_600} {
		p.AppealWindowSeconds = ok
		require.NoError(t, p.Validate(), ok)
	}
	for _, bad := range []uint64{0, 86_399, 1_209_601} {
		p.AppealWindowSeconds = bad
		require.ErrorIs(t, p.Validate(), types.ErrInvalidParams, bad)
	}
}

func TestNewParamsKeepsTheDefaults(t *testing.T) {
	p := types.NewParams(7)
	require.Equal(t, int64(7), p.UnjailCooldownBlocks)
	require.Equal(t, "0.300000000000000000", p.CoordinatedAttackFraction.String())
	require.Equal(t, uint64(604_800), p.AppealWindowSeconds)
}
