package keeper_test

import (
	"testing"

	"cosmossdk.io/math"
	"github.com/stretchr/testify/require"

	"github.com/saicon-network/saicon/chain/x/stakeengine/types"
)

// caps computes the genesis snapshot, as InitGenesis will.
func (f *fixture) caps(t *testing.T) types.CapState {
	t.Helper()
	st, err := f.k.RecomputeCaps(f.ctx, 0, true)
	require.NoError(t, err)
	return st
}

func (f *fixture) setParams(t *testing.T, edit func(*types.Params)) {
	t.Helper()
	p, err := f.k.Params.Get(f.ctx)
	require.NoError(t, err)
	edit(&p)
	require.NoError(t, f.k.Params.Set(f.ctx, p))
}

func (f *fixture) devnetFloor(t *testing.T) {
	f.setParams(t, func(p *types.Params) { p.ValidatorCapFloor = nscn(2_000_000) })
}

func (f *fixture) requireV10(t *testing.T) {
	t.Helper()
	msg, broken := f.k.EscrowInvariant(f.ctx)
	require.False(t, broken, msg)
}

func (f *fixture) entry(t *testing.T, id uint64) (types.QueueEntry, bool) {
	t.Helper()
	e, err := f.k.Queue.Get(f.ctx, id)
	if err != nil {
		return types.QueueEntry{}, false
	}
	return e, true
}

func (f *fixture) tokens(t *testing.T, b byte) math.Int {
	t.Helper()
	tok, err := f.k.ValidatorTokens(f.ctx, val(b))
	require.NoError(t, err)
	return tok
}

func TestEnqueue_EscrowsTheCoinsAndRecordsTheEntry(t *testing.T) {
	f := newFixture(t)
	f.caps(t)
	f.ctx = f.ctx.WithBlockHeight(7)

	id, err := f.k.Enqueue(f.ctx, acc(1), val(1), nscn(500))
	require.NoError(t, err)
	require.Equal(t, uint64(0), id)

	e, ok := f.entry(t, id)
	require.True(t, ok)
	require.Equal(t, acc(1).String(), e.Delegator)
	require.Equal(t, val(1).String(), e.Validator)
	require.Equal(t, nscn(500), e.Amount)
	require.Equal(t, int64(7), e.EnqueuedHeight)

	require.Equal(t, nscn(500), f.bank.bal(f.k.EscrowAddress()))
	require.Equal(t, nscn(9_000_000-500), f.bank.bal(acc(1)))
	n, err := f.k.QueueLen.Get(f.ctx)
	require.NoError(t, err)
	require.Equal(t, uint64(1), n)
	f.requireV10(t)

	id2, err := f.k.Enqueue(f.ctx, acc(1), val(1), nscn(1))
	require.NoError(t, err)
	require.Equal(t, uint64(1), id2, "ids are a monotonic sequence")
}

func TestEnqueue_Rejections(t *testing.T) {
	f := newFixture(t)
	f.caps(t)

	_, err := f.k.Enqueue(f.ctx, acc(1), val(1), nscn(1).SubRaw(1))
	require.ErrorIs(t, err, types.ErrQueueEntryTooSmall)

	_, err = f.k.Enqueue(f.ctx, acc(1), val(42), nscn(10))
	require.Error(t, err, "an unknown validator")

	_, err = f.k.Enqueue(f.ctx, acc(5), val(1), nscn(10))
	require.Error(t, err, "a delegator without the coins")

	f.setParams(t, func(p *types.Params) { p.MaxQueueEntries = 1 })
	_, err = f.k.Enqueue(f.ctx, acc(1), val(1), nscn(10))
	require.NoError(t, err)
	_, err = f.k.Enqueue(f.ctx, acc(1), val(1), nscn(10))
	require.ErrorIs(t, err, types.ErrQueueFull)

	f.requireV10(t)
}

// The fresh-devnet story: 1.68M cap, 1M bonded, 680k of headroom. A 700k entry
// fills 680k at the end of the block; cancelling returns exactly the 20k left.
func TestFillThenCancel_ReturnsExactlyTheRemainder(t *testing.T) {
	f := newFixture(t)
	f.devnetFloor(t)
	f.caps(t)

	id, err := f.k.Enqueue(f.ctx, acc(1), val(1), nscn(700_000))
	require.NoError(t, err)
	require.NoError(t, f.k.FillQueue(f.ctx))

	e, ok := f.entry(t, id)
	require.True(t, ok)
	require.Equal(t, nscn(20_000), e.Amount)
	require.Equal(t, nscn(1_680_000), f.staking.bonded)
	require.Equal(t, nscn(20_000), f.bank.bal(f.k.EscrowAddress()))
	h, err := f.k.Headroom(f.ctx)
	require.NoError(t, err)
	require.True(t, h.IsZero())
	f.requireV10(t)

	returned, err := f.k.Cancel(f.ctx, acc(1), id)
	require.NoError(t, err)
	require.Equal(t, nscn(20_000), returned)
	_, ok = f.entry(t, id)
	require.False(t, ok)
	require.True(t, f.bank.bal(f.k.EscrowAddress()).IsZero())
	require.Equal(t, nscn(9_000_000-680_000), f.bank.bal(acc(1)))
	n, err := f.k.QueueLen.Get(f.ctx)
	require.NoError(t, err)
	require.Equal(t, uint64(0), n)
	f.requireV10(t)
}

func TestCancel_Rejections(t *testing.T) {
	f := newFixture(t)
	f.caps(t)
	id, err := f.k.Enqueue(f.ctx, acc(1), val(1), nscn(10))
	require.NoError(t, err)

	_, err = f.k.Cancel(f.ctx, acc(1), id+1)
	require.ErrorIs(t, err, types.ErrQueueEntryNotFound)
	_, err = f.k.Cancel(f.ctx, acc(2), id)
	require.ErrorIs(t, err, types.ErrNotQueueOwner)
	f.requireV10(t)
}

// Register Q6: the queue fills by validator quality, not by arrival.
func TestFillQueue_HigherQualityValidatorFirst(t *testing.T) {
	f := newFixture(t)
	f.devnetFloor(t)
	f.staking.addValidator(val(2), nscn(100_000)) // bonded 1.1M → headroom 580k
	f.caps(t)
	f.part.scores[string(val(1))] = math.LegacyNewDecWithPrec(5, 1)
	f.part.scores[string(val(2))] = math.LegacyNewDecWithPrec(9, 1)
	require.NoError(t, f.bank.move(acc(1), acc(3), nscn(1_000_000)))
	require.NoError(t, f.bank.move(acc(1), acc(4), nscn(1_000_000)))

	low, err := f.k.Enqueue(f.ctx, acc(3), val(1), nscn(400_000)) // earlier, lower quality
	require.NoError(t, err)
	high, err := f.k.Enqueue(f.ctx, acc(4), val(2), nscn(400_000))
	require.NoError(t, err)
	require.NoError(t, f.k.FillQueue(f.ctx))

	_, ok := f.entry(t, high)
	require.False(t, ok, "the higher-quality entry is filled in full")
	e, ok := f.entry(t, low)
	require.True(t, ok)
	require.Equal(t, nscn(220_000), e.Amount, "the rest of the headroom, 180k, goes to the next")
	require.Equal(t, nscn(500_000), f.tokens(t, 2))
	require.Equal(t, nscn(1_180_000), f.tokens(t, 1))
	f.requireV10(t)
}

func TestFillQueue_SkipsInactiveAndFullValidatorsAndKeepsTheirEntries(t *testing.T) {
	f := newFixture(t) // floor 0: the strict 2% rule
	f.staking.addValidator(val(2), nscn(10_000))
	f.staking.addValidator(val(3), nscn(10_000))
	st := f.caps(t) // bonded 1.02M → validator cap 20,400
	require.Equal(t, nscn(20_400), st.ValidatorCap)
	f.reg.inactive[string(val(2))] = true

	full, err := f.k.Enqueue(f.ctx, acc(1), val(1), nscn(1_000)) // val(1) holds 1M, far over 20,400
	require.NoError(t, err)
	inactive, err := f.k.Enqueue(f.ctx, acc(1), val(2), nscn(1_000))
	require.NoError(t, err)
	open, err := f.k.Enqueue(f.ctx, acc(1), val(3), nscn(50_000))
	require.NoError(t, err)
	require.NoError(t, f.k.FillQueue(f.ctx))

	e, ok := f.entry(t, full)
	require.True(t, ok)
	require.Equal(t, nscn(1_000), e.Amount)
	e, ok = f.entry(t, inactive)
	require.True(t, ok)
	require.Equal(t, nscn(1_000), e.Amount)
	e, ok = f.entry(t, open)
	require.True(t, ok)
	require.Equal(t, nscn(39_600), e.Amount, "filled only up to val(3)'s room, 10,400")
	require.Equal(t, nscn(20_400), f.tokens(t, 3))
	f.requireV10(t)
}

func TestFillQueue_AtMostMaxFillsPerBlock(t *testing.T) {
	f := newFixture(t)
	f.devnetFloor(t)
	f.setParams(t, func(p *types.Params) { p.MaxFillsPerBlock = 2 })
	f.caps(t)
	for i := 0; i < 3; i++ {
		_, err := f.k.Enqueue(f.ctx, acc(1), val(1), nscn(1))
		require.NoError(t, err)
	}
	require.NoError(t, f.k.FillQueue(f.ctx))
	_, ok := f.entry(t, 0)
	require.False(t, ok)
	_, ok = f.entry(t, 1)
	require.False(t, ok)
	_, ok = f.entry(t, 2)
	require.True(t, ok, "the third waits for the next block")
	f.requireV10(t)
}

func TestFillQueue_NothingWhenTheCapIsReached(t *testing.T) {
	f := newFixture(t)
	f.devnetFloor(t)
	f.bank.supply = nscn(1_000_000) // global cap 600k, below the 1M bonded
	f.caps(t)
	id, err := f.k.Enqueue(f.ctx, acc(1), val(1), nscn(10))
	require.NoError(t, err)
	require.NoError(t, f.k.FillQueue(f.ctx))
	e, ok := f.entry(t, id)
	require.True(t, ok)
	require.Equal(t, nscn(10), e.Amount)
	require.Equal(t, nscn(1_000_000), f.staking.bonded)
	f.requireV10(t)
}

// A fill that fails does not halt the chain and does not consume the entry.
func TestFillQueue_AFailedFillLeavesTheEntry(t *testing.T) {
	f := newFixture(t)
	f.devnetFloor(t)
	f.caps(t)
	id, err := f.k.Enqueue(f.ctx, acc(1), val(1), nscn(10))
	require.NoError(t, err)
	f.bank.failSend = true
	require.NoError(t, f.k.FillQueue(f.ctx))
	e, ok := f.entry(t, id)
	require.True(t, ok)
	require.Equal(t, nscn(10), e.Amount)
	require.Equal(t, nscn(1_000_000), f.staking.bonded)
	f.requireV10(t)
}

func TestEscrowInvariant_DetectsAnyDrift(t *testing.T) {
	f := newFixture(t)
	f.caps(t)
	_, err := f.k.Enqueue(f.ctx, acc(1), val(1), nscn(10))
	require.NoError(t, err)
	f.requireV10(t)

	f.bank.set(f.k.EscrowAddress(), nscn(10).AddRaw(1))
	msg, broken := f.k.EscrowInvariant(f.ctx)
	require.True(t, broken)
	require.Contains(t, msg, "V-10")
}

func TestEndBlock_FillsAndHaltsOnABrokenV10(t *testing.T) {
	f := newFixture(t)
	f.devnetFloor(t)
	f.caps(t)
	id, err := f.k.Enqueue(f.ctx, acc(1), val(1), nscn(10))
	require.NoError(t, err)
	require.NoError(t, f.k.EndBlock(f.ctx))
	_, ok := f.entry(t, id)
	require.False(t, ok, "an entry that fits is filled at the end of the same block")

	f.bank.set(f.k.EscrowAddress(), math.OneInt())
	require.Panics(t, func() { _ = f.k.EndBlock(f.ctx) })
}

// The escrow send succeeds, then Delegate fails: the cache context must take the
// send back with it, or the coins would be gone from the escrow with the entry
// still claiming them and V-10 would halt the chain.
func TestFillQueue_AFailedDelegateRevertsTheEscrowSend(t *testing.T) {
	f := newFixture(t)
	f.devnetFloor(t)
	f.caps(t)
	id, err := f.k.Enqueue(f.ctx, acc(1), val(1), nscn(10))
	require.NoError(t, err)
	escrow, delegator, bonded := f.bank.bal(f.k.EscrowAddress()), f.bank.bal(acc(1)), f.staking.bonded

	f.staking.failDelegate = true
	require.NoError(t, f.k.FillQueue(f.ctx))

	e, ok := f.entry(t, id)
	require.True(t, ok)
	require.Equal(t, nscn(10), e.Amount)
	require.Equal(t, escrow, f.bank.bal(f.k.EscrowAddress()))
	require.Equal(t, delegator, f.bank.bal(acc(1)))
	require.Equal(t, bonded, f.staking.bonded)
	f.requireV10(t)
}
