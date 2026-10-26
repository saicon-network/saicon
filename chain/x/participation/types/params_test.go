package types_test

import (
	"testing"

	"cosmossdk.io/math"
	"github.com/stretchr/testify/require"

	"github.com/saicon-network/saicon/chain/x/participation/types"
)

func TestParams_Validate_EpDecay(t *testing.T) {
	require.NoError(t, types.NewParams(100, math.LegacyZeroDec()).Validate())
	require.NoError(t, types.NewParams(100, math.LegacyMustNewDecFromStr("0.5")).Validate())
	require.ErrorContains(t, types.NewParams(100, math.LegacyOneDec()).Validate(), "ep_decay must be in [0,1)")
	require.ErrorContains(t, types.NewParams(100, math.LegacyMustNewDecFromStr("-0.1")).Validate(), "ep_decay must be in [0,1)")
	require.ErrorContains(t, types.Params{}.Validate(), "ep_decay must not be nil")
}
