package types

import "fmt"

// DefaultParams returns the spec defaults: a round every 7 participation
// epochs, with a 100-block submission window.
func DefaultParams() Params {
	return Params{
		RotationEpochs: DefaultRotationEpochs,
		WindowBlocks:   DefaultWindowBlocks,
	}
}

// Validate enforces the tier-2 bounds: rotation_epochs in [1, 28] and
// window_blocks in [10, 200000], both inclusive.
func (p Params) Validate() error {
	if p.RotationEpochs < MinRotationEpochs || p.RotationEpochs > MaxRotationEpochs {
		return fmt.Errorf("rotation_epochs must be within [%d, %d], got %d",
			MinRotationEpochs, MaxRotationEpochs, p.RotationEpochs)
	}
	if p.WindowBlocks < MinWindowBlocks || p.WindowBlocks > MaxWindowBlocks {
		return fmt.Errorf("window_blocks must be within [%d, %d], got %d",
			MinWindowBlocks, MaxWindowBlocks, p.WindowBlocks)
	}
	return nil
}
