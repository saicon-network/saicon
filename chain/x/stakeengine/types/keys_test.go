package types_test

import (
	"testing"

	"cosmossdk.io/collections"
	storetypes "cosmossdk.io/store/types"
	"github.com/cosmos/cosmos-sdk/runtime"
	"github.com/stretchr/testify/require"

	"github.com/saicon-network/saicon/chain/x/stakeengine/types"
)

// collections rejects a schema in which one prefix is a byte-prefix of another,
// so a key added later that overlaps an existing one fails here, not at node start.
// "queue_len" is the length counter the keeper adds in a later task.
func TestStorePrefixesDoNotOverlap(t *testing.T) {
	sb := collections.NewSchemaBuilder(runtime.NewKVStoreService(storetypes.NewKVStoreKey(types.StoreKey)))
	for i, p := range []collections.Prefix{
		types.ParamsKey, types.CapStateKey, types.QueueKey, types.QueueSeqKey, collections.NewPrefix("queue_len"),
	} {
		collections.NewItem(sb, p, string(rune('a'+i)), collections.Uint64Value)
	}
	_, err := sb.Build()
	require.NoError(t, err)
}
