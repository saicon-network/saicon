package keeper

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"time"

	"cosmossdk.io/collections"
	errorsmod "cosmossdk.io/errors"
	"cosmossdk.io/math"
	sdk "github.com/cosmos/cosmos-sdk/types"
	stakingtypes "github.com/cosmos/cosmos-sdk/x/staking/types"

	"github.com/saicon-network/saicon/chain/x/compliance/types"
)

// defaultSanctionReason is recorded when the message carries no reason.
const defaultSanctionReason = "ratified by governance"

// plannedSanction is a SanctionPlan with what the execution needs next to each
// target: the offence record (its consensus address, height and power) and the
// hash bytes it is stored under. All three slices are in target order.
type plannedSanction struct {
	plan      types.SanctionPlan
	offences  []types.Offence
	hashBytes [][]byte
}

// PlanSanction is the read-only plan of a firing (spec §7.3), shared by the
// handler and the preview query so that a previewed firing executes unless the
// state moves. The checks run in this order, the first failure returns:
//
//  1. V-15 holds;
//  2. the fraction is inside the code bounds, equals the governed
//     coordinated_attack_fraction, and is not below the double-sign fraction in force;
//  3. the entity exists, in any status;
//  4. the entity is armed;
//  5. every hash is an offence of that entity that no sanction has fired;
//  6. the appeal window has passed, counted from the later of the arming and
//     the latest named offence;
//  7. per target, sorted by operator address bytes: the consensus address is
//     tombstoned, the outcome is NOT_SLASHABLE, NOTHING_TO_ADD or SLASHED, and
//     the classification is decided (targetOf).
func (k Keeper) PlanSanction(ctx context.Context, entityID string, hashes []string, fraction math.LegacyDec) (types.SanctionPlan, error) {
	p, err := k.planSanction(ctx, entityID, hashes, fraction)
	if err != nil {
		return types.SanctionPlan{}, err
	}
	return p.plan, nil
}

func (k Keeper) planSanction(ctx context.Context, entityID string, hashes []string, fraction math.LegacyDec) (plannedSanction, error) {
	sdkCtx := sdk.UnwrapSDKContext(ctx)

	// 1. V-15 (fail-safe: a break refuses the firing, C15).
	if msg, broken := k.CoordinatedAttackInvariant(ctx); broken {
		return plannedSanction{}, types.ErrCoordinatedInvariant.Wrap(msg)
	}

	// 2. The fraction. The bounds first: a message can reach the handler through
	// a path that skips ValidateBasic.
	if err := types.ValidateCoordinatedFraction(fraction); err != nil {
		return plannedSanction{}, err
	}
	params, err := k.Params.Get(ctx)
	if err != nil {
		return plannedSanction{}, err
	}
	if !fraction.Equal(params.CoordinatedAttackFraction) {
		return plannedSanction{}, types.ErrFractionMismatch.Wrapf("message %s, parameter %s",
			fraction, params.CoordinatedAttackFraction)
	}
	doubleSign, err := k.slashingKeeper.SlashFractionDoubleSign(ctx)
	if err != nil {
		return plannedSanction{}, errorsmod.Wrap(err, "reading the double-sign fraction")
	}
	if fraction.LT(doubleSign) {
		return plannedSanction{}, types.ErrInvalidFraction.Wrapf("%s is below the double-sign fraction in force, %s",
			fraction, doubleSign)
	}

	// 3. The entity exists (a revoked one can be sanctioned too).
	exists, err := k.entityKeeper.HasEntity(ctx, entityID)
	if err != nil {
		return plannedSanction{}, err
	}
	if !exists {
		return plannedSanction{}, types.ErrEntityNotFound.Wrapf("entity %q", entityID)
	}

	// 4. Armed.
	c, err := k.CaseOf(ctx, entityID)
	if err != nil {
		return plannedSanction{}, err
	}
	if !c.Armed {
		return plannedSanction{}, types.ErrNotArmed.Wrapf("entity %q has offences of %d distinct validators", entityID, len(c.Validators))
	}

	// 5. The named offences.
	hashBytes, err := types.ParseEvidenceHashes(hashes)
	if err != nil {
		return plannedSanction{}, err
	}
	named := make([]types.Offence, len(hashBytes))
	start := c.ArmedTime
	for i, h := range hashBytes {
		o, err := k.Offences.Get(ctx, h)
		if errors.Is(err, collections.ErrNotFound) {
			return plannedSanction{}, types.ErrUnknownOffence.Wrapf("%s", hashes[i])
		}
		if err != nil {
			return plannedSanction{}, err
		}
		if o.EntityId != entityID {
			return plannedSanction{}, types.ErrOffenceNotLinked.Wrapf("%s is linked to %q, not %q", hashes[i], o.EntityId, entityID)
		}
		if o.SanctionId != 0 {
			return plannedSanction{}, types.ErrAlreadySanctioned.Wrapf("%s was fired by sanction %d", hashes[i], o.SanctionId)
		}
		named[i] = o
		if o.RecordedTime.After(start) {
			start = o.RecordedTime
		}
	}

	// 6. The appeal window.
	earliest := start.Add(time.Duration(params.AppealWindowSeconds) * time.Second)
	if sdkCtx.BlockTime().Before(earliest) {
		return plannedSanction{}, types.ErrAppealWindowOpen.Wrapf("earliest execution at %s",
			earliest.UTC().Format(time.RFC3339))
	}

	// 7. The targets, sorted by operator address bytes (then hash bytes).
	type targetRef struct {
		op   sdk.ValAddress
		hash []byte
		o    types.Offence
	}
	refs := make([]targetRef, len(named))
	for i, o := range named {
		op, err := sdk.ValAddressFromBech32(o.Validator)
		if err != nil {
			return plannedSanction{}, errorsmod.Wrapf(err, "offence %s validator", o.EvidenceHash)
		}
		refs[i] = targetRef{op: op, hash: hashBytes[i], o: o}
	}
	sort.Slice(refs, func(i, j int) bool {
		if c := bytes.Compare(refs[i].op, refs[j].op); c != 0 {
			return c < 0
		}
		return bytes.Compare(refs[i].hash, refs[j].hash) < 0
	})

	out := plannedSanction{plan: types.SanctionPlan{EarliestExecutionTime: earliest}}
	for _, ref := range refs {
		o := ref.o
		cons, err := k.stakingKeeper.ConsensusAddressCodec().StringToBytes(o.ConsensusAddress)
		if err != nil {
			return plannedSanction{}, errorsmod.Wrapf(err, "offence %s consensus address", o.EvidenceHash)
		}
		if !k.slashingKeeper.IsTombstoned(ctx, sdk.ConsAddress(cons)) {
			return plannedSanction{}, types.ErrNotTombstoned.Wrapf("offence %s, validator %s", o.EvidenceHash, o.Validator)
		}

		extra := fraction.Sub(o.DoubleSignFraction)
		if extra.IsNegative() {
			extra = math.LegacyZeroDec()
		}
		outcome, classification, unbondingUntil, err := k.targetOf(ctx, ref.op, sdk.ConsAddress(cons), extra)
		if err != nil {
			return plannedSanction{}, errorsmod.Wrapf(err, "offence %s, validator %s", o.EvidenceHash, o.Validator)
		}
		if outcome == types.TargetOutcome_TARGET_OUTCOME_SLASHED && unbondingUntil != nil &&
			(out.plan.SlashableUntil == nil || unbondingUntil.Before(*out.plan.SlashableUntil)) {
			out.plan.SlashableUntil = unbondingUntil
		}

		out.plan.Targets = append(out.plan.Targets, types.SanctionTarget{
			EvidenceHash:    o.EvidenceHash,
			Validator:       o.Validator,
			ExtraFraction:   extra,
			ValidatorBurned: math.ZeroInt(),
			Outcome:         outcome,
			BaseTokens:      k.stakingKeeper.TokensFromConsensusPower(ctx, o.Power),
			Classification:  classification,
		})
		out.offences = append(out.offences, o)
		out.hashBytes = append(out.hashBytes, ref.hash)
	}
	return out, nil
}

// targetOf decides one target (spec §8.3, §8.6): the outcome, whether the
// target is classified, and, for a validator x/staking has unbonding, when it
// stops being slashable. Only the incarnation that committed the offence is
// slashed or classified: the consensus key must not have been removed since,
// and x/staking must hold it under the offence's operator. A target that is not
// slashable for that reason is NOT_SLASHABLE and skips the classification; one
// whose operator has no registry record is slashed as planned and skips the
// classification, which would otherwise fail the whole firing.
func (k Keeper) targetOf(ctx context.Context, operator sdk.ValAddress, cons sdk.ConsAddress, extra math.LegacyDec) (
	types.TargetOutcome, types.TargetClassification, *time.Time, error) {
	notSlashable := types.TargetOutcome_TARGET_OUTCOME_NOT_SLASHABLE

	removed, err := k.RemovedConsensus.Has(ctx, cons)
	if err != nil {
		return 0, 0, nil, errorsmod.Wrap(err, "reading the removed consensus keys")
	}
	if removed {
		return notSlashable, types.TargetClassification_TARGET_CLASSIFICATION_SKIPPED_KEY_REMOVED, nil, nil
	}
	val, err := k.stakingKeeper.GetValidatorByConsAddr(ctx, cons)
	if errors.Is(err, stakingtypes.ErrNoValidatorFound) {
		return notSlashable, types.TargetClassification_TARGET_CLASSIFICATION_SKIPPED_NO_VALIDATOR, nil, nil
	}
	if err != nil {
		return 0, 0, nil, errorsmod.Wrap(err, "reading the validator")
	}
	holder, err := sdk.ValAddressFromBech32(val.GetOperator())
	if err != nil {
		return 0, 0, nil, errorsmod.Wrapf(err, "operator of the validator x/staking holds")
	}
	if !bytes.Equal(holder, operator) {
		return notSlashable, types.TargetClassification_TARGET_CLASSIFICATION_SKIPPED_KEY_REUSED, nil, nil
	}

	outcome := types.TargetOutcome_TARGET_OUTCOME_SLASHED
	switch {
	case val.IsUnbonded():
		outcome = notSlashable
	case extra.IsZero():
		outcome = types.TargetOutcome_TARGET_OUTCOME_NOTHING_TO_ADD
	}
	var until *time.Time
	if val.IsUnbonding() {
		t := val.UnbondingTime
		until = &t
	}

	_, registered, err := k.validatorregistryKeeper.GetValidatorStatus(ctx, operator)
	if err != nil {
		return 0, 0, nil, errorsmod.Wrap(err, "reading the registry record")
	}
	if !registered {
		return outcome, types.TargetClassification_TARGET_CLASSIFICATION_SKIPPED_NO_REGISTRY_RECORD, until, nil
	}
	return outcome, types.TargetClassification_TARGET_CLASSIFICATION_CLASSIFIED, until, nil
}

// FireCoordinatedSanction executes a coordinated-attack sanction (spec §7, §8).
// It is the gov authority's message, ratified by a vote that named the evidence.
//
// The slashes run first and the sequence moves only after the last one, so a
// failed slash leaves no record of this module behind; the records, the
// classification and the events follow. The handler has no cache of its own:
// what makes the whole message atomic, the burns of earlier targets included, is
// x/gov, which runs a proposal's messages in a cache and writes it only when
// every message succeeds.
func (k msgServer) FireCoordinatedSanction(ctx context.Context, msg *types.MsgFireCoordinatedSanction) (*types.MsgFireCoordinatedSanctionResponse, error) {
	if err := k.assertAuthority(msg.Authority); err != nil {
		return nil, err
	}
	if err := msg.ValidateBasic(); err != nil {
		return nil, err
	}
	p, err := k.planSanction(ctx, msg.EntityId, msg.EvidenceHashes, msg.Fraction)
	if err != nil {
		return nil, err
	}
	sdkCtx := sdk.UnwrapSDKContext(ctx)

	// Spec §8.4: the slash runs with x/stakeengine's backstop asleep, as it does
	// in BeginBlock; slashing a redelegation into a locked-up operator would
	// otherwise fail the proposal.
	if err := k.window.OpenBeginBlockWindow(ctx); err != nil {
		return nil, err
	}
	defer func() {
		if err := k.window.CloseBeginBlockWindow(ctx); err != nil {
			sdkCtx.Logger().Error("compliance: closing the slash window failed", "err", err)
		}
	}()

	// Spec §8.5: the marker keeps the double-sign hook out of the sanction's own slash.
	slashCtx := sdkCtx.WithValue(sanctionInProgress{}, true)
	consCodec := k.stakingKeeper.ConsensusAddressCodec()
	for i := range p.plan.Targets {
		t := &p.plan.Targets[i]
		if t.Outcome != types.TargetOutcome_TARGET_OUTCOME_SLASHED {
			continue
		}
		o := p.offences[i]
		cons, err := consCodec.StringToBytes(o.ConsensusAddress)
		if err != nil {
			return nil, errorsmod.Wrapf(err, "offence %s consensus address", o.EvidenceHash)
		}
		burned, err := k.stakingKeeper.SlashWithInfractionReason(slashCtx, sdk.ConsAddress(cons),
			o.InfractionHeight-sdk.ValidatorUpdateDelay, o.Power, t.ExtraFraction,
			stakingtypes.Infraction_INFRACTION_DOUBLE_SIGN)
		if err != nil {
			return nil, errorsmod.Wrapf(err, "slashing validator %s", t.Validator)
		}
		if !burned.IsNil() {
			t.ValidatorBurned = burned
		}
	}

	id, err := k.SanctionSeq.Next(ctx)
	if err != nil {
		return nil, err
	}
	reason := msg.Reason
	if reason == "" {
		reason = defaultSanctionReason
	}
	flagReason := fmt.Sprintf("coordinated-attack sanction %d (entity %s): %s", id, msg.EntityId, reason)

	for i, t := range p.plan.Targets {
		operator, err := sdk.ValAddressFromBech32(t.Validator)
		if err != nil {
			return nil, errorsmod.Wrapf(err, "target %s", t.EvidenceHash)
		}
		// Punishment may zero reputation, never EP (V-8): the keeper's interface
		// cannot reach SetEP.
		if t.Classification == types.TargetClassification_TARGET_CLASSIFICATION_CLASSIFIED {
			if err := k.SetCompliance(ctx, operator, false,
				types.ComplianceEventType_COMPLIANCE_EVENT_TYPE_COORDINATED_ATTACK, flagReason); err != nil {
				return nil, err
			}
			k.zeroReputationBestEffort(ctx, operator)
		}

		o := p.offences[i]
		o.SanctionId = id
		if err := k.Offences.Set(ctx, p.hashBytes[i], o); err != nil {
			return nil, err
		}
		sdkCtx.EventManager().EmitEvent(sdk.NewEvent(types.EventTypeSanctionTarget,
			sdk.NewAttribute(types.AttributeKeySanctionID, strconv.FormatUint(id, 10)),
			sdk.NewAttribute(types.AttributeKeyValidator, t.Validator),
			sdk.NewAttribute(types.AttributeKeyEvidenceHash, t.EvidenceHash),
			sdk.NewAttribute(types.AttributeKeyExtraFraction, t.ExtraFraction.String()),
			sdk.NewAttribute(types.AttributeKeyValidatorBurned, t.ValidatorBurned.String()),
			sdk.NewAttribute(types.AttributeKeyOutcome, t.Outcome.String()),
			sdk.NewAttribute(types.AttributeKeyClassification, t.Classification.String()),
		))
	}

	// The targets are in operator order; the fired event lists the hashes in the
	// order the proposal named them.
	if err := k.Sanctions.Set(ctx, id, types.Sanction{
		Id:        id,
		EntityId:  msg.EntityId,
		Fraction:  msg.Fraction,
		Targets:   p.plan.Targets,
		Authority: msg.Authority,
		Reason:    reason,
		Height:    sdkCtx.BlockHeight(),
		Time:      sdkCtx.BlockTime(),
	}); err != nil {
		return nil, err
	}
	sdkCtx.EventManager().EmitEvent(sdk.NewEvent(types.EventTypeSanctionFired,
		sdk.NewAttribute(types.AttributeKeySanctionID, strconv.FormatUint(id, 10)),
		sdk.NewAttribute(types.AttributeKeyEntityID, msg.EntityId),
		sdk.NewAttribute(types.AttributeKeyFraction, msg.Fraction.String()),
		sdk.NewAttribute(types.AttributeKeyEvidenceHashes, strings.Join(msg.EvidenceHashes, ",")),
		sdk.NewAttribute(types.AttributeKeyAuthority, msg.Authority),
	))
	return &types.MsgFireCoordinatedSanctionResponse{SanctionId: id}, nil
}
