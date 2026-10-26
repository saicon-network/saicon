package types

import (
	"bytes"
	"sort"

	"cosmossdk.io/math"
)

// Candidate is a validator the router may send overflow to, already filtered
// for eligibility (S7 spec §4.2 step 3) by the keeper.
type Candidate struct {
	Operator      string // valoper bech32
	OperatorBytes []byte
	Score         math.LegacyDec // computed_score of the last closed epoch
	Tokens        math.Int
	Room          math.Int // validator_cap − tokens, > 0
}

// Leg is an amount for one validator.
type Leg struct {
	Validator string
	Amount    math.Int
}

// Weight is quality × remaining capacity × inverse current stake (spec §4.3).
// The S-13 measured comparison, when it lands, replaces this body and nothing else.
func Weight(c Candidate) math.LegacyDec {
	return c.Score.MulInt(c.Room).QuoInt(math.MaxInt(c.Tokens, math.OneInt()))
}

// order sorts by weight descending, then operator bytes ascending.
func order(cs []Candidate) []Candidate {
	out := append([]Candidate(nil), cs...)
	sort.SliceStable(out, func(i, j int) bool {
		wi, wj := Weight(out[i]), Weight(out[j])
		if !wi.Equal(wj) {
			return wi.GT(wj)
		}
		return bytes.Compare(out[i].OperatorBytes, out[j].OperatorBytes) < 0
	})
	return out
}

// Choose picks up to legs candidates by weight. A candidate the delegator
// already holds (held[operator]) is always allowed; at most free others are.
func Choose(cs []Candidate, held map[string]bool, free uint32, legs uint32) []Candidate {
	var out []Candidate
	var added uint32
	for _, c := range order(cs) {
		if uint32(len(out)) >= legs {
			break
		}
		if held[c.Operator] {
			out = append(out, c)
			continue
		}
		if added < free {
			out = append(out, c)
			added++
		}
	}
	return out
}

// Split spreads r over cs (already chosen and ordered by Choose) in proportion
// to weight, each capped by its room; what a capped leg cannot take is re-split
// over the others (water-filling). The truncation remainder goes to the
// heaviest leg that still has room. Returns the legs with a positive amount, in
// the order of cs, and what could not be placed.
func Split(r math.Int, cs []Candidate) ([]Leg, math.Int) {
	alloc := make([]math.Int, len(cs))
	for i := range alloc {
		alloc[i] = math.ZeroInt()
	}
	left := r
	active := make([]int, 0, len(cs))
	for i := range cs {
		active = append(active, i)
	}
	for round := 0; round < len(cs) && left.IsPositive() && len(active) > 0; round++ {
		sum := math.LegacyZeroDec()
		for _, i := range active {
			sum = sum.Add(Weight(cs[i]))
		}
		if !sum.IsPositive() {
			break
		}
		pot := math.LegacyNewDecFromInt(left)
		var next []int
		placed := math.ZeroInt()
		capped := false
		for _, i := range active {
			share := pot.Mul(Weight(cs[i])).Quo(sum).TruncateInt()
			free := cs[i].Room.Sub(alloc[i])
			if share.GTE(free) {
				share = free
				capped = true
			} else {
				next = append(next, i)
			}
			alloc[i] = alloc[i].Add(share)
			placed = placed.Add(share)
		}
		left = left.Sub(placed)
		active = next
		if !capped {
			break // nobody hit their room: only truncation dust is left
		}
	}
	for i := range cs { // dust, heaviest first (cs is in weight order)
		if !left.IsPositive() {
			break
		}
		give := math.MinInt(left, cs[i].Room.Sub(alloc[i]))
		alloc[i] = alloc[i].Add(give)
		left = left.Sub(give)
	}
	var legs []Leg
	for i, a := range alloc {
		if a.IsPositive() {
			legs = append(legs, Leg{Validator: cs[i].Operator, Amount: a})
		}
	}
	return legs, left
}

// SplitAtLeast is Split with a floor on each leg: while some leg is below min
// and more than one candidate remains, the candidate with the smallest leg is
// dropped and r is split again over the rest, so a dust leg does not take a
// validator slot. A single remaining leg is kept even if it is below min (the
// message as a whole met the minimum). If folding would leave more unplaced than
// the plain Split does, the plain legs are returned (dust allowed): folding never
// turns a placeable route into ErrNoRoute. left is what could not be placed.
func SplitAtLeast(r math.Int, cs []Candidate, min math.Int) ([]Leg, math.Int) {
	plainLegs, plainLeft := Split(r, cs)
	legs, left := splitFolding(r, cs, min)
	if left.GT(plainLeft) { // folding must never lose room the plain split had
		return plainLegs, plainLeft
	}
	return legs, left
}

func splitFolding(r math.Int, cs []Candidate, min math.Int) ([]Leg, math.Int) {
	cur := append([]Candidate(nil), cs...)
	for {
		legs, left := Split(r, cur)
		if len(cur) <= 1 {
			return legs, left
		}
		drop, smallest := -1, math.Int{}
		for i, cand := range cur {
			for _, l := range legs {
				if l.Validator != cand.Operator || !l.Amount.LT(min) {
					continue
				}
				// strict "<=": on equal legs the later (lighter) candidate goes
				if drop < 0 || l.Amount.LTE(smallest) {
					drop, smallest = i, l.Amount
				}
			}
		}
		if drop < 0 {
			return legs, left
		}
		cur = append(cur[:drop:drop], cur[drop+1:]...)
	}
}
