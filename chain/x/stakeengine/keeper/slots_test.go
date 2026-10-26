package keeper_test

import (
	"sort"
	"testing"

	"cosmossdk.io/collections"
	"cosmossdk.io/math"
	sdk "github.com/cosmos/cosmos-sdk/types"
	stakingtypes "github.com/cosmos/cosmos-sdk/x/staking/types"
	"github.com/stretchr/testify/require"

	"github.com/saicon-network/saicon/chain/x/stakeengine/types"
)

// delegate is a plain delegation through the staking stub, as x/staking's own
// MsgDelegate does it: the coins leave the delegator's account.
func (f *fixture) delegate(t *testing.T, d sdk.AccAddress, v sdk.ValAddress, amt math.Int) {
	t.Helper()
	validator, err := f.staking.GetValidator(f.ctx, v)
	require.NoError(t, err)
	_, err = f.staking.Delegate(f.ctx, d, amt, stakingtypes.Unbonded, validator, true)
	require.NoError(t, err)
}

func (f *fixture) indexCount(t *testing.T, d sdk.AccAddress, v sdk.ValAddress) uint64 {
	t.Helper()
	n, err := f.k.QueueByDelegator.Get(f.ctx, collections.Join(d.String(), v.String()))
	if err != nil {
		return 0
	}
	return n
}

func TestSlotsCountDelegationsAndQueueEntriesOnce(t *testing.T) {
	f := newFixture(t)
	d := acc(9)
	f.staking.addValidator(val(2), nscn(1000))
	f.staking.addValidator(val(3), nscn(1000))
	f.bank.set(d, nscn(10_000))
	// one delegation to val1, two queue entries to val2, one queue entry to val1
	f.delegate(t, d, val(1), nscn(200))
	_, err := f.k.Enqueue(f.ctx, d, val(2), nscn(200))
	require.NoError(t, err)
	_, err = f.k.Enqueue(f.ctx, d, val(2), nscn(200))
	require.NoError(t, err)
	_, err = f.k.Enqueue(f.ctx, d, val(1), nscn(200))
	require.NoError(t, err)
	slots, err := f.k.DelegatorSlots(f.ctx, d)
	require.NoError(t, err)
	want := []string{val(1).String(), val(2).String()}
	sort.Strings(want)            // the order is by bech32 string
	require.Equal(t, want, slots) // sorted, distinct
}

func TestSelfBondIsNotASlot(t *testing.T) {
	f := newFixture(t)
	op := acc(1) // val(1)'s operator, self-bonded by the fixture
	f.delegate(t, op, val(1), nscn(2_000))
	slots, err := f.k.DelegatorSlots(f.ctx, op)
	require.NoError(t, err)
	require.Empty(t, slots)
}

func TestEnqueueToAFourthValidatorIsRefused(t *testing.T) {
	f := newFixture(t)
	d := acc(9)
	for i := byte(2); i <= 5; i++ {
		f.staking.addValidator(val(i), nscn(1000))
	}
	f.bank.set(d, nscn(10_000))
	for i := byte(2); i <= 4; i++ {
		_, err := f.k.Enqueue(f.ctx, d, val(i), nscn(100))
		require.NoError(t, err)
	}
	_, err := f.k.Enqueue(f.ctx, d, val(5), nscn(100))
	require.ErrorIs(t, err, types.ErrTooManyValidators)
	_, err = f.k.Enqueue(f.ctx, d, val(3), nscn(100)) // an existing slot is fine
	require.NoError(t, err)
	f.requireV10(t)
}

func TestEnqueueRoutedSkipsTheQueueMinimumButNotTheSlotRule(t *testing.T) {
	f := newFixture(t)
	d := acc(9)
	f.setParams(t, func(p *types.Params) { p.MinQueueEntry = nscn(100) })
	for i := byte(2); i <= 5; i++ {
		f.staking.addValidator(val(i), nscn(1000))
	}
	f.bank.set(d, nscn(10_000))
	_, err := f.k.Enqueue(f.ctx, d, val(2), math.NewInt(5))
	require.ErrorIs(t, err, types.ErrQueueEntryTooSmall)
	_, err = f.k.EnqueueRouted(f.ctx, d, val(2), math.NewInt(5))
	require.NoError(t, err)
	for i := byte(3); i <= 4; i++ {
		_, err = f.k.EnqueueRouted(f.ctx, d, val(i), math.NewInt(5))
		require.NoError(t, err)
	}
	_, err = f.k.EnqueueRouted(f.ctx, d, val(5), math.NewInt(5))
	require.ErrorIs(t, err, types.ErrTooManyValidators)
	f.requireV10(t)
}

func TestTheIndexFollowsCancelAndFill(t *testing.T) {
	f := newFixture(t)
	d := acc(9)
	f.staking.addValidator(val(2), nscn(1000))
	f.bank.set(d, nscn(10_000))
	f.devnetFloor(t)
	f.caps(t)

	requireSlots := func(want ...string) {
		t.Helper()
		slots, err := f.k.DelegatorSlots(f.ctx, d)
		require.NoError(t, err)
		require.Equal(t, want, append([]string{}, slots...))
	}

	a, err := f.k.Enqueue(f.ctx, d, val(2), nscn(100))
	require.NoError(t, err)
	_, err = f.k.Enqueue(f.ctx, d, val(2), nscn(300))
	require.NoError(t, err)
	require.Equal(t, uint64(2), f.indexCount(t, d, val(2)))
	requireSlots(val(2).String())
	f.requireV10(t)

	// cancel one: the count drops, the other keeps the slot
	_, err = f.k.Cancel(f.ctx, d, a)
	require.NoError(t, err)
	require.Equal(t, uint64(1), f.indexCount(t, d, val(2)))
	requireSlots(val(2).String())
	f.requireV10(t)

	// a partial fill (50 SCN of room) leaves the entry, so the row stays
	capV, err := f.k.ValidatorCap(f.ctx)
	require.NoError(t, err)
	v := f.staking.validators[string(val(2))]
	v.Tokens = capV.Sub(nscn(50))
	f.staking.validators[string(val(2))] = v
	require.NoError(t, f.k.FillQueue(f.ctx))
	require.Equal(t, uint64(1), f.indexCount(t, d, val(2)))
	f.requireV10(t)

	// the rest fills completely: the row goes, the delegation holds the slot
	v.Tokens = capV.Sub(nscn(1000))
	f.staking.validators[string(val(2))] = v
	require.NoError(t, f.k.FillQueue(f.ctx))
	has, err := f.k.QueueByDelegator.Has(f.ctx, collections.Join(d.String(), val(2).String()))
	require.NoError(t, err)
	require.False(t, has, "a fully filled entry leaves no index row")
	requireSlots(val(2).String())
	f.requireV10(t)
}

func TestV10CatchesACorruptedIndex(t *testing.T) {
	f := newFixture(t)
	f.caps(t)
	_, err := f.k.Enqueue(f.ctx, acc(1), val(1), nscn(10))
	require.NoError(t, err)
	f.requireV10(t)

	key := collections.Join(acc(1).String(), val(1).String())
	require.NoError(t, f.k.QueueByDelegator.Set(f.ctx, key, 5))
	msg, broken := f.k.EscrowInvariant(f.ctx)
	require.True(t, broken)
	require.Contains(t, msg, "V-10 broken: the queue index")

	// a row with no entries behind it is caught too
	require.NoError(t, f.k.QueueByDelegator.Set(f.ctx, key, 1))
	require.NoError(t, f.k.QueueByDelegator.Set(f.ctx, collections.Join(acc(2).String(), val(1).String()), 1))
	msg, broken = f.k.EscrowInvariant(f.ctx)
	require.True(t, broken)
	require.Contains(t, msg, "V-10 broken: the queue index")
}

// Review fix 2: governance lowering max_validators_per_delegator must not
// truncate the slot list of a delegator that already holds more.
func TestSlotsSurviveALoweredParam(t *testing.T) {
	f := newFixture(t)
	d := acc(9)
	f.setParams(t, func(p *types.Params) { p.MaxValidatorsPerDelegator = 5 })
	f.bank.set(d, nscn(10_000))
	var held []string
	for i := byte(2); i <= 6; i++ {
		f.staking.addValidator(val(i), nscn(1000))
		f.delegate(t, d, val(i), nscn(200))
		held = append(held, val(i).String())
	}
	f.bank.set(d, nscn(10_000))
	sort.Strings(held)
	f.setParams(t, func(p *types.Params) { p.MaxValidatorsPerDelegator = 2 })

	slots, err := f.k.DelegatorSlots(f.ctx, d)
	require.NoError(t, err)
	require.Equal(t, held, slots)

	// a top-up to a held validator is not a new slot
	_, err = f.k.Enqueue(f.ctx, d, val(6), nscn(100))
	require.NoError(t, err)
	require.NoError(t, types.CheckNewSlot(slots, val(6).String(), 2))
	// and a really new one is refused
	f.staking.addValidator(val(7), nscn(1000))
	_, err = f.k.Enqueue(f.ctx, d, val(7), nscn(100))
	require.ErrorIs(t, err, types.ErrTooManyValidators)
}
