package store

import (
	"context"
	"fmt"
	"sort"
)

// ProposerInputs is everything the proposer-frequency view is derived from, as
// stored: nothing here was read from the chain for the request.
type ProposerInputs struct {
	// Rows are the concentration rows of the selected epochs, oldest first.
	Rows []ConcentrationRow
	// FirstWalked and Indexed bound the heights the follower counted: the lowest
	// height it walked from and the cursor.
	FirstWalked, Indexed int64
	// Powers holds each epoch's consensus set at the epoch's last block, in the
	// order Concentration serves it. An epoch whose detail was pruned has no entry.
	Powers map[uint64][]PowerRow
	// Counts holds the blocks each consensus address proposed, per epoch.
	Counts map[uint64]map[string]int64
}

// ProposerMaxEpochs caps one proposer-frequency answer: every epoch carries the
// whole validator set, so the payload is far larger per epoch than the other
// series endpoints.
const ProposerMaxEpochs = 200

// ProposerSeries selects closed epochs that have a concentration row, in
// [from, to]. With newest > 0 it takes the newest `newest` of them, otherwise the
// oldest, in both cases capped at ProposerMaxEpochs and returned oldest first.
func (s *Store) ProposerSeries(ctx context.Context, chainID string, from, to uint64, newest int) (*ProposerInputs, error) {
	limit := ProposerMaxEpochs
	order := "ASC"
	if newest > 0 {
		order = "DESC"
		if newest < limit {
			limit = newest
		}
	}
	rows, err := s.db.QueryContext(ctx,
		`SELECT `+concentrationColumns+` FROM concentration_epochs
		  WHERE chain_id = ? AND epoch BETWEEN ? AND ?
		  ORDER BY epoch `+order+` LIMIT ?`, chainID, from, to, limit)
	if err != nil {
		return nil, fmt.Errorf("proposer series: %w", err)
	}
	var list []ConcentrationRow
	for rows.Next() {
		r, err := scanConcentration(rows)
		if err != nil {
			_ = rows.Close()
			return nil, fmt.Errorf("proposer series scan: %w", err)
		}
		list = append(list, r)
	}
	if err := rows.Err(); err != nil {
		_ = rows.Close()
		return nil, err
	}
	_ = rows.Close()
	sort.Slice(list, func(i, j int) bool { return list[i].Epoch < list[j].Epoch })

	in := &ProposerInputs{
		Rows:   list,
		Powers: map[uint64][]PowerRow{},
		Counts: map[uint64]map[string]int64{},
	}
	if in.FirstWalked, err = s.FirstWalkedHeight(ctx, chainID); err != nil {
		return nil, err
	}
	if in.Indexed, err = s.IndexedHeight(ctx, chainID); err != nil {
		return nil, err
	}
	if len(list) == 0 {
		return in, nil
	}
	lo, hi := list[0].Epoch, list[len(list)-1].Epoch

	pr, err := s.db.QueryContext(ctx,
		`SELECT p.epoch, p.cons_addr, p.operator, p.power, p.unit, v.moniker
		   FROM power_snapshots p
		   LEFT JOIN validators v ON v.chain_id = p.chain_id AND v.operator = p.operator
		  WHERE p.chain_id = ? AND p.epoch BETWEEN ? AND ?
		  ORDER BY p.epoch ASC, p.power DESC, p.cons_addr ASC`, chainID, lo, hi)
	if err != nil {
		return nil, fmt.Errorf("proposer powers: %w", err)
	}
	defer func() { _ = pr.Close() }()
	for pr.Next() {
		var ep uint64
		var p PowerRow
		if err := pr.Scan(&ep, &p.ConsAddr, &p.Operator, &p.Power, &p.Unit, &p.Moniker); err != nil {
			return nil, fmt.Errorf("proposer powers scan: %w", err)
		}
		in.Powers[ep] = append(in.Powers[ep], p)
	}
	if err := pr.Err(); err != nil {
		return nil, err
	}

	cr, err := s.db.QueryContext(ctx,
		`SELECT epoch, cons_addr, count FROM proposals_raw
		  WHERE chain_id = ? AND epoch BETWEEN ? AND ?`, chainID, lo, hi)
	if err != nil {
		return nil, fmt.Errorf("proposer counts: %w", err)
	}
	defer func() { _ = cr.Close() }()
	for cr.Next() {
		var ep uint64
		var cons string
		var n int64
		if err := cr.Scan(&ep, &cons, &n); err != nil {
			return nil, fmt.Errorf("proposer counts scan: %w", err)
		}
		if in.Counts[ep] == nil {
			in.Counts[ep] = map[string]int64{}
		}
		in.Counts[ep][cons] = n
	}
	return in, cr.Err()
}
