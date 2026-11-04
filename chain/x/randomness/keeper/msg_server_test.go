package keeper_test

import (
	"testing"

	sdk "github.com/cosmos/cosmos-sdk/types"
	sdkerrors "github.com/cosmos/cosmos-sdk/types/errors"
	"github.com/stretchr/testify/require"

	"github.com/saicon-network/saicon/chain/x/randomness/keeper"
	"github.com/saicon-network/saicon/chain/x/randomness/types"
)

func TestMsgSubmitVrf(t *testing.T) {
	rt := newRoundTwo(t)
	srv := keeper.NewMsgServerImpl(rt.k)

	_, err := srv.SubmitVrf(rt.at(31), &types.MsgSubmitVrf{Operator: "not-an-address", Round: 2, Proof: rt.current(rt.k1)})
	require.ErrorIs(t, err, sdkerrors.ErrInvalidAddress)

	res, err := srv.SubmitVrf(rt.at(31), &types.MsgSubmitVrf{Operator: acc(1).String(), Round: 2, Proof: rt.current(rt.k1)})
	require.NoError(t, err)
	s, err := rt.k.Submissions.Get(rt.ctx, "acme")
	require.NoError(t, err)
	require.Equal(t, s.Output, res.Output)
	require.Len(t, res.Output, types.OutputHexLen)

	_, err = srv.SubmitVrf(rt.at(32), &types.MsgSubmitVrf{Operator: acc(1).String(), Round: 2, Proof: rt.current(rt.k1)})
	require.ErrorIs(t, err, types.ErrAlreadySubmitted)
}

func TestMsgUpdateParams(t *testing.T) {
	f := newFixture(t)
	srv := keeper.NewMsgServerImpl(f.k)
	f.run(1, 5)
	before := f.round()
	require.EqualValues(t, 20, before.RotationHeight)
	gov := sdk.AccAddress(f.k.GetAuthority()).String()

	_, err := srv.UpdateParams(f.ctx, &types.MsgUpdateParams{Authority: acc(9).String(), Params: types.DefaultParams()})
	require.ErrorIs(t, err, types.ErrInvalidSigner)

	_, err = srv.UpdateParams(f.ctx, &types.MsgUpdateParams{Authority: "garbage", Params: types.DefaultParams()})
	require.Error(t, err)

	_, err = srv.UpdateParams(f.ctx, &types.MsgUpdateParams{Authority: gov, Params: types.Params{RotationEpochs: 0, WindowBlocks: 100}})
	require.Error(t, err)
	p, err := f.k.Params.Get(f.ctx)
	require.NoError(t, err)
	require.EqualValues(t, 2, p.RotationEpochs, "a refused update changes nothing")

	next := types.Params{RotationEpochs: 5, WindowBlocks: 60}
	_, err = srv.UpdateParams(f.ctx, &types.MsgUpdateParams{Authority: gov, Params: next})
	require.NoError(t, err)
	p, err = f.k.Params.Get(f.ctx)
	require.NoError(t, err)
	require.Equal(t, next, p)
	require.Equal(t, before, f.round(), "the scheduled round keeps its heights")
}
