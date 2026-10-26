package types

import (
	"math"
	"time"

	"cosmossdk.io/collections"
)

const (
	// ModuleName defines the module name
	ModuleName = "validatorregistry"

	// StoreKey defines the primary module store key
	StoreKey = ModuleName

	// GovModuleName duplicates the gov module's name to avoid a dependency with x/gov.
	// It should be synced with the gov module's name if it is ever changed.
	// See: https://github.com/cosmos/cosmos-sdk/blob/v0.52.0-beta.2/x/gov/types/keys.go#L9
	GovModuleName = "gov"

	// DefaultLockupDurationSeconds is the 60-day validator lock-up applied at
	// registration when no explicit duration is provided. See
	// chain/docs/validators/README.md §3.
	//
	// Project decision 2026-09-22, lowered from 180 days. It governs registrations
	// made after the change: a record already carrying 180 days keeps it, because
	// unbonding.go clamps a stored duration UP to this default and never down, and
	// because the window is frozen at bonding time (V-3). A running chain therefore
	// does not shorten anyone's existing lock-up; only a fresh genesis starts
	// everyone at 60.
	DefaultLockupDurationSeconds int64 = 60 * 24 * 60 * 60

	// MaxLockupDurationSeconds caps the lock-up a validator may commit to: ten
	// years. The duration is signer-supplied and becomes a time.Duration in
	// nanoseconds, which overflows int64 past ~292 years and wraps the expiry into
	// the past — an unlocked self-stake. The cap keeps the arithmetic far from that
	// edge while leaving room for any lock-up an operator would mean seriously.
	MaxLockupDurationSeconds int64 = 10 * 365 * 24 * 60 * 60

	// MaxRepresentableLockupSeconds is the longest lock-up whose expiry can be
	// computed at all: one second more overflows the nanosecond time.Duration
	// (~292 years). Registration applies the much lower MaxLockupDurationSeconds;
	// this bound is for records that already exist — stored state and genesis —
	// which may predate that cap and are honoured as written up to here.
	MaxRepresentableLockupSeconds int64 = math.MaxInt64 / int64(time.Second)
)

// ParamsKey is the prefix to retrieve all Params
var ParamsKey = collections.NewPrefix("p_validatorregistry")

// ValidatorMetadataKey is the prefix for the ValidatorMetadata storage map.
// Keyed by validator operator address bytes (sdk.ValAddress).
// See chain/docs/architecture/README.md §7 — module prefix range 0x80..0x8F.
var ValidatorMetadataKey = collections.NewPrefix("vm_validatorregistry")
