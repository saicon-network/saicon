package types

import (
	"fmt"

	"github.com/cosmos/cosmos-sdk/types/bech32"
)

// DefaultGenesis returns the default genesis state
func DefaultGenesis() *GenesisState {
	return &GenesisState{
		Params: DefaultParams(),
	}
}

// Validate performs basic genesis state validation returning an error upon any
// failure.
//
// The address checks are cheap and they matter: every record below is keyed by
// the address it carries, so an empty one would silently collapse rows together
// on import rather than fail.
func (gs GenesisState) Validate() error {
	if err := gs.Params.Validate(); err != nil {
		return err
	}

	for i, s := range gs.Scores {
		if s.ValidatorAddress == "" {
			return fmt.Errorf("participation genesis: scores[%d]: empty validator_address", i)
		}
	}
	for i, e := range gs.EmpiricalPoints {
		if e.ValidatorAddress == "" {
			return fmt.Errorf("participation genesis: empirical_points[%d]: empty validator_address", i)
		}
		if e.AccumulatedEp.IsNil() {
			return fmt.Errorf("participation genesis: empirical_points[%d] (%s): accumulated_ep is not set",
				i, e.ValidatorAddress)
		}
		if e.AccumulatedEp.IsNegative() {
			return fmt.Errorf("participation genesis: empirical_points[%d] (%s): accumulated_ep is negative: %s",
				i, e.ValidatorAddress, e.AccumulatedEp)
		}
	}
	for _, l := range []struct {
		name string
		list []EpochCount
	}{
		{"epoch_blocks_signed", gs.EpochBlocksSigned},
		{"epoch_blocks_missed", gs.EpochBlocksMissed},
	} {
		for i, c := range l.list {
			if c.ValidatorAddress == "" {
				return fmt.Errorf("participation genesis: %s[%d]: empty validator_address", l.name, i)
			}
		}
	}
	return validateWarps(gs.Warps, gs.Scores)
}

// snapshotKey is a snapshot's store key in a form a map can hold: its epoch
// and the validator's address bytes.
type snapshotKey struct {
	epoch uint64
	addr  string
}

// validateWarps checks the WARP rows, and refuses every row invariant V-16
// would report once imported, so a genesis that validates also starts clean:
//
//   - a row with last_epoch 0 (punished before it was ever scored) is exactly 0;
//   - a row with last_epoch > 0 has a snapshot at that epoch in scores.
//
// The store keys rows and snapshots by address bytes, so both the duplicate
// check and the snapshot lookup compare bytes, whatever prefix each record was
// spelled with: two rows for one validator would leave whichever was imported
// last. A snapshot's warp, when it has one, must lie within [0, 100].
func validateWarps(rows []WarpState, scores []ParticipationScoreSnapshot) error {
	snapshots := make(map[snapshotKey]bool, len(scores))
	for i, s := range scores {
		if s.Warp != nil {
			if err := ValidateWarpValue(*s.Warp); err != nil {
				return fmt.Errorf("participation genesis: scores[%d] (%s): warp: %w", i, s.ValidatorAddress, err)
			}
		}
		// An undecodable address cannot back a row; InitGenesis refuses it.
		if _, raw, err := bech32.DecodeAndConvert(s.ValidatorAddress); err == nil {
			snapshots[snapshotKey{s.EpochId, string(raw)}] = true
		}
	}

	seen := make(map[string]int, len(rows))
	for i, w := range rows {
		if w.ValidatorAddress == "" {
			return fmt.Errorf("participation genesis: warps[%d]: empty validator_address", i)
		}
		_, raw, err := bech32.DecodeAndConvert(w.ValidatorAddress)
		if err != nil {
			return fmt.Errorf("participation genesis: warps[%d] (%s): invalid validator_address: %w", i, w.ValidatorAddress, err)
		}
		if j, dup := seen[string(raw)]; dup {
			return fmt.Errorf("participation genesis: warps[%d] (%s): duplicate validator, already in warps[%d]", i, w.ValidatorAddress, j)
		}
		seen[string(raw)] = i
		if err := ValidateWarpValue(w.Value); err != nil {
			return fmt.Errorf("participation genesis: warps[%d] (%s): %w", i, w.ValidatorAddress, err)
		}
		if w.LastEpoch == 0 {
			if !w.Value.IsZero() {
				return fmt.Errorf("participation genesis: warps[%d] (%s): never scored (last_epoch 0) but holds %s; only 0 is allowed",
					i, w.ValidatorAddress, w.Value)
			}
			continue
		}
		if !snapshots[snapshotKey{w.LastEpoch, string(raw)}] {
			return fmt.Errorf("participation genesis: warps[%d] (%s): no snapshot in scores at its last_epoch %d",
				i, w.ValidatorAddress, w.LastEpoch)
		}
	}
	return nil
}
