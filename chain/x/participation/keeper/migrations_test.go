package keeper_test

import (
	"testing"

	"cosmossdk.io/math"
	sdk "github.com/cosmos/cosmos-sdk/types"
	"github.com/stretchr/testify/require"
	"google.golang.org/protobuf/encoding/protowire"

	"github.com/saicon-network/saicon/chain/x/participation/keeper"
	"github.com/saicon-network/saicon/chain/x/participation/types"
)

// v1ParamsBytes encodes Params the way a ConsensusVersion 1 store holds them:
// fields 1 and 2 only, because the WARP fields did not exist.
func v1ParamsBytes(t *testing.T, epochLength uint64, epDecay math.LegacyDec) []byte {
	t.Helper()
	dec, err := epDecay.Marshal()
	require.NoError(t, err)
	var b []byte
	b = protowire.AppendTag(b, 1, protowire.VarintType)
	b = protowire.AppendVarint(b, epochLength)
	b = protowire.AppendTag(b, 2, protowire.BytesType)
	b = protowire.AppendBytes(b, dec)
	return b
}

// R9: the 1 -> 2 migration gives the WARP parameters their defaults and keeps
// the parameters already stored. It writes no WARP rows.
func TestMigrate1to2_DefaultsWarpAndKeepsTheRest(t *testing.T) {
	f := initFixture(t)
	epDecay := math.LegacyMustNewDecFromStr("0.01")
	kv := f.storeService.OpenKVStore(f.ctx)
	require.NoError(t, kv.Set(types.ParamsKey, v1ParamsBytes(t, 10, epDecay)))

	before, err := f.keeper.Params.Get(f.ctx)
	require.NoError(t, err)
	require.Equal(t, uint64(10), before.EpochLengthBlocks)
	require.True(t, before.WarpFall.IsNil(), "a version 1 store has no WARP parameters")
	require.True(t, before.WarpThreshold.IsNil())
	require.ErrorContains(t, before.Validate(), "warp_fall must not be nil")

	require.NoError(t, keeper.NewMigrator(f.keeper).Migrate1to2(sdk.UnwrapSDKContext(f.ctx)))

	after, err := f.keeper.Params.Get(f.ctx)
	require.NoError(t, err)
	require.NoError(t, after.Validate())
	require.Equal(t, uint64(10), after.EpochLengthBlocks, "epoch_length_blocks kept")
	require.True(t, epDecay.Equal(after.EpDecay), "ep_decay kept, got %s", after.EpDecay)
	require.Equal(t, types.DefaultWarpFall(), after.WarpFall)
	require.Equal(t, types.DefaultWarpRise(), after.WarpRise)
	require.Equal(t, types.DefaultWarpInitial(), after.WarpInitial)
	require.Equal(t, types.DefaultWarpThreshold(), after.WarpThreshold)

	n := 0
	require.NoError(t, f.keeper.Warp.Walk(f.ctx, nil, func([]byte, types.WarpState) (bool, error) {
		n++
		return false, nil
	}))
	require.Zero(t, n, "no WARP rows are seeded")
}
