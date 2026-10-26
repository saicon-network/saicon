package keeper_test

import (
	"testing"

	"cosmossdk.io/math"

	"github.com/saicon-network/saicon/chain/x/participation/types"

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

// TestGenesisRoundTripCarriesEveryCollection is the regression for the defect the
// simulation suite found on 2026-08-15: the module exported only its params, so
// an export/import cycle — a coordinated upgrade, a state migration, a genesis
// rebuild — silently erased every score snapshot and every accumulated EP.
// Eligibility is ACTIVE and compliant and EP > 0, so the whole set came back
// unable to earn with nothing reporting why.
//
// The state below is deliberately asymmetric: one validator has signed blocks and
// no missed entry, the other the reverse. That is how the chain actually writes
// them — one side per block — and merging the two into a single genesis record
// created keys on import that the exported chain never had.
func TestGenesisRoundTripCarriesEveryCollection(t *testing.T) {
	const (
		valA = "saicon1q85d869qu46e5ukwemwannkkh3xzhrrwm58ayr"
		valB = "saicon1pyysjzgfpyysjzgfpyysjzgfpyysjzgfcen8fs"
	)

	genesisState := types.GenesisState{
		Params: types.DefaultParams(),
		Scores: []types.ParticipationScoreSnapshot{
			{
				ValidatorAddress:  valA,
				EpochId:           7,
				BlocksSigned:      99,
				BlocksMissed:      1,
				VotesParticipated: 3,
				UptimeFraction:    math.LegacyMustNewDecFromStr("0.99"),
				ComputedScore:     math.LegacyMustNewDecFromStr("99.0"),
			},
			{
				ValidatorAddress:  valB,
				EpochId:           7,
				BlocksSigned:      50,
				BlocksMissed:      50,
				VotesParticipated: 0,
				UptimeFraction:    math.LegacyMustNewDecFromStr("0.5"),
				ComputedScore:     math.LegacyMustNewDecFromStr("50.0"),
			},
		},
		EmpiricalPoints: []types.EmpiricalPointsState{
			{ValidatorAddress: valA, AccumulatedEp: math.NewInt(1234), LastEpochUpdate: 7},
			{ValidatorAddress: valB, AccumulatedEp: math.ZeroInt(), LastEpochUpdate: 7},
		},
		// Asymmetric on purpose — see the note above.
		EpochBlocksSigned: []types.EpochCount{{ValidatorAddress: valA, Count: 12}},
		EpochBlocksMissed: []types.EpochCount{{ValidatorAddress: valB, Count: 4}},
	}

	f := initFixture(t)
	require.NoError(t, f.keeper.InitGenesis(f.ctx, genesisState))

	got, err := f.keeper.ExportGenesis(f.ctx)
	require.NoError(t, err)
	require.NoError(t, got.Validate())

	require.Len(t, got.Scores, 2, "score snapshots must survive the round trip")
	require.Len(t, got.EmpiricalPoints, 2, "accumulated EP must survive the round trip")

	require.Len(t, got.EpochBlocksSigned, 1,
		"a validator with no signed blocks must NOT gain an entry on export")
	require.Len(t, got.EpochBlocksMissed, 1,
		"a validator with no missed blocks must NOT gain an entry on export")
	require.Equal(t, uint64(12), got.EpochBlocksSigned[0].Count)
	require.Equal(t, uint64(4), got.EpochBlocksMissed[0].Count)

	// EP is the value that gates every reward, so compare it rather than trust
	// the count above.
	epByAddr := map[string]math.Int{}
	for _, e := range got.EmpiricalPoints {
		epByAddr[e.ValidatorAddress] = e.AccumulatedEp
	}
	require.Equal(t, math.NewInt(1234), epByAddr[valA], "EP came back changed")
	require.True(t, epByAddr[valB].IsZero())
}
