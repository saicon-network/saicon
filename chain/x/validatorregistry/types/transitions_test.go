package types_test

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/saicon-network/saicon/chain/x/validatorregistry/types"
)

func TestCanTransition_Allowed(t *testing.T) {
	allowed := [][2]types.Status{
		{types.Status_STATUS_BONDING, types.Status_STATUS_ACTIVE},
		{types.Status_STATUS_BONDING, types.Status_STATUS_INACTIVE},
		{types.Status_STATUS_ACTIVE, types.Status_STATUS_QUARANTINED},
		{types.Status_STATUS_ACTIVE, types.Status_STATUS_JAILED},
		{types.Status_STATUS_ACTIVE, types.Status_STATUS_UNBONDING},
		{types.Status_STATUS_QUARANTINED, types.Status_STATUS_ACTIVE},
		{types.Status_STATUS_QUARANTINED, types.Status_STATUS_JAILED},
		{types.Status_STATUS_QUARANTINED, types.Status_STATUS_UNBONDING},
		{types.Status_STATUS_JAILED, types.Status_STATUS_ACTIVE},
		{types.Status_STATUS_JAILED, types.Status_STATUS_UNBONDING},
		// V-5: a jailed validator that fully unbonds is removed by x/staking and
		// arrives at AfterValidatorRemoved straight from JAILED — jail must not be
		// a state with no exit.
		{types.Status_STATUS_JAILED, types.Status_STATUS_INACTIVE},
		{types.Status_STATUS_UNBONDING, types.Status_STATUS_INACTIVE},
		// V-5 follow-up (D4): removal by x/staking is ground truth, so EVERY
		// non-terminal status must be able to record the exit. Without these a
		// record whose validator staking already deleted is stranded reporting it
		// as live.
		{types.Status_STATUS_ACTIVE, types.Status_STATUS_INACTIVE},
		{types.Status_STATUS_QUARANTINED, types.Status_STATUS_INACTIVE},
		// V-5 follow-up (D1, BLOCKER): an aborted exit — the ordinary
		// downtime-jail-then-recover cycle — must be able to resume service.
		// ActivateOnBond picks which, fail-closed, from pre_unbonding_status.
		{types.Status_STATUS_UNBONDING, types.Status_STATUS_ACTIVE},
		{types.Status_STATUS_UNBONDING, types.Status_STATUS_QUARANTINED},
	}
	require.Len(t, allowed, 16, "the transition table defines exactly 16 allowed transitions")
	for _, tr := range allowed {
		require.Truef(t, types.CanTransition(tr[0], tr[1]),
			"expected %s -> %s to be allowed", tr[0].HumanString(), tr[1].HumanString())
	}
}

// TestCanTransition_ExhaustiveClosure walks every ordered pair of statuses and
// asserts the table contains EXACTLY the documented edges — nothing more. The
// allow-list test above only checks the length of its own literal slice, so an
// accidentally-opened edge (e.g. UNBONDING -> QUARANTINED) would slip past it.
func TestCanTransition_ExhaustiveClosure(t *testing.T) {
	all := []types.Status{
		types.Status_STATUS_BONDING,
		types.Status_STATUS_ACTIVE,
		types.Status_STATUS_QUARANTINED,
		types.Status_STATUS_JAILED,
		types.Status_STATUS_UNBONDING,
		types.Status_STATUS_INACTIVE,
	}
	allowed := map[[2]types.Status]bool{
		{types.Status_STATUS_BONDING, types.Status_STATUS_ACTIVE}:        true,
		{types.Status_STATUS_BONDING, types.Status_STATUS_INACTIVE}:      true,
		{types.Status_STATUS_ACTIVE, types.Status_STATUS_QUARANTINED}:    true,
		{types.Status_STATUS_ACTIVE, types.Status_STATUS_JAILED}:         true,
		{types.Status_STATUS_ACTIVE, types.Status_STATUS_UNBONDING}:      true,
		{types.Status_STATUS_QUARANTINED, types.Status_STATUS_ACTIVE}:    true,
		{types.Status_STATUS_QUARANTINED, types.Status_STATUS_JAILED}:    true,
		{types.Status_STATUS_QUARANTINED, types.Status_STATUS_UNBONDING}: true,
		{types.Status_STATUS_JAILED, types.Status_STATUS_ACTIVE}:         true,
		{types.Status_STATUS_JAILED, types.Status_STATUS_UNBONDING}:      true,
		{types.Status_STATUS_JAILED, types.Status_STATUS_INACTIVE}:       true,
		{types.Status_STATUS_UNBONDING, types.Status_STATUS_INACTIVE}:    true,
		{types.Status_STATUS_ACTIVE, types.Status_STATUS_INACTIVE}:       true,
		{types.Status_STATUS_QUARANTINED, types.Status_STATUS_INACTIVE}:  true,
		{types.Status_STATUS_UNBONDING, types.Status_STATUS_ACTIVE}:      true,
		{types.Status_STATUS_UNBONDING, types.Status_STATUS_QUARANTINED}: true,
	}
	require.Len(t, allowed, 16)

	for _, from := range all {
		for _, to := range all {
			want := allowed[[2]types.Status{from, to}]
			require.Equalf(t, want, types.CanTransition(from, to),
				"edge %s -> %s: expected allowed=%v", from.HumanString(), to.HumanString(), want)
		}
	}
}

func TestCanTransition_ForbiddenSelfLoops(t *testing.T) {
	for _, s := range []types.Status{
		types.Status_STATUS_BONDING,
		types.Status_STATUS_ACTIVE,
		types.Status_STATUS_QUARANTINED,
		types.Status_STATUS_JAILED,
		types.Status_STATUS_UNBONDING,
		types.Status_STATUS_INACTIVE,
	} {
		require.Falsef(t, types.CanTransition(s, s), "self-loop %s -> %s must be forbidden", s.HumanString(), s.HumanString())
	}
}

func TestCanTransition_ForbiddenExplicit(t *testing.T) {
	// §2.3 explicit forbidden + representative illegal edges.
	forbidden := [][2]types.Status{
		{types.Status_STATUS_INACTIVE, types.Status_STATUS_BONDING}, // re-onboarding forbidden
		// UNBONDING -> ACTIVE / QUARANTINED are now allowed (an aborted exit
		// resumes service); JAILED from UNBONDING is not — jailing goes through
		// x/compliance on a live validator, never on one already leaving.
		{types.Status_STATUS_UNBONDING, types.Status_STATUS_JAILED},
		{types.Status_STATUS_BONDING, types.Status_STATUS_QUARANTINED}, // no compliance events in bonding
		{types.Status_STATUS_BONDING, types.Status_STATUS_JAILED},
		{types.Status_STATUS_INACTIVE, types.Status_STATUS_ACTIVE},
		{types.Status_STATUS_JAILED, types.Status_STATUS_QUARANTINED},
	}
	for _, tr := range forbidden {
		require.Falsef(t, types.CanTransition(tr[0], tr[1]),
			"expected %s -> %s to be forbidden", tr[0].HumanString(), tr[1].HumanString())
	}
}

func TestCanTransition_UnspecifiedRejected(t *testing.T) {
	require.False(t, types.CanTransition(types.Status_STATUS_UNSPECIFIED, types.Status_STATUS_ACTIVE))
	require.False(t, types.CanTransition(types.Status_STATUS_ACTIVE, types.Status_STATUS_UNSPECIFIED))
	require.False(t, types.CanTransition(types.Status_STATUS_UNSPECIFIED, types.Status_STATUS_UNSPECIFIED))
}
