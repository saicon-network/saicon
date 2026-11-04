package keeper

import (
	"context"
	"fmt"

	"cosmossdk.io/collections"
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

	for i, o := range genState.Offences {
		hash, err := types.ParseEvidenceHash(o.EvidenceHash)
		if err != nil {
			return fmt.Errorf("compliance genesis: offences[%d]: %w", i, err)
		}
		if err := k.Offences.Set(ctx, hash, o); err != nil {
			return fmt.Errorf("compliance genesis: offences[%d] (%s): %w", i, o.EvidenceHash, err)
		}
		// The by-entity index is not exported; it is the duplicate-vote offences
		// that carry an entity.
		if o.Kind == types.MisbehaviorKind_MISBEHAVIOR_KIND_DUPLICATE_VOTE && o.EntityId != "" {
			if err := k.OffencesByEntity.Set(ctx, collections.Join(o.EntityId, hash)); err != nil {
				return fmt.Errorf("compliance genesis: offences[%d] (%s): index: %w", i, o.EvidenceHash, err)
			}
		}
	}

	for i, s := range genState.Sanctions {
		if err := k.Sanctions.Set(ctx, s.Id, s); err != nil {
			return fmt.Errorf("compliance genesis: sanctions[%d] (%d): %w", i, s.Id, err)
		}
	}

	consCodec := k.stakingKeeper.ConsensusAddressCodec()
	for i, addr := range genState.RemovedConsensusAddresses {
		cons, err := consCodec.StringToBytes(addr)
		if err != nil {
			return fmt.Errorf("compliance genesis: removed_consensus_addresses[%d] (%s): %w", i, addr, err)
		}
		if err := k.RemovedConsensus.Set(ctx, cons); err != nil {
			return fmt.Errorf("compliance genesis: removed_consensus_addresses[%d] (%s): %w", i, addr, err)
		}
	}

	// 0 reads as 1: the first sanction is 1.
	if err := k.SanctionSeq.Set(ctx, genState.EffectiveNextSanctionID()); err != nil {
		return fmt.Errorf("compliance genesis: sanction sequence: %w", err)
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

	oIter, err := k.Offences.Iterate(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer func() { _ = oIter.Close() }()
	for ; oIter.Valid(); oIter.Next() {
		o, err := oIter.Value()
		if err != nil {
			return nil, err
		}
		genesis.Offences = append(genesis.Offences, o)
	}

	sIter, err := k.Sanctions.Iterate(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer func() { _ = sIter.Close() }()
	for ; sIter.Valid(); sIter.Next() {
		s, err := sIter.Value()
		if err != nil {
			return nil, err
		}
		genesis.Sanctions = append(genesis.Sanctions, s)
	}

	consCodec := k.stakingKeeper.ConsensusAddressCodec()
	if err := k.RemovedConsensus.Walk(ctx, nil, func(cons []byte) (bool, error) {
		addr, err := consCodec.BytesToString(cons)
		if err != nil {
			return true, err
		}
		genesis.RemovedConsensusAddresses = append(genesis.RemovedConsensusAddresses, addr)
		return false, nil
	}); err != nil {
		return nil, err
	}

	next, err := k.SanctionSeq.Peek(ctx)
	if err != nil {
		return nil, err
	}
	genesis.NextSanctionId = next

	return genesis, nil
}
