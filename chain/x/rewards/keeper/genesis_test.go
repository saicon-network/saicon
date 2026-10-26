package keeper_test

import (
	"testing"

	"cosmossdk.io/math"

	"github.com/saicon-network/saicon/chain/x/rewards/types"

	"github.com/stretchr/testify/require"
)

func TestGenesis(t *testing.T) {
	genesisState := types.GenesisState{
		Params: types.DefaultParams(),
	}

	f := initFixture(t)
	err := f.keeper.InitGenesis(f.ctx, genesisState)
	require.NoError(t, err)
	got, err := f.keeper.ExportGenesis(f.ctx)
	require.NoError(t, err)
	require.NotNil(t, got)

	require.EqualExportedValues(t, genesisState.Params, got.Params)
}

// TestGenesisRoundTripCarriesSnapshotsAndAccumulators is the regression for the
// defect the simulation suite found on 2026-08-15: the module exported only its
// params, so an export/import cycle erased the payment record — the only audit
// trail of what the chain minted and to whom — along with the two accumulators
// that decide the next mint.
//
// The accumulators matter more than they look. Remainder is base units carried
// from an epoch that did not divide evenly (ADR-0011 D5); losing it destroys
// coins already accounted for. ObservedGasFees reduces how much the next mint
// must create (D2); losing it makes the chain mint above the security budget for
// that epoch.
func TestGenesisRoundTripCarriesSnapshotsAndAccumulators(t *testing.T) {
	const (
		valA = "saicon1qgpqyqszqgpqyqszqgpqyqszqgpqyqszx4qnpr"
		valB = "saicon1pyysjzgfpyysjzgfpyysjzgfpyysjzgfcen8fs"
	)

	genesisState := types.GenesisState{
		Params: types.DefaultParams(),
		Snapshots: []types.RewardSnapshot{
			{ValidatorAddress: valA, EpochId: 5, Amount: "238000000nscn", Eligible: true, Reason: "eligible"},
			// A denied record is state too: it is how a validator learns why it
			// was paid nothing, so dropping it is not a cosmetic loss.
			{ValidatorAddress: valB, EpochId: 5, Amount: "", Eligible: false, Reason: "EP=0"},
		},
		Remainder:       math.NewInt(17),
		ObservedGasFees: math.NewInt(4242),
	}

	f := initFixture(t)
	require.NoError(t, f.keeper.InitGenesis(f.ctx, genesisState))

	got, err := f.keeper.ExportGenesis(f.ctx)
	require.NoError(t, err)
	require.NoError(t, got.Validate())

	require.Len(t, got.Snapshots, 2, "reward snapshots must survive the round trip")
	require.Equal(t, math.NewInt(17), got.Remainder, "carried remainder came back changed")
	require.Equal(t, math.NewInt(4242), got.ObservedGasFees, "observed gas fees came back changed")

	byAddr := map[string]types.RewardSnapshot{}
	for _, s := range got.Snapshots {
		byAddr[s.ValidatorAddress] = s
	}
	require.True(t, byAddr[valA].Eligible)
	require.Equal(t, "238000000nscn", byAddr[valA].Amount)
	require.False(t, byAddr[valB].Eligible)
	require.Equal(t, "EP=0", byAddr[valB].Reason, "the reason a validator was paid nothing must survive")
}
