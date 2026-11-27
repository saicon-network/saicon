package keeper

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"strconv"

	"cosmossdk.io/collections"
	"cosmossdk.io/math"
	sdk "github.com/cosmos/cosmos-sdk/types"

	"github.com/saicon-network/saicon/chain/x/participation/types"
)

// GetWarp returns a validator's WARP row.
// Returns (row, true, nil) when present, (zero, false, nil) when absent: a
// validator that has never been scored nor punished has no WARP yet.
func (k Keeper) GetWarp(ctx context.Context, addr sdk.ValAddress) (types.WarpState, bool, error) {
	w, err := k.Warp.Get(ctx, addr.Bytes())
	if err != nil {
		if errors.Is(err, collections.ErrNotFound) {
			return types.WarpState{}, false, nil
		}
		return types.WarpState{}, false, err
	}
	return w, true, nil
}

// SetWarp persists a validator's WARP row, keyed by operator address bytes.
func (k Keeper) SetWarp(ctx context.Context, addr sdk.ValAddress, w types.WarpState) error {
	return k.Warp.Set(ctx, addr.Bytes(), w)
}

// warpUpdate is one validator's WARP step in an epoch.
type warpUpdate struct {
	ps, before, after math.LegacyDec
}

// nextWarp works out a scored validator's WARP for this epoch from its
// computed_score (S10 rulings R1, R6). It writes nothing: ComputeEpoch pins the
// result in the snapshot and then stores it.
//
// The starting point is the stored value, or warp_initial for a validator never
// measured before. A row ZeroReputation wrote before the first measurement
// counts as stored: the punished validator starts from 0, not from 80.
func (k Keeper) nextWarp(ctx context.Context, addr sdk.ValAddress, score math.LegacyDec, p types.Params) (warpUpdate, error) {
	before := p.WarpInitial
	w, found, err := k.GetWarp(ctx, addr)
	if err != nil {
		return warpUpdate{}, err
	}
	if found {
		if err := types.ValidateWarpValue(w.Value); err != nil {
			return warpUpdate{}, fmt.Errorf("stored WARP of %s: %w", addr, err)
		}
		before = w.Value
	}
	ps := types.WarpInput(score)
	return warpUpdate{
		ps:     ps,
		before: before,
		after:  types.NextWarp(before, ps, p.WarpFall, p.WarpRise),
	}, nil
}

// storeWarp records an epoch's WARP step and emits warp_updated.
func (k Keeper) storeWarp(ctx context.Context, addr sdk.ValAddress, epochID uint64, u warpUpdate, threshold math.LegacyDec) error {
	if err := k.SetWarp(ctx, addr, types.WarpState{
		ValidatorAddress: addr.String(),
		Value:            u.after,
		LastEpoch:        epochID,
	}); err != nil {
		return err
	}
	sdk.UnwrapSDKContext(ctx).EventManager().EmitEvent(sdk.NewEvent(
		types.EventTypeWarpUpdated,
		sdk.NewAttribute(types.AttributeKeyValidator, addr.String()),
		sdk.NewAttribute(types.AttributeKeyEpochID, strconv.FormatUint(epochID, 10)),
		sdk.NewAttribute(types.AttributeKeyPS, u.ps.String()),
		sdk.NewAttribute(types.AttributeKeyWarpBefore, u.before.String()),
		sdk.NewAttribute(types.AttributeKeyWarpAfter, u.after.String()),
		sdk.NewAttribute(types.AttributeKeyEligible, strconv.FormatBool(types.WarpEligible(u.after, threshold))),
	))
	return nil
}

// zeroWarp sets a validator's WARP to 0 (S10 ruling R7). The epoch it was last
// measured in is kept: punishment is not a measurement. A validator never
// measured gets a row anyway, with last_epoch 0, so that it does not start
// from warp_initial when it is first scored.
func (k Keeper) zeroWarp(ctx context.Context, addr sdk.ValAddress) error {
	w, _, err := k.GetWarp(ctx, addr)
	if err != nil {
		return err
	}
	w.ValidatorAddress = addr.String()
	w.Value = math.LegacyZeroDec()
	return k.SetWarp(ctx, addr, w)
}

// WarpInvariant implements invariant V-16: every WARP row is
//
//   - stored under the validator it names;
//   - set, and within [0, 100];
//   - backed by a snapshot at its last_epoch, the epoch that wrote it; or, with
//     last_epoch 0 (punished before it was ever scored), exactly 0.
//
// The engine maintains all three by construction (ComputeEpoch writes the
// snapshot and the row together; ZeroReputation keeps last_epoch). This is the
// cross-check, and it is checked rather than halted: until S11 hands WARP to
// consensus, a broken row distorts a preview, not a block.
//
// Returns (message, broken). broken == true indicates a violation.
func (k Keeper) WarpInvariant(ctx context.Context) (string, bool) {
	var (
		count  int
		detail string
	)
	breach := func(format string, args ...any) {
		count++
		detail += "  " + fmt.Sprintf(format, args...) + "\n"
	}

	err := k.Warp.Walk(ctx, nil, func(key []byte, w types.WarpState) (bool, error) {
		val := sdk.ValAddress(key)
		named, e := valAddrOf(w.ValidatorAddress)
		if e != nil || !bytes.Equal(named, key) {
			breach("the WARP row of %s names %s", val, w.ValidatorAddress)
		}
		if e := types.ValidateWarpValue(w.Value); e != nil {
			breach("the WARP of %s: %v", val, e)
			return false, nil
		}
		if w.LastEpoch == 0 {
			if !w.Value.IsZero() {
				breach("%s was never scored but holds %s", val, w.Value)
			}
			return false, nil
		}
		has, e := k.ParticipationScore.Has(ctx, collections.Join(w.LastEpoch, key))
		if e != nil {
			return true, e
		}
		if !has {
			breach("the WARP of %s has no snapshot at its last epoch %d", val, w.LastEpoch)
		}
		return false, nil
	})
	if err != nil {
		return fmt.Sprintf("participation V-16 invariant: iteration error: %v", err), true
	}
	if count > 0 {
		return fmt.Sprintf("participation V-16 invariant broken: %d WARP breach(es)\n%s", count, detail), true
	}
	return "participation V-16 invariant: every WARP row is well-formed", false
}

// WarpParams returns the WARP part of the module parameters, unvalidated.
// x/rewards reads warp_initial and warp_threshold through it for the
// voting-power preview, and refuses to answer when Validate fails.
func (k Keeper) WarpParams(ctx context.Context) (types.WarpParams, error) {
	p, err := k.Params.Get(ctx)
	if err != nil {
		return types.WarpParams{}, err
	}
	return p.Warp(), nil
}
