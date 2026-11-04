package keeper_test

import (
	"testing"

	"cosmossdk.io/math"
	"github.com/stretchr/testify/require"

	"github.com/saicon-network/saicon/chain/x/compliance/keeper"
	"github.com/saicon-network/saicon/chain/x/compliance/types"
)

func TestMsgUpdateParams(t *testing.T) {
	f := initFixture(t)
	ms := keeper.NewMsgServerImpl(f.keeper)

	params := types.DefaultParams()
	require.NoError(t, f.keeper.Params.Set(f.ctx, params))

	authorityStr, err := f.addressCodec.BytesToString(f.keeper.GetAuthority())
	require.NoError(t, err)

	// default params
	testCases := []struct {
		name      string
		input     *types.MsgUpdateParams
		expErr    bool
		expErrMsg string
	}{
		{
			name: "invalid authority",
			input: &types.MsgUpdateParams{
				Authority: "invalid",
				Params:    params,
			},
			expErr:    true,
			expErrMsg: "invalid authority",
		},
		{
			name: "zero params are refused",
			input: &types.MsgUpdateParams{
				Authority: authorityStr,
				Params:    types.Params{},
			},
			expErr:    true,
			expErrMsg: "coordinated-attack fraction out of bounds",
		},
		{
			name: "all good",
			input: &types.MsgUpdateParams{
				Authority: authorityStr,
				Params:    params,
			},
			expErr: false,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := ms.UpdateParams(f.ctx, tc.input)

			if tc.expErr {
				require.Error(t, err)
				require.Contains(t, err.Error(), tc.expErrMsg)
			} else {
				require.NoError(t, err)
			}
		})
	}
}

func TestUpdateParamsRefusesAFractionBelowTheChainsDoubleSignFraction(t *testing.T) {
	f := initFixture(t)
	f.slashing.d = math.LegacyNewDecWithPrec(35, 2)
	ms := keeper.NewMsgServerImpl(f.keeper)

	authorityStr, err := f.addressCodec.BytesToString(f.keeper.GetAuthority())
	require.NoError(t, err)
	before, err := f.keeper.Params.Get(f.ctx)
	require.NoError(t, err)

	below := types.DefaultParams() // 0.30 < 0.35
	_, err = ms.UpdateParams(f.ctx, &types.MsgUpdateParams{Authority: authorityStr, Params: below})
	require.ErrorIs(t, err, types.ErrInvalidFraction)
	after, err := f.keeper.Params.Get(f.ctx)
	require.NoError(t, err)
	require.Equal(t, before, after, "a refused update changes nothing")

	equal := types.DefaultParams()
	equal.CoordinatedAttackFraction = math.LegacyNewDecWithPrec(35, 2)
	_, err = ms.UpdateParams(f.ctx, &types.MsgUpdateParams{Authority: authorityStr, Params: equal})
	require.NoError(t, err)
	after, err = f.keeper.Params.Get(f.ctx)
	require.NoError(t, err)
	require.Equal(t, "0.350000000000000000", after.CoordinatedAttackFraction.String())
}
