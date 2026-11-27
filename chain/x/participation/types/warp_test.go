package types_test

import (
	"testing"

	"cosmossdk.io/math"
	"github.com/stretchr/testify/require"

	"github.com/saicon-network/saicon/chain/x/participation/types"
)

func dec(s string) math.LegacyDec { return math.LegacyMustNewDecFromStr(s) }

// next applies one epoch with the default fall and rise.
func next(warp, ps math.LegacyDec) math.LegacyDec {
	return types.NextWarp(warp, ps, types.DefaultWarpFall(), types.DefaultWarpRise())
}

// The rule of ADR-0009 §2, one branch at a time.
func TestNextWarp_FallsFastBelowAndRisesSlowlyAbove(t *testing.T) {
	// PS < WARP: WARP - 0.65·(WARP - PS)
	require.Equal(t, dec("64"), next(dec("90"), dec("50")))
	// PS > WARP: WARP + 0.10·(PS - WARP)
	require.Equal(t, dec("82"), next(dec("80"), dec("100")))
	// PS = WARP: both branches leave WARP where it is, so this pins the
	// outcome, not which branch ran. TestNextWarp_BranchBoundary pins that.
	require.Equal(t, dec("70"), next(dec("70"), dec("70")))
	// The asymmetry is the decision: the same gap costs more on the way down
	// than it earns on the way up.
	down := dec("80").Sub(next(dec("80"), dec("60")))
	up := next(dec("60"), dec("80")).Sub(dec("60"))
	require.True(t, down.GT(up), "fall %s must exceed rise %s", down, up)
}

// The branch boundary, one tick (1e-18) either side of PS = WARP, where the
// two formulas disagree. Below, the fall branch moves WARP down by
// round(0.65 tick) = 1 tick, where the rise formula would leave it; above, the
// rise branch leaves it (round(0.10 tick) = 0), where the fall formula would
// move it up a tick. So "PS < WARP falls, PS >= WARP rises" is pinned to the
// tick.
func TestNextWarp_BranchBoundary(t *testing.T) {
	w := dec("70")
	tick := math.LegacySmallestDec()
	fall, rise := types.DefaultWarpFall(), types.DefaultWarpRise()
	fallRule := func(ps math.LegacyDec) math.LegacyDec { return w.Sub(fall.Mul(w.Sub(ps))) }
	riseRule := func(ps math.LegacyDec) math.LegacyDec { return w.Add(rise.Mul(ps.Sub(w))) }

	below := w.Sub(tick)
	got := next(w, below)
	require.True(t, got.Equal(dec("69.999999999999999999")), "PS = WARP - 1e-18: %s", got)
	require.True(t, got.Equal(fallRule(below)))
	require.False(t, got.Equal(riseRule(below)), "the rise formula would give %s", riseRule(below))

	above := w.Add(tick)
	got = next(w, above)
	require.True(t, got.Equal(dec("70")), "PS = WARP + 1e-18: %s", got)
	require.True(t, got.Equal(riseRule(above)))
	require.False(t, got.Equal(fallRule(above)), "the fall formula would give %s", fallRule(above))
}

// The result never leaves [0, 100], whatever the input: the clamp is a
// property of the code, not of the parameters.
func TestNextWarp_Clamps(t *testing.T) {
	require.Equal(t, dec("100"), next(dec("100"), dec("150")))
	require.Equal(t, dec("100"), types.NextWarp(dec("90"), dec("200"), dec("0.65"), dec("1")))
	require.Equal(t, dec("0"), types.NextWarp(dec("10"), dec("-50"), dec("1"), dec("0.1")))
	require.Equal(t, dec("0"), next(dec("0"), dec("0")))
	require.Equal(t, dec("100"), types.ClampWarp(dec("100.000000000000000001")))
	require.Equal(t, dec("0"), types.ClampWarp(dec("-0.000000000000000001")))
	require.Equal(t, dec("42.5"), types.ClampWarp(dec("42.5")))
}

// R1: the epoch's computed_score (uptime on 0-1) times 100.
func TestWarpInput_ScalesTheScoreToZeroToHundred(t *testing.T) {
	require.Equal(t, dec("100"), types.WarpInput(math.LegacyOneDec()))
	require.Equal(t, dec("90"), types.WarpInput(dec("0.9")))
	require.Equal(t, dec("0"), types.WarpInput(math.LegacyZeroDec()))
}

// epochsToCross counts the epochs at PS = 100 that take w to the threshold.
func epochsToCross(w math.LegacyDec) int {
	n := 0
	for !types.WarpEligible(w, types.DefaultWarpThreshold()) {
		w = next(w, dec("100"))
		n++
	}
	return n
}

// ADR-0009's worked numbers, recomputed by the engine itself.
func TestNextWarp_ADR0009WorkedNumbers(t *testing.T) {
	// "From WARP = 90, one epoch at PS = 50 gives 90 − 0.65·40 = 64 — below the
	// eligibility threshold immediately — and 13 epochs of perfect participation
	// to return to 90."
	w := next(dec("90"), dec("50"))
	require.Equal(t, dec("64"), w)
	require.False(t, types.WarpEligible(w, types.DefaultWarpThreshold()))
	n := 0
	for w.LT(dec("90")) {
		w = next(w, dec("100"))
		n++
	}
	require.Equal(t, 13, n)

	// "Three consecutive epochs at PS = 0 take WARP from 80/95/100 down to
	// 3.4 / 4.1 / 4.3, and exactly 10 epochs at PS = 100 are then needed to cross
	// 65 again."
	for _, c := range []struct{ from, after string }{
		{"80", "3.43"}, {"95", "4.073125"}, {"100", "4.2875"},
	} {
		w := dec(c.from)
		for range 3 {
			w = next(w, dec("0"))
		}
		require.Equal(t, dec(c.after), w, "three dead epochs from %s", c.from)
		require.Equal(t, 10, epochsToCross(w), "recovery from %s", c.from)
	}

	// "Starting at 80, a single dead first epoch drops WARP to 28 and costs 7
	// clean epochs before first eligibility."
	w = next(types.DefaultWarpInitial(), dec("0"))
	require.Equal(t, dec("28"), w)
	require.Equal(t, 7, epochsToCross(w))
}

func TestWarpEligible_IsAtOrAboveTheThreshold(t *testing.T) {
	require.True(t, types.WarpEligible(dec("65"), dec("65")))
	require.True(t, types.WarpEligible(dec("100"), dec("65")))
	require.False(t, types.WarpEligible(dec("64.999999999999999999"), dec("65")))
}

func TestParams_DefaultsCarryTheWarpBaseline(t *testing.T) {
	p := types.DefaultParams()
	require.Equal(t, dec("0.65"), p.WarpFall)
	require.Equal(t, dec("0.10"), p.WarpRise)
	require.Equal(t, dec("80"), p.WarpInitial)
	require.Equal(t, dec("65"), p.WarpThreshold)
	require.NoError(t, p.Validate())
	// NewParams keeps the old two-argument form and fills WARP with defaults.
	require.Equal(t, p, types.NewParams(p.EpochLengthBlocks, p.EpDecay))
}

// R8: 0 < rise < fall <= 1 and 0 <= threshold <= initial <= 100.
func TestParams_ValidateWarpBounds(t *testing.T) {
	with := func(f func(p *types.Params)) types.Params {
		p := types.DefaultParams()
		f(&p)
		return p
	}
	for _, c := range []struct {
		name string
		p    types.Params
		err  string
	}{
		{"fall at 1", with(func(p *types.Params) { p.WarpFall = dec("1") }), ""},
		{"threshold 0", with(func(p *types.Params) { p.WarpThreshold = dec("0") }), ""},
		{"initial 100", with(func(p *types.Params) { p.WarpInitial = dec("100") }), ""},
		{"threshold equal to initial", with(func(p *types.Params) { p.WarpThreshold = dec("80") }), ""},
		{"rise just under fall", with(func(p *types.Params) { p.WarpRise = dec("0.649999999999999999") }), ""},

		{"nil fall", with(func(p *types.Params) { p.WarpFall = math.LegacyDec{} }), "warp_fall must not be nil"},
		{"nil rise", with(func(p *types.Params) { p.WarpRise = math.LegacyDec{} }), "warp_rise must not be nil"},
		{"nil initial", with(func(p *types.Params) { p.WarpInitial = math.LegacyDec{} }), "warp_initial must not be nil"},
		{"nil threshold", with(func(p *types.Params) { p.WarpThreshold = math.LegacyDec{} }), "warp_threshold must not be nil"},
		{"rise zero", with(func(p *types.Params) { p.WarpRise = dec("0") }), "0 < warp_rise < warp_fall <= 1"},
		{"rise negative", with(func(p *types.Params) { p.WarpRise = dec("-0.1") }), "0 < warp_rise < warp_fall <= 1"},
		{"rise equals fall (symmetric)", with(func(p *types.Params) { p.WarpRise = dec("0.65") }), "0 < warp_rise < warp_fall <= 1"},
		{"rise above fall", with(func(p *types.Params) { p.WarpRise = dec("0.8"); p.WarpFall = dec("0.2") }), "0 < warp_rise < warp_fall <= 1"},
		{"fall above 1", with(func(p *types.Params) { p.WarpFall = dec("1.000000000000000001") }), "0 < warp_rise < warp_fall <= 1"},
		{"threshold negative", with(func(p *types.Params) { p.WarpThreshold = dec("-1") }), "0 <= warp_threshold <= warp_initial <= 100"},
		{"threshold above initial", with(func(p *types.Params) { p.WarpThreshold = dec("81") }), "0 <= warp_threshold <= warp_initial <= 100"},
		{"initial above 100", with(func(p *types.Params) { p.WarpInitial = dec("101") }), "0 <= warp_threshold <= warp_initial <= 100"},
	} {
		t.Run(c.name, func(t *testing.T) {
			err := c.p.Validate()
			if c.err == "" {
				require.NoError(t, err)
				return
			}
			require.ErrorContains(t, err, c.err)
		})
	}
}

// WarpParams.Validate is the gate the epoch step and the voting-power preview
// share. Params stored from a genesis without WARP fields read back as zeros,
// not nils: Validate refuses those as well as nils.
func TestWarpParams_Validate(t *testing.T) {
	require.NoError(t, types.DefaultParams().Warp().Validate())
	require.ErrorContains(t, types.WarpParams{}.Validate(), "warp_fall must not be nil")
	zero := math.LegacyZeroDec()
	require.ErrorContains(t, types.WarpParams{Fall: zero, Rise: zero, Initial: zero, Threshold: zero}.Validate(),
		"0 < warp_rise < warp_fall <= 1")
	p := types.DefaultParams()
	p.WarpThreshold = dec("81")
	require.ErrorContains(t, p.Warp().Validate(), "0 <= warp_threshold <= warp_initial <= 100")
	require.Equal(t, p.ValidateWarp(), p.Warp().Validate(), "Params.ValidateWarp is the same check")
}
