package types

import (
	"fmt"

	"cosmossdk.io/math"
)

// DefaultGenesis returns the default genesis state.
//
// Remainder and ObservedGasFees are explicitly zero rather than left as the nil
// math.Int the struct would otherwise carry: a nil Int marshals as an empty
// string and comes back nil, which panics on the first arithmetic the emission
// path does with it.
func DefaultGenesis() *GenesisState {
	return &GenesisState{
		Params:          DefaultParams(),
		Remainder:       math.ZeroInt(),
		ObservedGasFees: math.ZeroInt(),
	}
}

// Validate performs basic genesis state validation returning an error upon any
// failure.
func (gs GenesisState) Validate() error {
	if err := gs.Params.Validate(); err != nil {
		return err
	}

	// A nil Int is what an older export or a hand-written genesis produces, and
	// it fails later and further away — inside the emission maths — rather than
	// here at startup.
	if gs.Remainder.IsNil() {
		return fmt.Errorf("rewards genesis: remainder is not set (use \"0\")")
	}
	if gs.Remainder.IsNegative() {
		return fmt.Errorf("rewards genesis: remainder is negative: %s", gs.Remainder)
	}
	if gs.ObservedGasFees.IsNil() {
		return fmt.Errorf("rewards genesis: observed_gas_fees is not set (use \"0\")")
	}
	if gs.ObservedGasFees.IsNegative() {
		return fmt.Errorf("rewards genesis: observed_gas_fees is negative: %s", gs.ObservedGasFees)
	}

	for i, s := range gs.Snapshots {
		if s.ValidatorAddress == "" {
			return fmt.Errorf("rewards genesis: snapshots[%d]: empty validator_address", i)
		}
	}
	return nil
}
