package keeper

import (
	"context"
	"strconv"

	"cosmossdk.io/math"
	sdk "github.com/cosmos/cosmos-sdk/types"
	stakingtypes "github.com/cosmos/cosmos-sdk/x/staking/types"

	"github.com/saicon-network/saicon/chain/x/participation/types"
	vrtypes "github.com/saicon-network/saicon/chain/x/validatorregistry/types"
)

// uptimeWeight is the weight of the uptime signal in the Participation Score
// weighted sum. v1 has a single signal (uptime) at weight 1.0; the structure
// admits additional weighted signals later without reshaping storage.
var uptimeWeight = math.LegacyOneDec()

// decayEP applies multiplicative EP decay over `idle` epochs:
//
//	EP_new = floor( EP * (1 - epDecay)^idle ), but never below 1.
//
// It is a no-op (returns ep unchanged) when decay is disabled (epDecay == 0),
// there are no idle epochs (idle <= 0), or EP is already at/below the floor of 1
// — including the grace state EP == 0, which must stay 0 until the validator
// first earns. Decay only ever applies to validators that have positive EP.
func decayEP(ep math.Int, idle int64, epDecay math.LegacyDec) math.Int {
	one := math.OneInt()
	if ep.LTE(one) || idle <= 0 || epDecay.IsNil() || epDecay.IsZero() {
		return ep
	}
	factor := math.LegacyOneDec().Sub(epDecay).Power(uint64(idle))
	res := math.LegacyNewDecFromInt(ep).Mul(factor).TruncateInt()
	if res.LT(one) {
		return one
	}
	return res
}

// ComputeEpoch computes a per-validator Participation Score (PS) snapshot and
// accumulates Empirical Points (EP) for every bonded (active) validator at the
// close of the given epoch. It is invoked from the module EndBlocker on a
// block-based epoch boundary (Decision D1 — no time-based x/epochs).
//
// PS formula (v1): computed_score = uptimeWeight * uptime_fraction, where
//
//	uptime = signed / (signed + missed)   (epoch-window counters)
//
// using the per-validator EpochBlocksSigned / EpochBlocksMissed counters
// accumulated by RecordEpochVotes during BeginBlock. If no blocks were observed
// for a validator this epoch (total == 0), uptime defaults to 1 (benefit of
// the doubt — the validator was not yet in the active set; no penalty).
// votes_participated is set equal to blocks_signed (epoch-window source).
//
// The weighted-sum structure (uptimeWeight * uptime + w2*signal2 + …) is
// intentionally open for additional signals (attestation latency, governance
// participation, etc.) in later milestones without reshaping storage or the
// snapshot proto.
//
// EP accumulation (§5.1.1, parameter-driven via ep_decay; see decayEP):
//   - first time we see the validator: initialize EP=0, LastEpochUpdate=epochID
//     (grace — no increment, no decay on the first observed epoch);
//   - score > 0: decay the idle gap since the last update, then EP += 1;
//   - score == 0: decay across the gap including this epoch.
//
// Decay is multiplicative toward a floor of 1 and is applied lazily on the
// epoch a validator is processed (gaps where it left the bonded set are charged
// when it returns). With ep_decay = 0 (default) there is no decay.
//
// After all validators are processed, the per-epoch counters are reset via
// ResetEpochCounters so the next epoch starts from zero.
func (k Keeper) ComputeEpoch(ctx context.Context, epochID uint64) error {
	computedAt := sdk.UnwrapSDKContext(ctx).BlockTime()

	// Reset the per-epoch counters unconditionally so a failed or partial epoch
	// never leaks stale tallies into the next epoch (which would double-count).
	defer func() {
		if rerr := k.ResetEpochCounters(ctx); rerr != nil {
			sdk.UnwrapSDKContext(ctx).Logger().Error(
				"participation: reset epoch counters failed", "epoch_id", epochID, "err", rerr)
		}
	}()

	params, perr := k.Params.Get(ctx)
	if perr != nil {
		return perr
	}
	epDecay := params.EpDecay

	var iterErr error
	err := k.stakingKeeper.IterateBondedValidatorsByPower(ctx, func(_ int64, v stakingtypes.ValidatorI) bool {
		valAddr, e := sdk.ValAddressFromBech32(v.GetOperator())
		if e != nil {
			iterErr = e
			return true
		}

		// V-7: PS/EP are computed only for SAiCoN-active validators. A bonded
		// validator whose SAiCoN status is not ACTIVE (e.g. quarantined — quarantine
		// does not unbond in staking) is skipped: no snapshot, no EP change. This is
		// consistent with lazy EP decay — the idle gap is charged when (if) the
		// validator returns to ACTIVE.
		status, found, e := k.validatorregistryKeeper.GetValidatorStatus(ctx, valAddr)
		if e != nil {
			iterErr = e
			return true
		}
		if !found || status != vrtypes.Status_STATUS_ACTIVE {
			return false
		}

		uptime, signed, missed, e := k.epochUptime(ctx, valAddr)
		if e != nil {
			iterErr = e
			return true
		}

		score := uptimeWeight.Mul(uptime)

		snap := types.ParticipationScoreSnapshot{
			ValidatorAddress:  valAddr.String(),
			EpochId:           epochID,
			BlocksSigned:      signed,
			BlocksMissed:      missed,
			VotesParticipated: signed,
			UptimeFraction:    uptime,
			ComputedScore:     score,
			ComputedAt:        computedAt,
		}
		if e := k.SetSnapshot(ctx, epochID, valAddr, snap); e != nil {
			iterErr = e
			return true
		}

		// EP accumulation (§5.1.1) — param-driven decay via decayEP.
		epState, found, e := k.GetEP(ctx, valAddr)
		if e != nil {
			iterErr = e
			return true
		}
		if !found {
			epState = types.EmpiricalPointsState{ValidatorAddress: valAddr.String(), AccumulatedEp: math.ZeroInt(), LastEpochUpdate: epochID}
		} else {
			cur := epState.AccumulatedEp
			if cur.IsNil() {
				cur = math.ZeroInt()
			}
			idle := int64(epochID) - int64(epState.LastEpochUpdate)
			if idle < 0 {
				idle = 0 // replay guard: epochID < LastEpochUpdate; decayEP no-ops on idle<=0
			}
			if score.IsPositive() {
				// Decay the idle gap (epochs strictly before this one), then earn.
				cur = decayEP(cur, idle-1, epDecay)
				cur = cur.Add(math.OneInt())
			} else {
				// This epoch is idle too — decay across the whole gap incl. now.
				cur = decayEP(cur, idle, epDecay)
			}
			epState.AccumulatedEp = cur
			epState.LastEpochUpdate = epochID
		}
		if e := k.SetEP(ctx, valAddr, epState); e != nil {
			iterErr = e
			return true
		}

		sdk.UnwrapSDKContext(ctx).EventManager().EmitEvent(sdk.NewEvent(
			types.EventTypeScoreComputed,
			sdk.NewAttribute(types.AttributeKeyValidator, valAddr.String()),
			sdk.NewAttribute(types.AttributeKeyEpochID, strconv.FormatUint(epochID, 10)),
			sdk.NewAttribute(types.AttributeKeyBlocksSigned, strconv.FormatUint(signed, 10)),
			sdk.NewAttribute(types.AttributeKeyBlocksMissed, strconv.FormatUint(missed, 10)),
			sdk.NewAttribute(types.AttributeKeyUptime, uptime.String()),
			sdk.NewAttribute(types.AttributeKeyScore, score.String()),
			sdk.NewAttribute(types.AttributeKeyEPAfter, epState.AccumulatedEp.String()),
		))
		return false
	})
	if err != nil {
		return err
	}
	if iterErr != nil {
		return iterErr
	}

	return nil
}
