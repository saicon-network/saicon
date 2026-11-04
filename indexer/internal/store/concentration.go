package store

import (
	"context"
	"database/sql"
	"fmt"
	"math/big"
	"sort"
)

// DetailRetentionEpochs is how many epochs of per-validator and per-entity
// detail are kept. The concentration_epochs rows themselves are kept for good.
const DetailRetentionEpochs = 2000

// ConcentrationRow mirrors concentration_epochs. Pointers are the NULL-able
// columns: nil writes NULL and reads back nil, and NULL is not zero.
type ConcentrationRow struct {
	Epoch  uint64
	Height int64
	// BlockHash is nil when the node no longer had the epoch's last block.
	BlockHash *string
	// PowerUnavailable and EntitiesUnavailable are nil when that part was read,
	// and a reason code when it could not be.
	PowerUnavailable, EntitiesUnavailable *string
	EntityCap                             *string
	SetSize, TotalPower, Top10Power       *int64
	NakamotoValidators                    *int64
	UnitCount, NakamotoEntities           *int64
	EntityCount, MaxActiveSeats           *int64
	ReadAt                                string
}

// PowerRow is one consensus-set member at an epoch's height.
type PowerRow struct {
	ConsAddr string
	Operator *string
	Power    int64
	Unit     *string
	// Moniker is filled on the read side only, from the validators table.
	Moniker *string
}

// SeatRow is one seat of an entity. Role is "member" or "charged_frozen".
type SeatRow struct {
	Operator, Role                       string
	RegistryStatus, Tokens, FrozenReason *string
	Power, FrozenHeight                  *int64
	// Moniker is filled on the read side only, from the validators table.
	Moniker *string
}

// EntityRow is one entity at an epoch's height, with its seats.
type EntityRow struct {
	EntityID, DisplayName, Status               string
	AttestedHeight, ActiveSeats, MaxActiveSeats *int64
	Tokens, EntityCap, Room, FrozenReason       *string
	Frozen                                      *bool
	Seats                                       []SeatRow
}

// WriteConcentration stores one epoch in a single transaction: it replaces
// whatever the epoch held in all four tables, writes the new rows, and drops
// the detail rows (powers, entities, seats) that fell out of the retention
// window. A failure leaves the database as it was, so the epoch is simply read
// again.
func (s *Store) WriteConcentration(ctx context.Context, chainID string, row ConcentrationRow, powers []PowerRow, entities []EntityRow) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin: %w", err)
	}
	defer func() { _ = tx.Rollback() }() // a no-op after Commit

	for _, tbl := range []string{"concentration_epochs", "power_snapshots", "entity_snapshots", "entity_seats"} {
		if _, err := tx.ExecContext(ctx,
			`DELETE FROM `+tbl+` WHERE chain_id = ? AND epoch = ?`, chainID, row.Epoch); err != nil {
			return fmt.Errorf("clear %s for epoch %d: %w", tbl, row.Epoch, err)
		}
	}

	if _, err := tx.ExecContext(ctx,
		`INSERT INTO concentration_epochs (chain_id, epoch, height, block_hash, power_unavailable,
		    set_size, total_power, top10_power, nakamoto_validators, entities_unavailable,
		    unit_count, nakamoto_entities, entity_count, entity_cap, max_active_seats, read_at)
		 VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)`,
		chainID, row.Epoch, row.Height, row.BlockHash, row.PowerUnavailable,
		row.SetSize, row.TotalPower, row.Top10Power, row.NakamotoValidators, row.EntitiesUnavailable,
		row.UnitCount, row.NakamotoEntities, row.EntityCount, row.EntityCap, row.MaxActiveSeats,
		row.ReadAt); err != nil {
		return fmt.Errorf("write concentration epoch %d: %w", row.Epoch, err)
	}

	for _, p := range powers {
		if _, err := tx.ExecContext(ctx,
			`INSERT INTO power_snapshots (chain_id, epoch, cons_addr, operator, power, unit)
			 VALUES (?,?,?,?,?,?)`,
			chainID, row.Epoch, p.ConsAddr, p.Operator, p.Power, p.Unit); err != nil {
			return fmt.Errorf("write power %s@%d: %w", p.ConsAddr, row.Epoch, err)
		}
	}

	for _, e := range entities {
		// Entities reads tokens back as an exact integer; a row it could not
		// read is refused here rather than stored.
		if e.Tokens != nil {
			if _, ok := new(big.Int).SetString(*e.Tokens, 10); !ok {
				return fmt.Errorf("entity %s@%d: tokens %q is not an integer", e.EntityID, row.Epoch, *e.Tokens)
			}
		}
		if _, err := tx.ExecContext(ctx,
			`INSERT INTO entity_snapshots (chain_id, epoch, entity_id, display_name, status,
			    attested_height, tokens, entity_cap, room, active_seats, max_active_seats,
			    frozen, frozen_reason)
			 VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?)`,
			chainID, row.Epoch, e.EntityID, e.DisplayName, e.Status,
			e.AttestedHeight, e.Tokens, e.EntityCap, e.Room, e.ActiveSeats, e.MaxActiveSeats,
			e.Frozen, e.FrozenReason); err != nil {
			return fmt.Errorf("write entity %s@%d: %w", e.EntityID, row.Epoch, err)
		}
		for _, st := range e.Seats {
			if _, err := tx.ExecContext(ctx,
				`INSERT INTO entity_seats (chain_id, epoch, entity_id, operator, role,
				    registry_status, tokens, power, frozen_reason, frozen_height)
				 VALUES (?,?,?,?,?,?,?,?,?,?)`,
				chainID, row.Epoch, e.EntityID, st.Operator, st.Role,
				st.RegistryStatus, st.Tokens, st.Power, st.FrozenReason, st.FrozenHeight); err != nil {
				return fmt.Errorf("write seat %s/%s@%d: %w", e.EntityID, st.Operator, row.Epoch, err)
			}
		}
	}

	// Epochs are unsigned and small ones have nothing to prune; subtracting
	// first would wrap.
	if row.Epoch >= DetailRetentionEpochs {
		cutoff := row.Epoch - DetailRetentionEpochs
		for _, tbl := range []string{"power_snapshots", "entity_snapshots", "entity_seats"} {
			if _, err := tx.ExecContext(ctx,
				`DELETE FROM `+tbl+` WHERE chain_id = ? AND epoch <= ?`, chainID, cutoff); err != nil {
				return fmt.Errorf("prune %s: %w", tbl, err)
			}
		}
	}
	return tx.Commit()
}

// HighestConcentrationEpoch is the newest epoch with a row, measured or
// unmeasurable, and false when there is none. It is the cursor of the pass.
func (s *Store) HighestConcentrationEpoch(ctx context.Context, chainID string) (uint64, bool, error) {
	var ep sql.NullInt64
	err := s.db.QueryRowContext(ctx,
		`SELECT MAX(epoch) FROM concentration_epochs WHERE chain_id = ?`, chainID).Scan(&ep)
	if err != nil {
		return 0, false, fmt.Errorf("highest concentration epoch for %s: %w", chainID, err)
	}
	if !ep.Valid {
		return 0, false, nil
	}
	return uint64(ep.Int64), true, nil
}

// EpochsWithEntitiesUnavailable lists, oldest first and at most limit of them,
// the epochs after epoch after whose entity part carries code and whose height
// is at least minHeight. The entity layer's lazy resolution uses it to find the
// epochs it wrote no_entity_layer before the layer appeared; after is its
// cursor, so an epoch that keeps failing does not head every list.
func (s *Store) EpochsWithEntitiesUnavailable(ctx context.Context, chainID, code string, minHeight int64, after uint64, limit int) ([]uint64, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT epoch FROM concentration_epochs
		  WHERE chain_id = ? AND entities_unavailable = ? AND height >= ? AND epoch > ?
		  ORDER BY epoch ASC LIMIT ?`, chainID, code, minHeight, after, limit)
	if err != nil {
		return nil, fmt.Errorf("epochs with entities %s for %s: %w", code, chainID, err)
	}
	defer func() { _ = rows.Close() }()
	var out []uint64
	for rows.Next() {
		var ep uint64
		if err := rows.Scan(&ep); err != nil {
			return nil, fmt.Errorf("epochs with entities %s scan: %w", code, err)
		}
		out = append(out, ep)
	}
	return out, rows.Err()
}

// MarkEntitiesUnavailable sets the entity part of one epoch from the code from
// to the code to, and touches nothing else: the power figures and the detail
// rows stay as they are. changed is false when the row no longer carries from.
func (s *Store) MarkEntitiesUnavailable(ctx context.Context, chainID string, epoch uint64, from, to string) (changed bool, err error) {
	res, err := s.db.ExecContext(ctx,
		`UPDATE concentration_epochs SET entities_unavailable = ?
		  WHERE chain_id = ? AND epoch = ? AND entities_unavailable = ?`, to, chainID, epoch, from)
	if err != nil {
		return false, fmt.Errorf("mark epoch %d entities %s: %w", epoch, to, err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return false, fmt.Errorf("mark epoch %d entities %s: %w", epoch, to, err)
	}
	return n == 1, nil
}

// NewestHashedEpoch is the height and block hash of the newest row that has a
// hash, and false when no row has one.
func (s *Store) NewestHashedEpoch(ctx context.Context, chainID string) (height int64, hash string, ok bool, err error) {
	err = s.db.QueryRowContext(ctx,
		`SELECT height, block_hash FROM concentration_epochs
		  WHERE chain_id = ? AND block_hash IS NOT NULL
		  ORDER BY epoch DESC LIMIT 1`, chainID).Scan(&height, &hash)
	if err == sql.ErrNoRows {
		return 0, "", false, nil
	}
	if err != nil {
		return 0, "", false, fmt.Errorf("newest hashed epoch for %s: %w", chainID, err)
	}
	return height, hash, true, nil
}

const concentrationColumns = `epoch, height, block_hash, power_unavailable, set_size, total_power,
	top10_power, nakamoto_validators, entities_unavailable, unit_count, nakamoto_entities,
	entity_count, entity_cap, max_active_seats, read_at`

// rowScanner is what both *sql.Row and *sql.Rows offer.
type rowScanner interface{ Scan(dest ...any) error }

func scanConcentration(sc rowScanner) (ConcentrationRow, error) {
	var r ConcentrationRow
	err := sc.Scan(&r.Epoch, &r.Height, &r.BlockHash, &r.PowerUnavailable, &r.SetSize, &r.TotalPower,
		&r.Top10Power, &r.NakamotoValidators, &r.EntitiesUnavailable, &r.UnitCount, &r.NakamotoEntities,
		&r.EntityCount, &r.EntityCap, &r.MaxActiveSeats, &r.ReadAt)
	return r, err
}

// readConcentrationRow returns the newest row (epoch == nil) or the given
// epoch's row, restricted by where, a condition on the row's unavailable
// columns. nil, nil when there is none.
func (s *Store) readConcentrationRow(ctx context.Context, chainID string, epoch *uint64, where string) (*ConcentrationRow, error) {
	q := `SELECT ` + concentrationColumns + ` FROM concentration_epochs
	       WHERE chain_id = ? AND ` + where
	args := []any{chainID}
	if epoch != nil {
		q += ` AND epoch = ?`
		args = append(args, *epoch)
	}
	q += ` ORDER BY epoch DESC LIMIT 1`
	r, err := scanConcentration(s.db.QueryRowContext(ctx, q, args...))
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("concentration row for %s: %w", chainID, err)
	}
	return &r, nil
}

// Concentration returns the newest epoch whose power was read (epoch == nil) or
// the given epoch if its power was read, with its validators ordered by power
// descending, then consensus address. nil, nil, nil when there is no such epoch.
// An epoch whose detail has been pruned returns its row and no powers.
func (s *Store) Concentration(ctx context.Context, chainID string, epoch *uint64) (*ConcentrationRow, []PowerRow, error) {
	row, err := s.readConcentrationRow(ctx, chainID, epoch, `power_unavailable IS NULL`)
	if err != nil || row == nil {
		return nil, nil, err
	}
	rows, err := s.db.QueryContext(ctx,
		`SELECT p.cons_addr, p.operator, p.power, p.unit, v.moniker
		   FROM power_snapshots p
		   LEFT JOIN validators v ON v.chain_id = p.chain_id AND v.operator = p.operator
		  WHERE p.chain_id = ? AND p.epoch = ?
		  ORDER BY p.power DESC, p.cons_addr ASC`, chainID, row.Epoch)
	if err != nil {
		return nil, nil, fmt.Errorf("powers for epoch %d: %w", row.Epoch, err)
	}
	defer func() { _ = rows.Close() }()
	var powers []PowerRow // nil when the detail is gone
	for rows.Next() {
		var p PowerRow
		if err := rows.Scan(&p.ConsAddr, &p.Operator, &p.Power, &p.Unit, &p.Moniker); err != nil {
			return nil, nil, fmt.Errorf("powers scan: %w", err)
		}
		powers = append(powers, p)
	}
	if err := rows.Err(); err != nil {
		return nil, nil, err
	}
	return row, powers, nil
}

// ConcentrationSeries returns every row in [from, to], oldest first, capped at
// MaxEpochsPerQuery. Unmeasurable epochs are included with their codes.
func (s *Store) ConcentrationSeries(ctx context.Context, chainID string, from, to uint64) ([]ConcentrationRow, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT `+concentrationColumns+` FROM concentration_epochs
		  WHERE chain_id = ? AND epoch BETWEEN ? AND ?
		  ORDER BY epoch ASC LIMIT ?`, chainID, from, to, MaxEpochsPerQuery)
	if err != nil {
		return nil, fmt.Errorf("concentration series: %w", err)
	}
	defer func() { _ = rows.Close() }()
	out := []ConcentrationRow{} // never nil: an empty range serves [], not null
	for rows.Next() {
		r, err := scanConcentration(rows)
		if err != nil {
			return nil, fmt.Errorf("concentration series scan: %w", err)
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// Entities returns the newest epoch with both its power and its entity part
// read (epoch == nil), or the given epoch if both were, with its entities and
// their seats. Entities are ordered by tokens descending, compared exactly as
// integers, those without tokens last, then by id; seats by role (members
// first), then operator. nil, nil, nil when there is no such epoch.
func (s *Store) Entities(ctx context.Context, chainID string, epoch *uint64) (*ConcentrationRow, []EntityRow, error) {
	row, err := s.readConcentrationRow(ctx, chainID, epoch,
		`power_unavailable IS NULL AND entities_unavailable IS NULL`)
	if err != nil || row == nil {
		return nil, nil, err
	}

	rows, err := s.db.QueryContext(ctx,
		`SELECT entity_id, display_name, status, attested_height, tokens, entity_cap, room,
		        active_seats, max_active_seats, frozen, frozen_reason
		   FROM entity_snapshots
		  WHERE chain_id = ? AND epoch = ?
		  ORDER BY entity_id ASC`, chainID, row.Epoch)
	if err != nil {
		return nil, nil, fmt.Errorf("entities for epoch %d: %w", row.Epoch, err)
	}
	defer func() { _ = rows.Close() }()
	var out []EntityRow
	idx := map[string]int{}
	for rows.Next() {
		var e EntityRow
		if err := rows.Scan(&e.EntityID, &e.DisplayName, &e.Status, &e.AttestedHeight, &e.Tokens,
			&e.EntityCap, &e.Room, &e.ActiveSeats, &e.MaxActiveSeats, &e.Frozen, &e.FrozenReason); err != nil {
			return nil, nil, fmt.Errorf("entities scan: %w", err)
		}
		idx[e.EntityID] = len(out)
		out = append(out, e)
	}
	if err := rows.Err(); err != nil {
		return nil, nil, err
	}

	seats, err := s.db.QueryContext(ctx,
		`SELECT st.entity_id, st.operator, st.role, st.registry_status, st.tokens, st.power,
		        st.frozen_reason, st.frozen_height, v.moniker
		   FROM entity_seats st
		   LEFT JOIN validators v ON v.chain_id = st.chain_id AND v.operator = st.operator
		  WHERE st.chain_id = ? AND st.epoch = ?
		  ORDER BY st.entity_id ASC, CASE st.role WHEN 'member' THEN 0 ELSE 1 END, st.operator ASC`,
		chainID, row.Epoch)
	if err != nil {
		return nil, nil, fmt.Errorf("seats for epoch %d: %w", row.Epoch, err)
	}
	defer func() { _ = seats.Close() }()
	for seats.Next() {
		var id string
		var st SeatRow
		if err := seats.Scan(&id, &st.Operator, &st.Role, &st.RegistryStatus, &st.Tokens, &st.Power,
			&st.FrozenReason, &st.FrozenHeight, &st.Moniker); err != nil {
			return nil, nil, fmt.Errorf("seats scan: %w", err)
		}
		i, ok := idx[id]
		if !ok {
			continue // a seat without its entity row cannot be written by WriteConcentration
		}
		out[i].Seats = append(out[i].Seats, st)
	}
	if err := seats.Err(); err != nil {
		return nil, nil, err
	}

	// Tokens are exact integers beyond float64: they are ordered here as big.Int,
	// never cast in SQL. SQL already ordered by id, which the stable sort keeps
	// for ties.
	vals := make([]*big.Int, len(out))
	for i, e := range out {
		if e.Tokens != nil {
			n, ok := new(big.Int).SetString(*e.Tokens, 10)
			if !ok {
				return nil, nil, fmt.Errorf("entity %s at epoch %d: tokens %q is not an integer", e.EntityID, row.Epoch, *e.Tokens)
			}
			vals[i] = n
		}
	}
	order := make([]int, len(out))
	for i := range order {
		order[i] = i
	}
	sort.SliceStable(order, func(a, b int) bool {
		x, y := vals[order[a]], vals[order[b]]
		switch {
		case x == nil:
			return false
		case y == nil:
			return true
		}
		return x.Cmp(y) > 0
	})
	sorted := make([]EntityRow, len(out))
	for i, j := range order {
		sorted[i] = out[j]
	}
	return row, sorted, nil
}
