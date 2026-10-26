package keeper

import (
	"bytes"
	"context"
	"errors"

	errorsmod "cosmossdk.io/errors"
	"cosmossdk.io/math"
	sdk "github.com/cosmos/cosmos-sdk/types"
	stakingtypes "github.com/cosmos/cosmos-sdk/x/staking/types"

	"github.com/saicon-network/saicon/chain/x/delegationrouter/types"
	setypes "github.com/saicon-network/saicon/chain/x/stakeengine/types"
	vrtypes "github.com/saicon-network/saicon/chain/x/validatorregistry/types"
)

// Route is what MsgDelegateRouted would execute now.
type Route struct {
	Direct []types.Leg // delegated now, primary first
	Queued []types.Leg // joins the queue, per leg's validator
	Fits   math.Int    // what the route could place (== amount on success)
}

// Plan computes the route MsgDelegateRouted would execute now (spec §4.2, §4.4).
// The message and the Route query both call it, so a preview never disagrees
// with execution in the same block.
func (k Keeper) Plan(ctx context.Context, delegator sdk.AccAddress, primary sdk.ValAddress, amount math.Int) (Route, error) {
	route := Route{Fits: math.ZeroInt()}
	min, err := k.stakeEngine.MinDelegation(ctx)
	if err != nil {
		return route, err
	}
	if err := setypes.CheckMinDelegation(amount, min); err != nil {
		return route, err
	}
	if ok, err := k.eligible(ctx, primary); err != nil {
		return route, err
	} else if !ok {
		return route, errorsmod.Wrapf(types.ErrPrimaryNotEligible, "%s is not an active, unjailed validator", primary)
	}
	vcap, err := k.stakeEngine.ValidatorCap(ctx)
	if err != nil {
		return route, err
	}
	room := func(v sdk.ValAddress) (math.Int, math.Int, error) {
		tok, err := k.stakeEngine.ValidatorTokens(ctx, v)
		if err != nil {
			return math.Int{}, math.Int{}, err
		}
		return tok, math.MaxInt(math.ZeroInt(), vcap.Sub(tok)), nil
	}

	slots, err := k.stakeEngine.DelegatorSlots(ctx, delegator)
	if err != nil {
		return route, err
	}
	max, err := k.stakeEngine.MaxValidatorsPerDelegator(ctx)
	if err != nil {
		return route, err
	}
	held := map[string]bool{}
	for _, s := range slots {
		held[s] = true
	}
	_, pRoom, err := room(primary)
	if err != nil {
		return route, err
	}
	first := math.MinInt(amount, pRoom)
	legs := []types.Leg{}
	if first.IsPositive() {
		// The primary takes a slot only when it gets a leg: a new primary at
		// its cap must not fail the route or eat a slot the overflow needs.
		if !held[primary.String()] && !bytes.Equal(delegator, primary) {
			if err := setypes.CheckNewSlot(slots, primary.String(), max); err != nil {
				return route, err
			}
			held[primary.String()] = true
		}
		legs = append(legs, types.Leg{Validator: primary.String(), Amount: first})
	}
	var free uint32
	if n := uint32(len(held)); n < max {
		free = max - n
	}
	rest := amount.Sub(first)
	if rest.IsPositive() {
		params, err := k.Params.Get(ctx)
		if err != nil {
			return route, err
		}
		cands, err := k.candidates(ctx, delegator, primary, params, room)
		if err != nil {
			return route, err
		}
		chosen := types.Choose(cands, held, free, params.MaxRouteLegs)
		more, left := types.SplitAtLeast(rest, chosen, min)
		legs = append(legs, more...)
		if left.IsPositive() {
			route.Fits = amount.Sub(left)
			return route, errorsmod.Wrapf(types.ErrNoRoute,
				"%s nscn fits under the validator caps of the eligible validators; %s nscn was asked", route.Fits, amount)
		}
	}
	route.Fits = amount

	headroom, err := k.stakeEngine.Headroom(ctx)
	if err != nil {
		return route, err
	}
	for _, l := range legs {
		direct := math.MinInt(l.Amount, math.MaxInt(headroom, math.ZeroInt()))
		if direct.IsPositive() {
			route.Direct = append(route.Direct, types.Leg{Validator: l.Validator, Amount: direct})
			headroom = headroom.Sub(direct)
		}
		if q := l.Amount.Sub(direct); q.IsPositive() {
			route.Queued = append(route.Queued, types.Leg{Validator: l.Validator, Amount: q})
		}
	}
	return route, nil
}

// eligible: registry status ACTIVE, and a staking validator that is not jailed.
func (k Keeper) eligible(ctx context.Context, v sdk.ValAddress) (bool, error) {
	val, err := k.stakingKeeper.GetValidator(ctx, v)
	if errors.Is(err, stakingtypes.ErrNoValidatorFound) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	return k.eligibleValidator(ctx, v, val)
}

func (k Keeper) eligibleValidator(ctx context.Context, addr sdk.ValAddress, v stakingtypes.Validator) (bool, error) {
	if v.Jailed {
		return false, nil
	}
	status, found, err := k.registryKeeper.GetValidatorStatus(ctx, addr)
	if err != nil {
		return false, err
	}
	return found && status == vrtypes.Status_STATUS_ACTIVE, nil
}

// candidates lists the validators the router may send overflow to, in the
// by-power order of x/staking (bounded by max_validators, deterministic): not
// the primary or the delegator's own validator, eligible, with room, scored at least min_route_score in the last
// closed epoch, and allowed by the entity cap.
func (k Keeper) candidates(ctx context.Context, delegator sdk.AccAddress, primary sdk.ValAddress, params types.Params,
	room func(sdk.ValAddress) (math.Int, math.Int, error)) ([]types.Candidate, error) {
	epoch, err := k.stakeEngine.LatestEpoch(ctx)
	if err != nil {
		return nil, err
	}
	bonded, err := k.stakingKeeper.GetBondedValidatorsByPower(ctx)
	if err != nil {
		return nil, err
	}
	var out []types.Candidate
	for _, v := range bonded {
		addr, err := sdk.ValAddressFromBech32(v.OperatorAddress)
		if err != nil {
			return nil, err
		}
		if bytes.Equal(addr, primary) || bytes.Equal(addr, delegator) {
			continue // never the primary, never the caller's own validator (its self-bond is locked)
		}
		ok, err := k.eligibleValidator(ctx, addr, v)
		if err != nil {
			return nil, err
		}
		if !ok {
			continue
		}
		tokens, r, err := room(addr)
		if err != nil {
			return nil, err
		}
		if !r.IsPositive() {
			continue
		}
		snap, found, err := k.participationKeeper.GetSnapshot(ctx, epoch, addr)
		if err != nil {
			return nil, err
		}
		if !found || snap.ComputedScore.IsNil() || snap.ComputedScore.LT(params.MinRouteScore) {
			continue
		}
		if !k.entityAllows(ctx, addr) {
			continue
		}
		out = append(out, types.Candidate{
			Operator: v.OperatorAddress, OperatorBytes: addr,
			Score: snap.ComputedScore, Tokens: tokens, Room: r,
		})
	}
	return out, nil
}

// entityAllows is the entity-cap filter of spec §4.2. Always true until S8's
// entity registry gives it a body; it is a function so S8 changes nothing else.
func (k Keeper) entityAllows(context.Context, sdk.ValAddress) bool { return true }
