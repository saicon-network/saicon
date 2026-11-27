package keeper

import (
	"context"
	"errors"
	"strconv"

	"cosmossdk.io/collections"
	"cosmossdk.io/math"
	sdk "github.com/cosmos/cosmos-sdk/types"
	distrtypes "github.com/cosmos/cosmos-sdk/x/distribution/types"
	stakingtypes "github.com/cosmos/cosmos-sdk/x/staking/types"

	"github.com/saicon-network/saicon/chain/x/rewards/types"
	vrtypes "github.com/saicon-network/saicon/chain/x/validatorregistry/types"
)

// MaybeRunEpoch runs reward distribution + the V-2 invariant assertion when
// `height` is an epoch boundary. Emission errors are logged and swallowed (a
// money op must not halt the chain); a broken V-2 invariant panics (fail-loud).
// No-op when height is not a boundary or the epoch length is 0.
//
// The epoch is all or nothing. DistributeEpochRewards runs in one cache
// context that is written only when it returns nil, so an error anywhere in it
// — after the mint, or halfway through the payout loop — leaves no trace:
// nothing minted, no share paid or booked, no snapshot, the carry and the
// observed gas fees untouched for the next epoch. A partial epoch would strand
// its mint in the module account where the carry does not see it.
//
// The epoch length is read from x/participation (EpochLength delegates to it) so
// rewards fire on the SAME boundary participation uses — no duplicated param, no
// drift. EndBlocker order is participation BEFORE rewards (app_config), so EP for
// the current epoch is already accumulated when this runs. This method is the
// unit-testable core of the rewards EndBlock (WI-016); module.EndBlock is a thin
// wrapper around it.
func (k Keeper) MaybeRunEpoch(ctx context.Context, height int64) error {
	sdkCtx := sdk.UnwrapSDKContext(ctx)

	epochLen, err := k.EpochLength(ctx)
	if err != nil {
		sdkCtx.Logger().Error("rewards: failed to read epoch length in EndBlock", "height", height, "err", err)
		return nil
	}

	if epochLen == 0 || height <= 1 || height%int64(epochLen) != 0 {
		return nil
	}

	epochID := uint64(height) / epochLen
	epochCtx, write := sdkCtx.CacheContext()
	if err := k.DistributeEpochRewards(epochCtx, epochID); err != nil {
		// Emission failure: log + continue (do NOT halt the chain on a money op).
		// The cache context is dropped, so the epoch left nothing behind and V-2
		// has nothing new to audit.
		sdkCtx.Logger().Error("rewards: DistributeEpochRewards failed (swallowed to avoid halt); the epoch was discarded",
			"height", height, "epoch_id", epochID, "err", err)
		return nil
	}
	// Writing also re-emits the epoch's events on the block's event manager.
	write()

	// V-2 hard invariant: EP=0 -> reward=0. Panic if broken (fail-loud), the same
	// gate+EndBlock pattern as WI-006's V-1.
	if msg, broken := k.RewardGateInvariant(ctx, epochID); broken {
		panic(msg)
	}

	sdkCtx.Logger().Info("rewards: epoch distributed", "height", height, "epoch_id", epochID)
	return nil
}

// Reason strings recorded on the RewardSnapshot for each emission decision.
const (
	reasonEligible          = "eligible"
	reasonNotActive         = "not active"
	reasonNonCompliant      = "non-compliant"
	reasonEPZero            = "EP=0"
	reasonDisabled          = "emission disabled"
	reasonCapReached        = "max supply reached"
	reasonNoBudget          = "no security budget this epoch"
	reasonFeesCoveredBudget = "gas fees covered the epoch budget; paid via distribution"
	// reasonNotAllocatable: the validator passed every gate, but x/distribution
	// cannot take its share — the staking validator is gone or has no tokens
	// (S10 R3). The share is carried, never sent to the operator account, and
	// the epoch is recorded unpaid like every other denial.
	reasonNotAllocatable = "not allocatable"
)

// DistributeEpochRewards emits one epoch's rewards through a SINGLE mint path.
//
// For each bonded validator the eligibility gate is:
//
//	status == STATUS_ACTIVE  AND  C == 1 (compliant)  AND  EP > 0
//
// EP=0 -> no reward, by construction; this is the foundation of invariant V-2.
// Every validator gets a RewardSnapshot recording the decision — the amount when
// paid, the reason when not — so an epoch that paid nothing is auditable rather
// than looking like an outage.
//
// Emission mechanics (ADR-0011 D3/D4/D5), and the reason this is not a mint
// inside the payout loop:
//
//   - The pool is minted ONCE per epoch. Minting per validator made the total
//     emitted an emergent property of a loop, which is exactly what blocker B8
//     requires be provable ("no double mint, exact pool conservation").
//   - The 21M cap is a PRECONDITION of the mint, clamping it to the remaining
//     headroom. An invariant that panicked after a breach would halt the chain
//     instead of preventing it.
//   - A remainder from an uneven division is already sitting in the module
//     account, so it REDUCES the next epoch's fresh issuance rather than adding
//     to the pool. Same shape as the gas-fee offset the Register specifies:
//     value already in hand substitutes for issuance. It is never paid to a
//     validator owner as rounding dust.
//   - Each share is paid THROUGH x/distribution (ADR-0011 F3, S10): the coins
//     go to the distribution module account and are booked against the
//     validator, so its commission reaches the operator and the rest reaches
//     its delegators through the stock withdrawal messages. A validator that
//     cannot be booked against is not paid, and its share is carried.
//
// Conservation, which the tests assert directly (paid_out is what left this
// module for x/distribution):
//
//	minted_this_epoch + carried_in == paid_out + carried_out
//
// The identity holds only for an epoch that completes. An error returns
// mid-epoch with its writes so far still in ctx, so the caller must run this in
// a context it writes only on success, as MaybeRunEpoch does.
func (k Keeper) DistributeEpochRewards(ctx context.Context, epochID uint64) error {
	params, err := k.Params.Get(ctx)
	if err != nil {
		return err
	}

	eligible, err := k.classify(ctx, epochID, params.Enabled)
	if err != nil {
		return err
	}
	if !params.Enabled || len(eligible) == 0 {
		// The kill switch stops the machine: nothing minted, and nothing accrued
		// for whoever turns it back on. classify() already recorded the reason
		// against every validator.
		return nil
	}

	carried, err := k.GetRemainder(ctx)
	if err != nil {
		return err
	}

	target, err := k.epochTarget(ctx, params)
	if err != nil {
		return err
	}
	if !target.IsPositive() {
		// The budget funds nothing this epoch — a chain with no supply yet, or
		// governance having zeroed a rate. Record it rather than paying from the
		// carry alone, which would drain the pool with no issuance behind it.
		for _, valAddr := range eligible {
			if e := k.denied(ctx, epochID, valAddr, reasonNoBudget); e != nil {
				return e
			}
		}
		return nil
	}

	// Gas revenue and the carried remainder both OFFSET issuance: value already
	// in validators' hands, or already in the module account, substitutes for
	// minting rather than adding to it (ADR-0011 D2/D5). Each applies once.
	fees, err := k.GetObservedGasFees(ctx)
	if err != nil {
		return err
	}
	toMint := target.Sub(carried).Sub(fees)
	if toMint.IsNegative() {
		toMint = math.ZeroInt()
	}
	if headroom := k.mintHeadroom(ctx); toMint.GT(headroom) {
		toMint = headroom
	}

	// Mint BEFORE deciding what can be paid. The carry must only ever describe
	// value that actually sits in the module account: carrying a remainder that
	// was never minted would break conservation silently, promising base units
	// nobody holds.
	if toMint.IsPositive() {
		if e := k.bankKeeper.MintCoins(ctx, types.ModuleName,
			sdk.NewCoins(sdk.NewCoin(types.RewardDenom, toMint))); e != nil {
			return e
		}
	}

	// The revenue has now been spent against this epoch and must not suppress
	// the next one.
	if err := k.resetObservedGasFees(ctx); err != nil {
		return err
	}

	pool := carried.Add(toMint)
	share := pool.QuoRaw(int64(len(eligible)))

	if share.IsZero() {
		// Nothing to pay from this module. Two very different causes, and a
		// validator reading its snapshot deserves to know which: either gas
		// revenue already covered the epoch's whole budget — in which case the
		// validator WAS paid, through x/distribution — or the supply ceiling is
		// binding and nothing more can ever be created.
		reason := reasonCapReached
		if fees.IsPositive() && fees.GTE(target.Sub(carried)) {
			reason = reasonFeesCoveredBudget
		}
		for _, valAddr := range eligible {
			if e := k.denied(ctx, epochID, valAddr, reason); e != nil {
				return e
			}
		}
		return k.SetRemainder(ctx, pool)
	}

	// The payout leg (ADR-0011 F3, S10 R1): each share goes through
	// x/distribution, which applies the validator's commission and credits the
	// rest to its delegators, the operator's self-delegation included. Everyone
	// withdraws with the stock distribution messages. Who is eligible and how
	// much each share is were settled above and do not depend on the route.
	reward := sdk.NewCoins(sdk.NewCoin(types.RewardDenom, share))
	var paid int64
	for _, valAddr := range eligible {
		val, ok, e := k.allocatableValidator(ctx, valAddr)
		if e != nil {
			return e
		}
		if !ok {
			// R3: the share stays in this module and joins the remainder below.
			// There is no second route to the operator account. The validator
			// passed every gate, but it was not paid, so the epoch is recorded
			// the way every unpaid epoch is.
			if e := k.denied(ctx, epochID, valAddr, reasonNotAllocatable); e != nil {
				return e
			}
			continue
		}
		if e := k.payThroughDistribution(ctx, val, reward); e != nil {
			return e
		}
		paid++

		snap := types.RewardSnapshot{
			ValidatorAddress: valAddr.String(),
			EpochId:          epochID,
			Amount:           reward.String(),
			Eligible:         true,
			Reason:           reasonEligible,
		}
		if e := k.SetRewardSnapshot(ctx, epochID, valAddr, snap); e != nil {
			return e
		}

		sdk.UnwrapSDKContext(ctx).EventManager().EmitEvent(sdk.NewEvent(
			types.EventTypeRewardMinted,
			sdk.NewAttribute(types.AttributeKeyValidator, valAddr.String()),
			sdk.NewAttribute(types.AttributeKeyEpochID, strconv.FormatUint(epochID, 10)),
			sdk.NewAttribute(types.AttributeKeyAmount, reward.String()),
		))
	}

	// Only the shares actually paid leave the module; an unallocatable
	// validator's share is carried like any other unpaid amount (R3).
	return k.SetRemainder(ctx, pool.Sub(share.MulRaw(paid)))
}

// allocatableValidator reads the staking validator a share is booked against.
// ok is false when x/distribution cannot take the share (S10 R3): the
// validator is not found, or it has no tokens. AllocateTokensToValidator would
// accept coins for a zero-token validator, but x/distribution's next period
// increment moves such a validator's rewards to the community pool, so PoP
// emission would leave the PoP gate. Any other read error is returned and
// aborts the epoch.
func (k Keeper) allocatableValidator(ctx context.Context, valAddr sdk.ValAddress) (stakingtypes.ValidatorI, bool, error) {
	val, err := k.stakingKeeper.Validator(ctx, valAddr)
	if err != nil {
		if errors.Is(err, stakingtypes.ErrNoValidatorFound) || errors.Is(err, collections.ErrNotFound) {
			return nil, false, nil
		}
		return nil, false, err
	}
	if val == nil {
		return nil, false, nil
	}
	if tokens := val.GetTokens(); tokens.IsNil() || !tokens.IsPositive() {
		return nil, false, nil
	}
	return val, true, nil
}

// payThroughDistribution moves one share into the x/distribution module
// account and books it against the validator (S10 R1). AllocateTokensToValidator
// moves no coins, so the transfer comes first, exactly as x/distribution's own
// AllocateTokens moves the fee collector's balance before booking it.
//
// A transfer without its booking would leave x/distribution holding coins no
// outstanding reward accounts for. That cannot be written: an error here
// aborts the epoch, and MaybeRunEpoch drops the epoch's cache context. The
// whole share counts as paid the moment it leaves this module (R4);
// x/distribution's decimal truncation dust stays in its module account, as it
// does for fees.
func (k Keeper) payThroughDistribution(ctx context.Context, val stakingtypes.ValidatorI, reward sdk.Coins) error {
	if err := k.bankKeeper.SendCoinsFromModuleToModule(ctx, types.ModuleName, distrtypes.ModuleName, reward); err != nil {
		return err
	}
	return k.distributionKeeper.AllocateTokensToValidator(ctx, val, sdk.NewDecCoinsFromCoins(reward...))
}

// classify walks the bonded set and returns the validators that pass all three
// reward gates, recording a snapshot with the reason for every one that does
// not. When emission is disabled it records that reason against ALL of them and
// returns nothing eligible — a disabled epoch is still a documented epoch.
func (k Keeper) classify(ctx context.Context, epochID uint64, enabled bool) ([]sdk.ValAddress, error) {
	var (
		eligible []sdk.ValAddress
		iterErr  error
	)

	err := k.stakingKeeper.IterateBondedValidatorsByPower(ctx, func(_ int64, v stakingtypes.ValidatorI) bool {
		valAddr, e := sdk.ValAddressFromBech32(v.GetOperator())
		if e != nil {
			iterErr = e
			return true
		}

		if !enabled {
			iterErr = k.denied(ctx, epochID, valAddr, reasonDisabled)
			return iterErr != nil
		}

		g, e := k.rewardGate(ctx, valAddr)
		if e != nil {
			iterErr = e
			return true
		}
		if reason := g.reason(); reason != "" {
			iterErr = k.denied(ctx, epochID, valAddr, reason)
			return iterErr != nil
		}
		eligible = append(eligible, valAddr)
		return false
	})
	if err != nil {
		return nil, err
	}
	if iterErr != nil {
		return nil, iterErr
	}
	return eligible, nil
}

// gateResult is one validator's standing against the three reward gates. The
// reward payout and the voting-power preview both read it, so the preview's
// eligible and the payout can never disagree on who passes.
type gateResult struct {
	status     vrtypes.Status // STATUS_UNSPECIFIED when the registry has no record
	active     bool           // gate 1: status == STATUS_ACTIVE
	compliant  bool           // gate 2: C == 1
	epPositive bool           // gate 3: EP > 0
}

// reason is the first gate the validator fails, in gate order, or "" when it
// passes all three.
func (g gateResult) reason() string {
	switch {
	case !g.active:
		return reasonNotActive
	case !g.compliant:
		return reasonNonCompliant
	case !g.epPositive:
		return reasonEPZero
	}
	return ""
}

// rewardGate evaluates the reward eligibility gate for one validator:
//
//	status == STATUS_ACTIVE  AND  C == 1 (compliant)  AND  EP > 0
func (k Keeper) rewardGate(ctx context.Context, valAddr sdk.ValAddress) (gateResult, error) {
	var g gateResult

	// --- gate 1: status == STATUS_ACTIVE -------------------------------------
	status, found, err := k.validatorregistryKeeper.GetValidatorStatus(ctx, valAddr)
	if err != nil {
		return gateResult{}, err
	}
	if found {
		g.status = status
	}
	g.active = found && status == vrtypes.Status_STATUS_ACTIVE

	// --- gate 2: compliant (C == 1) ------------------------------------------
	// No compliance record => compliant by default (validators only get a
	// record once flagged).
	compState, compFound, err := k.complianceKeeper.GetCompliance(ctx, valAddr)
	if err != nil {
		return gateResult{}, err
	}
	g.compliant = !compFound || compState.Compliant

	// --- gate 3: EP > 0 -------------------------------------------------------
	epState, epFound, err := k.participationKeeper.GetEP(ctx, valAddr)
	if err != nil {
		return gateResult{}, err
	}
	g.epPositive = epFound && !epState.AccumulatedEp.IsNil() && epState.AccumulatedEp.IsPositive()

	return g, nil
}

// denied records a zero-reward decision and emits the matching event.
func (k Keeper) denied(ctx context.Context, epochID uint64, valAddr sdk.ValAddress, reason string) error {
	snap := types.RewardSnapshot{
		ValidatorAddress: valAddr.String(),
		EpochId:          epochID,
		Amount:           "",
		Eligible:         false,
		Reason:           reason,
	}
	if err := k.SetRewardSnapshot(ctx, epochID, valAddr, snap); err != nil {
		return err
	}

	sdk.UnwrapSDKContext(ctx).EventManager().EmitEvent(sdk.NewEvent(
		types.EventTypeRewardDenied,
		sdk.NewAttribute(types.AttributeKeyValidator, valAddr.String()),
		sdk.NewAttribute(types.AttributeKeyEpochID, strconv.FormatUint(epochID, 10)),
		sdk.NewAttribute(types.AttributeKeyReason, reason),
	))
	return nil
}

// epochTarget is one epoch's share of the Register's annual security budget
// (ADR-0011 D1). The budget is sized from what the network actually secures —
// total bonded stake — clipped between a floor and a ceiling expressed as
// fractions of current supply, then divided by the number of epochs in a year.
//
// The epoch length comes from x/participation rather than a second copy here:
// epochs are counted in blocks, and one fact stored twice is one fact that
// drifts.
func (k Keeper) epochTarget(ctx context.Context, params types.Params) (math.Int, error) {
	bonded, err := k.stakingKeeper.TotalBondedTokens(ctx)
	if err != nil {
		return math.ZeroInt(), err
	}
	epochLen, err := k.participationKeeper.EpochLengthBlocks(ctx)
	if err != nil {
		return math.ZeroInt(), err
	}
	supply := k.bankKeeper.GetSupply(ctx, types.RewardDenom).Amount

	annual := types.AnnualSecurityBudget(params, bonded, supply)
	return types.EpochTarget(params, annual, epochLen), nil
}

// mintHeadroom is how much may still be created before the 21M ceiling. Never
// negative: a supply already at or above the cap yields zero, which stops the
// mint without erroring, because the chain must keep producing blocks.
func (k Keeper) mintHeadroom(ctx context.Context) math.Int {
	supply := k.bankKeeper.GetSupply(ctx, types.RewardDenom).Amount
	headroom := types.MaxSupplyNscn().Sub(supply)
	if headroom.IsNegative() {
		return math.ZeroInt()
	}
	return headroom
}

// GetRemainder returns the base units carried forward from previous epochs.
// Absent (a chain that has never distributed) reads as zero.
func (k Keeper) GetRemainder(ctx context.Context) (math.Int, error) {
	v, err := k.Remainder.Get(ctx)
	if err != nil {
		if errors.Is(err, collections.ErrNotFound) {
			return math.ZeroInt(), nil
		}
		return math.ZeroInt(), err
	}
	return v, nil
}

// SetRemainder stores the carry for the next epoch.
func (k Keeper) SetRemainder(ctx context.Context, v math.Int) error {
	return k.Remainder.Set(ctx, v)
}
