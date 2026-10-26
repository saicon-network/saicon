package keeper

import (
	"context"

	"cosmossdk.io/math"
	sdk "github.com/cosmos/cosmos-sdk/types"
)

// EpochUptimeForTest exposes the unexported epochUptime to the external
// keeper_test package. This file is compiled only under `go test`, so it adds
// no production API surface.
func (k Keeper) EpochUptimeForTest(ctx context.Context, valAddr sdk.ValAddress) (math.LegacyDec, uint64, uint64, error) {
	return k.epochUptime(ctx, valAddr)
}

// DecayEPForTest exposes the unexported decayEP helper to keeper_test.
func DecayEPForTest(ep math.Int, idle int64, epDecay math.LegacyDec) math.Int {
	return decayEP(ep, idle, epDecay)
}
