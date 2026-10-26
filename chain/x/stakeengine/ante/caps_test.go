package ante_test

import (
	"context"
	"sort"
	"testing"

	"cosmossdk.io/math"
	sdk "github.com/cosmos/cosmos-sdk/types"
	"github.com/cosmos/cosmos-sdk/x/authz"
	banktypes "github.com/cosmos/cosmos-sdk/x/bank/types"
	stakingtypes "github.com/cosmos/cosmos-sdk/x/staking/types"
	"github.com/stretchr/testify/require"
	protov2 "google.golang.org/protobuf/proto"

	seante "github.com/saicon-network/saicon/chain/x/stakeengine/ante"
	"github.com/saicon-network/saicon/chain/x/stakeengine/types"
)

const scn = int64(100_000_000)

func nscn(s int64) math.Int         { return math.NewInt(s).MulRaw(scn) }
func coin(s int64) sdk.Coin         { return sdk.NewCoin(types.BondDenom, nscn(s)) }
func valAddr(b byte) sdk.ValAddress { return sdk.ValAddress(accAddr(b)) }
func accAddr(b byte) sdk.AccAddress {
	return sdk.AccAddress([]byte{b, 1, 2, 3, 4, 5, 6, 7, 8, 9, 10, 11, 12, 13, 14, 15, 16, 17, 18, 19})
}

type fakeKeeper struct {
	headroom, vcap math.Int
	tokens, self   map[string]math.Int // key: valoper string
	err            error               // returned by every read, when set
	reads          int

	slots map[string][]string // delegator bech32 → sorted valoper slots held
	max   uint32
	min   math.Int
}

func (f *fakeKeeper) DelegatorSlots(_ context.Context, d sdk.AccAddress) ([]string, error) {
	f.reads++
	return append([]string(nil), f.slots[d.String()]...), f.err
}
func (f *fakeKeeper) MaxValidatorsPerDelegator(context.Context) (uint32, error) {
	f.reads++
	return f.max, f.err
}
func (f *fakeKeeper) MinDelegation(context.Context) (math.Int, error) {
	f.reads++
	return f.min, f.err
}

func (f *fakeKeeper) Headroom(context.Context) (math.Int, error) {
	f.reads++
	return f.headroom, f.err
}
func (f *fakeKeeper) ValidatorCap(context.Context) (math.Int, error) {
	f.reads++
	return f.vcap, f.err
}
func (f *fakeKeeper) ValidatorTokens(_ context.Context, v sdk.ValAddress) (math.Int, error) {
	f.reads++
	if t, ok := f.tokens[v.String()]; ok {
		return t, f.err
	}
	return math.ZeroInt(), f.err
}
func (f *fakeKeeper) SelfBond(_ context.Context, v sdk.ValAddress) (math.Int, error) {
	f.reads++
	if s, ok := f.self[v.String()]; ok {
		return s, f.err
	}
	return math.ZeroInt(), f.err
}

// 100k of headroom, a 50k validator cap, val(1) holding 40k of which 5k is self-bond.
func newKeeper() *fakeKeeper {
	return &fakeKeeper{
		headroom: nscn(100_000),
		vcap:     nscn(50_000),
		tokens:   map[string]math.Int{valAddr(1).String(): nscn(40_000)},
		self:     map[string]math.Int{valAddr(1).String(): nscn(5_000)},
		slots:    map[string][]string{},
		max:      3,
		min:      nscn(100),
	}
}

type msgsTx struct{ msgs []sdk.Msg }

func (t msgsTx) GetMsgs() []sdk.Msg                    { return t.msgs }
func (t msgsTx) GetMsgsV2() ([]protov2.Message, error) { return nil, nil }

func noopNext(ctx sdk.Context, _ sdk.Tx, _ bool) (sdk.Context, error) { return ctx, nil }

func runAt(k *fakeKeeper, height int64, msgs ...sdk.Msg) error {
	_, err := seante.NewCapsDecorator(k).AnteHandle(sdk.Context{}.WithBlockHeight(height), msgsTx{msgs}, false, noopNext)
	return err
}

func run(k *fakeKeeper, msgs ...sdk.Msg) error { return runAt(k, 1, msgs...) }

func delegate(from sdk.AccAddress, to sdk.ValAddress, amt int64) sdk.Msg {
	return &stakingtypes.MsgDelegate{DelegatorAddress: from.String(), ValidatorAddress: to.String(), Amount: coin(amt)}
}

func undelegate(from sdk.AccAddress, v sdk.ValAddress, amt int64) sdk.Msg {
	return &stakingtypes.MsgUndelegate{DelegatorAddress: from.String(), ValidatorAddress: v.String(), Amount: coin(amt)}
}

func createValidator(v sdk.ValAddress, amt int64) sdk.Msg {
	return &stakingtypes.MsgCreateValidator{ValidatorAddress: v.String(), Value: coin(amt)}
}

func nestExec(inner sdk.Msg, depth int) sdk.Msg {
	m := inner
	for i := 0; i < depth; i++ {
		e := authz.NewMsgExec(accAddr(9), []sdk.Msg{m})
		m = &e
	}
	return m
}

func TestCaps_GentxsAtHeightZeroAreNotChecked(t *testing.T) {
	k := newKeeper()
	k.err = types.ErrCapsNotComputed
	require.NoError(t, runAt(k, 0, createValidator(valAddr(5), 1_000_000)))
	require.ErrorIs(t, runAt(k, 1, createValidator(valAddr(5), 2_000)), types.ErrCapsNotComputed)
}

func TestCaps_ANonStakingTxReadsNothing(t *testing.T) {
	k := newKeeper()
	require.NoError(t, run(k, &banktypes.MsgSend{}))
	require.Zero(t, k.reads)
}

func TestCaps_CreateValidator(t *testing.T) {
	require.ErrorIs(t, run(newKeeper(), createValidator(valAddr(5), 1_999)), types.ErrBelowEntryFloor)
	require.NoError(t, run(newKeeper(), createValidator(valAddr(5), 2_000)), "exactly the floor enters")

	k := newKeeper()
	k.headroom = nscn(1_000)
	require.ErrorIs(t, run(k, createValidator(valAddr(5), 2_000)), types.ErrGlobalCapReached)

	require.ErrorIs(t, run(newKeeper(), createValidator(valAddr(5), 50_001)), types.ErrValidatorCapReached)
}

func TestCaps_Delegate(t *testing.T) {
	k := newKeeper()
	k.vcap = nscn(1_000_000)
	err := run(k, delegate(accAddr(2), valAddr(1), 100_001))
	require.ErrorIs(t, err, types.ErrGlobalCapReached)
	require.ErrorContains(t, err, "MsgEnqueueDelegation", "the rejection names the way in")
	require.NoError(t, run(k, delegate(accAddr(2), valAddr(1), 100_000)), "exactly the headroom fits")

	require.ErrorIs(t, run(newKeeper(), delegate(accAddr(2), valAddr(1), 10_001)), types.ErrValidatorCapReached)
	require.NoError(t, run(newKeeper(), delegate(accAddr(2), valAddr(1), 10_000)), "40k + 10k is exactly the cap")
}

func TestCaps_ChecksAreCumulativeWithinOneTx(t *testing.T) {
	k := newKeeper()
	k.vcap = nscn(1_000_000)
	require.ErrorIs(t, run(k,
		delegate(accAddr(2), valAddr(1), 60_000),
		delegate(accAddr(3), valAddr(2), 60_000),
	), types.ErrGlobalCapReached, "each fits the 100k headroom alone; together they do not")

	require.ErrorIs(t, run(newKeeper(),
		delegate(accAddr(2), valAddr(1), 6_000),
		delegate(accAddr(3), valAddr(1), 6_000),
	), types.ErrValidatorCapReached, "each fits val(1)'s 10k of room alone; together they do not")

	require.ErrorIs(t, run(newKeeper(),
		&banktypes.MsgSend{},
		nestExec(delegate(accAddr(2), valAddr(1), 6_000), 1),
		delegate(accAddr(3), valAddr(1), 6_000),
	), types.ErrValidatorCapReached, "an authz-wrapped message counts towards the same totals")
}

func TestCaps_CancelUnbondingCountsAsNewStake(t *testing.T) {
	k := newKeeper()
	k.headroom = nscn(1_000)
	require.ErrorIs(t, run(k, &stakingtypes.MsgCancelUnbondingDelegation{
		DelegatorAddress: accAddr(2).String(), ValidatorAddress: valAddr(1).String(), Amount: coin(1_001), CreationHeight: 1,
	}), types.ErrGlobalCapReached)
	require.ErrorIs(t, run(newKeeper(), &stakingtypes.MsgCancelUnbondingDelegation{
		DelegatorAddress: accAddr(2).String(), ValidatorAddress: valAddr(1).String(), Amount: coin(10_001), CreationHeight: 1,
	}), types.ErrValidatorCapReached)
}

func TestCaps_RedelegateChecksOnlyTheDestination(t *testing.T) {
	k := newKeeper()
	k.headroom = math.ZeroInt() // the global cap is full
	require.NoError(t, run(k, &stakingtypes.MsgBeginRedelegate{
		DelegatorAddress: accAddr(2).String(), ValidatorSrcAddress: valAddr(2).String(), ValidatorDstAddress: valAddr(1).String(), Amount: coin(10_000),
	}), "redelegation moves stake, it adds none")
	require.ErrorIs(t, run(newKeeper(), &stakingtypes.MsgBeginRedelegate{
		DelegatorAddress: accAddr(2).String(), ValidatorSrcAddress: valAddr(2).String(), ValidatorDstAddress: valAddr(1).String(), Amount: coin(10_001),
	}), types.ErrValidatorCapReached)
}

// val(1)'s operator has 5,000 SCN of self-bond.
func TestCaps_SelfBondMayNotFallBelowTheFloorExceptToZero(t *testing.T) {
	op := sdk.AccAddress(valAddr(1))
	require.ErrorIs(t, run(newKeeper(), undelegate(op, valAddr(1), 3_001)), types.ErrSelfBondBelowFloor)
	require.NoError(t, run(newKeeper(), undelegate(op, valAddr(1), 3_000)), "2,000 left is exactly the floor")
	require.NoError(t, run(newKeeper(), undelegate(op, valAddr(1), 5_000)), "a full exit is always allowed")
	require.ErrorIs(t, run(newKeeper(),
		undelegate(op, valAddr(1), 2_000),
		undelegate(op, valAddr(1), 2_000),
	), types.ErrSelfBondBelowFloor, "two withdrawals in one tx are summed")
	require.ErrorIs(t, run(newKeeper(), &stakingtypes.MsgBeginRedelegate{
		DelegatorAddress: op.String(), ValidatorSrcAddress: valAddr(1).String(), ValidatorDstAddress: valAddr(2).String(), Amount: coin(4_000),
	}), types.ErrSelfBondBelowFloor)
	require.NoError(t, run(newKeeper(), undelegate(accAddr(2), valAddr(1), 39_000)), "another delegator is not bound by the floor")
}

func TestCaps_AuthzNestingFailsClosed(t *testing.T) {
	require.ErrorIs(t, run(newKeeper(), nestExec(delegate(accAddr(2), valAddr(1), 10_001), 5)), types.ErrValidatorCapReached,
		"five layers deep is still unwrapped and checked")
	require.ErrorIs(t, run(newKeeper(), nestExec(delegate(accAddr(2), valAddr(1), 1), 6)), types.ErrAuthzTooDeep)
}

func TestCaps_OtherDenomsAreLeftToStaking(t *testing.T) {
	require.NoError(t, run(newKeeper(), &stakingtypes.MsgDelegate{
		DelegatorAddress: accAddr(2).String(), ValidatorAddress: valAddr(1).String(), Amount: sdk.NewCoin("uatom", nscn(1_000_000)),
	}))
}

func TestCaps_SelfBondFloorHoldsThroughAuthz(t *testing.T) {
	op := sdk.AccAddress(valAddr(1))
	require.ErrorIs(t, run(newKeeper(), nestExec(undelegate(op, valAddr(1), 3_001), 1)), types.ErrSelfBondBelowFloor)
	require.ErrorIs(t, run(newKeeper(), nestExec(undelegate(op, valAddr(1), 4_999), 1)), types.ErrSelfBondBelowFloor,
		"leaving 1 SCN is below the floor")
	require.NoError(t, run(newKeeper(), nestExec(undelegate(op, valAddr(1), 3_000), 1)))
	require.NoError(t, run(newKeeper(), nestExec(undelegate(op, valAddr(1), 5_000), 1)), "a full exit through authz is allowed")
}

func TestCaps_SelfBondWithdrawalsSumAcrossRedelegateAndUndelegate(t *testing.T) {
	op := sdk.AccAddress(valAddr(1))
	require.ErrorIs(t, run(newKeeper(),
		&stakingtypes.MsgBeginRedelegate{
			DelegatorAddress: op.String(), ValidatorSrcAddress: valAddr(1).String(), ValidatorDstAddress: valAddr(2).String(), Amount: coin(2_000),
		},
		undelegate(op, valAddr(1), 2_000),
	), types.ErrSelfBondBelowFloor, "5,000 - 2,000 - 2,000 leaves 1,000, below the floor")
}

func TestCaps_ANewValidatorAndADelegationToItShareTheCaps(t *testing.T) {
	newVal := valAddr(5)
	require.ErrorIs(t, run(newKeeper(),
		createValidator(newVal, 40_000),
		delegate(accAddr(2), newVal, 10_001),
	), types.ErrValidatorCapReached, "40k + 10,001 passes the 50k validator cap")
	require.NoError(t, run(newKeeper(),
		createValidator(newVal, 40_000),
		delegate(accAddr(2), newVal, 10_000),
	), "40k + 10k is exactly the cap")

	k := newKeeper()
	k.headroom = nscn(45_000)
	require.ErrorIs(t, run(k,
		createValidator(newVal, 40_000),
		delegate(accAddr(2), newVal, 6_000),
	), types.ErrGlobalCapReached, "the creation uses 40k of the 45k headroom")
}

// holds gives the delegator slots on the given validators, sorted as the keeper returns them.
func (f *fakeKeeper) holds(d sdk.AccAddress, vs ...sdk.ValAddress) {
	var out []string
	for _, v := range vs {
		out = append(out, v.String())
	}
	sort.Strings(out)
	f.slots[d.String()] = out
}

func TestCaps_MinimumDelegation(t *testing.T) {
	d := accAddr(2)
	below := &stakingtypes.MsgDelegate{DelegatorAddress: d.String(), ValidatorAddress: valAddr(1).String(), Amount: sdk.NewCoin(types.BondDenom, nscn(100).SubRaw(1))}
	require.ErrorIs(t, run(newKeeper(), below), types.ErrBelowMinDelegation)
	require.NoError(t, run(newKeeper(), delegate(d, valAddr(1), 100)), "exactly the minimum passes")
	require.ErrorIs(t, run(newKeeper(), nestExec(below, 1)), types.ErrBelowMinDelegation, "through authz")
}

func TestCaps_RedelegateAndCancelUnbondingAreNotSubjectToTheMinimum(t *testing.T) {
	d := accAddr(2)
	require.NoError(t, run(newKeeper(), &stakingtypes.MsgBeginRedelegate{
		DelegatorAddress: d.String(), ValidatorSrcAddress: valAddr(2).String(), ValidatorDstAddress: valAddr(1).String(), Amount: coin(1),
	}))
	require.NoError(t, run(newKeeper(), &stakingtypes.MsgCancelUnbondingDelegation{
		DelegatorAddress: d.String(), ValidatorAddress: valAddr(1).String(), Amount: coin(1), CreationHeight: 1,
	}))
}

func TestCaps_AtMostThreeValidatorsPerDelegator(t *testing.T) {
	d := accAddr(2)
	k := newKeeper()
	k.holds(d, valAddr(11), valAddr(12), valAddr(13))
	require.ErrorIs(t, run(k, delegate(d, valAddr(1), 100)), types.ErrTooManyValidators, "a fourth validator")
	require.NoError(t, run(k, delegate(d, valAddr(12), 100)), "one it already holds")
	require.ErrorIs(t, run(k, nestExec(delegate(d, valAddr(1), 100), 1)), types.ErrTooManyValidators, "through authz")
}

func TestCaps_SlotsAddedEarlierInTheSameTxCount(t *testing.T) {
	d := accAddr(2)
	k := newKeeper()
	k.holds(d, valAddr(11), valAddr(12))
	require.ErrorIs(t, run(k,
		delegate(d, valAddr(1), 100),
		delegate(d, valAddr(4), 100),
	), types.ErrTooManyValidators, "the second new validator is the fourth")
	require.ErrorIs(t, run(k,
		delegate(d, valAddr(1), 100),
		nestExec(delegate(d, valAddr(4), 100), 1),
	), types.ErrTooManyValidators, "also when one is wrapped in authz")
}

func TestCaps_TwoDelegationsToTheSameNewValidatorTakeOneSlot(t *testing.T) {
	d := accAddr(2)
	k := newKeeper()
	k.holds(d, valAddr(11), valAddr(12))
	require.NoError(t, run(k,
		delegate(d, valAddr(1), 100),
		delegate(d, valAddr(1), 100),
	), "the same new validator twice is one slot, not two")
}

func TestCaps_AnOperatorsOwnValidatorIsNotASlot(t *testing.T) {
	op := sdk.AccAddress(valAddr(1))
	k := newKeeper()
	k.holds(op, valAddr(11), valAddr(12), valAddr(13))
	require.NoError(t, run(k, delegate(op, valAddr(1), 100)))
}

func TestCaps_AHeldValidatorAddedAgainInTheTxIsNotCountedTwice(t *testing.T) {
	d := accAddr(2)
	k := newKeeper()
	k.holds(d, valAddr(1), valAddr(12))
	require.NoError(t, run(k,
		delegate(d, valAddr(1), 100),
		delegate(d, valAddr(4), 100),
	), "two held slots plus one new one is three")
}
