package keeper

import (
	"context"
	"fmt"

	sdk "github.com/cosmos/cosmos-sdk/types"

	"github.com/saicon-network/saicon/chain/x/randomness/types"
)

// InitGenesis restores the module's state. With no current_round it creates
// round 1 from initial_input (cutoff 0, unscheduled, SCHEDULED); otherwise it
// restores the round, its pool snapshot, submissions and draws, and rebuilds
// the designated-submitter index from the pool. Addresses are decoded with the
// global bech32 config, like every keeper. The stateless checks are
// GenesisState.Validate's, which the SDK does not run at InitChain: only the CLI
// (`saicond genesis validate`) calls it, so a hand-edited genesis that skipped
// the CLI reaches this function unchecked.
//
// What an imported round carries depends on its status. A SCHEDULED round's
// pool, designations and submissions never take part: openRound clears all
// three before it writes its own snapshot. An OPEN round is taken as it stands:
// its designated validators can submit until the rotation height, its imported
// submissions count toward the quorum, and its imported pool is the pool the
// close draws from. The close re-checks it through V-14, which re-verifies
// every submission's proof against its pool entry's key and the round alpha
// and the designation index against the pool, so a forged submission skips the
// round instead of drawing it. V-14 does not compare the pool with the entity
// registry: an OPEN round's pool is trusted as the genesis that carries it.
//
// A round imported past its rotation height needs no special handling: the
// first EndBlock closes it (spec §14).
func (k Keeper) InitGenesis(ctx context.Context, gs types.GenesisState) error {
	if err := k.Params.Set(ctx, gs.Params); err != nil {
		return err
	}
	if gs.CurrentRound == nil {
		if _, err := types.DecodeHex(gs.InitialInput, types.InputHexLen/2); err != nil {
			return fmt.Errorf("randomness genesis: initial_input: %w", err)
		}
		return k.CurrentRound.Set(ctx, types.FirstRound(gs.InitialInput))
	}
	if err := k.CurrentRound.Set(ctx, *gs.CurrentRound); err != nil {
		return err
	}
	for _, e := range gs.Pool {
		if err := k.Pool.Set(ctx, e.EntityId, e); err != nil {
			return err
		}
		if e.Validator == "" {
			continue
		}
		v, err := sdk.ValAddressFromBech32(e.Validator)
		if err != nil {
			return fmt.Errorf("randomness genesis: pool entity %s: %w", e.EntityId, err)
		}
		if err := k.Designated.Set(ctx, v, e.EntityId); err != nil {
			return err
		}
	}
	for _, s := range gs.Submissions {
		if err := k.Submissions.Set(ctx, s.EntityId, s); err != nil {
			return err
		}
	}
	for _, d := range gs.Draws {
		if err := k.draws.Set(ctx, d.Round, d); err != nil {
			return err
		}
	}
	return nil
}

// ExportGenesis returns the module's state. Every collection is walked in key
// order, so two nodes export byte-identical genesis. initial_input is exported
// as the published default: it is read only when current_round is absent, and
// the genesis input is not stored once round 1 exists.
func (k Keeper) ExportGenesis(ctx context.Context) (*types.GenesisState, error) {
	gs := types.DefaultGenesis()
	params, err := k.Params.Get(ctx)
	if err != nil {
		return nil, err
	}
	gs.Params = params
	round, err := k.CurrentRound.Get(ctx)
	if err != nil {
		return nil, err
	}
	gs.CurrentRound = &round
	if err := k.Pool.Walk(ctx, nil, func(_ string, e types.PoolEntry) (bool, error) {
		gs.Pool = append(gs.Pool, e)
		return false, nil
	}); err != nil {
		return nil, err
	}
	if err := k.Submissions.Walk(ctx, nil, func(_ string, s types.Submission) (bool, error) {
		gs.Submissions = append(gs.Submissions, s)
		return false, nil
	}); err != nil {
		return nil, err
	}
	if err := k.draws.Walk(ctx, nil, func(_ uint64, d types.Draw) (bool, error) {
		gs.Draws = append(gs.Draws, d)
		return false, nil
	}); err != nil {
		return nil, err
	}
	return gs, nil
}
