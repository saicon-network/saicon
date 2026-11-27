package store

import (
	"context"
	"database/sql"
	"fmt"
)

// PreviewEpochRow mirrors vp_preview_epochs. Unavailable is nil for a preview
// that was read; then TotalVP, Threshold and Cap carry the chain's values.
// For an unavailable one they are nil, and no PreviewRow exists.
type PreviewEpochRow struct {
	Epoch       uint64
	Height      int64
	Unavailable *string
	TotalVP     *string
	Threshold   *string
	Cap         *string
}

// PreviewRow mirrors vp_preview: one bonded validator of an available preview.
type PreviewRow struct {
	Operator     string
	VP           string
	VPShare      string
	StockShare   string
	Eligible     bool
	Reason       *string
	Warp         string
	WarpMeasured bool
}

// WritePreview stores how one epoch's preview read ended, and takes the epoch
// off the due list, in one transaction: an epoch row never stands beside
// validator rows from another read. rows must be empty for an unavailable epoch.
//
// It never downgrades. When the epoch already holds a preview and e carries a
// code, the stored preview and its rows are kept: a later read of the same
// height can say the node pruned it or failed, but not that the chain had no
// preview there. An available read replaces whatever was stored.
func (s *Store) WritePreview(ctx context.Context, chainID string, e PreviewEpochRow, rows []PreviewRow) error {
	if e.Unavailable != nil && len(rows) > 0 {
		return fmt.Errorf("preview epoch %d: unavailable (%s) with %d validator rows", e.Epoch, *e.Unavailable, len(rows))
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin: %w", err)
	}
	defer func() { _ = tx.Rollback() }() // a no-op after Commit

	if _, err := tx.ExecContext(ctx,
		`DELETE FROM vp_preview_due WHERE chain_id = ? AND epoch = ?`, chainID, e.Epoch); err != nil {
		return fmt.Errorf("preview epoch %d: due: %w", e.Epoch, err)
	}
	if e.Unavailable != nil {
		var held int
		if err := tx.QueryRowContext(ctx,
			`SELECT COUNT(*) FROM vp_preview_epochs WHERE chain_id = ? AND epoch = ? AND unavailable IS NULL`,
			chainID, e.Epoch).Scan(&held); err != nil {
			return fmt.Errorf("preview epoch %d: stored: %w", e.Epoch, err)
		}
		if held > 0 {
			return tx.Commit() // keep the preview; only the due row goes
		}
	}
	if _, err := tx.ExecContext(ctx,
		`DELETE FROM vp_preview WHERE chain_id = ? AND epoch = ?`, chainID, e.Epoch); err != nil {
		return fmt.Errorf("preview epoch %d: clear: %w", e.Epoch, err)
	}
	if _, err := tx.ExecContext(ctx,
		`INSERT INTO vp_preview_epochs (chain_id, epoch, height, unavailable, total_vp, threshold, cap)
		 VALUES (?,?,?,?,?,?,?)
		 ON CONFLICT(chain_id, epoch) DO UPDATE SET
		    height=excluded.height, unavailable=excluded.unavailable, total_vp=excluded.total_vp,
		    threshold=excluded.threshold, cap=excluded.cap`,
		chainID, e.Epoch, e.Height, e.Unavailable, e.TotalVP, e.Threshold, e.Cap); err != nil {
		return fmt.Errorf("preview epoch %d: %w", e.Epoch, err)
	}
	for _, r := range rows {
		if _, err := tx.ExecContext(ctx,
			`INSERT INTO vp_preview (chain_id, epoch, operator, vp, vp_share, stock_share, eligible,
			    reason, warp, warp_measured)
			 VALUES (?,?,?,?,?,?,?,?,?,?)`,
			chainID, e.Epoch, r.Operator, r.VP, r.VPShare, r.StockShare, r.Eligible,
			r.Reason, r.Warp, r.WarpMeasured); err != nil {
			return fmt.Errorf("preview epoch %d, validator %s: %w", e.Epoch, r.Operator, err)
		}
	}
	return tx.Commit()
}

// MarkPreviewDue puts the epoch on the preview pass's list, unless it already
// holds a preview. Adding an epoch that is already listed keeps its attempts.
func (s *Store) MarkPreviewDue(ctx context.Context, chainID string, epoch uint64) error {
	_, err := s.db.ExecContext(ctx,
		`INSERT OR IGNORE INTO vp_preview_due (chain_id, epoch, attempts)
		 SELECT ?, ?, 0
		  WHERE NOT EXISTS (SELECT 1 FROM vp_preview_epochs
		                     WHERE chain_id = ? AND epoch = ? AND unavailable IS NULL)`,
		chainID, epoch, chainID, epoch)
	if err != nil {
		return fmt.Errorf("preview due for epoch %d: %w", epoch, err)
	}
	return nil
}

// DuePreview is an epoch whose preview is still to be read.
type DuePreview struct {
	Epoch    uint64
	Attempts int
}

// DuePreviews lists up to limit due epochs, newest first: the page shows the
// newest preview, so that is the one read first after a catch-up or an outage.
func (s *Store) DuePreviews(ctx context.Context, chainID string, limit int) ([]DuePreview, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT epoch, attempts FROM vp_preview_due WHERE chain_id = ? ORDER BY epoch DESC LIMIT ?`,
		chainID, limit)
	if err != nil {
		return nil, fmt.Errorf("due previews: %w", err)
	}
	defer func() { _ = rows.Close() }()
	var out []DuePreview
	for rows.Next() {
		var d DuePreview
		if err := rows.Scan(&d.Epoch, &d.Attempts); err != nil {
			return nil, fmt.Errorf("due previews scan: %w", err)
		}
		out = append(out, d)
	}
	return out, rows.Err()
}

// PreviewFailed counts one more transient failure for a due epoch and returns
// the count so far.
func (s *Store) PreviewFailed(ctx context.Context, chainID string, epoch uint64) (int, error) {
	var n int
	err := s.db.QueryRowContext(ctx,
		`UPDATE vp_preview_due SET attempts = attempts + 1 WHERE chain_id = ? AND epoch = ? RETURNING attempts`,
		chainID, epoch).Scan(&n)
	if err != nil {
		return 0, fmt.Errorf("preview epoch %d: count failure: %w", epoch, err)
	}
	return n, nil
}

// Operators lists every validator's operator address as x/staking names it.
func (s *Store) Operators(ctx context.Context, chainID string) ([]string, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT operator FROM validators WHERE chain_id = ? ORDER BY operator`, chainID)
	if err != nil {
		return nil, fmt.Errorf("operators: %w", err)
	}
	defer func() { _ = rows.Close() }()
	var out []string
	for rows.Next() {
		var op string
		if err := rows.Scan(&op); err != nil {
			return nil, fmt.Errorf("operators scan: %w", err)
		}
		out = append(out, op)
	}
	return out, rows.Err()
}

// PreviewHead is what the scoreboard says about the preview as a whole.
type PreviewHead struct {
	// Epoch is the newest epoch with an available preview, and Threshold its
	// warp_threshold. Both nil when no epoch has one.
	Epoch     *uint64
	Threshold *string
	// Latest is the newest epoch with a preview row of either kind, and
	// Unavailable its code: nil when that epoch's preview was read, or when no
	// epoch has a row at all (every epoch read before this build).
	Latest      *uint64
	Unavailable *string
}

// PreviewHead reads the head in one statement, so its parts describe one moment.
func (s *Store) PreviewHead(ctx context.Context, chainID string) (PreviewHead, error) {
	var ep, latest sql.NullInt64
	var threshold, code sql.NullString
	err := s.db.QueryRowContext(ctx,
		`SELECT a.epoch, a.threshold, l.epoch, l.unavailable
		   FROM (SELECT 1) AS one
		   LEFT JOIN (SELECT epoch, threshold FROM vp_preview_epochs
		               WHERE chain_id = ? AND unavailable IS NULL
		               ORDER BY epoch DESC LIMIT 1) AS a ON 1
		   LEFT JOIN (SELECT epoch, unavailable FROM vp_preview_epochs
		               WHERE chain_id = ?
		               ORDER BY epoch DESC LIMIT 1) AS l ON 1`,
		chainID, chainID).Scan(&ep, &threshold, &latest, &code)
	if err != nil {
		return PreviewHead{}, fmt.Errorf("preview head for %s: %w", chainID, err)
	}
	var h PreviewHead
	if ep.Valid {
		e := uint64(ep.Int64)
		h.Epoch = &e
	}
	if threshold.Valid {
		h.Threshold = &threshold.String
	}
	if latest.Valid {
		l := uint64(latest.Int64)
		h.Latest = &l
	}
	if code.Valid {
		h.Unavailable = &code.String
	}
	return h, nil
}
