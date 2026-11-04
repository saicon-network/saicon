package keeper_test

import (
	"bytes"
	"encoding/hex"
	"sort"
	"strings"
	"testing"
	"time"

	"cosmossdk.io/collections"
	"cosmossdk.io/core/comet"
	"cosmossdk.io/log"
	"cosmossdk.io/math"
	evidencetypes "cosmossdk.io/x/evidence/types"
	sdk "github.com/cosmos/cosmos-sdk/types"
	stakingtypes "github.com/cosmos/cosmos-sdk/x/staking/types"
	"github.com/stretchr/testify/require"

	"github.com/saicon-network/saicon/chain/x/compliance/types"
)

// The fakes of cosmossdk.io/core/comet that the block context carries.

type fakeValidator struct {
	addr  []byte
	power int64
}

func (v fakeValidator) Address() []byte { return v.addr }
func (v fakeValidator) Power() int64    { return v.power }

type fakeEvidence struct {
	typ    comet.MisbehaviorType
	val    fakeValidator
	height int64
	time   time.Time
}

func (e fakeEvidence) Type() comet.MisbehaviorType { return e.typ }
func (e fakeEvidence) Validator() comet.Validator  { return e.val }
func (e fakeEvidence) Height() int64               { return e.height }
func (e fakeEvidence) Time() time.Time             { return e.time }
func (e fakeEvidence) TotalVotingPower() int64     { return 1000 }

type fakeEvidenceList []comet.Evidence

func (l fakeEvidenceList) Len() int                 { return len(l) }
func (l fakeEvidenceList) Get(i int) comet.Evidence { return l[i] }

type fakeBlockInfo struct{ ev []comet.Evidence }

func (b fakeBlockInfo) GetEvidence() comet.EvidenceList { return fakeEvidenceList(b.ev) }
func (b fakeBlockInfo) GetValidatorsHash() []byte       { return nil }
func (b fakeBlockInfo) GetProposerAddress() []byte      { return nil }
func (b fakeBlockInfo) GetLastCommit() comet.CommitInfo { return nil }

var t0 = time.Date(2026, 3, 1, 12, 0, 0, 0, time.UTC)

// actor is a validator the staking mock knows. The operator bytes order the
// actors (A < B < C), which is the order the keeper sorts validators in.
type actor struct {
	op   sdk.ValAddress
	cons []byte
}

func (f *fixture) newActor(n byte) actor {
	a := actor{op: sdk.ValAddress(bytes.Repeat([]byte{n}, 20)), cons: bytes.Repeat([]byte{0x10 + n}, 20)}
	f.staking.byCons[string(a.cons)] = stakingtypes.Validator{OperatorAddress: a.op.String()}
	return a
}

// vote is the misbehaviour entry for a's conflicting votes at a height.
func vote(a actor, typ comet.MisbehaviorType, height int64) fakeEvidence {
	return fakeEvidence{
		typ:    typ,
		val:    fakeValidator{addr: a.cons, power: 300},
		height: height,
		time:   t0.Add(time.Duration(height) * time.Second),
	}
}

func dupVote(a actor, height int64) fakeEvidence { return vote(a, comet.DuplicateVote, height) }

// hashOf is x/evidence's own key for the entry.
func (f *fixture) hashOf(e comet.Evidence) []byte {
	return evidencetypes.FromABCIEvidence(e, f.staking.consCdc).Hash()
}

// accept makes x/evidence's store hold the entry, as if it had slashed for it.
func (f *fixture) accept(e comet.Evidence) []byte {
	h := f.hashOf(e)
	f.evidence.hashes[string(h)] = true
	return h
}

// link makes the entity registry answer that a belonged to the entity at the height.
func (f *fixture) link(a actor, height int64, entity string) {
	f.entities.at[entityKey(a.op, height)] = entity
}

// block is a context for the BeginBlock of the given height carrying the entries.
func (f *fixture) block(height int64, ev ...comet.Evidence) sdk.Context {
	return sdk.UnwrapSDKContext(f.ctx).
		WithBlockHeight(height).
		WithBlockTime(t0.Add(time.Duration(height) * time.Second)).
		WithEventManager(sdk.NewEventManager()).
		WithCometInfo(fakeBlockInfo{ev: ev})
}

func eventsOf(ctx sdk.Context, typ string) []sdk.Event {
	var out []sdk.Event
	for _, e := range ctx.EventManager().Events() {
		if e.Type == typ {
			out = append(out, e)
		}
	}
	return out
}

func attr(t *testing.T, e sdk.Event, key string) string {
	t.Helper()
	for _, a := range e.Attributes {
		if a.Key == key {
			return a.Value
		}
	}
	t.Fatalf("event %s has no attribute %s", e.Type, key)
	return ""
}

func (f *fixture) offence(t *testing.T, hash []byte) (types.Offence, bool) {
	t.Helper()
	o, err := f.keeper.Offences.Get(f.ctx, hash)
	if err != nil {
		require.ErrorIs(t, err, collections.ErrNotFound)
		return types.Offence{}, false
	}
	return o, true
}

func (f *fixture) indexed(t *testing.T, entity string, hash []byte) bool {
	t.Helper()
	ok, err := f.keeper.OffencesByEntity.Has(f.ctx, collections.Join(entity, hash))
	require.NoError(t, err)
	return ok
}

func TestAStoredDuplicateVoteIsRecorded(t *testing.T) {
	f := initFixture(t)
	a := f.newActor(1)
	e := dupVote(a, 4)
	hash := f.accept(e)
	f.link(a, 4, "acme")

	ctx := f.block(5, e)
	f.keeper.RecordOffences(ctx)

	o, ok := f.offence(t, hash)
	require.True(t, ok)
	require.Equal(t, strings.ToUpper(hex.EncodeToString(hash)), o.EvidenceHash)
	require.Len(t, o.EvidenceHash, 64)
	require.Equal(t, a.op.String(), o.Validator)
	wantCons, err := f.staking.consCdc.BytesToString(a.cons)
	require.NoError(t, err)
	require.Equal(t, wantCons, o.ConsensusAddress)
	require.Equal(t, types.MisbehaviorKind_MISBEHAVIOR_KIND_DUPLICATE_VOTE, o.Kind)
	require.Equal(t, int64(4), o.InfractionHeight)
	require.True(t, e.time.Equal(o.InfractionTime))
	require.Equal(t, int64(300), o.Power)
	require.Equal(t, math.LegacyNewDecWithPrec(10, 2), o.DoubleSignFraction)
	require.Equal(t, "acme", o.EntityId)
	require.Equal(t, int64(5), o.RecordedHeight)
	require.True(t, t0.Add(5*time.Second).Equal(o.RecordedTime))
	require.Zero(t, o.SanctionId)
	require.True(t, f.indexed(t, "acme", hash))

	evs := eventsOf(ctx, types.EventTypeOffenceRecorded)
	require.Len(t, evs, 1)
	require.Equal(t, o.EvidenceHash, attr(t, evs[0], types.AttributeKeyEvidenceHash))
	require.Equal(t, a.op.String(), attr(t, evs[0], types.AttributeKeyValidator))
	require.Equal(t, "MISBEHAVIOR_KIND_DUPLICATE_VOTE", attr(t, evs[0], types.AttributeKeyKind))
	require.Equal(t, "4", attr(t, evs[0], types.AttributeKeyInfractionHeight))
	require.Equal(t, "acme", attr(t, evs[0], types.AttributeKeyEntityID))
	require.Empty(t, eventsOf(ctx, types.EventTypeEntityArmed), "one validator arms nothing")
}

func TestTheRecordCarriesTheFractionThatWasInForce(t *testing.T) {
	f := initFixture(t)
	f.slashing.d = math.LegacyNewDecWithPrec(5, 2)
	a := f.newActor(1)
	e := dupVote(a, 4)
	hash := f.accept(e)

	f.keeper.RecordOffences(f.block(5, e))

	o, ok := f.offence(t, hash)
	require.True(t, ok)
	require.Equal(t, math.LegacyNewDecWithPrec(5, 2), o.DoubleSignFraction, "d is read, never assumed to be 0.10")
}

func TestEvidenceXEvidenceIgnoredIsNotRecorded(t *testing.T) {
	f := initFixture(t)
	a := f.newActor(1)
	e := dupVote(a, 4)
	f.link(a, 4, "acme") // linked, but x/evidence did not store the hash

	ctx := f.block(5, e)
	f.keeper.RecordOffences(ctx)

	_, ok := f.offence(t, f.hashOf(e))
	require.False(t, ok)
	require.False(t, f.indexed(t, "acme", f.hashOf(e)))
	require.Empty(t, ctx.EventManager().Events())
	require.Zero(t, f.entities.calls)
}

func TestRecordingIsIdempotent(t *testing.T) {
	f := initFixture(t)
	a := f.newActor(1)
	e := dupVote(a, 4)
	hash := f.accept(e)
	f.link(a, 4, "acme")

	first := f.block(5, e)
	f.keeper.RecordOffences(first)
	second := f.block(6, e, e) // again, and twice in one block
	f.keeper.RecordOffences(second)

	require.Len(t, eventsOf(first, types.EventTypeOffenceRecorded), 1)
	require.Empty(t, second.EventManager().Events())
	o, ok := f.offence(t, hash)
	require.True(t, ok)
	require.Equal(t, int64(5), o.RecordedHeight, "the first record stands")
	c, err := f.keeper.CaseOf(f.ctx, "acme")
	require.NoError(t, err)
	require.Len(t, c.Offences, 1)
}

func TestALightClientAttackIsRecordedButNotLinked(t *testing.T) {
	f := initFixture(t)
	a := f.newActor(1)
	e := vote(a, comet.LightClientAttack, 4)
	hash := f.accept(e)
	f.link(a, 4, "acme") // would link if it were asked

	ctx := f.block(5, e)
	f.keeper.RecordOffences(ctx)

	o, ok := f.offence(t, hash)
	require.True(t, ok)
	require.Equal(t, types.MisbehaviorKind_MISBEHAVIOR_KIND_LIGHT_CLIENT_ATTACK, o.Kind)
	require.Empty(t, o.EntityId)
	require.False(t, f.indexed(t, "acme", hash))
	require.Zero(t, f.entities.calls, "EntityAt is for duplicate votes only")
	evs := eventsOf(ctx, types.EventTypeOffenceRecorded)
	require.Len(t, evs, 1)
	require.Equal(t, "MISBEHAVIOR_KIND_LIGHT_CLIENT_ATTACK", attr(t, evs[0], types.AttributeKeyKind))
	require.Empty(t, attr(t, evs[0], types.AttributeKeyEntityID))
}

func TestAnUnknownEvidenceTypeIsSkipped(t *testing.T) {
	f := initFixture(t)
	a := f.newActor(1)
	e := vote(a, comet.Unknown, 4)
	f.accept(e)

	ctx := f.block(5, e)
	f.keeper.RecordOffences(ctx)

	_, ok := f.offence(t, f.hashOf(e))
	require.False(t, ok)
	require.Empty(t, ctx.EventManager().Events())
}

func TestTwoValidatorsOfOneEntityArmItOnce(t *testing.T) {
	f := initFixture(t)
	a, b, c := f.newActor(1), f.newActor(2), f.newActor(3)
	ea, eb, ec := dupVote(a, 3), dupVote(b, 4), dupVote(c, 5)
	ha, hb, hc := f.accept(ea), f.accept(eb), f.accept(ec)
	f.link(a, 3, "acme")
	f.link(b, 4, "acme")
	f.link(c, 5, "acme")

	block5 := f.block(5, ea)
	f.keeper.RecordOffences(block5)
	require.Empty(t, eventsOf(block5, types.EventTypeEntityArmed))
	cs, err := f.keeper.CaseOf(f.ctx, "acme")
	require.NoError(t, err)
	require.False(t, cs.Armed)
	require.Zero(t, cs.ArmedHeight)
	require.True(t, cs.ArmedTime.IsZero())
	require.Equal(t, []string{a.op.String()}, cs.Validators)

	block6 := f.block(6, eb)
	f.keeper.RecordOffences(block6)
	armed := eventsOf(block6, types.EventTypeEntityArmed)
	require.Len(t, armed, 1)
	require.Equal(t, "acme", attr(t, armed[0], types.AttributeKeyEntityID))
	require.Equal(t, a.op.String()+","+b.op.String(), attr(t, armed[0], types.AttributeKeyValidators))
	require.Equal(t, "6", attr(t, armed[0], types.AttributeKeyArmedHeight))

	cs, err = f.keeper.CaseOf(f.ctx, "acme")
	require.NoError(t, err)
	require.True(t, cs.Armed)
	require.Equal(t, int64(6), cs.ArmedHeight)
	require.True(t, t0.Add(6*time.Second).Equal(cs.ArmedTime))
	require.Equal(t, []string{a.op.String(), b.op.String()}, cs.Validators)
	require.Len(t, cs.Offences, 2)
	require.Equal(t, sortedStrings([]string{types.FormatEvidenceHash(ha), types.FormatEvidenceHash(hb)}), cs.UnfiredHashes)

	block7 := f.block(7, ec)
	f.keeper.RecordOffences(block7)
	require.Len(t, eventsOf(block7, types.EventTypeOffenceRecorded), 1)
	require.Empty(t, eventsOf(block7, types.EventTypeEntityArmed), "a third validator does not arm it again")
	cs, err = f.keeper.CaseOf(f.ctx, "acme")
	require.NoError(t, err)
	require.True(t, cs.Armed)
	require.Equal(t, int64(6), cs.ArmedHeight, "armed by the record that brought the second validator")
	require.Equal(t, []string{a.op.String(), b.op.String(), c.op.String()}, cs.Validators)
	require.Len(t, cs.Offences, 3)
	// ordered by (recorded_height, hash)
	require.Equal(t, []int64{5, 6, 7}, []int64{cs.Offences[0].RecordedHeight, cs.Offences[1].RecordedHeight, cs.Offences[2].RecordedHeight})
	require.Len(t, cs.UnfiredHashes, 3)
	require.True(t, sort.StringsAreSorted(cs.UnfiredHashes))
	require.Contains(t, cs.UnfiredHashes, types.FormatEvidenceHash(hc))
}

func sortedStrings(in []string) []string {
	out := append([]string(nil), in...)
	sort.Strings(out)
	return out
}

func TestTwoOffencesInOneBlockOrderByHash(t *testing.T) {
	f := initFixture(t)
	a, b := f.newActor(1), f.newActor(2)
	ea, eb := dupVote(a, 3), dupVote(b, 3)
	ha, hb := f.accept(ea), f.accept(eb)
	f.link(a, 3, "acme")
	f.link(b, 3, "acme")

	ctx := f.block(5, ea, eb)
	f.keeper.RecordOffences(ctx)

	require.Len(t, eventsOf(ctx, types.EventTypeEntityArmed), 1, "armed once, by the second distinct validator")
	cs, err := f.keeper.CaseOf(f.ctx, "acme")
	require.NoError(t, err)
	require.True(t, cs.Armed)
	require.Equal(t, int64(5), cs.ArmedHeight)
	want := [][]byte{ha, hb}
	sort.Slice(want, func(i, j int) bool { return bytes.Compare(want[i], want[j]) < 0 })
	require.Equal(t, types.FormatEvidenceHash(want[0]), cs.Offences[0].EvidenceHash)
	require.Equal(t, types.FormatEvidenceHash(want[1]), cs.Offences[1].EvidenceHash)
}

func TestUnrelatedOffencesNeverArm(t *testing.T) {
	f := initFixture(t)
	a, b := f.newActor(1), f.newActor(2)
	ea, eb := dupVote(a, 4), dupVote(b, 4)
	ha, hb := f.accept(ea), f.accept(eb)
	f.link(a, 4, "acme")
	f.link(b, 4, "beta")

	ctx := f.block(5, ea, eb)
	f.keeper.RecordOffences(ctx)

	require.Len(t, eventsOf(ctx, types.EventTypeOffenceRecorded), 2)
	require.Empty(t, eventsOf(ctx, types.EventTypeEntityArmed), "a shared client bug across unrelated operators arms nothing")
	require.True(t, f.indexed(t, "acme", ha))
	require.True(t, f.indexed(t, "beta", hb))
	for _, id := range []string{"acme", "beta"} {
		cs, err := f.keeper.CaseOf(f.ctx, id)
		require.NoError(t, err)
		require.False(t, cs.Armed, id)
		require.Len(t, cs.Offences, 1, id)
	}
}

func TestOffencesOfOneValidatorNeverArm(t *testing.T) {
	f := initFixture(t)
	a := f.newActor(1)
	e1, e2 := dupVote(a, 3), dupVote(a, 4)
	f.accept(e1)
	f.accept(e2)
	f.link(a, 3, "acme")
	f.link(a, 4, "acme")

	ctx := f.block(5, e1, e2)
	f.keeper.RecordOffences(ctx)

	require.Len(t, eventsOf(ctx, types.EventTypeOffenceRecorded), 2)
	require.Empty(t, eventsOf(ctx, types.EventTypeEntityArmed), "distinct validators, not offences, arm an entity")
	cs, err := f.keeper.CaseOf(f.ctx, "acme")
	require.NoError(t, err)
	require.False(t, cs.Armed)
	require.Equal(t, []string{a.op.String()}, cs.Validators)
}

func TestAnUnlinkedValidatorNeverArms(t *testing.T) {
	f := initFixture(t)
	a, b := f.newActor(1), f.newActor(2)
	ea, eb := dupVote(a, 4), dupVote(b, 4)
	ha, hb := f.accept(ea), f.accept(eb)

	ctx := f.block(5, ea, eb)
	f.keeper.RecordOffences(ctx)

	require.Equal(t, 2, f.entities.calls)
	require.Empty(t, eventsOf(ctx, types.EventTypeEntityArmed))
	for _, h := range [][]byte{ha, hb} {
		o, ok := f.offence(t, h)
		require.True(t, ok)
		require.Empty(t, o.EntityId)
	}
	n := 0
	require.NoError(t, f.keeper.OffencesByEntity.Walk(f.ctx, nil, func(collections.Pair[string, []byte]) (bool, error) {
		n++
		return false, nil
	}))
	require.Zero(t, n, "no index entries")
}

func TestARecordingErrorIsLoggedAndTheNextEvidenceIsRecorded(t *testing.T) {
	f := initFixture(t)
	a, b := f.newActor(1), f.newActor(2)
	ea, eb := dupVote(a, 4), dupVote(b, 4)
	ha, hb := f.accept(ea), f.accept(eb)
	f.link(a, 4, "acme")
	f.link(b, 4, "acme")
	f.staking.failCons[string(a.cons)] = true

	var logged bytes.Buffer
	ctx := f.block(5, ea, eb).WithLogger(log.NewLogger(&logged, log.ColorOption(false)))
	require.NotPanics(t, func() { f.keeper.RecordOffences(ctx) })

	// Spec §6.1: the log names the evidence and the consensus address.
	consA, err := f.staking.consCdc.BytesToString(a.cons)
	require.NoError(t, err)
	require.Contains(t, logged.String(), "recording an offence failed")
	require.Contains(t, logged.String(), types.FormatEvidenceHash(ha))
	require.Contains(t, logged.String(), consA)
	require.NotContains(t, logged.String(), types.FormatEvidenceHash(hb), "only the failed entry is logged")

	_, ok := f.offence(t, ha)
	require.False(t, ok, "the failed entry wrote nothing")
	require.False(t, f.indexed(t, "acme", ha))
	_, ok = f.offence(t, hb)
	require.True(t, ok)
	evs := eventsOf(ctx, types.EventTypeOffenceRecorded)
	require.Len(t, evs, 1)
	require.Equal(t, b.op.String(), attr(t, evs[0], types.AttributeKeyValidator))
	require.Empty(t, eventsOf(ctx, types.EventTypeEntityArmed))
}

func TestAnUnknownValidatorIsLoggedAndSkipped(t *testing.T) {
	f := initFixture(t)
	a := f.newActor(1)
	ghost := actor{op: sdk.ValAddress(bytes.Repeat([]byte{9}, 20)), cons: bytes.Repeat([]byte{0x19}, 20)} // not in the staking mock
	eg, ea := dupVote(ghost, 4), dupVote(a, 4)
	f.accept(eg)
	ha := f.accept(ea)

	ctx := f.block(5, eg, ea)
	require.NotPanics(t, func() { f.keeper.RecordOffences(ctx) })

	_, ok := f.offence(t, f.hashOf(eg))
	require.False(t, ok)
	_, ok = f.offence(t, ha)
	require.True(t, ok)
}

func TestNoCometInfoIsANoOp(t *testing.T) {
	f := initFixture(t)
	a := f.newActor(1)
	e := dupVote(a, 4)
	f.accept(e)

	ctx := sdk.UnwrapSDKContext(f.ctx).WithEventManager(sdk.NewEventManager()) // no comet info
	require.NotPanics(t, func() { f.keeper.RecordOffences(ctx) })

	_, ok := f.offence(t, f.hashOf(e))
	require.False(t, ok)
	require.Empty(t, ctx.EventManager().Events())

	// and a block with no misbehaviour at all
	empty := f.block(5)
	require.NotPanics(t, func() { f.keeper.RecordOffences(empty) })
	require.Empty(t, empty.EventManager().Events())
}

func TestCaseOfAnEntityWithoutOffencesIsEmpty(t *testing.T) {
	f := initFixture(t)
	cs, err := f.keeper.CaseOf(f.ctx, "nobody")
	require.NoError(t, err)
	require.Equal(t, "nobody", cs.EntityId)
	require.False(t, cs.Armed)
	require.Empty(t, cs.Offences)
	require.Empty(t, cs.Validators)
	require.Empty(t, cs.UnfiredHashes)
}

func TestUnfiredHashesLeaveOutAFiredOffence(t *testing.T) {
	f := initFixture(t)
	a, b := f.newActor(1), f.newActor(2)
	ea, eb := dupVote(a, 3), dupVote(b, 4)
	ha := f.accept(ea)
	hb := f.accept(eb)
	f.link(a, 3, "acme")
	f.link(b, 4, "acme")
	f.keeper.RecordOffences(f.block(5, ea, eb))

	o, ok := f.offence(t, ha)
	require.True(t, ok)
	o.SanctionId = 7
	require.NoError(t, f.keeper.Offences.Set(f.ctx, ha, o))

	cs, err := f.keeper.CaseOf(f.ctx, "acme")
	require.NoError(t, err)
	require.Equal(t, []string{types.FormatEvidenceHash(hb)}, cs.UnfiredHashes)
	require.True(t, cs.Armed, "a fired offence still counts toward armed")
}
