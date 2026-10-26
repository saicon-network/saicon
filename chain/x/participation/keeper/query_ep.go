package keeper

import (
	"context"

	sdk "github.com/cosmos/cosmos-sdk/types"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/saicon-network/saicon/chain/x/participation/types"
)

// Ep returns the accumulated Empirical Points state for a single validator.
func (q queryServer) Ep(ctx context.Context, req *types.QueryEpRequest) (*types.QueryEpResponse, error) {
	if req == nil {
		return nil, status.Error(codes.InvalidArgument, "invalid request")
	}

	valAddr, err := parseValAddr(req.ValidatorAddress)
	if err != nil {
		return nil, status.Errorf(codes.InvalidArgument, "invalid validator address: %v", err)
	}

	ep, found, err := q.k.GetEP(ctx, valAddr)
	if err != nil {
		return nil, status.Error(codes.Internal, "internal error")
	}
	if !found {
		return nil, status.Errorf(codes.NotFound, "no empirical points for validator %s", req.ValidatorAddress)
	}

	return &types.QueryEpResponse{Ep: ep}, nil
}

// parseValAddr accepts a bech32 string and returns the operator address bytes.
// Accepts both valoper- and account-prefixed forms (v0.1 leniency; the module
// keys on the raw 20 bytes regardless of prefix). Mirrors the validatorregistry
// query helper.
func parseValAddr(s string) (sdk.ValAddress, error) {
	if va, err := sdk.ValAddressFromBech32(s); err == nil {
		return va, nil
	}
	acc, err := sdk.AccAddressFromBech32(s)
	if err != nil {
		return nil, err
	}
	return sdk.ValAddress(acc), nil
}
