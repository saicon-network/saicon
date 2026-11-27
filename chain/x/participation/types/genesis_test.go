package types_test

import (
	"testing"

	"cosmossdk.io/math"
	"github.com/stretchr/testify/require"

	"github.com/saicon-network/saicon/chain/x/participation/types"
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
			desc:     "valid genesis state",
			genState: &types.GenesisState{Params: types.NewParams(0, math.LegacyZeroDec())},
			valid:    true,
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

// WARP rows: every one names a validator once, with a value inside [0, 100],
// and passes V-16: 0 when never scored, else backed by a snapshot at its
// last_epoch.
func TestGenesisState_ValidateWarps(t *testing.T) {
	const (
		valA = "saicon1q85d869qu46e5ukwemwannkkh3xzhrrwm58ayr"
		valB = "saicon1pyysjzgfpyysjzgfpyysjzgfpyysjzgfcen8fs"
	)
	// valA's bytes under the valoper prefix: the same validator, spelled differently.
	valAOper := "saiconvaloper1q85d869qu46e5ukwemwannkkh3xzhrrwrn5ta7"

	row := func(addr, v string, epoch uint64) types.WarpState {
		return types.WarpState{ValidatorAddress: addr, Value: math.LegacyMustNewDecFromStr(v), LastEpoch: epoch}
	}
	snap := func(addr string, epoch uint64, warp string) types.ParticipationScoreSnapshot {
		s := types.ParticipationScoreSnapshot{ValidatorAddress: addr, EpochId: epoch}
		if warp != "" {
			w := math.LegacyMustNewDecFromStr(warp)
			s.Warp = &w
		}
		return s
	}
	// with backs every named row that has a last_epoch with a snapshot at it.
	with := func(rows ...types.WarpState) *types.GenesisState {
		gs := types.DefaultGenesis()
		gs.Warps = rows
		for _, r := range rows {
			if r.LastEpoch > 0 && r.ValidatorAddress != "" {
				gs.Scores = append(gs.Scores, snap(r.ValidatorAddress, r.LastEpoch, ""))
			}
		}
		return gs
	}
	bare := func(scores []types.ParticipationScoreSnapshot, rows ...types.WarpState) *types.GenesisState {
		gs := types.DefaultGenesis()
		gs.Warps = rows
		gs.Scores = scores
		return gs
	}
	for _, c := range []struct {
		name string
		gs   *types.GenesisState
		err  string
	}{
		{"two validators", with(row(valA, "80", 3), row(valB, "0", 0)), ""},
		{"the bounds themselves", with(row(valA, "100", 1), row(valB, "0", 1)), ""},
		{"empty address", with(row("", "80", 1)), "warps[0]: empty validator_address"},
		{"undecodable address", with(row("not-bech32", "80", 1)), "warps[0] (not-bech32): invalid validator_address"},
		{"nil value", with(types.WarpState{ValidatorAddress: valA, LastEpoch: 1}), "value is not set"},
		{"negative", with(row(valA, "-0.000000000000000001", 1)), "outside [0, 100]"},
		{"above 100", with(row(valA, "100.000000000000000001", 1)), "outside [0, 100]"},
		{"duplicate", with(row(valA, "80", 1), row(valB, "70", 1), row(valA, "60", 2)), "warps[2] (" + valA + "): duplicate validator"},
		{"duplicate under another prefix", with(row(valA, "80", 1), row(valAOper, "60", 2)), "duplicate validator"},
		{"never scored and not 0", bare(nil, row(valA, "50", 0)), "warps[0] (" + valA + "): never scored (last_epoch 0) but holds 50"},
		{"never scored, 0", bare(nil, row(valA, "0", 0)), ""},
		{"no snapshot at all", bare(nil, row(valA, "70", 3)), "warps[0] (" + valA + "): no snapshot in scores at its last_epoch 3"},
		{"a snapshot at another epoch", bare([]types.ParticipationScoreSnapshot{snap(valA, 2, "")}, row(valA, "70", 3)), "no snapshot in scores at its last_epoch 3"},
		{"another validator's snapshot", bare([]types.ParticipationScoreSnapshot{snap(valB, 3, "")}, row(valA, "70", 3)), "no snapshot in scores at its last_epoch 3"},
		{"a snapshot spelled under another prefix", bare([]types.ParticipationScoreSnapshot{snap(valAOper, 3, "70")}, row(valA, "70", 3)), ""},
		{"a snapshot warp above 100", bare([]types.ParticipationScoreSnapshot{snap(valA, 3, "100.000000000000000001")}), "scores[0] (" + valA + "): warp: value 100.000000000000000001 is outside [0, 100]"},
		{"a snapshot warp below 0", bare([]types.ParticipationScoreSnapshot{snap(valA, 3, "-1")}), "warp: value -1.000000000000000000 is outside [0, 100]"},
	} {
		t.Run(c.name, func(t *testing.T) {
			err := c.gs.Validate()
			if c.err == "" {
				require.NoError(t, err)
				return
			}
			require.ErrorContains(t, err, c.err)
		})
	}
}
