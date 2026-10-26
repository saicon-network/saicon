package keeper_test

import (
	"testing"

	sdk "github.com/cosmos/cosmos-sdk/types"
	"github.com/stretchr/testify/require"

	"github.com/saicon-network/saicon/chain/x/validatorregistry/types"
)

func TestSetComplianceFlag(t *testing.T) {
	f := initFixture(t)
	addr := seedValidator(t, f, "cflag", types.Status_STATUS_ACTIVE)

	// default seeded compliance flag is true
	md, _, _ := f.keeper.GetValidatorMetadata(f.ctx, addr)
	require.True(t, md.ComplianceFlag)

	// set to false (non-compliant)
	require.NoError(t, f.keeper.SetComplianceFlag(f.ctx, addr, false))
	md, _, _ = f.keeper.GetValidatorMetadata(f.ctx, addr)
	require.False(t, md.ComplianceFlag)

	// restore to true
	require.NoError(t, f.keeper.SetComplianceFlag(f.ctx, addr, true))
	md, _, _ = f.keeper.GetValidatorMetadata(f.ctx, addr)
	require.True(t, md.ComplianceFlag)
}

func TestSetComplianceFlag_NotFound(t *testing.T) {
	f := initFixture(t)
	ghost := sdk.ValAddress([]byte("ghost-cflag-val-20by"))[:20]

	err := f.keeper.SetComplianceFlag(f.ctx, ghost, false)
	require.Error(t, err)
	require.ErrorIs(t, err, types.ErrValidatorNotFound)
}
