package types

import "cosmossdk.io/collections"

const (
	// ModuleName defines the module name
	ModuleName = "rewards"

	// StoreKey defines the primary module store key
	StoreKey = ModuleName

	// GovModuleName duplicates the gov module's name to avoid a dependency with x/gov.
	// It should be synced with the gov module's name if it is ever changed.
	// See: https://github.com/cosmos/cosmos-sdk/blob/v0.52.0-beta.2/x/gov/types/keys.go#L9
	GovModuleName = "gov"
)

// ParamsKey is the prefix to retrieve all Params
var ParamsKey = collections.NewPrefix("p_rewards")

// RewardSnapshotKey is the prefix for per-epoch, per-validator reward records,
// keyed by (epoch_id, operator address bytes).
var RewardSnapshotKey = collections.NewPrefix("reward_snapshot")

// RemainderKey stores the base units carried forward from an epoch pool that did
// not divide evenly among its recipients (ADR-0011 D5).
var RemainderKey = collections.NewPrefix("remainder")

// ObservedGasFeesKey accumulates the gas revenue seen during the current epoch.
// It is an OBSERVATION, not a balance: the fees themselves are swept by
// x/distribution long before this module runs. See ADR-0011 D2.
var ObservedGasFeesKey = collections.NewPrefix("observed_gas_fees")
