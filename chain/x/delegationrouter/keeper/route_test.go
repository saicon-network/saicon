package keeper_test

import (
	"testing"

	"cosmossdk.io/math"
	sdk "github.com/cosmos/cosmos-sdk/types"
	authtypes "github.com/cosmos/cosmos-sdk/x/auth/types"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/saicon-network/saicon/chain/x/delegationrouter/keeper"
	"github.com/saicon-network/saicon/chain/x/delegationrouter/types"
	setypes "github.com/saicon-network/saicon/chain/x/stakeengine/types"
	vrtypes "github.com/saicon-network/saicon/chain/x/validatorregistry/types"
)

// ops lists the validators of legs in order.
func ops(legs []types.Leg) []string {
	var out []string
	for _, l := range legs {
		out = append(out, l.Validator)
	}
	return out
}

func sum(legs []types.Leg) math.Int {
	t := math.ZeroInt()
	for _, l := range legs {
		t = t.Add(l.Amount)
	}
	return t
}

func TestPrimaryTakesItAll(t *testing.T) {
	f := newFixture(t)
	p := f.addVal(1, 100, "0.9")
	f.addVal(2, 100, "0.9")

	r, err := f.k.Plan(f.ctx, acc(9), p, nscn(250_000))
	require.NoError(t, err)
	require.Equal(t, []types.Leg{{Validator: p.String(), Amount: nscn(250_000)}}, r.Direct)
	require.Empty(t, r.Queued)
	require.Equal(t, nscn(250_000), r.Fits)
}

func TestPrimaryFillsThenOverflowSplitsByWeight(t *testing.T) {
	f := newFixture(t)
	p := f.addVal(1, 0, "0.9")
	f.room(p, 100_000)
	a := f.addVal(2, 400_000, "0.9") // weight 0.9*600k/400k = 1.35
	b := f.addVal(3, 800_000, "0.8") // weight 0.8*200k/800k = 0.20

	r, err := f.k.Plan(f.ctx, acc(9), p, nscn(250_000))
	require.NoError(t, err)
	require.Equal(t, []string{p.String(), a.String(), b.String()}, ops(r.Direct), "primary first, then by weight")
	require.Equal(t, nscn(100_000), r.Direct[0].Amount)
	require.True(t, r.Direct[1].Amount.GT(r.Direct[2].Amount))
	require.Equal(t, nscn(250_000), sum(r.Direct))
	require.Empty(t, r.Queued)
}

func TestPrimaryMustBeActive(t *testing.T) {
	f := newFixture(t)
	p := f.addVal(1, 100, "0.9")
	f.reg.status[string(p)] = vrtypes.Status_STATUS_QUARANTINED
	_, err := f.k.Plan(f.ctx, acc(9), p, nscn(1000))
	require.ErrorIs(t, err, types.ErrPrimaryNotEligible)

	_, err = f.k.Plan(f.ctx, acc(9), val(77), nscn(1000))
	require.ErrorIs(t, err, types.ErrPrimaryNotEligible, "unknown primary")

	j := f.addVal(2, 100, "0.9")
	v := f.stk.validators[string(j)]
	v.Jailed = true
	f.stk.validators[string(j)] = v
	_, err = f.k.Plan(f.ctx, acc(9), j, nscn(1000))
	require.ErrorIs(t, err, types.ErrPrimaryNotEligible, "jailed in staking")
}

// Each exclusion alone makes an otherwise good candidate disappear.
func TestCandidateExclusions(t *testing.T) {
	cases := map[string]func(f *fixture, c sdk.ValAddress){
		"quarantined": func(f *fixture, c sdk.ValAddress) { f.reg.status[string(c)] = vrtypes.Status_STATUS_QUARANTINED },
		"jailed status": func(f *fixture, c sdk.ValAddress) {
			f.reg.status[string(c)] = vrtypes.Status_STATUS_JAILED
		},
		"jailed flag": func(f *fixture, c sdk.ValAddress) {
			v := f.stk.validators[string(c)]
			v.Jailed = true
			f.stk.validators[string(c)] = v
		},
		"score below minimum": func(f *fixture, c sdk.ValAddress) { f.part.scores[string(c)] = math.LegacyMustNewDecFromStr("0.74") },
		"no snapshot":         func(f *fixture, c sdk.ValAddress) { delete(f.part.scores, string(c)) },
		"no room":             func(f *fixture, c sdk.ValAddress) { f.room(c, 0) },
	}
	for name, mutate := range cases {
		t.Run(name, func(t *testing.T) {
			f := newFixture(t)
			p := f.addVal(1, 0, "0.9")
			f.room(p, 100_000)
			c := f.addVal(2, 100, "0.9")

			// Control: the candidate is used before the mutation.
			r, err := f.k.Plan(f.ctx, acc(9), p, nscn(150_000))
			require.NoError(t, err)
			require.Equal(t, []string{p.String(), c.String()}, ops(r.Direct))

			mutate(f, c)
			r, err = f.k.Plan(f.ctx, acc(9), p, nscn(150_000))
			require.ErrorIs(t, err, types.ErrNoRoute)
			require.Equal(t, nscn(100_000), r.Fits)
		})
	}
	t.Run("score exactly at the minimum is kept", func(t *testing.T) {
		f := newFixture(t)
		p := f.addVal(1, 0, "0.9")
		f.room(p, 100_000)
		c := f.addVal(2, 100, "0.75")
		r, err := f.k.Plan(f.ctx, acc(9), p, nscn(150_000))
		require.NoError(t, err)
		require.Equal(t, []string{p.String(), c.String()}, ops(r.Direct))
	})
	t.Run("the primary is never its own overflow", func(t *testing.T) {
		f := newFixture(t)
		p := f.addVal(1, 0, "0.9")
		f.room(p, 100_000)
		r, err := f.k.Plan(f.ctx, acc(9), p, nscn(150_000))
		require.ErrorIs(t, err, types.ErrNoRoute)
		require.Equal(t, nscn(100_000), r.Fits)
	})
}

func TestSlots(t *testing.T) {
	d := acc(9)
	t.Run("three held including the primary: only held candidates", func(t *testing.T) {
		f := newFixture(t)
		p := f.addVal(1, 0, "0.9")
		f.room(p, 100)
		h1 := f.addVal(2, 900_000, "0.8") // light but held
		h2 := f.addVal(3, 900_000, "0.8")
		x := f.addVal(4, 10, "0.99") // heavy but not held
		f.engine.slots[string(d)] = []string{p.String(), h1.String(), h2.String()}

		r, err := f.k.Plan(f.ctx, d, p, nscn(150))
		require.NoError(t, err)
		require.NotContains(t, ops(r.Direct), x.String())
		require.Contains(t, ops(r.Direct), h1.String())
	})
	t.Run("three held without the primary: too many validators", func(t *testing.T) {
		f := newFixture(t)
		p := f.addVal(1, 0, "0.9")
		h1, h2, h3 := f.addVal(2, 0, "0.9"), f.addVal(3, 0, "0.9"), f.addVal(4, 0, "0.9")
		f.engine.slots[string(d)] = []string{h1.String(), h2.String(), h3.String()}
		_, err := f.k.Plan(f.ctx, d, p, nscn(1000))
		require.ErrorIs(t, err, setypes.ErrTooManyValidators)
	})
	t.Run("one held and a new primary: at most one new candidate", func(t *testing.T) {
		f := newFixture(t)
		params := types.DefaultParams()
		params.MaxRouteLegs = 4
		require.NoError(t, f.k.Params.Set(f.ctx, params))
		p := f.addVal(1, 0, "0.9")
		f.room(p, 100)
		h1 := f.addVal(2, 900_000, "0.8")
		x := f.addVal(3, 10, "0.99")
		y := f.addVal(4, 20, "0.99")
		f.engine.slots[string(d)] = []string{h1.String()}

		r, err := f.k.Plan(f.ctx, d, p, nscn(150_000))
		require.NoError(t, err)
		used := ops(r.Direct)
		newOnes := 0
		for _, v := range []string{x.String(), y.String()} {
			for _, u := range used {
				if u == v {
					newOnes++
				}
			}
		}
		require.Equal(t, 1, newOnes, "legs %v", used)
		// h1's share is dust next to the fresh candidate's, so it is folded away.
		require.NotContains(t, used, h1.String())
	})
	t.Run("the operator's own validator needs no slot", func(t *testing.T) {
		f := newFixture(t)
		p := f.addVal(1, 100, "0.9")
		f.engine.slots[string(sdk.AccAddress(p))] = nil
		_, err := f.k.Plan(f.ctx, sdk.AccAddress(p), p, nscn(1000))
		require.NoError(t, err)
	})
}

func TestNotEnoughRoomExecutesNothing(t *testing.T) {
	f := newFixture(t)
	p := f.addVal(1, 0, "0.9")
	f.room(p, 100_000)
	f.addVal(2, 0, "0.9")
	f.room(val(2), 50_000)

	ms := keeper.NewMsgServerImpl(f.k)
	_, err := ms.DelegateRouted(f.ctx, &types.MsgDelegateRouted{
		Delegator: acc(9).String(), Validator: p.String(), Amount: sdk.NewCoin(types.BondDenom, nscn(300_000)),
	})
	require.ErrorIs(t, err, types.ErrNoRoute)
	require.Empty(t, f.stk.calls)
	require.Empty(t, f.engine.queued)

	r, err := f.k.Plan(f.ctx, acc(9), p, nscn(300_000))
	require.ErrorIs(t, err, types.ErrNoRoute)
	require.Equal(t, nscn(150_000), r.Fits)
}

func TestHeadroom(t *testing.T) {
	setup := func(headroom int64) (*fixture, sdk.ValAddress, sdk.ValAddress) {
		f := newFixture(t)
		p := f.addVal(1, 0, "0.9")
		f.room(p, 100_000)
		c := f.addVal(2, 100, "0.9")
		f.engine.headroom = nscn(headroom)
		return f, p, c
	}
	t.Run("zero headroom queues every leg", func(t *testing.T) {
		f, p, c := setup(0)
		r, err := f.k.Plan(f.ctx, acc(9), p, nscn(150_000))
		require.NoError(t, err)
		require.Empty(t, r.Direct)
		require.Equal(t, []types.Leg{{Validator: p.String(), Amount: nscn(100_000)}, {Validator: c.String(), Amount: nscn(50_000)}}, r.Queued)
	})
	t.Run("headroom inside the primary leg", func(t *testing.T) {
		f, p, c := setup(60_000)
		r, err := f.k.Plan(f.ctx, acc(9), p, nscn(150_000))
		require.NoError(t, err)
		require.Equal(t, []types.Leg{{Validator: p.String(), Amount: nscn(60_000)}}, r.Direct)
		require.Equal(t, []types.Leg{{Validator: p.String(), Amount: nscn(40_000)}, {Validator: c.String(), Amount: nscn(50_000)}}, r.Queued)
	})
	t.Run("headroom past the primary leg runs on into the next leg in order", func(t *testing.T) {
		f, p, c := setup(130_000)
		r, err := f.k.Plan(f.ctx, acc(9), p, nscn(150_000))
		require.NoError(t, err)
		require.Equal(t, []types.Leg{{Validator: p.String(), Amount: nscn(100_000)}, {Validator: c.String(), Amount: nscn(30_000)}}, r.Direct)
		require.Equal(t, []types.Leg{{Validator: c.String(), Amount: nscn(20_000)}}, r.Queued)
	})
}

func TestMinimumDelegation(t *testing.T) {
	f := newFixture(t)
	p := f.addVal(1, 0, "0.9")
	f.room(p, 100_000)
	c := f.addVal(2, 100, "0.9")

	_, err := f.k.Plan(f.ctx, acc(9), p, nscn(100).SubRaw(1))
	require.ErrorIs(t, err, setypes.ErrBelowMinDelegation)

	// A leg under the minimum inside a valid route is fine.
	r, err := f.k.Plan(f.ctx, acc(9), p, nscn(100_050))
	require.NoError(t, err)
	require.Equal(t, []string{p.String(), c.String()}, ops(r.Direct))
	require.Equal(t, nscn(50), r.Direct[1].Amount)
}

func TestPreviewIsTheMessage(t *testing.T) {
	f := newFixture(t)
	p := f.addVal(1, 0, "0.9")
	f.room(p, 100_000)
	c := f.addVal(2, 100, "0.9")
	f.engine.headroom = nscn(130_000)

	qs := keeper.NewQueryServerImpl(f.k)
	prev, err := qs.Route(f.ctx, &types.QueryRouteRequest{Delegator: acc(9).String(), Validator: p.String(), Amount: nscn(150_000).String()})
	require.NoError(t, err)
	require.Empty(t, prev.Reason)
	require.Equal(t, nscn(150_000), prev.Fits)

	ms := keeper.NewMsgServerImpl(f.k)
	res, err := ms.DelegateRouted(f.ctx, &types.MsgDelegateRouted{
		Delegator: acc(9).String(), Validator: p.String(), Amount: sdk.NewCoin(types.BondDenom, nscn(150_000)),
	})
	require.NoError(t, err)

	// What the preview says is what ran.
	require.Len(t, f.stk.calls, len(prev.Legs))
	for i, l := range prev.Legs {
		require.Equal(t, l.Validator, f.stk.calls[i].validator.String())
		require.Equal(t, l.Amount, f.stk.calls[i].amount)
		require.Equal(t, l, res.Legs[i])
		require.Equal(t, acc(9), f.stk.calls[i].delegator)
	}
	require.Len(t, f.engine.queued, len(prev.Queued))
	require.Len(t, res.Queued, len(prev.Queued))
	for i, q := range prev.Queued {
		require.Equal(t, q.Validator, f.engine.queued[i].validator.String())
		require.Equal(t, q.Amount, f.engine.queued[i].amount)
		require.Equal(t, q.Amount, res.Queued[i].Amount)
		require.Equal(t, f.engine.queued[i].id, res.Queued[i].EntryId)
	}
	require.Equal(t, c.String(), prev.Legs[1].Validator)
	require.Equal(t, nscn(20_000), prev.Queued[0].Amount)
}

func TestRouteQueryAnswersNoWithoutAnError(t *testing.T) {
	f := newFixture(t)
	p := f.addVal(1, 0, "0.9")
	f.room(p, 100_000)
	qs := keeper.NewQueryServerImpl(f.k)

	res, err := qs.Route(f.ctx, &types.QueryRouteRequest{Delegator: acc(9).String(), Validator: p.String(), Amount: nscn(300_000).String()})
	require.NoError(t, err)
	require.NotEmpty(t, res.Reason)
	require.Equal(t, nscn(100_000), res.Fits)
	require.Empty(t, res.Legs)

	res, err = qs.Route(f.ctx, &types.QueryRouteRequest{Delegator: acc(9).String(), Validator: val(77).String(), Amount: nscn(300).String()})
	require.NoError(t, err)
	require.Contains(t, res.Reason, "cannot take routed stake")

	for name, req := range map[string]*types.QueryRouteRequest{
		"bad delegator": {Delegator: "x", Validator: p.String(), Amount: "5"},
		"bad validator": {Delegator: acc(9).String(), Validator: "x", Amount: "5"},
		"bad amount":    {Delegator: acc(9).String(), Validator: p.String(), Amount: "abc"},
		"zero amount":   {Delegator: acc(9).String(), Validator: p.String(), Amount: "0"},
	} {
		_, err := qs.Route(f.ctx, req)
		require.Equal(t, codes.InvalidArgument, status.Code(err), name)
	}
	_, err = qs.Route(f.ctx, nil)
	require.Equal(t, codes.InvalidArgument, status.Code(err))
}

func TestRoutedEvent(t *testing.T) {
	f := newFixture(t)
	p := f.addVal(1, 0, "0.9")
	f.room(p, 100_000)
	c := f.addVal(2, 100, "0.9")
	f.engine.headroom = nscn(130_000)

	ms := keeper.NewMsgServerImpl(f.k)
	_, err := ms.DelegateRouted(f.ctx, &types.MsgDelegateRouted{
		Delegator: acc(9).String(), Validator: p.String(), Amount: sdk.NewCoin(types.BondDenom, nscn(150_000)),
	})
	require.NoError(t, err)

	var ev *sdk.Event
	for _, e := range f.ctx.EventManager().Events() {
		if e.Type == types.EventTypeDelegationRouted {
			e := e
			ev = &e
		}
	}
	require.NotNil(t, ev)
	attrs := map[string]string{}
	for _, a := range ev.Attributes {
		attrs[a.Key] = a.Value
	}
	require.Equal(t, acc(9).String(), attrs[types.AttributeKeyDelegator])
	require.Equal(t, p.String(), attrs[types.AttributeKeyPrimary])
	require.Equal(t, nscn(150_000).String(), attrs[types.AttributeKeyAmount])
	require.Equal(t, p.String()+":"+nscn(100_000).String()+","+c.String()+":"+nscn(30_000).String(), attrs[types.AttributeKeyLegs])
	require.Equal(t, c.String()+":"+nscn(20_000).String()+":1", attrs[types.AttributeKeyQueued])
}

func TestDenomAndAmountAreChecked(t *testing.T) {
	f := newFixture(t)
	p := f.addVal(1, 100, "0.9")
	ms := keeper.NewMsgServerImpl(f.k)
	_, err := ms.DelegateRouted(f.ctx, &types.MsgDelegateRouted{
		Delegator: acc(9).String(), Validator: p.String(), Amount: sdk.NewCoin("uatom", nscn(1000)),
	})
	require.ErrorIs(t, err, types.ErrInvalidDenom)
	_, err = ms.DelegateRouted(f.ctx, &types.MsgDelegateRouted{
		Delegator: "bad", Validator: p.String(), Amount: sdk.NewCoin(types.BondDenom, nscn(1000)),
	})
	require.Error(t, err)
	_, err = ms.DelegateRouted(f.ctx, &types.MsgDelegateRouted{
		Delegator: acc(9).String(), Validator: p.String(), Amount: sdk.Coin{Denom: types.BondDenom, Amount: math.ZeroInt()},
	})
	require.Error(t, err)
	require.Empty(t, f.stk.calls)
}

func TestUpdateParamsAndGenesis(t *testing.T) {
	f := newFixture(t)
	ms := keeper.NewMsgServerImpl(f.k)
	gov := authtypes.NewModuleAddress(types.GovModuleName).String()

	p := types.DefaultParams()
	p.MaxRouteLegs = 3
	_, err := ms.UpdateParams(f.ctx, &types.MsgUpdateParams{Authority: acc(1).String(), Params: p})
	require.ErrorIs(t, err, types.ErrInvalidSigner)

	bad := p
	bad.MaxRouteLegs = 9
	_, err = ms.UpdateParams(f.ctx, &types.MsgUpdateParams{Authority: gov, Params: bad})
	require.Error(t, err)

	_, err = ms.UpdateParams(f.ctx, &types.MsgUpdateParams{Authority: gov, Params: p})
	require.NoError(t, err)
	got, err := f.k.Params.Get(f.ctx)
	require.NoError(t, err)
	require.Equal(t, uint32(3), got.MaxRouteLegs)

	qs := keeper.NewQueryServerImpl(f.k)
	qp, err := qs.Params(f.ctx, &types.QueryParamsRequest{})
	require.NoError(t, err)
	require.Equal(t, uint32(3), qp.Params.MaxRouteLegs)

	gs, err := f.k.ExportGenesis(f.ctx)
	require.NoError(t, err)
	require.Equal(t, uint32(3), gs.Params.MaxRouteLegs)

	g := newFixture(t)
	require.NoError(t, g.k.InitGenesis(g.ctx, *gs))
	got, err = g.k.Params.Get(g.ctx)
	require.NoError(t, err)
	require.Equal(t, uint32(3), got.MaxRouteLegs)
}

// Review fix 1: a new primary takes a slot only when it gets a leg.
func TestNewPrimaryAtItsCapTakesNoSlot(t *testing.T) {
	d := acc(9)
	t.Run("three held, new full primary, overflow fits on the held ones", func(t *testing.T) {
		f := newFixture(t)
		p := f.addVal(1, 0, "0.9")
		f.room(p, 0)
		h1, h2, h3 := f.addVal(2, 100, "0.9"), f.addVal(3, 100, "0.9"), f.addVal(4, 100, "0.9")
		f.engine.slots[string(d)] = []string{h1.String(), h2.String(), h3.String()}

		r, err := f.k.Plan(f.ctx, d, p, nscn(1000))
		require.NoError(t, err)
		require.NotContains(t, ops(r.Direct), p.String())
		require.Equal(t, nscn(1000), sum(r.Direct))
	})
	t.Run("two held (full), new full primary, a fresh candidate is reachable", func(t *testing.T) {
		f := newFixture(t)
		f.engine.max = 2
		p := f.addVal(1, 0, "0.9")
		f.room(p, 0)
		a, b := f.addVal(2, 900_000, "0.9"), f.addVal(3, 900_000, "0.9")
		f.room(a, 0)
		f.room(b, 0)
		cc := f.addVal(4, 100, "0.9")
		f.engine.slots[string(d)] = []string{a.String(), b.String()}
		// the delegator holds A and B, both at cap, and the max is 2: C is a
		// third validator, so it must be refused rather than routed.
		_, err := f.k.Plan(f.ctx, d, p, nscn(1000))
		require.ErrorIs(t, err, types.ErrNoRoute)

		// with one slot free, the full primary must not eat it
		f.engine.max = 3
		r, err := f.k.Plan(f.ctx, d, p, nscn(1000))
		require.NoError(t, err)
		require.Equal(t, []string{cc.String()}, ops(r.Direct))
	})
	t.Run("three held, new primary WITH room: still too many validators", func(t *testing.T) {
		f := newFixture(t)
		p := f.addVal(1, 0, "0.9")
		h1, h2, h3 := f.addVal(2, 0, "0.9"), f.addVal(3, 0, "0.9"), f.addVal(4, 0, "0.9")
		f.engine.slots[string(d)] = []string{h1.String(), h2.String(), h3.String()}
		_, err := f.k.Plan(f.ctx, d, p, nscn(1000))
		require.ErrorIs(t, err, setypes.ErrTooManyValidators)
	})
}

// Review fix 3: the operator's own validator is never overflow for its router call.
func TestOperatorsOwnValidatorIsNotOverflow(t *testing.T) {
	f := newFixture(t)
	p := f.addVal(1, 0, "0.9")
	f.room(p, 100_000)
	vo := f.addVal(5, 0, "0.99") // most room, best weight
	q := f.addVal(6, 500_000, "0.9")
	op := sdk.AccAddress(vo)

	r, err := f.k.Plan(f.ctx, op, p, nscn(150_000))
	require.NoError(t, err)
	require.Equal(t, []string{p.String(), q.String()}, ops(r.Direct))

	// someone else may still route to it
	r, err = f.k.Plan(f.ctx, acc(9), p, nscn(150_000))
	require.NoError(t, err)
	require.Contains(t, ops(r.Direct), vo.String())
}

// Review fix 4: a dust overflow leg is folded into the others.
func TestTinyOverflowLegIsFolded(t *testing.T) {
	f := newFixture(t)
	p := f.addVal(1, 0, "0.9")
	f.room(p, 1000)
	a := f.addVal(2, 500_000, "0.9") // weight 0.9
	f.addVal(3, 990_000, "0.9")      // weight 0.009: ~1% of 5,000 SCN = 50 SCN < min 100
	r, err := f.k.Plan(f.ctx, acc(9), p, nscn(6000))
	require.NoError(t, err)
	require.Equal(t, []types.Leg{{Validator: p.String(), Amount: nscn(1000)}, {Validator: a.String(), Amount: nscn(5000)}}, r.Direct)
}
