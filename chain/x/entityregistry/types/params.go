package types

import "fmt"

// DefaultParams returns the ADR-0014 defaults: no attestors (the gov authority
// attests until some are listed) and two active seats per entity.
func DefaultParams() Params {
	return Params{
		Attestors:      []string{},
		MaxActiveSeats: 2,
	}
}

// Validate enforces the bounds: max_active_seats in [1, 3] and at most 10
// distinct saicon1… attestors.
func (p Params) Validate() error {
	if p.MaxActiveSeats < 1 || p.MaxActiveSeats > 3 {
		return fmt.Errorf("max_active_seats must be within [1, 3], got %d", p.MaxActiveSeats)
	}
	if len(p.Attestors) > MaxAttestors {
		return fmt.Errorf("attestors holds %d entries, at most %d", len(p.Attestors), MaxAttestors)
	}
	// Uniqueness is on decoded bytes: bech32 accepts an all-uppercase spelling
	// of the same address.
	seen := make([][]byte, 0, len(p.Attestors))
	for _, a := range p.Attestors {
		bz, err := DecodeAccount(a)
		if err != nil {
			return fmt.Errorf("attestor: %w", err)
		}
		if containsBytes(seen, bz) {
			return fmt.Errorf("attestor %s listed twice", a)
		}
		seen = append(seen, bz)
	}
	return nil
}
