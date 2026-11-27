package keeper

import (
	"context"
	"errors"
	"fmt"
	"math/big"
	"sort"

	"cosmossdk.io/math"
	sdk "github.com/cosmos/cosmos-sdk/types"
	stakingtypes "github.com/cosmos/cosmos-sdk/x/staking/types"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/saicon-network/saicon/chain/x/rewards/types"
	stakeenginetypes "github.com/saicon-network/saicon/chain/x/stakeengine/types"
)

// reasonWarpBelowThreshold is the preview's own ineligibility reason; the other
// three are the ones emission records.
const reasonWarpBelowThreshold = "warp below threshold"

// errWarpParamsInvalid means x/participation's WARP parameters do not pass
// WarpParams.Validate: a store that has not run the 1 -> 2 migration, or one
// written from params without WARP fields, which read back as zeros. The
// epoch step refuses the same params, so a preview from them would show a
// WARP that is not moving.
var errWarpParamsInvalid = errors.New("x/participation WARP parameters do not validate")

var (
	shareScale = big.NewInt(1_000_000)
	// warpDivisor turns capped·warp into capped·warp/100: a LegacyDec's
	// BigInt carries 18 decimals, and the scale is 100.
	warpDivisor = new(big.Int).Mul(big.NewInt(100), new(big.Int).Exp(big.NewInt(10), big.NewInt(math.LegacyPrecision), nil))
)

// previewVP is the preview's formula (S10 R11): floor(capped · warp/100 · C).
// Computed on integers, so the floor is exact.
func previewVP(capped math.Int, warp math.LegacyDec, compliant bool) math.Int {
	if !compliant || capped.IsZero() || warp.IsZero() {
		return math.ZeroInt()
	}
	n := new(big.Int).Mul(capped.BigInt(), warp.BigInt())
	return math.NewIntFromBigInt(n.Quo(n, warpDivisor))
}

// shareString renders part/total with 6 decimals, truncated. A zero total
// renders "0.000000": there is nothing to divide.
func shareString(part, total *big.Int) string {
	if total.Sign() == 0 {
		return "0.000000"
	}
	q := new(big.Int).Mul(part, shareScale)
	q.Quo(q, total)
	whole, frac := new(big.Int).QuoRem(q, shareScale, new(big.Int))
	return fmt.Sprintf("%s.%06d", whole, frac.Int64())
}

type previewRow struct {
	entry types.VotingPowerPreviewEntry
}

// VotingPowerPreview computes, for every bonded validator, the voting power
// ADR-0008 §1 would give it from today's state (S10 rulings R10-R12):
//
//	vp       = floor( min(bonded, validator_cap) · warp/100 · C )
//	eligible = status ACTIVE  ∧  C = 1  ∧  EP > 0  ∧  warp >= warp_threshold
//
// warp is the stored WARP row, or warp_initial when the validator has none.
// warp_measured is true only when the row comes from a measurement
// (last_epoch > 0): a validator punished before its first score keeps the row
// ZeroReputation wrote, so it is shown at 0, unmeasured. The first three
// conditions are the reward gate itself (rewardGate), so the preview and the
// payout never disagree on who passes them; reason names the first condition
// that fails, in the order above. vp_share divides by the eligible
// validators' vp; stock_share by the bonded set's CometBFT power. Rows are
// ordered by vp descending, ties by operator_address ascending as a string.
// Entity caps, seats and the lottery are not applied.
//
// It reads and writes nothing else, and nothing reads it: consensus power is
// untouched.
func (k Keeper) VotingPowerPreview(ctx context.Context) (*types.QueryVotingPowerPreviewResponse, error) {
	height := sdk.UnwrapSDKContext(ctx).BlockHeight()
	epochLen, err := k.participationKeeper.EpochLengthBlocks(ctx)
	if err != nil {
		return nil, err
	}
	var epoch uint64
	if epochLen > 0 && height > 0 {
		epoch = uint64(height) / epochLen
	}

	wp, err := k.participationKeeper.WarpParams(ctx)
	if err != nil {
		return nil, err
	}
	if err := wp.Validate(); err != nil {
		return nil, fmt.Errorf("%w: %w", errWarpParamsInvalid, err)
	}
	valCap, err := k.stakeengineKeeper.ValidatorCap(ctx)
	if err != nil {
		return nil, err
	}
	powerReduction := k.stakingKeeper.PowerReduction(ctx)

	var (
		rows    []previewRow
		iterErr error
	)
	err = k.stakingKeeper.IterateBondedValidatorsByPower(ctx, func(_ int64, v stakingtypes.ValidatorI) bool {
		row, e := k.previewRow(ctx, v, valCap, wp.Initial, wp.Threshold, powerReduction)
		if e != nil {
			iterErr = e
			return true
		}
		rows = append(rows, row)
		return false
	})
	if err != nil {
		return nil, err
	}
	if iterErr != nil {
		return nil, iterErr
	}

	totalVP, totalStock := new(big.Int), new(big.Int)
	for _, r := range rows {
		if r.entry.Eligible {
			totalVP.Add(totalVP, r.entry.Vp.BigInt())
		}
		totalStock.Add(totalStock, big.NewInt(r.entry.StockPower))
	}

	sort.SliceStable(rows, func(i, j int) bool {
		if c := rows[i].entry.Vp.BigInt().Cmp(rows[j].entry.Vp.BigInt()); c != 0 {
			return c > 0
		}
		return rows[i].entry.OperatorAddress < rows[j].entry.OperatorAddress
	})

	out := make([]types.VotingPowerPreviewEntry, 0, len(rows))
	for _, r := range rows {
		e := r.entry
		e.VpShare = "0"
		if e.Eligible {
			e.VpShare = shareString(e.Vp.BigInt(), totalVP)
		}
		e.StockShare = shareString(big.NewInt(e.StockPower), totalStock)
		out = append(out, e)
	}

	return &types.QueryVotingPowerPreviewResponse{
		Height: height,
		Epoch:  epoch,
		Params: types.VotingPowerPreviewParams{
			WarpThreshold: wp.Threshold,
			WarpInitial:   wp.Initial,
			ValidatorCap:  valCap,
		},
		Validators:      out,
		TotalVp:         math.NewIntFromBigInt(totalVP),
		TotalStockPower: totalStock.Int64(),
	}, nil
}

// previewRow evaluates one bonded validator. The shares are filled in once the
// totals are known.
func (k Keeper) previewRow(
	ctx context.Context,
	v stakingtypes.ValidatorI,
	valCap math.Int,
	initial, threshold math.LegacyDec,
	powerReduction math.Int,
) (previewRow, error) {
	valAddr, err := sdk.ValAddressFromBech32(v.GetOperator())
	if err != nil {
		return previewRow{}, err
	}

	bonded := v.GetBondedTokens()
	capped := math.MinInt(bonded, valCap)

	warp, measured := initial, false
	w, found, err := k.participationKeeper.GetWarp(ctx, valAddr)
	if err != nil {
		return previewRow{}, err
	}
	if found {
		if w.Value.IsNil() {
			return previewRow{}, fmt.Errorf("stored WARP of %s is not set", valAddr)
		}
		// The stored value is shown either way; only a row with a measured
		// epoch counts as measured (R7's zero row has last_epoch 0).
		warp, measured = w.Value, w.LastEpoch > 0
	}

	// The reward gate itself, so the preview and the payout never disagree on
	// who is eligible; WARP is the one condition the preview adds.
	g, err := k.rewardGate(ctx, valAddr)
	if err != nil {
		return previewRow{}, err
	}
	reason := g.reason()
	if reason == "" && warp.LT(threshold) {
		reason = reasonWarpBelowThreshold
	}

	return previewRow{
		entry: types.VotingPowerPreviewEntry{
			OperatorAddress: valAddr.String(),
			Bonded:          bonded,
			CappedBonded:    capped,
			Warp:            warp,
			WarpMeasured:    measured,
			Compliant:       g.compliant,
			Status:          g.status.String(),
			Vp:              previewVP(capped, warp, g.compliant),
			StockPower:      v.GetConsensusPower(powerReduction),
			Eligible:        reason == "",
			Reason:          reason,
		},
	}, nil
}

// VotingPowerPreview answers Query/VotingPowerPreview.
func (q queryServer) VotingPowerPreview(ctx context.Context, req *types.QueryVotingPowerPreviewRequest) (*types.QueryVotingPowerPreviewResponse, error) {
	if req == nil {
		return nil, status.Error(codes.InvalidArgument, "invalid request")
	}
	res, err := q.k.VotingPowerPreview(ctx)
	switch {
	case err == nil:
		return res, nil
	case errors.Is(err, stakeenginetypes.ErrCapsNotComputed):
		return nil, status.Error(codes.NotFound, err.Error())
	case errors.Is(err, errWarpParamsInvalid):
		return nil, status.Error(codes.FailedPrecondition, err.Error())
	default:
		return nil, status.Error(codes.Internal, err.Error())
	}
}
