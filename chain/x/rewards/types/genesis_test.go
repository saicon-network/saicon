package types_test

import (
	"testing"

	"github.com/saicon-network/saicon/chain/x/rewards/types"
	"github.com/stretchr/testify/require"
)

func TestGenesisState_Validate(t *testing.T) {
	tests := []struct {
		desc     string
		genState *types.GenesisState
		valid    bool
	}{
		{
			desc:     "default is valid",
			genState: types.DefaultGenesis(),
			valid:    true,
		},
		{
			desc: "valid genesis state",
			// A real genesis, not a zero value: emission params without the budget
			// rates cannot fund anything, and Validate now says so.
			genState: types.DefaultGenesis(),
			valid:    true,
		},
		{
			desc:     "params with unset budget rates are rejected",
			genState: &types.GenesisState{},
			valid:    false,
		},
	}
	for _, tc := range tests {
		t.Run(tc.desc, func(t *testing.T) {
			err := tc.genState.Validate()
			if tc.valid {
				require.NoError(t, err)
			} else {
				require.Error(t, err)
			}
		})
	}
}
