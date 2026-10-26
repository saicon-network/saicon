package keeper_test

import (
	"testing"

	sdk "github.com/cosmos/cosmos-sdk/types"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/saicon-network/saicon/chain/x/rewards/keeper"
	"github.com/saicon-network/saicon/chain/x/rewards/types"
)

func TestRewardSnapshotQuery_HappyPath(t *testing.T) {
	f := initFixture(t)
	addr := sdk.ValAddress([]byte("reward-snapshot-q20b"))[:20]

	snap := types.RewardSnapshot{
		ValidatorAddress: addr.String(),
		EpochId:          3,
		Amount:           "",
		Eligible:         false,
		Reason:           "EP=0",
	}
	require.NoError(t, f.keeper.SetRewardSnapshot(f.ctx, 3, addr, snap))

	q := keeper.NewQueryServerImpl(f.keeper)
	resp, err := q.RewardSnapshot(f.ctx, &types.QueryRewardSnapshotRequest{
		ValidatorAddress: addr.String(),
		EpochId:          3,
	})
	require.NoError(t, err)
	require.NotNil(t, resp)
	require.False(t, resp.Snapshot.Eligible)
	require.Equal(t, "EP=0", resp.Snapshot.Reason)
	require.Equal(t, uint64(3), resp.Snapshot.EpochId)
	require.Equal(t, addr.String(), resp.Snapshot.ValidatorAddress)
}

func TestRewardSnapshotQuery_NotFound_DifferentEpoch(t *testing.T) {
	f := initFixture(t)
	addr := sdk.ValAddress([]byte("reward-snapshot-q20b"))[:20]

	snap := types.RewardSnapshot{
		ValidatorAddress: addr.String(),
		EpochId:          3,
		Eligible:         false,
		Reason:           "EP=0",
	}
	require.NoError(t, f.keeper.SetRewardSnapshot(f.ctx, 3, addr, snap))

	q := keeper.NewQueryServerImpl(f.keeper)
	resp, err := q.RewardSnapshot(f.ctx, &types.QueryRewardSnapshotRequest{
		ValidatorAddress: addr.String(),
		EpochId:          4,
	})
	require.Error(t, err)
	require.Nil(t, resp)
	require.Equal(t, codes.NotFound, status.Code(err))
}

func TestRewardSnapshotQuery_NotFound_DifferentValidator(t *testing.T) {
	f := initFixture(t)
	addr := sdk.ValAddress([]byte("reward-snapshot-q20b"))[:20]
	other := sdk.ValAddress([]byte("reward-snapshot-oth1"))[:20]

	snap := types.RewardSnapshot{
		ValidatorAddress: addr.String(),
		EpochId:          3,
		Eligible:         false,
		Reason:           "EP=0",
	}
	require.NoError(t, f.keeper.SetRewardSnapshot(f.ctx, 3, addr, snap))

	q := keeper.NewQueryServerImpl(f.keeper)
	resp, err := q.RewardSnapshot(f.ctx, &types.QueryRewardSnapshotRequest{
		ValidatorAddress: other.String(),
		EpochId:          3,
	})
	require.Error(t, err)
	require.Nil(t, resp)
	require.Equal(t, codes.NotFound, status.Code(err))
}

func TestRewardSnapshotQuery_InvalidAddress(t *testing.T) {
	f := initFixture(t)

	q := keeper.NewQueryServerImpl(f.keeper)
	resp, err := q.RewardSnapshot(f.ctx, &types.QueryRewardSnapshotRequest{
		ValidatorAddress: "not-a-valid-bech32-address",
		EpochId:          3,
	})
	require.Error(t, err)
	require.Nil(t, resp)
	require.Equal(t, codes.InvalidArgument, status.Code(err))
}

func TestRewardSnapshotQuery_NilRequest(t *testing.T) {
	f := initFixture(t)

	q := keeper.NewQueryServerImpl(f.keeper)
	resp, err := q.RewardSnapshot(f.ctx, nil)
	require.Error(t, err)
	require.Nil(t, resp)
	require.Equal(t, codes.InvalidArgument, status.Code(err))
}
