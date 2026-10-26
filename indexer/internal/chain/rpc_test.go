package chain_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/saicon-network/saicon/indexer/internal/chain"
)

// Status.EarliestHeight is what a fresh database seeds its cursor from
// against a pruned node (main.go: fresh && status.EarliestHeight > 1). Get
// the field name or the parse wrong and every fresh index silently starts at
// height 1 against a node that no longer has it, instead of logging what
// pruning costs and starting where the node actually can serve from.
func TestRPCStatusParsesEarliestHeight(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{
			"result": {
				"node_info": {"network": "saicon-localnet-1"},
				"sync_info": {
					"latest_block_height": "1105",
					"earliest_block_height": "5000",
					"earliest_block_time": "2026-09-22T13:15:10.227896341Z"
				}
			}
		}`))
	}))
	defer srv.Close()

	rpc := chain.NewRPC(srv.URL)
	status, err := rpc.Status(context.Background())
	require.NoError(t, err)
	require.Equal(t, "saicon-localnet-1", status.ChainID)
	require.Equal(t, int64(1105), status.LatestHeight)
	require.Equal(t, int64(5000), status.EarliestHeight)
}

// A node that has never pruned answers "0" for earliest_block_height in some
// CometBFT versions and "1" in others; either way it must parse rather than
// error, since parseInt64 treats "" as 0 but a literal "0" or "1" must decode
// normally.
func TestRPCStatusParsesEarliestHeightUnpruned(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{
			"result": {
				"node_info": {"network": "saicon-devnet-1"},
				"sync_info": {
					"latest_block_height": "42",
					"earliest_block_height": "1",
					"earliest_block_time": "2026-01-01T00:00:00Z"
				}
			}
		}`))
	}))
	defer srv.Close()

	rpc := chain.NewRPC(srv.URL)
	status, err := rpc.Status(context.Background())
	require.NoError(t, err)
	require.Equal(t, int64(1), status.EarliestHeight)
}
