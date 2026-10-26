package keeper_test

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/saicon-network/saicon/chain/x/rewards/keeper"
	"github.com/saicon-network/saicon/chain/x/rewards/types"
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
			// Governance may tune the emission params, but it may not submit a set
			// with the budget rates unset: that would leave the annual security
			// budget undefined. The keeper degrades such a set to zero emission
			// rather than panicking in EndBlock, but the message is the place to
			// refuse it outright. (This case previously expected success, from
			// when Params carried no values that could be nonsensical.)
			name: "params with unset budget rates are rejected",
			input: &types.MsgUpdateParams{
				Authority: authorityStr,
				Params:    types.Params{},
			},
			expErr:    true,
			expErrMsg: "budget_bonded_rate is not set",
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
