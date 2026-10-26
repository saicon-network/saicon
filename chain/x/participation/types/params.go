package types

import (
	"fmt"

	"cosmossdk.io/math"
)

// NewParams creates a new Params instance.
func NewParams(epochLengthBlocks uint64, epDecay math.LegacyDec) Params {
	return Params{
		EpochLengthBlocks: epochLengthBlocks,
		EpDecay:           epDecay,
	}
}

// DefaultParams returns a default set of parameters.
func DefaultParams() Params {
	return NewParams(100, math.LegacyZeroDec())
}

// Validate validates the set of params.
//
// EpochLengthBlocks=0 is tolerated at the param layer and treated as "epoch
// tick disabled" by EndBlock (see module.go); it is not rejected here so the
// storage scaffold + update-params tests can use a zero value.
func (p Params) Validate() error {
	if p.EpDecay.IsNil() {
		return fmt.Errorf("ep_decay must not be nil")
	}
	if p.EpDecay.IsNegative() || p.EpDecay.GTE(math.LegacyOneDec()) {
		return fmt.Errorf("ep_decay must be in [0,1), got %s", p.EpDecay)
	}
	return nil
}
