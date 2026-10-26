package keeper_test

import (
	"testing"

	sdk "github.com/cosmos/cosmos-sdk/types"
	authtypes "github.com/cosmos/cosmos-sdk/x/auth/types"
	"github.com/stretchr/testify/require"

	"github.com/saicon-network/saicon/chain/x/stakeengine/keeper"
	"github.com/saicon-network/saicon/chain/x/stakeengine/types"
)

func TestMsgEnqueueAndCancel(t *testing.T) {
	f := newFixture(t)
	f.caps(t)
	ms := keeper.NewMsgServerImpl(f.k)

	_, err := ms.EnqueueDelegation(f.ctx, &types.MsgEnqueueDelegation{
		Delegator: acc(1).String(), Validator: val(1).String(), Amount: sdk.NewCoin("uatom", nscn(10)),
	})
	require.ErrorIs(t, err, types.ErrInvalidDenom)

	res, err := ms.EnqueueDelegation(f.ctx, &types.MsgEnqueueDelegation{
		Delegator: acc(1).String(), Validator: val(1).String(), Amount: sdk.NewCoin(types.BondDenom, nscn(10)),
	})
	require.NoError(t, err)

	out, err := ms.CancelQueuedDelegation(f.ctx, &types.MsgCancelQueuedDelegation{Delegator: acc(1).String(), Id: res.Id})
	require.NoError(t, err)
	require.Equal(t, sdk.NewCoin(types.BondDenom, nscn(10)), out.Returned)
}

func TestMsgUpdateParams(t *testing.T) {
	f := newFixture(t)
	ms := keeper.NewMsgServerImpl(f.k)
	gov := authtypes.NewModuleAddress(types.GovModuleName).String()

	p := types.DefaultParams()
	p.ValidatorCapFloor = nscn(2_000_000)
	_, err := ms.UpdateParams(f.ctx, &types.MsgUpdateParams{Authority: acc(1).String(), Params: p})
	require.ErrorIs(t, err, types.ErrInvalidSigner)

	bad := p
	bad.MaxSupplyFraction = types.DefaultParams().MaxSupplyFraction.MulInt64(2) // 0.16, above the 0.10 bound
	_, err = ms.UpdateParams(f.ctx, &types.MsgUpdateParams{Authority: gov, Params: bad})
	require.Error(t, err)

	_, err = ms.UpdateParams(f.ctx, &types.MsgUpdateParams{Authority: gov, Params: p})
	require.NoError(t, err)
	got, err := f.k.Params.Get(f.ctx)
	require.NoError(t, err)
	require.Equal(t, nscn(2_000_000), got.ValidatorCapFloor)
}
