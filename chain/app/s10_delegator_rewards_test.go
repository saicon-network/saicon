package app

import (
	"encoding/json"
	"fmt"
	"testing"

	"cosmossdk.io/math"
	"github.com/cosmos/cosmos-sdk/codec"
	sdk "github.com/cosmos/cosmos-sdk/types"
	authtypes "github.com/cosmos/cosmos-sdk/x/auth/types"
	distrkeeper "github.com/cosmos/cosmos-sdk/x/distribution/keeper"
	distrtypes "github.com/cosmos/cosmos-sdk/x/distribution/types"
	stakingtypes "github.com/cosmos/cosmos-sdk/x/staking/types"
	"github.com/stretchr/testify/require"

	rewardstypes "github.com/saicon-network/saicon/chain/x/rewards/types"
	stakeenginetypes "github.com/saicon-network/saicon/chain/x/stakeengine/types"
)

// x/rewards takes x/distribution's keeper by concrete type under depinject;
// this turns a drifted DistributionKeeper interface into a compile error.
var _ rewardstypes.DistributionKeeper = distrkeeper.Keeper{}

// withPayoutNumbers makes the S10 payout test's arithmetic exact: validator A
// charges a 10% commission, and the security budget is a clean 3% of bonded
// stake (floor 0, ceiling 1, so the clip never binds) over 5,000 blocks a
// year — 1,000 five-block epochs. Use it with withFastRounds.
func withPayoutNumbers() func(cdc codec.Codec, gen map[string]json.RawMessage) {
	return func(cdc codec.Codec, gen map[string]json.RawMessage) {
		var stGen stakingtypes.GenesisState
		cdc.MustUnmarshalJSON(gen[stakingtypes.ModuleName], &stGen)
		stGen.Validators[0].Commission = stakingtypes.NewCommission(
			math.LegacyNewDecWithPrec(10, 2), math.LegacyOneDec(), math.LegacyOneDec())
		gen[stakingtypes.ModuleName] = cdc.MustMarshalJSON(&stGen)

		var rGen rewardstypes.GenesisState
		cdc.MustUnmarshalJSON(gen[rewardstypes.ModuleName], &rGen)
		rGen.Params.BudgetBondedRate = math.LegacyNewDecWithPrec(3, 2)
		rGen.Params.BudgetFloorRate = math.LegacyZeroDec()
		rGen.Params.BudgetCeilingRate = math.LegacyOneDec()
		rGen.Params.BlocksPerYear = 5_000
		gen[rewardstypes.ModuleName] = cdc.MustMarshalJSON(&rGen)
	}
}

func decNscn(s string) sdk.DecCoins {
	return sdk.NewDecCoins(sdk.NewDecCoinFromDec(stakeenginetypes.BondDenom, math.LegacyMustNewDecFromStr(s)))
}

func (c *testChain) distrBalance() math.Int {
	c.t.Helper()
	return c.balance(authtypes.NewModuleAddress(distrtypes.ModuleName))
}

// distributionInvariants checks x/distribution's invariants at ctx. SDK v0.53
// deprecates x/crisis, and x/distribution no longer ships the invariant
// functions it used to register with it, so all four are restated here from
// their v0.50 definitions:
//
//   - module account: the distribution module account holds exactly
//     truncate(Σ validator outstanding rewards + community pool);
//   - non-negative outstanding: no validator's outstanding rewards are negative;
//   - reference count: the historical rewards' reference counts add up to one
//     per validator, one per delegation and one per slash event;
//   - can withdraw: every commission and every delegation's rewards can be
//     withdrawn without error, and afterwards no validator's outstanding
//     rewards are negative. Run in a discarded cache context.
func (c *testChain) distributionInvariants(ctx sdk.Context) (string, bool) {
	dk := c.app.DistrKeeper

	var (
		outstanding sdk.DecCoins
		msg         string
	)
	dk.IterateValidatorOutstandingRewards(ctx, func(val sdk.ValAddress, r distrtypes.ValidatorOutstandingRewards) bool {
		if r.Rewards.IsAnyNegative() {
			msg = fmt.Sprintf("non-negative outstanding: validator %s has %s", val, r.Rewards)
			return true
		}
		outstanding = outstanding.Add(r.Rewards...)
		return false
	})
	if msg != "" {
		return msg, true
	}
	feePool, err := dk.FeePool.Get(ctx)
	if err != nil {
		return fmt.Sprintf("module account: cannot read the fee pool: %v", err), true
	}
	want, _ := outstanding.Add(feePool.CommunityPool...).TruncateDecimal()
	have := c.app.BankKeeper.GetAllBalances(ctx, dk.GetDistributionAccount(ctx).GetAddress())
	if !have.Equal(want) {
		return fmt.Sprintf("module account: holds %s, outstanding + community pool is %s", have, want), true
	}

	var vals uint64
	if err := c.app.StakingKeeper.IterateValidators(ctx, func(int64, stakingtypes.ValidatorI) bool {
		vals++
		return false
	}); err != nil {
		return fmt.Sprintf("reference count: cannot iterate the validators: %v", err), true
	}
	allDels, err := c.app.StakingKeeper.GetAllDelegations(ctx)
	if err != nil {
		return fmt.Sprintf("reference count: cannot read the delegations: %v", err), true
	}
	var slashes uint64
	dk.IterateValidatorSlashEvents(ctx, func(sdk.ValAddress, uint64, distrtypes.ValidatorSlashEvent) bool {
		slashes++
		return false
	})
	expected := vals + uint64(len(allDels)) + slashes
	if stored := dk.GetValidatorHistoricalReferenceCount(ctx); stored != expected {
		return fmt.Sprintf("reference count: stored %d, expected %d = %d validators + %d delegations + %d slashes",
			stored, expected, vals, len(allDels), slashes), true
	}

	cache, _ := ctx.CacheContext()
	dels, err := c.app.StakingKeeper.GetAllDelegations(cache)
	if err != nil {
		return fmt.Sprintf("can withdraw: cannot read the delegations: %v", err), true
	}
	byVal := map[string][]sdk.AccAddress{}
	for _, d := range dels {
		byVal[d.ValidatorAddress] = append(byVal[d.ValidatorAddress], sdk.MustAccAddressFromBech32(d.DelegatorAddress))
	}
	if err := c.app.StakingKeeper.IterateValidators(cache, func(_ int64, val stakingtypes.ValidatorI) bool {
		valAddr, err := sdk.ValAddressFromBech32(val.GetOperator())
		if err != nil {
			msg = fmt.Sprintf("can withdraw: %v", err)
			return true
		}
		// A validator with no commission answers an error here; v0.50 ignored
		// it the same way.
		_, _ = dk.WithdrawValidatorCommission(cache, valAddr)
		for _, d := range byVal[val.GetOperator()] {
			if _, err := dk.WithdrawDelegationRewards(cache, d, valAddr); err != nil {
				msg = fmt.Sprintf("can withdraw: delegation %s -> %s: %v", d, valAddr, err)
				return true
			}
		}
		remaining, err := dk.GetValidatorOutstandingRewardsCoins(cache, valAddr)
		if err != nil || remaining.IsAnyNegative() {
			msg = fmt.Sprintf("can withdraw: validator %s outstanding after withdrawing all: %s (%v)", valAddr, remaining, err)
			return true
		}
		return false
	}); err != nil {
		return fmt.Sprintf("can withdraw: cannot iterate the validators: %v", err), true
	}
	return msg, msg != ""
}

func (c *testChain) requireDistributionInvariants() {
	c.t.Helper()
	msg, broken := c.distributionInvariants(c.ctx())
	require.False(c.t, broken, msg)
}

// The restated invariants are not vacuous: a coin that reaches the
// distribution module account without being booked — exactly what a transfer
// without its AllocateTokensToValidator would leave — breaks the module
// account check.
func TestDistributionInvariantsCatchUnbookedCoins(t *testing.T) {
	c := newTestChain(t)
	c.requireDistributionInvariants()

	ctx := c.ctx()
	cache, _ := ctx.CacheContext()
	one := sdk.NewCoins(sdk.NewCoin(stakeenginetypes.BondDenom, math.OneInt()))
	require.NoError(t, c.app.BankKeeper.SendCoinsFromAccountToModule(cache, c.delegators[0].addr, distrtypes.ModuleName, one))
	msg, broken := c.distributionInvariants(cache)
	require.True(t, broken)
	require.Contains(t, msg, "module account")

	// One reference too many on a historical rewards record breaks the
	// reference count.
	cache, _ = ctx.CacheContext()
	dk := c.app.DistrKeeper
	bumped := false
	dk.IterateValidatorHistoricalRewards(cache, func(val sdk.ValAddress, period uint64, r distrtypes.ValidatorHistoricalRewards) bool {
		r.ReferenceCount++
		require.NoError(t, dk.SetValidatorHistoricalRewards(cache, val, period, r))
		bumped = true
		return true
	})
	require.True(t, bumped, "genesis validators have historical rewards")
	msg, broken = c.distributionInvariants(cache)
	require.True(t, broken)
	require.Contains(t, msg, "reference count")
}

// S10: the PoP share reaches a validator's delegators and its operator's
// commission, and a wallet's claim pays it.
//
// Every figure below was computed outside Go (Python decimal, truncating to 18
// places the way LegacyDec does):
//
//	genesis supply  6,220,000 SCN = 622,000,000,000,000 nscn
//	bonded at the epoch: A 300,000 + 60,000 (delegator 0) + 30,000
//	                     (delegator 1) = 390,000; B and C 300,000 each
//	                     -> 990,000 SCN = 99,000,000,000,000 nscn
//	annual budget   0.03 x 99e12 = 2,970,000,000,000 (floor 0, ceiling 1)
//	epoch pool      2.97e12 / (5,000 / 5) = 2,970,000,000 nscn, all minted
//	share           2.97e9 / 3 eligible = 990,000,000 nscn each, remainder 0
//
// A's share through x/distribution, commission 10%:
//
//	commission      99,000,000
//	delegators      891,000,000, by stake over 39,000,000,000,000 nscn
//	ratio           891e6 / 39e12 = 0.000022846153846153 (truncated)
//	delegator 0     6e12 x ratio = 137,076,923.076918  -> withdraws 137,076,923
//	delegator 1     3e12 x ratio =  68,538,461.538459  -> withdraws  68,538,461
//	A self-bond     3e13 x ratio = 685,384,615.38459   -> withdraws 685,384,615
//	total withdrawn 989,999,999: 1 nscn of truncation dust stays in
//	                x/distribution, 0.999967 in the community pool and
//	                0.000033 in A's outstanding rewards
//
// The test runs epoch 1 (height 5) and withdraws in heights 6 to 9, before
// epoch 2 at height 10 pays again.
func TestPoPRewardReachesDelegatorsAndTheOperator(t *testing.T) {
	c := newTestChain(t, withFastRounds(), withPayoutNumbers())
	a := c.vals[0]
	d0, d1 := c.delegators[0], c.delegators[1]

	genesisSupply := c.supply()
	require.Equal(t, math.NewInt(622_000_000_000_000), genesisSupply)

	c.deliverOK(d0, msgDelegate(d0.addr.String(), a.val.String(), scnInt(60_000)))
	c.deliverOK(d1, msgDelegate(d1.addr.String(), a.val.String(), scnInt(30_000)))
	c.runTo(4)

	operatorBefore := c.balance(a.addr)
	distrBefore := c.distrBalance()
	require.True(t, distrBefore.IsZero(), "a zero-fee chain has nothing in x/distribution yet, got %s", distrBefore)
	require.Equal(t, genesisSupply, c.supply(), "nothing is minted before the first epoch")

	c.runTo(5) // epoch 1
	ctx := c.ctx()

	// --- x/rewards side: same decision, new route -------------------------
	share := math.NewInt(990_000_000)
	reward := sdk.NewCoins(sdk.NewCoin(stakeenginetypes.BondDenom, share))
	for i, v := range c.vals {
		snap, found, err := c.app.RewardsKeeper.GetRewardSnapshot(ctx, 1, v.val)
		require.NoError(t, err)
		require.True(t, found, "validator %d", i)
		require.True(t, snap.Eligible, "validator %d: %s", i, snap.Reason)
		require.Equal(t, "eligible", snap.Reason)
		require.Equal(t, reward.String(), snap.Amount, "validator %d", i)
	}
	remainder, err := c.app.RewardsKeeper.GetRemainder(ctx)
	require.NoError(t, err)
	require.True(t, remainder.IsZero(), "got %s", remainder)
	require.True(t, c.balance(authtypes.NewModuleAddress(rewardstypes.ModuleName)).IsZero(),
		"everything minted left the rewards module")

	// The operator account did not receive the share: before S10 it gained
	// 990,000,000 here.
	require.Equal(t, operatorBefore, c.balance(a.addr), "the PoP share must not land in the operator account")

	// Total supply moved by exactly the mint.
	minted := math.NewInt(2_970_000_000)
	require.Equal(t, genesisSupply.Add(minted), c.supply())

	// --- x/distribution side ----------------------------------------------
	require.Equal(t, minted, c.distrBalance(), "the three shares sit in the distribution module account")

	dk := c.app.DistrKeeper
	out, err := dk.GetValidatorOutstandingRewardsCoins(ctx, a.val)
	require.NoError(t, err)
	require.Equal(t, decNscn("990000000"), out, "outstanding rises by exactly the share")
	comm, err := dk.GetValidatorAccumulatedCommission(ctx, a.val)
	require.NoError(t, err)
	require.Equal(t, decNscn("99000000"), comm.Commission)
	cur, err := dk.GetValidatorCurrentRewards(ctx, a.val)
	require.NoError(t, err)
	require.Equal(t, decNscn("891000000"), cur.Rewards, "commission + delegators' part = the share")

	// What a wallet's claim screen reads before the claim.
	q := distrkeeper.NewQuerier(dk)
	for _, w := range []struct {
		who  sdk.AccAddress
		want string
	}{
		{d0.addr, "137076923.076918"},
		{d1.addr, "68538461.538459"},
		{a.addr, "685384615.38459"},
	} {
		res, err := q.DelegationRewards(ctx, &distrtypes.QueryDelegationRewardsRequest{
			DelegatorAddress: w.who.String(), ValidatorAddress: a.val.String(),
		})
		require.NoError(t, err)
		require.Equal(t, decNscn(w.want), res.Rewards, "pending rewards of %s", w.who)
	}
	c.requireDistributionInvariants()

	// --- the claims ---------------------------------------------------------
	claim := func(who acct, msg sdk.Msg, want int64) {
		t.Helper()
		before := c.balance(who.addr)
		c.deliverOK(who, msg)
		require.Equal(t, math.NewInt(want), c.balance(who.addr).Sub(before), "%T by %s", msg, who.addr)
	}
	claim(d0, distrtypes.NewMsgWithdrawDelegatorReward(d0.addr.String(), a.val.String()), 137_076_923)
	claim(d1, distrtypes.NewMsgWithdrawDelegatorReward(d1.addr.String(), a.val.String()), 68_538_461)
	claim(a, distrtypes.NewMsgWithdrawDelegatorReward(a.addr.String(), a.val.String()), 685_384_615)
	claim(a, distrtypes.NewMsgWithdrawValidatorCommission(a.val.String()), 99_000_000)
	require.Less(t, c.height, int64(10), "the claims must land before epoch 2 pays again")

	// 989,999,999 left x/distribution; 1 nscn of truncation dust stayed.
	ctx = c.ctx()
	require.Equal(t, minted.Sub(math.NewInt(989_999_999)), c.distrBalance())
	out, err = dk.GetValidatorOutstandingRewardsCoins(ctx, a.val)
	require.NoError(t, err)
	require.Equal(t, decNscn("0.000033"), out)
	feePool, err := dk.FeePool.Get(ctx)
	require.NoError(t, err)
	require.Equal(t, decNscn("0.999967"), feePool.CommunityPool)

	// Claims move coins, they never create them.
	require.Equal(t, genesisSupply.Add(minted), c.supply())
	c.requireDistributionInvariants()
}

// An epoch that fails partway through the payout loop is discarded whole, on
// the real keepers (MaybeRunEpoch runs the epoch in one cache context).
//
// Epoch 1 at height 5 pays normally. Then epoch 2's last validator in payout
// order has its outstanding-rewards record corrupted, so its
// AllocateTokensToValidator fails after booking its commission and current
// rewards. By then epoch 2 has minted, consumed the observed gas fees, paid
// the other two validators through x/distribution and written their
// snapshots. A run of the bare DistributeEpochRewards on a throwaway context
// shows exactly that partial state; MaybeRunEpoch must leave none of it.
func TestAnAbortedEpochLeavesNoTrace(t *testing.T) {
	c := newTestChain(t, withFastRounds(), withPayoutNumbers())
	c.runTo(9) // epoch 1 paid at height 5; height 10 closes epoch 2
	ctx, _ := c.ctx().CacheContext()
	require.Equal(t, int64(10), ctx.BlockHeight())
	rk, dk := c.app.RewardsKeeper, c.app.DistrKeeper
	rewardsAddr := authtypes.NewModuleAddress(rewardstypes.ModuleName)
	bal := func(a sdk.AccAddress) math.Int {
		return c.app.BankKeeper.GetBalance(ctx, a, stakeenginetypes.BondDenom).Amount
	}
	supply := func() math.Int {
		return c.app.BankKeeper.GetSupply(ctx, stakeenginetypes.BondDenom).Amount
	}

	// A carry the module account really holds, and fees observed this epoch.
	carry, fees := math.NewInt(7), math.NewInt(1_000)
	require.NoError(t, c.app.BankKeeper.MintCoins(ctx, rewardstypes.ModuleName,
		sdk.NewCoins(sdk.NewCoin(stakeenginetypes.BondDenom, carry))))
	require.NoError(t, rk.SetRemainder(ctx, carry))
	require.NoError(t, rk.AddObservedGasFees(ctx, fees))

	var order []sdk.ValAddress
	require.NoError(t, c.app.StakingKeeper.IterateBondedValidatorsByPower(ctx, func(_ int64, v stakingtypes.ValidatorI) bool {
		order = append(order, sdk.MustValAddressFromBech32(v.GetOperator()))
		return false
	}))
	require.Len(t, order, 3)
	last := order[len(order)-1]
	ctx.KVStore(c.app.GetKey(distrtypes.StoreKey)).Set(distrtypes.GetValidatorOutstandingRewardsKey(last), []byte{0xff})

	// Control: without the epoch's cache context, the failure leaves a
	// half-paid epoch behind.
	probe, _ := ctx.CacheContext()
	require.Error(t, rk.DistributeEpochRewards(probe, 2))
	for _, v := range order[:2] {
		snap, found, err := rk.GetRewardSnapshot(probe, 2, v)
		require.NoError(t, err)
		require.True(t, found && snap.Eligible, "the probe paid %s before failing", v)
	}
	probeFees, err := rk.GetObservedGasFees(probe)
	require.NoError(t, err)
	require.True(t, probeFees.IsZero(), "the probe consumed the observed fees")
	require.True(t, c.app.BankKeeper.GetSupply(probe, stakeenginetypes.BondDenom).Amount.GT(supply()), "the probe minted")

	supplyBefore, rewardsBefore, distrBefore := supply(), bal(rewardsAddr), bal(authtypes.NewModuleAddress(distrtypes.ModuleName))
	outBefore, err := dk.GetValidatorOutstandingRewardsCoins(ctx, order[0])
	require.NoError(t, err)
	commBefore, err := dk.GetValidatorAccumulatedCommission(ctx, last)
	require.NoError(t, err)
	eventsBefore := len(ctx.EventManager().Events())

	require.NoError(t, rk.MaybeRunEpoch(ctx, 10), "an emission error never halts the chain")

	require.Equal(t, supplyBefore, supply(), "nothing minted")
	require.Equal(t, rewardsBefore, bal(rewardsAddr), "the rewards module account holds exactly the carry")
	require.Equal(t, carry, rewardsBefore)
	require.Equal(t, distrBefore, bal(authtypes.NewModuleAddress(distrtypes.ModuleName)), "no share reached x/distribution")
	gotCarry, err := rk.GetRemainder(ctx)
	require.NoError(t, err)
	require.Equal(t, carry, gotCarry, "the carry is not consumed")
	gotFees, err := rk.GetObservedGasFees(ctx)
	require.NoError(t, err)
	require.Equal(t, fees, gotFees, "the observed fees are not consumed")
	outAfter, err := dk.GetValidatorOutstandingRewardsCoins(ctx, order[0])
	require.NoError(t, err)
	require.Equal(t, outBefore, outAfter, "the first validator's booking was discarded")
	commAfter, err := dk.GetValidatorAccumulatedCommission(ctx, last)
	require.NoError(t, err)
	require.Equal(t, commBefore, commAfter, "the failing validator's half-booking was discarded")
	for _, v := range order {
		_, found, err := rk.GetRewardSnapshot(ctx, 2, v)
		require.NoError(t, err)
		require.False(t, found, "no epoch-2 snapshot for %s", v)
	}
	require.Len(t, ctx.EventManager().Events(), eventsBefore, "a discarded epoch emits nothing")
}
