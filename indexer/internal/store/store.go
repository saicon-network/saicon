// Package store owns the SQLite file and every SQL statement in the service.
// Nothing outside this package writes SQL: the swap to Postgres, if it ever
// happens, is this package's implementation and nobody else's problem.
package store

import (
	"context"
	"database/sql"
	_ "embed"
	"fmt"
	"math/big"
	"sort"
	"time"

	_ "modernc.org/sqlite" // pure-Go driver: the monorepo builds CGO_ENABLED=0
)

//go:embed schema.sql
var schema string

type Store struct{ db *sql.DB }

// Open opens (creating if absent) the database at path and applies the schema.
func Open(path string) (*Store, error) {
	// _txlock=immediate takes the write lock at BEGIN rather than at first
	// write, so the follower's read-modify-write of a counter cannot interleave
	// with another writer between its SELECT and its UPDATE.
	db, err := sql.Open("sqlite", path+"?_pragma=busy_timeout(5000)&_pragma=journal_mode(WAL)&_txlock=immediate")
	if err != nil {
		return nil, fmt.Errorf("open %s: %w", path, err)
	}
	if _, err := db.ExecContext(context.Background(), schema); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("apply schema: %w", err)
	}
	if err := migrate(context.Background(), db); err != nil {
		_ = db.Close()
		return nil, err
	}
	return &Store{db: db}, nil
}

// addedColumns are columns that joined `chains` after the first version of this
// schema. CREATE TABLE IF NOT EXISTS is a no-op against a table that already
// exists, so a database written by an older build keeps its old shape and every
// query naming a newer column fails with "no such column" — which the binary
// would report as a corrupt database and exit on, for ever, against a volume
// that restart: unless-stopped never lets go of.
//
// CREATE INDEX IF NOT EXISTS does not have this problem: it is its own statement
// and applies to an existing table. Only columns need this.
var addedColumns = map[string]string{
	"first_walked_height": "INTEGER NOT NULL DEFAULT 1",
	"epoch_length":        "INTEGER",
}

// migrate adds any column of `chains` this build knows about and the file does
// not. Both of them are nullable or defaulted, so an older database gains them
// with the meaning "not recorded" — which is what it is.
func migrate(ctx context.Context, db *sql.DB) error {
	rows, err := db.QueryContext(ctx, `PRAGMA table_info(chains)`)
	if err != nil {
		return fmt.Errorf("read chains columns: %w", err)
	}
	have := map[string]bool{}
	for rows.Next() {
		var cid int
		var name, ctype string
		var notnull int
		var dflt any
		var pk int
		if err := rows.Scan(&cid, &name, &ctype, &notnull, &dflt, &pk); err != nil {
			_ = rows.Close()
			return fmt.Errorf("read chains columns: %w", err)
		}
		have[name] = true
	}
	_ = rows.Close()
	if err := rows.Err(); err != nil {
		return fmt.Errorf("read chains columns: %w", err)
	}
	if len(have) == 0 {
		return nil // no chains table yet; the schema above just created it
	}
	for name, decl := range addedColumns {
		if have[name] {
			continue
		}
		if _, err := db.ExecContext(ctx, `ALTER TABLE chains ADD COLUMN `+name+` `+decl); err != nil {
			return fmt.Errorf("add chains.%s: %w", name, err)
		}
	}
	return nil
}

func (s *Store) Close() error { return s.db.Close() }

// ChainID returns the chain this database holds, and false if it holds none.
func (s *Store) ChainID(ctx context.Context) (string, bool, error) {
	var id string
	err := s.db.QueryRowContext(ctx, `SELECT chain_id FROM chains LIMIT 1`).Scan(&id)
	switch {
	case err == sql.ErrNoRows:
		return "", false, nil
	case err != nil:
		return "", false, fmt.Errorf("read chain id: %w", err)
	}
	return id, true, nil
}

// SetChain records the chain this database indexes. Called once, on first sight.
//
// earliestBlockTime is the earliest block the node still retained at that
// moment (RPC Status's sync_info.earliest_block_time) — not the chain's
// genesis time, which a pruned node no longer has.
func (s *Store) SetChain(ctx context.Context, chainID string, earliestBlockTime time.Time) error {
	now := time.Now().UTC().Format(time.RFC3339Nano)
	_, err := s.db.ExecContext(ctx,
		`INSERT INTO chains (chain_id, indexed_height, earliest_block_time, first_seen, updated_at)
		 VALUES (?, 0, ?, ?, ?)
		 ON CONFLICT(chain_id) DO UPDATE SET updated_at = excluded.updated_at`,
		chainID, earliestBlockTime.UTC().Format(time.RFC3339Nano), now, now)
	if err != nil {
		return fmt.Errorf("set chain %s: %w", chainID, err)
	}
	return nil
}

// DB exposes the handle for tests that need to assert raw rows.
func (s *Store) DB() *sql.DB { return s.db }

// SeedCursor sets the indexed-height cursor directly, without counting a
// block. It exists for exactly one case: a fresh database pointed at a node
// that has already pruned below height 1. Called with anything other than a
// freshly-registered chain it would silently discard proposal counts already
// recorded, so the caller — not this method — is responsible for checking
// that the chain was just seen for the first time.
//
// Raise-only, in SQL rather than in a comment: a seed that could lower the
// cursor would re-count blocks the counter already holds.
//
// first_walked_height moves with it. The cursor says how far the walk has
// got; this says where it began, and the two together are what "we counted
// every block of epoch N" actually requires. Setting only the cursor would
// make every epoch below the retention floor look walked, and a validator
// with no proposals there indistinguishable from one we never looked for.
func (s *Store) SeedCursor(ctx context.Context, chainID string, height int64) error {
	_, err := s.db.ExecContext(ctx,
		`UPDATE chains SET indexed_height = ?, first_walked_height = ?, updated_at = ?
		  WHERE chain_id = ? AND indexed_height < ?`,
		height, height+1, time.Now().UTC().Format(time.RFC3339Nano), chainID, height)
	if err != nil {
		return fmt.Errorf("seed cursor for %s: %w", chainID, err)
	}
	return nil
}

// FirstWalkedHeight is the lowest height the follower walked from, 1 when it
// started at genesis.
func (s *Store) FirstWalkedHeight(ctx context.Context, chainID string) (int64, error) {
	var h int64
	err := s.db.QueryRowContext(ctx,
		`SELECT first_walked_height FROM chains WHERE chain_id = ?`, chainID).Scan(&h)
	if err == sql.ErrNoRows {
		return 1, nil
	}
	if err != nil {
		return 0, fmt.Errorf("first walked height for %s: %w", chainID, err)
	}
	return h, nil
}

// RecordEpochLength stores the epoch length this database is bucketed under.
// Called once, on first sight: a chain that already has a stored length keeps
// it — overwriting it would silently move the baseline the mismatch check
// compares against, defeating the one thing the check exists to catch.
func (s *Store) RecordEpochLength(ctx context.Context, chainID string, epochLen uint64) error {
	_, err := s.db.ExecContext(ctx,
		`UPDATE chains SET epoch_length = ?, updated_at = ?
		  WHERE chain_id = ? AND epoch_length IS NULL`,
		epochLen, time.Now().UTC().Format(time.RFC3339Nano), chainID)
	if err != nil {
		return fmt.Errorf("record epoch length for %s: %w", chainID, err)
	}
	return nil
}

// StoredEpochLength is the epoch length previously recorded for this chain,
// and false when none has been recorded yet — which includes every database
// created before this column existed, so an old database is not read as a
// mismatch the first time it is opened again.
func (s *Store) StoredEpochLength(ctx context.Context, chainID string) (uint64, bool, error) {
	var n sql.NullInt64
	err := s.db.QueryRowContext(ctx,
		`SELECT epoch_length FROM chains WHERE chain_id = ?`, chainID).Scan(&n)
	if err == sql.ErrNoRows {
		return 0, false, nil
	}
	if err != nil {
		return 0, false, fmt.Errorf("stored epoch length for %s: %w", chainID, err)
	}
	if !n.Valid {
		return 0, false, nil
	}
	return uint64(n.Int64), true, nil
}

// IndexedHeight is the last height counted for this chain, 0 if none.
func (s *Store) IndexedHeight(ctx context.Context, chainID string) (int64, error) {
	var h int64
	err := s.db.QueryRowContext(ctx, `SELECT indexed_height FROM chains WHERE chain_id = ?`, chainID).Scan(&h)
	if err == sql.ErrNoRows {
		return 0, nil
	}
	if err != nil {
		return 0, fmt.Errorf("read cursor for %s: %w", chainID, err)
	}
	return h, nil
}

// RecordBlock counts one proposal and advances the cursor, atomically.
//
// A height at or below the cursor is a replay — the follower restarted and is
// walking ground it already covered — and is ignored rather than counted again.
// Without that guard the only figure this service originates would drift upward
// on every crash, and nothing downstream could detect it: no chain query can
// return the number to compare against.
//
// RecordBlock is not safe to call concurrently for the same chain_id. A caller
// that loses the race gets nil back and its block is silently dropped without error.
func (s *Store) RecordBlock(ctx context.Context, chainID string, height int64, epoch uint64, consAddr string) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin: %w", err)
	}
	defer func() { _ = tx.Rollback() }() // a no-op after Commit

	var cursor int64
	err = tx.QueryRowContext(ctx, `SELECT indexed_height FROM chains WHERE chain_id = ?`, chainID).Scan(&cursor)
	if err == sql.ErrNoRows {
		return fmt.Errorf("read cursor for %s: chain not registered", chainID)
	}
	if err != nil {
		return fmt.Errorf("read cursor for %s: %w", chainID, err)
	}
	if height <= cursor {
		return nil // already counted
	}

	if _, err := tx.ExecContext(ctx,
		`INSERT INTO proposals_raw (chain_id, epoch, cons_addr, count) VALUES (?, ?, ?, 1)
		 ON CONFLICT(chain_id, epoch, cons_addr) DO UPDATE SET count = count + 1`,
		chainID, epoch, consAddr); err != nil {
		return fmt.Errorf("count proposal: %w", err)
	}
	if _, err := tx.ExecContext(ctx,
		`UPDATE chains SET indexed_height = ?, updated_at = ? WHERE chain_id = ?`,
		height, time.Now().UTC().Format(time.RFC3339Nano), chainID); err != nil {
		return fmt.Errorf("advance cursor: %w", err)
	}
	return tx.Commit()
}

// ValidatorRow mirrors the validators table. Pointer fields are the chain's
// silence: nil writes NULL, and NULL is not zero.
type ValidatorRow struct {
	Operator       string
	Moniker        string
	StakingStatus  string
	Jailed         bool
	Tokens         string
	CommissionRate string
	ComplianceFlag *bool
	// The registry fields are pointers because a validator can be in the staking
	// set with no registry record at all: x/validatorregistry creates it from the
	// AfterValidatorBonded hook only (keeper/hooks.go), while this service lists
	// staking validators with no status filter. A validator created but never
	// bonded, or since unbonded, has none. Written as zeroes, LockupSeconds = 0
	// would read as "no lock-up" on a chain whose V-3 invariant is the lock-up.
	RegistryStatus     *string
	BondingStart       *string
	BondingStartHeight *int64
	LockupSeconds      *int64
	AccumulatedEP      *string
	LastEpochUpdate    *uint64
}

// SnapshotRow mirrors epoch_snapshots. Same rule on pointers.
type SnapshotRow struct {
	Epoch          uint64  `json:"epoch"`
	Operator       string  `json:"operator"`
	ComputedScore  *string `json:"computed_score"`
	ComputedAt     *string `json:"computed_at"`
	BlocksSigned   *uint64 `json:"blocks_signed"`
	BlocksMissed   *uint64 `json:"blocks_missed"`
	RewardAmount   *string `json:"reward_amount"`
	RewardEligible *bool   `json:"reward_eligible"`
	RewardReason   *string `json:"reward_reason"`
	Proposals      *int64  `json:"proposals"`
}

func (s *Store) UpsertValidator(ctx context.Context, chainID string, v ValidatorRow) error {
	_, err := s.db.ExecContext(ctx,
		`INSERT INTO validators (chain_id, operator, moniker, staking_status, jailed, tokens,
		    commission_rate, registry_status, compliance_flag, bonding_start, bonding_start_height,
		    lockup_seconds, accumulated_ep, last_epoch_update, updated_at)
		 VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)
		 ON CONFLICT(chain_id, operator) DO UPDATE SET
		    moniker=excluded.moniker, staking_status=excluded.staking_status,
		    jailed=excluded.jailed, tokens=excluded.tokens,
		    commission_rate=excluded.commission_rate, registry_status=excluded.registry_status,
		    compliance_flag=excluded.compliance_flag, bonding_start=excluded.bonding_start,
		    bonding_start_height=excluded.bonding_start_height, lockup_seconds=excluded.lockup_seconds,
		    accumulated_ep=excluded.accumulated_ep, last_epoch_update=excluded.last_epoch_update,
		    updated_at=excluded.updated_at`,
		chainID, v.Operator, v.Moniker, v.StakingStatus, v.Jailed, v.Tokens,
		v.CommissionRate, v.RegistryStatus, v.ComplianceFlag, v.BondingStart,
		v.BondingStartHeight, v.LockupSeconds, v.AccumulatedEP, v.LastEpochUpdate,
		time.Now().UTC().Format(time.RFC3339Nano))
	if err != nil {
		return fmt.Errorf("upsert validator %s: %w", v.Operator, err)
	}
	return nil
}

func (s *Store) UpsertSnapshot(ctx context.Context, chainID string, r SnapshotRow) error {
	_, err := s.db.ExecContext(ctx,
		`INSERT INTO epoch_snapshots (chain_id, epoch, operator, computed_score, computed_at,
		    blocks_signed, blocks_missed, reward_amount, reward_eligible, reward_reason, proposals)
		 VALUES (?,?,?,?,?,?,?,?,?,?,?)
		 ON CONFLICT(chain_id, epoch, operator) DO UPDATE SET
		    computed_score=excluded.computed_score, computed_at=excluded.computed_at,
		    blocks_signed=excluded.blocks_signed, blocks_missed=excluded.blocks_missed,
		    reward_amount=excluded.reward_amount, reward_eligible=excluded.reward_eligible,
		    reward_reason=excluded.reward_reason, proposals=excluded.proposals`,
		chainID, r.Epoch, r.Operator, r.ComputedScore, r.ComputedAt,
		r.BlocksSigned, r.BlocksMissed, r.RewardAmount, r.RewardEligible,
		r.RewardReason, r.Proposals)
	if err != nil {
		return fmt.Errorf("upsert snapshot %s@%d: %w", r.Operator, r.Epoch, err)
	}
	return nil
}

func (s *Store) ProposalsFor(ctx context.Context, chainID string, epoch uint64, consAddr string) (int64, error) {
	var n int64
	err := s.db.QueryRowContext(ctx,
		`SELECT count FROM proposals_raw WHERE chain_id=? AND epoch=? AND cons_addr=?`,
		chainID, epoch, consAddr).Scan(&n)
	if err == sql.ErrNoRows {
		return 0, nil
	}
	if err != nil {
		return 0, fmt.Errorf("proposals for %s@%d: %w", consAddr, epoch, err)
	}
	return n, nil
}

// MarkEpochRead records that every validator's row for this epoch was written.
func (s *Store) MarkEpochRead(ctx context.Context, chainID string, epoch uint64) error {
	_, err := s.db.ExecContext(ctx,
		`INSERT INTO epochs_read (chain_id, epoch, completed_at) VALUES (?, ?, ?)
		 ON CONFLICT(chain_id, epoch) DO UPDATE SET completed_at = excluded.completed_at`,
		chainID, epoch, time.Now().UTC().Format(time.RFC3339Nano))
	if err != nil {
		return fmt.Errorf("mark epoch %d read for %s: %w", epoch, chainID, err)
	}
	return nil
}

// HighestEpochRead is the newest epoch this database has FINISHED reading, and
// false when it has finished none. It deliberately asks epochs_read rather than
// taking MAX(epoch) from the snapshots: that would call an epoch done the moment
// the first validator's row landed, and the loop would never come back for the
// rest.
func (s *Store) HighestEpochRead(ctx context.Context, chainID string) (uint64, bool, error) {
	var ep sql.NullInt64
	err := s.db.QueryRowContext(ctx,
		`SELECT MAX(epoch) FROM epochs_read WHERE chain_id = ?`, chainID).Scan(&ep)
	if err != nil {
		return 0, false, fmt.Errorf("highest epoch read for %s: %w", chainID, err)
	}
	if !ep.Valid {
		return 0, false, nil
	}
	return uint64(ep.Int64), true, nil
}

// ScoreRow is one line of the scoreboard: current state plus the latest epoch.
type ScoreRow struct {
	Operator      string `json:"operator"`
	Moniker       string `json:"moniker"`
	StakingStatus string `json:"staking_status"`
	Jailed        bool   `json:"jailed"`
	Tokens        string `json:"tokens"`

	// Everything below can legitimately be absent, and each one is a different
	// absence. A null registry_status means the validator has no registry record
	// at all; a null compliance_flag means x/compliance has never ruled on it; a
	// null proposals means this service has not finished walking that epoch. None
	// of them may be served as a zero or an empty string — COALESCE here would put
	// back exactly the confusion the database columns exist to prevent.
	RegistryStatus *string `json:"registry_status"`
	LockupSeconds  *int64  `json:"lockup_seconds"`
	ComplianceFlag *bool   `json:"compliance_flag"`
	AccumulatedEP  *string `json:"accumulated_ep"`
	LatestEpoch    *uint64 `json:"latest_epoch"`
	ComputedScore  *string `json:"computed_score"`
	Proposals      *int64  `json:"proposals"`
}

func (s *Store) Scoreboard(ctx context.Context, chainID string) ([]ScoreRow, error) {
	rows, err := s.db.QueryContext(ctx,
		// COALESCE only on the fields x/staking always answers, where the column is
		// nullable but UpsertValidator never leaves it unset. Everything the chain
		// can genuinely decline to answer is read as it is stored.
		//
		// A validator with no score sorts LAST rather than as a zero: "not scored
		// yet" is not the bottom of the table, it is not in the table's terms at
		// all, and putting it among the real zeros is a ranking claim we cannot make.
		//
		// SQL orders only the NULLs. The scores themselves are ordered in Go,
		// exactly — see the sort below.
		`SELECT v.operator, COALESCE(v.moniker,''), COALESCE(v.staking_status,''),
		        COALESCE(v.jailed,0), COALESCE(v.tokens,''),
		        v.registry_status, v.lockup_seconds, v.compliance_flag, v.accumulated_ep,
		        e.epoch, e.computed_score, e.proposals
		   FROM validators v
		   LEFT JOIN epoch_snapshots e
		     ON e.chain_id = v.chain_id AND e.operator = v.operator
		    AND e.epoch = (SELECT MAX(epoch) FROM epoch_snapshots
		                    WHERE chain_id = v.chain_id AND operator = v.operator)
		  WHERE v.chain_id = ?
		  ORDER BY e.computed_score IS NULL, v.operator ASC`, chainID)
	if err != nil {
		return nil, fmt.Errorf("scoreboard: %w", err)
	}
	defer func() { _ = rows.Close() }()

	out := []ScoreRow{} // never nil: an empty index serves [], not null
	for rows.Next() {
		var r ScoreRow
		if err := rows.Scan(&r.Operator, &r.Moniker, &r.StakingStatus,
			&r.Jailed, &r.Tokens, &r.RegistryStatus, &r.LockupSeconds,
			&r.ComplianceFlag, &r.AccumulatedEP,
			&r.LatestEpoch, &r.ComputedScore, &r.Proposals); err != nil {
			return nil, fmt.Errorf("scoreboard scan: %w", err)
		}
		out = append(out, r)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}

	// The scores are compared as exact decimals, not as floats.
	//
	// CAST(computed_score AS REAL) in SQL looked like the obvious ordering and is
	// wrong: the chain sends an 18-decimal sdk.Dec, float64 carries about 16
	// significant digits, and two scores that agree to 16 digits collapse to the
	// same value. The tie then falls through to the operator address, so the table
	// is ordered alphabetically while claiming to be ordered by participation.
	// Demonstrated: "1.000000000000000001" and "1.000000000000000002" both cast to
	// 1, and the lower-scoring validator was served above the higher one.
	//
	// Seventy-five rows make the cost of doing it properly irrelevant.
	sort.SliceStable(out, func(i, j int) bool {
		a, b := out[i].ComputedScore, out[j].ComputedScore
		switch {
		case a == nil && b == nil:
			return false // both unscored: keep the operator order SQL gave them
		case a == nil:
			return false // unscored sorts after scored
		case b == nil:
			return true
		}
		ra, oka := new(big.Rat).SetString(*a)
		rb, okb := new(big.Rat).SetString(*b)
		if !oka || !okb {
			// A score the chain sent that does not parse as a decimal is not
			// something to rank; leave the pair in the order SQL produced.
			return false
		}
		return ra.Cmp(rb) > 0
	})
	return out, nil
}

// MaxEpochsPerQuery caps a single range so one call cannot ask for an unbounded
// scan: a long-lived chain with short epochs accumulates tens of thousands of
// rows per validator.
const MaxEpochsPerQuery = 1000

// Epochs returns one validator's series, oldest first.
func (s *Store) Epochs(ctx context.Context, chainID, operator string, from, to uint64) ([]SnapshotRow, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT epoch, computed_score, computed_at, blocks_signed, blocks_missed,
		        reward_amount, reward_eligible, reward_reason, proposals
		   FROM epoch_snapshots
		  WHERE chain_id=? AND operator=? AND epoch BETWEEN ? AND ?
		  ORDER BY epoch ASC
		  LIMIT ?`, chainID, operator, from, to, MaxEpochsPerQuery)
	if err != nil {
		return nil, fmt.Errorf("epochs: %w", err)
	}
	defer func() { _ = rows.Close() }()

	out := []SnapshotRow{}
	for rows.Next() {
		r := SnapshotRow{Operator: operator}
		if err := rows.Scan(&r.Epoch, &r.ComputedScore, &r.ComputedAt, &r.BlocksSigned,
			&r.BlocksMissed, &r.RewardAmount, &r.RewardEligible, &r.RewardReason,
			&r.Proposals); err != nil {
			return nil, fmt.Errorf("epochs scan: %w", err)
		}
		out = append(out, r)
	}
	return out, rows.Err()
}
