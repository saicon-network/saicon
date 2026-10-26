package keeper

import (
	"bytes"
	"context"
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
	rec := types.HookRecord{Tokens: tokens, Bonded: bonded, SelfBond: math.ZeroInt(), Existed: existed}
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
