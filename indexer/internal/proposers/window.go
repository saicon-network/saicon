package proposers

import (
	"fmt"
	"math/big"
	"sort"

	"github.com/saicon-network/saicon/indexer/internal/concentration"
)

// WindowValidator is one validator over the window. Shares are 6-decimal
// truncated strings, Ratio is truncated to 3 decimals.
//
//   - ProposedShare = Σ proposed / Σ blocks over every comparable epoch.
//   - PowerShare = mean over every comparable epoch of power/T at the epoch's
//     last block, a validator outside the set at that block counting 0.
//   - Ratio = Σ proposed / Σ expected, over the epochs the validator was in the
//     set at the epoch's last block only: a check that proposal selection follows
//     voting power, not a measure of how that power moves. Null when the
//     validator was in the set for none of them.
type WindowValidator struct {
	Operator      string  `json:"operator"`
	ConsAddr      string  `json:"cons_addr"`
	Moniker       *string `json:"moniker"`
	Proposed      int64   `json:"proposed"`
	ProposedShare string  `json:"proposed_share"`
	PowerShare    string  `json:"power_share"`
	InSetEpochs   int     `json:"in_set_epochs"`
	Ratio         *string `json:"ratio"`
}

// Window is the aggregate over the comparable epochs of the answer.
type Window struct {
	// From and To are the first and last comparable epoch, null when none is.
	From       *uint64           `json:"from"`
	To         *uint64           `json:"to"`
	Epochs     int               `json:"epochs"`
	Blocks     int64             `json:"blocks"`
	Validators []WindowValidator `json:"validators"`
}

type agg struct {
	v                  WindowValidator
	proposed           int64
	powerSum, expected *big.Rat // powerSum: Σ power/T; expected: Σ power·blocks/T (in-set epochs)
	proposedInSet      int64
}

// truncRat renders r >= 0 truncated (never rounded) to d decimals.
func truncRat(r *big.Rat, d int) string {
	scale := new(big.Int).Exp(big.NewInt(10), big.NewInt(int64(d)), nil)
	q := new(big.Int).Mul(r.Num(), scale)
	q.Quo(q, r.Denom())
	whole, frac := new(big.Int).QuoRem(q, scale, new(big.Int))
	return fmt.Sprintf("%s.%0*s", whole.String(), d, frac.String())
}

func window(eps []Epoch) Window {
	w := Window{Validators: []WindowValidator{}}
	byCons := map[string]*agg{}
	for _, e := range eps {
		if e.Unavailable != nil || e.Validators == nil || e.TotalPower == nil {
			continue
		}
		if w.From == nil {
			n := e.Epoch
			w.From = &n
		}
		n := e.Epoch
		w.To = &n
		w.Epochs++
		w.Blocks += e.Blocks
		total, _ := new(big.Int).SetString(*e.TotalPower, 10)
		for _, v := range e.Validators {
			a := byCons[v.ConsAddr]
			if a == nil {
				a = &agg{powerSum: new(big.Rat), expected: new(big.Rat)}
				a.v = WindowValidator{ConsAddr: v.ConsAddr}
				byCons[v.ConsAddr] = a
			}
			// Newest epoch's naming wins (epochs are oldest first).
			a.v.Operator, a.v.Moniker = v.Operator, v.Moniker
			a.proposed += v.Proposed
			if v.Power == nil {
				continue // proposed but outside the set at the last block: no power to weigh
			}
			p, _ := new(big.Int).SetString(*v.Power, 10)
			a.v.InSetEpochs++
			a.proposedInSet += v.Proposed
			a.powerSum.Add(a.powerSum, new(big.Rat).SetFrac(p, total))
			a.expected.Add(a.expected, new(big.Rat).SetFrac(new(big.Int).Mul(p, big.NewInt(e.Blocks)), total))
		}
	}
	if w.Epochs == 0 {
		return w
	}
	list := make([]*agg, 0, len(byCons))
	for _, a := range byCons {
		a.v.Proposed = a.proposed
		a.v.ProposedShare = concentration.Share(a.proposed, w.Blocks)
		mean := new(big.Rat).Quo(a.powerSum, new(big.Rat).SetInt64(int64(w.Epochs)))
		a.v.PowerShare = truncRat(mean, 6)
		if a.expected.Sign() > 0 {
			r := new(big.Rat).Quo(new(big.Rat).SetInt64(a.proposedInSet), a.expected)
			s := truncRat(r, 3)
			a.v.Ratio = &s
		}
		list = append(list, a)
	}
	sort.Slice(list, func(i, j int) bool {
		if c := list[i].powerSum.Cmp(list[j].powerSum); c != 0 {
			return c > 0
		}
		if list[i].proposed != list[j].proposed {
			return list[i].proposed > list[j].proposed
		}
		return list[i].v.ConsAddr < list[j].v.ConsAddr
	})
	for _, a := range list {
		w.Validators = append(w.Validators, a.v)
	}
	return w
}
