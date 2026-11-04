package types

import (
	"cosmossdk.io/math"
)

// DefaultUnjailCooldownBlocks is the default minimum JAILED duration before a
// governed unjail is allowed: ~1 day at ~2s blocks. Gov-tunable; calibrate later.
const DefaultUnjailCooldownBlocks int64 = 43200

// The bounds of the coordinated-attack parameters are Go code on purpose
// (ADR-0013, tier 2: governance moves a parameter inside bounds it cannot
// move). Changing a bound is a consensus-breaking code change, never a
// proposal; spec §9 and TestGovernanceCannotMoveTheBounds pin this.
const (
	// MinAppealWindowSeconds is the shortest appeal window: 1 day.
	MinAppealWindowSeconds uint64 = 86_400
	// MaxAppealWindowSeconds is the longest appeal window: 14 days.
	MaxAppealWindowSeconds uint64 = 1_209_600
	// DefaultAppealWindowSeconds is the default appeal window: 7 days.
	DefaultAppealWindowSeconds uint64 = 604_800

	// MaxEvidenceHashesPerSanction bounds the offences one sanction names.
	MaxEvidenceHashesPerSanction = 16
	// MaxSanctionReasonBytes bounds the free-text reason of a sanction.
	MaxSanctionReasonBytes = 256
)

// CoordinatedFractionFloor is the lowest coordinated_attack_fraction: 0.10, the
// double-sign fraction the chain starts with. Go code, not a parameter.
func CoordinatedFractionFloor() math.LegacyDec { return math.LegacyNewDecWithPrec(10, 2) }

// CoordinatedFractionCeiling is the highest coordinated_attack_fraction: 1.00.
// Go code, not a parameter.
func CoordinatedFractionCeiling() math.LegacyDec { return math.LegacyOneDec() }

// DefaultCoordinatedAttackFraction is the default coordinated_attack_fraction: 0.30.
func DefaultCoordinatedAttackFraction() math.LegacyDec { return math.LegacyNewDecWithPrec(30, 2) }

// ValidateCoordinatedFraction checks f against the code bounds [0.10, 1.00].
// The stateful floor (never below the chain's current double-sign fraction) is
// checked by MsgUpdateParams and by the sanction plan, not here.
func ValidateCoordinatedFraction(f math.LegacyDec) error {
	if f.IsNil() {
		return ErrInvalidFraction.Wrap("fraction is not set")
	}
	if f.LT(CoordinatedFractionFloor()) || f.GT(CoordinatedFractionCeiling()) {
		return ErrInvalidFraction.Wrapf("%s is outside [%s, %s]", f, CoordinatedFractionFloor(), CoordinatedFractionCeiling())
	}
	return nil
}

// NewParams creates a new Params instance with the default coordinated-attack
// fraction and appeal window.
func NewParams(unjailCooldownBlocks int64) Params {
	return Params{
		UnjailCooldownBlocks:      unjailCooldownBlocks,
		CoordinatedAttackFraction: DefaultCoordinatedAttackFraction(),
		AppealWindowSeconds:       DefaultAppealWindowSeconds,
	}
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
	if err := ValidateCoordinatedFraction(p.CoordinatedAttackFraction); err != nil {
		return err
	}
	if p.AppealWindowSeconds < MinAppealWindowSeconds || p.AppealWindowSeconds > MaxAppealWindowSeconds {
		return ErrInvalidParams.Wrapf("appeal_window_seconds must be in [%d, %d], got %d",
			MinAppealWindowSeconds, MaxAppealWindowSeconds, p.AppealWindowSeconds)
	}
	return nil
}
