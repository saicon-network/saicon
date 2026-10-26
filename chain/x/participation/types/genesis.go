package types

import "fmt"

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
	return nil
}
