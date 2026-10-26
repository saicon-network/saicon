package types

import "cosmossdk.io/collections"

const (
	// ModuleName defines the module name
	ModuleName = "participation"

	// StoreKey defines the primary module store key
	StoreKey = ModuleName

	// GovModuleName duplicates the gov module's name to avoid a dependency with x/gov.
	// It should be synced with the gov module's name if it is ever changed.
	// See: https://github.com/cosmos/cosmos-sdk/blob/v0.52.0-beta.2/x/gov/types/keys.go#L9
	GovModuleName = "gov"
)

// ParamsKey is the prefix to retrieve all Params
var ParamsKey = collections.NewPrefix("p_participation")

// ParticipationScoreKey is the prefix for the ParticipationScore storage map,
// keyed by (epoch_id, validator operator address bytes).
// See chain/docs/validators/README.md §5.
var ParticipationScoreKey = collections.NewPrefix("ps_participation")

// EmpiricalPointsKey is the prefix for the EmpiricalPoints storage map,
// keyed by validator operator address bytes. EP gates rewards (invariant V-2).
var EmpiricalPointsKey = collections.NewPrefix("ep_participation")

// EpochBlocksSignedKey / EpochBlocksMissedKey are running per-validator precommit
// tallies for the CURRENT epoch (reset at each epoch boundary). Keyed by validator
// operator address bytes. Working state for the epoch-window uptime score (M1-B).
var EpochBlocksSignedKey = collections.NewPrefix("esi_participation")
var EpochBlocksMissedKey = collections.NewPrefix("emi_participation")
