package types

import "cosmossdk.io/collections"

const (
	// ModuleName defines the module name.
	ModuleName = "delegationrouter"

	// StoreKey defines the primary module store key.
	StoreKey = ModuleName

	// GovModuleName duplicates the gov module's name to avoid a dependency with x/gov.
	GovModuleName = "gov"

	// BondDenom is the staking denom; every amount is measured in it.
	BondDenom = "nscn"
)

var ParamsKey = collections.NewPrefix("p_delegationrouter")
