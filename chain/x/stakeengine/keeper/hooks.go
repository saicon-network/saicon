package keeper

import (
	"bytes"
	"context"
	"crypto/sha256"
	"errors"

	errorsmod "cosmossdk.io/errors"
	"cosmossdk.io/math"
	sdk "github.com/cosmos/cosmos-sdk/types"
	"github.com/cosmos/cosmos-sdk/types/address"
	stakingtypes "github.com/cosmos/cosmos-sdk/x/staking/types"

	"github.com/saicon-network/saicon/chain/x/stakeengine/types"
	vrtypes "github.com/saicon-network/saicon/chain/x/validatorregistry/types"
)

// Hooks is the staking backstop (S7 spec §6). The ante decorator never sees
// messages that x/gov, x/group or an ICA host execute; x/staking's hooks run on
// every path. Before x/staking changes a delegation the before hook notes the
// validator's tokens, the bonded pool and the operator's own stake; the after
// hook compares and applies the same predicates the ante does. A hook error
// aborts the operation, and the message with it.
//
// The entity aggregate cap and the freeze are checked here too (spec §8.3). A
// redelegation inside one entity is an Unbond of the source followed by a Delegate
// to the destination, so the hook keeps, per transaction, delegator and entity,
// what the delegator took out of the entity's members, and lets a later growth at
// a member of the same entity use it up — the hook's counterpart of the ante
// skipping MsgBeginRedelegate between two members.
type Hooks struct{ k Keeper }

var _ stakingtypes.StakingHooks = Hooks{}

func (k Keeper) Hooks() Hooks { return Hooks{k: k} }

func recordKey(d sdk.AccAddress, v sdk.ValAddress) []byte {
	key := append([]byte{}, types.HookRecordPrefix...)
	key = append(key, address.MustLengthPrefix(d)...)
	return append(key, address.MustLengthPrefix(v)...)
}

func (h Hooks) asleep(ctx context.Context) (bool, error) {
	if sdk.UnwrapSDKContext(ctx).BlockHeight() == 0 {
		return true, nil // gentxs, before this module's state exists
	}
	if in, err := h.k.inBeginBlock(ctx); in || err != nil {
		return in, err
	}
	// No caps yet: genesis with initial_height > 1 runs gentx hooks before our InitGenesis.
	if _, err := h.k.capState(ctx); err != nil {
		if errors.Is(err, types.ErrCapsNotComputed) {
			return true, nil
		}
		return false, err
	}
	return false, nil
}

func (h Hooks) record(ctx context.Context, d sdk.AccAddress, v sdk.ValAddress, existed bool) error {
	if sleep, err := h.asleep(ctx); sleep || err != nil {
		return err
	}
	tokens, err := h.k.ValidatorTokens(ctx, v)
	if err != nil {
		return err
	}
	bonded, err := h.k.stakingKeeper.TotalBondedTokens(ctx)
	if err != nil {
		return err
	}
	rec := types.HookRecord{
		Tokens: tokens, Bonded: bonded, SelfBond: math.ZeroInt(), Existed: existed, DelegationTokens: math.ZeroInt(),
	}
	// A direct lookup, not EntityStanding, which would sum the whole entity.
	if m, isMember, err := h.k.entityKeeper.EntityOf(ctx, v); err != nil {
		return err
	} else if isMember {
		rec.EntityId = m.EntityId
		if existed {
			if rec.DelegationTokens, err = h.k.delegationTokens(ctx, d, v); err != nil {
				return err
			}
		}
	}
	if bytes.Equal(d, v) {
		rec.IsOperator = true
		if rec.SelfBond, err = h.k.SelfBond(ctx, v); err != nil {
			return err
		}
	}
	bz, err := h.k.cdc.Marshal(&rec)
	if err != nil {
		return err
	}
	return h.k.transientService.OpenTransientStore(ctx).Set(recordKey(d, v), bz)
}

func (h Hooks) check(ctx context.Context, d sdk.AccAddress, v sdk.ValAddress, removed bool) error {
	if sleep, err := h.asleep(ctx); sleep || err != nil {
		return err
	}
	st := h.k.transientService.OpenTransientStore(ctx)
	key := recordKey(d, v)
	bz, err := st.Get(key)
	if err != nil || bz == nil {
		return err // no record: not an operation we observed
	}
	if err := st.Delete(key); err != nil {
		return err
	}
	var rec types.HookRecord
	if err := h.k.cdc.Unmarshal(bz, &rec); err != nil {
		return err
	}
	if err := h.noteOutflow(ctx, d, v, rec, removed); err != nil {
		return err
	}
	caps, err := h.k.capState(ctx)
	if err != nil {
		return err
	}
	bonded, err := h.k.stakingKeeper.TotalBondedTokens(ctx)
	if err != nil {
		return err
	}
	if err := types.CheckGrowth(rec.Bonded, bonded, caps.GlobalCap, types.ErrGlobalCapReached, "total bonded stake"); err != nil {
		return err
	}
	tokens, err := h.k.ValidatorTokens(ctx, v)
	if err != nil {
		return err
	}
	if err := types.CheckGrowth(rec.Tokens, tokens, caps.ValidatorCap, types.ErrValidatorCapReached, "validator "+v.String()); err != nil {
		return err
	}
	if err := h.checkEntity(ctx, d, v, rec, tokens); err != nil {
		return err
	}
	if rec.IsOperator {
		after := math.ZeroInt()
		if !removed {
			if after, err = h.k.SelfBond(ctx, v); err != nil {
				return err
			}
		}
		// A top-up raises the validator's tokens before this hook; an unbond has not
		// lowered them yet. "Tokens did not grow" therefore tells a real reduction
		// from exchange-rate truncation on a top-up. The ante enforces the entry
		// floor only on creation and withdrawal; so does the hook.
		shrinking := after.LT(rec.SelfBond) && !tokens.GT(rec.Tokens)
		if shrinking {
			locked, unlockAt, err := h.k.registryKeeper.SelfStakeLockup(ctx, v)
			if err != nil {
				return err
			}
			if locked {
				return errorsmod.Wrapf(vrtypes.ErrLockupActive,
					"validator %s's own stake is locked until %s", v, unlockAt.UTC().Format("2006-01-02 15:04:05 MST"))
			}
		}
		// The operator's own stake is never a slot.
		if shrinking || !rec.Existed {
			return types.CheckSelfBondFloor(after)
		}
		return nil
	}
	if !rec.Existed {
		slots, err := h.k.DelegatorSlots(ctx, d)
		if err != nil {
			return err
		}
		max, err := h.k.MaxValidatorsPerDelegator(ctx)
		if err != nil {
			return err
		}
		// The new delegation is already among the slots; judge the others.
		others := make([]string, 0, len(slots))
		for _, s := range slots {
			if s != v.String() {
				others = append(others, s)
			}
		}
		return types.CheckNewSlot(others, v.String(), max)
	}
	return nil
}

// noteOutflow adds what the delegator took out of a member of an entity to the
// transaction's counter for that entity. For an Unbond the validator's tokens have
// not fallen yet when the hook fires, so the delegation after is measured at the
// old exchange rate, like the delegation before.
func (h Hooks) noteOutflow(ctx context.Context, d sdk.AccAddress, v sdk.ValAddress, rec types.HookRecord, removed bool) error {
	if rec.EntityId == "" {
		return nil
	}
	after := math.ZeroInt()
	if !removed {
		var err error
		if after, err = h.k.delegationTokens(ctx, d, v); err != nil {
			return err
		}
	}
	out := rec.DelegationTokens.Sub(after)
	if !out.IsPositive() {
		return nil
	}
	return h.k.addOutflow(ctx, d, rec.EntityId, out)
}

// checkEntity applies the entity predicate to the growth of v's entity in this
// operation, after the exemption for stake the same delegator took out of the
// same entity earlier in the transaction.
func (h Hooks) checkEntity(ctx context.Context, d sdk.AccAddress, v sdk.ValAddress, rec types.HookRecord, tokens math.Int) error {
	standing, err := h.k.EntityStanding(ctx, v)
	if err != nil {
		return err
	}
	before, after := rec.Tokens, tokens
	if standing.EntityID != "" {
		// Only v changed in this operation, so the entity grew by what v grew.
		before, after = standing.Tokens.Sub(tokens.Sub(rec.Tokens)), standing.Tokens
		// A redelegation inside the entity is not growth (spec §8.3): what this
		// delegator took out of the same entity earlier in this transaction is
		// exempt, and is consumed as it is used.
		if grown := after.Sub(before); grown.IsPositive() {
			exempt, err := h.k.consumeOutflow(ctx, d, standing.EntityID, grown)
			if err != nil {
				return err
			}
			before = before.Add(exempt)
		}
	}
	return types.CheckEntityGrowth(standing, before, after)
}

func (h Hooks) BeforeDelegationCreated(ctx context.Context, d sdk.AccAddress, v sdk.ValAddress) error {
	return h.record(ctx, d, v, false)
}

func (h Hooks) BeforeDelegationSharesModified(ctx context.Context, d sdk.AccAddress, v sdk.ValAddress) error {
	return h.record(ctx, d, v, true)
}

func (h Hooks) AfterDelegationModified(ctx context.Context, d sdk.AccAddress, v sdk.ValAddress) error {
	return h.check(ctx, d, v, false)
}

func (h Hooks) BeforeDelegationRemoved(ctx context.Context, d sdk.AccAddress, v sdk.ValAddress) error {
	return h.check(ctx, d, v, true)
}

func (Hooks) AfterValidatorCreated(context.Context, sdk.ValAddress) error   { return nil }
func (Hooks) BeforeValidatorModified(context.Context, sdk.ValAddress) error { return nil }
func (Hooks) AfterValidatorRemoved(context.Context, sdk.ConsAddress, sdk.ValAddress) error {
	return nil
}
func (Hooks) AfterValidatorBonded(context.Context, sdk.ConsAddress, sdk.ValAddress) error {
	return nil
}
func (Hooks) AfterValidatorBeginUnbonding(context.Context, sdk.ConsAddress, sdk.ValAddress) error {
	return nil
}
func (Hooks) BeforeValidatorSlashed(context.Context, sdk.ValAddress, math.LegacyDec) error {
	return nil
}
func (Hooks) AfterUnbondingInitiated(context.Context, uint64) error { return nil }

// delegationTokens is the delegator's own delegation to v in tokens, zero when it
// has none.
func (k Keeper) delegationTokens(ctx context.Context, d sdk.AccAddress, v sdk.ValAddress) (math.Int, error) {
	del, err := k.stakingKeeper.GetDelegation(ctx, d, v)
	if errors.Is(err, stakingtypes.ErrNoDelegation) {
		return math.ZeroInt(), nil
	}
	if err != nil {
		return math.Int{}, err
	}
	validator, err := k.stakingKeeper.GetValidator(ctx, v)
	if err != nil {
		return math.Int{}, err
	}
	return validator.TokensFromShares(del.Shares).TruncateInt(), nil
}

// outflowKey is the transient key of the tokens delegator d took out of entity
// entityID's members in the current transaction. It is keyed by the hash of the
// transaction's bytes, so one transaction's withdrawal never exempts another's
// growth. Everything that reaches the hook outside a transaction — messages
// x/gov executes in EndBlock, and EndBlock queue fills for arbitrary delegators
// — shares an empty TxBytes and so one key per (delegator, entity) per block: a
// growth consumes only what the same delegator took out of the same entity
// outside a transaction earlier in that block.
func outflowKey(ctx context.Context, d sdk.AccAddress, entityID string) []byte {
	sum := sha256.Sum256(sdk.UnwrapSDKContext(ctx).TxBytes())
	key := append([]byte{}, types.EntityOutflowPrefix...)
	key = append(key, sum[:]...)
	key = append(key, address.MustLengthPrefix(d)...)
	return append(key, entityID...)
}

func (k Keeper) outflow(ctx context.Context, key []byte) (math.Int, error) {
	bz, err := k.transientService.OpenTransientStore(ctx).Get(key)
	if err != nil || bz == nil {
		return math.ZeroInt(), err
	}
	var n math.Int
	if err := n.Unmarshal(bz); err != nil {
		return math.Int{}, err
	}
	return n, nil
}

// addOutflow raises the delegator's counter for the entity by amount.
func (k Keeper) addOutflow(ctx context.Context, d sdk.AccAddress, entityID string, amount math.Int) error {
	key := outflowKey(ctx, d, entityID)
	cur, err := k.outflow(ctx, key)
	if err != nil {
		return err
	}
	bz, err := cur.Add(amount).Marshal()
	if err != nil {
		return err
	}
	return k.transientService.OpenTransientStore(ctx).Set(key, bz)
}

// consumeOutflow returns min(want, counter) and lowers the counter by it,
// deleting the counter at zero.
func (k Keeper) consumeOutflow(ctx context.Context, d sdk.AccAddress, entityID string, want math.Int) (math.Int, error) {
	key := outflowKey(ctx, d, entityID)
	cur, err := k.outflow(ctx, key)
	if err != nil || !cur.IsPositive() {
		return math.ZeroInt(), err
	}
	used := math.MinInt(want, cur)
	st := k.transientService.OpenTransientStore(ctx)
	if left := cur.Sub(used); left.IsPositive() {
		bz, err := left.Marshal()
		if err != nil {
			return math.Int{}, err
		}
		return used, st.Set(key, bz)
	}
	return used, st.Delete(key)
}
