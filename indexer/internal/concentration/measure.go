// Package concentration holds the pure measures of the decentralization
// dashboard: top-10 voting-power share, the Nakamoto coefficient by validator
// and by attested entity, and the cumulative concentration curves. Everything
// is exact integer arithmetic; shares are 6-decimal strings, truncated.
package concentration

import (
	"errors"
	"fmt"
	"math"
	"math/big"
	"sort"
)

// Holder is one member of the consensus set at the measured height.
type Holder struct {
	ConsAddr string // upper-case hex
	Operator string // "" when x/staking had no validator with this key
	Power    int64
	Unit     string // "entity:<id>" | "val:<operator>" | "cons:<ADDR>"; "" when the entity part is unavailable
}

// Measures are the per-epoch figures of spec §3.
type Measures struct {
	SetSize            int
	TotalPower         int64
	Top10Power         int64
	NakamotoValidators int
	HasUnits           bool // false when any Holder.Unit is ""
	UnitCount          int  // 0 unless HasUnits
	NakamotoEntities   int  // 0 unless HasUnits
}

// topN is the number of largest holders the top-10 figure sums.
const topN = 10

// Ranked returns a sorted copy of holders: power descending, then ConsAddr
// ascending. It is the one ordering every other function here (and the
// validators list the API serves) uses, so equal powers never reorder.
func Ranked(holders []Holder) []Holder {
	out := append([]Holder(nil), holders...)
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].Power != out[j].Power {
			return out[i].Power > out[j].Power
		}
		return out[i].ConsAddr < out[j].ConsAddr
	})
	return out
}

// Share returns num/den as a decimal string with six fractional digits,
// truncated (never rounded up across a line). It is defined only for
// 0 <= num <= den and den > 0; any other input returns "" rather than a
// figure. The arithmetic is done in big integers, so no pair of int64 values
// overflows.
func Share(num, den int64) string {
	if den <= 0 || num < 0 || num > den {
		return ""
	}
	q := new(big.Int).Mul(big.NewInt(num), big.NewInt(1_000_000))
	q.Quo(q, big.NewInt(den))
	m := q.Int64() // num <= den, so the quotient is <= 10^6
	return fmt.Sprintf("%d.%06d", m/1_000_000, m%1_000_000)
}

// UnitOf names the unit a validator is counted under: the entity it is a
// member of, else the entity a frozen seat of it is charged to, else the
// validator itself, else the bare consensus address. Both maps are keyed by
// operator address.
func UnitOf(operator, consAddr string, memberOf, chargedTo map[string]string) string {
	if operator != "" {
		if id, ok := memberOf[operator]; ok {
			return "entity:" + id
		}
		if id, ok := chargedTo[operator]; ok {
			return "entity:" + id
		}
		return "val:" + operator
	}
	return "cons:" + consAddr
}

// Measure computes the per-epoch figures. It errors on an empty set, a
// negative power, a total of zero, or a total that does not fit int64.
func Measure(holders []Holder) (Measures, error) {
	total, err := totalPower(holders)
	if err != nil {
		return Measures{}, err
	}
	ranked := Ranked(holders)
	m := Measures{SetSize: len(ranked), TotalPower: total}
	for i, hd := range ranked {
		if i < topN {
			m.Top10Power += hd.Power
		}
	}
	powers := make([]int64, len(ranked))
	for i, hd := range ranked {
		powers[i] = hd.Power
	}
	m.NakamotoValidators = nakamoto(powers, total)
	if units, ok := rankUnits(ranked); ok {
		m.HasUnits = true
		m.UnitCount = len(units)
		m.NakamotoEntities = nakamoto(units, total)
	}
	return m, nil
}

// Curve returns the cumulative share of the k largest holders for k = 1..N
// (validators) and of the k largest units for k = 1..M (entities), each a
// 6-decimal truncated string. entities is nil unless every Unit is set. An
// empty set, or one with no power, yields nil, nil.
func Curve(holders []Holder) (validators, entities []string) {
	total, err := totalPower(holders)
	if err != nil {
		return nil, nil
	}
	ranked := Ranked(holders)
	powers := make([]int64, len(ranked))
	for i, hd := range ranked {
		powers[i] = hd.Power
	}
	validators = cumulative(powers, total)
	if units, ok := rankUnits(ranked); ok {
		entities = cumulative(units, total)
	}
	return validators, entities
}

// totalPower sums the powers, rejecting what no consensus set can be.
func totalPower(holders []Holder) (int64, error) {
	if len(holders) == 0 {
		return 0, errors.New("concentration: empty consensus set")
	}
	var total int64
	for _, hd := range holders {
		if hd.Power < 0 {
			return 0, fmt.Errorf("concentration: negative power for %s", hd.ConsAddr)
		}
		if hd.Power > math.MaxInt64-total {
			return 0, errors.New("concentration: total power overflows int64")
		}
		total += hd.Power
	}
	if total == 0 {
		return 0, errors.New("concentration: total power is zero")
	}
	return total, nil
}

// rankUnits sums power per unit and returns the sums ordered by power
// descending, then unit key ascending. ok is false when any holder has no
// unit, in which case there is no entity view.
func rankUnits(ranked []Holder) (powers []int64, ok bool) {
	sums := make(map[string]int64, len(ranked))
	for _, hd := range ranked {
		if hd.Unit == "" {
			return nil, false
		}
		sums[hd.Unit] += hd.Power
	}
	keys := make([]string, 0, len(sums))
	for k := range sums {
		keys = append(keys, k)
	}
	sort.Slice(keys, func(i, j int) bool {
		if sums[keys[i]] != sums[keys[j]] {
			return sums[keys[i]] > sums[keys[j]]
		}
		return keys[i] < keys[j]
	})
	powers = make([]int64, len(keys))
	for i, k := range keys {
		powers[i] = sums[k]
	}
	return powers, true
}

// nakamoto is the smallest k with 3·(sum of the k largest) >= total, over
// powers already sorted descending. The test is written as cum >= ceil(T/3)
// so it cannot overflow however large the total is.
func nakamoto(powers []int64, total int64) int {
	need := total / 3
	if total%3 != 0 {
		need++
	}
	var cum int64
	for i, p := range powers {
		cum += p
		if cum >= need {
			return i + 1
		}
	}
	return len(powers)
}

// cumulative renders the running share of powers (sorted descending) of total.
func cumulative(powers []int64, total int64) []string {
	out := make([]string, len(powers))
	var cum int64
	for i, p := range powers {
		cum += p
		out[i] = Share(cum, total)
	}
	return out
}
