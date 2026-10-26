package types

import (
	"fmt"

	stakingtypes "github.com/cosmos/cosmos-sdk/x/staking/types"
)

// String returns the human-readable lowercase form of Status.
// Used in CLI and API responses. See ADR-0003.
func (s Status) HumanString() string {
	switch s {
	case Status_STATUS_BONDING:
		return "bonding"
	case Status_STATUS_ACTIVE:
		return "active"
	case Status_STATUS_QUARANTINED:
		return "quarantined"
	case Status_STATUS_JAILED:
		return "jailed"
	case Status_STATUS_UNBONDING:
		return "unbonding"
	case Status_STATUS_INACTIVE:
		return "inactive"
	default:
		return "unknown"
	}
}

// StatusFromString parses a CLI/API status string into Status enum.
// Returns Status_STATUS_UNSPECIFIED and an error for unknown values.
func StatusFromString(s string) (Status, error) {
	switch s {
	case "bonding":
		return Status_STATUS_BONDING, nil
	case "active":
		return Status_STATUS_ACTIVE, nil
	case "quarantined":
		return Status_STATUS_QUARANTINED, nil
	case "jailed":
		return Status_STATUS_JAILED, nil
	case "unbonding":
		return Status_STATUS_UNBONDING, nil
	case "inactive":
		return Status_STATUS_INACTIVE, nil
	default:
		return Status_STATUS_UNSPECIFIED, fmt.Errorf("unknown validator status: %q", s)
	}
}

// ToBondStatus maps SAiCoN Status to Cosmos SDK BondStatus (lossy many-to-one).
// See ADR-0003 §Mapping for rationale.
func (s Status) ToBondStatus() stakingtypes.BondStatus {
	switch s {
	case Status_STATUS_ACTIVE:
		return stakingtypes.Bonded
	case Status_STATUS_UNBONDING:
		return stakingtypes.Unbonding
	case Status_STATUS_BONDING,
		Status_STATUS_QUARANTINED,
		Status_STATUS_JAILED,
		Status_STATUS_INACTIVE:
		return stakingtypes.Unbonded
	default:
		return stakingtypes.Unspecified
	}
}

// IsValid reports whether s is a valid (non-UNSPECIFIED) Status value.
func (s Status) IsValid() bool {
	return s >= Status_STATUS_BONDING && s <= Status_STATUS_INACTIVE
}

// IsActive reports whether the validator is in the active set.
func (s Status) IsActive() bool {
	return s == Status_STATUS_ACTIVE
}

// IsRewardEligibleByStatus reports whether the status alone permits rewards.
// The full reward gate (status + C + EP + not_jailed + not_grace) is in x/rewards.
func (s Status) IsRewardEligibleByStatus() bool {
	return s == Status_STATUS_ACTIVE
}

// CanReceiveDelegations reports whether the status alone permits new delegations.
// Full eligibility check (including cap policy) is in x/delegationrouter.
func (s Status) CanReceiveDelegations() bool {
	return s == Status_STATUS_ACTIVE
}

// StatusDisplayOrder is the recommended ordering for CLI status displays.
// Numeric enum values are NOT lifecycle-ordered (e.g. BONDING=1, ACTIVE=2 doesn't
// mean active is "more advanced" — see ADR-0003 §Ordering).
var StatusDisplayOrder = []Status{
	Status_STATUS_ACTIVE,
	Status_STATUS_BONDING,
	Status_STATUS_UNBONDING,
	Status_STATUS_QUARANTINED,
	Status_STATUS_JAILED,
	Status_STATUS_INACTIVE,
}
