package app

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"
	"time"

	errorsmod "cosmossdk.io/errors"
	"cosmossdk.io/math"
	abci "github.com/cometbft/cometbft/abci/types"
	"github.com/cosmos/cosmos-sdk/codec"
	sdk "github.com/cosmos/cosmos-sdk/types"
	banktypes "github.com/cosmos/cosmos-sdk/x/bank/types"
	govv1 "github.com/cosmos/cosmos-sdk/x/gov/types/v1"
	stakingtypes "github.com/cosmos/cosmos-sdk/x/staking/types"
	"github.com/stretchr/testify/require"

	compliancetypes "github.com/saicon-network/saicon/chain/x/compliance/types"
	entitykeeper "github.com/saicon-network/saicon/chain/x/entityregistry/keeper"
	entitytypes "github.com/saicon-network/saicon/chain/x/entityregistry/types"
	stakeenginetypes "github.com/saicon-network/saicon/chain/x/stakeengine/types"
	vrtypes "github.com/saicon-network/saicon/chain/x/validatorregistry/types"
)

// The entity registry on the full app (S8 W1, spec §21 "App level"): the
// activation gate, the seat cap, the freeze, the 5% entity cap on every stake
// path, V-12 throughout, and the gas of the new transactions. Everything here
// goes through the real ante handler, message router, staking hooks and
// EndBlockers; the unit tests of the modules stand behind stubs.

// fundAccount sends n SCN from one of the harness delegators to a new account in
// a real block; the bank creates the account on receipt, so it can sign next.
func (c *testChain) fundAccount(from acct, to sdk.AccAddress, n int64) {
	c.t.Helper()
	res := c.deliverTx(from, banktypes.NewMsgSend(from.addr, to, sdk.NewCoins(scnCoin(n))))
	require.Equal(c.t, uint32(0), res.Code, res.Log)
}

// createValidatorBlock creates a validator for a, with selfBondSCN of its own, in
// a signed transaction (the ante runs), using newConsKey(consName). It returns
// the whole block's response: the validator bonds in that block's EndBlock, so
// the events of the bond hook are in the block's events, not the transaction's.
func (c *testChain) createValidatorBlock(a acct, consName string, selfBondSCN int64) *abci.ResponseFinalizeBlock {
	c.t.Helper()
	msg, err := stakingtypes.NewMsgCreateValidator(
		a.val.String(),
		newConsKey(consName).PubKey(),
		scnCoin(selfBondSCN),
		stakingtypes.Description{Moniker: consName},
		stakingtypes.NewCommissionRates(math.LegacyZeroDec(), math.LegacyOneDec(), math.LegacyOneDec()),
		math.OneInt(),
	)
	require.NoError(c.t, err)
	res := c.nextBlock(time.Second, c.signedTx(a, msg))
	require.Len(c.t, res.TxResults, 1)
	return res
}

// createValidatorTx is createValidatorBlock for callers that want the
// transaction's result.
func (c *testChain) createValidatorTx(a acct, consName string, selfBondSCN int64) *abci.ExecTxResult {
	c.t.Helper()
	return c.createValidatorBlock(a, consName, selfBondSCN).TxResults[0]
}

// attestMsg builds a MsgAttestEntity for the attestor.
func attestMsg(att acct, id string, validators ...sdk.ValAddress) *entitytypes.MsgAttestEntity {
	vals := make([]string, len(validators))
	for i, v := range validators {
		vals[i] = v.String()
	}
	return &entitytypes.MsgAttestEntity{
		Attestor:        att.addr.String(),
		EntityId:        id,
		DisplayName:     "Entity " + id,
		AttestationHash: strings.Repeat("0a", 32),
		Validators:      vals,
	}
}

// attest sends the attestation in a block of its own and requires it to succeed.
func (c *testChain) attest(att acct, id string, validators ...sdk.ValAddress) *abci.ExecTxResult {
	c.t.Helper()
	res := c.deliverTx(att, attestMsg(att, id, validators...))
	require.Equal(c.t, uint32(0), res.Code, res.Log)
	return res
}

// revoke revokes an entity as att in a block of its own and requires it to succeed.
func (c *testChain) revoke(att acct, id string) *abci.ExecTxResult {
	c.t.Helper()
	res := c.deliverTx(att, &entitytypes.MsgRevokeEntity{Authority: att.addr.String(), EntityId: id, Reason: "test"})
	require.Equal(c.t, uint32(0), res.Code, res.Log)
	return res
}

// regStatus is the validator's registry status.
func (c *testChain) regStatus(v sdk.ValAddress) vrtypes.Status {
	c.t.Helper()
	st, found, err := c.app.ValidatorregistryKeeper.GetValidatorStatus(c.ctx(), v)
	require.NoError(c.t, err)
	require.True(c.t, found, "no registry record for %s", v)
	return st
}

// where is the entity registry's answer for a validator (the ValidatorEntity query).
func (c *testChain) where(v sdk.ValAddress) *entitytypes.QueryValidatorEntityResponse {
	c.t.Helper()
	res, err := entitykeeper.NewQueryServerImpl(c.app.EntityregistryKeeper).ValidatorEntity(c.ctx(),
		&entitytypes.QueryValidatorEntityRequest{Validator: v.String()})
	require.NoError(c.t, err)
	return res
}

// requireRegistry requires V-12. It is asserted after every step of a sequence
// that must keep the registry whole; every nextBlock runs it in EndBlock too, so
// a break would panic there first.
func (c *testChain) requireRegistry() {
	c.t.Helper()
	msg, broken := c.app.EntityregistryKeeper.RegistryInvariant(c.ctx())
	require.False(c.t, broken, msg)
}

// requireEntityLimits requires V-13. Only for sequences that never put an entity
// over a limit: V-13 REPORTS an over-cap entity, which a base move, a merge or a
// governance decrease legitimately produces (freeze, never shed), so after such
// a sequence the freeze is what is asserted instead.
func (c *testChain) requireEntityLimits() {
	c.t.Helper()
	msg, broken := c.app.StakeengineKeeper.EntityLimitsInvariant(c.ctx())
	require.False(c.t, broken, msg)
}

func (c *testChain) requireInvariants() {
	c.t.Helper()
	c.requireRegistry()
	c.requireEntityLimits()
}

// requireTxError requires a transaction result to carry the registered error.
func requireTxError(t *testing.T, res *abci.ExecTxResult, want *errorsmod.Error) {
	t.Helper()
	require.Equal(t, want.ABCICode(), res.Code, "want %q, got code %d: %s", want.Error(), res.Code, res.Log)
	require.Equal(t, want.Codespace(), res.Codespace, res.Log)
}

// standing is the stake engine's entity standing for a validator.
func (c *testChain) standing(v sdk.ValAddress) stakeenginetypes.EntityStanding {
	c.t.Helper()
	s, err := c.app.StakeengineKeeper.EntityStanding(c.ctx(), v)
	require.NoError(c.t, err)
	return s
}

// ---------------------------------------------------------------------------

// 1. The gate: a validator created by transaction is held at BONDING until an
// attestor vouches for it, and the held bond hook does not halt the chain.
func TestANewValidatorIsHeldUntilAttested(t *testing.T) {
	att := newAcct("attestor")
	c := newTestChain(t, withAttestor(att))
	d := c.fourth.val

	blk := c.createValidatorBlock(c.fourth, "fourth", 2_000)
	require.Equal(t, uint32(0), blk.TxResults[0].Code, blk.TxResults[0].Log)

	// x/staking bonded D in this block's EndBlock; the registry did not activate it.
	sv, err := c.app.StakingKeeper.GetValidator(c.ctx(), d)
	require.NoError(t, err)
	require.True(t, sv.IsBonded(), "staking bonds the validator by stake, attested or not")
	require.Equal(t, vrtypes.Status_STATUS_BONDING, c.regStatus(d))
	w := c.where(d)
	require.True(t, w.Pending)
	require.False(t, w.Attested)
	held, ok := eventAttr(blk.Events, entitytypes.EventTypeActivationHeld, entitytypes.AttributeKeyValidator)
	require.True(t, ok, "no %s event in the block", entitytypes.EventTypeActivationHeld)
	require.Equal(t, d.String(), held)
	c.requireRegistry()

	// Held validators stay held through empty blocks.
	c.nextBlock(time.Second)
	require.Equal(t, vrtypes.Status_STATUS_BONDING, c.regStatus(d))

	// The attestor vouches; the entity registry promotes D at the end of that block.
	res := c.attest(att, "entity-d", d)
	require.Equal(t, uint32(0), res.Code, res.Log)
	require.Equal(t, vrtypes.Status_STATUS_ACTIVE, c.regStatus(d))
	w = c.where(d)
	require.False(t, w.Pending)
	require.True(t, w.Attested)
	require.Equal(t, "entity-d", w.EntityId)
	c.requireInvariants()
}

// 2. The seat cap: the third member of an entity waits, and takes a seat when one
// is freed.
func TestTheThirdSeatWaitsAndTakesAFreedSeat(t *testing.T) {
	att := newAcct("attestor")
	c := newTestChain(t, withAttestor(att))
	a, d := c.vals[0].val, c.fourth.val
	fifth := newAcct("fifth")
	c.fundAccount(c.delegators[3], fifth.addr, 20_000)
	e := fifth.val

	require.Equal(t, uint32(0), c.createValidatorTx(c.fourth, "fourth", 2_000).Code)
	c.attest(att, "entity-a", a, d)
	require.Equal(t, vrtypes.Status_STATUS_ACTIVE, c.regStatus(d), "two seats: A and D")

	require.Equal(t, uint32(0), c.createValidatorTx(fifth, "fifth", 2_000).Code)
	c.attest(att, "entity-a", a, d, e)
	require.Equal(t, vrtypes.Status_STATUS_BONDING, c.regStatus(e), "the third member waits")
	require.True(t, c.where(e).Pending)
	require.Equal(t, vrtypes.Status_STATUS_ACTIVE, c.regStatus(a))
	require.Equal(t, vrtypes.Status_STATUS_ACTIVE, c.regStatus(d))
	// The gate's own answer names the reason (the held event of the attest block
	// is the registry's, of the earlier refusal; the gate is asked again here, in a
	// cache context so nothing is written).
	cache, _ := c.ctx().CacheContext()
	err := c.app.EntityregistryKeeper.MayActivate(cache, e)
	require.ErrorIs(t, err, vrtypes.ErrActivationHeld)
	require.Contains(t, err.Error(), "already holds 2 active seats")
	c.nextBlock(time.Second)
	require.Equal(t, vrtypes.Status_STATUS_BONDING, c.regStatus(e), "still waiting")
	c.requireInvariants()

	// D's operator leaves, after its 60-day lock-up: D leaves the bonded set, its
	// seat frees, and E takes it in the block's EndBlock.
	c.nextBlock(61 * 24 * time.Hour)
	res := c.deliverTx(c.fourth, stakingtypes.NewMsgUndelegate(c.fourth.addr.String(), d.String(), scnCoin(2_000)))
	require.Equal(t, uint32(0), res.Code, res.Log)
	c.nextBlock(time.Second)

	dv, err := c.app.StakingKeeper.GetValidator(c.ctx(), d)
	require.NoError(t, err)
	require.False(t, dv.IsBonded(), "D left the bonded set")
	require.NotEqual(t, vrtypes.Status_STATUS_ACTIVE, c.regStatus(d))
	require.Equal(t, vrtypes.Status_STATUS_ACTIVE, c.regStatus(e), "E took the seat D freed")
	require.False(t, c.where(e).Pending)
	require.Equal(t, vrtypes.Status_STATUS_ACTIVE, c.regStatus(a))
	c.requireInvariants()
}

// 3. MsgClearFlag restores a quarantined validator to ACTIVE, which is a path
// into ACTIVE: it meets the gate. A validator no entity vouches for is refused,
// and the proposal ends FAILED without halting the chain.
func TestClearFlagCannotRestoreAnUnattestedValidator(t *testing.T) {
	c := newTestChain(t)
	b := c.vals[1].val

	// No existing app test submits compliance evidence, so quarantine B directly.
	require.NoError(t, c.app.ComplianceKeeper.SetCompliance(c.ctx(), b, false,
		compliancetypes.ComplianceEventType_COMPLIANCE_EVENT_TYPE_EXTERNAL_FLAG, "test"))
	c.nextBlock(time.Second)
	require.Equal(t, vrtypes.Status_STATUS_QUARANTINED, c.regStatus(b))

	// Revoke B's entity as the gov authority. B is not ACTIVE, so no seat freezes.
	require.NoError(t, c.deliverViaRouter(c.ctx(), &entitytypes.MsgRevokeEntity{
		Authority: c.govAddr().String(), EntityId: "entity-b", Reason: "test"}))
	c.nextBlock(time.Second)
	require.Nil(t, c.where(b).Frozen)
	require.False(t, c.where(b).Attested)
	require.Equal(t, vrtypes.Status_STATUS_QUARANTINED, c.regStatus(b))

	status, reason := c.runProposal(&compliancetypes.MsgClearFlag{
		Authority: c.govAddr().String(), ValidatorAddress: b.String(), Reason: "restore"})
	require.Equal(t, govv1.StatusFailed, status, "reason: %s", reason)
	require.Contains(t, reason, "not attested")
	require.Equal(t, vrtypes.Status_STATUS_QUARANTINED, c.regStatus(b), "the restore was refused, not half done")

	before := c.app.LastBlockHeight()
	c.nextBlock(time.Second)
	require.Equal(t, before+1, c.app.LastBlockHeight(), "the chain is alive")
	c.requireRegistry()
}

// 4. Freeze: a revoked entity's ACTIVE validator keeps its seat and takes no new
// stake, on every path; a delegator can still leave; the router sends nothing to it.
func TestRevokeFreezesStakeIntake(t *testing.T) {
	att := newAcct("attestor")
	c := newTestChain(t, withAttestor(att))
	cv := c.vals[2].val
	old, other := c.delegators[0], c.delegators[1]

	// A delegator that already holds stake in C, so it can undelegate later.
	res := c.deliverTx(old, msgDelegate(old.addr.String(), cv.String(), scnInt(10_000)))
	require.Equal(t, uint32(0), res.Code, res.Log)

	c.revoke(att, "entity-c")
	require.Equal(t, vrtypes.Status_STATUS_ACTIVE, c.regStatus(cv), "freeze, never shed: the seat stays")
	w := c.where(cv)
	require.NotNil(t, w.Frozen)
	require.Equal(t, "entity-c", w.Frozen.FormerEntityId)
	require.False(t, w.Attested)
	c.nextBlock(time.Second)
	require.NotNil(t, c.where(cv).Frozen, "the frozen row survives while the validator is ACTIVE")
	c.requireRegistry()

	// A transaction: the ante refuses it.
	tokens := c.tokens(c.ctx(), cv)
	res = c.deliverTx(other, msgDelegate(other.addr.String(), cv.String(), scnInt(1_000)))
	requireTxError(t, res, stakeenginetypes.ErrEntityFrozen)

	// The gov / group / ICA path: no ante, the staking hook refuses it.
	err := c.delegate(c.ctx(), other, cv, scnInt(1_000))
	require.ErrorIs(t, err, stakeenginetypes.ErrEntityFrozen)
	require.Equal(t, tokens, c.tokens(c.ctx(), cv))

	// Leaving is always open.
	res = c.deliverTx(old, stakingtypes.NewMsgUndelegate(old.addr.String(), cv.String(), scnCoin(5_000)))
	require.Equal(t, uint32(0), res.Code, res.Log)
	require.Equal(t, tokens.Sub(scnInt(5_000)), c.tokens(c.ctx(), cv))

	// The router skips a frozen primary: the whole amount goes elsewhere.
	res = c.deliverTx(other, c.routed(other, cv, 10_000))
	require.Equal(t, uint32(0), res.Code, res.Log)
	require.True(t, c.staked(other.addr, cv).IsZero(), "C got a leg")
	placed := c.staked(other.addr, c.vals[0].val).Add(c.staked(other.addr, c.vals[1].val))
	require.Equal(t, scnInt(10_000), placed, "the amount went to the other validators")
	c.requireInvariants()
}

// ---------------------------------------------------------------------------
// 5. The 5% entity cap on every stake path.
//
// The entity cap is 400,000 SCN here (the floor: 5% of the ~900,000 EWMA base is
// 45,000). Entity "entity-a" is A (300,000) and D (2,000), so it has 98,000 SCN
// of room while D alone, under the 400,000 per-validator cap, could take 398,000:
// the entity cap is the only rule that can refuse 100,000 SCN into D.

// entityAChain is a chain whose entity-a holds A and D, D ACTIVE with a
// 2,000 SCN self-bond. It returns the attestor.
func entityAChain(t *testing.T, mutate ...func(codec.Codec, map[string]json.RawMessage)) (*testChain, acct) {
	t.Helper()
	att := newAcct("attestor")
	c := newTestChain(t, append([]func(codec.Codec, map[string]json.RawMessage){withAttestor(att)}, mutate...)...)
	require.Equal(t, uint32(0), c.createValidatorTx(c.fourth, "fourth", 2_000).Code)
	c.attest(att, "entity-a", c.vals[0].val, c.fourth.val)
	require.Equal(t, vrtypes.Status_STATUS_ACTIVE, c.regStatus(c.fourth.val))

	st, err := c.app.StakeengineKeeper.CapState.Get(c.ctx())
	require.NoError(t, err)
	require.Equal(t, scnInt(400_000), st.EntityCap)
	s := c.standing(c.fourth.val)
	require.Equal(t, "entity-a", s.EntityID)
	require.False(t, s.Frozen)
	require.Equal(t, scnInt(validatorSelfSCN+2_000), s.Tokens)
	require.Equal(t, scnInt(98_000), s.Cap.Sub(s.Tokens), "room in the entity")
	// The per-validator cap alone would allow 100,000 SCN into D.
	require.True(t, c.tokens(c.ctx(), c.fourth.val).Add(scnInt(100_000)).LTE(st.ValidatorCap))
	return c, att
}

// requireEntityWithinCap requires that the entity is within its cap and its limits.
func (c *testChain) requireEntityWithinCap() {
	c.t.Helper()
	s := c.standing(c.fourth.val)
	require.True(c.t, s.Tokens.LTE(s.Cap), "entity holds %s, cap %s", s.Tokens, s.Cap)
	c.requireInvariants()
}

func TestTheEntityCapHoldsOnEveryPath(t *testing.T) {
	over := scnInt(100_000)

	t.Run("98,000 SCN fits", func(t *testing.T) {
		c, _ := entityAChain(t)
		d := c.delegators[0]
		res := c.deliverTx(d, msgDelegate(d.addr.String(), c.fourth.val.String(), scnInt(98_000)))
		require.Equal(t, uint32(0), res.Code, res.Log)
		s := c.standing(c.fourth.val)
		require.Equal(t, s.Cap, s.Tokens, "the entity is exactly at its cap")
		c.requireEntityWithinCap()

		// ...and not one SCN more (100 SCN is the minimum delegation), into either member.
		res = c.deliverTx(d, msgDelegate(d.addr.String(), c.fourth.val.String(), scnInt(100)))
		requireTxError(t, res, stakeenginetypes.ErrEntityCapReached)
		res = c.deliverTx(d, msgDelegate(d.addr.String(), c.vals[0].val.String(), scnInt(100)))
		requireTxError(t, res, stakeenginetypes.ErrEntityCapReached)
	})

	t.Run("MsgDelegate in a transaction", func(t *testing.T) {
		c, _ := entityAChain(t)
		d := c.delegators[0]
		balance := c.balance(d.addr)
		res := c.deliverTx(d, msgDelegate(d.addr.String(), c.fourth.val.String(), over))
		requireTxError(t, res, stakeenginetypes.ErrEntityCapReached)
		require.Equal(t, balance, c.balance(d.addr))
		c.requireEntityWithinCap()
	})

	t.Run("MsgDelegate through the message router", func(t *testing.T) {
		c, _ := entityAChain(t)
		before := c.tokens(c.ctx(), c.fourth.val)
		err := c.delegate(c.ctx(), c.delegators[0], c.fourth.val, over)
		require.ErrorIs(t, err, stakeenginetypes.ErrEntityCapReached)
		require.Equal(t, before, c.tokens(c.ctx(), c.fourth.val))
		c.requireEntityWithinCap()
	})

	t.Run("MsgDelegate inside a gov proposal", func(t *testing.T) {
		c, _ := entityAChain(t)
		c.fundGov()
		govBalance := c.balance(c.govAddr())
		before := c.tokens(c.ctx(), c.fourth.val)
		status, reason := c.runProposal(msgDelegate(c.govAddr().String(), c.fourth.val.String(), over))
		require.Equal(t, govv1.StatusFailed, status, "reason: %s", reason)
		require.Contains(t, reason, stakeenginetypes.ErrEntityCapReached.Error())
		require.Equal(t, before, c.tokens(c.ctx(), c.fourth.val))
		require.Equal(t, govBalance, c.balance(c.govAddr()))

		height := c.app.LastBlockHeight()
		c.nextBlock(time.Second)
		c.nextBlock(time.Second)
		require.Equal(t, height+2, c.app.LastBlockHeight(), "the chain is alive")
		c.requireEntityWithinCap()
	})

	// Redelegation: B is at its own 400,000 cap (entity-b the same), so the 100,000
	// SCN moving into D is growth for entity-a, which has room for 98,000.
	redelegationChain := func(t *testing.T) (*testChain, *stakingtypes.MsgBeginRedelegate) {
		c, _ := entityAChain(t)
		d := c.delegators[2]
		res := c.deliverTx(d, msgDelegate(d.addr.String(), c.vals[1].val.String(), over))
		require.Equal(t, uint32(0), res.Code, res.Log)
		require.Equal(t, scnInt(400_000), c.tokens(c.ctx(), c.vals[1].val))
		return c, stakingtypes.NewMsgBeginRedelegate(d.addr.String(), c.vals[1].val.String(), c.fourth.val.String(), scnCoin(100_000))
	}

	t.Run("redelegation into the entity, in a transaction", func(t *testing.T) {
		c, msg := redelegationChain(t)
		res := c.deliverTx(c.delegators[2], msg)
		requireTxError(t, res, stakeenginetypes.ErrEntityCapReached)
		require.Equal(t, scnInt(400_000), c.tokens(c.ctx(), c.vals[1].val))
		c.requireEntityWithinCap()
	})

	t.Run("redelegation into the entity, through the message router", func(t *testing.T) {
		c, msg := redelegationChain(t)
		err := c.deliverViaRouter(c.ctx(), msg)
		require.ErrorIs(t, err, stakeenginetypes.ErrEntityCapReached)
		require.Equal(t, scnInt(400_000), c.tokens(c.ctx(), c.vals[1].val))
		c.requireEntityWithinCap()
	})

	// The operator's top-up: no exemption for the validator's own stake.
	topUpChain := func(t *testing.T) *testChain {
		c, _ := entityAChain(t)
		c.fundAccount(c.delegators[3], c.fourth.addr, 100_000)
		return c
	}

	t.Run("operator top-up of its own bond, in a transaction", func(t *testing.T) {
		c := topUpChain(t)
		res := c.deliverTx(c.fourth, msgDelegate(c.fourth.addr.String(), c.fourth.val.String(), over))
		requireTxError(t, res, stakeenginetypes.ErrEntityCapReached)
		c.requireEntityWithinCap()
	})

	t.Run("operator top-up of its own bond, through the message router", func(t *testing.T) {
		c := topUpChain(t)
		err := c.delegate(c.ctx(), c.fourth, c.fourth.val, over)
		require.ErrorIs(t, err, stakeenginetypes.ErrEntityCapReached)
		c.requireEntityWithinCap()
	})

	t.Run("MsgDelegateRouted with the entity's own validator as primary", func(t *testing.T) {
		c, _ := entityAChain(t)
		d := c.delegators[0]
		res := c.deliverTx(d, c.routed(d, c.fourth.val, 100_000))
		require.Equal(t, uint32(0), res.Code, res.Log)

		require.Equal(t, scnInt(98_000), c.staked(d.addr, c.fourth.val), "D's leg is the entity's room")
		require.True(t, c.staked(d.addr, c.vals[0].val).IsZero(), "A shares D's entity room: no leg")
		rest := c.staked(d.addr, c.vals[1].val).Add(c.staked(d.addr, c.vals[2].val))
		require.Equal(t, scnInt(2_000), rest, "the rest went to B and C")
		s := c.standing(c.fourth.val)
		require.Equal(t, s.Cap, s.Tokens)
		c.requireEntityWithinCap()
	})

	t.Run("queue fill", func(t *testing.T) {
		// The global cap at 5% of max supply: 1,050,000 SCN against 902,000 bonded.
		c, _ := entityAChain(t, fivePercent)
		filler, queuer := c.delegators[0], c.delegators[1]
		k := c.app.StakeengineKeeper
		dv := c.fourth.val

		// Fill the global cap: B to its 400,000 and C up to the headroom's end.
		res := c.deliverTx(filler, msgDelegate(filler.addr.String(), c.vals[1].val.String(), scnInt(100_000)))
		require.Equal(t, uint32(0), res.Code, res.Log)
		res = c.deliverTx(filler, msgDelegate(filler.addr.String(), c.vals[2].val.String(), scnInt(48_000)))
		require.Equal(t, uint32(0), res.Code, res.Log)
		headroom, err := k.Headroom(c.ctx())
		require.NoError(t, err)
		require.True(t, headroom.IsZero(), "headroom %s", headroom)

		// 100,000 SCN for D waits in the queue: no room anywhere.
		res = c.deliverTx(queuer, &stakeenginetypes.MsgEnqueueDelegation{
			Delegator: queuer.addr.String(), Validator: dv.String(), Amount: scnCoin(100_000)})
		require.Equal(t, uint32(0), res.Code, res.Log)
		c.nextBlock(time.Second)
		require.Equal(t, scnInt(100_000), sumQueued(c.queue()))
		require.True(t, c.staked(queuer.addr, dv).IsZero())

		// The filler leaves: 148,000 SCN of global headroom opens in one block. The
		// per-validator cap of D alone (398,000 of room) and the headroom would
		// take all 100,000; the entity's room takes 98,000.
		res = c.deliverTx(filler,
			stakingtypes.NewMsgUndelegate(filler.addr.String(), c.vals[1].val.String(), scnCoin(100_000)),
			stakingtypes.NewMsgUndelegate(filler.addr.String(), c.vals[2].val.String(), scnCoin(48_000)))
		require.Equal(t, uint32(0), res.Code, res.Log)
		headroom, err = k.Headroom(c.ctx())
		require.NoError(t, err)
		require.True(t, headroom.IsPositive(), "the global headroom was not what limited the fill: %s left", headroom)

		require.Equal(t, scnInt(98_000), c.staked(queuer.addr, dv), "only the entity's room was filled")
		q := c.queue()
		require.Len(t, q, 1)
		require.Equal(t, scnInt(2_000), q[0].amount, "the rest still waits")
		for range 3 {
			c.nextBlock(time.Second)
		}
		require.Equal(t, scnInt(2_000), sumQueued(c.queue()), "and keeps waiting: the entity has no room")
		require.Equal(t, scnInt(98_000), c.staked(queuer.addr, dv))
		msg, broken := k.EscrowInvariant(c.ctx())
		require.False(t, broken, msg)
		c.requireEntityWithinCap()
	})
}

// Requirement 2: stake moves between two members of one entity at its cap even
// when the source validator has been slashed (exchange rate below 1). It is not
// growth for the entity, in the ante or in the hook, and the rounding of the
// tokens-per-share conversion must not turn it into a false refusal.
func TestRedelegationBetweenMembersAtTheCapWithASlashedSource(t *testing.T) {
	c, _ := entityAChain(t)
	a, d := c.vals[0].val, c.fourth.val

	// Slash A by 10%: its shares are worth 0.9 tokens each from now on. The
	// infraction is this block's, so no redelegation or unbonding is touched.
	ctx := c.ctx()
	av, err := c.app.StakingKeeper.GetValidator(ctx, a)
	require.NoError(t, err)
	consAddr, err := av.GetConsAddr()
	require.NoError(t, err)
	_, err = c.app.StakingKeeper.Slash(ctx, consAddr, ctx.BlockHeight(), av.ConsensusPower(sdk.DefaultPowerReduction),
		math.LegacyNewDecWithPrec(10, 2))
	require.NoError(t, err)
	av, err = c.app.StakingKeeper.GetValidator(ctx, a)
	require.NoError(t, err)
	require.Equal(t, scnInt(270_000), av.Tokens)
	require.True(t, av.DelegatorShares.GT(math.LegacyNewDecFromInt(av.Tokens)), "exchange rate below 1")

	// Bring the entity back to exactly its cap: 272,000 held, 128,000 of room.
	d0, d1 := c.delegators[0], c.delegators[1]
	res := c.deliverTx(d0, msgDelegate(d0.addr.String(), a.String(), scnInt(100_000)))
	require.Equal(t, uint32(0), res.Code, res.Log)
	res = c.deliverTx(d1, msgDelegate(d1.addr.String(), d.String(), scnInt(28_000)))
	require.Equal(t, uint32(0), res.Code, res.Log)
	s := c.standing(d)
	require.Equal(t, s.Cap, s.Tokens, "the entity is exactly at its cap")
	res = c.deliverTx(d1, msgDelegate(d1.addr.String(), d.String(), scnInt(100)))
	requireTxError(t, res, stakeenginetypes.ErrEntityCapReached)

	// A control: the same movement from another entity's validator is growth and is refused.
	d2 := c.delegators[2]
	res = c.deliverTx(d2, msgDelegate(d2.addr.String(), c.vals[1].val.String(), scnInt(50_000)))
	require.Equal(t, uint32(0), res.Code, res.Log)
	res = c.deliverTx(d2, stakingtypes.NewMsgBeginRedelegate(d2.addr.String(), c.vals[1].val.String(), d.String(), scnCoin(50_000)))
	requireTxError(t, res, stakeenginetypes.ErrEntityCapReached)

	redelegate := func(from acct, src, dst sdk.ValAddress, nscn math.Int) stakingtypes.MsgBeginRedelegate {
		return *stakingtypes.NewMsgBeginRedelegate(from.addr.String(), src.String(), dst.String(),
			sdk.NewCoin(stakeenginetypes.BondDenom, nscn))
	}

	// Out of the slashed A into D, in a transaction (the ante) — and with amounts
	// that do not divide evenly at the 0.9 rate.
	for i, nscn := range []math.Int{
		scnInt(50_000),
		scnInt(7_777).AddRaw(12_345_678),
		math.NewInt(1),
		math.NewInt(3),
	} {
		msg := redelegate(d0, a, d, nscn)
		res = c.deliverTx(d0, &msg)
		require.Equal(t, uint32(0), res.Code, "tx %d (%s nscn): %s", i, nscn, res.Log)
	}
	// Into the slashed A out of D, through the message router (the hook alone).
	for i, nscn := range []math.Int{scnInt(10_000), scnInt(3_333).AddRaw(33_333_333), math.NewInt(7)} {
		msg := redelegate(d1, d, a, nscn)
		require.NoError(t, c.deliverViaRouter(c.ctx(), &msg), "router %d (%s nscn)", i, nscn)
	}
	// Both, in one transaction.
	m1, m2 := redelegate(d0, a, d, scnInt(1_000).AddRaw(1)), redelegate(d0, a, d, scnInt(2_000).AddRaw(2))
	res = c.deliverTx(d0, &m1, &m2)
	require.Equal(t, uint32(0), res.Code, res.Log)

	// Redelegation between members never grows the entity; the cap still holds.
	s = c.standing(d)
	require.True(t, s.Tokens.LTE(s.Cap), "entity holds %s, cap %s", s.Tokens, s.Cap)
	require.False(t, s.Frozen)
	c.requireInvariants()

	// The exemption is what matters once the entity is OVER its cap (a governance
	// decrease; freeze, never shed): it takes no new stake, yet its members may
	// still move stake among themselves. Below the cap the hook would pass these
	// anyway, because the source's tokens have fallen before the destination's grow.
	// V-13 reports the over-cap entity by design, so it is not asserted here.
	ctx = c.ctx()
	cs, err := c.app.StakeengineKeeper.CapState.Get(ctx)
	require.NoError(t, err)
	cs.EntityCap = scnInt(350_000)
	require.NoError(t, c.app.StakeengineKeeper.CapState.Set(ctx, cs))
	s = c.standing(d)
	require.True(t, s.Tokens.GT(s.Cap), "the entity is over its lowered cap")

	res = c.deliverTx(d1, msgDelegate(d1.addr.String(), d.String(), scnInt(100)))
	requireTxError(t, res, stakeenginetypes.ErrEntityCapReached)
	err = c.delegate(c.ctx(), d1, d, scnInt(100))
	require.ErrorIs(t, err, stakeenginetypes.ErrEntityCapReached)

	msg := redelegate(d0, a, d, scnInt(5_000).AddRaw(7))
	res = c.deliverTx(d0, &msg)
	require.Equal(t, uint32(0), res.Code, res.Log)
	msg = redelegate(d1, d, a, scnInt(1_000).AddRaw(3))
	require.NoError(t, c.deliverViaRouter(c.ctx(), &msg), "the hook alone lets a member-to-member move through")
	res = c.deliverTx(d2, stakingtypes.NewMsgBeginRedelegate(d2.addr.String(), c.vals[1].val.String(), d.String(), scnCoin(100)))
	requireTxError(t, res, stakeenginetypes.ErrEntityCapReached)
	c.requireRegistry()
}

// 6. V-12 holds through every step of a sequence that creates, attests, revokes,
// re-attests, merges and removes, and V-13 (nothing here goes over a limit) with it.
func TestV12HoldsThroughAllOfIt(t *testing.T) {
	att := newAcct("attestor")
	c := newTestChain(t, withAttestor(att))
	a, d := c.vals[0].val, c.fourth.val
	fifth := newAcct("fifth")
	c.fundAccount(c.delegators[3], fifth.addr, 20_000)
	e := fifth.val
	c.requireInvariants()

	// (i) create D: held.
	require.Equal(t, uint32(0), c.createValidatorTx(c.fourth, "fourth", 2_000).Code)
	require.Equal(t, vrtypes.Status_STATUS_BONDING, c.regStatus(d))
	c.requireInvariants()

	// (ii) attest entity-d [D]: ACTIVE.
	c.attest(att, "entity-d", d)
	require.Equal(t, vrtypes.Status_STATUS_ACTIVE, c.regStatus(d))
	c.requireInvariants()

	// (iii) revoke entity-d: D is frozen, still ACTIVE.
	c.revoke(att, "entity-d")
	require.Equal(t, vrtypes.Status_STATUS_ACTIVE, c.regStatus(d))
	require.NotNil(t, c.where(d).Frozen)
	c.requireInvariants()

	// (iv) attest entity-a [A, D]: allowed, because the revocation deleted D's
	// member row (J8 refuses only a member of another attested entity). D's frozen
	// seat is cleared, and entity-a holds two seats.
	c.attest(att, "entity-a", a, d)
	require.Nil(t, c.where(d).Frozen, "joining an entity clears the frozen seat")
	require.True(t, c.where(d).Attested)
	seats, err := c.app.EntityregistryKeeper.ActiveSeats(c.ctx(), "entity-a")
	require.NoError(t, err)
	require.Equal(t, uint32(2), seats)
	c.requireInvariants()

	// (v) create E and attest entity-a [A, D, E]: E is held, the entity stays at two seats.
	require.Equal(t, uint32(0), c.createValidatorTx(fifth, "fifth", 2_000).Code)
	c.requireInvariants()
	c.attest(att, "entity-a", a, d, e)
	require.Equal(t, vrtypes.Status_STATUS_BONDING, c.regStatus(e))
	require.True(t, c.where(e).Pending)
	c.requireInvariants()

	// (vi) re-attest entity-a [A, E]: D is removed while ACTIVE, so its seat is
	// frozen — and still charged to entity-a while D is ACTIVE, so E stays held:
	// the entity's two seats are A and the frozen D.
	c.attest(att, "entity-a", a, e)
	require.Equal(t, vrtypes.Status_STATUS_ACTIVE, c.regStatus(d), "freeze, never shed")
	require.NotNil(t, c.where(d).Frozen)
	require.Equal(t, vrtypes.Status_STATUS_BONDING, c.regStatus(e), "D's frozen seat still counts for entity-a")
	require.True(t, c.where(e).Pending)
	seats, err = c.app.EntityregistryKeeper.ActiveSeats(c.ctx(), "entity-a")
	require.NoError(t, err)
	require.Equal(t, uint32(2), seats)
	c.requireInvariants()

	// Blocks after it all: the frozen seat is kept, and charged, while D is ACTIVE.
	c.nextBlock(time.Second)
	c.nextBlock(time.Second)
	require.NotNil(t, c.where(d).Frozen)
	require.Equal(t, vrtypes.Status_STATUS_BONDING, c.regStatus(e))
	c.requireInvariants()
}

// 7. Gas, measured through deliverTx (limit 3,000,000). The bounds are spec §19's;
// the numbers are logged for the spec's implementation notes.
func TestGasOfTheNewTransactions(t *testing.T) {
	att := newAcct("attestor")
	c := newTestChain(t, withAttestor(att))
	a, d := c.vals[0].val, c.fourth.val

	check := func(name string, res *abci.ExecTxResult, bound int64) {
		t.Helper()
		require.Equal(t, uint32(0), res.Code, "%s: %s", name, res.Log)
		t.Logf("GAS %-58s used %9d  (bound < %d)", name, res.GasUsed, bound)
		require.Less(t, res.GasUsed, bound, name)
	}

	// MsgAttestEntity, one validator.
	check("MsgAttestEntity, 1 validator (re-attest entity-a)",
		c.deliverTx(att, attestMsg(att, "entity-a", a)), 200_000)

	// MsgCreateValidator with the 2,000 SCN entry self-bond. The bond hook (and
	// with it the activation gate) runs in EndBlock, outside the transaction's gas.
	check("MsgCreateValidator, 2,000 SCN self-bond (D)",
		c.createValidatorTx(c.fourth, "fourth", 2_000), 300_000)

	// MsgAttestEntity, eight members: D and seven that need not exist.
	eight := []sdk.ValAddress{d}
	for i := range 7 {
		eight = append(eight, sdk.ValAddress(bytes.Repeat([]byte{byte(0x60 + i)}, 20)))
	}
	check("MsgAttestEntity, 8 validators (gas-eight)",
		c.deliverTx(att, attestMsg(att, "gas-eight", eight...)), 300_000)
	require.Equal(t, vrtypes.Status_STATUS_ACTIVE, c.regStatus(d))

	// MsgDelegate into a one-member entity, then into the eight-member one.
	del := c.delegators[0]
	check("MsgDelegate 1,000 SCN into a one-member entity (A)",
		c.deliverTx(del, msgDelegate(del.addr.String(), a.String(), scnInt(1_000))), 200_000)
	check("MsgDelegate 1,000 SCN into an 8-member entity (D)",
		c.deliverTx(del, msgDelegate(del.addr.String(), d.String(), scnInt(1_000))), 300_000)

	// MsgDelegateRouted with the 8-member entity's validator as primary.
	r := c.delegators[1]
	check("MsgDelegateRouted 100,000 SCN, primary D (8-member entity)",
		c.deliverTx(r, c.routed(r, d, 100_000)), 1_500_000)

	// Not in the spec's table: the router at its heaviest, a spill over every
	// other validator.
	s := c.delegators[2]
	res := c.deliverTx(s, c.routed(s, a, 250_000))
	check("MsgDelegateRouted 250,000 SCN, primary A, spilling to B and C (extra)", res, 1_500_000)
	require.Equal(t, scnInt(75_500), c.staked(s.addr, c.vals[1].val), "the spill reached the other validators")

	// MsgRevokeEntity last: it freezes D.
	check("MsgRevokeEntity, 8 members (gas-eight)",
		c.deliverTx(att, &entitytypes.MsgRevokeEntity{Authority: att.addr.String(), EntityId: "gas-eight", Reason: "test"}),
		200_000)
	require.NotNil(t, c.where(d).Frozen)
	c.requireRegistry()
}

// 8. A frozen seat stays charged to its former entity while its validator is
// ACTIVE (final review, Important 2). It keeps its seat and its income (J2), so
// it keeps counting against the owner's two seats and 5%: otherwise re-attesting
// the entity with fresh validators — or revoking and re-attesting it — would
// let one owner exceed both. The charge ends when the frozen validator leaves
// ACTIVE.
func TestFrozenSeatsStayChargedToTheirFormerEntity(t *testing.T) {
	for _, revokeFirst := range []bool{false, true} {
		name := "re-attest"
		if revokeFirst {
			name = "revoke, then re-attest the same id"
		}
		t.Run(name, func(t *testing.T) {
			c, att := entityAChain(t) // entity-a = {A 300,000, D 2,000}, both ACTIVE, two seats
			a, d := c.vals[0].val, c.fourth.val
			fifth := newAcct("fifth")
			c.fundAccount(c.delegators[3], fifth.addr, 20_000)
			e := fifth.val
			require.Equal(t, uint32(0), c.createValidatorTx(fifth, "fifth", 2_000).Code)
			require.Equal(t, vrtypes.Status_STATUS_BONDING, c.regStatus(e), "E is not attested yet")

			if revokeFirst {
				c.revoke(att, "entity-a")
			}
			c.attest(att, "entity-a", e)

			// A and D are frozen and keep their seats (freeze, never shed)...
			for _, v := range []sdk.ValAddress{a, d} {
				require.Equal(t, vrtypes.Status_STATUS_ACTIVE, c.regStatus(v))
				w := c.where(v)
				require.NotNil(t, w.Frozen)
				require.Equal(t, "entity-a", w.Frozen.FormerEntityId)
			}
			// ...and still count against entity-a: E is held at the seat cap.
			seats, err := c.app.EntityregistryKeeper.ActiveSeats(c.ctx(), "entity-a")
			require.NoError(t, err)
			require.Equal(t, uint32(2), seats)
			require.Equal(t, vrtypes.Status_STATUS_BONDING, c.regStatus(e), "a third seat for one owner")
			require.True(t, c.where(e).Pending)

			// The 5% cap: entity-a holds A + D + E = 304,000 SCN, 96,000 of room.
			s := c.standing(e)
			require.Equal(t, "entity-a", s.EntityID)
			require.Equal(t, scnInt(validatorSelfSCN+2_000+2_000), s.Tokens)
			over := scnInt(100_000)
			del := c.delegators[0]
			res := c.deliverTx(del, msgDelegate(del.addr.String(), e.String(), over))
			requireTxError(t, res, stakeenginetypes.ErrEntityCapReached)
			err = c.delegate(c.ctx(), del, e, over)
			require.ErrorIs(t, err, stakeenginetypes.ErrEntityCapReached)
			res = c.deliverTx(del, msgDelegate(del.addr.String(), e.String(), scnInt(96_000)))
			require.Equal(t, uint32(0), res.Code, "the room that is left still fits: %s", res.Log)
			c.requireInvariants()

			// D's operator leaves after its lock-up: D leaves ACTIVE, its charge ends,
			// and E takes the seat in the block's EndBlock.
			c.nextBlock(61 * 24 * time.Hour)
			res = c.deliverTx(c.fourth, stakingtypes.NewMsgUndelegate(c.fourth.addr.String(), d.String(), scnCoin(2_000)))
			require.Equal(t, uint32(0), res.Code, res.Log)
			c.nextBlock(time.Second)
			require.NotEqual(t, vrtypes.Status_STATUS_ACTIVE, c.regStatus(d))
			require.Nil(t, c.where(d).Frozen, "the row is swept once D is not ACTIVE")
			require.Equal(t, vrtypes.Status_STATUS_ACTIVE, c.regStatus(e), "E took the seat D's charge held")
			seats, err = c.app.EntityregistryKeeper.ActiveSeats(c.ctx(), "entity-a")
			require.NoError(t, err)
			require.Equal(t, uint32(2), seats, "E and the still-ACTIVE frozen A")
			require.Equal(t, scnInt(validatorSelfSCN+2_000+96_000), c.standing(e).Tokens, "A and E; D's charge is gone")
			c.requireInvariants()
		})
	}
}
