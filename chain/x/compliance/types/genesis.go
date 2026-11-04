package types

import (
	"bytes"
	"fmt"

	"cosmossdk.io/math"
	"github.com/cosmos/cosmos-sdk/types/bech32"

	entityregistrytypes "github.com/saicon-network/saicon/chain/x/entityregistry/types"
)

// DefaultGenesis returns the default genesis state
func DefaultGenesis() *GenesisState {
	return &GenesisState{
		Params:         DefaultParams(),
		NextSanctionId: 1,
	}
}

// EffectiveNextSanctionID is the id the next sanction gets: next_sanction_id,
// where 0 reads as 1. Proto3 omits a zero and a hand-written genesis may leave
// the field out.
func (gs GenesisState) EffectiveNextSanctionID() uint64 {
	return max(1, gs.NextSanctionId)
}

// Validate performs basic genesis state validation returning an error upon any
// failure (spec §12). Addresses are checked against the explicit prefixes
// through x/entityregistry's decoders.
func (gs GenesisState) Validate() error {
	if err := gs.Params.Validate(); err != nil {
		return err
	}

	// Offences. Keys are the decoded hash and validator bytes, so a spelling
	// that decodes to the same bytes counts as the same.
	offences := make(map[string]*Offence, len(gs.Offences))
	validatorsOf := make(map[string]map[string]bool) // entity -> distinct validators, duplicate votes only
	for i := range gs.Offences {
		o := &gs.Offences[i]
		hash, err := ParseEvidenceHash(o.EvidenceHash)
		if err != nil {
			return fmt.Errorf("offence %d: %w", i, err)
		}
		if _, dup := offences[string(hash)]; dup {
			return fmt.Errorf("offence %s appears twice", o.EvidenceHash)
		}
		offences[string(hash)] = o

		validator, err := entityregistrytypes.DecodeValidator(o.Validator)
		if err != nil {
			return fmt.Errorf("offence %s validator: %w", o.EvidenceHash, err)
		}
		switch o.Kind {
		case MisbehaviorKind_MISBEHAVIOR_KIND_DUPLICATE_VOTE:
		case MisbehaviorKind_MISBEHAVIOR_KIND_LIGHT_CLIENT_ATTACK:
			if o.EntityId != "" {
				return fmt.Errorf("offence %s: a light-client attack is never linked to an entity", o.EvidenceHash)
			}
		default:
			return fmt.Errorf("offence %s has kind %s", o.EvidenceHash, o.Kind)
		}
		if o.EntityId != "" {
			if err := entityregistrytypes.ValidateEntityID(o.EntityId); err != nil {
				return fmt.Errorf("offence %s: %w", o.EvidenceHash, err)
			}
		}
		if o.Power < 1 {
			return fmt.Errorf("offence %s: power %d must be >= 1", o.EvidenceHash, o.Power)
		}
		if _, err := DecodeConsensusAddress(o.ConsensusAddress); err != nil {
			return fmt.Errorf("offence %s consensus_address: %w", o.EvidenceHash, err)
		}
		if o.InfractionHeight < 1 || o.RecordedHeight < 1 {
			return fmt.Errorf("offence %s: heights must be >= 1 (infraction %d, recorded %d)",
				o.EvidenceHash, o.InfractionHeight, o.RecordedHeight)
		}
		if o.RecordedHeight < o.InfractionHeight {
			return fmt.Errorf("offence %s recorded at height %d, before its infraction at %d",
				o.EvidenceHash, o.RecordedHeight, o.InfractionHeight)
		}
		if o.DoubleSignFraction.IsNil() || o.DoubleSignFraction.IsNegative() || o.DoubleSignFraction.GT(math.LegacyOneDec()) {
			return fmt.Errorf("offence %s: double_sign_fraction %s is outside [0, 1]", o.EvidenceHash, o.DoubleSignFraction)
		}
		if o.Kind == MisbehaviorKind_MISBEHAVIOR_KIND_DUPLICATE_VOTE && o.EntityId != "" {
			if validatorsOf[o.EntityId] == nil {
				validatorsOf[o.EntityId] = make(map[string]bool)
			}
			validatorsOf[o.EntityId][string(validator)] = true
		}
	}

	// Sanctions.
	next := gs.EffectiveNextSanctionID()
	ids := make(map[uint64]bool, len(gs.Sanctions))
	listedBy := make(map[string]uint64) // offence hash -> the sanction listing it
	for _, s := range gs.Sanctions {
		if s.Id == 0 {
			return fmt.Errorf("sanction id 0 is invalid")
		}
		if ids[s.Id] {
			return fmt.Errorf("sanction %d appears twice", s.Id)
		}
		ids[s.Id] = true
		if s.Id >= next {
			return fmt.Errorf("sanction %d is not below next_sanction_id %d", s.Id, next)
		}
		if err := entityregistrytypes.ValidateEntityID(s.EntityId); err != nil {
			return fmt.Errorf("sanction %d: %w", s.Id, err)
		}
		if err := ValidateCoordinatedFraction(s.Fraction); err != nil {
			return fmt.Errorf("sanction %d: %w", s.Id, err)
		}
		if len(s.Targets) == 0 {
			return fmt.Errorf("sanction %d has no targets", s.Id)
		}
		for _, t := range s.Targets {
			hash, err := ParseEvidenceHash(t.EvidenceHash)
			if err != nil {
				return fmt.Errorf("sanction %d target: %w", s.Id, err)
			}
			o, ok := offences[string(hash)]
			if !ok {
				return fmt.Errorf("sanction %d target %s is not a recorded offence", s.Id, t.EvidenceHash)
			}
			if by, dup := listedBy[string(hash)]; dup {
				return fmt.Errorf("offence %s is listed by sanctions %d and %d", t.EvidenceHash, by, s.Id)
			}
			listedBy[string(hash)] = s.Id
			if o.SanctionId != s.Id {
				return fmt.Errorf("sanction %d target %s: the offence carries sanction id %d", s.Id, t.EvidenceHash, o.SanctionId)
			}
			if o.EntityId != s.EntityId {
				return fmt.Errorf("sanction %d target %s: the offence is linked to %q, the sanction to %q",
					s.Id, t.EvidenceHash, o.EntityId, s.EntityId)
			}
			if err := validateTarget(s.Id, t, o); err != nil {
				return err
			}
		}
		if len(validatorsOf[s.EntityId]) < 2 {
			return fmt.Errorf("sanction %d: entity %q is not armed by the genesis offences", s.Id, s.EntityId)
		}
	}

	// Every fired offence is listed by exactly the sanction it names.
	for i := range gs.Offences {
		o := &gs.Offences[i]
		if o.SanctionId == 0 {
			continue
		}
		hash, _ := ParseEvidenceHash(o.EvidenceHash) // parsed above
		if listedBy[string(hash)] != o.SanctionId {
			return fmt.Errorf("offence %s carries sanction id %d, which does not list it", o.EvidenceHash, o.SanctionId)
		}
	}

	// Removed consensus keys: well formed and unique.
	removed := make(map[string]bool, len(gs.RemovedConsensusAddresses))
	for _, addr := range gs.RemovedConsensusAddresses {
		bz, err := DecodeConsensusAddress(addr)
		if err != nil {
			return fmt.Errorf("removed_consensus_addresses: %w", err)
		}
		if removed[string(bz)] {
			return fmt.Errorf("removed consensus address %s appears twice", addr)
		}
		removed[string(bz)] = true
	}
	return nil
}

// ConsensusAddressPrefix is the bech32 prefix of consensus addresses.
const ConsensusAddressPrefix = entityregistrytypes.AccountPrefix + "valcons"

// DecodeConsensusAddress returns the bytes of a saiconvalcons1… address and
// rejects any other prefix, as entityregistry's decoders do for the others.
func DecodeConsensusAddress(addr string) ([]byte, error) {
	hrp, bz, err := bech32.DecodeAndConvert(addr)
	if err != nil {
		return nil, fmt.Errorf("invalid consensus address %q: %w", addr, err)
	}
	if hrp != ConsensusAddressPrefix {
		return nil, fmt.Errorf("consensus address %q has prefix %q, want %q", addr, hrp, ConsensusAddressPrefix)
	}
	if len(bz) == 0 {
		return nil, fmt.Errorf("consensus address %q is empty", addr)
	}
	return bz, nil
}

// ValidateRecord checks the fields of a sanction target that proto3 cannot make
// non-optional, so genesis and V-15 check them: the fraction and both token
// amounts are set and not negative, the outcome is one of the three, and the
// classification is set and agrees with the outcome (a target skipped for its
// key is never slashable).
func (t SanctionTarget) ValidateRecord() error {
	if t.ExtraFraction.IsNil() || t.ExtraFraction.IsNegative() {
		return fmt.Errorf("extra_fraction %s must be set and not negative", t.ExtraFraction)
	}
	if t.ValidatorBurned.IsNil() || t.ValidatorBurned.IsNegative() {
		return fmt.Errorf("validator_burned must be set and not negative")
	}
	if t.BaseTokens.IsNil() || t.BaseTokens.IsNegative() {
		return fmt.Errorf("base_tokens must be set and not negative")
	}
	switch t.Outcome {
	case TargetOutcome_TARGET_OUTCOME_SLASHED,
		TargetOutcome_TARGET_OUTCOME_NOTHING_TO_ADD,
		TargetOutcome_TARGET_OUTCOME_NOT_SLASHABLE:
	default:
		return fmt.Errorf("outcome %s is not one of the three", t.Outcome)
	}
	switch t.Classification {
	case TargetClassification_TARGET_CLASSIFICATION_CLASSIFIED,
		TargetClassification_TARGET_CLASSIFICATION_SKIPPED_NO_REGISTRY_RECORD:
	case TargetClassification_TARGET_CLASSIFICATION_SKIPPED_KEY_REMOVED,
		TargetClassification_TARGET_CLASSIFICATION_SKIPPED_KEY_REUSED,
		TargetClassification_TARGET_CLASSIFICATION_SKIPPED_NO_VALIDATOR:
		if t.Outcome != TargetOutcome_TARGET_OUTCOME_NOT_SLASHABLE {
			return fmt.Errorf("classification %s with outcome %s: a target skipped for its key is not slashable",
				t.Classification, t.Outcome)
		}
	default:
		return fmt.Errorf("classification %s is not a recorded one", t.Classification)
	}
	return nil
}

// validateTarget checks the nested record of one sanction target against its
// offence: the amounts and the outcome, and that the validator is the offence's.
func validateTarget(sanctionID uint64, t SanctionTarget, o *Offence) error {
	where := fmt.Sprintf("sanction %d target %s", sanctionID, t.EvidenceHash)
	if err := t.ValidateRecord(); err != nil {
		return fmt.Errorf("%s: %w", where, err)
	}
	target, err := entityregistrytypes.DecodeValidator(t.Validator)
	if err != nil {
		return fmt.Errorf("%s validator: %w", where, err)
	}
	offender, err := entityregistrytypes.DecodeValidator(o.Validator)
	if err != nil {
		return fmt.Errorf("%s offence validator: %w", where, err)
	}
	if !bytes.Equal(target, offender) {
		return fmt.Errorf("%s: %s is not the validator of its offence, %s", where, t.Validator, o.Validator)
	}
	return nil
}
