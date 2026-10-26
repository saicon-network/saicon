package types

import (
	"cosmossdk.io/collections"
)

const (
	// ModuleName defines the module name
	ModuleName = "compliance"

	// StoreKey defines the primary module store key
	StoreKey = ModuleName

	// GovModuleName duplicates the gov module's name to avoid a dependency with x/gov.
	// It should be synced with the gov module's name if it is ever changed.
	// See: https://github.com/cosmos/cosmos-sdk/blob/v0.52.0-beta.2/x/gov/types/keys.go#L9
	GovModuleName = "gov"
)

// ParamsKey is the prefix to retrieve all Params
var ParamsKey = collections.NewPrefix("p_compliance")

// ComplianceStateKey is the prefix for the ComplianceState storage map,
// keyed by validator operator address bytes (sdk.ValAddress).
// See chain/docs/architecture/README.md §7 — compliance prefix range 0xA0..0xAF.
var ComplianceStateKey = collections.NewPrefix("cs_compliance")

// SUPERSEDED (ADR-0010). The double-sign inference used to key off a midpoint
// of the SDK's 0.01 downtime / 0.05 double-sign band. Under the Register that
// band is empty — downtime carries a zero stake slash and double-sign is 0.10 —
// so BeforeValidatorSlashed now treats any positive burn as a double-sign,
// which x/staking makes exact by never invoking the hook on a zero burn.
