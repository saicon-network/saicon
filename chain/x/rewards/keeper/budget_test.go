package keeper_test

import (
	"testing"

	"cosmossdk.io/math"
	"github.com/stretchr/testify/require"

	"github.com/saicon-network/saicon/chain/x/rewards/types"
)

// The Register's annual security budget (ADR-0011 D1):
//
//	AnnualSecurityBudget = clip(0.15 x TotalBonded,
//	                            0.005  x CurrentSupply,   // floor
//	                            0.0075 x CurrentSupply)   // ceiling
//	EpochTarget = AnnualSecurityBudget / epochsPerYear
//
// The point of the shape: the budget tracks what the network actually secures,
// but never falls below a floor (security cannot be starved when little is
// bonded) nor rises above a ceiling (a stake boom cannot inflate the supply).

// scn converts display SCN to base units for readable fixtures.
func scn(n int64) math.Int { return math.NewInt(n).Mul(math.NewInt(100_000_000)) }

func TestAnnualBudget_StakeTermBindsBetweenFloorAndCeiling(t *testing.T) {
	// Supply 17,375,600 SCN — the Stage 5 snapshot the Register's headline
	// numbers were computed against. Rates are the Register's (v1.0 rates).
	supply := scn(17_375_600)
	// floor   = 0.005  x supply =  86,878 SCN/yr
	// ceiling = 0.0075 x supply = 130,317 SCN/yr
	// the stake term binds while TotalBonded is in [579,187 … 868,780] SCN
	bonded := scn(700_000) // 0.15 x 700,000 = 105,000 SCN/yr — inside the band

	got := types.AnnualSecurityBudget(registerV1Params(), bonded, supply)

	require.True(t, got.Equal(scn(105_000)),
		"the stake term must bind inside the band, got %s", got)
}

// Uses the v1.0 rates (registerV1Params).
func TestAnnualBudget_FloorHoldsWhenLittleIsBonded(t *testing.T) {
	supply := scn(17_375_600)
	bonded := scn(100_000) // 0.15 x 100,000 = 15,000 SCN/yr — far below the floor

	got := types.AnnualSecurityBudget(registerV1Params(), bonded, supply)

	require.True(t, got.Equal(scn(86_878)),
		"security must not be starved when little is bonded, got %s", got)
}

// Uses the v1.0 rates (registerV1Params).
func TestAnnualBudget_CeilingHoldsWhenMuchIsBonded(t *testing.T) {
	supply := scn(17_375_600)
	// At the Register's ~870,000 SCN global bonded cap the ceiling already binds,
	// which is the operating point the headline 357 SCN/day comes from.
	bonded := scn(870_000)

	got := types.AnnualSecurityBudget(registerV1Params(), bonded, supply)

	require.True(t, got.Equal(scn(130_317)),
		"a stake boom must not inflate the supply, got %s", got)
}

// The Register's two headline daily figures must reproduce from the formula.
// If these ever stop matching, either the constants or the Register moved.
// Uses the v1.0 rates (registerV1Params), not the ÷7 defaults.
func TestAnnualBudget_ReproducesTheRegisterHeadlineFigures(t *testing.T) {
	supply := scn(17_375_600)
	p := registerV1Params()

	floorYear := types.AnnualSecurityBudget(p, math.ZeroInt(), supply)
	ceilYear := types.AnnualSecurityBudget(p, scn(10_000_000), supply)

	// 86,878 / 365 = 238.02 SCN/day ; 130,317 / 365 = 357.03 SCN/day
	require.Equal(t, "238", floorYear.Quo(math.NewInt(365)).Quo(math.NewInt(100_000_000)).String(),
		"the Register's medium figure is 238 SCN/day")
	require.Equal(t, "357", ceilYear.Quo(math.NewInt(365)).Quo(math.NewInt(100_000_000)).String(),
		"the Register's max figure is 357 SCN/day")
}

// Uses the v1.0 rates (registerV1Params).
func TestAnnualBudget_ZeroSupplyIsZeroBudget(t *testing.T) {
	// A chain with no supply cannot fund anything; the floor of nothing is
	// nothing. Guards against a genesis-time division producing a phantom budget.
	got := types.AnnualSecurityBudget(registerV1Params(), scn(1_000), math.ZeroInt())
	require.True(t, got.IsZero(), "got %s", got)
}

// --- epoch conversion ------------------------------------------------------

// Epochs are counted in BLOCKS, never wall-clock, so the annual-to-epoch
// conversion must be too.
func TestEpochTarget_DerivesEpochsPerYearFromBlocks(t *testing.T) {
	p := registerV1Params() // blocks_per_year = 6,311,520

	// 100-block epochs (the devnet default) => 63,115 epochs/yr
	got := types.EpochTarget(p, scn(130_317), 100)
	want := scn(130_317).Quo(math.NewInt(63_115))
	require.True(t, got.Equal(want), "expected %s, got %s", want, got)
}

// A longer epoch means fewer of them, so each carries proportionally more.
func TestEpochTarget_LongerEpochsCarryMore(t *testing.T) {
	p := registerV1Params()
	annual := scn(130_317)

	short := types.EpochTarget(p, annual, 100)
	long := types.EpochTarget(p, annual, 17_280) // ~24h at 5s blocks

	require.True(t, long.GT(short), "a day-long epoch must carry more than a 100-block one")
}

// Division guards: neither a zero epoch length nor a zero blocks_per_year may
// panic or produce a nonsense target. Both are reachable through governance.
func TestEpochTarget_ZeroDivisorsYieldZero(t *testing.T) {
	p := registerV1Params()
	require.True(t, types.EpochTarget(p, scn(1_000), 0).IsZero(), "zero epoch length")

	p.BlocksPerYear = 0
	require.True(t, types.EpochTarget(p, scn(1_000), 100).IsZero(), "zero blocks per year")
}

// An epoch longer than a year would make epochs_per_year zero by integer
// division — the target must degrade to zero rather than divide by it.
func TestEpochTarget_EpochLongerThanAYearYieldsZero(t *testing.T) {
	p := registerV1Params()
	require.True(t, types.EpochTarget(p, scn(1_000), p.BlocksPerYear+1).IsZero())
}

// --- params validation -----------------------------------------------------

func TestParams_ValidateRejectsAnInvertedClip(t *testing.T) {
	p := registerV1Params()
	p.BudgetFloorRate = math.LegacyNewDecWithPrec(9, 3)   // 0.009
	p.BudgetCeilingRate = math.LegacyNewDecWithPrec(1, 3) // 0.001 — below the floor

	err := p.Validate()
	require.ErrorContains(t, err, "below budget_floor_rate",
		"an inverted clip would silently emit the floor forever")
}

func TestParams_ValidateRejectsOutOfRangeRates(t *testing.T) {
	for name, mutate := range map[string]func(*types.Params){
		"negative":      func(p *types.Params) { p.BudgetBondedRate = math.LegacyNewDec(-1) },
		"above one":     func(p *types.Params) { p.BudgetBondedRate = math.LegacyNewDec(2) },
		"unset ceiling": func(p *types.Params) { p.BudgetCeilingRate = math.LegacyDec{} },
	} {
		t.Run(name, func(t *testing.T) {
			p := registerV1Params()
			mutate(&p)
			require.Error(t, p.Validate())
		})
	}
}

func TestParams_DefaultsAreTheRegisterRatesDividedBySeven(t *testing.T) {
	p := types.DefaultParams()
	require.NoError(t, p.Validate())
	// S6, accepted 2026-09-24: every clip rate is the Register v1.0 value / 7,
	// computed at LegacyDec precision rather than rounded by hand.
	require.Equal(t, "0.021428571428571428", p.BudgetBondedRate.String())
	require.Equal(t, "0.000714285714285714", p.BudgetFloorRate.String())
	require.Equal(t, "0.001071428571428571", p.BudgetCeilingRate.String())

	v1 := registerV1Params()
	require.Equal(t, "0.150000000000000000", v1.BudgetBondedRate.String())
	require.Equal(t, "0.005000000000000000", v1.BudgetFloorRate.String())
	require.Equal(t, "0.007500000000000000", v1.BudgetCeilingRate.String())
}

// DEVELOPMENT.md states that on the devnet, where the ceiling binds, issuance
// falls from ~205 to ~29 SCN per day. This pins that sentence to the code.
func TestDefaults_DevnetIssuanceIsAboutTwentyNineScnADay(t *testing.T) {
	supply := math.NewInt(10_000_000).MulRaw(100_000_000) // 10M SCN: the devnet genesis
	bonded := math.NewInt(1_000_000).MulRaw(100_000_000)  // 1M SCN self-bonded
	annual := types.AnnualSecurityBudget(types.DefaultParams(), bonded, supply)
	require.Equal(t, math.NewInt(1_071_428_571_428), annual,
		"the ceiling binds: 0.0075/7 × 10M SCN, truncated to nscn")
	perDay := annual.QuoRaw(365)
	require.True(t,
		perDay.GT(math.NewInt(29).MulRaw(100_000_000)) && perDay.LT(math.NewInt(30).MulRaw(100_000_000)),
		"expected between 29 and 30 SCN/day, got %s nscn", perDay)
}
