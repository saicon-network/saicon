package types

import "cosmossdk.io/collections"

const (
	// ModuleName defines the module name. It is also the escrow module account.
	ModuleName = "stakeengine"

	// StoreKey defines the primary module store key.
	StoreKey = ModuleName

	// GovModuleName duplicates the gov module's name to avoid a dependency with x/gov.
	GovModuleName = "gov"

	// BondDenom is the staking denom; every cap is measured in it.
	BondDenom = "nscn"

	// AccountPrefix and ValidatorPrefix are the chain's bech32 prefixes. Params and
	// genesis validate addresses against them explicitly, not against the
	// process-global sdk config — which is "cosmos" inside unit tests.
	AccountPrefix   = "saicon"
	ValidatorPrefix = "saiconvaloper"
)

var (
	ParamsKey   = collections.NewPrefix("p_stakeengine")
	CapStateKey = collections.NewPrefix("cap_state")
	QueueKey    = collections.NewPrefix("queue_entry")
	QueueSeqKey = collections.NewPrefix("queue_seq")
	QueueLenKey = collections.NewPrefix("queue_len")
	// QueueByDelegatorKey indexes the queue by (delegator, validator) → entry
	// count, so the ≤ 3 slot rule reads it without walking the queue.
	QueueByDelegatorKey = collections.NewPrefix("queue_by_delegator")
)

// Transient-store keys of the staking backstop (S7 spec §6). Transient: reset
// every block, never in consensus state.
var (
	HookRecordPrefix = []byte{0x01}
	BeginBlockWindow = []byte{0x02}
)

// NonCirculatingModules are module accounts whose balances are not publicly owned
// and are subtracted from public circulating supply: the community pool and
// distribution's accruals, the fee collector, gov deposits, and the x/rewards
// pool. The bonded pool, the not-bonded pool and this module's escrow are
// deliberately ABSENT: those tokens belong to the public, and subtracting them
// would make bonding shrink the cap that limits bonding (ADR-0012, open problem 2).
var NonCirculatingModules = []string{"distribution", "fee_collector", "gov", "rewards"}
