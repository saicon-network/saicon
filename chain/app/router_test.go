package app

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"cosmossdk.io/math"
	abci "github.com/cometbft/cometbft/abci/types"
	"github.com/cosmos/cosmos-sdk/codec"
	sdk "github.com/cosmos/cosmos-sdk/types"
	stakingtypes "github.com/cosmos/cosmos-sdk/x/staking/types"
	"github.com/stretchr/testify/require"

	routertypes "github.com/saicon-network/saicon/chain/x/delegationrouter/types"
	stakeenginetypes "github.com/saicon-network/saicon/chain/x/stakeengine/types"
)

// The delegation router, end to end: a signed MsgDelegateRouted through
// FinalizeBlock on the real app (ante handler, message router, staking hooks,
// EndBlock queue fill).

// withStakeParams edits x/stakeengine's genesis params.
func withStakeParams(edit func(p *stakeenginetypes.Params)) func(codec.Codec, map[string]json.RawMessage) {
	return func(cdc codec.Codec, gen map[string]json.RawMessage) {
		var gs stakeenginetypes.GenesisState
		cdc.MustUnmarshalJSON(gen[stakeenginetypes.ModuleName], &gs)
		edit(&gs.Params)
		gen[stakeenginetypes.ModuleName] = cdc.MustMarshalJSON(&gs)
	}
}

// fivePercent lowers the global cap to 5% of max supply: 1,050,000 SCN against
// the 900,000 bonded at genesis, so 150,000 SCN of headroom.
var fivePercent = withStakeParams(func(p *stakeenginetypes.Params) {
	p.MaxSupplyFraction = math.LegacyNewDecWithPrec(5, 2)
})

func (c *testChain) routed(d acct, to sdk.ValAddress, amountSCN int64) *routertypes.MsgDelegateRouted {
	return &routertypes.MsgDelegateRouted{Delegator: d.addr.String(), Validator: to.String(), Amount: scnCoin(amountSCN)}
}

// queryRoute asks Query/Route through the app's gRPC query router.
func (c *testChain) queryRoute(d acct, to sdk.ValAddress, amountSCN int64) *routertypes.QueryRouteResponse {
	c.t.Helper()
	req := &routertypes.QueryRouteRequest{
		Delegator: d.addr.String(), Validator: to.String(), Amount: scnInt(amountSCN).String(),
	}
	bz, err := c.app.AppCodec().Marshal(req)
	require.NoError(c.t, err)
	handler := c.app.GRPCQueryRouter().Route("/chain.delegationrouter.v1.Query/Route")
	require.NotNil(c.t, handler)
	res, err := handler(c.ctx(), &abci.RequestQuery{Data: bz})
	require.NoError(c.t, err)
	var out routertypes.QueryRouteResponse
	require.NoError(c.t, c.app.AppCodec().Unmarshal(res.Value, &out))
	return &out
}

// staked is what d has delegated to v, in tokens (0 if nothing).
func (c *testChain) staked(d sdk.AccAddress, v sdk.ValAddress) math.Int {
	ctx := c.ctx()
	del, err := c.app.StakingKeeper.GetDelegation(ctx, d, v)
	if err != nil {
		return math.ZeroInt()
	}
	val, err := c.app.StakingKeeper.GetValidator(ctx, v)
	require.NoError(c.t, err)
	return val.TokensFromShares(del.Shares).TruncateInt()
}

type queued struct {
	validator string
	amount    math.Int
}

func (c *testChain) queue() []queued {
	var out []queued
	err := c.app.StakeengineKeeper.Queue.Walk(c.ctx(), nil, func(_ uint64, e stakeenginetypes.QueueEntry) (bool, error) {
		out = append(out, queued{e.Validator, e.Amount})
		return false, nil
	})
	require.NoError(c.t, err)
	return out
}

func sumQueued(q []queued) math.Int {
	total := math.ZeroInt()
	for _, e := range q {
		total = total.Add(e.amount)
	}
	return total
}

func eventAttr(events []abci.Event, typ, key string) (string, bool) {
	for _, e := range events {
		if e.Type != typ {
			continue
		}
		for _, a := range e.Attributes {
			if a.Key == key {
				return a.Value, true
			}
		}
	}
	return "", false
}

func TestRouterSpillsAnOversizedDelegationOverTheOtherValidators(t *testing.T) {
	c := newTestChain(t)
	d := c.delegators[0]
	a, b, cc := c.vals[0].val, c.vals[1].val, c.vals[2].val
	balance := c.balance(d.addr)

	// Query/Route before the message names the legs the message then executes.
	preview := c.queryRoute(d, a, 250_000)
	require.True(t, preview.Fits.Equal(scnInt(250_000)), "fits %s", preview.Fits)
	require.Empty(t, preview.Queued)
	require.Len(t, preview.Legs, 3)

	res := c.deliverTx(d, c.routed(d, a, 250_000))
	require.Equal(t, uint32(0), res.Code, res.Log)

	// A has room for 100,000; B and C share the other 150,000.
	require.Equal(t, scnInt(100_000), c.staked(d.addr, a))
	require.Equal(t, scnInt(75_000), c.staked(d.addr, b))
	require.Equal(t, scnInt(75_000), c.staked(d.addr, cc))
	require.Equal(t, scnInt(validatorSelfSCN+100_000), c.tokens(c.ctx(), a))
	require.Equal(t, balance.Sub(scnInt(250_000)), c.balance(d.addr))
	require.Empty(t, c.queue())

	// The preview was the execution.
	for _, leg := range preview.Legs {
		v, err := sdk.ValAddressFromBech32(leg.Validator)
		require.NoError(t, err)
		require.Equal(t, leg.Amount, c.staked(d.addr, v), "leg %s", leg.Validator)
	}

	// The event names the legs.
	legs, ok := eventAttr(res.Events, routertypes.EventTypeDelegationRouted, routertypes.AttributeKeyLegs)
	require.True(t, ok, "no %s event", routertypes.EventTypeDelegationRouted)
	require.Contains(t, legs, a.String()+":"+scnInt(100_000).String())
	require.Contains(t, legs, b.String()+":"+scnInt(75_000).String())
	amount, _ := eventAttr(res.Events, routertypes.EventTypeDelegationRouted, routertypes.AttributeKeyAmount)
	require.Equal(t, scnInt(250_000).String(), amount)
}

func TestPlainDelegationOfTheSameAmountIsRefusedWithTheRouterHint(t *testing.T) {
	c := newTestChain(t)
	d := c.delegators[0]
	balance := c.balance(d.addr)

	res := c.deliverTx(d, msgDelegate(d.addr.String(), c.vals[0].val.String(), scnInt(250_000)))
	require.Equal(t, stakeenginetypes.ErrValidatorCapReached.ABCICode(), res.Code, res.Log)
	require.Contains(t, res.Log, "MsgDelegateRouted (saicond tx delegationrouter delegate)")
	require.Equal(t, balance, c.balance(d.addr))
}

func TestRouterQueuesWhatTheGlobalCapCannotTakeAndTheNextBlocksFillIt(t *testing.T) {
	c := newTestChain(t, fivePercent)
	d := c.delegators[0]
	a, b, cc := c.vals[0].val, c.vals[1].val, c.vals[2].val
	k := c.app.StakeengineKeeper
	balance := c.balance(d.addr)

	headroom, err := k.Headroom(c.ctx())
	require.NoError(t, err)
	require.Equal(t, scnInt(150_000), headroom)

	preview := c.queryRoute(d, a, 250_000)
	require.True(t, preview.Fits.Equal(scnInt(250_000)))
	require.NotEmpty(t, preview.Queued)

	res := c.deliverTx(d, c.routed(d, a, 250_000))
	require.Equal(t, uint32(0), res.Code, res.Log)

	// 150,000 went in at once, in leg order: A's 100,000, then the first of the
	// other two legs (75,000 each) as far as the headroom goes.
	require.Equal(t, scnInt(100_000), c.staked(d.addr, a))
	require.Equal(t, scnInt(50_000), c.staked(d.addr, b).Add(c.staked(d.addr, cc)))

	// The other 100,000 is queued, on its legs' validators, and left the account:
	// each of B and C holds 75,000 between its delegation and its queue entry.
	q := c.queue()
	require.Len(t, q, 2)
	require.Equal(t, scnInt(100_000), sumQueued(q))
	for _, e := range q {
		v, err := sdk.ValAddressFromBech32(e.validator)
		require.NoError(t, err)
		require.Equal(t, scnInt(75_000), c.staked(d.addr, v).Add(e.amount), "leg %s", e.validator)
	}
	require.Equal(t, balance.Sub(scnInt(250_000)), c.balance(d.addr))
	escrow := c.balance(k.EscrowAddress())
	require.Equal(t, scnInt(100_000), escrow)
	msg, broken := k.EscrowInvariant(c.ctx())
	require.False(t, broken, msg)

	// No room yet: empty blocks leave the queue as it is.
	c.nextBlock(time.Second)
	c.nextBlock(time.Second)
	require.Equal(t, scnInt(100_000), sumQueued(c.queue()))

	// The delegator takes its A leg out: 100,000 of headroom opens, and the block's
	// EndBlock fills the queue from it.
	undelegate := stakingtypes.NewMsgUndelegate(d.addr.String(), a.String(), scnCoin(100_000))
	res = c.deliverTx(d, undelegate)
	require.Equal(t, uint32(0), res.Code, res.Log)

	require.Empty(t, c.queue(), "the queue filled as room opened")
	require.True(t, c.balance(k.EscrowAddress()).IsZero())
	msg, broken = k.EscrowInvariant(c.ctx())
	require.False(t, broken, msg)
	require.Equal(t, scnInt(75_000), c.staked(d.addr, b))
	require.Equal(t, scnInt(75_000), c.staked(d.addr, cc))
	require.Equal(t, math.ZeroInt(), c.staked(d.addr, a))
}

// All or nothing: when a later leg fails, the legs that already went through
// are undone with it.
func TestRouterIsAllOrNothing(t *testing.T) {
	c := newTestChain(t, fivePercent, withStakeParams(func(p *stakeenginetypes.Params) {
		p.MaxQueueEntries = 1 // the second queued leg has nowhere to go
	}))
	d := c.delegators[0]
	a := c.vals[0].val
	balance := c.balance(d.addr)
	k := c.app.StakeengineKeeper

	// Precondition: the route has a direct leg first, then two queued legs.
	preview := c.queryRoute(d, a, 250_000)
	require.True(t, preview.Fits.Equal(scnInt(250_000)))
	require.NotEmpty(t, preview.Legs)
	require.Len(t, preview.Queued, 2)

	res := c.deliverTx(d, c.routed(d, a, 250_000))
	require.Equal(t, stakeenginetypes.ErrQueueFull.ABCICode(), res.Code, res.Log)
	require.Equal(t, stakeenginetypes.ErrQueueFull.Codespace(), res.Codespace)

	for _, v := range c.vals {
		require.True(t, c.staked(d.addr, v.val).IsZero(), "a delegation survived the failed message on %s", v.val)
	}
	require.Equal(t, balance, c.balance(d.addr))
	require.Empty(t, c.queue())
	require.True(t, c.balance(k.EscrowAddress()).IsZero())
	require.Equal(t, scnInt(3*validatorSelfSCN), c.app.BankKeeper.GetBalance(c.ctx(),
		c.app.AuthKeeper.GetModuleAddress(stakingtypes.BondedPoolName), stakeenginetypes.BondDenom).Amount)
}

// A delegator already on three validators cannot route to a fourth.
func TestRouterRefusesAFourthValidator(t *testing.T) {
	c := newTestChain(t)
	d := c.delegators[0]

	for _, v := range c.vals {
		res := c.deliverTx(d, msgDelegate(d.addr.String(), v.val.String(), scnInt(10_000)))
		require.Equal(t, uint32(0), res.Code, res.Log)
	}

	// A fourth validator, created in a transaction; the next block bonds it and
	// the registry marks it ACTIVE, so it is a valid primary.
	create, err := stakingtypes.NewMsgCreateValidator(
		c.fourth.val.String(), newConsKey("fourth").PubKey(), scnCoin(5_000),
		stakingtypes.Description{Moniker: "D"},
		stakingtypes.NewCommissionRates(math.LegacyZeroDec(), math.LegacyOneDec(), math.LegacyOneDec()),
		math.OneInt())
	require.NoError(t, err)
	res := c.deliverTx(c.fourth, create)
	require.Equal(t, uint32(0), res.Code, res.Log)
	c.nextBlock(time.Second)

	preview := c.queryRoute(d, c.fourth.val, 1_000)
	require.Empty(t, preview.Legs)
	require.True(t, strings.Contains(preview.Reason, "maximum number of validators"), "reason: %q", preview.Reason)

	res = c.deliverTx(d, c.routed(d, c.fourth.val, 1_000))
	require.Equal(t, stakeenginetypes.ErrTooManyValidators.ABCICode(), res.Code, res.Log)
	require.True(t, c.staked(d.addr, c.fourth.val).IsZero())
}
