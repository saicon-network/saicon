package types

import "cosmossdk.io/collections"

const (
	// ModuleName defines the module name.
	ModuleName = "randomness"

	// StoreKey defines the primary module store key.
	StoreKey = ModuleName

	// GovModuleName duplicates the gov module's name to avoid a dependency with x/gov.
	GovModuleName = "gov"

	// ValidatorPrefix is the bech32 prefix of validator operator addresses.
	ValidatorPrefix = "saiconvaloper"
)

// Constants of the construction (spec §13, tier 3: changing one is a code change).
const (
	// QuorumNumerator and QuorumDenominator are the 2/3 quorum of spec §9.2.
	QuorumNumerator   = 2
	QuorumDenominator = 3

	// ShadowLotterySeats is how many entities a shadow draw names (the
	// Register's 22 of 75).
	ShadowLotterySeats uint32 = 22

	// MaxDrawHistory is how many closed rounds are kept.
	MaxDrawHistory uint64 = 52

	// DefaultRotationEpochs is the default params.rotation_epochs.
	DefaultRotationEpochs uint32 = 7
	// DefaultWindowBlocks is the default params.window_blocks.
	DefaultWindowBlocks uint64 = 100

	// MinRotationEpochs and MaxRotationEpochs bound params.rotation_epochs (tier 2).
	MinRotationEpochs uint32 = 1
	MaxRotationEpochs uint32 = 28
	// MinWindowBlocks and MaxWindowBlocks bound params.window_blocks (tier 2).
	MinWindowBlocks uint64 = 10
	MaxWindowBlocks uint64 = 200000
)

// Domain prefixes of the hashed and proved strings (spec §5.3, §9.1, §10.1, §14).
const (
	// AlphaDomain prefixes the VRF input of a round.
	AlphaDomain = "saicon/randomness/alpha/v1"
	// SeedDomain prefixes the hash that chains the seed.
	SeedDomain = "saicon/randomness/seed/v1"
	// DrawDomain prefixes the per-entity draw score.
	DrawDomain = "saicon/randomness/draw/v1"
	// InitialInputDomain is hashed to the default initial input.
	InitialInputDomain = "saicon/randomness/initial-input/v1"
)

// Store prefixes. No prefix may be a prefix of another: the collections schema
// builder refuses that.
var (
	ParamsKey = collections.NewPrefix("p_randomness")
	// CurrentRoundKey holds the one current round.
	CurrentRoundKey = collections.NewPrefix("rn_round")
	// PoolKey holds the pool snapshot, by entity id.
	PoolKey = collections.NewPrefix("rn_pool")
	// DesignatedKey indexes the keyed pool entries by validator.
	DesignatedKey = collections.NewPrefix("rn_designated")
	// SubmissionsKey holds the current round's submissions, by entity id.
	SubmissionsKey = collections.NewPrefix("rn_submissions")
	// DrawsKey holds the closed rounds, by round number.
	DrawsKey = collections.NewPrefix("rn_draws")
)
