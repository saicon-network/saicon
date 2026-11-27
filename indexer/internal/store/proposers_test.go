package store_test

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/saicon-network/saicon/indexer/internal/store"
)

func TestProposerSeriesGathersStoredInputs(t *testing.T) {
	ctx := context.Background()
	s, err := store.Open(filepath.Join(t.TempDir(), "p.db"))
	require.NoError(t, err)
	t.Cleanup(func() { _ = s.Close() })
	require.NoError(t, s.SetChain(ctx, "c1", time.Now()))
	require.NoError(t, s.UpsertValidator(ctx, "c1", store.ValidatorRow{Operator: "op1", Moniker: "node1", Tokens: "1"}))
	for h := 1; h <= 8; h++ {
		require.NoError(t, s.RecordBlock(ctx, "c1", int64(h), uint64((h-1)/4+1), []string{"AA", "BB"}[h%2]))
	}
	for e := uint64(1); e <= 2; e++ {
		r := readRow(e)
		r.Height = int64(e) * 4
		require.NoError(t, s.WriteConcentration(ctx, "c1", r, threePowers(), nil))
	}

	in, err := s.ProposerSeries(ctx, "c1", 0, 100, 0)
	require.NoError(t, err)
	require.Len(t, in.Rows, 2)
	require.Equal(t, int64(1), in.FirstWalked)
	require.Equal(t, int64(8), in.Indexed)
	require.Equal(t, int64(2), in.Counts[1]["AA"])
	require.Equal(t, int64(2), in.Counts[1]["BB"])
	require.Len(t, in.Powers[2], 3)
	require.Equal(t, "node1", *in.Powers[1][2].Moniker) // power 100: op1 sorts last
	require.Nil(t, in.Powers[1][0].Moniker)

	newest, err := s.ProposerSeries(ctx, "c1", 0, 100, 1)
	require.NoError(t, err)
	require.Len(t, newest.Rows, 1)
	require.Equal(t, uint64(2), newest.Rows[0].Epoch)
	require.Empty(t, newest.Powers[1], "only the selected epochs' detail is read")

	empty, err := s.ProposerSeries(ctx, "c1", 50, 60, 0)
	require.NoError(t, err)
	require.Empty(t, empty.Rows)
}

func TestProposerSeriesIsCapped(t *testing.T) {
	ctx := context.Background()
	s, err := store.Open(filepath.Join(t.TempDir(), "p.db"))
	require.NoError(t, err)
	t.Cleanup(func() { _ = s.Close() })
	require.NoError(t, s.SetChain(ctx, "c1", time.Now()))
	n := store.ProposerMaxEpochs + 5
	for e := 1; e <= n; e++ {
		r := readRow(uint64(e))
		require.NoError(t, s.WriteConcentration(ctx, "c1", r, nil, nil))
	}
	oldest, err := s.ProposerSeries(ctx, "c1", 0, 1<<40, 0)
	require.NoError(t, err)
	require.Len(t, oldest.Rows, store.ProposerMaxEpochs)
	require.Equal(t, uint64(1), oldest.Rows[0].Epoch)
	newest, err := s.ProposerSeries(ctx, "c1", 0, 1<<40, 5000)
	require.NoError(t, err)
	require.Len(t, newest.Rows, store.ProposerMaxEpochs)
	require.Equal(t, uint64(n), newest.Rows[len(newest.Rows)-1].Epoch)
}
