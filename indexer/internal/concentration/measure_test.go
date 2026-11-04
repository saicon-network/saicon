package concentration

import (
	"math"
	"testing"

	"github.com/stretchr/testify/require"
)

func h(addr string, p int64, unit string) Holder {
	return Holder{ConsAddr: addr, Operator: "op" + addr, Power: p, Unit: unit}
}

func TestTop10AndNakamotoOnNonRoundPowers(t *testing.T) {
	// 12 holders, total 1,000,003: the ten largest hold 999,991.
	hs := []Holder{h("A", 333_334, "val:A"), h("B", 200_001, "val:B"), h("C", 150_000, "val:C"),
		h("D", 100_000, "val:D"), h("E", 80_000, "val:E"), h("F", 60_000, "val:F"), h("G", 40_000, "val:G"),
		h("H", 20_000, "val:H"), h("I", 10_000, "val:I"), h("J", 6_656, "val:J"), h("K", 7, "val:K"), h("L", 5, "val:L")}
	m, err := Measure(hs)
	require.NoError(t, err)
	require.Equal(t, 12, m.SetSize)
	require.Equal(t, int64(1_000_003), m.TotalPower)
	require.Equal(t, int64(999_991), m.Top10Power)
	require.Equal(t, 2, m.NakamotoValidators) // 3·333,334 = 1,000,002 < 1,000,003; with B it holds
	require.Equal(t, "0.999988", Share(m.Top10Power, m.TotalPower))
	require.True(t, m.HasUnits)
	require.Equal(t, 12, m.UnitCount)
	require.Equal(t, 2, m.NakamotoEntities)
}

func TestNakamotoCountsExactlyOneThird(t *testing.T) {
	m, err := Measure([]Holder{h("A", 1, "val:A"), h("B", 1, "val:B"), h("C", 1, "val:C")})
	require.NoError(t, err)
	require.Equal(t, 1, m.NakamotoValidators) // 3·1 = 3 = T

	m, err = Measure([]Holder{h("A", 100, "val:A"), h("B", 100, "val:B"), h("C", 100, "val:C"), h("D", 1, "val:D")})
	require.NoError(t, err)
	require.Equal(t, 2, m.NakamotoValidators) // 3·100 = 300 < 301; 3·200 ≥ 301
}

func TestNakamotoOnTotalNearInt64Max(t *testing.T) {
	// 3·cum would overflow int64 here; the comparison must stay exact.
	const third = math.MaxInt64 / 3
	m, err := Measure([]Holder{h("A", third, "val:A"), h("B", third, "val:B"), h("C", math.MaxInt64-2*third, "val:C")})
	require.NoError(t, err)
	require.Equal(t, int64(math.MaxInt64), m.TotalPower)
	// MaxInt64 = 3·third + 1, so the largest holder (third + 1) is at least one third.
	require.Equal(t, 1, m.NakamotoValidators)

	// Two holders of floor(T/3) each: 3·third < T, so one is not enough, two are.
	m, err = Measure([]Holder{h("A", third, "val:A"), h("B", third, "val:B"), h("C", third, "val:C"), h("D", 1, "val:D")})
	require.NoError(t, err)
	require.Equal(t, 2, m.NakamotoValidators)
}

func TestSumOverflowIsAnError(t *testing.T) {
	_, err := Measure([]Holder{h("A", math.MaxInt64, "val:A"), h("B", 1, "val:B")})
	require.Error(t, err)
}

func TestTenOrFewerHoldEverything(t *testing.T) {
	m, err := Measure([]Holder{h("A", 7, "entity:x")})
	require.NoError(t, err)
	require.Equal(t, m.TotalPower, m.Top10Power)
	require.Equal(t, "1.000000", Share(m.Top10Power, m.TotalPower))
	require.Equal(t, 1, m.NakamotoValidators)
	require.Equal(t, 1, m.NakamotoEntities)
	require.Equal(t, 1, m.UnitCount)

	ten := make([]Holder, 10)
	for i := range ten {
		ten[i] = h(string(rune('A'+i)), 1, "")
	}
	m, err = Measure(ten)
	require.NoError(t, err)
	require.Equal(t, int64(10), m.Top10Power)
	require.Equal(t, "1.000000", Share(m.Top10Power, m.TotalPower))

	eleven := append(ten, h("K", 1, ""))
	m, err = Measure(eleven)
	require.NoError(t, err)
	require.Equal(t, int64(10), m.Top10Power)
	require.Equal(t, "0.909090", Share(m.Top10Power, m.TotalPower))
}

func TestRankedBreaksTiesByAddress(t *testing.T) {
	a, b, c := Holder{ConsAddr: "0A", Power: 5}, Holder{ConsAddr: "0B", Power: 7}, Holder{ConsAddr: "0C", Power: 5}
	want := []string{"0B", "0A", "0C"}
	for _, in := range [][]Holder{{c, a, b}, {a, c, b}, {b, c, a}, {c, b, a}} {
		before := append([]Holder(nil), in...)
		got := Ranked(in)
		require.Equal(t, want, []string{got[0].ConsAddr, got[1].ConsAddr, got[2].ConsAddr})
		require.Equal(t, before, in, "the input slice is unchanged")
	}
}

func TestEntityGroupingMergesMembers(t *testing.T) {
	hs := []Holder{h("A", 500, "entity:x"), h("B", 300, "entity:x"), h("C", 400, "val:C")}
	m, err := Measure(hs)
	require.NoError(t, err)
	require.True(t, m.HasUnits)
	require.Equal(t, 2, m.UnitCount)
	require.Equal(t, 1, m.NakamotoEntities)   // 800·3 ≥ 1,200
	require.Equal(t, 1, m.NakamotoValidators) // 500·3 ≥ 1,200
	v, e := Curve(hs)
	require.Equal(t, []string{"0.666666", "1.000000"}, e)
	require.Equal(t, []string{"0.416666", "0.750000", "1.000000"}, v)
}

func TestNakamotoEntitiesDifferFromValidators(t *testing.T) {
	// T = 100, need = 34. Validators: 30 < 34, 30+30 = 60 -> 2. Units: entity:x = 60 -> 1.
	hs := []Holder{h("A", 30, "entity:x"), h("B", 30, "entity:x"), h("C", 30, "val:C"), h("D", 10, "val:D")}
	m, err := Measure(hs)
	require.NoError(t, err)
	require.Equal(t, 2, m.NakamotoValidators)
	require.Equal(t, 1, m.NakamotoEntities)
	require.Equal(t, 3, m.UnitCount)
}

func TestEntityUnitsTieBreakByKey(t *testing.T) {
	// Equal unit power: the unit key decides the order, so the curve and Nakamoto are stable.
	// Units: b = 3, a = 3, c = 4 (T = 10). Order c, a, b: cum 4, 7, 10.
	hs := []Holder{h("1", 3, "entity:b"), h("2", 3, "entity:a"), h("3", 4, "entity:c")}
	m, err := Measure(hs)
	require.NoError(t, err)
	require.Equal(t, 3, m.UnitCount)
	require.Equal(t, 1, m.NakamotoEntities) // 3·4 = 12 ≥ 10
	_, e := Curve(hs)
	require.Equal(t, []string{"0.400000", "0.700000", "1.000000"}, e)
}

func TestChargedFrozenSeatOfARevokedEntityGroupsUnderIt(t *testing.T) {
	require.Equal(t, "entity:demo-third", UnitOf("opT", "T", map[string]string{}, map[string]string{"opT": "demo-third"}))
	require.Equal(t, "entity:acme", UnitOf("opT", "T",
		map[string]string{"opT": "acme"}, map[string]string{"opT": "demo-third"}))
	require.Equal(t, "val:opT", UnitOf("opT", "T", nil, nil))
	require.Equal(t, "cons:T", UnitOf("", "T", map[string]string{"": "x"}, map[string]string{"": "y"}))
}

func TestUnitsUnavailable(t *testing.T) {
	hs := []Holder{h("A", 500, "entity:x"), h("B", 300, ""), h("C", 400, "val:C")}
	m, err := Measure(hs)
	require.NoError(t, err)
	require.False(t, m.HasUnits)
	require.Equal(t, 0, m.UnitCount)
	require.Equal(t, 0, m.NakamotoEntities)
	require.Equal(t, 1, m.NakamotoValidators)
	v, e := Curve(hs)
	require.Nil(t, e)
	require.Len(t, v, 3)
}

func TestEmptySetIsAnError(t *testing.T) {
	_, err := Measure(nil)
	require.Error(t, err)
	_, err = Measure([]Holder{h("A", 0, "val:A")})
	require.Error(t, err)
	_, err = Measure([]Holder{h("A", -1, "val:A"), h("B", 5, "val:B")})
	require.Error(t, err)

	v, e := Curve(nil)
	require.Empty(t, v)
	require.Empty(t, e)
	v, e = Curve([]Holder{h("A", 0, "val:A")})
	require.Empty(t, v)
	require.Empty(t, e)
	require.Empty(t, Ranked(nil))
}

func TestShareNeverOverflows(t *testing.T) {
	require.Equal(t, "0.999999", Share(1<<60, 1<<60+1))
	require.Equal(t, "1.000000", Share(math.MaxInt64, math.MaxInt64))
	require.Equal(t, "0.000000", Share(0, 5))
	require.Equal(t, "", Share(1, 0))
	require.Equal(t, "", Share(-1, 5))
	require.Equal(t, "", Share(6, 5))
}
