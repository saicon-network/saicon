package keeper

import (
	"context"

	sdk "github.com/cosmos/cosmos-sdk/types"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/saicon-network/saicon/chain/x/validatorregistry/types"
)

// Validator returns metadata for a single validator by operator address.
func (q queryServer) Validator(ctx context.Context, req *types.QueryValidatorRequest) (*types.QueryValidatorResponse, error) {
	if req == nil {
		return nil, status.Error(codes.InvalidArgument, "invalid request")
	}

	valAddr, err := parseValAddr(req.ValidatorAddress)
	if err != nil {
		return nil, status.Errorf(codes.InvalidArgument, "invalid validator address: %v", err)
	}

	md, found, err := q.k.GetValidatorMetadata(ctx, valAddr)
	if err != nil {
		return nil, status.Error(codes.Internal, "internal error")
	}
	if !found {
		return nil, status.Errorf(codes.NotFound, "validator %s not found", req.ValidatorAddress)
	}

	return &types.QueryValidatorResponse{Validator: md}, nil
}

// Validators lists validator metadata, optionally filtered by status string.
// Note: filtering is applied in-memory after iteration; pagination over the
// filtered set is not yet wired (v0.1 — registry is small). status_filter
// empty returns all.
func (q queryServer) Validators(ctx context.Context, req *types.QueryValidatorsRequest) (*types.QueryValidatorsResponse, error) {
	if req == nil {
		return nil, status.Error(codes.InvalidArgument, "invalid request")
	}

	var filter types.Status
	hasFilter := req.StatusFilter != ""
	if hasFilter {
		s, err := types.StatusFromString(req.StatusFilter)
		if err != nil {
			return nil, status.Errorf(codes.InvalidArgument, "invalid status_filter: %v", err)
		}
		filter = s
	}

	var out []types.ValidatorMetadata
	err := q.k.IterateValidatorMetadata(ctx, func(_ sdk.ValAddress, md types.ValidatorMetadata) bool {
		if !hasFilter || md.Status == filter {
			out = append(out, md)
		}
		return false
	})
	if err != nil {
		return nil, status.Error(codes.Internal, "internal error")
	}

	return &types.QueryValidatorsResponse{Validators: out}, nil
}

// parseValAddr accepts a bech32 string and returns the operator address bytes.
// Accepts both valoper- and account-prefixed forms (v0.1 leniency; the module
// keys on the raw 20 bytes regardless of prefix).
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
