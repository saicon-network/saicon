package app

import (
	"encoding/json"

	"cosmossdk.io/math"
	"github.com/cosmos/cosmos-sdk/codec"
	"github.com/cosmos/cosmos-sdk/x/slashing"
	slashingtypes "github.com/cosmos/cosmos-sdk/x/slashing/types"
)

// GenesisState of the blockchain is represented here as a map of raw json
// messages key'd by a identifier string.
// The identifier is used to determine which module genesis information belongs
// to so it may be appropriately routed during init chain.
// Within this application default genesis information is retrieved from
// the ModuleBasicManager which populates json from each BasicModule
// object provided to it during init.
type GenesisState map[string]json.RawMessage

// DoubleSignSlashFraction is the share of bonded stake a double-sign burns
// (ADR-0010 §2: the Register doubles the SDK's 5%; project decisions 2026-07-28
// and 2026-10-28). The binary's default genesis carries it, the genesis scripts
// write the same value, and the v0.10.0 upgrade raises a lower stored fraction
// to it.
func DoubleSignSlashFraction() math.LegacyDec {
	return math.LegacyNewDecWithPrec(10, 2)
}

// DowntimeSlashFraction is zero: ordinary downtime carries no stake slash and
// is punished through the participation layer (ADR-0010 §1). x/compliance's
// BeforeValidatorSlashed treats any positive burn as a double-sign, which is
// exact only while this is zero, because x/staking skips the hook on a zero
// burn.
func DowntimeSlashFraction() math.LegacyDec {
	return math.LegacyZeroDec()
}

// slashingBasic is x/slashing's basic module with the Register's slashing
// policy as its default genesis. It is supplied in AppConfig as a custom module
// basic, the route the SDK offers for changing a module's defaults, so every
// path that builds a genesis from the basic manager (`saicond init`,
// `saicond testnet`, App.DefaultGenesis and so the test harness) starts from it.
// The simulations draw their slashing genesis at random and bypass it, so
// app/sim_params.json pins the same two values for them. The genesis scripts
// overwrite the two fields with jq;
// TestGenesisScriptsAndTheBinaryAgreeOnTheSlashingPolicy keeps both equal.
type slashingBasic struct {
	slashing.AppModuleBasic
}

// DefaultGenesis is the SDK's default with the double-sign and downtime
// fractions of the slashing policy.
func (slashingBasic) DefaultGenesis(cdc codec.JSONCodec) json.RawMessage {
	gs := slashingtypes.DefaultGenesisState()
	gs.Params.SlashFractionDoubleSign = DoubleSignSlashFraction()
	gs.Params.SlashFractionDowntime = DowntimeSlashFraction()
	return cdc.MustMarshalJSON(gs)
}
