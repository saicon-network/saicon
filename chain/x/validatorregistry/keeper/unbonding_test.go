package keeper_test

import (
	"testing"
	"time"

	sdk "github.com/cosmos/cosmos-sdk/types"
	"github.com/stretchr/testify/require"

	"github.com/saicon-network/saicon/chain/x/validatorregistry/types"
)

// completion is the instant the staking record says the cooldown ends.
var v5Completion = time.Date(2026, 8, 1, 12, 0, 0, 0, time.UTC)

func v5Fixture(t *testing.T, stub stubStakingKeeper) *fixture {
	t.Helper()
	f := initFixtureWithStaking(t, stub)
	f.ctx = sdk.UnwrapSDKContext(f.ctx).WithBlockTime(time.Date(2026, 7, 11, 12, 0, 0, 0, time.UTC))
	return f
}

// --- BeginUnbonding --------------------------------------------------------

// ACTIVE and QUARANTINED move to UNBONDING and get both timestamps.
func TestBeginUnbonding_ActiveAndQuarantinedMoveToUnbonding(t *testing.T) {
	for _, from := range []types.Status{types.Status_STATUS_ACTIVE, types.Status_STATUS_QUARANTINED} {
		t.Run(from.HumanString(), func(t *testing.T) {
			f := v5Fixture(t, stubStakingKeeper{validatorUnbondingTime: v5Completion})
			addr := seedValidator(t, f, "v5-"+from.HumanString(), from)

			require.NoError(t, f.keeper.BeginUnbonding(f.ctx, addr))

			md, found, err := f.keeper.GetValidatorMetadata(f.ctx, addr)
			require.NoError(t, err)
			require.True(t, found)
			require.Equal(t, types.Status_STATUS_UNBONDING, md.Status)
			require.NotNil(t, md.UnbondingStart, "unbonding_start must be recorded")
			require.Equal(t, v5Completion, md.UnbondingCompleteTime, "completion is frozen from the staking record")
		})
	}
}

// A JAILED validator keeps its status but still gets the exit timing recorded —
// without it the JAILED -> INACTIVE exit would be unverifiable by V-5.
func TestBeginUnbonding_JailedKeepsStatusButRecordsTiming(t *testing.T) {
	f := v5Fixture(t, stubStakingKeeper{validatorUnbondingTime: v5Completion})
	addr := seedValidator(t, f, "v5-jailed", types.Status_STATUS_JAILED)

	require.NoError(t, f.keeper.BeginUnbonding(f.ctx, addr))

	md, found, err := f.keeper.GetValidatorMetadata(f.ctx, addr)
	require.NoError(t, err)
	require.True(t, found)
	require.Equal(t, types.Status_STATUS_JAILED, md.Status, "a jailed validator must stay jailed")
	require.NotNil(t, md.UnbondingStart, "timing is recorded even while jailed")
	require.Equal(t, v5Completion, md.UnbondingCompleteTime)
}

// REGRESSION (adversarial review, CRITICAL): an operator may re-create a validator
// at the same valoper address after x/staking removed it, leaving a stale INACTIVE
// record that ActivateOnBond deliberately skips. If that validator later leaves the
// bonded set, BeginUnbonding must NOT stamp a future completion instant onto the
// terminal record — doing so made UnstakeCooldownInvariant panic in the very same
// EndBlock (network-wide, deterministic, surviving restart).
func TestBeginUnbonding_InactiveIsUntouchedAndCannotArmTheInvariant(t *testing.T) {
	f := v5Fixture(t, stubStakingKeeper{validatorUnbondingTime: v5Completion})
	addr := seedValidator(t, f, "v5-stale-inactive", types.Status_STATUS_INACTIVE)

	require.NoError(t, f.keeper.BeginUnbonding(f.ctx, addr))

	md, found, err := f.keeper.GetValidatorMetadata(f.ctx, addr)
	require.NoError(t, err)
	require.True(t, found)
	require.Equal(t, types.Status_STATUS_INACTIVE, md.Status)
	require.True(t, md.UnbondingCompleteTime.IsZero(),
		"a terminal INACTIVE record must not receive a completion instant")
	require.Nil(t, md.UnbondingStart, "a terminal INACTIVE record must not receive an unbonding start")

	// The whole point: the invariant must stay clean, i.e. no halt.
	_, broken := f.keeper.UnstakeCooldownInvariant(f.ctx)
	require.False(t, broken, "BeginUnbonding on an INACTIVE record must never arm the V-5 panic")
}

// A BONDING validator is not in the bonded set, so it cannot legitimately be
// leaving it; the hook must leave the record alone rather than stamp timings that
// a later BONDING -> INACTIVE exit would trip over.
func TestBeginUnbonding_BondingIsUntouched(t *testing.T) {
	f := v5Fixture(t, stubStakingKeeper{validatorUnbondingTime: v5Completion})
	addr := seedValidator(t, f, "v5-bonding", types.Status_STATUS_BONDING)

	require.NoError(t, f.keeper.BeginUnbonding(f.ctx, addr))

	md, _, err := f.keeper.GetValidatorMetadata(f.ctx, addr)
	require.NoError(t, err)
	require.Equal(t, types.Status_STATUS_BONDING, md.Status)
	require.True(t, md.UnbondingCompleteTime.IsZero())
	require.Nil(t, md.UnbondingStart)
}

// --- ResetOnValidatorCreated (re-onboarding = fresh registration) -----------

// The root cause behind the CRITICAL halt: after a full exit the registry kept a
// terminal record, and x/staking allows re-creating a validator at the same
// address. Creation must discard the TERMINAL record so the following bond
// registers the validator afresh instead of leaving it a permanently-INACTIVE
// ghost that can never earn.
func TestResetOnValidatorCreated_DiscardsTerminalRecord(t *testing.T) {
	f := v5Fixture(t, stubStakingKeeper{})
	addr := seedValidator(t, f, "v5-recreate-inactive", types.Status_STATUS_INACTIVE)

	require.NoError(t, f.keeper.ResetOnValidatorCreated(f.ctx, addr))

	_, found, err := f.keeper.GetValidatorMetadata(f.ctx, addr)
	require.NoError(t, err)
	require.False(t, found, "a terminal record must be discarded on re-creation")

	// The audit trail survives the deletion as an event.
	var emitted bool
	for _, e := range sdk.UnwrapSDKContext(f.ctx).EventManager().Events() {
		if e.Type == types.EventTypeRegistryReset {
			emitted = true
		}
	}
	require.True(t, emitted, "expected a validator_registry_reset event")
}

// D3 (MEDIUM) regression: the reset must NOT touch a live record.
//
// Deleting indiscriminately made an ordinary sequence destructive: an operator
// who pre-registers via MsgRegisterValidator (a BONDING record carrying a FROZEN
// custom lock-up — the proto calls it frozen for that validator) and then submits
// the staking MsgCreateValidator had that registration silently deleted, and the
// following bond re-created it with the 180-day default and compliance reset to
// true. A JAILED record is likewise a live sanction, not a leftover.
func TestResetOnValidatorCreated_PreservesLiveRecord(t *testing.T) {
	const fiveYears = int64(5 * 365 * 24 * 60 * 60)

	for _, live := range []types.Status{
		types.Status_STATUS_BONDING, // pre-registration via MsgRegisterValidator
		types.Status_STATUS_JAILED,  // an active sanction
	} {
		t.Run(live.HumanString(), func(t *testing.T) {
			f := v5Fixture(t, stubStakingKeeper{})
			addr := seedValidator(t, f, "v5-live-"+live.HumanString(), live)

			// Give it a frozen custom lock-up and a non-default compliance flag,
			// the two values a blind reset would silently revert.
			md, _, err := f.keeper.GetValidatorMetadata(f.ctx, addr)
			require.NoError(t, err)
			md.LockupDurationSeconds = fiveYears
			md.ComplianceFlag = false
			require.NoError(t, f.keeper.SetValidatorMetadata(f.ctx, addr, md))

			require.NoError(t, f.keeper.ResetOnValidatorCreated(f.ctx, addr))

			got, found, err := f.keeper.GetValidatorMetadata(f.ctx, addr)
			require.NoError(t, err)
			require.True(t, found, "a live record must survive validator creation")
			require.Equal(t, live, got.Status, "status must be untouched")
			require.Equal(t, fiveYears, got.LockupDurationSeconds,
				"a frozen custom lock-up must not silently revert to the default")
			require.False(t, got.ComplianceFlag, "a sanction must not be cleared")

			for _, e := range sdk.UnwrapSDKContext(f.ctx).EventManager().Events() {
				require.NotEqual(t, types.EventTypeRegistryReset, e.Type,
					"no reset event may be emitted when nothing was reset")
			}
		})
	}
}

// A first-time creation (and genesis, where the hook fires before any bond) has no
// metadata and must be a silent no-op.
func TestResetOnValidatorCreated_FirstTimeIsNoop(t *testing.T) {
	f := v5Fixture(t, stubStakingKeeper{})
	fresh := sdk.ValAddress([]byte("v5-first-time-valida"))[:20]

	require.NoError(t, f.keeper.ResetOnValidatorCreated(f.ctx, fresh))

	_, found, err := f.keeper.GetValidatorMetadata(f.ctx, fresh)
	require.NoError(t, err)
	require.False(t, found)
}

// End-to-end of the re-onboarding path: exit -> re-create -> bond must yield a
// live ACTIVE validator, not a ghost. This is the behaviour the fix exists for.
func TestRecreateAfterExit_YieldsFreshActiveValidator(t *testing.T) {
	f := v5Fixture(t, stubStakingKeeper{})
	addr := seedValidator(t, f, "v5-full-recreate", types.Status_STATUS_UNBONDING)

	// staking removes the fully-unbonded validator
	require.NoError(t, f.keeper.CompleteExit(f.ctx, addr))
	md, _, err := f.keeper.GetValidatorMetadata(f.ctx, addr)
	require.NoError(t, err)
	require.Equal(t, types.Status_STATUS_INACTIVE, md.Status)

	// the operator re-creates the validator at the same address, then it bonds
	require.NoError(t, f.keeper.ResetOnValidatorCreated(f.ctx, addr))
	require.NoError(t, f.keeper.ActivateOnBond(f.ctx, addr))

	md, found, err := f.keeper.GetValidatorMetadata(f.ctx, addr)
	require.NoError(t, err)
	require.True(t, found)
	require.Equal(t, types.Status_STATUS_ACTIVE, md.Status,
		"a re-created validator must come back ACTIVE, not stay a terminal ghost")
	require.True(t, md.UnbondingCompleteTime.IsZero(), "fresh registration carries no exit timing")
	require.Nil(t, md.UnbondingStart)
}

// --- D1 (BLOCKER): an aborted exit must resume service ----------------------

// The whole reason this fix exists. An ordinary downtime jail is NOT a SAiCoN
// compliance event — the slash fraction is far below the double-sign threshold,
// so the registry keeps saying ACTIVE — yet x/staking still drops the validator
// from the bonded set, which drives the registry to UNBONDING. When the operator
// repairs the node and re-bonds, service must resume. Before this fix the record
// stayed UNBONDING forever: no participation score, no EP, no rewards, and the
// governed unjail could not reach it either (it requires STATUS_JAILED).
func TestDowntimeJailThenRecover_RestoresActive(t *testing.T) {
	f := v5Fixture(t, stubStakingKeeper{validatorUnbondingTime: v5Completion})
	addr := seedValidator(t, f, "v5-downtime-cycle", types.Status_STATUS_ACTIVE)

	// staking drops it from the bonded set (the jail path)
	require.NoError(t, f.keeper.BeginUnbonding(f.ctx, addr))
	md, _, err := f.keeper.GetValidatorMetadata(f.ctx, addr)
	require.NoError(t, err)
	require.Equal(t, types.Status_STATUS_UNBONDING, md.Status)
	require.Equal(t, types.Status_STATUS_ACTIVE, md.PreUnbondingStatus,
		"what it was leaving must be recorded, not guessed at on the way back")

	// operator repairs the node, staking MsgUnjail, validator re-bonds
	require.NoError(t, f.keeper.ActivateOnBond(f.ctx, addr))

	md, _, err = f.keeper.GetValidatorMetadata(f.ctx, addr)
	require.NoError(t, err)
	require.Equal(t, types.Status_STATUS_ACTIVE, md.Status,
		"a repaired validator must earn again; UNBONDING forever was the BLOCKER")
	require.Nil(t, md.UnbondingStart, "the aborted exit must leave no timing behind")
	require.True(t, md.UnbondingCompleteTime.IsZero(),
		"a stale future completion on a live record is what armed the earlier CRITICAL")
	require.Equal(t, types.Status_STATUS_UNSPECIFIED, md.PreUnbondingStatus)

	// and the V-5 invariant stays clean throughout
	_, broken := f.keeper.UnstakeCooldownInvariant(f.ctx)
	require.False(t, broken)
}

// A quarantined validator must NOT launder its sanction by unbonding and
// re-bonding: it comes back QUARANTINED, not ACTIVE.
func TestAbortedExit_QuarantineSurvivesTheRoundTrip(t *testing.T) {
	f := v5Fixture(t, stubStakingKeeper{validatorUnbondingTime: v5Completion})
	addr := seedValidator(t, f, "v5-quarantine-cycle", types.Status_STATUS_QUARANTINED)

	require.NoError(t, f.keeper.BeginUnbonding(f.ctx, addr))
	require.NoError(t, f.keeper.ActivateOnBond(f.ctx, addr))

	md, _, err := f.keeper.GetValidatorMetadata(f.ctx, addr)
	require.NoError(t, err)
	require.Equal(t, types.Status_STATUS_QUARANTINED, md.Status,
		"unbond-then-rebond must not wash off a quarantine")
}

// The restore is fail-CLOSED on a conjunction. Neither signal is trusted alone:
// pre_unbonding_status is absent on records that entered unbonding before the
// field existed, and the compliance mirror is hardcoded true on two write paths
// with its V-4.1 invariant unenforced.
func TestAbortedExit_RestoreIsFailClosed(t *testing.T) {
	for _, tc := range []struct {
		name       string
		preStatus  types.Status
		compliant  bool
		wantStatus types.Status
	}{
		{"both signals clean", types.Status_STATUS_ACTIVE, true, types.Status_STATUS_ACTIVE},
		{"mirror says sanctioned", types.Status_STATUS_ACTIVE, false, types.Status_STATUS_QUARANTINED},
		{"pre-status was quarantined", types.Status_STATUS_QUARANTINED, true, types.Status_STATUS_QUARANTINED},
		{"pre-status not recorded (legacy)", types.Status_STATUS_UNSPECIFIED, true, types.Status_STATUS_QUARANTINED},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := v5Fixture(t, stubStakingKeeper{})
			addr := seedValidator(t, f, "v5-failclosed-"+tc.name[:8], types.Status_STATUS_UNBONDING)

			md, _, err := f.keeper.GetValidatorMetadata(f.ctx, addr)
			require.NoError(t, err)
			md.PreUnbondingStatus = tc.preStatus
			md.ComplianceFlag = tc.compliant
			require.NoError(t, f.keeper.SetValidatorMetadata(f.ctx, addr, md))

			require.NoError(t, f.keeper.ActivateOnBond(f.ctx, addr))

			got, _, err := f.keeper.GetValidatorMetadata(f.ctx, addr)
			require.NoError(t, err)
			require.Equal(t, tc.wantStatus, got.Status)
		})
	}
}

// No registry metadata -> the hook must be a silent no-op.
func TestBeginUnbonding_UnknownValidatorIsNoop(t *testing.T) {
	f := v5Fixture(t, stubStakingKeeper{validatorUnbondingTime: v5Completion})
	unknown := sdk.ValAddress([]byte("v5-unknown-validator"))[:20]

	require.NoError(t, f.keeper.BeginUnbonding(f.ctx, unknown))

	_, found, err := f.keeper.GetValidatorMetadata(f.ctx, unknown)
	require.NoError(t, err)
	require.False(t, found)
}

// When the staking record cannot be read, fall back to blockTime + params.
func TestBeginUnbonding_FallsBackToParamsWhenValidatorMissing(t *testing.T) {
	const period = 21 * 24 * time.Hour
	f := v5Fixture(t, stubStakingKeeper{validatorMissing: true, paramsUnbondingTime: period})
	addr := seedValidator(t, f, "v5-fallback", types.Status_STATUS_ACTIVE)
	now := sdk.UnwrapSDKContext(f.ctx).BlockTime()

	require.NoError(t, f.keeper.BeginUnbonding(f.ctx, addr))

	md, _, err := f.keeper.GetValidatorMetadata(f.ctx, addr)
	require.NoError(t, err)
	require.Equal(t, now.Add(period), md.UnbondingCompleteTime)
}

// --- CompleteExit ----------------------------------------------------------

// Both legal sources reach INACTIVE: the normal UNBONDING exit and the jailed exit.
func TestCompleteExit_FromUnbondingAndJailed(t *testing.T) {
	for _, from := range []types.Status{types.Status_STATUS_UNBONDING, types.Status_STATUS_JAILED} {
		t.Run(from.HumanString(), func(t *testing.T) {
			f := v5Fixture(t, stubStakingKeeper{})
			addr := seedValidator(t, f, "v5-exit-"+from.HumanString(), from)

			require.NoError(t, f.keeper.CompleteExit(f.ctx, addr))

			md, _, err := f.keeper.GetValidatorMetadata(f.ctx, addr)
			require.NoError(t, err)
			require.Equal(t, types.Status_STATUS_INACTIVE, md.Status)
		})
	}
}

// The hook can fire more than once; a second call must not error.
func TestCompleteExit_AlreadyInactiveIsIdempotent(t *testing.T) {
	f := v5Fixture(t, stubStakingKeeper{})
	addr := seedValidator(t, f, "v5-already-inactive", types.Status_STATUS_INACTIVE)

	require.NoError(t, f.keeper.CompleteExit(f.ctx, addr))

	md, _, err := f.keeper.GetValidatorMetadata(f.ctx, addr)
	require.NoError(t, err)
	require.Equal(t, types.Status_STATUS_INACTIVE, md.Status)
}

func TestCompleteExit_UnknownValidatorIsNoop(t *testing.T) {
	f := v5Fixture(t, stubStakingKeeper{})
	unknown := sdk.ValAddress([]byte("v5-exit-unknown-val!"))[:20]
	require.NoError(t, f.keeper.CompleteExit(f.ctx, unknown))
}

// D4 (MEDIUM) regression: removal by x/staking is GROUND TRUTH, so the exit must
// be recordable from EVERY non-terminal status.
//
// Previously ACTIVE had no edge to INACTIVE, on the theory that an active
// validator must pass through UNBONDING. It does not have to: the governed
// MsgUnjail moves JAILED -> ACTIVE while x/staking still has the validator jailed
// and unbonding, and if the operator never returns, staking removes it straight
// from ACTIVE. CompleteExit was then rejected by the V-1 gate, staking only LOGGED
// the error, and the registry permanently reported a deleted validator as ACTIVE —
// the ghost class this lifecycle exists to eliminate.
func TestCompleteExit_RecordsExitFromEveryNonTerminalStatus(t *testing.T) {
	for _, from := range []types.Status{
		types.Status_STATUS_BONDING,
		types.Status_STATUS_ACTIVE,
		types.Status_STATUS_QUARANTINED,
		types.Status_STATUS_JAILED,
		types.Status_STATUS_UNBONDING,
	} {
		t.Run(from.HumanString(), func(t *testing.T) {
			f := v5Fixture(t, stubStakingKeeper{})
			addr := seedValidator(t, f, "v5-anyexit-"+from.HumanString(), from)

			require.NoError(t, f.keeper.CompleteExit(f.ctx, addr))

			md, _, err := f.keeper.GetValidatorMetadata(f.ctx, addr)
			require.NoError(t, err)
			require.Equal(t, types.Status_STATUS_INACTIVE, md.Status,
				"a validator x/staking has deleted must never stay recorded as live")
		})
	}
}
