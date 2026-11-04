package types_test

import (
	"strings"
	"testing"

	sdk "github.com/cosmos/cosmos-sdk/types"
	"github.com/stretchr/testify/require"

	"github.com/saicon-network/saicon/chain/crypto/vrf"
	"github.com/saicon-network/saicon/chain/x/randomness/types"
)

func valoper(b byte) string {
	return sdk.MustBech32ifyAddressBytes(types.ValidatorPrefix, fill(b, 20))
}

// vrfKeyHex is the public key of a deterministic test key, as 64 hex characters.
func vrfKeyHex(t *testing.T, b byte) string {
	t.Helper()
	k, err := vrf.NewKeyFromSeed(fill(b, 32))
	require.NoError(t, err)
	return hexOf(k.PublicKey())
}

func hexOf(b []byte) string {
	const digits = "0123456789abcdef"
	var sb strings.Builder
	for _, c := range b {
		sb.WriteByte(digits[c>>4])
		sb.WriteByte(digits[c&15])
	}
	return sb.String()
}

func hexN(c string, n int) string { return strings.Repeat(c, n) }

// openGenesis is a valid state: round 3 OPEN with a three-entity pool (one
// unkeyed), two submissions and two closed rounds.
func openGenesis(t *testing.T) *types.GenesisState {
	t.Helper()
	return &types.GenesisState{
		Params:       types.DefaultParams(),
		InitialInput: types.DefaultInitialInput(),
		CurrentRound: &types.Round{
			Number: 3, Input: hexN("cd", 32),
			KeyCutoffHeight: 10, WindowOpenHeight: 20, RotationHeight: 30,
			Status: types.RoundStatus_ROUND_STATUS_OPEN,
		},
		Pool: []types.PoolEntry{
			{EntityId: "acme", Validator: valoper(1), VrfPublicKey: vrfKeyHex(t, 1)},
			{EntityId: "beta", Validator: valoper(2), VrfPublicKey: vrfKeyHex(t, 2)},
			{EntityId: "gamma"},
		},
		Submissions: []types.Submission{
			{EntityId: "acme", Validator: valoper(1), Output: hexN("ab", 64), Proof: hexN("cd", 80), Height: 25},
		},
		Draws: []types.Draw{
			{
				Round: 1, Outcome: types.DrawOutcome_DRAW_OUTCOME_DRAWN,
				Input: hexN("11", 32), Seed: hexN("22", 32),
				WindowOpenHeight: 5, RotationHeight: 15,
				Pool: []string{"acme", "beta"}, Keyed: 2, Outputs: []string{hexN("33", 64), hexN("44", 64)},
				Submitted: 2, Required: 2, Winners: []string{"beta", "acme"}, CohortRound: 1,
			},
			{
				Round: 2, Outcome: types.DrawOutcome_DRAW_OUTCOME_SKIPPED,
				Input: hexN("22", 32), Seed: hexN("55", 32),
				WindowOpenHeight: 15, RotationHeight: 25,
				Pool: []string{"acme", "beta"}, Keyed: 2, Submitted: 1, Required: 2,
				SkipReason: "quorum not met: 1 of 2 submitted, 2 required (2 had an eligible key)", CohortRound: 1,
			},
		},
	}
}

func TestDefaultGenesisIsValid(t *testing.T) {
	gs := types.DefaultGenesis()
	require.NoError(t, gs.Validate())
	require.Nil(t, gs.CurrentRound)
	require.Equal(t, types.DefaultInitialInput(), gs.InitialInput)
	require.NoError(t, openGenesis(t).Validate())
}

func TestGenesisAcceptsAnUnscheduledNextRound(t *testing.T) {
	gs := types.DefaultGenesis()
	gs.CurrentRound = &types.Round{
		Number: 4, Input: hexN("ee", 32), KeyCutoffHeight: 30,
		Status: types.RoundStatus_ROUND_STATUS_SCHEDULED,
	}
	gs.Draws = []types.Draw{{
		Round: 3, Outcome: types.DrawOutcome_DRAW_OUTCOME_SKIPPED,
		Input: hexN("11", 32), Seed: hexN("22", 32), SkipReason: "pool is empty",
	}}
	require.NoError(t, gs.Validate())
}

func TestGenesisRejects(t *testing.T) {
	scheduled := func(gs *types.GenesisState) {
		gs.CurrentRound = &types.Round{
			Number: 3, Input: hexN("cd", 32), KeyCutoffHeight: 10, WindowOpenHeight: 20, RotationHeight: 30,
			Status: types.RoundStatus_ROUND_STATUS_SCHEDULED,
		}
		gs.Pool, gs.Submissions = nil, nil
	}
	for _, tc := range []struct {
		name string
		mut  func(t *testing.T, gs *types.GenesisState)
	}{
		{"bad params", func(_ *testing.T, gs *types.GenesisState) { gs.Params.WindowBlocks = 1 }},
		{"short initial input", func(_ *testing.T, gs *types.GenesisState) { gs.InitialInput = "ab" }},
		{"uppercase initial input", func(_ *testing.T, gs *types.GenesisState) { gs.InitialInput = hexN("AB", 32) }},
		{"empty initial input", func(_ *testing.T, gs *types.GenesisState) { gs.InitialInput = "" }},
		{"status unspecified", func(_ *testing.T, gs *types.GenesisState) {
			gs.CurrentRound.Status = types.RoundStatus_ROUND_STATUS_UNSPECIFIED
		}},
		{"number zero", func(_ *testing.T, gs *types.GenesisState) { gs.CurrentRound.Number = 0; gs.Draws = nil }},
		{"bad round input", func(_ *testing.T, gs *types.GenesisState) { gs.CurrentRound.Input = "zz" }},
		{"negative cutoff", func(_ *testing.T, gs *types.GenesisState) { gs.CurrentRound.KeyCutoffHeight = -1 }},
		{"cutoff after window open", func(_ *testing.T, gs *types.GenesisState) { gs.CurrentRound.KeyCutoffHeight = 21 }},
		{"window open at rotation", func(_ *testing.T, gs *types.GenesisState) { gs.CurrentRound.WindowOpenHeight = 30 }},
		{"window open after rotation", func(_ *testing.T, gs *types.GenesisState) { gs.CurrentRound.WindowOpenHeight = 31 }},
		{"unscheduled with a window", func(_ *testing.T, gs *types.GenesisState) {
			scheduled(gs)
			gs.CurrentRound.RotationHeight = 0
		}},
		{"open without rotation height", func(_ *testing.T, gs *types.GenesisState) {
			gs.CurrentRound.RotationHeight = 0
			gs.CurrentRound.WindowOpenHeight = 0
		}},
		{"pool while scheduled", func(t *testing.T, gs *types.GenesisState) {
			scheduled(gs)
			gs.Pool = []types.PoolEntry{{EntityId: "acme"}}
		}},
		{"submission while scheduled", func(t *testing.T, gs *types.GenesisState) {
			scheduled(gs)
			gs.Submissions = []types.Submission{{EntityId: "acme", Validator: valoper(1), Output: hexN("ab", 64), Proof: hexN("cd", 80), Height: 25}}
		}},
		{"pool without a round", func(_ *testing.T, gs *types.GenesisState) {
			gs.CurrentRound, gs.Submissions, gs.Draws = nil, nil, nil
		}},
		{"duplicate pool entity", func(_ *testing.T, gs *types.GenesisState) {
			gs.Pool = append(gs.Pool, types.PoolEntry{EntityId: "gamma"})
		}},
		{"bad pool entity id", func(_ *testing.T, gs *types.GenesisState) { gs.Pool[2].EntityId = "X" }},
		{"validator without key", func(_ *testing.T, gs *types.GenesisState) { gs.Pool[0].VrfPublicKey = "" }},
		{"key without validator", func(_ *testing.T, gs *types.GenesisState) { gs.Pool[0].Validator = "" }},
		{"cosmosvaloper validator", func(_ *testing.T, gs *types.GenesisState) {
			gs.Pool[0].Validator = sdk.MustBech32ifyAddressBytes("cosmosvaloper", fill(1, 20))
		}},
		{"duplicate designated validator", func(t *testing.T, gs *types.GenesisState) {
			gs.Pool[1].Validator = gs.Pool[0].Validator
		}},
		{"malformed pool key", func(_ *testing.T, gs *types.GenesisState) { gs.Pool[0].VrfPublicKey = hexN("ab", 31) }},
		{"submission outside the pool", func(_ *testing.T, gs *types.GenesisState) { gs.Submissions[0].EntityId = "delta" }},
		{"submission by another validator", func(_ *testing.T, gs *types.GenesisState) { gs.Submissions[0].Validator = valoper(2) }},
		{"submission for an unkeyed entity", func(_ *testing.T, gs *types.GenesisState) {
			gs.Submissions[0] = types.Submission{EntityId: "gamma", Validator: valoper(3), Output: hexN("ab", 64), Proof: hexN("cd", 80), Height: 25}
		}},
		{"duplicate submission", func(_ *testing.T, gs *types.GenesisState) {
			gs.Submissions = append(gs.Submissions, gs.Submissions[0])
		}},
		{"short output", func(_ *testing.T, gs *types.GenesisState) { gs.Submissions[0].Output = hexN("ab", 63) }},
		{"short proof", func(_ *testing.T, gs *types.GenesisState) { gs.Submissions[0].Proof = hexN("cd", 79) }},
		{"negative submission height", func(_ *testing.T, gs *types.GenesisState) { gs.Submissions[0].Height = -1 }},
		{"draw outcome unspecified", func(_ *testing.T, gs *types.GenesisState) {
			gs.Draws[0].Outcome = types.DrawOutcome_DRAW_OUTCOME_UNSPECIFIED
		}},
		{"53 draws", func(_ *testing.T, gs *types.GenesisState) {
			gs.CurrentRound.Number = 100
			gs.Draws = nil
			for i := uint64(1); i <= 53; i++ {
				gs.Draws = append(gs.Draws, types.Draw{
					Round: i, Outcome: types.DrawOutcome_DRAW_OUTCOME_SKIPPED,
					Input: hexN("11", 32), Seed: hexN("22", 32), SkipReason: "pool is empty",
				})
			}
		}},
		{"duplicate draw round", func(_ *testing.T, gs *types.GenesisState) { gs.Draws[1].Round = 1 }},
		{"draw round zero", func(_ *testing.T, gs *types.GenesisState) { gs.Draws[0].Round = 0 }},
		{"draw round equals current", func(_ *testing.T, gs *types.GenesisState) { gs.Draws[1].Round = 3 }},
		{"draw round past current", func(_ *testing.T, gs *types.GenesisState) { gs.Draws[1].Round = 4 }},
		{"draw bad seed", func(_ *testing.T, gs *types.GenesisState) { gs.Draws[0].Seed = "ab" }},
		{"draw bad output", func(_ *testing.T, gs *types.GenesisState) { gs.Draws[0].Outputs[0] = "ab" }},
		{"drawn with too few winners", func(_ *testing.T, gs *types.GenesisState) { gs.Draws[0].Winners = []string{"acme"} }},
		{"drawn with a winner outside the pool", func(_ *testing.T, gs *types.GenesisState) {
			gs.Draws[0].Winners = []string{"acme", "delta"}
		}},
		{"drawn with a repeated winner", func(_ *testing.T, gs *types.GenesisState) {
			gs.Draws[0].Winners = []string{"acme", "acme"}
		}},
		{"drawn with an empty pool", func(_ *testing.T, gs *types.GenesisState) {
			gs.Draws[0].Pool, gs.Draws[0].Winners = nil, nil
		}},
		{"skipped with winners", func(_ *testing.T, gs *types.GenesisState) { gs.Draws[1].Winners = []string{"acme"} }},
		{"skipped without a reason", func(_ *testing.T, gs *types.GenesisState) { gs.Draws[1].SkipReason = "" }},
		{"drawn with a reason", func(_ *testing.T, gs *types.GenesisState) { gs.Draws[0].SkipReason = "x" }},
		{"bad draw pool id", func(_ *testing.T, gs *types.GenesisState) { gs.Draws[0].Pool[0] = "X" }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			gs := openGenesis(t)
			tc.mut(t, gs)
			require.Error(t, gs.Validate())
		})
	}
}

func TestGenesisAccepts22WinnersOutOf30(t *testing.T) {
	gs := types.DefaultGenesis()
	gs.CurrentRound = &types.Round{Number: 2, Input: hexN("11", 32), Status: types.RoundStatus_ROUND_STATUS_SCHEDULED}
	pool := make([]string, 30)
	for i := range pool {
		pool[i] = "entity-" + string(rune('a'+i%26)) + string(rune('a'+i/26))
	}
	d := types.Draw{
		Round: 1, Outcome: types.DrawOutcome_DRAW_OUTCOME_DRAWN, Input: hexN("11", 32), Seed: hexN("22", 32),
		Pool: pool, Submitted: 30, Required: 20, Winners: types.Winners(fill(0x22, 32), pool), CohortRound: 1,
	}
	gs.Draws = []types.Draw{d}
	require.Len(t, d.Winners, 22)
	require.NoError(t, gs.Validate())
}
