package keeper

import (
	"context"
	"fmt"

	"github.com/cosmos/cosmos-sdk/types/bech32"

	"github.com/saicon-network/saicon/chain/x/compliance/types"
)

// InitGenesis initializes the module's state from a provided genesis state.
//
// The compliance states are restored because they are the SOURCE OF TRUTH that
// x/validatorregistry's compliance_flag only mirrors. Losing them across an
// export/import was worse than losing the mirror: the reward gate reads a missing
// state as innocent (`compliant := !found || state.Compliant`), so a validator
// sanctioned with C=0 became reward-eligible again after a routine coordinated
// restart, and the evidence trail behind the sanction was gone with it.
func (k Keeper) InitGenesis(ctx context.Context, genState types.GenesisState) error {
	if err := k.Params.Set(ctx, genState.Params); err != nil {
		return err
	}

	for i, cs := range genState.ComplianceStates {
		// Prefix-agnostic on purpose — see the matching note in the registry's
		// InitGenesis: the injected codec is the account codec, so an export carries
		// account-prefixed addresses while a hand-edited genesis may use the
		// canonical valoper form. Both decode to the same bytes.
		_, raw, err := bech32.DecodeAndConvert(cs.ValidatorAddress)
		if err != nil {
			return fmt.Errorf("compliance genesis: compliance_states[%d] (%s): %w", i, cs.ValidatorAddress, err)
		}
		if err := k.ComplianceState.Set(ctx, raw, cs); err != nil {
			return fmt.Errorf("compliance genesis: compliance_states[%d] (%s): %w", i, cs.ValidatorAddress, err)
		}
	}

	return nil
}

// ExportGenesis returns the module's exported genesis.
//
// States are emitted in store-key order, so the export is deterministic — two
// nodes exporting the same state must produce identical consensus-critical output.
func (k Keeper) ExportGenesis(ctx context.Context) (*types.GenesisState, error) {
	genesis := types.DefaultGenesis()

	params, err := k.Params.Get(ctx)
	if err != nil {
		return nil, err
	}
	genesis.Params = params

	iter, err := k.ComplianceState.Iterate(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer func() { _ = iter.Close() }()

	for ; iter.Valid(); iter.Next() {
		cs, err := iter.Value()
		if err != nil {
			return nil, err
		}
		genesis.ComplianceStates = append(genesis.ComplianceStates, cs)
	}

	return genesis, nil
}
