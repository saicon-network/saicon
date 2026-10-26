package ante

import (
	"bytes"
	"context"
	"sort"

	errorsmod "cosmossdk.io/errors"
	"cosmossdk.io/math"
	sdk "github.com/cosmos/cosmos-sdk/types"
	"github.com/cosmos/cosmos-sdk/x/authz"
	stakingtypes "github.com/cosmos/cosmos-sdk/x/staking/types"

	"github.com/saicon-network/saicon/chain/x/stakeengine/types"
)

// CapsKeeper is the stakeengine surface the decorator reads.
type CapsKeeper interface {
	Headroom(ctx context.Context) (math.Int, error)
	ValidatorCap(ctx context.Context) (math.Int, error)
	ValidatorTokens(ctx context.Context, val sdk.ValAddress) (math.Int, error)
	SelfBond(ctx context.Context, val sdk.ValAddress) (math.Int, error)
	DelegatorSlots(ctx context.Context, delegator sdk.AccAddress) ([]string, error)
	MaxValidatorsPerDelegator(ctx context.Context) (uint32, error)
	MinDelegation(ctx context.Context) (math.Int, error)
}

// CapsDecorator enforces the entry floor, the global cap and the per-validator
// cap on staking messages (ADR-0012, spec §7). It recurses into authz.MsgExec
// and fails closed beyond maxAuthzDepth, like the V-3 lock-up decorator.
type CapsDecorator struct{ k CapsKeeper }

func NewCapsDecorator(k CapsKeeper) CapsDecorator { return CapsDecorator{k: k} }

const maxAuthzDepth = 5

func (d CapsDecorator) AnteHandle(ctx sdk.Context, tx sdk.Tx, simulate bool, next sdk.AnteHandler) (sdk.Context, error) {
	// Gentxs pass through the ante handler at height 0, inside x/genutil's
	// InitGenesis — before this module's InitGenesis has computed the caps.
	if ctx.BlockHeight() == 0 {
		return next(ctx, tx, simulate)
	}
	c := &checker{
		k:         d.k,
		ctx:       ctx,
		added:     map[string]math.Int{},
		withdrawn: map[string]math.Int{},
		newSlots:  map[string]map[string]bool{},
	}
	if err := c.check(tx.GetMsgs(), 0); err != nil {
		return ctx, err
	}
	return next(ctx, tx, simulate)
}

// checker carries one transaction's running totals, so its messages are checked
// together: two delegations that each fit cannot pass the cap side by side.
type checker struct {
	k   CapsKeeper
	ctx sdk.Context

	loaded   bool
	headroom math.Int
	vcap     math.Int

	added     map[string]math.Int // valoper → tokens this tx adds to it
	withdrawn map[string]math.Int // valoper → self-bond this tx takes out of it

	newSlots map[string]map[string]bool // delegator → validators this tx adds
}

func get(m map[string]math.Int, key string) math.Int {
	if v, ok := m[key]; ok {
		return v
	}
	return math.ZeroInt()
}

// load reads the caps on the first staking message only, so a transaction with
// none pays nothing for this decorator.
func (c *checker) load() error {
	if c.loaded {
		return nil
	}
	h, err := c.k.Headroom(c.ctx)
	if err != nil {
		return err
	}
	v, err := c.k.ValidatorCap(c.ctx)
	if err != nil {
		return err
	}
	c.headroom, c.vcap, c.loaded = h, v, true
	return nil
}

// bondable is true for a positive amount in the bond denom. Anything else is
// x/staking's to reject, with its own error.
func bondable(c sdk.Coin) bool {
	return c.Denom == types.BondDenom && !c.Amount.IsNil() && c.Amount.IsPositive()
}

func (c *checker) check(msgs []sdk.Msg, depth int) error {
	for _, m := range msgs {
		var err error
		switch msg := m.(type) {
		case *stakingtypes.MsgCreateValidator:
			err = c.createValidator(msg)
		case *stakingtypes.MsgDelegate:
			if err = c.delegation(msg); err == nil {
				err = c.newStake(msg.ValidatorAddress, msg.Amount)
			}
		case *stakingtypes.MsgCancelUnbondingDelegation:
			err = c.newStake(msg.ValidatorAddress, msg.Amount)
		case *stakingtypes.MsgBeginRedelegate:
			err = c.redelegate(msg)
		case *stakingtypes.MsgUndelegate:
			err = c.withdrawSelfBond(msg.DelegatorAddress, msg.ValidatorAddress, msg.Amount)
		case *authz.MsgExec:
			if depth >= maxAuthzDepth {
				// Fail CLOSED: past the cap we cannot prove the inner messages
				// respect the caps, and no legitimate tx nests authz this deep.
				return errorsmod.Wrapf(types.ErrAuthzTooDeep, "nested beyond depth %d", maxAuthzDepth)
			}
			inner, e := msg.GetMessages()
			if e != nil {
				return e
			}
			err = c.check(inner, depth+1)
		}
		if err != nil {
			return err
		}
	}
	return nil
}

func (c *checker) createValidator(msg *stakingtypes.MsgCreateValidator) error {
	if !bondable(msg.Value) {
		return nil
	}
	if msg.Value.Amount.LT(types.EntryFloor()) {
		return errorsmod.Wrapf(types.ErrBelowEntryFloor,
			"a new validator brings %s nscn of its own; the entry floor is %s nscn (2,000 SCN)", msg.Value.Amount, types.EntryFloor())
	}
	va, err := sdk.ValAddressFromBech32(msg.ValidatorAddress)
	if err != nil {
		return nil
	}
	if err := c.load(); err != nil {
		return err
	}
	if msg.Value.Amount.GT(c.headroom) {
		return errorsmod.Wrapf(types.ErrGlobalCapReached,
			"a new validator's %s nscn exceeds the %s nscn of headroom under the global cap", msg.Value.Amount, c.headroom)
	}
	if err := c.fitsValidator(va, msg.Value.Amount); err != nil {
		return err
	}
	c.headroom = c.headroom.Sub(msg.Value.Amount)
	return nil
}

// delegation applies the S7 per-message rules to a MsgDelegate: the minimum
// amount, and the limit on validators per delegator. Slots this tx already adds
// count towards the limit.
func (c *checker) delegation(msg *stakingtypes.MsgDelegate) error {
	if !bondable(msg.Amount) {
		return nil
	}
	min, err := c.k.MinDelegation(c.ctx)
	if err != nil {
		return err
	}
	if err := types.CheckMinDelegation(msg.Amount.Amount, min); err != nil {
		return err
	}
	acc, err := sdk.AccAddressFromBech32(msg.DelegatorAddress)
	if err != nil {
		return nil
	}
	va, err := sdk.ValAddressFromBech32(msg.ValidatorAddress)
	if err != nil {
		return nil
	}
	if bytes.Equal(acc, va) {
		return nil // self-bond is not a slot
	}
	slots, err := c.k.DelegatorSlots(c.ctx, acc)
	if err != nil {
		return err
	}
	for v := range c.newSlots[msg.DelegatorAddress] {
		slots = append(slots, v)
	}
	// A validator held and also added by this tx must count once.
	sort.Strings(slots)
	uniq := slots[:0]
	for i, v := range slots {
		if i == 0 || v != slots[i-1] {
			uniq = append(uniq, v)
		}
	}
	max, err := c.k.MaxValidatorsPerDelegator(c.ctx)
	if err != nil {
		return err
	}
	if err := types.CheckNewSlot(uniq, va.String(), max); err != nil {
		return err
	}
	if c.newSlots[msg.DelegatorAddress] == nil {
		c.newSlots[msg.DelegatorAddress] = map[string]bool{}
	}
	c.newSlots[msg.DelegatorAddress][va.String()] = true
	return nil
}

func (c *checker) newStake(validator string, amount sdk.Coin) error {
	if !bondable(amount) {
		return nil
	}
	va, err := sdk.ValAddressFromBech32(validator)
	if err != nil {
		return nil
	}
	if err := c.load(); err != nil {
		return err
	}
	if amount.Amount.GT(c.headroom) {
		return errorsmod.Wrapf(types.ErrGlobalCapReached,
			"%s nscn exceeds the %s nscn of headroom under the global cap; join the stake queue with "+
				"MsgEnqueueDelegation (saicond tx stakeengine enqueue)", amount.Amount, c.headroom)
	}
	if err := c.fitsValidator(va, amount.Amount); err != nil {
		return err
	}
	c.headroom = c.headroom.Sub(amount.Amount)
	return nil
}

func (c *checker) redelegate(msg *stakingtypes.MsgBeginRedelegate) error {
	if !bondable(msg.Amount) {
		return nil
	}
	if err := c.withdrawSelfBond(msg.DelegatorAddress, msg.ValidatorSrcAddress, msg.Amount); err != nil {
		return err
	}
	dst, err := sdk.ValAddressFromBech32(msg.ValidatorDstAddress)
	if err != nil {
		return nil
	}
	if err := c.load(); err != nil {
		return err
	}
	return c.fitsValidator(dst, msg.Amount.Amount)
}

// fitsValidator checks val against the per-validator cap, counting what earlier
// messages in this tx already add to it, and records the addition.
func (c *checker) fitsValidator(val sdk.ValAddress, amount math.Int) error {
	tokens, err := c.k.ValidatorTokens(c.ctx, val)
	if err != nil {
		return err
	}
	key := val.String()
	after := tokens.Add(get(c.added, key)).Add(amount)
	if after.GT(c.vcap) {
		return errorsmod.Wrapf(types.ErrValidatorCapReached,
			"validator %s would hold %s nscn; the per-validator cap is %s nscn; "+
				"use MsgDelegateRouted (saicond tx delegationrouter delegate) to spread it", key, after, c.vcap)
	}
	c.added[key] = get(c.added, key).Add(amount)
	return nil
}

// withdrawSelfBond applies the entry floor to an operator taking its own stake
// out: what remains must be at least the floor, or nothing. A full exit is
// always allowed. Other delegators are not bound by it.
func (c *checker) withdrawSelfBond(delegator, validator string, amount sdk.Coin) error {
	if !bondable(amount) {
		return nil
	}
	acc, err := sdk.AccAddressFromBech32(delegator)
	if err != nil {
		return nil
	}
	val, err := sdk.ValAddressFromBech32(validator)
	if err != nil {
		return nil
	}
	if !bytes.Equal(acc, val) {
		return nil
	}
	self, err := c.k.SelfBond(c.ctx, val)
	if err != nil {
		return err
	}
	key := val.String()
	remaining := self.Sub(get(c.withdrawn, key)).Sub(amount.Amount)
	if remaining.IsNegative() {
		return nil // more than it has: x/staking rejects it
	}
	if !remaining.IsZero() && remaining.LT(types.EntryFloor()) {
		return errorsmod.Wrapf(types.ErrSelfBondBelowFloor,
			"validator %s would keep %s nscn of its own stake, below the %s nscn entry floor; withdraw all of it to exit",
			key, remaining, types.EntryFloor())
	}
	c.withdrawn[key] = get(c.withdrawn, key).Add(amount.Amount)
	return nil
}
