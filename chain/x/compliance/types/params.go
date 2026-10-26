package types

// DefaultUnjailCooldownBlocks is the default minimum JAILED duration before a
// governed unjail is allowed: ~1 day at ~2s blocks. Gov-tunable; calibrate later.
const DefaultUnjailCooldownBlocks int64 = 43200

// NewParams creates a new Params instance.
func NewParams(unjailCooldownBlocks int64) Params {
	return Params{UnjailCooldownBlocks: unjailCooldownBlocks}
}

// DefaultParams returns a default set of parameters.
func DefaultParams() Params {
	return NewParams(DefaultUnjailCooldownBlocks)
}

// Validate validates the set of params.
func (p Params) Validate() error {
	if p.UnjailCooldownBlocks < 0 {
		return ErrInvalidParams.Wrapf("unjail_cooldown_blocks must be >= 0, got %d", p.UnjailCooldownBlocks)
	}
	return nil
}
