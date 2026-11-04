package keeper

import (
	"context"
	"errors"

	"cosmossdk.io/collections"
	errorsmod "cosmossdk.io/errors"
	"cosmossdk.io/math"
	"github.com/cosmos/cosmos-sdk/types/query"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/saicon-network/saicon/chain/x/compliance/types"
	entityregistrytypes "github.com/saicon-network/saicon/chain/x/entityregistry/types"
)

// Offence returns one recorded equivocation by its evidence hash.
func (q queryServer) Offence(ctx context.Context, req *types.QueryOffenceRequest) (*types.QueryOffenceResponse, error) {
	if req == nil {
		return nil, status.Error(codes.InvalidArgument, "invalid request")
	}
	hash, err := types.ParseEvidenceHash(req.EvidenceHash)
	if err != nil {
		return nil, status.Errorf(codes.InvalidArgument, "invalid evidence hash: %v", err)
	}
	o, err := q.k.Offences.Get(ctx, hash)
	if errors.Is(err, collections.ErrNotFound) {
		return nil, status.Errorf(codes.NotFound, "no offence with hash %s", req.EvidenceHash)
	}
	if err != nil {
		return nil, status.Error(codes.Internal, "internal error")
	}
	return &types.QueryOffenceResponse{Offence: o}, nil
}

// Offences pages through every recorded equivocation in hash order.
func (q queryServer) Offences(ctx context.Context, req *types.QueryOffencesRequest) (*types.QueryOffencesResponse, error) {
	if req == nil {
		return nil, status.Error(codes.InvalidArgument, "invalid request")
	}
	offences, pageRes, err := query.CollectionPaginate(ctx, q.k.Offences, req.Pagination,
		func(_ []byte, o types.Offence) (types.Offence, error) { return o, nil })
	if err != nil {
		return nil, status.Error(codes.InvalidArgument, err.Error())
	}
	return &types.QueryOffencesResponse{Offences: offences, Pagination: pageRes}, nil
}

// Case returns one entity's case. An entity without offences, or one the
// registry never had, answers an empty, unarmed case rather than an error.
func (q queryServer) Case(ctx context.Context, req *types.QueryCaseRequest) (*types.QueryCaseResponse, error) {
	if req == nil {
		return nil, status.Error(codes.InvalidArgument, "invalid request")
	}
	c, err := q.k.CaseOf(ctx, req.EntityId)
	if err != nil {
		return nil, status.Error(codes.Internal, "internal error")
	}
	return &types.QueryCaseResponse{Case: c}, nil
}

// ArmedCases lists every armed entity with at least one unfired offence, sorted
// by entity id (the order of the by-entity index).
func (q queryServer) ArmedCases(ctx context.Context, req *types.QueryArmedCasesRequest) (*types.QueryArmedCasesResponse, error) {
	if req == nil {
		return nil, status.Error(codes.InvalidArgument, "invalid request")
	}
	it, err := q.k.OffencesByEntity.Iterate(ctx, nil)
	if err != nil {
		return nil, status.Error(codes.Internal, "internal error")
	}
	defer func() { _ = it.Close() }()

	var entities []string
	for ; it.Valid(); it.Next() {
		key, err := it.Key()
		if err != nil {
			return nil, status.Error(codes.Internal, "internal error")
		}
		if n := len(entities); n == 0 || entities[n-1] != key.K1() {
			entities = append(entities, key.K1())
		}
	}

	var cases []types.CoordinatedCase
	for _, id := range entities {
		c, err := q.k.CaseOf(ctx, id)
		if err != nil {
			return nil, status.Error(codes.Internal, "internal error")
		}
		if c.Armed && len(c.UnfiredHashes) > 0 {
			cases = append(cases, c)
		}
	}
	return &types.QueryArmedCasesResponse{Cases: cases}, nil
}

// Sanction returns one executed sanction by id.
func (q queryServer) Sanction(ctx context.Context, req *types.QuerySanctionRequest) (*types.QuerySanctionResponse, error) {
	if req == nil {
		return nil, status.Error(codes.InvalidArgument, "invalid request")
	}
	s, err := q.k.Sanctions.Get(ctx, req.Id)
	if errors.Is(err, collections.ErrNotFound) {
		return nil, status.Errorf(codes.NotFound, "no sanction with id %d", req.Id)
	}
	if err != nil {
		return nil, status.Error(codes.Internal, "internal error")
	}
	return &types.QuerySanctionResponse{Sanction: s}, nil
}

// Sanctions pages through every executed sanction in id order.
func (q queryServer) Sanctions(ctx context.Context, req *types.QuerySanctionsRequest) (*types.QuerySanctionsResponse, error) {
	if req == nil {
		return nil, status.Error(codes.InvalidArgument, "invalid request")
	}
	sanctions, pageRes, err := query.CollectionPaginate(ctx, q.k.Sanctions, req.Pagination,
		func(_ uint64, s types.Sanction) (types.Sanction, error) { return s, nil })
	if err != nil {
		return nil, status.Error(codes.InvalidArgument, err.Error())
	}
	return &types.QuerySanctionsResponse{Sanctions: sanctions, Pagination: pageRes}, nil
}

// SanctionPreview shows what a MsgFireCoordinatedSanction would do right now. It
// parses its inputs first, so a malformed entity id, hash list or fraction is an
// InvalidArgument error; it then runs PlanSanction, the function the firing
// itself uses, on the query context. A planning refusal, which is always one of
// this module's registered errors, is an answer, not a gRPC error: {fireable:
// false, reason: the refusal's text}. Any other error (a store or another
// module's read failing) is codes.Internal, because it says nothing about
// whether the firing would execute. Nothing is written and no event is emitted.
func (q queryServer) SanctionPreview(ctx context.Context, req *types.QuerySanctionPreviewRequest) (*types.QuerySanctionPreviewResponse, error) {
	if req == nil {
		return nil, status.Error(codes.InvalidArgument, "invalid request")
	}
	if err := entityregistrytypes.ValidateEntityID(req.EntityId); err != nil {
		return nil, status.Errorf(codes.InvalidArgument, "invalid entity id: %v", err)
	}
	if _, err := types.ParseEvidenceHashes(req.EvidenceHashes); err != nil {
		return nil, status.Errorf(codes.InvalidArgument, "invalid evidence hashes: %v", err)
	}
	fraction, err := math.LegacyNewDecFromStr(req.Fraction)
	if err != nil {
		return nil, status.Errorf(codes.InvalidArgument, "invalid fraction %q: %v", req.Fraction, err)
	}

	plan, err := q.k.PlanSanction(ctx, req.EntityId, req.EvidenceHashes, fraction)
	if err != nil {
		if codespace, _, _ := errorsmod.ABCIInfo(err, false); codespace != types.ModuleName {
			return nil, status.Error(codes.Internal, "internal error")
		}
		return &types.QuerySanctionPreviewResponse{Fireable: false, Reason: err.Error()}, nil
	}
	return &types.QuerySanctionPreviewResponse{Fireable: true, Plan: plan}, nil
}
