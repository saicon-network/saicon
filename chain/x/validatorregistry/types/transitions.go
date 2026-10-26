package types

// allowedTransitions mirrors the validator status transition table from
// chain/docs/validators/README.md §2.2 (allowed) and §2.3 (forbidden),
// row for row.
//
// Encoding rules:
//   - A present from→to edge means the transition is allowed.
//   - STATUS_INACTIVE is terminal: it has no outgoing edges (inactive→bonding
//     is forbidden per §2.3 — re-onboarding is a fresh registration).
//   - Self-loops are absent, so CanTransition(x, x) == false for all x.
//   - Any pair not present is forbidden (invariant V-1).
var allowedTransitions = map[Status]map[Status]struct{}{
	Status_STATUS_BONDING: {
		Status_STATUS_ACTIVE:   {},
		Status_STATUS_INACTIVE: {},
	},
	Status_STATUS_ACTIVE: {
		Status_STATUS_QUARANTINED: {},
		Status_STATUS_JAILED:      {},
		Status_STATUS_UNBONDING:   {},
		// V-5 follow-up: x/staking removing a validator is GROUND TRUTH. The
		// registry must always be able to record that the validator is gone; a
		// record with no path to INACTIVE becomes a ghost reporting a deleted
		// validator as live. Reachable today: gov unjail moves JAILED -> ACTIVE
		// while staking still has the validator unbonding, and it is then removed
		// straight from ACTIVE. The cooldown guarantee rests on
		// UnstakeCooldownInvariant, not on the shape of this table.
		Status_STATUS_INACTIVE: {},
	},
	Status_STATUS_QUARANTINED: {
		Status_STATUS_ACTIVE:    {},
		Status_STATUS_JAILED:    {},
		Status_STATUS_UNBONDING: {},
		// Same reason as ACTIVE -> INACTIVE above.
		Status_STATUS_INACTIVE: {},
	},
	Status_STATUS_JAILED: {
		Status_STATUS_ACTIVE:    {},
		Status_STATUS_UNBONDING: {},
		// V-5: a jailed validator that fully unbonds is deleted by x/staking, which
		// fires AfterValidatorRemoved straight from JAILED (the jail path never sets
		// STATUS_UNBONDING — see the exit-lifecycle design). Without this edge jail
		// would be a state with no exit.
		Status_STATUS_INACTIVE: {},
	},
	Status_STATUS_UNBONDING: {
		Status_STATUS_INACTIVE: {},
		// V-5 follow-up (D1, BLOCKER): leaving the bonded set is NOT irreversible.
		// An ordinary downtime jail routes a validator through
		// AfterValidatorBeginUnbonding, so the registry marks it UNBONDING; when the
		// operator repairs the node and the validator re-bonds, service must resume.
		// Without these edges the record stayed UNBONDING forever — no participation
		// score, no EP, no rewards, and not even the governed unjail could reach it.
		//
		// Which of the two applies is decided by ActivateOnBond from the recorded
		// pre_unbonding_status, fail-closed: a quarantine must not be laundered by
		// unbonding and re-bonding.
		Status_STATUS_ACTIVE:      {},
		Status_STATUS_QUARANTINED: {},
	},
	// STATUS_INACTIVE: terminal — no outgoing edges.
}

// CanTransition reports whether a validator may move from status `from` to
// status `to` per the V-1 transition table (§2.2/§2.3).
//
// Returns false if either status is invalid (incl. STATUS_UNSPECIFIED), if
// `from` is terminal, or if the from→to edge is not in the allowed table.
// Self-transitions are never allowed.
func CanTransition(from, to Status) bool {
	if !from.IsValid() || !to.IsValid() {
		return false
	}
	tos, ok := allowedTransitions[from]
	if !ok {
		return false
	}
	_, ok = tos[to]
	return ok
}
