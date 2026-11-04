package store_test

import (
	"context"
	"database/sql"
	"fmt"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/saicon-network/saicon/indexer/internal/store"
)

func sp(s string) *string { return &s }
func ip(n int64) *int64   { return &n }

// readRow is a fully measured epoch whose block hash is derived from the epoch.
func readRow(epoch uint64) store.ConcentrationRow {
	return store.ConcentrationRow{
		Epoch: epoch, Height: int64(epoch) * 100, BlockHash: sp(fmt.Sprintf("HASH%d", epoch)),
		SetSize: ip(3), TotalPower: ip(600), Top10Power: ip(600), NakamotoValidators: ip(1),
		UnitCount: ip(2), NakamotoEntities: ip(1), EntityCount: ip(0),
		EntityCap: sp("1000"), MaxActiveSeats: ip(4),
		ReadAt: "2026-10-29T10:00:00Z",
	}
}

func threePowers() []store.PowerRow {
	return []store.PowerRow{
		{ConsAddr: "AA", Operator: sp("op1"), Power: 100, Unit: sp("val:op1")},
		{ConsAddr: "BB", Operator: sp("op2"), Power: 300, Unit: sp("val:op2")},
		{ConsAddr: "CC", Operator: sp("op3"), Power: 300, Unit: sp("val:op3")},
	}
}

func count(t *testing.T, s *store.Store, table string) int {
	t.Helper()
	var n int
	require.NoError(t, s.DB().QueryRowContext(context.Background(), `SELECT COUNT(*) FROM `+table).Scan(&n))
	return n
}

func TestAnOldDatabaseGainsTheTables(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "s6.db")

	// The S6 schema only: five tables and the one index, with real rows in them.
	old, err := sql.Open("sqlite", path)
	require.NoError(t, err)
	_, err = old.ExecContext(ctx, `
CREATE TABLE chains (
    chain_id            TEXT PRIMARY KEY,
    indexed_height      INTEGER NOT NULL DEFAULT 0,
    first_walked_height INTEGER NOT NULL DEFAULT 1,
    epoch_length        INTEGER,
    earliest_block_time TEXT,
    first_seen          TEXT NOT NULL,
    updated_at          TEXT NOT NULL
);
CREATE TABLE validators (
    chain_id             TEXT NOT NULL,
    operator             TEXT NOT NULL,
    moniker              TEXT,
    staking_status       TEXT,
    jailed               INTEGER,
    tokens               TEXT,
    commission_rate      TEXT,
    registry_status      TEXT,
    compliance_flag      INTEGER,
    bonding_start        TEXT,
    bonding_start_height INTEGER,
    lockup_seconds       INTEGER,
    accumulated_ep       TEXT,
    last_epoch_update    INTEGER,
    updated_at           TEXT NOT NULL,
    PRIMARY KEY (chain_id, operator)
);
CREATE TABLE epoch_snapshots (
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
CREATE TABLE proposals_raw (
    chain_id  TEXT NOT NULL,
    epoch     INTEGER NOT NULL,
    cons_addr TEXT NOT NULL,
    count     INTEGER NOT NULL DEFAULT 0,
    PRIMARY KEY (chain_id, epoch, cons_addr)
);
CREATE INDEX idx_snapshots_operator ON epoch_snapshots (chain_id, operator, epoch);
CREATE TABLE epochs_read (
    chain_id     TEXT NOT NULL,
    epoch        INTEGER NOT NULL,
    completed_at TEXT NOT NULL,
    PRIMARY KEY (chain_id, epoch)
);
INSERT INTO chains VALUES ('c1', 4242, 1, 100, 't', 't', 't');
INSERT INTO validators (chain_id, operator, moniker, updated_at) VALUES ('c1', 'op1', 'alpha', 't');
INSERT INTO proposals_raw VALUES ('c1', 3, 'AA', 17);
INSERT INTO epochs_read VALUES ('c1', 3, 't');`)
	require.NoError(t, err)
	require.NoError(t, old.Close())

	s, err := store.Open(path)
	require.NoError(t, err)
	t.Cleanup(func() { _ = s.Close() })

	_, ok, err := s.HighestConcentrationEpoch(ctx, "c1")
	require.NoError(t, err)
	require.False(t, ok, "a database that never measured has no cursor")

	require.NoError(t, s.WriteConcentration(ctx, "c1", readRow(5), threePowers(), nil))
	e, ok, err := s.HighestConcentrationEpoch(ctx, "c1")
	require.NoError(t, err)
	require.True(t, ok)
	require.Equal(t, uint64(5), e)

	// The rows the old database already held are untouched.
	h, err := s.IndexedHeight(ctx, "c1")
	require.NoError(t, err)
	require.Equal(t, int64(4242), h)
	n, err := s.ProposalsFor(ctx, "c1", 3, "AA")
	require.NoError(t, err)
	require.Equal(t, int64(17), n)
	require.Equal(t, 1, count(t, s, "validators"))
	require.Equal(t, 1, count(t, s, "epochs_read"))

	// And the monikers of the old table join in on read.
	_, powers, err := s.Concentration(ctx, "c1", nil)
	require.NoError(t, err)
	require.Len(t, powers, 3)
	require.NotNil(t, powers[2].Moniker, "op1 is in validators")
	require.Equal(t, "alpha", *powers[2].Moniker)
	require.Nil(t, powers[0].Moniker, "op2 is not in validators: NULL, not an empty string")
}

func TestWriteIsOneTransaction(t *testing.T) {
	ctx := context.Background()
	s := openTemp(t)
	dup := append(threePowers(), store.PowerRow{ConsAddr: "AA", Power: 1})
	ents := []store.EntityRow{{EntityID: "a", DisplayName: "A", Status: "ENTITY_STATUS_ATTESTED"}}

	require.Error(t, s.WriteConcentration(ctx, "c1", readRow(5), dup, ents))

	_, ok, err := s.HighestConcentrationEpoch(ctx, "c1")
	require.NoError(t, err)
	require.False(t, ok)
	for _, tbl := range []string{"concentration_epochs", "power_snapshots", "entity_snapshots", "entity_seats"} {
		require.Zero(t, count(t, s, tbl), tbl)
	}
}

func TestRewriteReplaces(t *testing.T) {
	ctx := context.Background()
	s := openTemp(t)
	require.NoError(t, s.WriteConcentration(ctx, "c1", readRow(5), threePowers(), nil))
	require.NoError(t, s.WriteConcentration(ctx, "c1", readRow(5), threePowers()[:2], nil))

	require.Equal(t, 1, count(t, s, "concentration_epochs"))
	_, powers, err := s.Concentration(ctx, "c1", nil)
	require.NoError(t, err)
	require.Len(t, powers, 2)
}

func TestRewriteReplacesEntitiesAndSeats(t *testing.T) {
	ctx := context.Background()
	s := openTemp(t)
	ents := []store.EntityRow{{
		EntityID: "a", DisplayName: "A", Status: "ENTITY_STATUS_ATTESTED",
		Seats: []store.SeatRow{{Operator: "op1", Role: "member"}, {Operator: "op2", Role: "member"}},
	}}
	require.NoError(t, s.WriteConcentration(ctx, "c1", readRow(5), nil, ents))
	ents[0].Seats = ents[0].Seats[:1]
	require.NoError(t, s.WriteConcentration(ctx, "c1", readRow(5), nil, ents))
	require.Equal(t, 1, count(t, s, "entity_snapshots"))
	require.Equal(t, 1, count(t, s, "entity_seats"))
}

func TestWriteIsScopedToItsChain(t *testing.T) {
	ctx := context.Background()
	s := openTemp(t)
	require.NoError(t, s.WriteConcentration(ctx, "c1", readRow(5), threePowers(), nil))
	require.NoError(t, s.WriteConcentration(ctx, "c2", readRow(5), threePowers()[:1], nil))
	require.NoError(t, s.WriteConcentration(ctx, "c2", readRow(5), threePowers()[:1], nil))

	_, p1, err := s.Concentration(ctx, "c1", nil)
	require.NoError(t, err)
	require.Len(t, p1, 3, "rewriting c2 must not touch c1")
}

func TestDetailIsPrunedAfter2000Epochs(t *testing.T) {
	ctx := context.Background()
	s := openTemp(t)
	ents := []store.EntityRow{{
		EntityID: "a", DisplayName: "A", Status: "ENTITY_STATUS_ATTESTED",
		Seats: []store.SeatRow{{Operator: "op1", Role: "member"}},
	}}
	require.NoError(t, s.WriteConcentration(ctx, "c1", readRow(1), threePowers(), ents))
	require.NoError(t, s.WriteConcentration(ctx, "c1", readRow(2001), threePowers(), ents))

	// Epoch 1 is exactly DetailRetentionEpochs behind 2001: detail gone, row stays.
	row, powers, err := s.Concentration(ctx, "c1", ptr(uint64(1)))
	require.NoError(t, err)
	require.NotNil(t, row)
	require.Empty(t, powers)
	_, ents1, err := s.Entities(ctx, "c1", ptr(uint64(1)))
	require.NoError(t, err)
	require.Empty(t, ents1)
	require.Equal(t, 2, count(t, s, "concentration_epochs"))
	require.Equal(t, 3, count(t, s, "power_snapshots"))
	require.Equal(t, 1, count(t, s, "entity_snapshots"))
	require.Equal(t, 1, count(t, s, "entity_seats"))

	// 2000 epochs behind is pruned, 1999 behind is kept.
	require.NoError(t, s.WriteConcentration(ctx, "c1", readRow(2000), threePowers(), nil))
	require.NoError(t, s.WriteConcentration(ctx, "c1", readRow(3999), threePowers(), nil))
	_, p2000, err := s.Concentration(ctx, "c1", ptr(uint64(2000)))
	require.NoError(t, err)
	require.Len(t, p2000, 3, "epoch 2000 is 1999 behind 3999")
	require.NoError(t, s.WriteConcentration(ctx, "c1", readRow(4000), threePowers(), nil))
	_, p2000, err = s.Concentration(ctx, "c1", ptr(uint64(2000)))
	require.NoError(t, err)
	require.Empty(t, p2000)
}

func TestSmallEpochsDoNotUnderflowThePruneBound(t *testing.T) {
	ctx := context.Background()
	s := openTemp(t)
	require.NoError(t, s.WriteConcentration(ctx, "c1", readRow(0), threePowers(), nil))
	require.NoError(t, s.WriteConcentration(ctx, "c1", readRow(1), threePowers(), nil))
	require.Equal(t, 6, count(t, s, "power_snapshots"))
}

func TestPruningIsScopedToItsChain(t *testing.T) {
	ctx := context.Background()
	s := openTemp(t)
	require.NoError(t, s.WriteConcentration(ctx, "c2", readRow(1), threePowers(), nil))
	require.NoError(t, s.WriteConcentration(ctx, "c1", readRow(5000), threePowers(), nil))
	_, p, err := s.Concentration(ctx, "c2", ptr(uint64(1)))
	require.NoError(t, err)
	require.Len(t, p, 3)
}

func ptr[T any](v T) *T { return &v }

func TestLatestSkipsUnavailableRows(t *testing.T) {
	ctx := context.Background()
	s := openTemp(t)
	require.NoError(t, s.WriteConcentration(ctx, "c1", readRow(7), threePowers(), nil))
	r8 := store.ConcentrationRow{Epoch: 8, Height: 800, PowerUnavailable: sp("pruned"), ReadAt: "t"}
	require.NoError(t, s.WriteConcentration(ctx, "c1", r8, nil, nil))

	row, _, err := s.Concentration(ctx, "c1", nil)
	require.NoError(t, err)
	require.NotNil(t, row)
	require.Equal(t, uint64(7), row.Epoch)

	row, powers, err := s.Concentration(ctx, "c1", ptr(uint64(8)))
	require.NoError(t, err)
	require.Nil(t, row)
	require.Nil(t, powers)

	row, _, err = s.Concentration(ctx, "c1", ptr(uint64(99)))
	require.NoError(t, err)
	require.Nil(t, row)

	series, err := s.ConcentrationSeries(ctx, "c1", 0, 100)
	require.NoError(t, err)
	require.Len(t, series, 2)
	require.Equal(t, uint64(7), series[0].Epoch)
	require.Equal(t, uint64(8), series[1].Epoch)
	require.NotNil(t, series[1].PowerUnavailable)
	require.Equal(t, "pruned", *series[1].PowerUnavailable)

	hi, ok, err := s.HighestConcentrationEpoch(ctx, "c1")
	require.NoError(t, err)
	require.True(t, ok)
	require.Equal(t, uint64(8), hi, "an unavailable row is still the cursor")
}

func TestNoMeasuredEpochIsNilNilNil(t *testing.T) {
	ctx := context.Background()
	s := openTemp(t)
	row, powers, err := s.Concentration(ctx, "c1", nil)
	require.NoError(t, err)
	require.Nil(t, row)
	require.Nil(t, powers)
	row, ents, err := s.Entities(ctx, "c1", nil)
	require.NoError(t, err)
	require.Nil(t, row)
	require.Nil(t, ents)
}

func TestPowersAreOrderedByPowerThenConsAddr(t *testing.T) {
	ctx := context.Background()
	s := openTemp(t)
	in := []store.PowerRow{
		{ConsAddr: "CC", Power: 5}, {ConsAddr: "BB", Power: 9}, {ConsAddr: "AA", Power: 5},
	}
	require.NoError(t, s.WriteConcentration(ctx, "c1", readRow(1), in, nil))
	_, out, err := s.Concentration(ctx, "c1", nil)
	require.NoError(t, err)
	require.Equal(t, []string{"BB", "AA", "CC"}, []string{out[0].ConsAddr, out[1].ConsAddr, out[2].ConsAddr})
}

func TestEntitiesNewestReadAndOrdered(t *testing.T) {
	ctx := context.Background()
	s := openTemp(t)
	frozen := true
	ents := []store.EntityRow{
		{EntityID: "b", DisplayName: "B", Status: "ENTITY_STATUS_ATTESTED", Tokens: sp("200")},
		{EntityID: "a", DisplayName: "A", Status: "ENTITY_STATUS_ATTESTED", Tokens: sp("300"),
			Frozen: &frozen, FrozenReason: sp("why"),
			Seats: []store.SeatRow{
				{Operator: "op9", Role: "charged_frozen", FrozenHeight: ip(40), FrozenReason: sp("x")},
				{Operator: "op2", Role: "member", RegistryStatus: sp("ACTIVE"), Tokens: sp("7"), Power: ip(3)},
				{Operator: "op1", Role: "member"},
			}},
		{EntityID: "r", DisplayName: "R", Status: "ENTITY_STATUS_REVOKED"},
	}
	require.NoError(t, s.WriteConcentration(ctx, "c1", readRow(9), threePowers(), ents))
	r10 := store.ConcentrationRow{
		Epoch: 10, Height: 1000, SetSize: ip(3), ReadAt: "t",
		EntitiesUnavailable: sp("frozen_count_mismatch: a: chain 2, found 1"),
	}
	require.NoError(t, s.WriteConcentration(ctx, "c1", r10, threePowers(), nil))

	row, out, err := s.Entities(ctx, "c1", nil)
	require.NoError(t, err)
	require.NotNil(t, row)
	require.Equal(t, uint64(9), row.Epoch)
	require.Len(t, out, 3)
	require.Equal(t, []string{"a", "b", "r"}, []string{out[0].EntityID, out[1].EntityID, out[2].EntityID})

	require.NotNil(t, out[0].Frozen)
	require.True(t, *out[0].Frozen)
	require.Nil(t, out[1].Frozen, "an unwritten frozen flag stays NULL")
	seats := out[0].Seats
	require.Len(t, seats, 3)
	require.Equal(t, []string{"op1", "op2", "op9"}, []string{seats[0].Operator, seats[1].Operator, seats[2].Operator})
	require.Equal(t, "charged_frozen", seats[2].Role)
	require.Equal(t, int64(40), *seats[2].FrozenHeight)
	require.Empty(t, out[1].Seats)

	// An explicit unavailable epoch is not served as data.
	row, out, err = s.Entities(ctx, "c1", ptr(uint64(10)))
	require.NoError(t, err)
	require.Nil(t, row)
	require.Nil(t, out)

	// But its power half is.
	row, p, err := s.Concentration(ctx, "c1", nil)
	require.NoError(t, err)
	require.Equal(t, uint64(10), row.Epoch)
	require.Len(t, p, 3)
}

func TestEntitiesOrderTokensExactly(t *testing.T) {
	ctx := context.Background()
	s := openTemp(t)
	// Differ only past float64 precision, and sort wrongly as strings ("9" > "10").
	ents := []store.EntityRow{
		{EntityID: "lo", DisplayName: "L", Status: "S", Tokens: sp("100000000000000000001")},
		{EntityID: "hi", DisplayName: "H", Status: "S", Tokens: sp("100000000000000000002")},
		{EntityID: "nine", DisplayName: "N", Status: "S", Tokens: sp("9")},
		{EntityID: "ten", DisplayName: "T", Status: "S", Tokens: sp("10")},
		{EntityID: "tie2", DisplayName: "T", Status: "S", Tokens: sp("5")},
		{EntityID: "tie1", DisplayName: "T", Status: "S", Tokens: sp("5")},
		{EntityID: "none", DisplayName: "N", Status: "S"},
	}
	require.NoError(t, s.WriteConcentration(ctx, "c1", readRow(1), nil, ents))
	_, out, err := s.Entities(ctx, "c1", nil)
	require.NoError(t, err)
	var ids []string
	for _, e := range out {
		ids = append(ids, e.EntityID)
	}
	require.Equal(t, []string{"hi", "lo", "ten", "nine", "tie1", "tie2", "none"}, ids)
}

func TestNullsStayNull(t *testing.T) {
	ctx := context.Background()
	s := openTemp(t)
	bare := store.ConcentrationRow{Epoch: 4, Height: 400, ReadAt: "t"}
	powers := []store.PowerRow{{ConsAddr: "AA", Power: 1}}
	ents := []store.EntityRow{{
		EntityID: "a", DisplayName: "A", Status: "S",
		Seats: []store.SeatRow{{Operator: "op", Role: "member"}},
	}}
	require.NoError(t, s.WriteConcentration(ctx, "c1", bare, powers, ents))

	series, err := s.ConcentrationSeries(ctx, "c1", 0, 10)
	require.NoError(t, err)
	require.Len(t, series, 1)
	require.Equal(t, store.ConcentrationRow{Epoch: 4, Height: 400, ReadAt: "t"}, series[0])

	_, p, err := s.Concentration(ctx, "c1", nil)
	require.NoError(t, err)
	require.Equal(t, []store.PowerRow{{ConsAddr: "AA", Power: 1}}, p)

	// The entity part of this row was never marked unavailable, so it reads.
	_, e, err := s.Entities(ctx, "c1", nil)
	require.NoError(t, err)
	require.Equal(t, []store.EntityRow{{
		EntityID: "a", DisplayName: "A", Status: "S",
		Seats: []store.SeatRow{{Operator: "op", Role: "member"}},
	}}, e)
}

func TestZeroStaysZero(t *testing.T) {
	ctx := context.Background()
	s := openTemp(t)
	row := store.ConcentrationRow{
		Epoch: 4, Height: 400, SetSize: ip(0), TotalPower: ip(0), EntityCap: sp("0"), ReadAt: "t",
	}
	require.NoError(t, s.WriteConcentration(ctx, "c1", row, nil, nil))
	got, _, err := s.Concentration(ctx, "c1", nil)
	require.NoError(t, err)
	require.Equal(t, int64(0), *got.SetSize)
	require.Equal(t, "0", *got.EntityCap)
	require.Nil(t, got.NakamotoValidators)
}

func TestSeriesIsCappedAt1000(t *testing.T) {
	ctx := context.Background()
	s := openTemp(t)
	tx, err := s.DB().BeginTx(ctx, nil)
	require.NoError(t, err)
	for e := 1; e <= 1005; e++ {
		_, err := tx.ExecContext(ctx,
			`INSERT INTO concentration_epochs (chain_id, epoch, height, read_at) VALUES ('c1', ?, ?, 't')`, e, e)
		require.NoError(t, err)
	}
	require.NoError(t, tx.Commit())

	series, err := s.ConcentrationSeries(ctx, "c1", 0, 2000)
	require.NoError(t, err)
	require.Len(t, series, 1000)
	require.Equal(t, uint64(1), series[0].Epoch)
	require.Equal(t, uint64(1000), series[999].Epoch)
}

func TestNewestHashedEpoch(t *testing.T) {
	ctx := context.Background()
	s := openTemp(t)
	_, _, ok, err := s.NewestHashedEpoch(ctx, "c1")
	require.NoError(t, err)
	require.False(t, ok)

	require.NoError(t, s.WriteConcentration(ctx, "c1", readRow(3), threePowers(), nil))
	r4 := readRow(4)
	r4.BlockHash = nil
	require.NoError(t, s.WriteConcentration(ctx, "c1", r4, threePowers(), nil))

	h, hash, ok, err := s.NewestHashedEpoch(ctx, "c1")
	require.NoError(t, err)
	require.True(t, ok)
	require.Equal(t, int64(300), h)
	require.Equal(t, "HASH3", hash)
}

// A rewrite that fails part-way (here: a second row for the same entity) rolls
// back as a whole: the epoch keeps exactly what it held before, in all four
// tables. The lazy re-read of no_entity_layer epochs depends on this.
func TestAFailedRewriteKeepsTheOldRows(t *testing.T) {
	ctx := context.Background()
	s := openTemp(t)
	old := readRow(5)
	old.EntitiesUnavailable = sp("no_entity_layer")
	old.UnitCount, old.NakamotoEntities, old.EntityCount, old.EntityCap, old.MaxActiveSeats = nil, nil, nil, nil, nil
	require.NoError(t, s.WriteConcentration(ctx, "c1", old, threePowers(), nil))

	fresh := readRow(5)
	fresh.EntityCount = ip(2)
	dup := []store.EntityRow{
		{EntityID: "e", DisplayName: "E", Status: "ENTITY_STATUS_ATTESTED", Tokens: sp("1"),
			Seats: []store.SeatRow{{Operator: "op1", Role: "member"}}},
		{EntityID: "e", DisplayName: "E again", Status: "ENTITY_STATUS_ATTESTED"},
	}
	err := s.WriteConcentration(ctx, "c1", fresh, threePowers()[:1], dup)
	require.Error(t, err)

	row, powers, err := s.Concentration(ctx, "c1", ptr(uint64(5)))
	require.NoError(t, err)
	require.NotNil(t, row)
	require.Equal(t, "no_entity_layer", str(row.EntitiesUnavailable))
	require.Nil(t, row.EntityCount)
	require.Len(t, powers, 3)
	require.Equal(t, 1, count(t, s, "concentration_epochs"))
	require.Equal(t, 0, count(t, s, "entity_snapshots"))
	require.Equal(t, 0, count(t, s, "entity_seats"))
}

// Tokens are written verbatim from the chain; a value that is not an integer is
// bad data, and ordering it silently as "no tokens" would hide that.
func TestUnparseableTokensAreAnError(t *testing.T) {
	ctx := context.Background()
	s := openTemp(t)
	ents := []store.EntityRow{
		{EntityID: "b", DisplayName: "B", Status: "S", Tokens: sp("3")},
	}
	require.NoError(t, s.WriteConcentration(ctx, "c1", readRow(1), nil, ents))
	// WriteConcentration refuses such a row; a database written another way
	// (by hand, or by an older build) is still read with an error, not a guess.
	_, err := s.DB().ExecContext(ctx, `INSERT INTO entity_snapshots (chain_id, epoch, entity_id, display_name, status, tokens)
		VALUES ('c1', 1, 'a', 'A', 'S', '12x')`)
	require.NoError(t, err)
	row, out, err := s.Entities(ctx, "c1", nil)
	require.Error(t, err)
	require.Contains(t, err.Error(), "12x")
	require.Nil(t, row)
	require.Nil(t, out)
}

func str(p *string) string {
	if p == nil {
		return "<nil>"
	}
	return *p
}

// Why the callers clamp: the driver itself refuses the largest uint64 as a
// query argument. If this ever starts passing, the clamps are no longer needed.
func TestTheDriverRefusesAHighBitEpoch(t *testing.T) {
	ctx := context.Background()
	s := openTemp(t)
	_, err := s.ConcentrationSeries(ctx, "c1", 0, ^uint64(0))
	require.Error(t, err)
	require.Contains(t, err.Error(), "high bit")
	_, err = s.ConcentrationSeries(ctx, "c1", 0, uint64(1<<63-1))
	require.NoError(t, err)
}

func TestEpochsWithEntitiesUnavailable(t *testing.T) {
	ctx := context.Background()
	s := openTemp(t)
	for _, ep := range []uint64{3, 4, 5, 6, 7} {
		r := readRow(ep) // height = 100·epoch
		r.EntitiesUnavailable = sp("no_entity_layer")
		if ep == 6 {
			r.EntitiesUnavailable = sp("pruned")
		}
		require.NoError(t, s.WriteConcentration(ctx, "c1", r, nil, nil))
	}
	other := readRow(9)
	other.EntitiesUnavailable = sp("no_entity_layer")
	require.NoError(t, s.WriteConcentration(ctx, "c2", other, nil, nil))

	got, err := s.EpochsWithEntitiesUnavailable(ctx, "c1", "no_entity_layer", 400, 0, 10)
	require.NoError(t, err)
	require.Equal(t, []uint64{4, 5, 7}, got, "height >= 400, this code only, oldest first, this chain only")

	got, err = s.EpochsWithEntitiesUnavailable(ctx, "c1", "no_entity_layer", 400, 0, 2)
	require.NoError(t, err)
	require.Equal(t, []uint64{4, 5}, got)

	got, err = s.EpochsWithEntitiesUnavailable(ctx, "c1", "no_entity_layer", 400, 4, 10)
	require.NoError(t, err)
	require.Equal(t, []uint64{5, 7}, got, "only epochs after the cursor")

	got, err = s.EpochsWithEntitiesUnavailable(ctx, "c1", "no_entity_layer", 800, 0, 10)
	require.NoError(t, err)
	require.Empty(t, got)
}

func TestMarkEntitiesUnavailableTouchesOnlyThatColumn(t *testing.T) {
	ctx := context.Background()
	s := openTemp(t)
	r := readRow(4)
	r.EntitiesUnavailable = sp("no_entity_layer")
	require.NoError(t, s.WriteConcentration(ctx, "c1", r, threePowers(), nil))

	changed, err := s.MarkEntitiesUnavailable(ctx, "c1", 4, "no_entity_layer", "pruned")
	require.NoError(t, err)
	require.True(t, changed)
	row, powers, err := s.Concentration(ctx, "c1", ptr(uint64(4)))
	require.NoError(t, err)
	require.Equal(t, "pruned", str(row.EntitiesUnavailable))
	require.Equal(t, int64(600), *row.TotalPower)
	require.Equal(t, "HASH4", str(row.BlockHash))
	require.Len(t, powers, 3)

	// Only a row that still carries the expected code is changed.
	changed, err = s.MarkEntitiesUnavailable(ctx, "c1", 4, "no_entity_layer", "pruned")
	require.NoError(t, err)
	require.False(t, changed)
}

// No row with non-integer tokens can be written, so the entity view never
// fails on what the service itself stored.
func TestWriteRefusesNonIntegerTokens(t *testing.T) {
	ctx := context.Background()
	s := openTemp(t)
	require.NoError(t, s.WriteConcentration(ctx, "c1", readRow(1), nil,
		[]store.EntityRow{{EntityID: "a", DisplayName: "A", Status: "S", Tokens: sp("5")}}))
	for _, bad := range []string{"", "12x", "1.5", "+-1"} {
		err := s.WriteConcentration(ctx, "c1", readRow(2), nil,
			[]store.EntityRow{{EntityID: "a", DisplayName: "A", Status: "S", Tokens: sp(bad)}})
		require.Error(t, err, "%q", bad)
	}
	require.Equal(t, 1, count(t, s, "concentration_epochs"), "the refused epoch left nothing")
	row, out, err := s.Entities(ctx, "c1", nil)
	require.NoError(t, err)
	require.Equal(t, uint64(1), row.Epoch)
	require.Len(t, out, 1)
}
