package app

import (
	"testing"
	"time"

	"cosmossdk.io/math"
	evidencetypes "cosmossdk.io/x/evidence/types"
	sdk "github.com/cosmos/cosmos-sdk/types"
	"github.com/cosmos/cosmos-sdk/x/authz"
	govv1 "github.com/cosmos/cosmos-sdk/x/gov/types/v1"
	stakingtypes "github.com/cosmos/cosmos-sdk/x/staking/types"
	"github.com/stretchr/testify/require"

	stakeenginetypes "github.com/saicon-network/saicon/chain/x/stakeengine/types"
	vrtypes "github.com/saicon-network/saicon/chain/x/validatorregistry/types"
)

// These tests attempt every S7 staking rule the way x/gov, x/group and an ICA
// host deliver messages — straight through the message router, with no ante
// handler — and show the rule holds. The ante is a convenience for fast,
// well-worded rejections; the staking hooks are what holds on every path.

var afterLockup = genesisTime.Add(61 * 24 * time.Hour)

func msgDelegate(from, to string, amount math.Int) *stakingtypes.MsgDelegate {
	return stakingtypes.NewMsgDelegate(from, to, sdk.NewCoin(stakeenginetypes.BondDenom, amount))
}

// 1. A delegation past a validator's cap is refused. This is also the wiring
// proof: it can only fail with ErrValidatorCapReached if x/staking's keeper
// carries x/stakeengine's hooks, because no ante handler runs on this path.
func TestRouterPathRefusesADelegationPastTheValidatorCap(t *testing.T) {
	c := newTestChain(t)
	ctx := c.ctx()
	a := c.vals[0].val

	// Room under the cap is 100,000 SCN: 50,000 fits, so the refusal below is the cap's.
	require.NoError(t, c.delegate(ctx, c.delegators[0], a, scnInt(50_000)))

	err := c.delegate(ctx, c.delegators[1], a, scnInt(100_000))
	require.ErrorIs(t, err, stakeenginetypes.ErrValidatorCapReached)
	require.Equal(t, scnInt(validatorSelfSCN+50_000), c.tokens(ctx, a), "a refused delegation leaves no trace")
}

// 2. The global cap, reached by delegating across validators.
func TestRouterPathRefusesADelegationPastTheGlobalCap(t *testing.T) {
	c := newTestChain(t)
	ctx := c.ctx()
	k := c.app.StakeengineKeeper

	// Lower the global cap to 5% of max supply: 1,050,000 SCN against 900,000 bonded.
	params, err := k.Params.Get(ctx)
	require.NoError(t, err)
	params.MaxSupplyFraction = math.LegacyNewDecWithPrec(5, 2)
	require.NoError(t, k.Params.Set(ctx, params))
	_, err = k.RecomputeCaps(ctx, 1, false)
	require.NoError(t, err)
	headroom, err := k.Headroom(ctx)
	require.NoError(t, err)
	require.Equal(t, scnInt(150_000), headroom)

	require.NoError(t, c.delegate(ctx, c.delegators[0], c.vals[0].val, scnInt(100_000)))
	require.NoError(t, c.delegate(ctx, c.delegators[1], c.vals[1].val, scnInt(50_000)))

	err = c.delegate(ctx, c.delegators[2], c.vals[2].val, scnInt(100))
	require.ErrorIs(t, err, stakeenginetypes.ErrGlobalCapReached)
}

// 3. At most three validators per delegator, enforced on delegation and on a
// partial redelegation; a full redelegation keeps the count and is allowed.
func TestRouterPathLimitsValidatorsPerDelegator(t *testing.T) {
	c := newTestChain(t)
	ctx := c.ctx()
	d := c.delegators[0]

	for _, v := range c.vals {
		require.NoError(t, c.delegate(ctx, d, v.val, scnInt(10_000)))
	}
	require.NoError(t, c.createFourthValidator(ctx, 5_000))
	fourth := c.fourth.val

	require.ErrorIs(t, c.delegate(ctx, d, fourth, scnInt(10_000)), stakeenginetypes.ErrTooManyValidators)

	partial := stakingtypes.NewMsgBeginRedelegate(d.addr.String(), c.vals[0].val.String(), fourth.String(), scnCoin(5_000))
	require.ErrorIs(t, c.deliverViaRouter(ctx, partial), stakeenginetypes.ErrTooManyValidators)

	full := stakingtypes.NewMsgBeginRedelegate(d.addr.String(), c.vals[0].val.String(), fourth.String(), scnCoin(10_000))
	require.NoError(t, c.deliverViaRouter(ctx, full))
	require.False(t, c.hasDelegation(ctx, d.addr, c.vals[0].val))
	require.True(t, c.hasDelegation(ctx, d.addr, fourth))
}

// 4. An operator keeps at least the entry floor of its own stake, or leaves
// entirely; after the lock-up a full exit is allowed.
func TestRouterPathKeepsTheOperatorAtOrAboveTheFloorOrOut(t *testing.T) {
	c := newTestChain(t)
	ctx := c.ctx().WithBlockTime(afterLockup)
	a := c.vals[0]
	floor := stakeenginetypes.EntryFloor()
	self := scnInt(validatorSelfSCN)

	below := stakingtypes.NewMsgUndelegate(a.addr.String(), a.val.String(),
		sdk.NewCoin(stakeenginetypes.BondDenom, self.Sub(floor).AddRaw(1)))
	require.ErrorIs(t, c.deliverViaRouter(ctx, below), stakeenginetypes.ErrSelfBondBelowFloor)

	exactly := stakingtypes.NewMsgUndelegate(a.addr.String(), a.val.String(),
		sdk.NewCoin(stakeenginetypes.BondDenom, self.Sub(floor)))
	require.NoError(t, c.deliverViaRouter(ctx, exactly), "leaving exactly the floor is allowed")

	exit := stakingtypes.NewMsgUndelegate(a.addr.String(), a.val.String(), sdk.NewCoin(stakeenginetypes.BondDenom, floor))
	require.NoError(t, c.deliverViaRouter(ctx, exit), "a full exit after the lock-up is allowed")
	require.False(t, c.hasDelegation(ctx, a.addr, a.val))
}

// 5. Inside the 60-day lock-up the operator's own stake cannot move.
func TestRouterPathHoldsTheLockup(t *testing.T) {
	c := newTestChain(t)
	ctx := c.ctx()
	a := c.vals[0]

	undelegate := stakingtypes.NewMsgUndelegate(a.addr.String(), a.val.String(), scnCoin(1_000))
	require.ErrorIs(t, c.deliverViaRouter(ctx, undelegate), vrtypes.ErrLockupActive)

	redelegate := stakingtypes.NewMsgBeginRedelegate(a.addr.String(), a.val.String(), c.vals[1].val.String(), scnCoin(1_000))
	require.ErrorIs(t, c.deliverViaRouter(ctx, redelegate), vrtypes.ErrLockupActive)

	require.Equal(t, scnInt(validatorSelfSCN), c.tokens(ctx, a.val))
}

// 6. authz.MsgExec re-dispatches through the message router, so it meets the
// same hooks.
func TestRouterPathRefusesAnAuthzWrappedDelegationPastTheCap(t *testing.T) {
	c := newTestChain(t)
	ctx := c.ctx()
	d := c.delegators[0]

	inner := msgDelegate(d.addr.String(), c.vals[0].val.String(), scnInt(150_000))
	exec := authz.NewMsgExec(d.addr, []sdk.Msg{inner})
	require.ErrorIs(t, c.deliverViaRouter(ctx, &exec), stakeenginetypes.ErrValidatorCapReached)
	require.Equal(t, scnInt(validatorSelfSCN), c.tokens(ctx, c.vals[0].val))
}

// 7. A validator is created with at least the entry floor of its own stake.
func TestRouterPathRefusesAValidatorBelowTheEntryFloor(t *testing.T) {
	c := newTestChain(t)
	ctx := c.ctx()

	require.ErrorIs(t, c.createFourthValidator(ctx, 1_000), stakeenginetypes.ErrSelfBondBelowFloor)
	_, err := c.app.StakingKeeper.GetValidator(ctx, c.fourth.val)
	require.ErrorIs(t, err, stakingtypes.ErrNoValidatorFound, "the refused validator does not exist")

	require.NoError(t, c.createFourthValidator(ctx, 2_000), "the same message with the floor is accepted")
}

// 8. A real gov proposal. x/gov executes its messages through the message
// router at EndBlock: a MsgDelegate from the gov account past a validator's cap
// fails the proposal, and the chain goes on producing blocks.
func TestGovProposalPastTheCapFailsWithoutHaltingTheChain(t *testing.T) {
	c := newTestChain(t)
	c.fundGov()
	a := c.vals[0].val
	govBalance := c.balance(c.govAddr())
	require.Equal(t, scnInt(500_000), govBalance)

	// 200,000 SCN would take A from 300,000 to 500,000, past the 400,000 cap.
	status, reason := c.runProposal(msgDelegate(c.govAddr().String(), a.String(), scnInt(200_000)))
	require.Equal(t, govv1.StatusFailed, status, "reason: %s", reason)
	require.Contains(t, reason, "would reach", "the proposal failed on the cap, not on something else")
	require.Equal(t, scnInt(validatorSelfSCN), c.tokens(c.ctx(), a))
	require.Equal(t, govBalance, c.balance(c.govAddr()))

	// The chain is alive.
	before := c.height
	c.nextBlock(time.Second)
	c.nextBlock(time.Second)
	require.Equal(t, before+2, c.app.LastBlockHeight())
}

// 8b. The control: the same machinery passes a proposal whose delegation fits,
// so the failure above is the cap's doing.
func TestGovProposalWithinTheCapPasses(t *testing.T) {
	c := newTestChain(t)
	c.fundGov()
	a := c.vals[0].val

	status, reason := c.runProposal(msgDelegate(c.govAddr().String(), a.String(), scnInt(50_000)))
	require.Equal(t, govv1.StatusPassed, status, "reason: %s", reason)
	require.Equal(t, scnInt(validatorSelfSCN+50_000), c.tokens(c.ctx(), a))
}

// runProposal submits msg as a gov proposal, has the three validators vote YES,
// steps time past the voting period and returns the proposal's final status and
// failure reason.
func (c *testChain) runProposal(msg sdk.Msg) (govv1.ProposalStatus, string) {
	c.t.Helper()
	params, err := c.app.GovKeeper.Params.Get(c.ctx())
	require.NoError(c.t, err)

	submit, err := govv1.NewMsgSubmitProposal([]sdk.Msg{msg}, params.MinDeposit, c.delegators[0].addr.String(),
		"", "S7 test proposal", "S7 test proposal", false)
	require.NoError(c.t, err)
	res := c.deliverTx(c.delegators[0], submit)
	require.Equal(c.t, uint32(0), res.Code, res.Log)

	var votes [][]byte
	for _, v := range c.vals {
		votes = append(votes, c.signedTx(v, govv1.NewMsgVote(v.addr, 1, govv1.OptionYes, "")))
	}
	vres := c.nextBlock(time.Second, votes...)
	for _, r := range vres.TxResults {
		require.Equal(c.t, uint32(0), r.Code, r.Log)
	}

	// Past the 10-second voting period: EndBlock tallies and executes.
	c.nextBlock(30 * time.Second)

	p, err := c.app.GovKeeper.Proposals.Get(c.ctx(), 1)
	require.NoError(c.t, err)
	return p.Status, p.FailedReason
}

// 9. Caps freeze, they never force-shed: with a cap below existing stake a
// delegator can still leave, while growth stays refused.
func TestRouterPathKeepsDecreasePathsOpenAboveACap(t *testing.T) {
	c := newTestChain(t)
	ctx := c.ctx()
	a := c.vals[0].val
	d := c.delegators[0]
	k := c.app.StakeengineKeeper

	require.NoError(t, c.delegate(ctx, d, a, scnInt(50_000)))

	st, err := k.CapState.Get(ctx)
	require.NoError(t, err)
	st.ValidatorCap = scnInt(100_000) // below A's 350,000
	st.GlobalCap = scnInt(500_000)    // below the 950,000 bonded
	require.NoError(t, k.CapState.Set(ctx, st))

	require.Error(t, c.delegate(ctx, d, a, scnInt(100)), "growth stays refused")

	undelegate := stakingtypes.NewMsgUndelegate(d.addr.String(), a.String(), scnCoin(10_000))
	require.NoError(t, c.deliverViaRouter(ctx, undelegate), "leaving is always open")
	require.Equal(t, scnInt(validatorSelfSCN+40_000), c.tokens(ctx, a))
}

// 10. A slash must not halt the chain. Slashing a validator slashes the
// redelegations out of it, which unbonds the destination — here an operator's
// own validator, inside its lock-up — and that unbond fires the delegation
// hooks. BeginBlock is where slashing runs, and a hook error there would halt
// the chain; so the hooks sleep in a window PreBlock opens. Both halves matter:
// without the window the same slash errors, which is why the window exists.
func TestASlashInsideTheBeginBlockWindowDoesNotError(t *testing.T) {
	c := newTestChain(t)
	ctx := c.ctx()
	source, operator := c.vals[0], c.vals[1] // operator B redelegates from A into its own validator

	require.NoError(t, c.delegate(ctx, operator, source.val, scnInt(10_000)))
	redelegate := stakingtypes.NewMsgBeginRedelegate(operator.addr.String(), source.val.String(), operator.val.String(), scnCoin(10_000))
	require.NoError(t, c.deliverViaRouter(ctx, redelegate))

	slash := func(ctx sdk.Context) error {
		val, err := c.app.StakingKeeper.GetValidator(ctx, source.val)
		require.NoError(t, err)
		consAddr, err := val.GetConsAddr()
		require.NoError(t, err)
		// The infraction was at height 1, before the redelegation (height 2).
		_, err = c.app.StakingKeeper.Slash(ctx, consAddr, 1, val.ConsensusPower(sdk.DefaultPowerReduction),
			math.LegacyNewDecWithPrec(50, 2))
		return err
	}

	t.Run("outside the window the hooks refuse it", func(t *testing.T) {
		cache, _ := ctx.CacheContext()
		require.ErrorIs(t, slash(cache), vrtypes.ErrLockupActive)
	})

	t.Run("inside the window it goes through", func(t *testing.T) {
		cache, _ := ctx.CacheContext()
		require.NoError(t, c.app.StakeengineKeeper.OpenBeginBlockWindow(cache))
		before := c.tokens(cache, operator.val)
		require.NoError(t, slash(cache))
		require.True(t, c.tokens(cache, operator.val).LT(before), "the operator's own stake really was slashed")

		// BeginBlock's last module closes the window: the hooks wake again.
		require.NoError(t, c.app.StakeengineKeeper.CloseBeginBlockWindow(cache))
		undelegate := stakingtypes.NewMsgUndelegate(operator.addr.String(), operator.val.String(), scnCoin(1_000))
		require.ErrorIs(t, c.deliverViaRouter(cache, undelegate), vrtypes.ErrLockupActive)
	})
}

// 11. Evidence cannot slash outside BeginBlock. The app registers no evidence
// handler (x/evidence is wired without a router), so a submitted Equivocation
// can never reach x/slashing: the only evidence that slashes is what CometBFT
// reports in FinalizeBlock, which runs inside the BeginBlock window.
//
// The refusal is not the clean ErrNoEvidenceHandlerExists one would expect:
// x/evidence's keeper dereferences its nil router and panics. A transaction
// recovers that into a failed result (baseapp.runTx) and x/gov and x/group
// recover it too, so nothing slashes and nothing halts — but it is a panic, not
// an error, and is reported as a finding.
func TestSubmittedEvidenceHasNoHandler(t *testing.T) {
	c := newTestChain(t)

	consAddr := sdk.ConsAddress(c.consKeys[0].PubKey().Address())
	eq := &evidencetypes.Equivocation{
		Height:           1,
		Time:             genesisTime,
		Power:            30_000_000,
		ConsensusAddress: consAddr.String(),
	}
	msg, err := evidencetypes.NewMsgSubmitEvidence(c.delegators[0].addr, eq)
	require.NoError(t, err)

	t.Run("through the message router it panics rather than slashing", func(t *testing.T) {
		require.Panics(t, func() { _ = c.deliverViaRouter(c.ctx(), msg) })
	})

	t.Run("as a transaction it fails and nothing is slashed", func(t *testing.T) {
		res := c.deliverTx(c.delegators[0], msg)
		require.NotZero(t, res.Code, "the evidence was accepted")
		require.Contains(t, res.Log, "panic")
		require.Equal(t, scnInt(validatorSelfSCN), c.tokens(c.ctx(), c.vals[0].val))
		val, err := c.app.StakingKeeper.GetValidator(c.ctx(), c.vals[0].val)
		require.NoError(t, err)
		require.False(t, val.Jailed)
	})
}

// The ante handler is the courteous front door: the same rules, refused before
// any state changes, with the same error.
func TestAnteRefusesWithTheSameErrorAndChangesNothing(t *testing.T) {
	t.Run("validator cap", func(t *testing.T) {
		c := newTestChain(t)
		d := c.delegators[0]
		balance := c.balance(d.addr)

		res := c.deliverTx(d, msgDelegate(d.addr.String(), c.vals[0].val.String(), scnInt(150_000)))
		require.Equal(t, stakeenginetypes.ErrValidatorCapReached.ABCICode(), res.Code, res.Log)
		require.Equal(t, stakeenginetypes.ErrValidatorCapReached.Codespace(), res.Codespace)
		require.Equal(t, balance, c.balance(d.addr))
		require.Equal(t, scnInt(validatorSelfSCN), c.tokens(c.ctx(), c.vals[0].val))
	})

	t.Run("self-bond floor", func(t *testing.T) {
		c := newTestChain(t)
		c.nextBlock(61 * 24 * time.Hour) // past the lock-up
		a := c.vals[0]
		floor := stakeenginetypes.EntryFloor()
		amount := scnInt(validatorSelfSCN).Sub(floor).AddRaw(1)

		res := c.deliverTx(a, stakingtypes.NewMsgUndelegate(a.addr.String(), a.val.String(),
			sdk.NewCoin(stakeenginetypes.BondDenom, amount)))
		require.Equal(t, stakeenginetypes.ErrSelfBondBelowFloor.ABCICode(), res.Code, res.Log)
		require.Equal(t, stakeenginetypes.ErrSelfBondBelowFloor.Codespace(), res.Codespace)
		require.Equal(t, scnInt(validatorSelfSCN), c.tokens(c.ctx(), a.val))
	})
}
