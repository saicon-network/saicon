package types_test

import (
	"testing"

	"cosmossdk.io/math"
	"github.com/stretchr/testify/require"

	"github.com/saicon-network/saicon/chain/x/stakeengine/types"
)

func nscnOf(s int64) math.Int { return math.NewInt(s).MulRaw(scn) }

var maxSupply = nscnOf(21_000_000)

func TestGlobalCap_TheSmallerArmWins(t *testing.T) {
	p := types.DefaultParams() // 0.08 of max supply = 1,680,000 SCN; 0.60 of public circulating

	// Public circulating 10M SCN: 0.60 × 10M = 6M, so the max-supply arm (1.68M) binds.
	require.Equal(t, nscnOf(1_680_000), types.GlobalCap(p, maxSupply, nscnOf(10_000_000)))

	// Public circulating 1.45M SCN (the Register snapshot): 0.60 × 1.45M = 870k binds.
	require.Equal(t, nscnOf(870_000), types.GlobalCap(p, maxSupply, nscnOf(1_450_000)))
}

func TestNextEWMA(t *testing.T) {
	// The first observation is the observation itself.
	require.Equal(t, "1000.000000000000000000",
		types.NextEWMA(math.LegacyDec{}, math.NewInt(1000), 30, true).String())

	// alpha = 2/(N+1). N=1 gives alpha=1: the EWMA is just the latest value.
	require.Equal(t, "250.000000000000000000",
		types.NextEWMA(math.LegacyNewDec(1000), math.NewInt(250), 1, false).String())

	// N=3 gives alpha=0.5: halfway between 1000 and 0.
	require.Equal(t, "500.000000000000000000",
		types.NextEWMA(math.LegacyNewDec(1000), math.ZeroInt(), 3, false).String())

	// A missing previous value is treated as a first observation, never as zero.
	require.Equal(t, "400.000000000000000000",
		types.NextEWMA(math.LegacyDec{}, math.NewInt(400), 30, false).String())
}

func TestValidatorCap_FractionOrFloorWhicheverIsLarger(t *testing.T) {
	p := types.DefaultParams()
	ewma := math.LegacyNewDecFromInt(nscnOf(1_000_000))

	// Floor 0: the strict 2% rule — 20,000 SCN of a 1M base.
	require.Equal(t, nscnOf(20_000), types.ValidatorCap(p, ewma))

	// The devnet floor, 2,000,000 SCN, is larger and wins.
	p.ValidatorCapFloor = nscnOf(2_000_000)
	require.Equal(t, nscnOf(2_000_000), types.ValidatorCap(p, ewma))

	// A floor below 2% changes nothing.
	p.ValidatorCapFloor = nscnOf(10_000)
	require.Equal(t, nscnOf(20_000), types.ValidatorCap(p, ewma))
}

func TestPublicCirculating(t *testing.T) {
	require.Equal(t, math.NewInt(600),
		types.PublicCirculating(math.NewInt(1000), math.NewInt(300), math.NewInt(100)))
	// Never negative.
	require.Equal(t, math.ZeroInt(),
		types.PublicCirculating(math.NewInt(100), math.NewInt(300)))
	// An unset input is skipped, not a panic.
	require.Equal(t, math.NewInt(100),
		types.PublicCirculating(math.NewInt(100), math.Int{}))
}
