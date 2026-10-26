package keeper_test

import (
	"testing"

	sdk "github.com/cosmos/cosmos-sdk/types"
	"github.com/stretchr/testify/require"

	"github.com/saicon-network/saicon/chain/x/validatorregistry/keeper"
	"github.com/saicon-network/saicon/chain/x/validatorregistry/types"
)

func TestQueryValidator(t *testing.T) {
	f := initFixture(t)
	qs := keeper.NewQueryServerImpl(f.keeper)

	addr := seedValidator(t, f, "q1", types.Status_STATUS_ACTIVE)

	resp, err := qs.Validator(f.ctx, &types.QueryValidatorRequest{
		ValidatorAddress: sdk.ValAddress(addr).String(),
	})
	require.NoError(t, err)
	require.Equal(t, types.Status_STATUS_ACTIVE, resp.Validator.Status)
}

func TestQueryValidator_NotFound(t *testing.T) {
	f := initFixture(t)
	qs := keeper.NewQueryServerImpl(f.keeper)

	ghost := sdk.ValAddress([]byte("ghost-val-addr-20by!"))[:20]
	_, err := qs.Validator(f.ctx, &types.QueryValidatorRequest{ValidatorAddress: ghost.String()})
	require.Error(t, err)
}

func TestQueryValidators_All(t *testing.T) {
	f := initFixture(t)
	qs := keeper.NewQueryServerImpl(f.keeper)

	seedValidator(t, f, "a1", types.Status_STATUS_ACTIVE)
	seedValidator(t, f, "a2", types.Status_STATUS_BONDING)
	seedValidator(t, f, "a3", types.Status_STATUS_ACTIVE)

	resp, err := qs.Validators(f.ctx, &types.QueryValidatorsRequest{})
	require.NoError(t, err)
	require.Len(t, resp.Validators, 3)
}

func TestQueryValidators_StatusFilter(t *testing.T) {
	f := initFixture(t)
	qs := keeper.NewQueryServerImpl(f.keeper)

	seedValidator(t, f, "b1", types.Status_STATUS_ACTIVE)
	seedValidator(t, f, "b2", types.Status_STATUS_BONDING)
	seedValidator(t, f, "b3", types.Status_STATUS_ACTIVE)

	resp, err := qs.Validators(f.ctx, &types.QueryValidatorsRequest{StatusFilter: "active"})
	require.NoError(t, err)
	require.Len(t, resp.Validators, 2)
	for _, v := range resp.Validators {
		require.Equal(t, types.Status_STATUS_ACTIVE, v.Status)
	}
}

func TestQueryValidators_InvalidFilter(t *testing.T) {
	f := initFixture(t)
	qs := keeper.NewQueryServerImpl(f.keeper)

	_, err := qs.Validators(f.ctx, &types.QueryValidatorsRequest{StatusFilter: "bogus"})
	require.Error(t, err)
}
