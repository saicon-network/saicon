package keeper_test

import (
	"bytes"
	"strings"
	"testing"

	"cosmossdk.io/collections"
	"cosmossdk.io/core/comet"
	"cosmossdk.io/math"
	stakingtypes "github.com/cosmos/cosmos-sdk/x/staking/types"
	"github.com/stretchr/testify/require"

	"github.com/saicon-network/saicon/chain/x/compliance/types"
	vrtypes "github.com/saicon-network/saicon/chain/x/validatorregistry/types"
)

// consistentEnv is the worked case after one firing: acme's offence A is fired
// by sanction 1, B is unfired, and a light-client-attack offence of a fourth
// validator has no entity. V-15 holds on it.
type invariantEnv struct {
	*sanctionEnv
	hLCA []byte
}

func newInvariantEnv(t *testing.T) *invariantEnv {
	t.Helper()
	e := newSanctionEnv(t)
	d := e.newActor(4)
	hLCA := e.hashOf(vote(d, comet.LightClientAttack, 10))
	e.evidence.hashes[string(hLCA)] = true
	cons, err := e.staking.consCdc.BytesToString(d.cons)
	require.NoError(t, err)
	require.NoError(t, e.keeper.Offences.Set(e.ctx, hLCA, types.Offence{
		EvidenceHash:       types.FormatEvidenceHash(hLCA),
		Validator:          d.op.String(),
		ConsensusAddress:   cons,
		Kind:               types.MisbehaviorKind_MISBEHAVIOR_KIND_LIGHT_CLIENT_ATTACK,
		InfractionHeight:   10,
		InfractionTime:     t0,
		Power:              300,
		DoubleSignFraction: dec010,
		RecordedHeight:     10,
		RecordedTime:       t0,
	}))
	_, err = e.fire(e.ha)
	require.NoError(t, err)
	return &invariantEnv{sanctionEnv: e, hLCA: hLCA}
}

func (e *invariantEnv) offenceOf(h []byte) types.Offence {
	o, err := e.keeper.Offences.Get(e.ctx, h)
	require.NoError(e.t, err)
	return o
}

func (e *invariantEnv) setOffence(h []byte, o types.Offence) {
	require.NoError(e.t, e.keeper.Offences.Set(e.ctx, h, o))
}

func (e *invariantEnv) sanction1() types.Sanction {
	s, err := e.keeper.Sanctions.Get(e.ctx, 1)
	require.NoError(e.t, err)
	return s
}

func (e *invariantEnv) setSanction(id uint64, s types.Sanction) {
	require.NoError(e.t, e.keeper.Sanctions.Set(e.ctx, id, s))
}

func TestCoordinatedAttackInvariantHoldsOnConsistentStates(t *testing.T) {
	t.Run("empty", func(t *testing.T) {
		f := initFixture(t)
		require.NoError(t, f.keeper.SanctionSeq.Set(f.ctx, 1))
		msg, broken := f.keeper.CoordinatedAttackInvariant(f.ctx)
		require.False(t, broken, msg)
	})
	t.Run("armed and unfired", func(t *testing.T) {
		e := newSanctionEnv(t)
		msg, broken := e.keeper.CoordinatedAttackInvariant(e.ctx)
		require.False(t, broken, msg)
	})
	t.Run("one offence fired, one not, one light-client attack", func(t *testing.T) {
		e := newInvariantEnv(t)
		msg, broken := e.keeper.CoordinatedAttackInvariant(e.ctx)
		require.False(t, broken, msg)
	})
}

// Each clause (a)-(f) of spec §13, and the nested records of a sanction, broken
// in turn: the invariant reports a break and the message names the clause.
func TestCoordinatedAttackInvariant(t *testing.T) {
	cases := []struct {
		name   string
		clause string
		break_ func(e *invariantEnv)
	}{
		// (a) every offence under its own hash, kind set, x/evidence has it.
		{"a: x/evidence lacks the hash", "a", func(e *invariantEnv) { delete(e.evidence.hashes, string(e.hb)) }},
		{"a: stored under another key", "a", func(e *invariantEnv) {
			other := bytes.Repeat([]byte{0xEE}, 32)
			e.evidence.hashes[string(other)] = true
			e.setOffence(other, e.offenceOf(e.hb))
		}},
		{"a: kind unspecified", "a", func(e *invariantEnv) {
			o := e.offenceOf(e.hb)
			o.Kind = types.MisbehaviorKind_MISBEHAVIOR_KIND_UNSPECIFIED
			e.setOffence(e.hb, o)
		}},
		{"a: power zero", "a", func(e *invariantEnv) {
			o := e.offenceOf(e.hb)
			o.Power = 0
			e.setOffence(e.hb, o)
		}},
		{"a: consensus address does not decode", "a", func(e *invariantEnv) {
			o := e.offenceOf(e.hb)
			o.ConsensusAddress = "not-an-address"
			e.setOffence(e.hb, o)
		}},
		// (b) the consensus address is tombstoned.
		{"b: not tombstoned", "b", func(e *invariantEnv) { e.slashing.tombstoned = false }},
		// (c) the by-entity index is exactly the linked duplicate-vote offences.
		{"c: index entry missing", "c", func(e *invariantEnv) {
			require.NoError(e.t, e.keeper.OffencesByEntity.Remove(e.ctx, collections.Join("acme", e.hb)))
		}},
		{"c: index entry for an offence without an entity", "c", func(e *invariantEnv) {
			require.NoError(e.t, e.keeper.OffencesByEntity.Set(e.ctx, collections.Join("acme", e.hLCA)))
		}},
		{"c: index entry for no offence", "c", func(e *invariantEnv) {
			require.NoError(e.t, e.keeper.OffencesByEntity.Set(e.ctx, collections.Join("acme", bytes.Repeat([]byte{0xDD}, 32))))
		}},
		{"c: index entry under another entity than the offence's", "c", func(e *invariantEnv) {
			require.NoError(e.t, e.keeper.OffencesByEntity.Set(e.ctx, collections.Join("other", e.hb)))
		}},
		{"c: light-client attack with an entity", "c", func(e *invariantEnv) {
			o := e.offenceOf(e.hLCA)
			o.EntityId = "acme"
			e.setOffence(e.hLCA, o)
		}},
		// (d) sanctions.
		{"d: id not below the sequence", "d", func(e *invariantEnv) { require.NoError(e.t, e.keeper.SanctionSeq.Set(e.ctx, 1)) }},
		{"d: stored under another id", "d", func(e *invariantEnv) {
			s := e.sanction1()
			require.NoError(e.t, e.keeper.SanctionSeq.Set(e.ctx, 9))
			e.setSanction(5, s)
		}},
		{"d: fraction out of bounds", "d", func(e *invariantEnv) {
			s := e.sanction1()
			s.Fraction = math.LegacyNewDecWithPrec(5, 2)
			e.setSanction(1, s)
		}},
		{"d: no targets", "d", func(e *invariantEnv) {
			s := e.sanction1()
			s.Targets = nil
			e.setSanction(1, s)
		}},
		{"d: target offence is unknown", "d", func(e *invariantEnv) {
			s := e.sanction1()
			s.Targets[0].EvidenceHash = strings.Repeat("CD", 32)
			e.setSanction(1, s)
		}},
		{"d: target offence carries no sanction id", "d", func(e *invariantEnv) {
			o := e.offenceOf(e.ha)
			o.SanctionId = 0
			e.setOffence(e.ha, o)
		}},
		{"d: fired offence of no sanction", "d", func(e *invariantEnv) {
			o := e.offenceOf(e.hb)
			o.SanctionId = 9
			e.setOffence(e.hb, o)
		}},
		{"d: fired offence listed by another sanction", "d", func(e *invariantEnv) {
			o := e.offenceOf(e.hb)
			o.SanctionId = 1
			e.setOffence(e.hb, o)
		}},
		{"d: target offence of another entity", "d", func(e *invariantEnv) {
			s := e.sanction1()
			s.EntityId = "other"
			e.setSanction(1, s)
		}},
		{"d: target validator is not its offence's", "d", func(e *invariantEnv) {
			s := e.sanction1()
			s.Targets[0].Validator = e.b.op.String()
			e.setSanction(1, s)
		}},
		{"d: negative extra fraction", "d", func(e *invariantEnv) {
			s := e.sanction1()
			s.Targets[0].ExtraFraction = math.LegacyNewDecWithPrec(-1, 2)
			e.setSanction(1, s)
		}},
		{"d: negative validator burned", "d", func(e *invariantEnv) {
			s := e.sanction1()
			s.Targets[0].ValidatorBurned = math.NewInt(-1)
			e.setSanction(1, s)
		}},
		{"d: negative base tokens", "d", func(e *invariantEnv) {
			s := e.sanction1()
			s.Targets[0].BaseTokens = math.NewInt(-1)
			e.setSanction(1, s)
		}},
		{"d: outcome unspecified", "d", func(e *invariantEnv) {
			s := e.sanction1()
			s.Targets[0].Outcome = types.TargetOutcome_TARGET_OUTCOME_UNSPECIFIED
			e.setSanction(1, s)
		}},
		{"d: classification unspecified", "d", func(e *invariantEnv) {
			s := e.sanction1()
			s.Targets[0].Classification = types.TargetClassification_TARGET_CLASSIFICATION_UNSPECIFIED
			e.setSanction(1, s)
		}},
		{"d: a slashed target skipped for its key", "d", func(e *invariantEnv) {
			s := e.sanction1()
			s.Targets[0].Classification = types.TargetClassification_TARGET_CLASSIFICATION_SKIPPED_KEY_REMOVED
			e.setSanction(1, s)
		}},
		{"d: one offence listed twice", "d", func(e *invariantEnv) {
			s := e.sanction1()
			s.Targets = append(s.Targets, s.Targets[0])
			e.setSanction(1, s)
		}},
		// (e) a sanctioned entity is armed.
		{"e: sanctioned entity is not armed", "e", func(e *invariantEnv) {
			c := e.newActor(5)
			e.setStaking(c, stakingtypes.Unbonding, t0)
			e.vr.statuses[c.op.String()] = vrtypes.Status_STATUS_JAILED
			hc := e.putOffence(e.t, c, 10, "solo", 10, t0, dec010)
			o := e.offenceOf(hc)
			o.SanctionId = 2
			e.setOffence(hc, o)
			e.setSanction(2, types.Sanction{
				Id: 2, EntityId: "solo", Fraction: dec030,
				Targets: []types.SanctionTarget{{
					EvidenceHash: o.EvidenceHash, Validator: o.Validator, ExtraFraction: dec020,
					ValidatorBurned: math.ZeroInt(), Outcome: types.TargetOutcome_TARGET_OUTCOME_SLASHED,
					BaseTokens: math.NewInt(1), Classification: types.TargetClassification_TARGET_CLASSIFICATION_CLASSIFIED,
				}},
			})
			require.NoError(e.t, e.keeper.SanctionSeq.Set(e.ctx, 3))
		}},
		// (f) a removed consensus address is tombstoned.
		{"f: removed consensus address not tombstoned", "f", func(e *invariantEnv) {
			c := e.newActor(6)
			e.slashing.notTombstoned = map[string]bool{string(c.cons): true}
			require.NoError(e.t, e.keeper.RemovedConsensus.Set(e.ctx, c.cons))
		}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			e := newInvariantEnv(t)
			msg, broken := e.keeper.CoordinatedAttackInvariant(e.ctx)
			require.False(t, broken, "the state must hold before it is broken: %s", msg)

			tc.break_(e)
			msg, broken = e.keeper.CoordinatedAttackInvariant(e.ctx)
			require.True(t, broken)
			require.True(t, strings.HasPrefix(msg, "V-15 ("+tc.clause+")"), "message %q should name clause %s", msg, tc.clause)
		})
	}
}

// A broken V-15 refuses the firing and the message travels in the error; the
// invariant itself never halts (it returns, it does not panic).
func TestABrokenInvariantRefusesTheFiring(t *testing.T) {
	e := newInvariantEnv(t)
	e.slashing.tombstoned = false
	_, err := e.fire(e.hb)
	require.ErrorIs(t, err, types.ErrCoordinatedInvariant)
	require.Contains(t, err.Error(), "V-15 (b)")
	require.Len(t, e.staking.slashes, 1, "only the first firing slashed")
}
