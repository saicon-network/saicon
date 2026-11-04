package keeper_test

import (
	"encoding/hex"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/saicon-network/saicon/chain/crypto/vrf"
	"github.com/saicon-network/saicon/chain/x/randomness/keeper"
	"github.com/saicon-network/saicon/chain/x/randomness/types"
)

func TestDefaultGenesisCreatesRoundOne(t *testing.T) {
	f := newFixture(t)
	require.NoError(t, f.k.InitGenesis(f.ctx, *types.DefaultGenesis()))

	r := f.round()
	require.EqualValues(t, 1, r.Number)
	require.Equal(t, types.DefaultInitialInput(), r.Input)
	require.Zero(t, r.KeyCutoffHeight)
	require.Zero(t, r.WindowOpenHeight)
	require.Zero(t, r.RotationHeight)
	require.Equal(t, types.RoundStatus_ROUND_STATUS_SCHEDULED, r.Status)

	params, err := f.k.Params.Get(f.ctx)
	require.NoError(t, err)
	require.Equal(t, types.DefaultParams(), params)
}

func TestGenesisInitialInputSeedsRoundOne(t *testing.T) {
	f := newFixture(t)
	gs := types.DefaultGenesis()
	gs.InitialInput = hex.EncodeToString(make([]byte, 32))
	require.NoError(t, f.k.InitGenesis(f.ctx, *gs))
	require.Equal(t, gs.InitialInput, f.round().Input)

	exported, err := f.k.ExportGenesis(f.ctx)
	require.NoError(t, err)
	require.Equal(t, types.DefaultInitialInput(), exported.InitialInput,
		"the field is read only while current_round is absent, so it exports as the default")
	require.NotNil(t, exported.CurrentRound)

	bad := types.DefaultGenesis()
	bad.InitialInput = "zz"
	other := newFixture(t)
	require.Error(t, other.k.InitGenesis(other.ctx, *bad))
}

// sampleGenesis is an OPEN round 5 with two pool entries (one keyed), one
// submission and three closed rounds, written in key order. No Validate: the
// unit fixture's address prefix is cosmos.
func sampleGenesis(t *testing.T) (types.GenesisState, vrf.PrivateKey) {
	t.Helper()
	k1, err := vrf.NewKeyFromSeed(repeatByte(1, vrf.SeedSize))
	require.NoError(t, err)
	input := types.DefaultInitialInput()
	proof := k1.Prove(types.RoundAlpha(testChainID, 5, hexBytes(t, input, 32)))
	output, err := vrf.Verify(k1.PublicKey(), proof, types.RoundAlpha(testChainID, 5, hexBytes(t, input, 32)))
	require.NoError(t, err)

	seed := func(b byte) string { return hex.EncodeToString(repeatByte(b, 32)) }
	out := func(b byte) string { return hex.EncodeToString(repeatByte(b, vrf.OutputSize)) }
	return types.GenesisState{
		Params:       types.Params{RotationEpochs: 3, WindowBlocks: 50},
		InitialInput: types.DefaultInitialInput(),
		CurrentRound: &types.Round{
			Number: 5, Input: input, KeyCutoffHeight: 10,
			WindowOpenHeight: 30, RotationHeight: 40, Status: types.RoundStatus_ROUND_STATUS_OPEN,
		},
		Pool: []types.PoolEntry{
			{EntityId: "acme", Validator: val(1).String(), VrfPublicKey: hex.EncodeToString(k1.PublicKey())},
			{EntityId: "beta"},
		},
		Submissions: []types.Submission{{
			EntityId: "acme", Validator: val(1).String(),
			Output: hex.EncodeToString(output), Proof: hex.EncodeToString(proof), Height: 33,
		}},
		Draws: []types.Draw{
			{Round: 2, Outcome: types.DrawOutcome_DRAW_OUTCOME_SKIPPED, Input: seed(1), Seed: seed(2),
				WindowOpenHeight: 30, RotationHeight: 40, Pool: []string{"acme", "beta"},
				SkipReason: "quorum not met: 0 of 2 submitted, 2 required (0 had an eligible key)", Required: 2},
			{Round: 3, Outcome: types.DrawOutcome_DRAW_OUTCOME_DRAWN, Input: seed(2), Seed: seed(3),
				WindowOpenHeight: 50, RotationHeight: 60, Pool: []string{"acme", "beta"}, Keyed: 2,
				Outputs: []string{out(1), out(2)}, Submitted: 2, Required: 2,
				Winners: []string{"beta", "acme"}, CohortRound: 3},
			{Round: 4, Outcome: types.DrawOutcome_DRAW_OUTCOME_SKIPPED, Input: seed(3), Seed: seed(4),
				WindowOpenHeight: 70, RotationHeight: 80, Pool: []string{"acme"}, Keyed: 1,
				SkipReason: "quorum not met: 0 of 1 submitted, 1 required (1 had an eligible key)",
				Required:   1, CohortRound: 3},
		},
	}, k1
}

func repeatByte(b byte, n int) []byte {
	out := make([]byte, n)
	for i := range out {
		out[i] = b
	}
	return out
}

func TestGenesisRoundTrips(t *testing.T) {
	gs, _ := sampleGenesis(t)
	a := newFixture(t)
	require.NoError(t, a.k.InitGenesis(a.ctx, gs))

	// The designated index is derived from the pool.
	id, err := a.k.Designated.Get(a.ctx, val(1))
	require.NoError(t, err)
	require.Equal(t, "acme", id)
	has, err := a.k.Designated.Has(a.ctx, val(2))
	require.NoError(t, err)
	require.False(t, has)

	exportedA, err := a.k.ExportGenesis(a.ctx)
	require.NoError(t, err)
	require.Equal(t, gs, *exportedA, "the export is the state that was imported")

	b := newFixture(t)
	require.NoError(t, b.k.InitGenesis(b.ctx, *exportedA))
	exportedB, err := b.k.ExportGenesis(b.ctx)
	require.NoError(t, err)
	require.Equal(t, exportedA, exportedB)
}

// TestARestartPastTheRotationClosesTheRoundOnce (Review Focus 5): a chain
// restarted from an exported genesis whose first block is past the rotation
// height closes the imported round at the first EndBlock, once.
func TestARestartPastTheRotationClosesTheRoundOnce(t *testing.T) {
	countDraws := func(f *fixture) int {
		n := 0
		require.NoError(t, keeper.Draws(f.k).Walk(f.ctx, nil, func(uint64, types.Draw) (bool, error) {
			n++
			return false, nil
		}))
		return n
	}

	t.Run("an OPEN round with its quorum is drawn", func(t *testing.T) {
		gs, _ := sampleGenesis(t)
		gs.Params = types.Params{RotationEpochs: 2, WindowBlocks: 10} // grid of 10 with the fixture's epoch length
		gs.Draws = nil
		gs.Pool = gs.Pool[:1] // one entity, one submission: quorum 1 of 1
		f := newFixture(t)
		require.NoError(t, f.k.InitGenesis(f.ctx, gs))
		require.EqualValues(t, 40, f.round().RotationHeight)

		f.run(100, 100)

		d := f.draw(5)
		require.Equal(t, types.DrawOutcome_DRAW_OUTCOME_DRAWN, d.Outcome)
		require.EqualValues(t, 1, d.Submitted)
		require.Equal(t, []string{"acme"}, d.Winners)
		r := f.round()
		require.EqualValues(t, 6, r.Number)
		require.Equal(t, d.Seed, r.Input)
		require.EqualValues(t, 120, r.RotationHeight, "scheduled from the first block: R >= 111 on the grid of 10")
		require.EqualValues(t, 110, r.WindowOpenHeight)
		require.EqualValues(t, 30, r.KeyCutoffHeight)
		require.Equal(t, 1, countDraws(f))

		f.run(101, 101)
		require.Equal(t, 1, countDraws(f), "no second record")
		require.EqualValues(t, 6, f.round().Number)
	})

	t.Run("a SCHEDULED round is skipped as never opened", func(t *testing.T) {
		gs, _ := sampleGenesis(t)
		gs.Params = types.Params{RotationEpochs: 2, WindowBlocks: 10}
		gs.Draws, gs.Pool, gs.Submissions = nil, nil, nil
		gs.CurrentRound.Status = types.RoundStatus_ROUND_STATUS_SCHEDULED
		f := newFixture(t)
		require.NoError(t, f.k.InitGenesis(f.ctx, gs))

		f.run(100, 100)

		d := f.draw(5)
		require.Equal(t, types.DrawOutcome_DRAW_OUTCOME_SKIPPED, d.Outcome)
		require.Equal(t, "window never opened", d.SkipReason)
		r := f.round()
		require.EqualValues(t, 6, r.Number)
		require.EqualValues(t, 120, r.RotationHeight)
		require.Equal(t, 1, countDraws(f))

		f.run(101, 101)
		require.Equal(t, 1, countDraws(f), "no second record")
	})
}
