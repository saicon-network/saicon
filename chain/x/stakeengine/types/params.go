package types

import (
	"fmt"

	"cosmossdk.io/math"
	authtypes "github.com/cosmos/cosmos-sdk/x/auth/types"
)

// scn is one SCN in nscn.
const scn int64 = 100_000_000

// EntryFloorNscn is the minimum OWN self-bond a validator must bring to enter:
// 2,000 SCN (project decision 2026-09-22). Tier 1 under ADR-0013: a code constant,
// like the 60-day lock-up in x/validatorregistry/types/keys.go, so governance
// cannot lower it — there is no parameter to set. Raising it is a binary change.
const EntryFloorNscn int64 = 2_000 * scn

// EntryFloor is EntryFloorNscn as a math.Int.
func EntryFloor() math.Int { return math.NewInt(EntryFloorNscn) }

// maxValidatorCapFloorNscn bounds validator_cap_floor: 2,100,000 SCN, 10% of max
// supply. A floor this large disables the 2% rule in practice; the production
// value and a tighter bound are an owner review item before testnet.
const maxValidatorCapFloorNscn int64 = 2_100_000 * scn

// MinDelegationDefault is the Register's minimum delegation: 100 SCN (v1
// carry-over, DEVELOPMENT.md §2.8).
func MinDelegationDefault() math.Int { return math.NewInt(100 * scn) }

// maxMinDelegationNscn bounds min_delegation: 10,000 SCN.
const maxMinDelegationNscn int64 = 10_000 * scn

// MaxValidatorsPerDelegatorBound is the upper bound of max_validators_per_delegator
// (Validate). The rule holds from genesis and the parameter can never exceed
// this, so no delegator holds more validators than the bound, whatever the
// parameter is later lowered to.
const MaxValidatorsPerDelegatorBound uint32 = 5

// DefaultParams returns the spec defaults (2026-10-04 design).
func DefaultParams() Params {
	return Params{
		MaxSupplyFraction:         math.LegacyNewDecWithPrec(8, 2),
		PublicCirculatingFraction: math.LegacyNewDecWithPrec(60, 2),
		ValidatorCapFraction:      math.LegacyNewDecWithPrec(2, 2),
		ValidatorCapFloor:         math.ZeroInt(),
		EwmaWindowEpochs:          30,
		MinQueueEntry:             MinDelegationDefault(),
		MaxQueueEntries:           10_000,
		MaxFillsPerBlock:          50,
		ExcludedAddresses:         []string{},
		MaxValidatorsPerDelegator: 3,
		MinDelegation:             MinDelegationDefault(),
	}
}

// Validate enforces the tier-2 bounds. ADR-0013: a bound kept by convention is
// not a bound, so each lives here, where MsgUpdateParams calls it.
func (p Params) Validate() error {
	decs := []struct {
		name   string
		v      math.LegacyDec
		lo, hi math.LegacyDec
	}{
		{"max_supply_fraction", p.MaxSupplyFraction, math.LegacyNewDecWithPrec(5, 2), math.LegacyNewDecWithPrec(10, 2)},
		{"public_circulating_fraction", p.PublicCirculatingFraction, math.LegacyNewDecWithPrec(30, 2), math.LegacyNewDecWithPrec(80, 2)},
		{"validator_cap_fraction", p.ValidatorCapFraction, math.LegacyNewDecWithPrec(1, 2), math.LegacyNewDecWithPrec(5, 2)},
	}
	for _, d := range decs {
		if d.v.IsNil() {
			return fmt.Errorf("%s is not set", d.name)
		}
		if d.v.LT(d.lo) || d.v.GT(d.hi) {
			return fmt.Errorf("%s must be within [%s, %s], got %s", d.name, d.lo, d.hi, d.v)
		}
	}
	if p.ValidatorCapFloor.IsNil() || p.ValidatorCapFloor.IsNegative() ||
		p.ValidatorCapFloor.GT(math.NewInt(maxValidatorCapFloorNscn)) {
		return fmt.Errorf("validator_cap_floor must be within [0, %d] nscn, got %s", maxValidatorCapFloorNscn, p.ValidatorCapFloor)
	}
	if p.EwmaWindowEpochs < 1 || p.EwmaWindowEpochs > 365 {
		return fmt.Errorf("ewma_window_epochs must be within [1, 365], got %d", p.EwmaWindowEpochs)
	}
	if p.MinQueueEntry.IsNil() || p.MinQueueEntry.LT(math.NewInt(scn)) {
		return fmt.Errorf("min_queue_entry must be at least %d nscn (1 SCN), got %s", scn, p.MinQueueEntry)
	}
	if p.MaxQueueEntries < 1 || p.MaxQueueEntries > 100_000 {
		return fmt.Errorf("max_queue_entries must be within [1, 100000], got %d", p.MaxQueueEntries)
	}
	if p.MaxFillsPerBlock < 1 || p.MaxFillsPerBlock > 500 {
		return fmt.Errorf("max_fills_per_block must be within [1, 500], got %d", p.MaxFillsPerBlock)
	}
	if p.MaxValidatorsPerDelegator < 2 || p.MaxValidatorsPerDelegator > MaxValidatorsPerDelegatorBound {
		return fmt.Errorf("max_validators_per_delegator must be within [2, %d], got %d", MaxValidatorsPerDelegatorBound, p.MaxValidatorsPerDelegator)
	}
	if p.MinDelegation.IsNil() || p.MinDelegation.LT(math.NewInt(scn)) || p.MinDelegation.GT(math.NewInt(maxMinDelegationNscn)) {
		return fmt.Errorf("min_delegation must be within [%d, %d] nscn (1 – 10,000 SCN), got %s", scn, maxMinDelegationNscn, p.MinDelegation)
	}
	if len(p.ExcludedAddresses) > 100 {
		return fmt.Errorf("excluded_addresses holds at most 100 entries, got %d", len(p.ExcludedAddresses))
	}
	// Uniqueness is on the decoded bytes: bech32 accepts an all-uppercase
	// spelling, so comparing strings would let one account appear twice.
	// A module account in NonCirculatingModules is already subtracted from public
	// circulating supply; listing it as an excluded address would subtract it twice.
	moduleAddrs := make(map[string]string, len(NonCirculatingModules))
	for _, name := range NonCirculatingModules {
		moduleAddrs[string(authtypes.NewModuleAddress(name))] = name
	}
	seen := make(map[string]bool, len(p.ExcludedAddresses))
	for _, a := range p.ExcludedAddresses {
		bz, err := DecodeAccount(a)
		if err != nil {
			return fmt.Errorf("excluded_addresses: %w", err)
		}
		if name, ok := moduleAddrs[string(bz)]; ok {
			return fmt.Errorf("excluded_addresses: %s is the %s module account, already subtracted from public circulating supply", a, name)
		}
		if seen[string(bz)] {
			return fmt.Errorf("excluded_addresses: %s appears twice", a)
		}
		seen[string(bz)] = true
	}
	return nil
}
