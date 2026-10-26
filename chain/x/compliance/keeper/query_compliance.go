package keeper

import (
	"context"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/saicon-network/saicon/chain/x/compliance/types"
)

// Compliance returns the stored compliance state for a single validator.
func (q queryServer) Compliance(ctx context.Context, req *types.QueryComplianceRequest) (*types.QueryComplianceResponse, error) {
	if req == nil {
		return nil, status.Error(codes.InvalidArgument, "invalid request")
	}
	valAddr, err := parseValAddr(req.ValidatorAddress)
	if err != nil {
		return nil, status.Errorf(codes.InvalidArgument, "invalid validator address: %v", err)
	}
	state, found, err := q.k.GetCompliance(ctx, valAddr)
	if err != nil {
		return nil, status.Error(codes.Internal, "internal error")
	}
	if !found {
		return nil, status.Errorf(codes.NotFound, "no compliance record for %s", req.ValidatorAddress)
	}
	return &types.QueryComplianceResponse{State: state}, nil
}
