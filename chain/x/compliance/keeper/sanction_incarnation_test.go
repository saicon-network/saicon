package keeper_test

import (
	"testing"
	"time"

	sdk "github.com/cosmos/cosmos-sdk/types"
	stakingtypes "github.com/cosmos/cosmos-sdk/x/staking/types"
	"github.com/stretchr/testify/require"

	"github.com/saicon-network/saicon/chain/x/compliance/keeper"
	"github.com/saicon-network/saicon/chain/x/compliance/types"
	vrtypes "github.com/saicon-network/saicon/chain/x/validatorregistry/types"
)

// A sanction slashes and classifies only the incarnation that committed the
// offence (spec §8.3, §8.6). These tests reach every branch of targetOf.

// removeA runs x/compliance's own AfterValidatorRemoved hook for A's old
// incarnation, as x/staking does when it deletes the fully unbonded validator.
func (e *sanctionEnv) removeA() {
	e.t.Helper()
	require.NoError(e.t, e.keeper.Hooks().AfterValidatorRemoved(e.ctx, e.a.cons, e.a.op))
	delete(e.staking.byCons, string(e.a.cons))
}

// previewOf is the SanctionPreview query of acme at the governed fraction.
func (e *sanctionEnv) previewOf(hashes []string) (*types.QuerySanctionPreviewResponse, error) {
	return keeper.NewQueryServerImpl(e.keeper).SanctionPreview(e.ctx, &types.QuerySanctionPreviewRequest{
		EntityId: "acme", EvidenceHashes: hashes, Fraction: dec030.String(),
	})
}

// requireUntouched requires that the firing wrote no compliance record for op,
// left its registry status and flag alone and zeroed no reputation.
func (e *sanctionEnv) requireUntouched(op string, status vrtypes.Status) {
	e.t.Helper()
	_, flagged := e.vr.flags[op]
	require.False(e.t, flagged, "the registry flag of %s was written", op)
	require.Equal(e.t, status, e.vr.statuses[op])
	require.False(e.t, e.participation.zeroed[op], "the reputation of %s was zeroed", op)
}

// The final review's repro 1, adapted: A's old incarnation was removed and a
// NEW validator of another operator now sits on A's tombstoned consensus
// address, bonded, months after the infraction. Before the fix it was slashed.
func TestAnotherOperatorRecreatedOnTheOldKeyIsNotSlashed(t *testing.T) {
	e := newSanctionEnv(t)
	e.removeA()
	other := e.newActor(9)
	e.staking.byCons[string(e.a.cons)] = stakingtypes.Validator{OperatorAddress: other.op.String(), Status: stakingtypes.Bonded}
	e.vr.statuses[other.op.String()] = vrtypes.Status_STATUS_ACTIVE
	e.atTime(t0.Add(120 * day))

	plan, err := e.plan("acme", hexes(e.ha), dec030)
	require.NoError(t, err)
	require.Equal(t, types.TargetOutcome_TARGET_OUTCOME_NOT_SLASHABLE, plan.Targets[0].Outcome)
	_, err = e.fire(e.ha)
	require.NoError(t, err)
	require.Empty(t, e.staking.slashes, "the new incarnation's stake is not slashed for the old offence")
	e.requireUntouched(other.op.String(), vrtypes.Status_STATUS_ACTIVE)
}

// The final review's repro 2, adapted: A's operator re-created its validator
// after the old one was removed, reusing the same consensus key: bonded, ACTIVE.
// Before the fix it was slashed and jailed.
func TestTheSameOperatorRecreatedOnTheOldKeyIsNeitherSlashedNorJailed(t *testing.T) {
	e := newSanctionEnv(t)
	e.removeA()
	e.setStaking(e.a, stakingtypes.Bonded, time.Time{})
	e.vr.statuses[e.a.op.String()] = vrtypes.Status_STATUS_ACTIVE
	e.atTime(t0.Add(120 * day))

	plan, err := e.plan("acme", hexes(e.ha), dec030)
	require.NoError(t, err)
	require.Equal(t, types.TargetOutcome_TARGET_OUTCOME_NOT_SLASHABLE, plan.Targets[0].Outcome)
	_, err = e.fire(e.ha)
	require.NoError(t, err)
	require.Empty(t, e.staking.slashes)
	e.requireUntouched(e.a.op.String(), vrtypes.Status_STATUS_ACTIVE)
}

func TestTheTargetOfEachIncarnationCase(t *testing.T) {
	other := func(e *sanctionEnv) actor { return e.newActor(9) }
	cases := []struct {
		name    string
		setup   func(e *sanctionEnv) (watch string, status vrtypes.Status)
		outcome types.TargetOutcome
		class   types.TargetClassification
		slashed bool
	}{
		{"the key was removed and re-created by the same operator", func(e *sanctionEnv) (string, vrtypes.Status) {
			e.removeA()
			e.setStaking(e.a, stakingtypes.Bonded, time.Time{})
			e.vr.statuses[e.a.op.String()] = vrtypes.Status_STATUS_ACTIVE
			return e.a.op.String(), vrtypes.Status_STATUS_ACTIVE
		}, types.TargetOutcome_TARGET_OUTCOME_NOT_SLASHABLE, types.TargetClassification_TARGET_CLASSIFICATION_SKIPPED_KEY_REMOVED, false},
		{"the key was removed and nothing holds it", func(e *sanctionEnv) (string, vrtypes.Status) {
			e.removeA()
			e.vr.statuses[e.a.op.String()] = vrtypes.Status_STATUS_INACTIVE
			return e.a.op.String(), vrtypes.Status_STATUS_INACTIVE
		}, types.TargetOutcome_TARGET_OUTCOME_NOT_SLASHABLE, types.TargetClassification_TARGET_CLASSIFICATION_SKIPPED_KEY_REMOVED, false},
		{"another operator holds the key (no removal recorded)", func(e *sanctionEnv) (string, vrtypes.Status) {
			o := other(e)
			e.staking.byCons[string(e.a.cons)] = stakingtypes.Validator{OperatorAddress: o.op.String(), Status: stakingtypes.Bonded}
			e.vr.statuses[o.op.String()] = vrtypes.Status_STATUS_ACTIVE
			return o.op.String(), vrtypes.Status_STATUS_ACTIVE
		}, types.TargetOutcome_TARGET_OUTCOME_NOT_SLASHABLE, types.TargetClassification_TARGET_CLASSIFICATION_SKIPPED_KEY_REUSED, false},
		{"x/staking holds no validator on the key", func(e *sanctionEnv) (string, vrtypes.Status) {
			delete(e.staking.byCons, string(e.a.cons))
			// The operator re-created under a new key: a live, innocent record.
			e.vr.statuses[e.a.op.String()] = vrtypes.Status_STATUS_ACTIVE
			return e.a.op.String(), vrtypes.Status_STATUS_ACTIVE
		}, types.TargetOutcome_TARGET_OUTCOME_NOT_SLASHABLE, types.TargetClassification_TARGET_CLASSIFICATION_SKIPPED_NO_VALIDATOR, false},
		{"the operator has no registry record", func(e *sanctionEnv) (string, vrtypes.Status) {
			delete(e.vr.statuses, e.a.op.String())
			return e.a.op.String(), vrtypes.Status_STATUS_UNSPECIFIED
		}, types.TargetOutcome_TARGET_OUTCOME_SLASHED, types.TargetClassification_TARGET_CLASSIFICATION_SKIPPED_NO_REGISTRY_RECORD, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			e := newSanctionEnv(t)
			watch, status := tc.setup(e)

			plan, err := e.plan("acme", hexes(e.ha, e.hb), dec030)
			require.NoError(t, err)
			a, b := plan.Targets[0], plan.Targets[1]
			require.Equal(t, e.a.op.String(), a.Validator)
			require.Equal(t, tc.outcome, a.Outcome)
			require.Equal(t, tc.class, a.Classification)
			require.Equal(t, types.TargetOutcome_TARGET_OUTCOME_SLASHED, b.Outcome)
			require.Equal(t, types.TargetClassification_TARGET_CLASSIFICATION_CLASSIFIED, b.Classification)

			// The preview shares the plan: the firing is fireable and executes.
			res, err := e.previewOf(hexes(e.ha, e.hb))
			require.NoError(t, err)
			require.True(t, res.Fireable, res.Reason)
			require.Equal(t, plan, res.Plan)

			_, err = e.fire(e.ha, e.hb)
			require.NoError(t, err)
			if tc.slashed {
				require.Len(t, e.staking.slashes, 2)
			} else {
				require.Len(t, e.staking.slashes, 1, "only B is slashed")
				require.Equal(t, e.b.cons, e.staking.slashes[0].cons)
			}
			_, found, err := e.keeper.GetCompliance(e.ctx, e.a.op)
			require.NoError(t, err)
			require.False(t, found, "no compliance record for A's operator")
			e.requireUntouched(watch, status)

			// B is classified as before; the record and the event carry A's skip.
			st, found, err := e.keeper.GetCompliance(e.ctx, e.b.op)
			require.NoError(t, err)
			require.True(t, found)
			require.Equal(t, types.ComplianceEventType_COMPLIANCE_EVENT_TYPE_COORDINATED_ATTACK, st.LastEvent)
			s, err := e.keeper.Sanctions.Get(e.ctx, 1)
			require.NoError(t, err)
			require.Equal(t, tc.class, s.Targets[0].Classification)
			o, err := e.keeper.Offences.Get(e.ctx, e.ha)
			require.NoError(t, err)
			require.Equal(t, uint64(1), o.SanctionId, "the offence is fired all the same")
			targets := eventsOf(sdk.UnwrapSDKContext(e.ctx), types.EventTypeSanctionTarget)
			require.Len(t, targets, 2)
			require.Equal(t, tc.class.String(), attr(t, targets[0], types.AttributeKeyClassification))
			require.Equal(t, types.TargetClassification_TARGET_CLASSIFICATION_CLASSIFIED.String(),
				attr(t, targets[1], types.AttributeKeyClassification))

			msg, broken := e.keeper.CoordinatedAttackInvariant(e.ctx)
			require.False(t, broken, msg)
		})
	}
}

// An Unbonded validator that x/staking still holds under the offence's key and
// operator is the same incarnation: not slashable, but classified.
func TestAnUnbondedOffenderIsStillClassified(t *testing.T) {
	e := newSanctionEnv(t)
	e.setStaking(e.a, stakingtypes.Unbonded, time.Time{})
	plan, err := e.plan("acme", hexes(e.ha), dec030)
	require.NoError(t, err)
	require.Equal(t, types.TargetOutcome_TARGET_OUTCOME_NOT_SLASHABLE, plan.Targets[0].Outcome)
	require.Equal(t, types.TargetClassification_TARGET_CLASSIFICATION_CLASSIFIED, plan.Targets[0].Classification)
}

func TestTheRemovalHookRecordsOnlyTombstonedKeys(t *testing.T) {
	e := newSanctionEnv(t)
	c := e.newActor(7)
	e.slashing.notTombstoned = map[string]bool{string(c.cons): true}

	require.NoError(t, e.keeper.Hooks().AfterValidatorRemoved(e.ctx, c.cons, c.op))
	has, err := e.keeper.RemovedConsensus.Has(e.ctx, c.cons)
	require.NoError(t, err)
	require.False(t, has, "an ordinary exit is not recorded: the key may double-sign later as a new incarnation")

	require.NoError(t, e.keeper.Hooks().AfterValidatorRemoved(e.ctx, e.a.cons, e.a.op))
	has, err = e.keeper.RemovedConsensus.Has(e.ctx, e.a.cons)
	require.NoError(t, err)
	require.True(t, has)

	// Idempotent, and an empty address is ignored; neither errors.
	require.NoError(t, e.keeper.Hooks().AfterValidatorRemoved(e.ctx, e.a.cons, e.a.op))
	require.NoError(t, e.keeper.Hooks().AfterValidatorRemoved(e.ctx, nil, e.a.op))
}

// slashable_until counts only the targets the firing will slash.
func TestSlashableUntilCountsOnlySlashedTargets(t *testing.T) {
	e := newSanctionEnv(t)
	o, err := e.keeper.Offences.Get(e.ctx, e.ha)
	require.NoError(t, err)
	o.DoubleSignFraction = dec030 // A: nothing to add
	require.NoError(t, e.keeper.Offences.Set(e.ctx, e.ha, o))
	e.setStaking(e.a, stakingtypes.Unbonding, t0.Add(9*day))
	e.setStaking(e.b, stakingtypes.Unbonding, t0.Add(15*day))

	plan, err := e.plan("acme", hexes(e.ha, e.hb), dec030)
	require.NoError(t, err)
	require.Equal(t, types.TargetOutcome_TARGET_OUTCOME_NOTHING_TO_ADD, plan.Targets[0].Outcome)
	require.NotNil(t, plan.SlashableUntil)
	require.Equal(t, t0.Add(15*day), plan.SlashableUntil.UTC(), "A's earlier unbonding does not bound it")
}
