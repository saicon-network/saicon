package types

import (
	"context"

	sdk "github.com/cosmos/cosmos-sdk/types"

	entitytypes "github.com/saicon-network/saicon/chain/x/entityregistry/types"
	stakeenginetypes "github.com/saicon-network/saicon/chain/x/stakeengine/types"
	vrtypes "github.com/saicon-network/saicon/chain/x/validatorregistry/types"
)

// NOTE (depinject): every interface declares methods only the intended keeper
// has, so the concrete keepers bind uniquely (see x/stakeengine).

// EntityregistryKeeper is the entity registry surface the round uses.
type EntityregistryKeeper interface {
	// Tickets lists one ticket per attested entity, sorted by id.
	Tickets(ctx context.Context) ([]entitytypes.Ticket, error)
	// EntityOf returns the member row of a validator, if it belongs to an entity.
	EntityOf(ctx context.Context, val sdk.ValAddress) (entitytypes.Member, bool, error)
}

// ValidatorregistryKeeper is the registry status the pool snapshot reads.
type ValidatorregistryKeeper interface {
	GetValidatorStatus(ctx context.Context, addr sdk.ValAddress) (vrtypes.Status, bool, error)
}

// StakeengineKeeper is the entity standing the pool snapshot reads.
type StakeengineKeeper interface {
	EntityStanding(ctx context.Context, v sdk.ValAddress) (stakeenginetypes.EntityStanding, error)
}

// ParticipationKeeper is the epoch grid rotations are placed on.
type ParticipationKeeper interface {
	EpochLengthBlocks(ctx context.Context) (uint64, error)
}
