package chain_test

import (
	"context"
	"os"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/saicon-network/saicon/indexer/internal/chain"
)

// The REST shapes this service depends on, asserted against a running chain.
// Go types catch a renamed proto field at build time; they do not catch the
// JSON boundary, which is what this covers.
//
// Set INDEXER_TEST_REST to run it:
//
//	INDEXER_TEST_REST=http://localhost:1317 go test ./internal/chain/
func TestRESTContract(t *testing.T) {
	base := os.Getenv("INDEXER_TEST_REST")
	if base == "" {
		t.Skip("set INDEXER_TEST_REST to a running chain's REST endpoint")
	}
	ctx := context.Background()
	r := chain.NewREST(base)

	epochLen, err := r.EpochLength(ctx)
	require.NoError(t, err)
	require.Greater(t, epochLen, uint64(0), "epoch_length_blocks must be positive")

	vals, err := r.Validators(ctx)
	require.NoError(t, err)
	require.NotEmpty(t, vals, "a running chain has at least one validator")
	require.NotEmpty(t, vals[0].Operator)
	require.NotEmpty(t, vals[0].ConsensusPubKey)

	reg, err := r.RegistryValidators(ctx)
	require.NoError(t, err)
	require.NotEmpty(t, reg, "the registry mirrors the staking set")
	require.NotEmpty(t, reg[0].Status)

	ep, err := r.EP(ctx, vals[0].Operator)
	require.NoError(t, err)
	if ep != nil {
		require.NotEmpty(t, ep.AccumulatedEP, "an EP record carries a value")
	}
}
