package keeper_test

import (
	"testing"

	"cosmossdk.io/collections"
	"cosmossdk.io/math"
	sdk "github.com/cosmos/cosmos-sdk/types"
	authtypes "github.com/cosmos/cosmos-sdk/x/auth/types"
	stakingtypes "github.com/cosmos/cosmos-sdk/x/staking/types"
	"github.com/stretchr/testify/require"

	"github.com/saicon-network/saicon/chain/x/stakeengine/types"
)

// ADR-0012's trap: if bonding moved tokens OUT of public circulating supply, the
// cap would shrink as stake grew, and "60% of public circulating" would quietly
// become "37.5% of total supply". Bonded, unbonding and queued tokens are
// publicly owned and must keep counting.
func TestPublicCirculating_BondingAndQueuingDoNotShrinkIt(t *testing.T) {
	f := newFixture(t)
	before, err := f.k.PublicCirculating(f.ctx)
	require.NoError(t, err)
	require.Equal(t, nscn(10_000_000), before)

	// Bond another 500k: tokens move from an account into the bonded pool.
	_, err = f.staking.Delegate(f.ctx, acc(1), nscn(500_000), stakingtypes.Unbonded, f.staking.validators[string(val(1))], true)
	require.NoError(t, err)
	// Queue 100k: tokens move into the stakeengine escrow.
	require.NoError(t, f.bank.move(acc(1), f.k.EscrowAddress(), nscn(100_000)))

	after, err := f.k.PublicCirculating(f.ctx)
	require.NoError(t, err)
	require.Equal(t, before, after, "bonding and queuing must not move public circulating supply")
}

func TestPublicCirculating_SubtractsNonCirculatingModulesAndExcludedHoldings(t *testing.T) {
	f := newFixture(t)
	f.bank.set(authtypes.NewModuleAddress("distribution"), nscn(100))
	f.bank.set(authtypes.NewModuleAddress("fee_collector"), nscn(50))
	f.bank.set(authtypes.NewModuleAddress("gov"), nscn(25))
	f.bank.set(authtypes.NewModuleAddress("rewards"), nscn(10))

	// An excluded treasury: 1,000 liquid, 500 delegated, 200 unbonding, 300 queued.
	treasury := acc(9)
	f.bank.set(treasury, nscn(1_000))
	f.staking.delegations[string(treasury)] = []stakingtypes.Delegation{{
		DelegatorAddress: treasury.String(), ValidatorAddress: val(1).String(), Shares: math.LegacyNewDecFromInt(nscn(500)),
	}}
	f.staking.ubds[string(treasury)] = []stakingtypes.UnbondingDelegation{{
		DelegatorAddress: treasury.String(), ValidatorAddress: val(1).String(),
		Entries: []stakingtypes.UnbondingDelegationEntry{{Balance: nscn(200), InitialBalance: nscn(200)}},
	}}
	require.NoError(t, f.k.Queue.Set(f.ctx, 7, types.QueueEntry{
		Id: 7, Delegator: treasury.String(), Validator: val(1).String(), Amount: nscn(300),
	}))

	p := types.DefaultParams()
	p.ExcludedAddresses = []string{sdk.MustBech32ifyAddressBytes(types.AccountPrefix, treasury)}
	require.NoError(t, f.k.Params.Set(f.ctx, p))

	got, err := f.k.PublicCirculating(f.ctx)
	require.NoError(t, err)
	want := nscn(10_000_000).Sub(nscn(185)).Sub(nscn(2_000))
	require.Equal(t, want, got)
}

// Only the excluded owner's queued amount leaves public circulating supply; a
// stranger's entry in the same queue stays in, and a malformed owner is an error
// rather than a silent skip.
func TestPublicCirculating_OnlyExcludedOwnersQueuedAmountsCount(t *testing.T) {
	f := newFixture(t)
	treasury, stranger := acc(9), acc(8)
	require.NoError(t, f.k.Queue.Set(f.ctx, 1, types.QueueEntry{
		Id: 1, Delegator: treasury.String(), Validator: val(1).String(), Amount: nscn(300),
	}))
	require.NoError(t, f.k.Queue.Set(f.ctx, 2, types.QueueEntry{
		Id: 2, Delegator: stranger.String(), Validator: val(1).String(), Amount: nscn(700),
	}))
	require.NoError(t, f.k.Queue.Set(f.ctx, 3, types.QueueEntry{
		Id: 3, Delegator: treasury.String(), Validator: val(1).String(), Amount: nscn(50),
	}))
	p := types.DefaultParams()
	p.ExcludedAddresses = []string{sdk.MustBech32ifyAddressBytes(types.AccountPrefix, treasury)}
	require.NoError(t, f.k.Params.Set(f.ctx, p))

	got, err := f.k.PublicCirculating(f.ctx)
	require.NoError(t, err)
	require.Equal(t, nscn(10_000_000).Sub(nscn(350)), got)

	require.NoError(t, f.k.Queue.Set(f.ctx, 4, types.QueueEntry{
		Id: 4, Delegator: "not-an-address", Validator: val(1).String(), Amount: nscn(1),
	}))
	_, err = f.k.PublicCirculating(f.ctx)
	require.Error(t, err, "an undecodable queue owner must fail, not be skipped")
}

func TestRecomputeCaps_GlobalCapEWMAAndValidatorCap(t *testing.T) {
	f := newFixture(t)

	st, err := f.k.RecomputeCaps(f.ctx, 0, true)
	require.NoError(t, err)
	require.Equal(t, uint64(0), st.Epoch)
	require.Equal(t, nscn(10_000_000), st.PublicCirculating)
	require.Equal(t, nscn(1_680_000), st.GlobalCap, "0.08 × 21M binds below 0.60 × 10M")
	require.Equal(t, math.LegacyNewDecFromInt(nscn(1_000_000)), st.EwmaBonded, "first observation")
	require.Equal(t, nscn(20_000), st.ValidatorCap, "2% of 1M with the default floor of 0")

	stored, err := f.k.CapState.Get(f.ctx)
	require.NoError(t, err)
	require.Equal(t, st, stored)

	// Bond another 1M: the next epoch's EWMA moves alpha = 2/31 of the way.
	_, err = f.staking.Delegate(f.ctx, acc(1), nscn(1_000_000), stakingtypes.Unbonded, f.staking.validators[string(val(1))], true)
	require.NoError(t, err)
	st2, err := f.k.RecomputeCaps(f.ctx, 1, false)
	require.NoError(t, err)
	alpha := math.LegacyNewDec(2).QuoInt64(31)
	want := alpha.MulInt(nscn(2_000_000)).Add(math.LegacyOneDec().Sub(alpha).MulInt(nscn(1_000_000)))
	require.Equal(t, want, st2.EwmaBonded)
	require.Equal(t, uint64(1), st2.Epoch)
}

func TestRecomputeCaps_TheFloorRaisesTheValidatorCap(t *testing.T) {
	f := newFixture(t)
	p := types.DefaultParams()
	p.ValidatorCapFloor = nscn(2_000_000) // the devnet value
	require.NoError(t, f.k.Params.Set(f.ctx, p))
	st, err := f.k.RecomputeCaps(f.ctx, 0, true)
	require.NoError(t, err)
	require.Equal(t, nscn(2_000_000), st.ValidatorCap)
}

func TestMaybeRecomputeCaps_OnlyOnTheParticipationBoundary(t *testing.T) {
	f := newFixture(t)
	for _, h := range []int64{1, 99, 101, 150} {
		require.NoError(t, f.k.MaybeRecomputeCaps(f.ctx, h))
		_, err := f.k.CapState.Get(f.ctx)
		require.ErrorIs(t, err, collections.ErrNotFound, "height %d is not an epoch boundary", h)
	}
	require.NoError(t, f.k.MaybeRecomputeCaps(f.ctx, 200))
	st, err := f.k.CapState.Get(f.ctx)
	require.NoError(t, err)
	require.Equal(t, uint64(2), st.Epoch, "x/participation closes epoch h/epochLen at h%epochLen==0")
}

func TestHeadroomAndAccessors(t *testing.T) {
	f := newFixture(t)
	_, err := f.k.Headroom(f.ctx)
	require.ErrorIs(t, err, types.ErrCapsNotComputed)

	_, err = f.k.RecomputeCaps(f.ctx, 0, true)
	require.NoError(t, err)

	h, err := f.k.Headroom(f.ctx)
	require.NoError(t, err)
	require.Equal(t, nscn(680_000), h, "1.68M cap − 1M bonded")

	vc, err := f.k.ValidatorCap(f.ctx)
	require.NoError(t, err)
	require.Equal(t, nscn(20_000), vc)

	tok, err := f.k.ValidatorTokens(f.ctx, val(1))
	require.NoError(t, err)
	require.Equal(t, nscn(1_000_000), tok)
	tok, err = f.k.ValidatorTokens(f.ctx, val(42))
	require.NoError(t, err)
	require.True(t, tok.IsZero(), "an unknown validator holds nothing")

	sb, err := f.k.SelfBond(f.ctx, val(1))
	require.NoError(t, err)
	require.Equal(t, nscn(1_000_000), sb)
	sb, err = f.k.SelfBond(f.ctx, val(42))
	require.NoError(t, err)
	require.True(t, sb.IsZero())

	// Headroom goes negative when the cap falls below what is bonded; it is not clamped.
	f.bank.supply = nscn(1_000_000) // public circulating collapses → 0.60 × arm shrinks
	_, err = f.k.RecomputeCaps(f.ctx, 1, false)
	require.NoError(t, err)
	h, err = f.k.Headroom(f.ctx)
	require.NoError(t, err)
	require.True(t, h.IsNegative())
}
