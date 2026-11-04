package keeper

import (
	"context"
	"errors"

	"cosmossdk.io/collections"
	sdk "github.com/cosmos/cosmos-sdk/types"
	"github.com/cosmos/cosmos-sdk/types/query"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/saicon-network/saicon/chain/x/entityregistry/types"
)

var _ types.QueryServer = queryServer{}

// NewQueryServerImpl returns an implementation of the QueryServer interface
// for the provided Keeper.
func NewQueryServerImpl(k Keeper) types.QueryServer {
	return queryServer{k}
}

type queryServer struct {
	k Keeper
}

// Params returns the module parameters.
func (q queryServer) Params(ctx context.Context, req *types.QueryParamsRequest) (*types.QueryParamsResponse, error) {
	if req == nil {
		return nil, status.Error(codes.InvalidArgument, "invalid request")
	}
	params, err := q.k.Params.Get(ctx)
	if err != nil {
		return nil, status.Error(codes.Internal, err.Error())
	}
	return &types.QueryParamsResponse{Params: params}, nil
}

// Entities pages through every entity record, attested and revoked.
func (q queryServer) Entities(ctx context.Context, req *types.QueryEntitiesRequest) (*types.QueryEntitiesResponse, error) {
	if req == nil {
		return nil, status.Error(codes.InvalidArgument, "invalid request")
	}
	entities, pageRes, err := query.CollectionPaginate(ctx, q.k.Entities, req.Pagination,
		func(_ string, e types.Entity) (types.Entity, error) { return e, nil })
	if err != nil {
		return nil, status.Error(codes.Internal, err.Error())
	}
	return &types.QueryEntitiesResponse{Entities: entities, Pagination: pageRes}, nil
}

// Entity returns one entity with its member rows and its current active seats.
func (q queryServer) Entity(ctx context.Context, req *types.QueryEntityRequest) (*types.QueryEntityResponse, error) {
	if req == nil {
		return nil, status.Error(codes.InvalidArgument, "invalid request")
	}
	e, err := q.k.Entities.Get(ctx, req.EntityId)
	if err != nil {
		if errors.Is(err, collections.ErrNotFound) {
			return nil, status.Errorf(codes.NotFound, "entity %q not found", req.EntityId)
		}
		return nil, status.Error(codes.Internal, err.Error())
	}
	vals, err := q.k.MemberValidators(ctx, req.EntityId)
	if err != nil {
		return nil, status.Error(codes.Internal, err.Error())
	}
	members := make([]types.Member, 0, len(vals))
	for _, v := range vals {
		m, err := q.k.Members.Get(ctx, v)
		if err != nil {
			return nil, status.Error(codes.Internal, err.Error())
		}
		members = append(members, m)
	}
	seats, err := q.k.ActiveSeats(ctx, req.EntityId)
	if err != nil {
		return nil, status.Error(codes.Internal, err.Error())
	}
	return &types.QueryEntityResponse{Entity: e, Members: members, ActiveSeats: seats}, nil
}

// ValidatorEntity reports where a validator stands: attested into an entity,
// holding a frozen seat, or waiting for the activation gate. A validator the
// module has never seen gets an all-zero answer, not an error.
func (q queryServer) ValidatorEntity(ctx context.Context, req *types.QueryValidatorEntityRequest) (*types.QueryValidatorEntityResponse, error) {
	if req == nil {
		return nil, status.Error(codes.InvalidArgument, "invalid request")
	}
	val, err := sdk.ValAddressFromBech32(req.Validator)
	if err != nil {
		return nil, status.Errorf(codes.InvalidArgument, "invalid validator: %s", err)
	}
	res := &types.QueryValidatorEntityResponse{}
	m, found, err := q.k.EntityOf(ctx, val)
	if err != nil {
		return nil, status.Error(codes.Internal, err.Error())
	}
	if found {
		res.Attested = true
		res.EntityId = m.EntityId
	}
	fs, err := q.k.Frozen.Get(ctx, val)
	switch {
	case err == nil:
		res.Frozen = &fs
	case !errors.Is(err, collections.ErrNotFound):
		return nil, status.Error(codes.Internal, err.Error())
	}
	pending, err := q.k.Pending.Has(ctx, val)
	if err != nil {
		return nil, status.Error(codes.Internal, err.Error())
	}
	res.Pending = pending
	return res, nil
}

// Tickets returns one lottery ticket row per attested entity (spec §11).
func (q queryServer) Tickets(ctx context.Context, req *types.QueryTicketsRequest) (*types.QueryTicketsResponse, error) {
	if req == nil {
		return nil, status.Error(codes.InvalidArgument, "invalid request")
	}
	tickets, err := q.k.Tickets(ctx)
	if err != nil {
		return nil, status.Error(codes.Internal, err.Error())
	}
	return &types.QueryTicketsResponse{Tickets: tickets}, nil
}

// VrfKey reports a validator's standing key offer and the key bound on its
// member row. A validator with neither gets an empty answer, not an error.
func (q queryServer) VrfKey(ctx context.Context, req *types.QueryVrfKeyRequest) (*types.QueryVrfKeyResponse, error) {
	if req == nil {
		return nil, status.Error(codes.InvalidArgument, "invalid request")
	}
	val, err := sdk.ValAddressFromBech32(req.Validator)
	if err != nil {
		return nil, status.Errorf(codes.InvalidArgument, "invalid validator: %s", err)
	}
	res := &types.QueryVrfKeyResponse{}
	offer, err := q.k.VrfKeyOffers.Get(ctx, val)
	switch {
	case err == nil:
		res.Offer = &offer
	case !errors.Is(err, collections.ErrNotFound):
		return nil, status.Error(codes.Internal, err.Error())
	}
	m, found, err := q.k.EntityOf(ctx, val)
	if err != nil {
		return nil, status.Error(codes.Internal, err.Error())
	}
	if found {
		res.EntityId = m.EntityId
		res.VrfPublicKey = m.VrfPublicKey
		res.VrfKeyHeight = m.VrfKeyHeight
	}
	return res, nil
}
