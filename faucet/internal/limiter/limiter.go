// Package limiter decides whether a payout may happen and remembers every
// payout in SQLite so a restart cannot reset the limits.
//
// A payout is a reservation written BEFORE the transaction is sent. If the
// send fails the reservation is released; if the process dies mid-send the
// reservation stays, so the faucet errs on the side of paying out less.
package limiter

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"sync"
	"time"

	_ "modernc.org/sqlite" // pure-Go driver, CGO-free like the rest of the monorepo
)

// Config holds the rules. Amount and Budget are in the base denom.
type Config struct {
	Amount       uint64
	Budget       uint64        // total payout allowed per BudgetWindow
	Window       time.Duration // per-address and per-IP cooldown
	BudgetWindow time.Duration // rolling window of the global budget
}

// Reason codes returned in LimitError.
const (
	ReasonAddress = "address_limited"
	ReasonIP      = "ip_limited"
	ReasonBudget  = "budget_exhausted"
)

// LimitError is returned when a request is refused by a rule.
type LimitError struct {
	Reason     string
	RetryAfter time.Duration
}

func (e *LimitError) Error() string { return e.Reason }

// IsLimit unwraps a LimitError.
func IsLimit(err error) (*LimitError, bool) {
	var le *LimitError
	ok := errors.As(err, &le)
	return le, ok
}

type Limiter struct {
	cfg Config
	db  *sql.DB
	mu  sync.Mutex
	now func() time.Time
}

const schema = `
CREATE TABLE IF NOT EXISTS grants (
  id      INTEGER PRIMARY KEY AUTOINCREMENT,
  address TEXT NOT NULL,
  ip      TEXT NOT NULL,
  amount  INTEGER NOT NULL,
  ts      INTEGER NOT NULL,
  txhash  TEXT NOT NULL DEFAULT ''
);
CREATE INDEX IF NOT EXISTS grants_address ON grants(address, ts);
CREATE INDEX IF NOT EXISTS grants_ip ON grants(ip, ts);
CREATE INDEX IF NOT EXISTS grants_ts ON grants(ts);
`

// Open opens (creating if absent) the database at path.
func Open(path string, cfg Config) (*Limiter, error) {
	if cfg.Amount == 0 {
		return nil, errors.New("amount must be positive")
	}
	if cfg.BudgetWindow == 0 {
		cfg.BudgetWindow = 24 * time.Hour
	}
	db, err := sql.Open("sqlite", path+"?_pragma=busy_timeout(5000)&_pragma=journal_mode(WAL)")
	if err != nil {
		return nil, fmt.Errorf("open %s: %w", path, err)
	}
	db.SetMaxOpenConns(1)
	if _, err := db.Exec(schema); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("apply schema: %w", err)
	}
	return &Limiter{cfg: cfg, db: db, now: time.Now}, nil
}

// Budget and Window report the rules this limiter enforces, so /status can show
// the same numbers instead of a separately wired copy.
func (l *Limiter) Budget() uint64        { return l.cfg.Budget }
func (l *Limiter) Window() time.Duration { return l.cfg.Window }

func (l *Limiter) Close() error { return l.db.Close() }

// Reserve checks all three rules and, if they pass, records a pending grant
// and returns its id. Everything is under one mutex: two concurrent requests
// for the same address cannot both pass.
func (l *Limiter) Reserve(ctx context.Context, address, ip string) (int64, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	now := l.now()

	retention := l.cfg.Window
	if l.cfg.BudgetWindow > retention {
		retention = l.cfg.BudgetWindow
	}
	if _, err := l.db.ExecContext(ctx, `DELETE FROM grants WHERE ts <= ?`, now.Add(-retention).Unix()); err != nil {
		return 0, err
	}

	for _, c := range []struct{ col, val, reason string }{
		{"address", address, ReasonAddress},
		{"ip", ip, ReasonIP},
	} {
		var last sql.NullInt64
		q := `SELECT MAX(ts) FROM grants WHERE ` + c.col + ` = ? AND ts > ?`
		if err := l.db.QueryRowContext(ctx, q, c.val, now.Add(-l.cfg.Window).Unix()).Scan(&last); err != nil {
			return 0, err
		}
		if last.Valid {
			retry := time.Unix(last.Int64, 0).Add(l.cfg.Window).Sub(now)
			return 0, &LimitError{Reason: c.reason, RetryAfter: retry}
		}
	}

	spent, oldest, err := l.spent(ctx, now)
	if err != nil {
		return 0, err
	}
	if spent+l.cfg.Amount > l.cfg.Budget {
		retry := time.Duration(0)
		if oldest.Valid {
			retry = time.Unix(oldest.Int64, 0).Add(l.cfg.BudgetWindow).Sub(now)
		}
		return 0, &LimitError{Reason: ReasonBudget, RetryAfter: retry}
	}

	res, err := l.db.ExecContext(ctx,
		`INSERT INTO grants(address, ip, amount, ts) VALUES(?,?,?,?)`,
		address, ip, l.cfg.Amount, now.Unix())
	if err != nil {
		return 0, err
	}
	return res.LastInsertId()
}

func (l *Limiter) spent(ctx context.Context, now time.Time) (uint64, sql.NullInt64, error) {
	var sum, oldest sql.NullInt64
	err := l.db.QueryRowContext(ctx,
		`SELECT SUM(amount), MIN(ts) FROM grants WHERE ts > ?`,
		now.Add(-l.cfg.BudgetWindow).Unix()).Scan(&sum, &oldest)
	return uint64(sum.Int64), oldest, err
}

// Commit records the transaction hash of a reservation that was sent.
func (l *Limiter) Commit(ctx context.Context, id int64, txhash string) error {
	l.mu.Lock()
	defer l.mu.Unlock()
	_, err := l.db.ExecContext(ctx, `UPDATE grants SET txhash = ? WHERE id = ?`, txhash, id)
	return err
}

// Release deletes a reservation whose send failed, giving the allowance back.
func (l *Limiter) Release(ctx context.Context, id int64) error {
	l.mu.Lock()
	defer l.mu.Unlock()
	_, err := l.db.ExecContext(ctx, `DELETE FROM grants WHERE id = ?`, id)
	return err
}

// BudgetLeft returns how much of the rolling budget is still unspent.
func (l *Limiter) BudgetLeft(ctx context.Context) (uint64, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	spent, _, err := l.spent(ctx, l.now())
	if err != nil {
		return 0, err
	}
	if spent >= l.cfg.Budget {
		return 0, nil
	}
	return l.cfg.Budget - spent, nil
}
