package keeper_test

import (
	"testing"

	"cosmossdk.io/math"
	moduletestutil "github.com/cosmos/cosmos-sdk/types/module/testutil"

	module "github.com/saicon-network/saicon/chain/x/participation/module"
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

// WARP rows survive export and import, and a snapshot written before the
// engine keeps an absent warp: absent means unknown, and a round trip that
// turned it into 0 would read as a validator that had been punished.
func TestGenesisRoundTripCarriesWarp(t *testing.T) {
	const (
		valA = "saiconvaloper1q85d869qu46e5ukwemwannkkh3xzhrrwrn5ta7"
		valB = "saicon1pyysjzgfpyysjzgfpyysjzgfpyysjzgfcen8fs"
	)
	w82 := math.LegacyMustNewDecFromStr("82")
	genesisState := types.GenesisState{
		Params: types.DefaultParams(),
		Scores: []types.ParticipationScoreSnapshot{
			{ValidatorAddress: valA, EpochId: 6, UptimeFraction: math.LegacyOneDec(), ComputedScore: math.LegacyOneDec()},
			{ValidatorAddress: valA, EpochId: 7, UptimeFraction: math.LegacyOneDec(), ComputedScore: math.LegacyOneDec(), Warp: &w82},
		},
		Warps: []types.WarpState{
			{ValidatorAddress: valA, Value: w82, LastEpoch: 7},
			{ValidatorAddress: valB, Value: math.LegacyZeroDec(), LastEpoch: 0},
		},
	}
	require.NoError(t, genesisState.Validate())

	f := initFixture(t)
	require.NoError(t, f.keeper.InitGenesis(f.ctx, genesisState))
	msg, broken := f.keeper.WarpInvariant(f.ctx)
	require.False(t, broken, msg)

	got, err := f.keeper.ExportGenesis(f.ctx)
	require.NoError(t, err)
	require.NoError(t, got.Validate())

	// Through JSON too, as a real export/import goes.
	cdc := moduletestutil.MakeTestEncodingConfig(module.AppModule{}).Codec
	bz, err := cdc.MarshalJSON(got)
	require.NoError(t, err)
	var back types.GenesisState
	require.NoError(t, cdc.UnmarshalJSON(bz, &back))

	require.Len(t, back.Warps, 2)
	byAddr := map[string]types.WarpState{}
	for _, w := range back.Warps {
		byAddr[w.ValidatorAddress] = w
	}
	require.Equal(t, w82, byAddr[valA].Value)
	require.Equal(t, uint64(7), byAddr[valA].LastEpoch)
	require.True(t, byAddr[valB].Value.IsZero())
	require.Equal(t, uint64(0), byAddr[valB].LastEpoch)

	require.Len(t, back.Scores, 2)
	for _, s := range back.Scores {
		switch s.EpochId {
		case 6:
			require.Nil(t, s.Warp, "a pre-WARP snapshot must not gain a warp")
		case 7:
			require.NotNil(t, s.Warp)
			require.Equal(t, w82, *s.Warp)
		}
	}
}

// A genesis written before S10 has params without the four WARP fields.
// Stored as they are, they read back as zeros and WARP never steps, with a log
// line as the only trace; InitGenesis refuses them instead (ADR-0009 R8), and
// so does every other params rule.
func TestInitGenesisRefusesInvalidParams(t *testing.T) {
	cdc := moduletestutil.MakeTestEncodingConfig(module.AppModule{}).Codec
	var preS10 types.GenesisState
	require.NoError(t, cdc.UnmarshalJSON([]byte(`{"params":{"epoch_length_blocks":"100","ep_decay":"0.000000000000000000"}}`), &preS10))
	require.True(t, preS10.Params.WarpFall.IsNil())

	f := initFixture(t)
	require.ErrorContains(t, f.keeper.InitGenesis(f.ctx, preS10), "participation genesis: params: warp_fall must not be nil")
	stored, err := f.keeper.Params.Get(f.ctx)
	require.NoError(t, err)
	require.Equal(t, types.DefaultParams(), stored, "the fixture's params are left as they were")

	bad := *types.DefaultGenesis()
	bad.Params.WarpRise = bad.Params.WarpFall
	require.ErrorContains(t, initFixture(t).keeper.InitGenesis(f.ctx, bad), "0 < warp_rise < warp_fall <= 1")

	bad = *types.DefaultGenesis()
	bad.Params.EpDecay = math.LegacyOneDec()
	require.ErrorContains(t, initFixture(t).keeper.InitGenesis(f.ctx, bad), "ep_decay must be in [0,1)")
}
