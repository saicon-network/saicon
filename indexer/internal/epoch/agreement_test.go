package epoch_test

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/saicon-network/saicon/indexer/internal/chain"
	"github.com/saicon-network/saicon/indexer/internal/epoch"
	"github.com/saicon-network/saicon/indexer/internal/store"
)

// The point of the whole service: what it serves must equal what the chain
// says, validator by validator. Running, but disagreeing, is the failure this
// catches and no unit test can.
//
//	INDEXER_TEST_REST=http://localhost:1317 go test ./internal/epoch/ -run TestAgreesWithChain
func TestAgreesWithChain(t *testing.T) {
	base := os.Getenv("INDEXER_TEST_REST")
	if base == "" {
		t.Skip("set INDEXER_TEST_REST to a running chain's REST endpoint")
	}
	ctx := context.Background()
	rest := chain.NewREST(base)

	s, err := store.Open(filepath.Join(t.TempDir(), "agree.db"))
	require.NoError(t, err)
	defer func() { _ = s.Close() }()
	require.NoError(t, s.SetChain(ctx, "live", time.Now()))

	require.NoError(t, epoch.New(s, rest, 100).ReadEpoch(ctx, "live", 1))

	vals, err := rest.Validators(ctx)
	require.NoError(t, err)
	require.NotEmpty(t, vals)

	board, err := s.Scoreboard(ctx, "live")
	require.NoError(t, err)
	require.Len(t, board, len(vals), "the scoreboard holds every validator the chain has")

	byOp := map[string]store.ScoreRow{}
	for _, r := range board {
		byOp[r.Operator] = r
	}
	for _, v := range vals {
		row, ok := byOp[v.Operator]
		require.True(t, ok, "validator %s missing from the scoreboard", v.Operator)
		require.Equal(t, v.Tokens, row.Tokens, "tokens must match the chain exactly")
		require.Equal(t, v.Status, row.StakingStatus)

		ep, err := rest.EP(ctx, v.Operator)
		require.NoError(t, err)
		if ep == nil {
			require.Nil(t, row.AccumulatedEP, "no EP record must stay NULL, not 0")
		} else {
			require.NotNil(t, row.AccumulatedEP)
			require.Equal(t, ep.AccumulatedEP, *row.AccumulatedEP)
		}
	}
}
