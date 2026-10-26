package keeper_test

import (
	"testing"

	"cosmossdk.io/collections"
	"cosmossdk.io/math"
	sdk "github.com/cosmos/cosmos-sdk/types"
	stakingtypes "github.com/cosmos/cosmos-sdk/x/staking/types"
	"github.com/stretchr/testify/require"

	"github.com/saicon-network/saicon/chain/x/stakeengine/types"
	vrtypes "github.com/saicon-network/saicon/chain/x/validatorregistry/types"
)

// grow simulates x/staking's Delegate around the hooks: Before*, then the state
// change, then AfterDelegationModified.
func (f *fixture) grow(t *testing.T, d sdk.AccAddress, v sdk.ValAddress, amt math.Int, existed bool) error {
	h := f.k.Hooks()
	var err error
	if existed {
		err = h.BeforeDelegationSharesModified(f.ctx, d, v)
	} else {
		err = h.BeforeDelegationCreated(f.ctx, d, v)
	}
	require.NoError(t, err)
	f.staking.applyDelegation(d, v, amt)
	return h.AfterDelegationModified(f.ctx, d, v)
}

// shrink simulates x/staking's Unbond: BeforeDelegationSharesModified, the
// delegation changes, the hook (Removed or AfterModified) fires, and only then
// the validator's tokens fall.
func (f *fixture) shrink(t *testing.T, d sdk.AccAddress, v sdk.ValAddress, amt math.Int, removed bool) error {
	h := f.k.Hooks()
	require.NoError(t, h.BeforeDelegationSharesModified(f.ctx, d, v))
	before := append([]stakingtypes.Delegation(nil), f.staking.delegations[string(d)]...)
	var err error
	if removed {
		err = h.BeforeDelegationRemoved(f.ctx, d, v)
		f.staking.shrinkDelegation(d, v, amt, true)
	} else {
		f.staking.shrinkDelegation(d, v, amt, false)
		err = h.AfterDelegationModified(f.ctx, d, v)
	}
	if err != nil {
		// A failed message reverts, as the real multistore would.
		f.staking.delegations[string(d)] = before
		return err
	}
	f.staking.removeTokens(v, amt)
	return nil
}

// hookFixture: val(1) bonded with 1M SCN self-bond, caps of 2M SCN global and
// 1.5M SCN per validator, a second validator val(2) and a third val(3), each
// with a 2,000 SCN self-bond, and delegators acc(10)...
func hookFixture(t *testing.T) *fixture {
	f := newFixture(t)
	f.staking.addValidator(val(2), nscn(2_000))
	f.staking.addValidator(val(3), nscn(2_000))
	f.staking.addValidator(val(4), nscn(2_000))
	require.NoError(t, f.k.CapState.Set(f.ctx, types.CapState{
		Epoch:             1,
		PublicCirculating: nscn(10_000_000),
		GlobalCap:         nscn(2_000_000),
		EwmaBonded:        math.LegacyNewDecFromInt(nscn(1_000_000)),
		ValidatorCap:      nscn(1_500_000),
	}))
	return f
}

func TestHookGlobalCap(t *testing.T) {
	f := hookFixture(t)
	d := acc(10)
	headroom := nscn(2_000_000).Sub(f.staking.bonded)
	t.Run("growth to exactly the cap is ok", func(t *testing.T) {
		require.NoError(t, f.grow(t, d, val(2), headroom, false))
	})
	t.Run("growth past the cap is refused", func(t *testing.T) {
		err := f.grow(t, d, val(2), math.OneInt(), true)
		require.ErrorIs(t, err, types.ErrGlobalCapReached)
	})
}

func TestHookValidatorCap(t *testing.T) {
	f := hookFixture(t)
	require.NoError(t, f.k.CapState.Set(f.ctx, types.CapState{
		Epoch: 1, PublicCirculating: nscn(10_000_000), GlobalCap: nscn(100_000_000),
		EwmaBonded: math.LegacyNewDecFromInt(nscn(1_000_000)), ValidatorCap: nscn(10_000),
	}))
	err := f.grow(t, acc(10), val(2), nscn(8_001), false)
	require.ErrorIs(t, err, types.ErrValidatorCapReached)
	require.NoError(t, f.grow(t, acc(11), val(3), nscn(8_000), false))
}

func TestHookShrinkAboveCapIsFine(t *testing.T) {
	f := hookFixture(t)
	require.NoError(t, f.grow(t, acc(10), val(2), nscn(5_000), false))
	// Caps fall below existing stake.
	require.NoError(t, f.k.CapState.Set(f.ctx, types.CapState{
		Epoch: 2, PublicCirculating: nscn(10_000_000), GlobalCap: nscn(1_000),
		EwmaBonded: math.LegacyNewDecFromInt(nscn(1_000)), ValidatorCap: nscn(1_000),
	}))
	require.NoError(t, f.shrink(t, acc(10), val(2), nscn(1_000), false))
	require.NoError(t, f.shrink(t, acc(10), val(2), nscn(4_000), true))
}

func TestHookSlots(t *testing.T) {
	f := hookFixture(t)
	d := acc(10)
	require.NoError(t, f.grow(t, d, val(1), nscn(100), false))
	require.NoError(t, f.grow(t, d, val(2), nscn(100), false))
	require.NoError(t, f.grow(t, d, val(3), nscn(100), false))

	t.Run("a 4th validator is refused", func(t *testing.T) {
		err := f.grow(t, d, val(4), nscn(100), false)
		require.ErrorIs(t, err, types.ErrTooManyValidators)
	})
	t.Run("an existing slot is ok", func(t *testing.T) {
		require.NoError(t, f.grow(t, d, val(2), nscn(100), true))
	})
	t.Run("the operator's own stake is not a slot", func(t *testing.T) {
		// val(5)'s operator holds 3 other slots, then its self-delegation is
		// CREATED (existed=false), as MsgCreateValidator does.
		op := sdk.AccAddress(val(5))
		f.staking.validators[string(val(5))] = stakingtypes.Validator{
			OperatorAddress: val(5).String(), Tokens: math.ZeroInt(),
			DelegatorShares: math.LegacyZeroDec(), Status: stakingtypes.Bonded,
		}
		for _, v := range []sdk.ValAddress{val(1), val(2), val(3)} {
			require.NoError(t, f.grow(t, op, v, nscn(100), false))
		}
		require.NoError(t, f.grow(t, op, val(5), types.EntryFloor(), false))
	})
	t.Run("a queue entry's slot is reused by its fill", func(t *testing.T) {
		g := hookFixture(t)
		e := acc(11)
		require.NoError(t, g.grow(t, e, val(1), nscn(100), false))
		require.NoError(t, g.grow(t, e, val(2), nscn(100), false))
		// A queue entry for val(3) holds that slot; the fill creates the delegation.
		require.NoError(t, g.k.QueueByDelegator.Set(g.ctx, collections.Join(e.String(), val(3).String()), 1))
		require.NoError(t, g.grow(t, e, val(3), nscn(100), false))
	})
}

func TestHookSelfBondFloor(t *testing.T) {
	f := hookFixture(t)
	op := sdk.AccAddress(val(2))
	floor := types.EntryFloor()
	require.NoError(t, f.grow(t, op, val(2), nscn(1_000), true)) // 3,000 SCN of own stake

	t.Run("leaving floor-1 is refused", func(t *testing.T) {
		err := f.shrink(t, op, val(2), nscn(3_000).Sub(floor).AddRaw(1), false)
		require.ErrorIs(t, err, types.ErrSelfBondBelowFloor)
	})
	t.Run("leaving exactly the floor is ok", func(t *testing.T) {
		require.NoError(t, f.shrink(t, op, val(2), nscn(1_000), false))
	})
	t.Run("a full exit is ok", func(t *testing.T) {
		require.NoError(t, f.shrink(t, op, val(2), floor, true))
	})
}

func TestHookLockup(t *testing.T) {
	f := hookFixture(t)
	op := sdk.AccAddress(val(2))
	require.NoError(t, f.grow(t, op, val(2), nscn(1_000), true))
	f.reg.locked[string(val(2))] = true
	err := f.shrink(t, op, val(2), nscn(1_000), false)
	require.ErrorIs(t, err, vrtypes.ErrLockupActive)
	f.reg.locked[string(val(2))] = false
	require.NoError(t, f.shrink(t, op, val(2), nscn(1_000), false))
}

func TestHookSleepsInBeginBlockWindow(t *testing.T) {
	f := hookFixture(t)
	op := sdk.AccAddress(val(2))
	require.NoError(t, f.grow(t, op, val(2), nscn(1_000), true))
	f.reg.locked[string(val(2))] = true

	require.NoError(t, f.k.OpenBeginBlockWindow(f.ctx))
	// Case 1: growth past the global cap.
	require.NoError(t, f.grow(t, acc(10), val(2), nscn(2_000_000), false))
	// Case 6: self-bond below the floor.
	require.NoError(t, f.shrink(t, op, val(2), nscn(2_500), false))
	// Case 7: inside the lock-up.
	require.NoError(t, f.shrink(t, op, val(2), nscn(100), false))

	require.NoError(t, f.k.CloseBeginBlockWindow(f.ctx))
	err := f.grow(t, acc(11), val(3), nscn(1), false)
	require.ErrorIs(t, err, types.ErrGlobalCapReached)
	// The floor and the lock-up bite again too (self-bond is 400 SCN now).
	require.ErrorIs(t, f.shrink(t, op, val(2), nscn(100), false), vrtypes.ErrLockupActive)
	f.reg.locked[string(val(2))] = false
	require.ErrorIs(t, f.shrink(t, op, val(2), nscn(100), false), types.ErrSelfBondBelowFloor)
}

// Before stakeengine's InitGenesis there is no CapState; a genesis with
// initial_height > 1 runs gentx Delegate hooks at a non-zero height.
func TestHookSleepsWithoutCapState(t *testing.T) {
	f := newFixture(t)
	f.ctx = f.ctx.WithBlockHeight(5)
	f.staking.addValidator(val(2), nscn(2_000))
	require.NoError(t, f.grow(t, acc(10), val(2), nscn(9_000_000), false))
}

func TestHookHeightZero(t *testing.T) {
	f := hookFixture(t)
	f.ctx = f.ctx.WithBlockHeight(0)
	require.NoError(t, f.grow(t, acc(10), val(2), nscn(5_000_000), false))
	op := sdk.AccAddress(val(3))
	f.reg.locked[string(val(3))] = true
	require.NoError(t, f.shrink(t, op, val(3), nscn(1_500), false))
}

func TestHookAfterWithoutRecord(t *testing.T) {
	f := hookFixture(t)
	require.NoError(t, f.k.Hooks().BeforeDelegationRemoved(f.ctx, sdk.AccAddress(val(2)), val(2)))
	require.NoError(t, f.k.Hooks().AfterDelegationModified(f.ctx, acc(10), val(2)))
}

// slashTo lowers a validator's tokens (not its shares): the exchange rate falls
// and every delegation's value with it, as a slash does.
func (f *fixture) slashTo(v sdk.ValAddress, tokens math.Int) {
	cur := f.staking.validators[string(v)]
	cur.Tokens = tokens
	f.staking.validators[string(v)] = cur
}

func TestHookEntryFloorOnlyOnCreationOrShrink(t *testing.T) {
	t.Run("a top-up below the floor after a slash is allowed", func(t *testing.T) {
		f := hookFixture(t)
		op := sdk.AccAddress(val(2))
		f.slashTo(val(2), nscn(1_800)) // own stake 1,800 SCN, below the 2,000 floor
		require.NoError(t, f.grow(t, op, val(2), nscn(100), true))
		sb, err := f.k.SelfBond(f.ctx, val(2))
		require.NoError(t, err)
		require.True(t, sb.LT(types.EntryFloor()), "the fixture must stay below the floor")
	})
	t.Run("creation below the floor is refused", func(t *testing.T) {
		f := hookFixture(t)
		f.staking.validators[string(val(5))] = stakingtypes.Validator{
			OperatorAddress: val(5).String(), Tokens: math.ZeroInt(),
			DelegatorShares: math.LegacyZeroDec(), Status: stakingtypes.Bonded,
		}
		err := f.grow(t, sdk.AccAddress(val(5)), val(5), nscn(1_500), false)
		require.ErrorIs(t, err, types.ErrSelfBondBelowFloor)
	})
	t.Run("creation at the floor is allowed", func(t *testing.T) {
		f := hookFixture(t)
		f.staking.validators[string(val(5))] = stakingtypes.Validator{
			OperatorAddress: val(5).String(), Tokens: math.ZeroInt(),
			DelegatorShares: math.LegacyZeroDec(), Status: stakingtypes.Bonded,
		}
		require.NoError(t, f.grow(t, sdk.AccAddress(val(5)), val(5), types.EntryFloor(), false))
	})
	t.Run("a partial exit below the floor is still refused after a slash", func(t *testing.T) {
		f := hookFixture(t)
		op := sdk.AccAddress(val(2))
		require.NoError(t, f.grow(t, op, val(2), nscn(1_000), true))
		err := f.shrink(t, op, val(2), nscn(1_100), false) // 3,000 -> 1,900
		require.ErrorIs(t, err, types.ErrSelfBondBelowFloor)
	})
}

func TestHookLockupAllowsTopUp(t *testing.T) {
	f := hookFixture(t)
	op := sdk.AccAddress(val(2))
	f.reg.locked[string(val(2))] = true
	require.NoError(t, f.grow(t, op, val(2), nscn(500), true), "a top-up is not a withdrawal")
	err := f.shrink(t, op, val(2), nscn(500), false)
	require.ErrorIs(t, err, vrtypes.ErrLockupActive)
}
