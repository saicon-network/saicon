package keeper

import (
	"bytes"
	"context"
	"sort"
	"strconv"
	"strings"

	"cosmossdk.io/collections"
	"cosmossdk.io/core/comet"
	evidencetypes "cosmossdk.io/x/evidence/types"
	sdk "github.com/cosmos/cosmos-sdk/types"

	"github.com/saicon-network/saicon/chain/x/compliance/types"
)

// RecordOffences records every equivocation of this block that x/evidence
// accepted (spec §6.1). It runs in BeginBlock after x/evidence and never
// returns an error: a failure is logged and the next entry is processed.
func (k Keeper) RecordOffences(ctx context.Context) {
	sdkCtx := sdk.UnwrapSDKContext(ctx)
	info := sdkCtx.CometInfo()
	if info == nil || info.GetEvidence() == nil {
		return
	}
	list := info.GetEvidence()
	for i := 0; i < list.Len(); i++ {
		e := list.Get(i)
		var kind types.MisbehaviorKind
		switch e.Type() {
		case comet.DuplicateVote:
			kind = types.MisbehaviorKind_MISBEHAVIOR_KIND_DUPLICATE_VOTE
		case comet.LightClientAttack:
			kind = types.MisbehaviorKind_MISBEHAVIOR_KIND_LIGHT_CLIENT_ATTACK
		default:
			continue
		}
		cache, write := sdkCtx.CacheContext()
		if err := k.recordOffence(cache, e, kind); err != nil {
			eq := evidencetypes.FromABCIEvidence(e, k.stakingKeeper.ConsensusAddressCodec())
			sdkCtx.Logger().Error("compliance: recording an offence failed; it cannot arm",
				"evidence_hash", types.FormatEvidenceHash(eq.Hash()), "consensus_address", eq.ConsensusAddress,
				"height", e.Height(), "err", err)
			continue
		}
		write()
	}
}

// recordOffence records one misbehaviour entry, if x/evidence accepted it and it
// is not recorded yet. Events go to ctx's event manager, so the caller's cache
// context carries them to the block only when the write succeeds.
func (k Keeper) recordOffence(ctx sdk.Context, e comet.Evidence, kind types.MisbehaviorKind) error {
	eq := evidencetypes.FromABCIEvidence(e, k.stakingKeeper.ConsensusAddressCodec())
	hash := eq.Hash()

	recorded, err := k.Offences.Has(ctx, hash)
	if err != nil {
		return err
	}
	if recorded {
		return nil
	}
	stored, err := k.evidence.HasEquivocation(ctx, hash)
	if err != nil {
		return err
	}
	if !stored {
		return nil
	}

	validator, err := k.stakingKeeper.GetValidatorByConsAddr(ctx, sdk.ConsAddress(e.Validator().Address()))
	if err != nil {
		return err
	}
	operator, err := sdk.ValAddressFromBech32(validator.GetOperator())
	if err != nil {
		return err
	}
	d, err := k.slashingKeeper.SlashFractionDoubleSign(ctx)
	if err != nil {
		return err
	}

	entity := ""
	if kind == types.MisbehaviorKind_MISBEHAVIOR_KIND_DUPLICATE_VOTE {
		id, linked, err := k.entityKeeper.EntityAt(ctx, operator, eq.Height)
		if err != nil {
			return err
		}
		if linked {
			entity = id
		}
	}

	var before int
	if entity != "" {
		vals, err := k.entityValidators(ctx, entity)
		if err != nil {
			return err
		}
		before = len(vals)
	}

	offence := types.Offence{
		EvidenceHash:       types.FormatEvidenceHash(hash),
		Validator:          operator.String(),
		ConsensusAddress:   eq.ConsensusAddress,
		Kind:               kind,
		InfractionHeight:   eq.Height,
		InfractionTime:     eq.Time,
		Power:              eq.Power,
		DoubleSignFraction: d,
		EntityId:           entity,
		RecordedHeight:     ctx.BlockHeight(),
		RecordedTime:       ctx.BlockTime(),
	}
	if err := k.Offences.Set(ctx, hash, offence); err != nil {
		return err
	}
	if entity != "" {
		if err := k.OffencesByEntity.Set(ctx, collections.Join(entity, hash)); err != nil {
			return err
		}
	}
	ctx.EventManager().EmitEvent(sdk.NewEvent(types.EventTypeOffenceRecorded,
		sdk.NewAttribute(types.AttributeKeyEvidenceHash, offence.EvidenceHash),
		sdk.NewAttribute(types.AttributeKeyValidator, offence.Validator),
		sdk.NewAttribute(types.AttributeKeyKind, kind.String()),
		sdk.NewAttribute(types.AttributeKeyInfractionHeight, strconv.FormatInt(offence.InfractionHeight, 10)),
		sdk.NewAttribute(types.AttributeKeyEntityID, entity),
	))

	if entity == "" || before != 1 {
		return nil
	}
	vals, err := k.entityValidators(ctx, entity)
	if err != nil {
		return err
	}
	if len(vals) != 2 {
		return nil
	}
	ctx.EventManager().EmitEvent(sdk.NewEvent(types.EventTypeEntityArmed,
		sdk.NewAttribute(types.AttributeKeyEntityID, entity),
		sdk.NewAttribute(types.AttributeKeyValidators, joinValidators(vals)),
		sdk.NewAttribute(types.AttributeKeyArmedHeight, strconv.FormatInt(ctx.BlockHeight(), 10)),
	))
	return nil
}

// linkedOffences returns the duplicate-vote offences linked to an entity,
// ordered by hash bytes (the index order), with their hash bytes.
func (k Keeper) linkedOffences(ctx context.Context, entityID string) ([]types.Offence, [][]byte, error) {
	it, err := k.OffencesByEntity.Iterate(ctx, collections.NewPrefixedPairRange[string, []byte](entityID))
	if err != nil {
		return nil, nil, err
	}
	defer func() { _ = it.Close() }()

	var (
		offences []types.Offence
		hashes   [][]byte
	)
	for ; it.Valid(); it.Next() {
		key, err := it.Key()
		if err != nil {
			return nil, nil, err
		}
		o, err := k.Offences.Get(ctx, key.K2())
		if err != nil {
			return nil, nil, err
		}
		offences = append(offences, o)
		hashes = append(hashes, key.K2())
	}
	return offences, hashes, nil
}

// distinctValidators returns the operators of the offences, sorted by address
// bytes, each once.
func distinctValidators(offences []types.Offence) ([]sdk.ValAddress, error) {
	vals := make([]sdk.ValAddress, 0, len(offences))
	for _, o := range offences {
		addr, err := sdk.ValAddressFromBech32(o.Validator)
		if err != nil {
			return nil, err
		}
		vals = append(vals, addr)
	}
	sort.Slice(vals, func(i, j int) bool { return bytes.Compare(vals[i], vals[j]) < 0 })
	out := vals[:0]
	for i, v := range vals {
		if i == 0 || !bytes.Equal(v, vals[i-1]) {
			out = append(out, v)
		}
	}
	return out, nil
}

// entityValidators returns the distinct validators with a duplicate-vote offence
// linked to the entity, sorted by operator address bytes.
func (k Keeper) entityValidators(ctx context.Context, entityID string) ([]sdk.ValAddress, error) {
	offences, _, err := k.linkedOffences(ctx, entityID)
	if err != nil {
		return nil, err
	}
	return distinctValidators(offences)
}

func joinValidators(vals []sdk.ValAddress) string {
	s := make([]string, len(vals))
	for i, v := range vals {
		s[i] = v.String()
	}
	return strings.Join(s, ",")
}

// CaseOf is the query view of one entity's case (spec §6.3, §11): the
// duplicate-vote offences linked to it ordered by (recorded_height, hash),
// whether it is armed, and which offences no sanction has fired yet. An entity
// without offences returns an empty, unarmed case.
func (k Keeper) CaseOf(ctx context.Context, entityID string) (types.CoordinatedCase, error) {
	offences, hashes, err := k.linkedOffences(ctx, entityID)
	if err != nil {
		return types.CoordinatedCase{}, err
	}
	order := make([]int, len(offences))
	for i := range order {
		order[i] = i
	}
	sort.SliceStable(order, func(a, b int) bool {
		oa, ob := offences[order[a]], offences[order[b]]
		if oa.RecordedHeight != ob.RecordedHeight {
			return oa.RecordedHeight < ob.RecordedHeight
		}
		return bytes.Compare(hashes[order[a]], hashes[order[b]]) < 0
	})

	c := types.CoordinatedCase{EntityId: entityID}
	seen := map[string]bool{}
	for _, i := range order {
		o := offences[i]
		c.Offences = append(c.Offences, o)
		if !seen[o.Validator] {
			seen[o.Validator] = true
			if len(seen) == 2 {
				c.Armed = true
				c.ArmedHeight = o.RecordedHeight
				c.ArmedTime = o.RecordedTime
			}
		}
	}
	vals, err := distinctValidators(offences)
	if err != nil {
		return types.CoordinatedCase{}, err
	}
	for _, v := range vals {
		c.Validators = append(c.Validators, v.String())
	}
	// The index order is hash order, so the unfired hashes come out sorted.
	for i, o := range offences {
		if o.SanctionId == 0 {
			c.UnfiredHashes = append(c.UnfiredHashes, types.FormatEvidenceHash(hashes[i]))
		}
	}
	return c, nil
}
