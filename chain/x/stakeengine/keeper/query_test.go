package keeper_test

import (
	"testing"

	"cosmossdk.io/math"
	"github.com/cosmos/cosmos-sdk/types/query"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/saicon-network/saicon/chain/x/stakeengine/keeper"
	"github.com/saicon-network/saicon/chain/x/stakeengine/types"
)

func TestQueryParams_ReportsTheEntryFloor(t *testing.T) {
	f := newFixture(t)
	res, err := keeper.NewQueryServerImpl(f.k).Params(f.ctx, &types.QueryParamsRequest{})
	require.NoError(t, err)
	want := types.DefaultParams()
	want.MinQueueEntry = math.NewInt(1e8) // the fixture's lowered queue minimum, see newFixture
	want.ExcludedAddresses = nil          // the store round-trip turns an empty list into nil; on the wire they are the same
	require.Equal(t, want, res.Params)
	require.Equal(t, nscn(2_000), res.EntryFloor)
}

func TestQueryCaps(t *testing.T) {
	f := newFixture(t)
	qs := keeper.NewQueryServerImpl(f.k)

	_, err := qs.Caps(f.ctx, &types.QueryCapsRequest{})
	require.Equal(t, codes.NotFound, status.Code(err), "before the first snapshot")

	f.caps(t)
	res, err := qs.Caps(f.ctx, &types.QueryCapsRequest{})
	require.NoError(t, err)
	require.Equal(t, nscn(1_680_000), res.CapState.GlobalCap)
	require.Equal(t, nscn(1_000_000), res.TotalBonded)
	require.Equal(t, nscn(680_000), res.Headroom)
}

func TestQueryValidatorCap(t *testing.T) {
	f := newFixture(t) // floor 0
	f.caps(t)
	qs := keeper.NewQueryServerImpl(f.k)

	res, err := qs.ValidatorCap(f.ctx, &types.QueryValidatorCapRequest{ValidatorAddress: val(1).String()})
	require.NoError(t, err)
	require.Equal(t, nscn(1_000_000), res.Tokens)
	require.Equal(t, nscn(20_000), res.ValidatorCap)
	require.Equal(t, nscn(-980_000), res.Room, "a validator over its cap has negative room")

	_, err = qs.ValidatorCap(f.ctx, &types.QueryValidatorCapRequest{ValidatorAddress: "nonsense"})
	require.Equal(t, codes.InvalidArgument, status.Code(err))
}

func TestQueryQueue_InFillOrderWithOffsetPagination(t *testing.T) {
	f := newFixture(t)
	f.staking.addValidator(val(2), nscn(10_000))
	f.staking.addValidator(val(3), nscn(10_000))
	f.caps(t)
	f.part.scores[string(val(1))] = math.LegacyNewDecWithPrec(1, 1)
	f.part.scores[string(val(2))] = math.LegacyNewDecWithPrec(9, 1)
	f.part.scores[string(val(3))] = math.LegacyNewDecWithPrec(5, 1)
	for _, b := range []byte{1, 2, 3} { // ids 0, 1, 2
		_, err := f.k.Enqueue(f.ctx, acc(1), val(b), nscn(10))
		require.NoError(t, err)
	}
	qs := keeper.NewQueryServerImpl(f.k)

	res, err := qs.Queue(f.ctx, &types.QueryQueueRequest{})
	require.NoError(t, err)
	require.Len(t, res.Entries, 3)
	require.Equal(t, []uint64{1, 2, 0}, []uint64{res.Entries[0].Id, res.Entries[1].Id, res.Entries[2].Id})
	require.Equal(t, uint64(3), res.Pagination.Total)

	res, err = qs.Queue(f.ctx, &types.QueryQueueRequest{Pagination: &query.PageRequest{Offset: 1, Limit: 1}})
	require.NoError(t, err)
	require.Len(t, res.Entries, 1)
	require.Equal(t, uint64(2), res.Entries[0].Id)

	res, err = qs.Queue(f.ctx, &types.QueryQueueRequest{Pagination: &query.PageRequest{Offset: 9}})
	require.NoError(t, err)
	require.Empty(t, res.Entries)

	_, err = qs.Queue(f.ctx, &types.QueryQueueRequest{Pagination: &query.PageRequest{Key: []byte{1}}})
	require.Equal(t, codes.InvalidArgument, status.Code(err))
}

func TestQueryQueueEntryAndDelegatorQueue(t *testing.T) {
	f := newFixture(t)
	f.caps(t)
	require.NoError(t, f.bank.move(acc(1), acc(3), nscn(100)))
	mine, err := f.k.Enqueue(f.ctx, acc(3), val(1), nscn(10))
	require.NoError(t, err)
	_, err = f.k.Enqueue(f.ctx, acc(1), val(1), nscn(20))
	require.NoError(t, err)
	qs := keeper.NewQueryServerImpl(f.k)

	one, err := qs.QueueEntry(f.ctx, &types.QueryQueueEntryRequest{Id: mine})
	require.NoError(t, err)
	require.Equal(t, nscn(10), one.Entry.Amount)
	_, err = qs.QueueEntry(f.ctx, &types.QueryQueueEntryRequest{Id: 99})
	require.Equal(t, codes.NotFound, status.Code(err))

	list, err := qs.DelegatorQueue(f.ctx, &types.QueryDelegatorQueueRequest{DelegatorAddress: acc(3).String()})
	require.NoError(t, err)
	require.Len(t, list.Entries, 1)
	require.Equal(t, mine, list.Entries[0].Id)

	_, err = qs.DelegatorQueue(f.ctx, &types.QueryDelegatorQueueRequest{DelegatorAddress: "nonsense"})
	require.Equal(t, codes.InvalidArgument, status.Code(err))
}
