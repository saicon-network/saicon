package types_test

import (
	"testing"

	"github.com/cosmos/cosmos-sdk/types/bech32"
	"github.com/saicon-network/saicon/chain/x/validatorregistry/types"
	"github.com/stretchr/testify/require"
)

// withLockup is a genesis holding one well-formed validator record with the given
// lock-up duration.
func withLockup(t *testing.T, lockup int64) *types.GenesisState {
	addr, err := bech32.ConvertAndEncode("saicon", make([]byte, 20))
	require.NoError(t, err)
	gs := types.DefaultGenesis()
	gs.Validators = []types.ValidatorMetadata{{
		ValidatorAddress:      addr,
		Status:                types.Status_STATUS_BONDING,
		LockupDurationSeconds: lockup,
	}}
	return gs
}

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
			desc:     "valid genesis state",
			genState: &types.GenesisState{},
			valid:    true,
		},
		{
			desc:     "lock-up above the registration cap may predate it",
			genState: withLockup(t, types.MaxLockupDurationSeconds+1),
			valid:    true,
		},
		{
			desc:     "lock-up at the longest representable",
			genState: withLockup(t, types.MaxRepresentableLockupSeconds),
			valid:    true,
		},
		{
			desc:     "lock-up that overflows the expiry",
			genState: withLockup(t, types.MaxRepresentableLockupSeconds+1),
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
