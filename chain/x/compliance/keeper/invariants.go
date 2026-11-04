package keeper

import (
	"bytes"
	"context"
	"fmt"

	"cosmossdk.io/collections"
	sdk "github.com/cosmos/cosmos-sdk/types"

	"github.com/saicon-network/saicon/chain/x/compliance/types"
)

// v15 formats a V-15 break naming its clause.
func v15(clause, format string, args ...any) (string, bool) {
	return fmt.Sprintf("V-15 (%s): ", clause) + fmt.Sprintf(format, args...), true
}

// CoordinatedAttackInvariant is V-15, coordinated-attack integrity (spec §13).
// It returns the first break found, or ("", false) when the records agree:
//
//	(a) every Offence is stored under its own hash, has a kind, sane fields, and
//	    x/evidence holds that hash;
//	(b) its consensus address is tombstoned in x/slashing;
//	(c) OffencesByEntity is exactly the set of duplicate-vote offences with an
//	    entity, and a light-client-attack offence has none;
//	(d) every Sanction is stored under its id, below the sequence, with a
//	    fraction in bounds and well-formed targets, each naming an offence that
//	    carries that sanction id and entity, and every fired offence is listed by
//	    its sanction;
//	(e) every sanctioned entity is armed;
//	(f) every consensus address recorded as removed decodes and is tombstoned.
//
// It runs at the start of every firing and every preview, and never halts: a
// break refuses the firing (fail-safe) and is reported by the preview. A store
// read error is reported as a break, for the same reason.
func (k Keeper) CoordinatedAttackInvariant(ctx context.Context) (string, bool) {
	var records []offenceRecord
	byKey := make(map[string]types.Offence)
	if err := k.Offences.Walk(ctx, nil, func(key []byte, o types.Offence) (bool, error) {
		records = append(records, offenceRecord{key: key, o: o})
		byKey[string(key)] = o
		return false, nil
	}); err != nil {
		return v15("a", "reading the offences: %s", err)
	}

	consCodec := k.stakingKeeper.ConsensusAddressCodec()
	for _, r := range records {
		o := r.o
		hash, err := types.ParseEvidenceHash(o.EvidenceHash)
		if err != nil || !bytes.Equal(hash, r.key) {
			return v15("a", "offence stored under %X carries hash %q", r.key, o.EvidenceHash)
		}
		switch o.Kind {
		case types.MisbehaviorKind_MISBEHAVIOR_KIND_DUPLICATE_VOTE, types.MisbehaviorKind_MISBEHAVIOR_KIND_LIGHT_CLIENT_ATTACK:
		default:
			return v15("a", "offence %s has kind %s", o.EvidenceHash, o.Kind)
		}
		if o.Power < 1 {
			return v15("a", "offence %s has power %d", o.EvidenceHash, o.Power)
		}
		if _, err := sdk.ValAddressFromBech32(o.Validator); err != nil {
			return v15("a", "offence %s validator %q: %s", o.EvidenceHash, o.Validator, err)
		}
		cons, err := consCodec.StringToBytes(o.ConsensusAddress)
		if err != nil || len(cons) == 0 {
			return v15("a", "offence %s consensus address %q does not decode", o.EvidenceHash, o.ConsensusAddress)
		}
		stored, err := k.evidence.HasEquivocation(ctx, r.key)
		if err != nil {
			return v15("a", "reading x/evidence for %s: %s", o.EvidenceHash, err)
		}
		if !stored {
			return v15("a", "x/evidence holds no equivocation for offence %s", o.EvidenceHash)
		}
		if !k.slashingKeeper.IsTombstoned(ctx, sdk.ConsAddress(cons)) {
			return v15("b", "the consensus address of offence %s is not tombstoned", o.EvidenceHash)
		}
	}

	if msg, broken := k.checkEntityIndex(ctx, records, byKey); broken {
		return msg, broken
	}
	if msg, broken := k.checkSanctions(ctx, records, byKey); broken {
		return msg, broken
	}
	return k.checkRemovedConsensus(ctx)
}

// checkRemovedConsensus is clause (f): the hook records only tombstoned keys,
// and x/slashing never clears a tombstone.
func (k Keeper) checkRemovedConsensus(ctx context.Context) (string, bool) {
	var broken string
	if err := k.RemovedConsensus.Walk(ctx, nil, func(cons []byte) (bool, error) {
		if len(cons) == 0 {
			broken, _ = v15("f", "an empty consensus address is recorded as removed")
			return true, nil
		}
		if !k.slashingKeeper.IsTombstoned(ctx, sdk.ConsAddress(cons)) {
			broken, _ = v15("f", "removed consensus address %X is not tombstoned", cons)
			return true, nil
		}
		return false, nil
	}); err != nil {
		return v15("f", "reading the removed consensus addresses: %s", err)
	}
	return broken, broken != ""
}

// offenceRecord is an Offence with the key it is stored under.
type offenceRecord struct {
	key []byte
	o   types.Offence
}

// checkEntityIndex is clause (c).
func (k Keeper) checkEntityIndex(ctx context.Context, records []offenceRecord, byKey map[string]types.Offence) (string, bool) {
	indexed := make(map[string]bool)
	if err := k.OffencesByEntity.Walk(ctx, nil, func(key collections.Pair[string, []byte]) (bool, error) {
		indexed[key.K1()+"\x00"+string(key.K2())] = true
		o, ok := byKey[string(key.K2())]
		if !ok {
			return true, fmt.Errorf("index entry (%s, %X) names no offence", key.K1(), key.K2())
		}
		if o.Kind != types.MisbehaviorKind_MISBEHAVIOR_KIND_DUPLICATE_VOTE || o.EntityId != key.K1() {
			return true, fmt.Errorf("index entry (%s, %X) does not match its offence (kind %s, entity %q)",
				key.K1(), key.K2(), o.Kind, o.EntityId)
		}
		return false, nil
	}); err != nil {
		return v15("c", "%s", err)
	}
	for _, r := range records {
		o, key := r.o, string(r.key)
		switch {
		case o.Kind == types.MisbehaviorKind_MISBEHAVIOR_KIND_LIGHT_CLIENT_ATTACK && o.EntityId != "":
			return v15("c", "light-client-attack offence %s is linked to %q", o.EvidenceHash, o.EntityId)
		case o.Kind == types.MisbehaviorKind_MISBEHAVIOR_KIND_DUPLICATE_VOTE && o.EntityId != "" &&
			!indexed[o.EntityId+"\x00"+key]:
			return v15("c", "duplicate-vote offence %s linked to %q is missing from the entity index", o.EvidenceHash, o.EntityId)
		}
	}
	return "", false
}

// checkSanctions is clauses (d) and (e).
func (k Keeper) checkSanctions(ctx context.Context, records []offenceRecord, byKey map[string]types.Offence) (string, bool) {
	next, err := k.SanctionSeq.Peek(ctx)
	if err != nil {
		return v15("d", "reading the sanction sequence: %s", err)
	}

	listedBy := make(map[string]uint64) // offence hash bytes -> the sanction listing it
	var entities []string               // sanctioned entities, once each, in sanction id order
	seenEntity := make(map[string]bool)
	var broken string
	err = k.Sanctions.Walk(ctx, nil, func(id uint64, s types.Sanction) (bool, error) {
		fail := func(format string, args ...any) (bool, error) {
			broken, _ = v15("d", "sanction %d: "+format, append([]any{id}, args...)...)
			return true, nil
		}
		if s.Id != id {
			return fail("stored under id %d", s.Id)
		}
		if id >= next {
			return fail("not below the sequence %d", next)
		}
		if err := types.ValidateCoordinatedFraction(s.Fraction); err != nil {
			return fail("%s", err)
		}
		if len(s.Targets) == 0 {
			return fail("has no targets")
		}
		for _, t := range s.Targets {
			hash, err := types.ParseEvidenceHash(t.EvidenceHash)
			if err != nil {
				return fail("target hash %q: %s", t.EvidenceHash, err)
			}
			o, ok := byKey[string(hash)]
			if !ok {
				return fail("target %s names no offence", t.EvidenceHash)
			}
			if o.SanctionId != id {
				return fail("target %s: the offence carries sanction id %d", t.EvidenceHash, o.SanctionId)
			}
			if o.EntityId != s.EntityId {
				return fail("target %s: the offence is linked to %q, the sanction to %q", t.EvidenceHash, o.EntityId, s.EntityId)
			}
			if by, dup := listedBy[string(hash)]; dup {
				return fail("target %s is also listed by sanction %d", t.EvidenceHash, by)
			}
			listedBy[string(hash)] = id
			if err := t.ValidateRecord(); err != nil {
				return fail("target %s: %s", t.EvidenceHash, err)
			}
			target, err1 := sdk.ValAddressFromBech32(t.Validator)
			offender, err2 := sdk.ValAddressFromBech32(o.Validator)
			if err1 != nil || err2 != nil || !bytes.Equal(target, offender) {
				return fail("target %s: validator %q is not the validator of its offence, %q", t.EvidenceHash, t.Validator, o.Validator)
			}
		}
		if !seenEntity[s.EntityId] {
			seenEntity[s.EntityId] = true
			entities = append(entities, s.EntityId)
		}
		return false, nil
	})
	if err != nil {
		return v15("d", "reading the sanctions: %s", err)
	}
	if broken != "" {
		return broken, true
	}
	for _, r := range records {
		o := r.o
		if o.SanctionId != 0 && listedBy[string(r.key)] != o.SanctionId {
			return v15("d", "offence %s carries sanction id %d, which does not list it", o.EvidenceHash, o.SanctionId)
		}
	}

	for _, entity := range entities {
		vals, err := k.entityValidators(ctx, entity)
		if err != nil {
			return v15("e", "reading the offences of entity %q: %s", entity, err)
		}
		if len(vals) < 2 {
			return v15("e", "sanctioned entity %q has duplicate-vote offences of %d distinct validators, want at least 2", entity, len(vals))
		}
	}
	return "", false
}
