package keeper_test

import (
	"testing"

	"cosmossdk.io/math"
	"github.com/stretchr/testify/require"

	"github.com/saicon-network/saicon/chain/x/rewards/types"
)

// ADR-0011 D2 — gas fees OFFSET issuance, they do not add to it:
//
//	Emission = max(0, EpochTarget − GasFees)
//
// The security budget is a target TOTAL paid to validators. Every SCN of fee
// revenue displaces one SCN of newly-created supply, which is what eventually
// stops minting before the 21M cap is reached.
//
// Note what this module does NOT do: it does not take the fees. x/distribution
// sweeps the fee collector every block, long before this module runs, and pays
// them out stake-weighted. x/rewards only OBSERVES the revenue and mints less
// accordingly. Taking the fees instead would double-pay them — the trap the ADR
// flags — and rerouting them through the PoP gate is the open F1/F3 decision.

func TestGasOffset_FeesReduceTheMint(t *testing.T) {
	f, _ := eligible(t, 2, testSupply, registerV1Params())

	fees := math.NewInt(1_000_000_000) // a sixth of testPool
	require.NoError(t, f.keeper.AddObservedGasFees(f.ctx, fees))

	require.NoError(t, f.keeper.DistributeEpochRewards(f.ctx, 4))

	require.Len(t, f.bank.minted, 1)
	require.True(t, f.bank.minted[0].AmountOf(types.RewardDenom).Equal(testPool.Sub(fees)),
		"issuance must fall by exactly the fee revenue: expected %s, got %s",
		testPool.Sub(fees), f.bank.minted[0])
}

// Fee revenue at or above the target stops minting entirely — the end state the
// Register describes, where the cap is never reached.
func TestGasOffset_FeesAtOrAboveTheTargetStopMintingEntirely(t *testing.T) {
	for _, fees := range []math.Int{testPool, testPool.MulRaw(3)} {
		t.Run(fees.String(), func(t *testing.T) {
			f, addrs := eligible(t, 2, testSupply, registerV1Params())
			require.NoError(t, f.keeper.AddObservedGasFees(f.ctx, fees))

			require.NoError(t, f.keeper.DistributeEpochRewards(f.ctx, 5))

			require.Empty(t, f.bank.minted,
				"fees already cover the whole budget; minting anything would add to it, not offset it")
			// The validators are not paid twice: they already received the fee
			// revenue through x/distribution.
			require.Empty(t, f.bank.sends)

			snap, found, err := f.keeper.GetRewardSnapshot(f.ctx, 5, addrs[0])
			require.NoError(t, err)
			require.True(t, found, "the epoch must still be explicable")
			require.Contains(t, snap.Reason, "gas fees")
		})
	}
}

// The counter is per epoch. Revenue observed in one epoch must not keep
// suppressing issuance in the next.
func TestGasOffset_CounterResetsEachEpoch(t *testing.T) {
	f, _ := eligible(t, 2, testSupply, registerV1Params())

	fees := math.NewInt(2_000_000_000)
	require.NoError(t, f.keeper.AddObservedGasFees(f.ctx, fees))
	require.NoError(t, f.keeper.DistributeEpochRewards(f.ctx, 6))
	require.True(t, f.bank.minted[0].AmountOf(types.RewardDenom).Equal(testPool.Sub(fees)))

	observed, err := f.keeper.GetObservedGasFees(f.ctx)
	require.NoError(t, err)
	require.True(t, observed.IsZero(), "the counter must be cleared once spent, got %s", observed)

	// A second epoch with no new revenue mints the full budget again.
	require.NoError(t, f.keeper.DistributeEpochRewards(f.ctx, 7))
	require.Len(t, f.bank.minted, 2)
	require.True(t, f.bank.minted[1].AmountOf(types.RewardDenom).Equal(testPool),
		"last epoch's fees must not suppress this epoch, got %s", f.bank.minted[1])
}

// Fees and the carried remainder both offset issuance, and they compose: neither
// may be counted twice, and together they can only reduce the mint to zero.
func TestGasOffset_ComposesWithTheCarriedRemainder(t *testing.T) {
	f, _ := eligible(t, 2, testSupply, registerV1Params())

	fees := math.NewInt(1_000_000_000)
	carry := math.NewInt(500_000_000)
	require.NoError(t, f.keeper.AddObservedGasFees(f.ctx, fees))
	require.NoError(t, f.keeper.SetRemainder(f.ctx, carry))

	require.NoError(t, f.keeper.DistributeEpochRewards(f.ctx, 8))

	require.Len(t, f.bank.minted, 1)
	require.True(t, f.bank.minted[0].AmountOf(types.RewardDenom).Equal(testPool.Sub(fees).Sub(carry)),
		"both offsets apply once each: expected %s, got %s",
		testPool.Sub(fees).Sub(carry), f.bank.minted[0])

	// What validators receive from THIS module is still the carry plus the mint —
	// the fee half reached them through x/distribution.
	paid := math.ZeroInt()
	for _, s := range f.bank.sends {
		paid = paid.Add(s.amt.AmountOf(types.RewardDenom))
	}
	require.True(t, paid.Equal(testPool.Sub(fees)),
		"x/rewards pays the budget minus what x/distribution already paid, got %s", paid)
}

// Observation accumulates across the blocks of an epoch rather than replacing.
func TestGasOffset_ObservationsAccumulate(t *testing.T) {
	f, _ := eligible(t, 1, testSupply, registerV1Params())

	for i := 0; i < 3; i++ {
		require.NoError(t, f.keeper.AddObservedGasFees(f.ctx, math.NewInt(100)))
	}

	observed, err := f.keeper.GetObservedGasFees(f.ctx)
	require.NoError(t, err)
	require.True(t, observed.Equal(math.NewInt(300)), "got %s", observed)
}

// A zero or negative observation is ignored rather than corrupting the counter —
// this is fed from a balance read in BeginBlock and must never go backwards.
func TestGasOffset_IgnoresNonPositiveObservations(t *testing.T) {
	f, _ := eligible(t, 1, testSupply, registerV1Params())

	require.NoError(t, f.keeper.AddObservedGasFees(f.ctx, math.NewInt(500)))
	require.NoError(t, f.keeper.AddObservedGasFees(f.ctx, math.ZeroInt()))
	require.NoError(t, f.keeper.AddObservedGasFees(f.ctx, math.NewInt(-100)))

	observed, err := f.keeper.GetObservedGasFees(f.ctx)
	require.NoError(t, err)
	require.True(t, observed.Equal(math.NewInt(500)), "got %s", observed)
}
