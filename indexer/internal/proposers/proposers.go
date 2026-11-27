// Package proposers derives, per closed epoch, each validator's share of the
// blocks it proposed beside its share of consensus voting power. It reads only
// what the store holds and holds no chain access.
package proposers

import (
	"fmt"
	"math/big"
	"sort"
	"strconv"
	"strings"

	"github.com/saicon-network/saicon/indexer/internal/concentration"
	"github.com/saicon-network/saicon/indexer/internal/store"
)

// Reason codes an epoch is unavailable under, besides the codes the concentration
// pass stores for an epoch whose power it could not read (passed through as is).
const (
	// CodePruned: the epoch's per-validator detail is outside the retained window.
	CodePruned = "pruned"
	// CodeBeforeHistory: the follower started after the epoch's first block.
	CodeBeforeHistory = "blocks_before_history"
	// CodeNotIndexed: the follower has not yet walked to the epoch's last block.
	CodeNotIndexed = "blocks_not_indexed"
	// CodeIncomplete: the proposals counted for the epoch are not one per block.
	CodeIncomplete = "blocks_incomplete"
	// CodeEpochLength: the epoch length cannot be derived from the stored row.
	CodeEpochLength = "epoch_length_unknown"
)

// Validator is one validator of an epoch's set. Operator is the operator
// address, or "cons:<ADDR>" when the chain had no validator for the key.
type Validator struct {
	Operator      string  `json:"operator"`
	ConsAddr      string  `json:"cons_addr"`
	Moniker       *string `json:"moniker"`
	Proposed      int64   `json:"proposed"`
	Power         *string `json:"power"`
	ProposedShare string  `json:"proposed_share"`
	PowerShare    *string `json:"power_share"`
	Expected      *string `json:"expected"`
}

// Epoch is one closed epoch. When Unavailable is set the figures that depend on
// it (TotalPower, Validators) are nil: unavailable is a fact, not a row of zeros.
type Epoch struct {
	Epoch       uint64      `json:"epoch"`
	Height      int64       `json:"height"`
	EpochLength *int64      `json:"epoch_length"`
	Blocks      int64       `json:"blocks"`
	TotalPower  *string     `json:"total_power"`
	Unavailable *string     `json:"unavailable"`
	Validators  []Validator `json:"validators"`
}

// Result is the served answer: the per-epoch rows and the window aggregated
// over them.
type Result struct {
	Epochs []Epoch `json:"epochs"`
	Window Window  `json:"window"`
}

// Build turns the stored inputs into the served epochs, oldest first, and
// aggregates the window over them. A validator's operator address and moniker,
// when known in any epoch of the series, name it in every epoch: a key the
// chain's staking state did not resolve at one height (pruned, or the validator
// left the set) is still the same validator.
func Build(in *store.ProposerInputs) Result {
	out := make([]Epoch, 0, len(in.Rows))
	for _, row := range in.Rows {
		out = append(out, one(in, row))
	}
	identify(out)
	return Result{Epochs: out, Window: window(out)}
}

// identify gives every row of a consensus address the operator and moniker the
// newest epoch that knows them carries.
func identify(eps []Epoch) {
	op := map[string]string{}
	mon := map[string]*string{}
	for _, e := range eps { // oldest first, so the newest knowledge wins
		for _, v := range e.Validators {
			if !strings.HasPrefix(v.Operator, "cons:") {
				op[v.ConsAddr] = v.Operator
			}
			if v.Moniker != nil {
				mon[v.ConsAddr] = v.Moniker
			}
		}
	}
	for i := range eps {
		for j := range eps[i].Validators {
			v := &eps[i].Validators[j]
			if o, ok := op[v.ConsAddr]; ok {
				v.Operator = o
			}
			if v.Moniker == nil {
				v.Moniker = mon[v.ConsAddr]
			}
		}
	}
}

func code(c string) *string { return &c }

func one(in *store.ProposerInputs, row store.ConcentrationRow) Epoch {
	e := Epoch{Epoch: row.Epoch, Height: row.Height}
	counts := in.Counts[row.Epoch]
	for _, n := range counts {
		e.Blocks += n
	}

	// Epoch e covers heights (e-1)·L+1 .. e·L, and its measurement height is e·L,
	// so L is recovered from the row itself.
	if row.Epoch == 0 || row.Height <= 0 || row.Height%int64(row.Epoch) != 0 {
		e.Unavailable = code(CodeEpochLength)
		return e
	}
	L := row.Height / int64(row.Epoch)
	e.EpochLength = &L
	first := (int64(row.Epoch)-1)*L + 1

	switch {
	case row.PowerUnavailable != nil:
		e.Unavailable = code(*row.PowerUnavailable)
	case first < in.FirstWalked:
		e.Unavailable = code(CodeBeforeHistory)
	case row.Height > in.Indexed:
		e.Unavailable = code(CodeNotIndexed)
	case e.Blocks != L:
		e.Unavailable = code(CodeIncomplete)
	case len(in.Powers[row.Epoch]) == 0 || row.TotalPower == nil || *row.TotalPower <= 0:
		e.Unavailable = code(CodePruned)
	}
	if e.Unavailable != nil {
		return e
	}

	total := *row.TotalPower
	ts := strconv.FormatInt(total, 10)
	e.TotalPower = &ts
	bigTotal, bigBlocks := big.NewInt(total), big.NewInt(e.Blocks)

	seen := map[string]bool{}
	e.Validators = make([]Validator, 0, len(in.Powers[row.Epoch]))
	for _, p := range in.Powers[row.Epoch] {
		seen[p.ConsAddr] = true
		proposed := counts[p.ConsAddr]
		v := Validator{
			Operator: "cons:" + p.ConsAddr, ConsAddr: p.ConsAddr, Moniker: p.Moniker, Proposed: proposed,
			ProposedShare: concentration.Share(proposed, e.Blocks),
		}
		if p.Operator != nil {
			v.Operator = *p.Operator
		}
		ps := strconv.FormatInt(p.Power, 10)
		v.Power = &ps
		if sh := concentration.Share(p.Power, total); sh != "" {
			v.PowerShare = &sh
		}
		// expected = power·blocks/total, truncated to two decimals.
		num := new(big.Int).Mul(big.NewInt(p.Power), bigBlocks)
		exp := new(big.Int).Quo(new(big.Int).Mul(num, big.NewInt(100)), bigTotal)
		es := fmt.Sprintf("%d.%02d", new(big.Int).Quo(exp, big.NewInt(100)).Int64(), new(big.Int).Rem(exp, big.NewInt(100)).Int64())
		v.Expected = &es
		e.Validators = append(e.Validators, v)
	}

	// A validator that proposed in the epoch but is not in the set at its last
	// block (it left mid-epoch) has proposals and no power at that snapshot:
	// its power figures are null, not zero.
	var gone []Validator
	for cons, n := range counts {
		if seen[cons] {
			continue
		}
		gone = append(gone, Validator{
			Operator: "cons:" + cons, ConsAddr: cons, Proposed: n,
			ProposedShare: concentration.Share(n, e.Blocks),
		})
	}
	sort.Slice(gone, func(i, j int) bool {
		if gone[i].Proposed != gone[j].Proposed {
			return gone[i].Proposed > gone[j].Proposed
		}
		return gone[i].ConsAddr < gone[j].ConsAddr
	})
	e.Validators = append(e.Validators, gone...)
	return e
}
