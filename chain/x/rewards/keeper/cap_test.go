package keeper_test

import (
	"os"
	"path"
	"regexp"
	"testing"

	"cosmossdk.io/math"
	sdk "github.com/cosmos/cosmos-sdk/types"
	stakingtypes "github.com/cosmos/cosmos-sdk/x/staking/types"
	"github.com/stretchr/testify/require"

	"github.com/saicon-network/saicon/chain/x/rewards/types"
	vrtypes "github.com/saicon-network/saicon/chain/x/validatorregistry/types"
)

// The four acceptance criteria blocker B8 states for the emission rework
// (chain/docs/adr/ADR-0011-emission-and-single-mint-path.md, D3/D4):
// no double mint, exact pool conservation, a hard 21M cap, and a kill switch.

// eligible wires n validators that all pass the three reward gates, on a chain
// whose current supply is `supply`.
func eligible(t *testing.T, n int, supply math.Int, params types.Params) (*emissionFixture, []sdk.ValAddress) {
	t.Helper()
	addrs := make([]sdk.ValAddress, n)
	vals := make([]stakingtypes.ValidatorI, n)
	statuses := map[string]vrtypes.Status{}
	eps := map[string]math.Int{}
	for i := 0; i < n; i++ {
		a := valAddr(byte(40 + i))
		addrs[i] = a
		vals[i] = fakeValidator{operator: a.String()}
		statuses[a.String()] = vrtypes.Status_STATUS_ACTIVE
		eps[a.String()] = math.NewInt(5)
	}
	f := initEmissionFixture(t,
		mockStakingKeeper{totalBonded: testBonded, validators: vals},
		mockParticipationKeeper{ep: eps, epochLen: 100},
		mockVRKeeper{statuses: statuses},
		mockComplianceKeeper{},
		params,
	)
	f.bank.supply = supply
	return f, addrs
}

// B8 (1) — NO DOUBLE MINT. However many validators are paid, the epoch pool is
// created by exactly ONE MintCoins call. The original shape minted once per
// validator inside the distribution loop, which is what makes conservation
// unprovable and lets a second minter hide.
func TestEmission_MintsExactlyOncePerEpoch(t *testing.T) {
	f, _ := eligible(t, 4, math.NewInt(1_000_000), registerV1Params())

	require.NoError(t, f.keeper.DistributeEpochRewards(f.ctx, 7))

	require.Len(t, f.bank.minted, 1, "the epoch pool must be minted in a single call")
	require.Len(t, f.bank.sends, 4, "each eligible validator is still paid individually")
}

// B8 (2) — EXACT CONSERVATION, across a capped epoch and the one after it.
// Everything minted is either paid out or carried; nothing is paid that was not
// minted. The uneven split that produces a remainder arises where it actually
// can — when the supply ceiling clamps the pool below the per-validator target.
func TestEmission_PoolIsExactlyConserved(t *testing.T) {
	// 5 base units of headroom shared by 3 validators: 1 each, 2 left over.
	headroom := math.NewInt(5)
	f, _ := eligible(t, 3, types.MaxSupplyNscn().Sub(headroom), types.NewParams(1_000_000, true))

	require.NoError(t, f.keeper.DistributeEpochRewards(f.ctx, 1))

	minted := math.ZeroInt()
	for _, m := range f.bank.minted {
		minted = minted.Add(m.AmountOf(types.RewardDenom))
	}
	paid := math.ZeroInt()
	for _, s := range f.bank.sends {
		paid = paid.Add(s.amt.AmountOf(types.RewardDenom))
	}
	carried, err := f.keeper.GetRemainder(f.ctx)
	require.NoError(t, err)

	require.True(t, minted.Equal(headroom), "the mint is clamped to the headroom, got %s", minted)
	require.True(t, minted.Equal(paid.Add(carried)),
		"minted %s must equal paid %s + carried %s", minted, paid, carried)
	require.True(t, carried.IsPositive(), "an uneven split must leave a carry, got %s", carried)

	// And it still holds after a second epoch, where the cap now allows nothing.
	require.NoError(t, f.keeper.DistributeEpochRewards(f.ctx, 2))

	minted2 := math.ZeroInt()
	for _, m := range f.bank.minted {
		minted2 = minted2.Add(m.AmountOf(types.RewardDenom))
	}
	paid2 := math.ZeroInt()
	for _, s := range f.bank.sends {
		paid2 = paid2.Add(s.amt.AmountOf(types.RewardDenom))
	}
	carried2, err := f.keeper.GetRemainder(f.ctx)
	require.NoError(t, err)

	require.True(t, minted2.Equal(headroom), "no further issuance is possible at the cap")
	require.True(t, minted2.Equal(paid2.Add(carried2)),
		"conservation must hold cumulatively: %s vs %s + %s", minted2, paid2, carried2)
}

// The carry REDUCES fresh issuance rather than adding to the pool: value already
// held in the module account substitutes for minting, the same shape as the
// gas-fee offset the Register specifies. Without this the carry would inflate
// supply by re-minting units that already exist.
func TestEmission_CarryReducesFreshIssuance(t *testing.T) {
	f, _ := eligible(t, 2, testSupply, registerV1Params())

	carry := math.NewInt(400_000)
	require.NoError(t, f.keeper.SetRemainder(f.ctx, carry))

	require.NoError(t, f.keeper.DistributeEpochRewards(f.ctx, 9))

	require.Len(t, f.bank.minted, 1)
	require.True(t, f.bank.minted[0].AmountOf(types.RewardDenom).Equal(testPool.Sub(carry)),
		"expected the budget %s minus the carry %s, got %s", testPool, carry, f.bank.minted[0])

	// The validators still receive the full epoch budget — the carry funded part
	// of it instead of new issuance.
	paid := math.ZeroInt()
	for _, s := range f.bank.sends {
		paid = paid.Add(s.amt.AmountOf(types.RewardDenom))
	}
	require.True(t, paid.Equal(testPool), "payout must not depend on how it was funded, got %s", paid)
}

// B8 (3) — HARD 21M CAP. The cap is a PRECONDITION of the mint, not an invariant
// noticing afterwards: an EndBlock check firing after a breach would halt the
// chain instead of preventing it.
func TestEmission_NeverMintsPastTheMaxSupply(t *testing.T) {
	nearCap := types.MaxSupplyNscn().Sub(math.OneInt()) // one nscn of headroom
	f, _ := eligible(t, 2, nearCap, types.NewParams(1_000_000, true))

	require.NoError(t, f.keeper.DistributeEpochRewards(f.ctx, 3))

	require.Len(t, f.bank.minted, 1)
	minted := f.bank.minted[0].AmountOf(types.RewardDenom)
	require.True(t, minted.Equal(math.OneInt()),
		"only the remaining headroom may be minted, got %s", minted)
	require.True(t, nearCap.Add(minted).Equal(types.MaxSupplyNscn()),
		"supply must land exactly on the cap, never above it")
}

// At the cap the mint stops — without erroring, because the chain must keep
// producing blocks.
func TestEmission_AtTheCapMintsNothingAndDoesNotFail(t *testing.T) {
	f, addrs := eligible(t, 2, types.MaxSupplyNscn(), registerV1Params())

	require.NoError(t, f.keeper.DistributeEpochRewards(f.ctx, 4))

	require.Empty(t, f.bank.minted, "no headroom means no mint at all")
	require.Empty(t, f.bank.sends, "and nothing to pay out")

	// The reason is recorded per validator, so a stopped emission is auditable
	// rather than looking like an outage.
	snap, found, err := f.keeper.GetRewardSnapshot(f.ctx, 4, addrs[0])
	require.NoError(t, err)
	require.True(t, found)
	require.False(t, snap.Eligible)
	require.Contains(t, snap.Reason, "max supply")
}

// B8 (4) — KILL SWITCH. Disabling emission must be auditable: a validator that
// earns nothing must be able to see WHY, not just find an empty epoch.
func TestEmission_KillSwitchRecordsWhyNothingWasPaid(t *testing.T) {
	f, addrs := eligible(t, 2, math.NewInt(1_000_000),
		types.NewParams(types.DefaultRewardPerEpochAmount, false))

	require.NoError(t, f.keeper.DistributeEpochRewards(f.ctx, 5))

	require.Empty(t, f.bank.minted, "disabled means no mint")
	require.Empty(t, f.bank.sends)

	for _, a := range addrs {
		snap, found, err := f.keeper.GetRewardSnapshot(f.ctx, 5, a)
		require.NoError(t, err)
		require.True(t, found, "a disabled epoch must still leave a record for %s", a)
		require.False(t, snap.Eligible)
		require.Contains(t, snap.Reason, "disabled")
	}
}

// With emission off nothing accrues either: the switch stops the machine, it
// does not quietly bank value for whoever turns it back on.
func TestEmission_KillSwitchDoesNotAccrueRemainder(t *testing.T) {
	f, _ := eligible(t, 3, math.NewInt(1_000_000), types.NewParams(1_000_001, false))

	require.NoError(t, f.keeper.DistributeEpochRewards(f.ctx, 6))

	carried, err := f.keeper.GetRemainder(f.ctx)
	require.NoError(t, err)
	require.True(t, carried.IsZero(), "a disabled epoch must accrue nothing, got %s", carried)
}

// The budget funds the epoch, and only the validators that pass the gates share
// it. A jailed validator does not shrink the pool — it enlarges everyone else's
// share, which is the intended incentive: the security budget is paid for
// securing the chain, and whoever is actually doing it collects it.
func TestEmission_BudgetIsSharedAmongEligibleValidatorsOnly(t *testing.T) {
	a0, a1, a2 := valAddr(60), valAddr(61), valAddr(62)

	f := initEmissionFixture(t,
		mockStakingKeeper{totalBonded: testBonded, validators: []stakingtypes.ValidatorI{
			fakeValidator{operator: a0.String()},
			fakeValidator{operator: a1.String()},
			fakeValidator{operator: a2.String()},
		}},
		mockParticipationKeeper{ep: map[string]math.Int{
			a0.String(): math.NewInt(5), a1.String(): math.NewInt(5), a2.String(): math.NewInt(5),
		}, epochLen: 100},
		mockVRKeeper{statuses: map[string]vrtypes.Status{
			a0.String(): vrtypes.Status_STATUS_ACTIVE,
			a1.String(): vrtypes.Status_STATUS_JAILED, // not eligible
			a2.String(): vrtypes.Status_STATUS_ACTIVE,
		}},
		mockComplianceKeeper{},
		registerV1Params(),
	)
	f.bank.supply = testSupply

	require.NoError(t, f.keeper.DistributeEpochRewards(f.ctx, 8))

	require.Len(t, f.bank.minted, 1)
	require.True(t, f.bank.minted[0].AmountOf(types.RewardDenom).Equal(testPool),
		"the pool is the epoch budget regardless of how many qualify, got %s", f.bank.minted[0])

	require.Len(t, f.bank.sends, 2, "only the two eligible validators are paid")
	half := testPool.QuoRaw(2)
	for _, s := range f.bank.sends {
		require.True(t, s.amt.AmountOf(types.RewardDenom).Equal(half),
			"each of the two shares half the budget, got %s", s.amt)
	}
}

// REGRESSION (found by a live run, not by these tests): the shipped genesis must
// fund LESS than the maximum supply, or the cap leaves zero headroom and the
// reward path is dead from block one — every epoch denying with "max supply
// reached".
//
// The default genesis used to fund 1e19 nscn, 4,762x the 21M ceiling. Every unit
// test here used a synthetic supply near the cap and so never noticed; the
// project's own end-to-end script did, once someone ran it. This test reads the
// shipped defaults so the two can never drift apart again.
func TestShippedGenesisLeavesHeadroomUnderTheCap(t *testing.T) {
	for _, script := range []string{
		"../../../docker/entrypoint-singlenode.sh",
		"../../../docker/testnet/gen-genesis.sh",
	} {
		t.Run(path.Base(script), func(t *testing.T) {
			src, err := os.ReadFile(script)
			require.NoError(t, err)

			m := regexp.MustCompile(`GENESIS_AMOUNT:-(\d+)nscn`).FindSubmatch(src)
			require.NotNil(t, m, "could not find the default GENESIS_AMOUNT in %s", script)

			funded, ok := math.NewIntFromString(string(m[1]))
			require.True(t, ok)

			require.True(t, funded.LT(types.MaxSupplyNscn()),
				"genesis funds %s nscn but the ceiling is %s: emission would have zero headroom "+
					"and every epoch would deny with %q",
				funded, types.MaxSupplyNscn(), "max supply reached")
		})
	}
}

// The ceiling binds the initial allocation as well as later emission: a genesis
// funded above 21M must not start. At the ceiling exactly it starts, with zero
// headroom for emission.
func TestGenesisRefusesSupplyAboveTheCap(t *testing.T) {
	f, _ := eligible(t, 1, types.MaxSupplyNscn(), types.DefaultParams())
	require.NoError(t, f.keeper.InitGenesis(f.ctx, *types.DefaultGenesis()))

	f, _ = eligible(t, 1, types.MaxSupplyNscn().AddRaw(1), types.DefaultParams())
	err := f.keeper.InitGenesis(f.ctx, *types.DefaultGenesis())
	require.ErrorContains(t, err, "exceeds the maximum supply")
}
