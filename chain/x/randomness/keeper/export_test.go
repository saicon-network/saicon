package keeper

import (
	"cosmossdk.io/collections"

	"github.com/saicon-network/saicon/chain/x/randomness/types"
)

// Draws exposes the unexported draw history to the keeper's own tests. This
// file is compiled only under `go test`, so it adds no production API.
func Draws(k Keeper) collections.Map[uint64, types.Draw] { return k.draws }
