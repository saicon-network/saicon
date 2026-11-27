package keeper_test

import (
	"errors"
	"testing"

	"cosmossdk.io/math"
	sdk "github.com/cosmos/cosmos-sdk/types"
	distrtypes "github.com/cosmos/cosmos-sdk/x/distribution/types"
	stakingtypes "github.com/cosmos/cosmos-sdk/x/staking/types"
	"github.com/stretchr/testify/require"

	"github.com/saicon-network/saicon/chain/x/rewards/keeper"
	"github.com/saicon-network/saicon/chain/x/rewards/types"
	vrtypes "github.com/saicon-network/saicon/chain/x/validatorregistry/types"
)

// S10: the PoP share is paid THROUGH x/distribution (ADR-0011 F3). These tests
// pin x/rewards' side of the route with mocks: what leaves the module, where
// it goes and what gets booked. The split itself — commission to the
// operator, the rest to the delegators by stake — is x/distribution's
// arithmetic, and app/s10_delegator_rewards_test.go checks it on the real
// keepers with exact numbers.

// testEpochHeight is testEpoch's boundary at payoutFixture's 100-block epochs,
// for the tests that go through MaybeRunEpoch.
const testEpochHeight int64 = 700

// payoutFixture wires the given staking validators as all passing the three
// reward gates, with the S10 mocks around them.
func payoutFixture(t *testing.T, sk mockStakingKeeper) *emissionFixture {
	t.Helper()
	statuses := map[string]vrtypes.Status{}
	eps := map[string]math.Int{}
	for _, v := range sk.validators {
		statuses[v.GetOperator()] = vrtypes.Status_STATUS_ACTIVE
		eps[v.GetOperator()] = math.NewInt(5)
	}
	sk.totalBonded = testBonded
	return initEmissionFixture(t, sk,
		mockParticipationKeeper{ep: eps, epochLen: 100},
		mockVRKeeper{statuses: statuses},
		mockComplianceKeeper{},
		registerV1Params(),
	)
}

// stakingValidator is a real x/staking validator: bonded, with the given
// tokens and commission rate.
func stakingValidator(addr sdk.ValAddress, tokens math.Int, rate math.LegacyDec) stakingtypes.Validator {
	return stakingtypes.Validator{
		OperatorAddress: addr.String(),
		Status:          stakingtypes.Bonded,
		Tokens:          tokens,
		DelegatorShares: math.LegacyNewDecFromInt(tokens),
		Commission:      stakingtypes.NewCommission(rate, math.LegacyOneDec(), math.LegacyOneDec()),
	}
}

func eventsOfType(ctx sdk.Context, typ string) []map[string]string {
	var out []map[string]string
	for _, e := range ctx.EventManager().Events() {
		if e.Type != typ {
			continue
		}
		attrs := map[string]string{}
		for _, a := range e.Attributes {
			attrs[a.Key] = a.Value
		}
		out = append(out, attrs)
	}
	return out
}

// R1 + R4: the whole share leaves the rewards module for the x/distribution
// module account and is booked, unchanged, against the validator x/staking
// returns — the one carrying the 10% commission x/distribution will apply.
// Nothing is sent to the operator account: the bank interface no longer has an
// account-facing send at all.
func TestPayout_GoesThroughDistributionAgainstTheStakingValidator(t *testing.T) {
	va := valAddr(70)
	val := stakingValidator(va, math.NewInt(390_000), math.LegacyNewDecWithPrec(10, 2))
	f := payoutFixture(t, mockStakingKeeper{validators: []stakingtypes.ValidatorI{val}})
	f.bank.ledger = f.storeKey

	sdkCtx := sdk.UnwrapSDKContext(f.ctx)
	require.NoError(t, f.keeper.DistributeEpochRewards(sdkCtx, testEpoch))

	// One validator takes the whole epoch budget, 6e9 nscn.
	reward := sdk.NewCoins(sdk.NewCoin(types.RewardDenom, testPool))
	require.Len(t, f.bank.minted, 1)
	require.True(t, f.bank.minted[0].Equal(reward))

	require.Len(t, f.bank.sends, 1)
	require.Equal(t, sendRecord{from: types.ModuleName, to: distrtypes.ModuleName, amt: reward}, f.bank.sends[0])
	require.Equal(t, []byte(reward.String()), sdkCtx.KVStore(f.storeKey).Get(sendMarker(distrtypes.ModuleName)),
		"the transfer was written through to the block's store")

	require.Len(t, f.distr.allocs, 1)
	got := f.distr.allocs[0]
	require.Equal(t, va.String(), got.val.GetOperator())
	require.True(t, got.val.GetCommission().Equal(math.LegacyNewDecWithPrec(10, 2)),
		"the validator handed to x/distribution must carry its own commission rate, got %s", got.val.GetCommission())
	// R4: x/rewards books the share as it paid it. Outstanding rewards rise by
	// exactly 6e9, with no truncation on this side of the route.
	require.Equal(t, sdk.NewDecCoins(sdk.NewDecCoin(types.RewardDenom, testPool)), got.tokens)

	// R4: rewards module balance before minus after is what was paid. Before =
	// the mint, after = nothing left (one validator, no remainder).
	require.True(t, f.bank.moduleBalance(types.ModuleName).IsZero(),
		"rewards module keeps %s", f.bank.moduleBalance(types.ModuleName))
	require.True(t, f.bank.moduleBalance(distrtypes.ModuleName).Equal(testPool))
	carried, err := f.keeper.GetRemainder(f.ctx)
	require.NoError(t, err)
	require.True(t, carried.IsZero(), "got %s", carried)

	// The snapshot and the event are what they were before S10: the amount
	// routed to the validator, commission and delegator shares together.
	snap, found, err := f.keeper.GetRewardSnapshot(f.ctx, testEpoch, va)
	require.NoError(t, err)
	require.True(t, found)
	require.True(t, snap.Eligible)
	require.Equal(t, "eligible", snap.Reason)
	require.Equal(t, reward.String(), snap.Amount)

	minted := eventsOfType(sdkCtx, types.EventTypeRewardMinted)
	require.Len(t, minted, 1)
	require.Equal(t, map[string]string{
		types.AttributeKeyValidator: va.String(),
		types.AttributeKeyEpochID:   "7",
		types.AttributeKeyAmount:    reward.String(),
	}, minted[0])
}

// R3: a validator with zero tokens passes every gate but cannot be booked
// against — x/distribution would move its rewards to the community pool at the
// next period increment. Its share is not paid and is carried instead; the
// equal split itself does not change. It was not paid, so its snapshot is a
// denial like every other unpaid epoch, with reason "not allocatable".
func TestPayout_ZeroTokensIsNotAllocatableAndItsShareIsCarried(t *testing.T) {
	paid, empty := valAddr(71), valAddr(72)
	f := payoutFixture(t, mockStakingKeeper{validators: []stakingtypes.ValidatorI{
		fakeValidator{operator: paid.String()},
		fakeValidator{operator: empty.String(), tokens: math.ZeroInt()},
	}})

	sdkCtx := sdk.UnwrapSDKContext(f.ctx)
	require.NoError(t, f.keeper.DistributeEpochRewards(sdkCtx, testEpoch))

	// pool 6e9 over two eligible validators: share 3e9 each. One is paid.
	share := testPool.QuoRaw(2)
	require.Len(t, f.bank.minted, 1)
	require.True(t, f.bank.minted[0].AmountOf(types.RewardDenom).Equal(testPool))
	require.Len(t, f.bank.sends, 1)
	require.True(t, f.bank.sends[0].amt.AmountOf(types.RewardDenom).Equal(share))
	require.Len(t, f.distr.allocs, 1)
	require.Equal(t, paid.String(), f.distr.allocs[0].val.GetOperator())

	// remainder = pool − share·(number paid) = 6e9 − 3e9·1 = 3e9, and it sits
	// in the module account.
	carried, err := f.keeper.GetRemainder(f.ctx)
	require.NoError(t, err)
	require.True(t, carried.Equal(share), "got %s", carried)
	require.True(t, f.bank.moduleBalance(types.ModuleName).Equal(carried))

	snap, found, err := f.keeper.GetRewardSnapshot(f.ctx, testEpoch, empty)
	require.NoError(t, err)
	require.True(t, found)
	require.False(t, snap.Eligible, "an unpaid epoch is recorded as a denial")
	require.Empty(t, snap.Amount)
	require.Equal(t, "not allocatable", snap.Reason)

	denied := eventsOfType(sdkCtx, types.EventTypeRewardDenied)
	require.Len(t, denied, 1)
	require.Equal(t, empty.String(), denied[0][types.AttributeKeyValidator])
	require.Equal(t, "not allocatable", denied[0][types.AttributeKeyReason])
	minted := eventsOfType(sdkCtx, types.EventTypeRewardMinted)
	require.Len(t, minted, 1)
	require.Equal(t, paid.String(), minted[0][types.AttributeKeyValidator])

	// V-2 only looks at paid snapshots; the unpaid one is not a breach.
	msg, broken := f.keeper.RewardGateInvariant(f.ctx, testEpoch)
	require.False(t, broken, msg)

	// The carried share offsets the next epoch's issuance like any other carry:
	// mint = 6e9 − 3e9 = 3e9, the pool is 6e9 again, and with the validator
	// still empty 3e9 is carried again. Conservation holds across both epochs.
	require.NoError(t, f.keeper.DistributeEpochRewards(f.ctx, testEpoch+1))
	require.Len(t, f.bank.minted, 2)
	require.True(t, f.bank.minted[1].AmountOf(types.RewardDenom).Equal(testPool.Sub(share)),
		"got %s", f.bank.minted[1])

	mintedTotal, paidTotal := math.ZeroInt(), math.ZeroInt()
	for _, m := range f.bank.minted {
		mintedTotal = mintedTotal.Add(m.AmountOf(types.RewardDenom))
	}
	for _, s := range f.bank.sends {
		paidTotal = paidTotal.Add(s.amt.AmountOf(types.RewardDenom))
	}
	carried, err = f.keeper.GetRemainder(f.ctx)
	require.NoError(t, err)
	require.True(t, mintedTotal.Equal(paidTotal.Add(carried)),
		"minted %s must equal paid %s + carried %s", mintedTotal, paidTotal, carried)
	require.True(t, f.bank.moduleBalance(types.ModuleName).Equal(carried))
}

// R3: a validator x/staking no longer finds is not allocatable either.
func TestPayout_MissingValidatorIsNotAllocatable(t *testing.T) {
	paid, gone := valAddr(73), valAddr(74)
	f := payoutFixture(t, mockStakingKeeper{
		validators: []stakingtypes.ValidatorI{
			fakeValidator{operator: paid.String()},
			fakeValidator{operator: gone.String()},
		},
		gone: map[string]bool{gone.String(): true},
	})

	require.NoError(t, f.keeper.DistributeEpochRewards(f.ctx, testEpoch))

	require.Len(t, f.distr.allocs, 1)
	require.Equal(t, paid.String(), f.distr.allocs[0].val.GetOperator())

	snap, found, err := f.keeper.GetRewardSnapshot(f.ctx, testEpoch, gone)
	require.NoError(t, err)
	require.True(t, found)
	require.False(t, snap.Eligible)
	require.Empty(t, snap.Amount)
	require.Equal(t, "not allocatable", snap.Reason)

	carried, err := f.keeper.GetRemainder(f.ctx)
	require.NoError(t, err)
	require.True(t, carried.Equal(testPool.QuoRaw(2)), "got %s", carried)
}

// R3 names exactly two conditions. Any other staking read error aborts the
// epoch, as every other error on the money path does.
func TestPayout_StakingReadErrorAborts(t *testing.T) {
	va := valAddr(75)
	f := payoutFixture(t, mockStakingKeeper{
		validators: []stakingtypes.ValidatorI{fakeValidator{operator: va.String()}},
		getErr:     errors.New("staking store boom"),
	})

	err := f.keeper.DistributeEpochRewards(f.ctx, testEpoch)
	require.ErrorContains(t, err, "staking store boom")
	require.Empty(t, f.bank.sends)
	require.Empty(t, f.distr.allocs)
}

// R3 reads back as an unpaid epoch. RewardSummary counts the record but not a
// payment, and adds nothing to the total; an eligible snapshot with an empty
// amount would instead read as a paid epoch whose money went missing and fail
// the query as store corruption.
func TestPayout_NotAllocatableEpochReadsAsUnpaidInTheSummary(t *testing.T) {
	paid, empty := valAddr(77), valAddr(78)
	f := payoutFixture(t, mockStakingKeeper{validators: []stakingtypes.ValidatorI{
		fakeValidator{operator: paid.String()},
		fakeValidator{operator: empty.String(), tokens: math.ZeroInt()},
	}})
	require.NoError(t, f.keeper.DistributeEpochRewards(f.ctx, testEpoch))

	q := keeper.NewQueryServerImpl(f.keeper)
	res, err := q.RewardSummary(f.ctx, &types.QueryRewardSummaryRequest{
		ValidatorAddress: empty.String(), FromEpoch: testEpoch, ToEpoch: testEpoch,
	})
	require.NoError(t, err)
	require.Equal(t, uint64(1), res.EpochsWithRecord)
	require.Zero(t, res.EpochsPaid)
	require.True(t, res.TotalAmount.IsZero(), "got %s", res.TotalAmount)

	// The paid validator's summary is the share, 6e9 / 2.
	res, err = q.RewardSummary(f.ctx, &types.QueryRewardSummaryRequest{
		ValidatorAddress: paid.String(), FromEpoch: testEpoch, ToEpoch: testEpoch,
	})
	require.NoError(t, err)
	require.Equal(t, uint64(1), res.EpochsPaid)
	require.True(t, res.TotalAmount.Equal(testPool.QuoRaw(2)), "got %s", res.TotalAmount)
}

// The transfer and the booking land together or not at all. If the booking
// fails after the transfer, x/distribution must not be left holding coins no
// outstanding reward accounts for: the error aborts the epoch, and
// MaybeRunEpoch discards the epoch's cache context with the transfer in it.
func TestPayout_AFailedBookingLeavesNoTransferBehind(t *testing.T) {
	va := valAddr(76)
	f := payoutFixture(t, mockStakingKeeper{validators: []stakingtypes.ValidatorI{fakeValidator{operator: va.String()}}})
	f.bank.ledger = f.storeKey
	f.distr.allocErr = errors.New("allocation boom")

	sdkCtx := sdk.UnwrapSDKContext(f.ctx)
	require.NoError(t, f.keeper.MaybeRunEpoch(sdkCtx, testEpochHeight), "an emission error never halts the chain")

	// The mock bank saw the send (its slices are not store-backed), but the
	// store write it made inside the epoch's cache context was never written.
	require.Len(t, f.bank.sends, 1)
	require.Nil(t, sdkCtx.KVStore(f.storeKey).Get(sendMarker(distrtypes.ModuleName)),
		"a transfer without its booking must not reach the block's store")

	_, found, err := f.keeper.GetRewardSnapshot(f.ctx, testEpoch, va)
	require.NoError(t, err)
	require.False(t, found)
}

// An epoch that fails halfway through the payout loop is discarded whole. The
// first validator is paid, then the staking read for the second fails: by then
// the epoch has minted, consumed the observed gas fees, sent and booked one
// share and written its snapshot. None of it may survive. Before the
// epoch-level cache context, the mint stayed in the module account where the
// carry did not see it, and the fee offset was spent on an epoch that paid
// nothing.
//
// The mock bank's balances and supply are Go values and cannot be rolled
// back, so this test reads x/rewards' own store and the bank's ledger markers;
// app/s10_delegator_rewards_test.go checks supply and module balances on the
// real keepers.
func TestMaybeRunEpoch_AnAbortMidLoopLeavesNoTrace(t *testing.T) {
	first, second := valAddr(79), valAddr(80)
	f := payoutFixture(t, mockStakingKeeper{
		validators: []stakingtypes.ValidatorI{
			fakeValidator{operator: first.String()},
			fakeValidator{operator: second.String()},
		},
		getErr:    errors.New("staking store boom"),
		getErrFor: second.String(),
	})
	f.bank.ledger = f.storeKey
	sdkCtx := sdk.UnwrapSDKContext(f.ctx)

	carry, fees := math.NewInt(1_000), math.NewInt(2_000)
	require.NoError(t, f.keeper.SetRemainder(f.ctx, carry))
	f.bank.credit(types.ModuleName, carry) // a carry is value the module account already holds
	require.NoError(t, f.keeper.AddObservedGasFees(f.ctx, fees))
	eventsBefore := len(sdkCtx.EventManager().Events())

	require.NoError(t, f.keeper.MaybeRunEpoch(sdkCtx, testEpochHeight), "an emission error never halts the chain")

	// The epoch did get that far: one mint, one share sent and booked.
	require.Len(t, f.bank.minted, 1)
	require.Len(t, f.bank.sends, 1)
	require.Len(t, f.distr.allocs, 1)
	require.Equal(t, first.String(), f.distr.allocs[0].val.GetOperator())

	// And none of it reached the block's store.
	store := sdkCtx.KVStore(f.storeKey)
	require.Nil(t, store.Get(mintMarker(types.ModuleName)), "the mint must be discarded")
	require.Nil(t, store.Get(sendMarker(distrtypes.ModuleName)), "the first share's transfer must be discarded")
	gotCarry, err := f.keeper.GetRemainder(f.ctx)
	require.NoError(t, err)
	require.True(t, gotCarry.Equal(carry), "the carry is untouched, got %s", gotCarry)
	gotFees, err := f.keeper.GetObservedGasFees(f.ctx)
	require.NoError(t, err)
	require.True(t, gotFees.Equal(fees), "the observed fees are not consumed, got %s", gotFees)
	for _, v := range []sdk.ValAddress{first, second} {
		_, found, err := f.keeper.GetRewardSnapshot(f.ctx, testEpoch, v)
		require.NoError(t, err)
		require.False(t, found, "no snapshot for %s", v)
	}
	require.Len(t, sdkCtx.EventManager().Events(), eventsBefore, "a discarded epoch emits nothing")
}
