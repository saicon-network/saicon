package store_test

import (
	"context"
	"database/sql"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	_ "modernc.org/sqlite"

	"github.com/saicon-network/saicon/indexer/internal/store"
)

func openTemp(t *testing.T) *store.Store {
	t.Helper()
	s, err := store.Open(filepath.Join(t.TempDir(), "test.db"))
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, s.Close()) })
	return s
}

func TestChainIDRoundTrip(t *testing.T) {
	ctx := context.Background()
	s := openTemp(t)

	_, ok, err := s.ChainID(ctx)
	require.NoError(t, err)
	require.False(t, ok, "a fresh database knows no chain")

	earliestBlockTime := time.Date(2026, 9, 22, 10, 0, 0, 0, time.UTC)
	require.NoError(t, s.SetChain(ctx, "saicon-devnet-1", earliestBlockTime))

	id, ok, err := s.ChainID(ctx)
	require.NoError(t, err)
	require.True(t, ok)
	require.Equal(t, "saicon-devnet-1", id)
}

func TestRecordBlockIsIdempotentUnderReplay(t *testing.T) {
	ctx := context.Background()
	s := openTemp(t)
	require.NoError(t, s.SetChain(ctx, "c1", time.Now()))

	// Heights 1..5 in epoch 0, all by the same proposer.
	for h := int64(1); h <= 5; h++ {
		require.NoError(t, s.RecordBlock(ctx, "c1", h, 0, "consA"))
	}
	h, err := s.IndexedHeight(ctx, "c1")
	require.NoError(t, err)
	require.Equal(t, int64(5), h)

	// A restart replays from an older cursor. Blocks already counted must not
	// be counted twice: this is exactly what a crash mid-epoch looks like.
	for h := int64(3); h <= 4; h++ {
		require.NoError(t, s.RecordBlock(ctx, "c1", h, 0, "consA"))
	}

	var count int64
	require.NoError(t, s.DB().QueryRowContext(ctx,
		`SELECT count FROM proposals_raw WHERE chain_id='c1' AND epoch=0 AND cons_addr='consA'`).Scan(&count))
	require.Equal(t, int64(5), count, "replayed blocks must not be counted twice")

	h, err = s.IndexedHeight(ctx, "c1")
	require.NoError(t, err)
	require.Equal(t, int64(5), h, "cursor stays at tip even after replaying earlier heights")
}

func TestRecordBlockRejectsUnknownChain(t *testing.T) {
	ctx := context.Background()
	s := openTemp(t)

	// RecordBlock should fail if the chain has never been registered.
	err := s.RecordBlock(ctx, "unknown-chain", 1, 0, "consA")
	require.Error(t, err)
	require.Contains(t, err.Error(), "unknown-chain")

	// Verify nothing was written to proposals_raw.
	var count int64
	err = s.DB().QueryRowContext(ctx,
		`SELECT COUNT(*) FROM proposals_raw WHERE chain_id='unknown-chain'`).Scan(&count)
	require.NoError(t, err)
	require.Equal(t, int64(0), count, "no proposals recorded for unknown chain")
}

func TestHighestEpochRead(t *testing.T) {
	ctx := context.Background()
	s := openTemp(t)
	require.NoError(t, s.SetChain(ctx, "c1", time.Now()))

	ep, ok, err := s.HighestEpochRead(ctx, "c1")
	require.NoError(t, err)
	require.False(t, ok, "an empty database has read no epoch")
	require.Equal(t, uint64(0), ep)

	// A snapshot row alone must not count as "read": HighestEpochRead answers
	// from epochs_read, which is only written once every validator's row for
	// an epoch has landed. Writing snapshots without marking the epoch must
	// leave HighestEpochRead unmoved.
	require.NoError(t, s.UpsertSnapshot(ctx, "c1", store.SnapshotRow{Epoch: 3, Operator: "op1"}))
	require.NoError(t, s.UpsertSnapshot(ctx, "c1", store.SnapshotRow{Epoch: 7, Operator: "op1"}))

	ep, ok, err = s.HighestEpochRead(ctx, "c1")
	require.NoError(t, err)
	require.False(t, ok, "snapshot rows alone do not mark an epoch read")
	require.Equal(t, uint64(0), ep)

	require.NoError(t, s.MarkEpochRead(ctx, "c1", 3))
	require.NoError(t, s.MarkEpochRead(ctx, "c1", 7))

	ep, ok, err = s.HighestEpochRead(ctx, "c1")
	require.NoError(t, err)
	require.True(t, ok)
	require.Equal(t, uint64(7), ep, "the highest epoch marked read, not the last one inserted")
}

func TestSeedCursorRaisesOnlyAndMovesTheWalkFloor(t *testing.T) {
	ctx := context.Background()
	s := openTemp(t)
	require.NoError(t, s.SetChain(ctx, "c1", time.Now()))

	h, err := s.FirstWalkedHeight(ctx, "c1")
	require.NoError(t, err)
	require.Equal(t, int64(1), h, "a fresh chain walked from genesis")

	require.NoError(t, s.SeedCursor(ctx, "c1", 4999))
	cursor, err := s.IndexedHeight(ctx, "c1")
	require.NoError(t, err)
	require.Equal(t, int64(4999), cursor)
	h, err = s.FirstWalkedHeight(ctx, "c1")
	require.NoError(t, err)
	require.Equal(t, int64(5000), h, "the walk floor moves with the seed, one past the seeded height")

	// A seed at or below the current cursor must not lower it: that would
	// re-open blocks RecordBlock has already counted to being recounted, and
	// would falsely lower the walk floor to a height nothing re-walked.
	require.NoError(t, s.SeedCursor(ctx, "c1", 100))
	cursor, err = s.IndexedHeight(ctx, "c1")
	require.NoError(t, err)
	require.Equal(t, int64(4999), cursor, "a lower seed must not lower the cursor")
	h, err = s.FirstWalkedHeight(ctx, "c1")
	require.NoError(t, err)
	require.Equal(t, int64(5000), h, "the walk floor stays put along with the cursor")
}

func TestEpochLengthRoundTripsAndIsNotOverwritten(t *testing.T) {
	ctx := context.Background()
	s := openTemp(t)
	require.NoError(t, s.SetChain(ctx, "c1", time.Now()))

	_, ok, err := s.StoredEpochLength(ctx, "c1")
	require.NoError(t, err)
	require.False(t, ok, "a fresh chain has no recorded epoch length")

	require.NoError(t, s.RecordEpochLength(ctx, "c1", 100))
	n, ok, err := s.StoredEpochLength(ctx, "c1")
	require.NoError(t, err)
	require.True(t, ok)
	require.Equal(t, uint64(100), n)

	// A second call — the process restarting against the same chain, still
	// reporting 100 — must not touch the stored value even with a different
	// number: overwriting it would move the baseline the mismatch check
	// compares against, silently accepting whatever the chain reports right
	// now as if it had always been that way.
	require.NoError(t, s.RecordEpochLength(ctx, "c1", 200))
	n, ok, err = s.StoredEpochLength(ctx, "c1")
	require.NoError(t, err)
	require.True(t, ok)
	require.Equal(t, uint64(100), n, "the first recorded length must not be overwritten")
}

// float64 carries about sixteen significant digits; the chain's sdk.Dec scores
// carry eighteen decimal places. CAST(computed_score AS REAL) collapsed two
// scores that differ only past the sixteenth digit to the same float, the tie
// fell through to the operator address, and the lower-scoring validator was
// served above the higher one. The two scored operators below are chosen so
// that address order is the OPPOSITE of score order: "zzz..." (higher score)
// sorts after "aaa..." (lower score) alphabetically, so a test that just
// checked "the higher score is first" could pass by accident if the ordering
// silently fell back to the address. It can't here.
func TestScoreboardOrdersByExactScoreNotFloat(t *testing.T) {
	ctx := context.Background()
	s := openTemp(t)
	require.NoError(t, s.SetChain(ctx, "c1", time.Now()))

	scoreLow := "1.000000000000000001"
	scoreHigh := "1.000000000000000002"

	require.NoError(t, s.UpsertValidator(ctx, "c1", store.ValidatorRow{
		Operator: "saiconvaloper1aaaaearly", Moniker: "low-but-early", StakingStatus: "BOND_STATUS_BONDED", Tokens: "1",
	}))
	require.NoError(t, s.UpsertSnapshot(ctx, "c1", store.SnapshotRow{
		Epoch: 1, Operator: "saiconvaloper1aaaaearly", ComputedScore: &scoreLow,
	}))

	require.NoError(t, s.UpsertValidator(ctx, "c1", store.ValidatorRow{
		Operator: "saiconvaloper1zzzzlate", Moniker: "high-but-late", StakingStatus: "BOND_STATUS_BONDED", Tokens: "1",
	}))
	require.NoError(t, s.UpsertSnapshot(ctx, "c1", store.SnapshotRow{
		Epoch: 1, Operator: "saiconvaloper1zzzzlate", ComputedScore: &scoreHigh,
	}))

	require.NoError(t, s.UpsertValidator(ctx, "c1", store.ValidatorRow{
		Operator: "saiconvaloper1mmmunscored", Moniker: "unscored", StakingStatus: "BOND_STATUS_BONDED", Tokens: "1",
	}))

	rows, err := s.Scoreboard(ctx, "c1")
	require.NoError(t, err)
	require.Len(t, rows, 3)

	require.Equal(t, "saiconvaloper1zzzzlate", rows[0].Operator, "higher score sorts first, despite a later address")
	require.Equal(t, "saiconvaloper1aaaaearly", rows[1].Operator, "lower score sorts second, despite an earlier address")
	require.Equal(t, "saiconvaloper1mmmunscored", rows[2].Operator, "unscored sorts last: not the bottom of the table, not part of it")
}

// A database written by an older build keeps its old shape: CREATE TABLE IF NOT
// EXISTS is a no-op against a table that already exists. Without a migration the
// first query naming a newer column fails with "no such column", which the binary
// reports as a corrupt database and exits on — for ever, against a volume that
// restart: unless-stopped never releases.
func TestOpenAddsColumnsMissingFromAnOlderDatabase(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "old.db")

	// The `chains` table exactly as the first version of this schema wrote it.
	old, err := sql.Open("sqlite", path)
	require.NoError(t, err)
	_, err = old.ExecContext(ctx, `CREATE TABLE chains (
	    chain_id       TEXT PRIMARY KEY,
	    indexed_height INTEGER NOT NULL DEFAULT 0,
	    first_seen     TEXT NOT NULL,
	    updated_at     TEXT NOT NULL
	);
	INSERT INTO chains VALUES ('c1', 4242, 't', 't');`)
	require.NoError(t, err)
	require.NoError(t, old.Close())

	s, err := store.Open(path)
	require.NoError(t, err, "an older database must be migrated, not rejected")
	t.Cleanup(func() { require.NoError(t, s.Close()) })

	// The rows it already held survive.
	h, err := s.IndexedHeight(ctx, "c1")
	require.NoError(t, err)
	require.Equal(t, int64(4242), h, "migrating must not lose the cursor")

	// The new columns answer, and answer "not recorded" rather than a value.
	_, ok, err := s.StoredEpochLength(ctx, "c1")
	require.NoError(t, err, "a missing column must read as absent, not as an error")
	require.False(t, ok, "an older database recorded no epoch length")

	from, err := s.FirstWalkedHeight(ctx, "c1")
	require.NoError(t, err)
	require.Equal(t, int64(1), from, "the default says the walk began at genesis")

	// And the migration is idempotent.
	require.NoError(t, s.Close())
	s2, err := store.Open(path)
	require.NoError(t, err)
	require.NoError(t, s2.Close())
	s, err = store.Open(path)
	require.NoError(t, err)
}
