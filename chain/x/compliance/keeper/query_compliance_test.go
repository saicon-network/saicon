package keeper_test

import (
	"testing"

	sdk "github.com/cosmos/cosmos-sdk/types"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/saicon-network/saicon/chain/x/compliance/keeper"
	"github.com/saicon-network/saicon/chain/x/compliance/types"
)

func TestComplianceQuery_HappyPath(t *testing.T) {
	f := initFixture(t)
	addr := sdk.ValAddress([]byte("compliance-query-20b"))[:20]

	require.NoError(t, f.keeper.SetCompliance(
		f.ctx, addr, false,
		types.ComplianceEventType_COMPLIANCE_EVENT_TYPE_EXTERNAL_FLAG,
		"flagged for test",
	))

	q := keeper.NewQueryServerImpl(f.keeper)
	resp, err := q.Compliance(f.ctx, &types.QueryComplianceRequest{ValidatorAddress: addr.String()})
	require.NoError(t, err)
	require.NotNil(t, resp)
	require.False(t, resp.State.Compliant)
	require.Equal(t, types.ComplianceEventType_COMPLIANCE_EVENT_TYPE_EXTERNAL_FLAG, resp.State.LastEvent)
	require.Equal(t, "flagged for test", resp.State.Reason)
}

func TestComplianceQuery_NotFound(t *testing.T) {
	f := initFixture(t)
	addr := sdk.ValAddress([]byte("compliance-unknown20"))[:20]

	q := keeper.NewQueryServerImpl(f.keeper)
	resp, err := q.Compliance(f.ctx, &types.QueryComplianceRequest{ValidatorAddress: addr.String()})
	require.Error(t, err)
	require.Nil(t, resp)
	require.Equal(t, codes.NotFound, status.Code(err))
}

func TestComplianceQuery_InvalidAddress(t *testing.T) {
	f := initFixture(t)

	q := keeper.NewQueryServerImpl(f.keeper)
	resp, err := q.Compliance(f.ctx, &types.QueryComplianceRequest{ValidatorAddress: "not-a-valid-bech32-address"})
	require.Error(t, err)
	require.Nil(t, resp)
	require.Equal(t, codes.InvalidArgument, status.Code(err))
}

func TestComplianceQuery_NilRequest(t *testing.T) {
	f := initFixture(t)

	q := keeper.NewQueryServerImpl(f.keeper)
	resp, err := q.Compliance(f.ctx, nil)
	require.Error(t, err)
	require.Nil(t, resp)
	require.Equal(t, codes.InvalidArgument, status.Code(err))
}
