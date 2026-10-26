package keeper

import (
	"context"
	"fmt"
	"time"

	sdk "github.com/cosmos/cosmos-sdk/types"
	"github.com/cosmos/cosmos-sdk/types/bech32"

	"github.com/saicon-network/saicon/chain/x/validatorregistry/types"
)

// InitGenesis initializes the module's state from a provided genesis state.
//
// The validator records are restored verbatim. Without this the registry was
// silently erased by any export/import cycle: participation and rewards would
// then see "not found" for every validator (nobody ever earns again) and the
// V-3 self-stake lock-up would stop being enforced, because the ante decorator
// reads this same metadata and a missing record reads as "not locked".
//
// Ordering note: this module's InitGenesis runs AFTER x/staking's (see the
// InitGenesis order in app/app_config.go), so any record auto-created by the
// AfterValidatorBonded hook while staking was initialising is overwritten here
// by the exported one. That is the desired precedence — the exported record
// carries the real bonding_start, the frozen lock-up and the compliance flag,
// whereas an auto-created one would restart the lock-up from the restart
// height. TestGenesisImportWinsOverHookAutoCreate pins this.
func (k Keeper) InitGenesis(ctx context.Context, genState types.GenesisState) error {
	if err := k.Params.Set(ctx, genState.Params); err != nil {
		return err
	}

	sdkCtx := sdk.UnwrapSDKContext(ctx)
	height := sdkCtx.BlockHeight()
	blockTime := sdkCtx.BlockTime()

	for i, md := range genState.Validators {
		// Decoded prefix-agnostically: this module's injected codec is the ACCOUNT
		// codec, so its own export carries account-prefixed addresses, but a
		// hand-edited genesis may reasonably use the canonical valoper form. Both
		// resolve to the same bytes; SetValidatorMetadata re-normalises the stored
		// string. Rejecting the canonical form here would mean a genesis that passes
		// `validate-genesis` and then fails to start.
		_, raw, err := bech32.DecodeAndConvert(md.ValidatorAddress)
		if err != nil {
			return fmt.Errorf("validatorregistry genesis: validators[%d] (%s): %w", i, md.ValidatorAddress, err)
		}
		addr := sdk.ValAddress(raw)

		// This module PANICS in EndBlock when an invariant breaks, so a genesis
		// record must never be able to arm one. GenesisState.Validate cannot be
		// relied on for that: module.Manager.InitGenesis never calls it — it is
		// reachable only from the `validate-genesis` and `gentx` CLI commands — so
		// the check has to live here, where a bad record fails as a clean STARTUP
		// error before any block is produced, rather than as a network-wide halt on
		// block one that survives every restart.
		if md.Status == types.Status_STATUS_INACTIVE && blockTime.Before(md.UnbondingCompleteTime) {
			return fmt.Errorf(
				"validatorregistry genesis: validators[%d] (%s): INACTIVE with unbonding_complete_time %s after genesis time %s — this would break invariant V-5 on the first block",
				i, md.ValidatorAddress,
				md.UnbondingCompleteTime.UTC().Format(time.RFC3339), blockTime.UTC().Format(time.RFC3339),
			)
		}

		// Same reasoning: a lock-up too long to express in nanoseconds is refused at
		// startup. LockupExpiry would saturate it safely, but a genesis carrying one
		// was not produced by this chain and should not start silently. Exported
		// records longer than the ten-year registration cap but representable are
		// accepted — they may predate the cap.
		if md.LockupDurationSeconds > types.MaxRepresentableLockupSeconds {
			return fmt.Errorf(
				"validatorregistry genesis: validators[%d] (%s): lockup_duration_seconds %d overflows the expiry (maximum %d)",
				i, md.ValidatorAddress, md.LockupDurationSeconds, types.MaxRepresentableLockupSeconds,
			)
		}

		// Rebase the two absolute block heights onto THIS chain's height space.
		//
		// A zero-height export (`--for-zero-height`) rewrites every staking
		// unbonding and redelegation entry to CreationHeight 0 and restarts the
		// chain near height 1, while these fields would otherwise arrive carrying
		// the old chain's heights. Left verbatim they break two guards in opposite
		// and equally bad directions: V-3's creation-height comparison never matches
		// again (the lock-up backstop silently stops firing), and the unjail cooldown
		// `now - last_status_change_height` goes negative and never clears, bricking
		// jail recovery exactly the way D1 did.
		//
		// Clamping is a no-op for an ordinary export, where the chain resumes at a
		// height above these values.
		md.BondingStartHeight = clampHeight(md.BondingStartHeight, height)
		md.LastStatusChangeHeight = clampHeight(md.LastStatusChangeHeight, height)

		if err := k.SetValidatorMetadata(ctx, addr, md); err != nil {
			return fmt.Errorf("validatorregistry genesis: validators[%d] (%s): %w", i, md.ValidatorAddress, err)
		}
	}

	return nil
}

// clampHeight brings a stored absolute block height into the current chain's
// height space: never above the current height, never below the first block.
// See the rebasing note in InitGenesis.
func clampHeight(stored, current int64) int64 {
	if stored > current {
		stored = current
	}
	return types.LockStartHeight(stored)
}

// ExportGenesis returns the module's exported genesis.
//
// Records are emitted in store-key order (collections.Map iterates by key), so
// the export is deterministic: two nodes exporting the same state produce
// identical output, which genesis — consensus-critical state — requires.
func (k Keeper) ExportGenesis(ctx context.Context) (*types.GenesisState, error) {
	genesis := types.DefaultGenesis()

	params, err := k.Params.Get(ctx)
	if err != nil {
		return nil, err
	}
	genesis.Params = params

	if err := k.IterateValidatorMetadata(ctx, func(_ sdk.ValAddress, md types.ValidatorMetadata) bool {
		genesis.Validators = append(genesis.Validators, md)
		return false
	}); err != nil {
		return nil, err
	}

	return genesis, nil
}
