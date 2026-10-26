package types_test

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/saicon-network/saicon/chain/x/compliance/types"
)

func TestParamsValidate(t *testing.T) {
	require.NoError(t, types.DefaultParams().Validate())
	require.Equal(t, types.DefaultUnjailCooldownBlocks, types.DefaultParams().UnjailCooldownBlocks)

	require.NoError(t, types.NewParams(0).Validate())

	bad := types.Params{UnjailCooldownBlocks: -1}
	require.Error(t, bad.Validate())
}
