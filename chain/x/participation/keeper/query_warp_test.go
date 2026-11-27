package keeper_test

import (
	"testing"

	"cosmossdk.io/math"
	"github.com/cosmos/cosmos-sdk/codec"
	sdk "github.com/cosmos/cosmos-sdk/types"
	"github.com/cosmos/cosmos-sdk/types/query"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/saicon-network/saicon/chain/x/participation/keeper"
	"github.com/saicon-network/saicon/chain/x/participation/types"
)

func requireCode(t *testing.T, err error, code codes.Code) {
	t.Helper()
	require.Error(t, err)
	st, ok := status.FromError(err)
	require.True(t, ok, "not a gRPC status: %v", err)
	require.Equal(t, code, st.Code(), st.Message())
}

func TestWarpQuery(t *testing.T) {
	f := initFixture(t)
	q := keeper.NewQueryServerImpl(f.keeper)
	hi := sdk.ValAddress([]byte("warp-query-high-addr"))[:20]
	lo := sdk.ValAddress([]byte("warp-query-low-addr0"))[:20]
	require.NoError(t, f.keeper.SetWarp(f.ctx, hi, types.WarpState{ValidatorAddress: hi.String(), Value: math.LegacyNewDec(65), LastEpoch: 4}))
	require.NoError(t, f.keeper.SetWarp(f.ctx, lo, types.WarpState{ValidatorAddress: lo.String(), Value: math.LegacyMustNewDecFromStr("64.9"), LastEpoch: 4}))

	wantParams := types.WarpParams{
		Fall: types.DefaultWarpFall(), Rise: types.DefaultWarpRise(),
		Initial: types.DefaultWarpInitial(), Threshold: types.DefaultWarpThreshold(),
	}

	res, err := q.Warp(f.ctx, &types.QueryWarpRequest{ValidatorAddress: hi.String()})
	require.NoError(t, err)
	require.Equal(t, types.WarpRecord{ValidatorAddress: hi.String(), Value: math.LegacyNewDec(65), LastEpoch: 4, Eligible: true}, res.Warp)
	require.Equal(t, wantParams, res.Params)

	// The account-prefixed form resolves to the same row, as for EP.
	res, err = q.Warp(f.ctx, &types.QueryWarpRequest{ValidatorAddress: sdk.AccAddress(lo).String()})
	require.NoError(t, err)
	require.False(t, res.Warp.Eligible, "64.9 is below the threshold of 65")

	_, err = q.Warp(f.ctx, &types.QueryWarpRequest{ValidatorAddress: sdk.ValAddress([]byte("warp-query-none-addr"))[:20].String()})
	requireCode(t, err, codes.NotFound)
	_, err = q.Warp(f.ctx, &types.QueryWarpRequest{ValidatorAddress: "not-an-address"})
	requireCode(t, err, codes.InvalidArgument)
	_, err = q.Warp(f.ctx, nil)
	requireCode(t, err, codes.InvalidArgument)
}

func TestWarpsQuery_Paginates(t *testing.T) {
	f := initFixture(t)
	q := keeper.NewQueryServerImpl(f.keeper)

	empty, err := q.Warps(f.ctx, &types.QueryWarpsRequest{})
	require.NoError(t, err)
	require.Empty(t, empty.Warps)
	require.Equal(t, types.DefaultWarpThreshold(), empty.Params.Threshold)

	addrs := []sdk.ValAddress{
		sdk.ValAddress([]byte("warp-page-addr-cc-20"))[:20],
		sdk.ValAddress([]byte("warp-page-addr-aa-20"))[:20],
		sdk.ValAddress([]byte("warp-page-addr-bb-20"))[:20],
	}
	values := []int64{70, 60, 65}
	for i, a := range addrs {
		require.NoError(t, f.keeper.SetWarp(f.ctx, a, types.WarpState{ValidatorAddress: a.String(), Value: math.LegacyNewDec(values[i]), LastEpoch: 2}))
	}

	page1, err := q.Warps(f.ctx, &types.QueryWarpsRequest{Pagination: &query.PageRequest{Limit: 2}})
	require.NoError(t, err)
	require.Len(t, page1.Warps, 2)
	require.NotEmpty(t, page1.Pagination.NextKey)
	page2, err := q.Warps(f.ctx, &types.QueryWarpsRequest{Pagination: &query.PageRequest{Key: page1.Pagination.NextKey}})
	require.NoError(t, err)
	require.Len(t, page2.Warps, 1)

	// Operator address byte order: aa, bb, cc.
	got := append(page1.Warps, page2.Warps...)
	require.Equal(t, []string{addrs[1].String(), addrs[2].String(), addrs[0].String()},
		[]string{got[0].ValidatorAddress, got[1].ValidatorAddress, got[2].ValidatorAddress})
	require.Equal(t, []bool{false, true, true}, []bool{got[0].Eligible, got[1].Eligible, got[2].Eligible},
		"60 is below the threshold of 65; 65 and 70 are not")

	_, err = q.Warps(f.ctx, nil)
	requireCode(t, err, codes.InvalidArgument)
}

// A snapshot written before the engine renders its warp as null, never as 0:
// absent means unknown, and 0 is what a punished validator shows.
func TestSnapshotQuery_RendersAnAbsentWarpAsNull(t *testing.T) {
	f := initFixture(t)
	q := keeper.NewQueryServerImpl(f.keeper)
	addr := sdk.ValAddress([]byte("warp-null-render-a20"))[:20]
	require.NoError(t, f.keeper.SetSnapshot(f.ctx, 3, addr, snapshotAt(addr, 3, 30)))
	w := math.LegacyNewDec(82)
	withWarp := snapshotAt(addr, 4, 40)
	withWarp.Warp = &w
	require.NoError(t, f.keeper.SetSnapshot(f.ctx, 4, addr, withWarp))

	res, err := q.Snapshot(f.ctx, &types.QuerySnapshotRequest{ValidatorAddress: addr.String(), EpochId: 3})
	require.NoError(t, err)
	bz, err := codec.ProtoMarshalJSON(res, nil)
	require.NoError(t, err)
	require.Contains(t, string(bz), `"warp":null`)

	res, err = q.Snapshot(f.ctx, &types.QuerySnapshotRequest{ValidatorAddress: addr.String(), EpochId: 4})
	require.NoError(t, err)
	bz, err = codec.ProtoMarshalJSON(res, nil)
	require.NoError(t, err)
	require.Contains(t, string(bz), `"warp":"82.000000000000000000"`)
}
