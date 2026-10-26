package types

import (
	"fmt"

	sdk "github.com/cosmos/cosmos-sdk/types"
	"github.com/cosmos/cosmos-sdk/types/bech32"
)

// DefaultGenesis returns the default genesis state
func DefaultGenesis() *GenesisState {
	return &GenesisState{
		Params: DefaultParams(),
	}
}

// Validate performs basic genesis state validation returning an error upon any
// failure.
//
// Validation of the validator records is deliberately STRUCTURAL only: it
// rejects a genesis that cannot be loaded into the store without ambiguity
// (duplicate or unparseable addresses, an invalid status enum, a nonsensical
// lock-up) and nothing more. It does not police the VALUES an operator writes —
// a coordinated upgrade may legitimately need to edit exported state, and
// x/staking and x/slashing take the same stance with their own genesis. The
// trust boundary here is the upgrade ceremony, not this function.
func (gs GenesisState) Validate() error {
	if err := gs.Params.Validate(); err != nil {
		return err
	}

	seen := make(map[string]struct{}, len(gs.Validators))
	for i, md := range gs.Validators {
		if md.ValidatorAddress == "" {
			return fmt.Errorf("validators[%d]: empty validator address", i)
		}
		// Decoded prefix-agnostically ON PURPOSE. The keeper normalises this field
		// with the module's injected address.Codec, which depinject binds to the
		// ACCOUNT codec — so stored addresses carry the account prefix ("saicon1…")
		// rather than the validator one ("saiconvaloper1…"). Pinning a prefix here
		// would reject every genuinely exported record. (That prefix mismatch is a
		// pre-existing wart in SetValidatorMetadata, tracked separately; genesis
		// must accept what the keeper actually writes.)
		_, raw, err := bech32.DecodeAndConvert(md.ValidatorAddress)
		if err != nil {
			return fmt.Errorf("validators[%d] (%s): invalid validator address: %w", i, md.ValidatorAddress, err)
		}
		if err := sdk.VerifyAddressFormat(raw); err != nil {
			return fmt.Errorf("validators[%d] (%s): invalid validator address: %w", i, md.ValidatorAddress, err)
		}
		if _, dup := seen[md.ValidatorAddress]; dup {
			return fmt.Errorf("validators[%d]: duplicate validator address %s", i, md.ValidatorAddress)
		}
		seen[md.ValidatorAddress] = struct{}{}

		if !md.Status.IsValid() {
			return fmt.Errorf("validators[%d] (%s): invalid status %d (must not be STATUS_UNSPECIFIED)",
				i, md.ValidatorAddress, md.Status)
		}
		if md.LockupDurationSeconds < 0 {
			return fmt.Errorf("validators[%d] (%s): negative lockup_duration_seconds %d",
				i, md.ValidatorAddress, md.LockupDurationSeconds)
		}
		if md.LockupDurationSeconds > MaxRepresentableLockupSeconds {
			return fmt.Errorf("validators[%d] (%s): lockup_duration_seconds %d overflows the expiry (maximum %d)",
				i, md.ValidatorAddress, md.LockupDurationSeconds, MaxRepresentableLockupSeconds)
		}
	}

	return nil
}
