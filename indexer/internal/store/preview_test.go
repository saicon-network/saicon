package store_test

import (
	"context"
	"database/sql"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/saicon-network/saicon/indexer/internal/store"
)

// R1: a database written before epoch_snapshots.warp existed gains the column
// through migrate(), keeps every row it held, and those rows read NULL: no WARP
// was read for them, which is not a WARP of 0.
func TestOpenAddsWarpToAnOlderEpochSnapshotsTable(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "old.db")

	// epoch_snapshots exactly as the build before S10 wrote it, with one row.
	old, err := sql.Open("sqlite", path)
	require.NoError(t, err)
	_, err = old.ExecContext(ctx, `CREATE TABLE epoch_snapshots (
	    chain_id        TEXT NOT NULL,
	    epoch           INTEGER NOT NULL,
	    operator        TEXT NOT NULL,
	    computed_score  TEXT,
	    computed_at     TEXT,
	    blocks_signed   INTEGER,
	    blocks_missed   INTEGER,
	    reward_amount   TEXT,
	    reward_eligible INTEGER,
	    reward_reason   TEXT,
	    proposals       INTEGER,
	    PRIMARY KEY (chain_id, epoch, operator)
	);
	INSERT INTO epoch_snapshots (chain_id, epoch, operator, computed_score)
	     VALUES ('c1', 4, 'saiconvaloper1old', '0.990000000000000000');`)
	require.NoError(t, err)
	require.NoError(t, old.Close())

	s, err := store.Open(path)
	require.NoError(t, err, "an older database must be migrated, not rejected")
	t.Cleanup(func() { require.NoError(t, s.Close()) })

	rows, err := s.Epochs(ctx, "c1", "saiconvaloper1old", 0, 100)
	require.NoError(t, err, "the new column must answer")
	require.Len(t, rows, 1)
	require.Equal(t, "0.990000000000000000", *rows[0].ComputedScore, "the old row survives")
	require.Nil(t, rows[0].Warp, "a row read before the column existed has no WARP: NULL")

	// A new epoch writes its WARP into the migrated table.
	require.NoError(t, s.UpsertSnapshot(ctx, "c1", store.SnapshotRow{
		Epoch: 5, Operator: "saiconvaloper1old", Warp: sp("82.000000000000000000"),
	}))
	rows, err = s.Epochs(ctx, "c1", "saiconvaloper1old", 0, 100)
	require.NoError(t, err)
	require.Len(t, rows, 2)
	require.Equal(t, "82.000000000000000000", *rows[1].Warp)

	// Idempotent: opening again adds nothing and fails on nothing.
	require.NoError(t, s.Close())
	s2, err := store.Open(path)
	require.NoError(t, err)
	require.NoError(t, s2.Close())
	s, err = store.Open(path)
	require.NoError(t, err)
}

func TestPreviewHeadAndTheBoardJoin(t *testing.T) {
	ctx := context.Background()
	s := openTemp(t)
	require.NoError(t, s.SetChain(ctx, "c1", time.Now()))
	for _, op := range []string{"saiconvaloper1a", "saiconvaloper1b", "saiconvaloper1c"} {
		require.NoError(t, s.UpsertValidator(ctx, "c1", store.ValidatorRow{Operator: op, StakingStatus: "BOND_STATUS_BONDED", Tokens: "1"}))
	}
	require.NoError(t, s.UpsertSnapshot(ctx, "c1", store.SnapshotRow{Epoch: 6, Operator: "saiconvaloper1a",
		ComputedScore: sp("1.000000000000000000"), Warp: sp("86.878000000000000000")}))
	require.NoError(t, s.UpsertSnapshot(ctx, "c1", store.SnapshotRow{Epoch: 6, Operator: "saiconvaloper1b",
		ComputedScore: sp("0.000000000000000000"), Warp: sp("28.000000000000000000")}))

	// Nothing read yet: every part of the head is absent.
	b, err := s.Board(ctx, "c1")
	require.NoError(t, err)
	require.Nil(t, b.Head.Epoch)
	require.Nil(t, b.Head.Threshold)
	require.Nil(t, b.Head.Latest)
	require.Nil(t, b.Head.Unavailable)
	for _, r := range b.Rows {
		require.Nil(t, r.Preview)
	}

	// Epoch 5 read, epoch 6 unavailable.
	require.NoError(t, s.WritePreview(ctx, "c1", store.PreviewEpochRow{Epoch: 5, Height: 50,
		TotalVP: sp("85420000000000"), Threshold: sp("65.000000000000000000"), Cap: sp("200000000000000")},
		[]store.PreviewRow{
			{Operator: "saiconvaloper1a", VP: "85420000000000", VPShare: "1.000000", StockShare: "0.980000",
				Eligible: true, Warp: "85.420000000000000000", WarpMeasured: true},
			{Operator: "saiconvaloper1b", VP: "560000000000", VPShare: "0", StockShare: "0.020000",
				Eligible: false, Reason: sp("warp below threshold"), Warp: "28.000000000000000000", WarpMeasured: true},
		}))
	require.NoError(t, s.WritePreview(ctx, "c1", store.PreviewEpochRow{Epoch: 6, Height: 60, Unavailable: sp("pruned")}, nil))

	b, err = s.Board(ctx, "c1")
	require.NoError(t, err)
	require.Equal(t, uint64(5), *b.Head.Epoch, "the newest AVAILABLE preview")
	require.Equal(t, "65.000000000000000000", *b.Head.Threshold)
	require.Equal(t, uint64(6), *b.Head.Latest)
	require.Equal(t, "pruned", *b.Head.Unavailable, "the newest epoch's code")

	by := map[string]store.ScoreRow{}
	for _, r := range b.Rows {
		by[r.Operator] = r
	}
	a := by["saiconvaloper1a"]
	require.Equal(t, "86.878000000000000000", *a.Warp, "the latest snapshot's WARP, not the preview's")
	require.NotNil(t, a.Preview)
	require.Equal(t, "1.000000", a.Preview.VPShare)
	require.True(t, a.Preview.Eligible)
	require.Nil(t, a.Preview.Reason)

	bb := by["saiconvaloper1b"]
	require.Equal(t, "0", bb.Preview.VPShare)
	require.False(t, bb.Preview.Eligible)
	require.Equal(t, "warp below threshold", *bb.Preview.Reason)

	c := by["saiconvaloper1c"]
	require.Nil(t, c.Warp, "no snapshot: no WARP")
	require.Nil(t, c.Preview, "not in the preview: no preview, not a zero one")

	// A newer available epoch replaces the head and the joined rows; reading an
	// epoch again replaces its rows rather than adding to them.
	require.NoError(t, s.WritePreview(ctx, "c1", store.PreviewEpochRow{Epoch: 7, Height: 70,
		TotalVP: sp("1"), Threshold: sp("60.000000000000000000"), Cap: sp("1")},
		[]store.PreviewRow{{Operator: "saiconvaloper1c", VP: "1", VPShare: "1.000000", StockShare: "1.000000",
			Eligible: true, Warp: "80.000000000000000000"}}))
	require.NoError(t, s.WritePreview(ctx, "c1", store.PreviewEpochRow{Epoch: 7, Height: 70,
		TotalVP: sp("1"), Threshold: sp("60.000000000000000000"), Cap: sp("1")},
		[]store.PreviewRow{{Operator: "saiconvaloper1c", VP: "1", VPShare: "1.000000", StockShare: "1.000000",
			Eligible: true, Warp: "80.000000000000000000"}}))
	b, err = s.Board(ctx, "c1")
	require.NoError(t, err)
	require.Equal(t, uint64(7), *b.Head.Epoch)
	require.Nil(t, b.Head.Unavailable, "the newest epoch was read")
	require.Equal(t, "60.000000000000000000", *b.Head.Threshold)
	for _, r := range b.Rows {
		if r.Operator == "saiconvaloper1c" {
			require.NotNil(t, r.Preview)
		} else {
			require.Nil(t, r.Preview, "%s is not in epoch 7's preview", r.Operator)
		}
	}
	var n int
	require.NoError(t, s.DB().QueryRowContext(ctx, `SELECT COUNT(*) FROM vp_preview WHERE epoch = 7`).Scan(&n))
	require.Equal(t, 1, n)

	// An unavailable epoch with validator rows is refused, not half-written.
	require.Error(t, s.WritePreview(ctx, "c1", store.PreviewEpochRow{Epoch: 8, Height: 80, Unavailable: sp("error")},
		[]store.PreviewRow{{Operator: "saiconvaloper1a", VP: "1", VPShare: "1", StockShare: "1", Warp: "1"}}))
}

// M1: the due list. An epoch is listed once, its failure count survives being
// listed again, the newest comes first, and writing the epoch's outcome takes
// it off the list.
func TestThePreviewDueList(t *testing.T) {
	ctx := context.Background()
	s := openTemp(t)
	require.NoError(t, s.SetChain(ctx, "c1", time.Now()))
	for _, e := range []uint64{3, 5, 4} {
		require.NoError(t, s.MarkPreviewDue(ctx, "c1", e))
	}
	n, err := s.PreviewFailed(ctx, "c1", 4)
	require.NoError(t, err)
	require.Equal(t, 1, n)
	n, err = s.PreviewFailed(ctx, "c1", 4)
	require.NoError(t, err)
	require.Equal(t, 2, n)
	require.NoError(t, s.MarkPreviewDue(ctx, "c1", 4), "listing again is not an error")

	d, err := s.DuePreviews(ctx, "c1", 10)
	require.NoError(t, err)
	require.Equal(t, []store.DuePreview{{Epoch: 5}, {Epoch: 4, Attempts: 2}, {Epoch: 3}}, d, "newest first, count kept")

	require.NoError(t, s.WritePreview(ctx, "c1", store.PreviewEpochRow{Epoch: 4, Height: 40, Unavailable: sp("pruned")}, nil))
	require.NoError(t, s.WritePreview(ctx, "c1", store.PreviewEpochRow{Epoch: 5, Height: 50,
		TotalVP: sp("1"), Threshold: sp("65"), Cap: sp("1")}, nil))
	d, err = s.DuePreviews(ctx, "c1", 10)
	require.NoError(t, err)
	require.Equal(t, []store.DuePreview{{Epoch: 3}}, d)

	// An epoch that holds a preview is not listed again; one with a code is.
	require.NoError(t, s.MarkPreviewDue(ctx, "c1", 5))
	require.NoError(t, s.MarkPreviewDue(ctx, "c1", 4))
	d, err = s.DuePreviews(ctx, "c1", 10)
	require.NoError(t, err)
	require.Equal(t, []store.DuePreview{{Epoch: 4}, {Epoch: 3}}, d)
}

// L1: a code never replaces a stored preview, and never deletes its rows. A
// preview replaces a code, and a code replaces a code.
func TestWritePreviewNeverDowngrades(t *testing.T) {
	ctx := context.Background()
	s := openTemp(t)
	require.NoError(t, s.SetChain(ctx, "c1", time.Now()))
	avail := store.PreviewEpochRow{Epoch: 7, Height: 70, TotalVP: sp("5"), Threshold: sp("65.000000000000000000"), Cap: sp("9")}
	rows := []store.PreviewRow{{Operator: "saiconvaloper1a", VP: "5", VPShare: "1.000000", StockShare: "1.000000",
		Eligible: true, Warp: "80"}}
	state := func() (code sql.NullString, total sql.NullString, n int) {
		require.NoError(t, s.DB().QueryRowContext(ctx,
			`SELECT unavailable, total_vp FROM vp_preview_epochs WHERE chain_id='c1' AND epoch=7`).Scan(&code, &total))
		require.NoError(t, s.DB().QueryRowContext(ctx, `SELECT COUNT(*) FROM vp_preview WHERE epoch=7`).Scan(&n))
		return
	}

	require.NoError(t, s.WritePreview(ctx, "c1", store.PreviewEpochRow{Epoch: 7, Height: 70, Unavailable: sp("route_missing")}, nil))
	require.NoError(t, s.WritePreview(ctx, "c1", store.PreviewEpochRow{Epoch: 7, Height: 70, Unavailable: sp("pruned")}, nil))
	c, _, n := state()
	require.Equal(t, "pruned", c.String, "a code replaces a code")
	require.Zero(t, n)

	require.NoError(t, s.WritePreview(ctx, "c1", avail, rows))
	c, total, n := state()
	require.False(t, c.Valid, "a preview replaces a code")
	require.Equal(t, "5", total.String)
	require.Equal(t, 1, n)

	for _, code := range []string{"pruned", "error", "route_missing"} {
		require.NoError(t, s.MarkPreviewDue(ctx, "c1", 7))
		require.NoError(t, s.WritePreview(ctx, "c1", store.PreviewEpochRow{Epoch: 7, Height: 70, Unavailable: sp(code)}, nil))
		c, total, n = state()
		require.False(t, c.Valid, code+" must not replace the preview")
		require.Equal(t, "5", total.String)
		require.Equal(t, 1, n, code+" must not delete the rows")
	}
	d, err := s.DuePreviews(ctx, "c1", 10)
	require.NoError(t, err)
	require.Empty(t, d, "the refused write still takes the epoch off the list")
}

// L7: each epoch of a validator's history carries its own preview's threshold,
// and null when that epoch has no available preview.
func TestEpochsCarryTheirOwnPreviewThreshold(t *testing.T) {
	ctx := context.Background()
	s := openTemp(t)
	require.NoError(t, s.SetChain(ctx, "c1", time.Now()))
	const op = "saiconvaloper1a"
	for e := uint64(1); e <= 3; e++ {
		require.NoError(t, s.UpsertSnapshot(ctx, "c1", store.SnapshotRow{Epoch: e, Operator: op, Warp: sp("66.000000000000000000")}))
	}
	require.NoError(t, s.WritePreview(ctx, "c1", store.PreviewEpochRow{Epoch: 1, Height: 10,
		TotalVP: sp("1"), Threshold: sp("70.000000000000000000"), Cap: sp("1")}, nil))
	require.NoError(t, s.WritePreview(ctx, "c1", store.PreviewEpochRow{Epoch: 2, Height: 20, Unavailable: sp("pruned")}, nil))

	eps, err := s.Epochs(ctx, "c1", op, 1, 3)
	require.NoError(t, err)
	require.Len(t, eps, 3)
	require.Equal(t, "70.000000000000000000", *eps[0].PreviewThreshold)
	require.Nil(t, eps[1].PreviewThreshold, "an unavailable preview has no threshold")
	require.Nil(t, eps[2].PreviewThreshold, "no preview row at all")
}
