package keeper_test

import (
	"bytes"
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"cosmossdk.io/collections"
	"cosmossdk.io/log"
	"cosmossdk.io/math"
	sdk "github.com/cosmos/cosmos-sdk/types"
	stakingtypes "github.com/cosmos/cosmos-sdk/x/staking/types"
	"github.com/stretchr/testify/require"

	"github.com/saicon-network/saicon/chain/x/compliance/keeper"
	"github.com/saicon-network/saicon/chain/x/compliance/types"
	vrtypes "github.com/saicon-network/saicon/chain/x/validatorregistry/types"
)

const day = 24 * time.Hour

var (
	dec010 = math.LegacyNewDecWithPrec(10, 2)
	dec020 = math.LegacyNewDecWithPrec(20, 2)
	dec030 = math.LegacyNewDecWithPrec(30, 2)
)

// putOffence writes an accepted duplicate-vote offence of a straight into the
// collections: x/evidence's store holds its hash, a's consensus address is as in
// the Equivocation, and a linked offence enters the by-entity index.
func (f *fixture) putOffence(t *testing.T, a actor, infraction int64, entity string, recordedHeight int64,
	recorded time.Time, d math.LegacyDec) []byte {
	t.Helper()
	e := dupVote(a, infraction)
	hash := f.accept(e)
	cons, err := f.staking.consCdc.BytesToString(a.cons)
	require.NoError(t, err)
	o := types.Offence{
		EvidenceHash:       types.FormatEvidenceHash(hash),
		Validator:          a.op.String(),
		ConsensusAddress:   cons,
		Kind:               types.MisbehaviorKind_MISBEHAVIOR_KIND_DUPLICATE_VOTE,
		InfractionHeight:   infraction,
		InfractionTime:     e.time,
		Power:              e.val.power,
		DoubleSignFraction: d,
		EntityId:           entity,
		RecordedHeight:     recordedHeight,
		RecordedTime:       recorded,
	}
	require.NoError(t, f.keeper.Offences.Set(f.ctx, hash, o))
	if entity != "" {
		require.NoError(t, f.keeper.OffencesByEntity.Set(f.ctx, collections.Join(entity, hash)))
	}
	return hash
}

// setStaking tells the staking mock the status of a's validator.
func (f *fixture) setStaking(a actor, status stakingtypes.BondStatus, unbondingTime time.Time) {
	f.staking.byCons[string(a.cons)] = stakingtypes.Validator{
		OperatorAddress: a.op.String(),
		Status:          status,
		UnbondingTime:   unbondingTime,
	}
}

// atTime moves the fixture's block context to the time (height 1000) with a
// fresh event manager.
func (f *fixture) atTime(tm time.Time) {
	f.ctx = sdk.UnwrapSDKContext(f.ctx).WithBlockHeight(1000).WithBlockTime(tm).WithEventManager(sdk.NewEventManager())
}

// sanctionEnv is the fixture of the spec's worked case: validators A and B are
// both linked to "acme", both offences recorded at height 10 and time T (so the
// entity is armed at T), both tombstoned and unbonding, the block time T + 7 days.
type sanctionEnv struct {
	*fixture
	t      *testing.T
	ms     types.MsgServer
	a, b   actor
	ha, hb []byte
}

func newSanctionEnv(t *testing.T) *sanctionEnv {
	t.Helper()
	f := initFixture(t)
	f.slashing.tombstoned = true
	// Genesis and the migration start the sequence at 1.
	require.NoError(t, f.keeper.SanctionSeq.Set(f.ctx, 1))
	e := &sanctionEnv{fixture: f, t: t, ms: keeper.NewMsgServerImpl(f.keeper), a: f.newActor(1), b: f.newActor(2)}
	e.ha = f.putOffence(t, e.a, 10, "acme", 10, t0, dec010)
	e.hb = f.putOffence(t, e.b, 10, "acme", 10, t0, dec010)
	for _, a := range []actor{e.a, e.b} {
		f.vr.statuses[a.op.String()] = vrtypes.Status_STATUS_JAILED
		f.setStaking(a, stakingtypes.Unbonding, t0.Add(21*day))
	}
	f.atTime(t0.Add(7 * day))
	return e
}

func hexes(hashes ...[]byte) []string {
	out := make([]string, len(hashes))
	for i, h := range hashes {
		out[i] = types.FormatEvidenceHash(h)
	}
	return out
}

func (e *sanctionEnv) msg(hashes ...[]byte) *types.MsgFireCoordinatedSanction {
	return &types.MsgFireCoordinatedSanction{
		Authority:      authorityStr(e.t, e.fixture),
		EntityId:       "acme",
		EvidenceHashes: hexes(hashes...),
		Fraction:       dec030,
		Reason:         "coordinated double-sign at 10",
	}
}

func (e *sanctionEnv) fire(hashes ...[]byte) (*types.MsgFireCoordinatedSanctionResponse, error) {
	return e.ms.FireCoordinatedSanction(e.ctx, e.msg(hashes...))
}

func (e *sanctionEnv) plan(entity string, hashes []string, fraction math.LegacyDec) (types.SanctionPlan, error) {
	return e.keeper.PlanSanction(e.ctx, entity, hashes, fraction)
}

func TestPlanRefusals(t *testing.T) {
	unknown := strings.Repeat("AB", 32)
	cases := []struct {
		name  string
		setup func(e *sanctionEnv) (entity string, hashes []string, fraction math.LegacyDec)
		want  error
	}{
		{"V-15 broken: an offence whose hash x/evidence lacks", func(e *sanctionEnv) (string, []string, math.LegacyDec) {
			delete(e.evidence.hashes, string(e.hb))
			return "acme", hexes(e.ha), dec030
		}, types.ErrCoordinatedInvariant},
		{"fraction differs from the parameter", func(e *sanctionEnv) (string, []string, math.LegacyDec) {
			return "acme", hexes(e.ha, e.hb), math.LegacyNewDecWithPrec(40, 2)
		}, types.ErrFractionMismatch},
		{"fraction below the bound, as a message without ValidateBasic carries it", func(e *sanctionEnv) (string, []string, math.LegacyDec) {
			return "acme", hexes(e.ha, e.hb), math.LegacyNewDecWithPrec(5, 2)
		}, types.ErrInvalidFraction},
		{"fraction below the double-sign fraction in force", func(e *sanctionEnv) (string, []string, math.LegacyDec) {
			e.slashing.d = math.LegacyNewDecWithPrec(35, 2)
			return "acme", hexes(e.ha, e.hb), dec030
		}, types.ErrInvalidFraction},
		{"entity does not exist", func(e *sanctionEnv) (string, []string, math.LegacyDec) {
			e.entities.absent = map[string]bool{"nobody": true}
			return "nobody", hexes(e.ha), dec030
		}, types.ErrEntityNotFound},
		{"entity with one offence is not armed", func(e *sanctionEnv) (string, []string, math.LegacyDec) {
			c := e.newActor(3)
			hc := e.putOffence(e.t, c, 10, "solo", 10, t0, dec010)
			return "solo", hexes(hc), dec030
		}, types.ErrNotArmed},
		{"unknown hash", func(e *sanctionEnv) (string, []string, math.LegacyDec) {
			return "acme", []string{unknown}, dec030
		}, types.ErrUnknownOffence},
		{"hash of an offence linked to another entity", func(e *sanctionEnv) (string, []string, math.LegacyDec) {
			c := e.newActor(3)
			hc := e.putOffence(e.t, c, 10, "beta", 10, t0, dec010)
			return "acme", hexes(e.ha, hc), dec030
		}, types.ErrOffenceNotLinked},
		{"hash already fired", func(e *sanctionEnv) (string, []string, math.LegacyDec) {
			_, err := e.fire(e.ha)
			require.NoError(e.t, err)
			return "acme", hexes(e.ha), dec030
		}, types.ErrAlreadySanctioned},
		{"appeal window one second short", func(e *sanctionEnv) (string, []string, math.LegacyDec) {
			e.atTime(t0.Add(7*day - time.Second))
			return "acme", hexes(e.ha, e.hb), dec030
		}, types.ErrAppealWindowOpen},
		{"validator not tombstoned", func(e *sanctionEnv) (string, []string, math.LegacyDec) {
			// V-15 asks once per offence (two); the plan's own check is the third
			// call. By construction x/slashing agrees with V-15 (b); this is the
			// fail-closed second look.
			e.slashing.falseAfter = 2
			return "acme", hexes(e.ha, e.hb), dec030
		}, types.ErrNotTombstoned},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			e := newSanctionEnv(t)
			entity, hashes, fraction := tc.setup(e)
			_, err := e.plan(entity, hashes, fraction)
			require.ErrorIs(t, err, tc.want)
			// Nothing in a plan writes: a refused plan leaves the staking mock untouched.
			if !errors.Is(tc.want, types.ErrAlreadySanctioned) {
				require.Empty(t, e.staking.slashes)
			}
		})
	}
}

func TestAppealWindowErrorNamesTheEarliestTime(t *testing.T) {
	e := newSanctionEnv(t)
	e.atTime(t0.Add(7*day - time.Second))
	_, err := e.plan("acme", hexes(e.ha), dec030)
	require.ErrorIs(t, err, types.ErrAppealWindowOpen)
	require.Contains(t, err.Error(), t0.Add(7*day).UTC().Format(time.RFC3339))
}

func TestPlanRefusesAMalformedOrRepeatedHashList(t *testing.T) {
	e := newSanctionEnv(t)
	_, err := e.plan("acme", []string{"abc"}, dec030)
	require.ErrorIs(t, err, types.ErrInvalidEvidenceHash)

	// The preview does not run ValidateBasic, so the plan itself refuses a repeat:
	// a repeated hash must never slash one validator twice.
	h := hexes(e.ha)[0]
	_, err = e.plan("acme", []string{h, h}, dec030)
	require.ErrorIs(t, err, types.ErrInvalidEvidenceHash)

	_, err = e.plan("acme", nil, dec030)
	require.ErrorIs(t, err, types.ErrInvalidSanction)
}

func TestPlanOutcomes(t *testing.T) {
	e := newSanctionEnv(t)
	aUnbondingTime := t0.Add(15 * day)
	e.setStaking(e.a, stakingtypes.Unbonding, aUnbondingTime)
	e.setStaking(e.b, stakingtypes.Unbonded, time.Time{})

	// Targets are sorted by operator bytes whatever the order of the hashes.
	for _, hashes := range [][]string{hexes(e.ha, e.hb), hexes(e.hb, e.ha)} {
		plan, err := e.plan("acme", hashes, dec030)
		require.NoError(t, err)
		require.Len(t, plan.Targets, 2)
		a, b := plan.Targets[0], plan.Targets[1]

		require.Equal(t, e.a.op.String(), a.Validator)
		require.Equal(t, types.FormatEvidenceHash(e.ha), a.EvidenceHash)
		require.Equal(t, types.TargetOutcome_TARGET_OUTCOME_SLASHED, a.Outcome)
		require.Equal(t, dec020, a.ExtraFraction)
		require.Equal(t, math.NewInt(300_000_000), a.BaseTokens, "power 300 x 10^6")
		require.True(t, a.ValidatorBurned.IsZero(), "a plan burns nothing")

		require.Equal(t, e.b.op.String(), b.Validator)
		require.Equal(t, types.TargetOutcome_TARGET_OUTCOME_NOT_SLASHABLE, b.Outcome)

		require.Equal(t, t0.Add(7*day), plan.EarliestExecutionTime.UTC())
		require.NotNil(t, plan.SlashableUntil)
		require.Equal(t, aUnbondingTime, plan.SlashableUntil.UTC(), "the Unbonded target does not bound it")
	}

	t.Run("a validator x/staking no longer has is not slashable", func(t *testing.T) {
		e := newSanctionEnv(t)
		delete(e.staking.byCons, string(e.b.cons))
		plan, err := e.plan("acme", hexes(e.ha, e.hb), dec030)
		require.NoError(t, err)
		require.Equal(t, types.TargetOutcome_TARGET_OUTCOME_SLASHED, plan.Targets[0].Outcome)
		require.Equal(t, types.TargetOutcome_TARGET_OUTCOME_NOT_SLASHABLE, plan.Targets[1].Outcome)
	})

	t.Run("nothing to add when the recorded fraction already is F", func(t *testing.T) {
		e := newSanctionEnv(t)
		o, err := e.keeper.Offences.Get(e.ctx, e.ha)
		require.NoError(t, err)
		o.DoubleSignFraction = dec030
		require.NoError(t, e.keeper.Offences.Set(e.ctx, e.ha, o))
		plan, err := e.plan("acme", hexes(e.ha, e.hb), dec030)
		require.NoError(t, err)
		require.Equal(t, types.TargetOutcome_TARGET_OUTCOME_NOTHING_TO_ADD, plan.Targets[0].Outcome)
		require.True(t, plan.Targets[0].ExtraFraction.IsZero())
		require.Equal(t, types.TargetOutcome_TARGET_OUTCOME_SLASHED, plan.Targets[1].Outcome)
	})

	t.Run("a recorded fraction above F is clamped to nothing to add", func(t *testing.T) {
		e := newSanctionEnv(t)
		o, err := e.keeper.Offences.Get(e.ctx, e.ha)
		require.NoError(t, err)
		o.DoubleSignFraction = math.LegacyNewDecWithPrec(50, 2)
		require.NoError(t, e.keeper.Offences.Set(e.ctx, e.ha, o))
		plan, err := e.plan("acme", hexes(e.ha), dec030)
		require.NoError(t, err)
		require.Equal(t, types.TargetOutcome_TARGET_OUTCOME_NOTHING_TO_ADD, plan.Targets[0].Outcome)
		require.True(t, plan.Targets[0].ExtraFraction.IsZero())
	})

	t.Run("no target unbonding leaves slashable_until absent", func(t *testing.T) {
		e := newSanctionEnv(t)
		e.setStaking(e.a, stakingtypes.Bonded, time.Time{})
		e.setStaking(e.b, stakingtypes.Unbonded, time.Time{})
		plan, err := e.plan("acme", hexes(e.ha, e.hb), dec030)
		require.NoError(t, err)
		require.Nil(t, plan.SlashableUntil)
	})
}

func TestTheWindowStartsAtTheLaterOfArmingAndTheNamedOffence(t *testing.T) {
	e := newSanctionEnv(t)
	c := e.newActor(3)
	e.setStaking(c, stakingtypes.Unbonding, t0.Add(21*day))
	e.vr.statuses[c.op.String()] = vrtypes.Status_STATUS_JAILED
	// C is recorded a day after the entity was armed.
	hc := e.putOffence(t, c, 10, "acme", 20, t0.Add(day), dec010)

	e.atTime(t0.Add(7 * day))
	_, err := e.fire(hc)
	require.ErrorIs(t, err, types.ErrAppealWindowOpen)

	e.atTime(t0.Add(8 * day))
	_, err = e.fire(hc)
	require.NoError(t, err)
}

func TestFireSlashesEachTargetByTheExtraFraction(t *testing.T) {
	e := newSanctionEnv(t)
	// A is slashed; B is Unbonded (not slashable); C had d = 0.30 already.
	c := e.newActor(3)
	e.setStaking(e.b, stakingtypes.Unbonded, time.Time{})
	e.setStaking(c, stakingtypes.Unbonding, t0.Add(21*day))
	e.vr.statuses[c.op.String()] = vrtypes.Status_STATUS_JAILED
	hc := e.putOffence(t, c, 10, "acme", 10, t0, dec030)

	e.staking.slashFn = func(_ context.Context, call slashCall) (math.Int, error) {
		require.Equal(t, 1, e.window.opened, "the slash window is open before the first slash")
		require.Equal(t, 0, e.window.closed)
		return math.NewInt(777), nil
	}
	resp, err := e.fire(e.ha, e.hb, hc)
	require.NoError(t, err)
	require.Equal(t, uint64(1), resp.SanctionId)
	require.Equal(t, 1, e.window.opened)
	require.Equal(t, 1, e.window.closed, "the window closes after the last slash")

	// Exactly one slash: A, at the infraction height minus one, on the recorded power.
	require.Len(t, e.staking.slashes, 1)
	call := e.staking.slashes[0]
	require.Equal(t, e.a.cons, call.cons)
	require.Equal(t, int64(10)-sdk.ValidatorUpdateDelay, call.height)
	require.Equal(t, int64(300), call.power)
	require.Equal(t, dec020, call.fraction)
	require.Equal(t, stakingtypes.Infraction_INFRACTION_DOUBLE_SIGN, call.infraction)

	s, err := e.keeper.Sanctions.Get(e.ctx, 1)
	require.NoError(t, err)
	require.Equal(t, uint64(1), s.Id)
	require.Equal(t, "acme", s.EntityId)
	require.Equal(t, dec030, s.Fraction)
	require.Equal(t, authorityStr(t, e.fixture), s.Authority)
	require.Equal(t, "coordinated double-sign at 10", s.Reason)
	require.Equal(t, int64(1000), s.Height)
	require.Equal(t, t0.Add(7*day), s.Time.UTC())
	require.Len(t, s.Targets, 3)

	want := []struct {
		hash    []byte
		op      sdk.ValAddress
		outcome types.TargetOutcome
		extra   math.LegacyDec
		burned  int64
	}{
		{e.ha, e.a.op, types.TargetOutcome_TARGET_OUTCOME_SLASHED, dec020, 777},
		{e.hb, e.b.op, types.TargetOutcome_TARGET_OUTCOME_NOT_SLASHABLE, dec020, 0},
		{hc, c.op, types.TargetOutcome_TARGET_OUTCOME_NOTHING_TO_ADD, math.LegacyZeroDec(), 0},
	}
	for i, w := range want {
		got := s.Targets[i]
		require.Equal(t, types.FormatEvidenceHash(w.hash), got.EvidenceHash)
		require.Equal(t, w.op.String(), got.Validator)
		require.Equal(t, w.outcome, got.Outcome)
		require.Equal(t, w.extra, got.ExtraFraction)
		require.Equal(t, math.NewInt(w.burned), got.ValidatorBurned)
		require.Equal(t, math.NewInt(300_000_000), got.BaseTokens)

		o, err := e.keeper.Offences.Get(e.ctx, w.hash)
		require.NoError(t, err)
		require.Equal(t, uint64(1), o.SanctionId)

		// Every target, slashed or not, is classified: C=0, event 6, still JAILED, reputation zeroed.
		st, found, err := e.keeper.GetCompliance(e.ctx, w.op)
		require.NoError(t, err)
		require.True(t, found)
		require.False(t, st.Compliant)
		require.Equal(t, types.ComplianceEventType_COMPLIANCE_EVENT_TYPE_COORDINATED_ATTACK, st.LastEvent)
		require.Contains(t, st.Reason, "coordinated-attack sanction 1 (entity acme): coordinated double-sign at 10")
		require.False(t, e.vr.flags[w.op.String()])
		require.Equal(t, vrtypes.Status_STATUS_JAILED, e.vr.statuses[w.op.String()])
		require.True(t, e.participation.zeroed[w.op.String()])
	}
	require.False(t, e.participation.epTouched)

	next, err := e.keeper.SanctionSeq.Peek(e.ctx)
	require.NoError(t, err)
	require.Equal(t, uint64(2), next)

	fired := eventsOf(sdk.UnwrapSDKContext(e.ctx), types.EventTypeSanctionFired)
	require.Len(t, fired, 1)
	require.Equal(t, "1", attr(t, fired[0], types.AttributeKeySanctionID))
	require.Equal(t, "acme", attr(t, fired[0], types.AttributeKeyEntityID))
	require.Equal(t, dec030.String(), attr(t, fired[0], types.AttributeKeyFraction))
	require.Equal(t, strings.Join(hexes(e.ha, e.hb, hc), ","), attr(t, fired[0], types.AttributeKeyEvidenceHashes))
	require.Equal(t, authorityStr(t, e.fixture), attr(t, fired[0], types.AttributeKeyAuthority))

	targets := eventsOf(sdk.UnwrapSDKContext(e.ctx), types.EventTypeSanctionTarget)
	require.Len(t, targets, 3)
	require.Equal(t, e.a.op.String(), attr(t, targets[0], types.AttributeKeyValidator))
	require.Equal(t, "777", attr(t, targets[0], types.AttributeKeyValidatorBurned))
	require.Equal(t, dec020.String(), attr(t, targets[0], types.AttributeKeyExtraFraction))
	require.Equal(t, types.TargetOutcome_TARGET_OUTCOME_SLASHED.String(), attr(t, targets[0], types.AttributeKeyOutcome))
	require.Equal(t, types.TargetOutcome_TARGET_OUTCOME_NOT_SLASHABLE.String(), attr(t, targets[1], types.AttributeKeyOutcome))

	flagged := eventsOf(sdk.UnwrapSDKContext(e.ctx), types.EventTypeComplianceFlagged)
	require.Len(t, flagged, 3)
	for _, ev := range flagged {
		require.Equal(t, types.ComplianceEventType_COMPLIANCE_EVENT_TYPE_COORDINATED_ATTACK.String(),
			attr(t, ev, types.AttributeKeyEvent))
	}
}

func TestFireWithoutAReasonRecordsTheDefault(t *testing.T) {
	e := newSanctionEnv(t)
	m := e.msg(e.ha)
	m.Reason = ""
	_, err := e.ms.FireCoordinatedSanction(e.ctx, m)
	require.NoError(t, err)
	st, _, err := e.keeper.GetCompliance(e.ctx, e.a.op)
	require.NoError(t, err)
	require.Contains(t, st.Reason, "ratified by governance")
}

func TestFireRunsValidateBasic(t *testing.T) {
	e := newSanctionEnv(t)
	m := e.msg(e.ha)
	m.EvidenceHashes = []string{strings.ToLower(m.EvidenceHashes[0])}
	_, err := e.ms.FireCoordinatedSanction(e.ctx, m)
	require.ErrorIs(t, err, types.ErrInvalidEvidenceHash)
}

func TestFireTwiceIsRefused(t *testing.T) {
	e := newSanctionEnv(t)
	_, err := e.fire(e.ha, e.hb)
	require.NoError(t, err)
	slashes := len(e.staking.slashes)

	_, err = e.fire(e.ha, e.hb)
	require.ErrorIs(t, err, types.ErrAlreadySanctioned)
	next, err := e.keeper.SanctionSeq.Peek(e.ctx)
	require.NoError(t, err)
	require.Equal(t, uint64(2), next, "the sequence did not move")
	require.Len(t, e.staking.slashes, slashes, "nothing was slashed again")
}

func TestFireASubsetThenTheRest(t *testing.T) {
	e := newSanctionEnv(t)
	r1, err := e.fire(e.ha)
	require.NoError(t, err)
	require.Equal(t, uint64(1), r1.SanctionId)
	r2, err := e.fire(e.hb)
	require.NoError(t, err)
	require.Equal(t, uint64(2), r2.SanctionId)

	s1, err := e.keeper.Sanctions.Get(e.ctx, 1)
	require.NoError(t, err)
	require.Len(t, s1.Targets, 1)
	require.Equal(t, e.a.op.String(), s1.Targets[0].Validator)
	s2, err := e.keeper.Sanctions.Get(e.ctx, 2)
	require.NoError(t, err)
	require.Equal(t, e.b.op.String(), s2.Targets[0].Validator)

	c, err := e.keeper.CaseOf(e.ctx, "acme")
	require.NoError(t, err)
	require.Empty(t, c.UnfiredHashes)
}

// The slash is the only fallible step, so it runs on the raw context (no cache:
// this pins the handler's own ordering, not x/gov's discard of a failed
// proposal's cache) before the first write and before the sequence moves.
func TestASlashErrorWritesNothing(t *testing.T) {
	e := newSanctionEnv(t)
	boom := errors.New("staking: injected slash failure")
	e.staking.slashFn = func(_ context.Context, _ slashCall) (math.Int, error) {
		if len(e.staking.slashes) == 2 {
			return math.Int{}, boom
		}
		return math.NewInt(5), nil
	}

	_, err := e.fire(e.ha, e.hb)
	require.ErrorIs(t, err, boom)
	require.Len(t, e.staking.slashes, 2, "the first target was slashed, the second failed")

	n := 0
	require.NoError(t, e.keeper.Sanctions.Walk(e.ctx, nil, func(uint64, types.Sanction) (bool, error) { n++; return false, nil }))
	require.Zero(t, n, "no sanction stored")
	next, err := e.keeper.SanctionSeq.Peek(e.ctx)
	require.NoError(t, err)
	require.Equal(t, uint64(1), next, "the sequence did not move")
	for _, h := range [][]byte{e.ha, e.hb} {
		o, err := e.keeper.Offences.Get(e.ctx, h)
		require.NoError(t, err)
		require.Zero(t, o.SanctionId)
	}
	for _, a := range []actor{e.a, e.b} {
		_, found, err := e.keeper.GetCompliance(e.ctx, a.op)
		require.NoError(t, err)
		require.False(t, found, "no compliance state written")
		require.Equal(t, vrtypes.Status_STATUS_JAILED, e.vr.statuses[a.op.String()])
	}
	require.Empty(t, e.vr.flags)
	require.Empty(t, e.participation.zeroed)
	require.Empty(t, eventsOf(sdk.UnwrapSDKContext(e.ctx), types.EventTypeSanctionFired))
	require.Empty(t, eventsOf(sdk.UnwrapSDKContext(e.ctx), types.EventTypeSanctionTarget))
	require.Empty(t, eventsOf(sdk.UnwrapSDKContext(e.ctx), types.EventTypeComplianceFlagged))
	require.Equal(t, 1, e.window.opened)
	require.Equal(t, 1, e.window.closed, "the window closes on the error path")
}

func TestOnlyTheGovAuthorityFires(t *testing.T) {
	e := newSanctionEnv(t)
	other, err := e.addressCodec.BytesToString(sdk.AccAddress(strings.Repeat("\x07", 20)))
	require.NoError(t, err)
	m := e.msg(e.ha)
	m.Authority = other
	_, err = e.ms.FireCoordinatedSanction(e.ctx, m)
	require.ErrorIs(t, err, types.ErrInvalidSigner)
	require.Empty(t, e.staking.slashes)
}

// Review Focus 2: x/staking calls BeforeValidatorSlashed again inside the
// sanction's own slash, and the double-sign hook must stay out of it. This is
// the only test that tells a missing marker apart: in the full flow the
// sanction's SetCompliance runs after the slash and overwrites whatever the hook
// wrote.
func TestTheSanctionDoesNotReenterTheDoubleSignHook(t *testing.T) {
	e := newSanctionEnv(t)
	e.vr.statuses[e.a.op.String()] = vrtypes.Status_STATUS_ACTIVE

	var calledHook bool
	e.staking.slashFn = func(ctx context.Context, call slashCall) (math.Int, error) {
		require.NoError(t, e.keeper.Hooks().BeforeValidatorSlashed(ctx, e.a.op, call.fraction))
		calledHook = true
		_, found, err := e.keeper.GetCompliance(ctx, e.a.op)
		require.NoError(t, err)
		require.False(t, found, "the hook wrote a compliance record inside the sanction's slash")
		require.Equal(t, vrtypes.Status_STATUS_ACTIVE, e.vr.statuses[e.a.op.String()], "the hook jailed the validator")
		require.Empty(t, e.vr.flags)
		require.Empty(t, e.participation.zeroed)
		return math.NewInt(1), nil
	}
	_, err := e.fire(e.ha)
	require.NoError(t, err)
	require.True(t, calledHook)

	// What is recorded is the sanction's own classification.
	st, found, err := e.keeper.GetCompliance(e.ctx, e.a.op)
	require.NoError(t, err)
	require.True(t, found)
	require.Equal(t, types.ComplianceEventType_COMPLIANCE_EVENT_TYPE_COORDINATED_ATTACK, st.LastEvent)
	require.NotContains(t, st.Reason, "double-sign slash detected")

	// Outside a sanction the same hook call still flags a critical violation.
	e.vr.statuses[e.b.op.String()] = vrtypes.Status_STATUS_ACTIVE
	require.NoError(t, e.keeper.Hooks().BeforeValidatorSlashed(e.ctx, e.b.op, dec020))
	st, found, err = e.keeper.GetCompliance(e.ctx, e.b.op)
	require.NoError(t, err)
	require.True(t, found)
	require.Equal(t, types.ComplianceEventType_COMPLIANCE_EVENT_TYPE_CRITICAL_VIOLATION, st.LastEvent)
}

// Closing the slash window cannot undo a firing, but its failure is logged.
func TestAFailedWindowCloseIsLogged(t *testing.T) {
	e := newSanctionEnv(t)
	e.window.closeErr = errors.New("stakeengine: injected close failure")
	var logged bytes.Buffer
	e.ctx = sdk.UnwrapSDKContext(e.ctx).WithLogger(log.NewLogger(&logged, log.ColorOption(false)))

	_, err := e.fire(e.ha)
	require.NoError(t, err)
	require.Equal(t, 1, e.window.closed)
	require.Contains(t, logged.String(), "closing the slash window failed")
	require.Contains(t, logged.String(), "injected close failure")
}
