package follow_test

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/saicon-network/saicon/indexer/internal/follow"
	"github.com/saicon-network/saicon/indexer/internal/store"
)

// fakeRPC answers from a fixed slice, so the test owns the chain.
type fakeRPC struct {
	tip       int64
	proposers map[int64]string
	calls     int
}

func (f *fakeRPC) Tip(context.Context) (int64, error) { return f.tip, nil }
func (f *fakeRPC) ProposerAt(_ context.Context, h int64) (string, error) {
	f.calls++
	return f.proposers[h], nil
}

func newStore(t *testing.T) *store.Store {
	t.Helper()
	s, err := store.Open(filepath.Join(t.TempDir(), "f.db"))
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, s.Close()) })
	require.NoError(t, s.SetChain(context.Background(), "c1", time.Now()))
	return s
}

func TestFollowerCountsEachBlockOnce(t *testing.T) {
	ctx := context.Background()
	s := newStore(t)
	rpc := &fakeRPC{tip: 5, proposers: map[int64]string{1: "A", 2: "A", 3: "B", 4: "A", 5: "B"}}

	f := follow.New(s, rpc, 100)
	n, err := f.Once(ctx, "c1")
	require.NoError(t, err)
	require.Equal(t, int64(5), n)

	var a, b int64
	require.NoError(t, s.DB().QueryRowContext(ctx,
		`SELECT count FROM proposals_raw WHERE cons_addr='A'`).Scan(&a))
	require.NoError(t, s.DB().QueryRowContext(ctx,
		`SELECT count FROM proposals_raw WHERE cons_addr='B'`).Scan(&b))
	require.Equal(t, int64(3), a)
	require.Equal(t, int64(2), b)
}

// The load-bearing test. A crash mid-pass must not inflate the only number
// this service originates.
func TestFollowerIsIdempotentAcrossRestart(t *testing.T) {
	ctx := context.Background()
	s := newStore(t)
	rpc := &fakeRPC{tip: 5, proposers: map[int64]string{1: "A", 2: "A", 3: "A", 4: "A", 5: "A"}}

	// First pass covers everything.
	f1 := follow.New(s, rpc, 100)
	_, err := f1.Once(ctx, "c1")
	require.NoError(t, err)

	// A brand-new follower over the same store — what a restart is.
	f2 := follow.New(s, rpc, 100)
	n, err := f2.Once(ctx, "c1")
	require.NoError(t, err)
	require.Equal(t, int64(0), n, "a restart at the tip counts nothing")

	var count int64
	require.NoError(t, s.DB().QueryRowContext(ctx,
		`SELECT count FROM proposals_raw WHERE cons_addr='A'`).Scan(&count))
	require.Equal(t, int64(5), count, "five blocks, counted five times in total")
}

// A devnet reset keeps the chain id, so the id check at startup will not catch it.
// What catches it is the chain being shorter than the index — and it must be loud,
// because the alternative is a loop that does nothing successfully for ever.
func TestFollowerRefusesAChainShorterThanTheIndex(t *testing.T) {
	ctx := context.Background()
	s := newStore(t)
	rpc := &fakeRPC{tip: 10, proposers: map[int64]string{}}
	for h := int64(1); h <= 10; h++ {
		rpc.proposers[h] = "A"
	}
	f := follow.New(s, rpc, 100)
	_, err := f.Once(ctx, "c1")
	require.NoError(t, err)

	// The chain comes back from genesis under the same id.
	rpc.tip = 3

	n, err := f.Once(ctx, "c1")
	require.Error(t, err, "a chain shorter than the index must not look like success")
	require.Equal(t, int64(0), n)

	var re *follow.ResetError
	require.ErrorAs(t, err, &re)
	require.Equal(t, int64(10), re.Cursor)
	require.Equal(t, int64(3), re.Tip)
}

func TestFollowerAssignsBlocksToTheRightEpoch(t *testing.T) {
	ctx := context.Background()
	s := newStore(t)
	rpc := &fakeRPC{tip: 12, proposers: map[int64]string{}}
	for h := int64(1); h <= 12; h++ {
		rpc.proposers[h] = "A"
	}

	f := follow.New(s, rpc, 5) // the chain's numbering: 1-5 -> 1, 6-10 -> 2, 11-12 -> 3
	_, err := f.Once(ctx, "c1")
	require.NoError(t, err)

	rows := map[uint64]int64{}
	r, err := s.DB().QueryContext(ctx, `SELECT epoch, count FROM proposals_raw ORDER BY epoch`)
	require.NoError(t, err)
	defer func() { _ = r.Close() }()
	for r.Next() {
		var e uint64
		var c int64
		require.NoError(t, r.Scan(&e, &c))
		rows[e] = c
	}
	require.Equal(t, map[uint64]int64{1: 5, 2: 5, 3: 2}, rows,
		"there is no epoch 0 on this chain; x/participation closes epoch h/epochLen at h%epochLen==0")
}

// The two functions answer different questions and the difference is a whole
// epoch. Pinned here because getting it wrong pairs a score from one window with
// a proposal count from the next.
func TestEpochOfAndLastClosedEpochDisagreeByOneOnPurpose(t *testing.T) {
	const l = 100
	require.Equal(t, uint64(1), follow.EpochOf(1, l))
	require.Equal(t, uint64(1), follow.EpochOf(100, l))
	require.Equal(t, uint64(2), follow.EpochOf(101, l))
	require.Equal(t, uint64(5), follow.EpochOf(471, l))

	require.Equal(t, uint64(0), follow.LastClosedEpoch(99, l), "nothing has closed yet")
	require.Equal(t, uint64(1), follow.LastClosedEpoch(100, l))
	require.Equal(t, uint64(1), follow.LastClosedEpoch(199, l))
	require.Equal(t, uint64(4), follow.LastClosedEpoch(471, l),
		"measured on the localnet: epochs 1-4 answer 200, epoch 5 answers 404")

	// epochLen 1: the chain skips the close at height 1, so epoch 1 never closes.
	require.Equal(t, uint64(0), follow.LastClosedEpoch(1, 1))
	require.Equal(t, uint64(2), follow.LastClosedEpoch(2, 1))
	require.Equal(t, uint64(1), follow.EpochOf(1, 1))
	require.Equal(t, uint64(2), follow.EpochOf(2, 1))
}
