package app

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"sort"
	"strings"
	"testing"
	"time"

	"cosmossdk.io/log"
	abci "github.com/cometbft/cometbft/abci/types"
	dbm "github.com/cosmos/cosmos-db"
	"github.com/cosmos/cosmos-sdk/baseapp"
	"github.com/cosmos/cosmos-sdk/client/flags"
	"github.com/cosmos/cosmos-sdk/codec"
	simtestutil "github.com/cosmos/cosmos-sdk/testutil/sims"
	sdk "github.com/cosmos/cosmos-sdk/types"
	authtypes "github.com/cosmos/cosmos-sdk/x/auth/types"
	banktypes "github.com/cosmos/cosmos-sdk/x/bank/types"
	stakingtypes "github.com/cosmos/cosmos-sdk/x/staking/types"
	"github.com/stretchr/testify/require"

	"github.com/saicon-network/saicon/chain/crypto/vrf"
	entitytypes "github.com/saicon-network/saicon/chain/x/entityregistry/types"
	participationtypes "github.com/saicon-network/saicon/chain/x/participation/types"
	randomnesskeeper "github.com/saicon-network/saicon/chain/x/randomness/keeper"
	randomnesstypes "github.com/saicon-network/saicon/chain/x/randomness/types"
	stakeenginetypes "github.com/saicon-network/saicon/chain/x/stakeengine/types"
	vrtypes "github.com/saicon-network/saicon/chain/x/validatorregistry/types"
)

// runTo finalizes empty blocks until the chain stands at height h and returns
// the last block's response (nil when the chain is already there).
func (c *testChain) runTo(h int64) *abci.ResponseFinalizeBlock {
	c.t.Helper()
	require.LessOrEqual(c.t, c.height, h, "the chain is already past height %d", h)
	var res *abci.ResponseFinalizeBlock
	for c.height < h {
		res = c.nextBlock(time.Second)
	}
	return res
}

// round is the Round query's answer over the committed state.
func (c *testChain) round() *randomnesstypes.QueryRoundResponse {
	c.t.Helper()
	res, err := randomnesskeeper.NewQueryServerImpl(c.app.RandomnessKeeper).Round(c.ctx(), &randomnesstypes.QueryRoundRequest{})
	require.NoError(c.t, err)
	return res
}

// draw is the stored record of closed round n, read through the Draw query:
// the draw history is unexported and the query service is how it is read.
func (c *testChain) draw(n uint64) randomnesstypes.Draw {
	c.t.Helper()
	res, err := randomnesskeeper.NewQueryServerImpl(c.app.RandomnessKeeper).Draw(c.ctx(), &randomnesstypes.QueryDrawRequest{Round: n})
	require.NoError(c.t, err)
	return res.Draw
}

// submitMsg builds MsgSubmitVrf for the operator v with a proof by k over the
// round's alpha for the given input (hex, as Round.Input carries it).
func submitMsg(v acct, round uint64, k vrf.PrivateKey, input string) *randomnesstypes.MsgSubmitVrf {
	in, err := hex.DecodeString(input)
	if err != nil {
		panic(err)
	}
	return &randomnesstypes.MsgSubmitVrf{
		Operator: v.addr.String(),
		Round:    round,
		Proof:    hex.EncodeToString(k.Prove(randomnesstypes.RoundAlpha(testChainID, round, in))),
	}
}

// offerMsg builds MsgOfferVrfKey for the operator v and the key k, with the
// proof of possession over the chain id, the validator and the key.
func offerMsg(v acct, k vrf.PrivateKey) *entitytypes.MsgOfferVrfKey {
	return &entitytypes.MsgOfferVrfKey{
		Operator:          v.addr.String(),
		VrfPublicKey:      hex.EncodeToString(k.PublicKey()),
		ProofOfPossession: hex.EncodeToString(k.Prove(entitytypes.PopAlpha(testChainID, v.val, k.PublicKey()))),
	}
}

// signedMsg is one transaction of a multi-transaction block.
type signedMsg struct {
	signer acct
	msg    sdk.Msg
}

// deliverMany puts one signed transaction per entry in a single block and
// returns their results in order.
func (c *testChain) deliverMany(txs ...signedMsg) []*abci.ExecTxResult {
	c.t.Helper()
	raw := make([][]byte, len(txs))
	for i, tx := range txs {
		raw[i] = c.signedTx(tx.signer, tx.msg)
	}
	res := c.nextBlock(time.Second, raw...)
	require.Len(c.t, res.TxResults, len(txs))
	return res.TxResults
}

// eventsOf lists a block's events of one type.
func eventsOf(res *abci.ResponseFinalizeBlock, typ string) []abci.Event {
	var out []abci.Event
	for _, e := range res.Events {
		if e.Type == typ {
			out = append(out, e)
		}
	}
	return out
}

// attrOf is the value of an event's attribute, "" when it has none.
func attrOf(e abci.Event, key string) string {
	for _, a := range e.Attributes {
		if a.Key == key {
			return a.Value
		}
	}
	return ""
}

// requireRandomness requires V-14 on the committed state.
func (c *testChain) requireRandomness() {
	c.t.Helper()
	msg, broken := c.app.RandomnessKeeper.RandomnessInvariant(c.ctx())
	require.False(c.t, broken, msg)
}

// requireAllInvariants is W1's V-12 and V-13 and W2's V-14.
func (c *testChain) requireAllInvariants() {
	c.t.Helper()
	c.requireInvariants()
	c.requireRandomness()
}

// vrfKeyNamed is a deterministic VRF key for the tests that need keys beyond the
// harness validators': its seed is SHA-256 of a name.
func vrfKeyNamed(name string) vrf.PrivateKey {
	seed := sha256.Sum256([]byte("s8-test/vrf/" + name))
	k, err := vrf.NewKeyFromSeed(seed[:])
	if err != nil {
		panic(err)
	}
	return k
}

// roundAlphaOf is the alpha a round's proofs are made over.
func roundAlphaOf(t *testing.T, round uint64, inputHex string) []byte {
	t.Helper()
	in, err := hex.DecodeString(inputHex)
	require.NoError(t, err)
	return randomnesstypes.RoundAlpha(testChainID, round, in)
}

// outputOf is the VRF output of key k for the round, computed by the test from
// the key alone (not read from the chain).
func outputOf(t *testing.T, k vrf.PrivateKey, round uint64, inputHex string) string {
	t.Helper()
	alpha := roundAlphaOf(t, round, inputHex)
	out, err := vrf.Verify(k.PublicKey(), k.Prove(alpha), alpha)
	require.NoError(t, err)
	return hex.EncodeToString(out)
}

// requireRecordMatchesTheFormulas recomputes a closed round from the keys the
// test holds and compares it with the record the chain published: the outputs
// (sorted), the seed, and, for a DRAWN round, the winners. The formulas are
// the types package's own, called here with the test's inputs only.
func requireRecordMatchesTheFormulas(t *testing.T, d randomnesstypes.Draw, keys ...vrf.PrivateKey) {
	t.Helper()
	outputs := make([]string, len(keys))
	raw := make([][]byte, len(keys))
	for i, k := range keys {
		outputs[i] = outputOf(t, k, d.Round, d.Input)
		b, err := hex.DecodeString(outputs[i])
		require.NoError(t, err)
		raw[i] = b
	}
	sort.Strings(outputs)
	if len(outputs) == 0 {
		outputs = nil
	}
	require.Equal(t, outputs, d.Outputs, "round %d outputs", d.Round)

	input, err := hex.DecodeString(d.Input)
	require.NoError(t, err)
	seed := randomnesstypes.Seed(d.Round, input, raw)
	require.Equal(t, hex.EncodeToString(seed), d.Seed, "round %d seed", d.Round)
	if d.Outcome == randomnesstypes.DrawOutcome_DRAW_OUTCOME_DRAWN {
		require.Equal(t, randomnesstypes.Winners(seed, d.Pool), d.Winners, "round %d winners", d.Round)
	}
}

// submitGenesis delivers, in one block, a valid MsgSubmitVrf for the current
// round from each listed genesis validator (0 = A, 1 = B, 2 = C) and requires
// every transaction to succeed.
func (c *testChain) submitGenesis(idx ...int) {
	c.t.Helper()
	r := c.round().Round
	txs := make([]signedMsg, len(idx))
	for i, v := range idx {
		txs[i] = signedMsg{c.vals[v], submitMsg(c.vals[v], r.Number, harnessVrfKey(v), r.Input)}
	}
	for i, res := range c.deliverMany(txs...) {
		require.Equal(c.t, uint32(0), res.Code, "validator %d: %s", idx[i], res.Log)
		require.Len(c.t, eventsOfTx(res, randomnesstypes.EventTypeOutputSubmitted), 1)
	}
}

// eventsOfTx lists a transaction's events of one type.
func eventsOfTx(res *abci.ExecTxResult, typ string) []abci.Event {
	var out []abci.Event
	for _, e := range res.Events {
		if e.Type == typ {
			out = append(out, e)
		}
	}
	return out
}

// requireOnlyOne requires exactly one event of the type in the block and
// returns it.
func requireOnlyOne(t *testing.T, res *abci.ResponseFinalizeBlock, typ string) abci.Event {
	t.Helper()
	evs := eventsOf(res, typ)
	require.Len(t, evs, 1, "events of type %s", typ)
	return evs[0]
}

// ---------------------------------------------------------------------------

// 1. Round 1 is a warm-up: no key was bound before its cutoff (0), so it always
// ends SKIPPED. Its seed becomes round 2's input, round 2 draws on three
// verified outputs, and the test recomputes the seed and the winners from the
// keys it holds.
//
// Window arithmetic under withFastRounds (epoch 5, rotation every 2 epochs,
// window 10): round 1 is scheduled by block 1 for rotation 20, window [10, 20),
// snapshot in block 9. Round 2 is scheduled by block 20's EndBlock for rotation
// 40, window [30, 40), snapshot in block 29, key cutoff 10.
//
// The submissions go in block 31, not 30: a client that estimates gas with a
// simulation (--gas auto) runs it on the check state of the latest committed
// block, which is 29 while block 30 is being built, and the first window block
// is refused there (TestTheFirstWindowBlock pins that behaviour).
func TestRoundOneIsAWarmUpAndRoundTwoDraws(t *testing.T) {
	c := newTestChain(t, withFastRounds())

	// Round 1's window is open, the pool holds all three entities, and no
	// entity is keyed: a genesis key is bound at height 0, which is not before
	// the cutoff 0.
	c.runTo(11)
	r := c.round()
	require.Equal(t, uint64(1), r.Round.Number)
	require.Equal(t, randomnesstypes.RoundStatus_ROUND_STATUS_OPEN, r.Round.Status)
	require.Len(t, r.Pool, 3)
	for _, e := range r.Pool {
		require.Empty(t, e.Validator, "round 1 designates nobody: %s", e.EntityId)
		require.Empty(t, e.VrfPublicKey)
	}
	// ...so a submission in round 1 is refused: nobody is designated.
	res := c.deliverTx(c.vals[0], submitMsg(c.vals[0], 1, harnessVrfKey(0), r.Round.Input))
	requireTxError(t, res, randomnesstypes.ErrNotDesignated)

	closed := c.runTo(20)
	d1 := c.draw(1)
	require.Equal(t, randomnesstypes.DrawOutcome_DRAW_OUTCOME_SKIPPED, d1.Outcome)
	require.Equal(t, "quorum not met: 0 of 3 submitted, 2 required (0 had an eligible key)", d1.SkipReason)
	require.Len(t, d1.Pool, 3)
	require.Zero(t, d1.Keyed)
	require.Zero(t, d1.Submitted)
	require.Equal(t, uint32(2), d1.Required)
	require.Zero(t, d1.CohortRound, "no round has drawn yet")
	require.Equal(t, randomnesstypes.DefaultInitialInput(), d1.Input)
	require.Equal(t, int64(10), d1.WindowOpenHeight)
	require.Equal(t, int64(20), d1.RotationHeight)
	requireRecordMatchesTheFormulas(t, d1)
	requireOnlyOne(t, closed, randomnesstypes.EventTypeDrawSkipped)

	// Round 2 was created by that very EndBlock, with round 1's seed as input.
	r = c.round()
	require.Equal(t, uint64(2), r.Round.Number)
	require.Equal(t, d1.Seed, r.Round.Input)
	require.Equal(t, int64(10), r.Round.KeyCutoffHeight)
	require.Equal(t, int64(30), r.Round.WindowOpenHeight)
	require.Equal(t, int64(40), r.Round.RotationHeight)
	require.Equal(t, randomnesstypes.RoundStatus_ROUND_STATUS_SCHEDULED, r.Round.Status)

	c.runTo(30)
	r = c.round()
	require.Equal(t, randomnesstypes.RoundStatus_ROUND_STATUS_OPEN, r.Round.Status)
	require.Len(t, r.Pool, 3)
	for i, e := range r.Pool {
		require.Equal(t, c.vals[i].val.String(), e.Validator, "entity %s", e.EntityId)
		require.Equal(t, hex.EncodeToString(harnessVrfKey(i).PublicKey()), e.VrfPublicKey)
	}
	require.Equal(t, uint32(2), r.Required)

	c.submitGenesis(0, 1, 2) // block 31
	r = c.round()
	require.Len(t, r.Submissions, 3)
	for _, s := range r.Submissions {
		require.Equal(t, int64(31), s.Height)
	}

	closed = c.runTo(40)
	d2 := c.draw(2)
	require.Equal(t, randomnesstypes.DrawOutcome_DRAW_OUTCOME_DRAWN, d2.Outcome)
	require.Empty(t, d2.SkipReason)
	require.Equal(t, uint32(3), d2.Submitted)
	require.Equal(t, uint32(2), d2.Required)
	require.Equal(t, uint32(3), d2.Keyed)
	require.Equal(t, uint64(2), d2.CohortRound)
	require.Equal(t, d1.Seed, d2.Input, "round 2's input is round 1's seed")
	require.Equal(t, []string{"entity-a", "entity-b", "entity-c"}, d2.Pool)
	require.Len(t, d2.Winners, 3, "a pool of three is below the 22 seats: all three are named")
	require.ElementsMatch(t, d2.Pool, d2.Winners)

	// The seed and the winners, recomputed from the keys, not read back.
	requireRecordMatchesTheFormulas(t, d2, harnessVrfKey(0), harnessVrfKey(1), harnessVrfKey(2))

	seedEv := requireOnlyOne(t, closed, randomnesstypes.EventTypeSeedComputed)
	require.Equal(t, d2.Seed, attrOf(seedEv, randomnesstypes.AttributeKeySeed))
	drawEv := requireOnlyOne(t, closed, randomnesstypes.EventTypeShadowDraw)
	require.Equal(t, strings.Join(d2.Winners, ","), attrOf(drawEv, randomnesstypes.AttributeKeyWinners))
	require.Empty(t, eventsOf(closed, randomnesstypes.EventTypeDrawSkipped))

	// Round 3 stands scheduled on round 2's seed, its cutoff round 2's window open.
	r = c.round()
	require.Equal(t, uint64(3), r.Round.Number)
	require.Equal(t, d2.Seed, r.Round.Input)
	require.Equal(t, int64(30), r.Round.KeyCutoffHeight)
	require.Empty(t, r.Submissions)
	require.Empty(t, r.Pool)

	c.requireAllInvariants()
}

// untouchedAgainst requires that the listed genesis validators (0 = A, 1 = B,
// 2 = C) stand exactly where they stand on a control chain, built from the same
// genesis and run to the same height with no randomness transaction at all:
// registry status, staking record (jailed, bonded, tokens), accumulated EP,
// the reward decision of each of the last epochs, and the entity tickets.
//
// EP is compared with the control, not with an earlier height: x/participation
// credits every validator one point per closed epoch, submitter or not, and
// under withFastRounds an epoch is five blocks. What the draw must not do is
// move a validator's EP away from the control's. (LastEpochUpdate moves with
// every epoch too, so it is left out, epoch.go:157-160.)
func (c *testChain) untouchedAgainst(ctl *testChain, idx ...int) {
	c.t.Helper()
	require.Equal(c.t, ctl.height, c.height, "the control must stand at the same height")
	ctx, cctx := c.ctx(), ctl.ctx()

	tickets, err := c.app.EntityregistryKeeper.Tickets(ctx)
	require.NoError(c.t, err)
	want, err := ctl.app.EntityregistryKeeper.Tickets(cctx)
	require.NoError(c.t, err)
	require.NotEmpty(c.t, want)
	require.Equal(c.t, want, tickets, "tickets")

	epoch := uint64(c.height / 5)
	for _, i := range idx {
		v := c.vals[i].val
		require.Equal(c.t, vrtypes.Status_STATUS_ACTIVE, c.regStatus(v), "validator %d registry status", i)
		require.Equal(c.t, ctl.regStatus(v), c.regStatus(v))

		got, err := c.app.StakingKeeper.GetValidator(ctx, v)
		require.NoError(c.t, err)
		ref, err := ctl.app.StakingKeeper.GetValidator(cctx, v)
		require.NoError(c.t, err)
		require.False(c.t, got.Jailed, "validator %d is jailed", i)
		require.Equal(c.t, stakingtypes.Bonded, got.Status, "validator %d", i)
		require.Equal(c.t, ref.Tokens, got.Tokens, "validator %d tokens", i)
		require.Equal(c.t, scnInt(validatorSelfSCN), got.Tokens, "validator %d lost stake", i)
		require.Equal(c.t, ref.DelegatorShares, got.DelegatorShares, "validator %d shares", i)

		ep, found, err := c.app.ParticipationKeeper.GetEP(ctx, v)
		require.NoError(c.t, err)
		require.True(c.t, found)
		epRef, found, err := ctl.app.ParticipationKeeper.GetEP(cctx, v)
		require.NoError(c.t, err)
		require.True(c.t, found)
		require.True(c.t, epRef.AccumulatedEp.IsPositive())
		require.True(c.t, epRef.AccumulatedEp.Equal(ep.AccumulatedEp),
			"validator %d EP %s, control %s", i, ep.AccumulatedEp, epRef.AccumulatedEp)

		for e := epoch - 2; e <= epoch; e++ {
			snap, found, err := c.app.RewardsKeeper.GetRewardSnapshot(ctx, e, v)
			require.NoError(c.t, err)
			require.True(c.t, found, "validator %d epoch %d has no reward decision", i, e)
			snapRef, found, err := ctl.app.RewardsKeeper.GetRewardSnapshot(cctx, e, v)
			require.NoError(c.t, err)
			require.True(c.t, found)
			require.True(c.t, snap.Eligible, "validator %d epoch %d: %s", i, e, snap.Reason)
			require.Equal(c.t, snapRef, snap, "validator %d epoch %d reward decision", i, e)
		}
	}
}

// controlChain is a fresh chain with the same genesis as a test's, run to
// height h and never sent a randomness transaction.
func controlChain(t *testing.T, h int64, mutate ...func(codec.Codec, map[string]json.RawMessage)) *testChain {
	t.Helper()
	ctl := newTestChain(t, mutate...)
	ctl.runTo(h)
	return ctl
}

// 2. A round that misses its quorum is SKIPPED, and that is all that happens:
// a validator that did not submit keeps its registry status, its EP, its stake,
// its reward decisions and its ticket, and its entity stays in the next pool.
func TestAMissedQuorumSkipsAndNothingElseChanges(t *testing.T) {
	c := newTestChain(t, withFastRounds())
	c.runTo(30)
	c.submitGenesis(0, 1, 2) // block 31
	c.runTo(40)
	d2 := c.draw(2)
	require.Equal(t, randomnesstypes.DrawOutcome_DRAW_OUTCOME_DRAWN, d2.Outcome)

	// Round 3's window is [50, 60). The snapshot of what B and C are is taken at
	// block 50, the first window block, before anybody submits.
	c.runTo(50)
	r := c.round()
	require.Equal(t, uint64(3), r.Round.Number)
	require.Equal(t, randomnesstypes.RoundStatus_ROUND_STATUS_OPEN, r.Round.Status)
	require.Len(t, r.Pool, 3)
	ticketsAt50, err := c.app.EntityregistryKeeper.Tickets(c.ctx())
	require.NoError(t, err)
	statusAt50 := map[int]vrtypes.Status{1: c.regStatus(c.vals[1].val), 2: c.regStatus(c.vals[2].val)}

	c.submitGenesis(0) // block 51: only A
	closed := c.runTo(60)

	d3 := c.draw(3)
	require.Equal(t, randomnesstypes.DrawOutcome_DRAW_OUTCOME_SKIPPED, d3.Outcome)
	require.Equal(t, "quorum not met: 1 of 3 submitted, 2 required (3 had an eligible key)", d3.SkipReason)
	require.Equal(t, uint64(2), d3.CohortRound, "the cohort of round 2 stands")
	require.Equal(t, uint32(1), d3.Submitted)
	require.Equal(t, uint32(2), d3.Required)
	require.Empty(t, d3.Winners)
	require.Equal(t, d2.Seed, d3.Input)
	// A skipped round still hashes the outputs it did get: they feed round 4.
	requireRecordMatchesTheFormulas(t, d3, harnessVrfKey(0))

	skipped := requireOnlyOne(t, closed, randomnesstypes.EventTypeDrawSkipped)
	require.Equal(t, "3", attrOf(skipped, randomnesstypes.AttributeKeyRound))
	require.Equal(t, "2", attrOf(skipped, randomnesstypes.AttributeKeyCohortRound))
	require.Equal(t, "1", attrOf(skipped, randomnesstypes.AttributeKeySubmitted))
	require.Equal(t, "2", attrOf(skipped, randomnesstypes.AttributeKeyRequired))
	require.Equal(t, "3", attrOf(skipped, randomnesstypes.AttributeKeyPoolSize))
	require.Equal(t, d3.SkipReason, attrOf(skipped, randomnesstypes.AttributeKeyReason))
	requireOnlyOne(t, closed, randomnesstypes.EventTypeSeedComputed)
	require.Empty(t, eventsOf(closed, randomnesstypes.EventTypeShadowDraw))

	// Round 4 goes on from the skipped round's seed.
	r = c.round()
	require.Equal(t, uint64(4), r.Round.Number)
	require.Equal(t, d3.Seed, r.Round.Input)
	require.Equal(t, int64(50), r.Round.KeyCutoffHeight)

	// Nothing about B and C moved: registry status and tickets are what they
	// were at block 50; EP, stake and rewards are what a chain that never ran
	// the draw has at block 60; and A, the submitter, is no different.
	for i, st := range statusAt50 {
		require.Equal(t, st, c.regStatus(c.vals[i].val), "validator %d", i)
	}
	ticketsAt60, err := c.app.EntityregistryKeeper.Tickets(c.ctx())
	require.NoError(t, err)
	require.Equal(t, ticketsAt50, ticketsAt60)
	c.untouchedAgainst(controlChain(t, 60, withFastRounds()), 0, 1, 2)

	epA, _, err := c.app.ParticipationKeeper.GetEP(c.ctx(), c.vals[0].val)
	require.NoError(t, err)
	for _, i := range []int{1, 2} {
		ep, _, err := c.app.ParticipationKeeper.GetEP(c.ctx(), c.vals[i].val)
		require.NoError(t, err)
		require.True(t, epA.AccumulatedEp.Equal(ep.AccumulatedEp), "validator %d EP differs from the submitter's", i)
	}
	c.requireAllInvariants()
}

// 3. Every refusal of spec §8 reaches the caller through a signed transaction,
// and a refused submission writes nothing: the round closes on the one valid
// submission.
func TestSubmissionsRefusedThroughTransactions(t *testing.T) {
	c := newTestChain(t, withFastRounds())
	c.runTo(28)
	// Round 2 is current from block 20; its input is round 1's seed.
	input := c.round().Round.Input
	a, b := c.vals[0], c.vals[1]

	// Block 29: before the window (it opens at 30, the snapshot is taken at the
	// end of block 29).
	res := c.deliverTx(a, submitMsg(a, 2, harnessVrfKey(0), input))
	requireTxError(t, res, randomnesstypes.ErrWindowClosed)
	require.Contains(t, res.Log, "the window opens at height 30")
	require.Equal(t, int64(29), c.height)

	c.runTo(30)
	require.Equal(t, randomnesstypes.RoundStatus_ROUND_STATUS_OPEN, c.round().Round.Status)

	// 31: another round's number, though the proof is a good one for round 2.
	res = c.deliverTx(a, submitMsg(a, 3, harnessVrfKey(0), input))
	requireTxError(t, res, randomnesstypes.ErrWrongRound)

	// 32: D has an account and no seat in the pool.
	res = c.deliverTx(c.fourth, submitMsg(c.fourth, 2, harnessVrfKey(0), input))
	requireTxError(t, res, randomnesstypes.ErrNotDesignated)

	// 33: A's account with a proof made under B's key.
	res = c.deliverTx(a, submitMsg(a, 2, harnessVrfKey(1), input))
	requireTxError(t, res, randomnesstypes.ErrInvalidProof)

	// 34: A, correctly. 35: A again.
	res = c.deliverTx(a, submitMsg(a, 2, harnessVrfKey(0), input))
	require.Equal(t, uint32(0), res.Code, res.Log)
	res = c.deliverTx(a, submitMsg(a, 2, harnessVrfKey(0), input))
	requireTxError(t, res, randomnesstypes.ErrAlreadySubmitted)
	require.Len(t, c.round().Submissions, 1)

	// 40: the rotation height. The round is still OPEN until this block's
	// EndBlock, and B's proof is a good one, but the window closed at 39.
	c.runTo(39)
	res = c.deliverTx(b, submitMsg(b, 2, harnessVrfKey(1), input))
	requireTxError(t, res, randomnesstypes.ErrWindowClosed)
	require.Contains(t, res.Log, "late: the window closed at height 39")
	require.Equal(t, int64(40), c.height)

	// The refused ones left nothing behind: round 2 closed on A's one output.
	d2 := c.draw(2)
	require.Equal(t, randomnesstypes.DrawOutcome_DRAW_OUTCOME_SKIPPED, d2.Outcome)
	require.Equal(t, uint32(1), d2.Submitted)
	requireRecordMatchesTheFormulas(t, d2, harnessVrfKey(0))
	c.requireAllInvariants()
}

// The edges of the window, as a client meets them. A --gas auto simulation runs
// on the check state of the latest committed block, so while block O (30) is
// being built it sees block 29, whose height is below the window: the first
// window block is refused in simulation, by design of the check state. The
// same transaction included in block 30 is accepted (the block's own height is
// the one the handler sees), and a simulation after block 30 passes. Clients
// therefore submit from O+1 on; the real-transaction tests above and below do.
func TestTheFirstWindowBlock(t *testing.T) {
	c := newTestChain(t, withFastRounds())
	c.runTo(29)
	r := c.round().Round
	require.Equal(t, randomnesstypes.RoundStatus_ROUND_STATUS_OPEN, r.Status, "the snapshot is taken in block 29")
	a, b := c.vals[0], c.vals[1]

	txA := c.signedTx(a, submitMsg(a, 2, harnessVrfKey(0), r.Input))
	_, _, err := c.app.Simulate(txA)
	require.ErrorIs(t, err, randomnesstypes.ErrWindowClosed, "simulation at latest height O-1")

	// The same bytes in block 30 are accepted.
	res := c.nextBlock(time.Second, txA)
	require.Len(t, res.TxResults, 1)
	require.Equal(t, uint32(0), res.TxResults[0].Code, res.TxResults[0].Log)

	// A simulation now (latest height 30) passes, for a validator that has not submitted.
	_, sim, err := c.app.Simulate(c.signedTx(b, submitMsg(b, 2, harnessVrfKey(1), r.Input)))
	require.NoError(t, err)
	require.NotNil(t, sim)
	c.requireAllInvariants()
}

// 4. The key ceremony through transactions, for a fourth validator: it offers a
// key, the attestor binds it, the first round whose cutoff is after the binding
// designates it, and it submits. A binding to a key that was never offered is
// refused.
func TestTheKeyCeremonyThroughTransactions(t *testing.T) {
	att := newAcct("attestor")
	c := newTestChain(t, withFastRounds(), withAttestor(att))
	d := c.fourth
	kD := vrfKeyNamed("ceremony-d")
	kX := vrfKeyNamed("ceremony-never-offered")

	c.createValidatorBlock(d, "fourth", 5_000)
	c.attest(att, "entity-d", d.val)
	require.Equal(t, vrtypes.Status_STATUS_ACTIVE, c.regStatus(d.val))

	bind := func(k vrf.PrivateKey) *entitytypes.MsgAttestEntity {
		m := attestMsg(att, "entity-d", d.val)
		m.VrfKeys = []entitytypes.MemberVrfKey{{Validator: d.val.String(), VrfPublicKey: hex.EncodeToString(k.PublicKey())}}
		return m
	}

	// No offer yet: the binding is refused, and nothing is written.
	requireTxError(t, c.deliverTx(att, bind(kD)), entitytypes.ErrVrfKeyNotOffered)
	m, _, err := c.app.EntityregistryKeeper.EntityOf(c.ctx(), d.val)
	require.NoError(t, err)
	require.Empty(t, m.VrfPublicKey)

	// The operator offers kD.
	res := c.deliverTx(d, offerMsg(d, kD))
	require.Equal(t, uint32(0), res.Code, res.Log)
	offer, err := c.app.EntityregistryKeeper.VrfKeyOffers.Get(c.ctx(), d.val)
	require.NoError(t, err)
	require.Equal(t, hex.EncodeToString(kD.PublicKey()), offer.VrfPublicKey)

	// A binding to another key than the offered one is refused as well.
	requireTxError(t, c.deliverTx(att, bind(kX)), entitytypes.ErrVrfKeyNotOffered)
	m, _, err = c.app.EntityregistryKeeper.EntityOf(c.ctx(), d.val)
	require.NoError(t, err)
	require.Empty(t, m.VrfPublicKey)

	// The attestor binds the offered key.
	res = c.deliverTx(att, bind(kD))
	require.Equal(t, uint32(0), res.Code, res.Log)
	bound := c.height
	m, _, err = c.app.EntityregistryKeeper.EntityOf(c.ctx(), d.val)
	require.NoError(t, err)
	require.Equal(t, hex.EncodeToString(kD.PublicKey()), m.VrfPublicKey)
	require.Equal(t, bound, m.VrfKeyHeight)
	require.Less(t, bound, int64(10), "bound before round 1's window opens, so round 2's cutoff (10) is after it")

	// Round 1 (cutoff 0) cannot designate it; round 2 (cutoff 10 > bound) does.
	c.runTo(11)
	var d1 *randomnesstypes.PoolEntry
	for _, e := range c.round().Pool {
		if e.EntityId == "entity-d" {
			d1 = &e
		}
	}
	require.NotNil(t, d1, "entity-d is in round 1's pool")
	require.Empty(t, d1.Validator)

	c.runTo(30)
	r := c.round()
	require.Equal(t, uint64(2), r.Round.Number)
	require.Len(t, r.Pool, 4)
	var pe *randomnesstypes.PoolEntry
	for _, e := range r.Pool {
		if e.EntityId == "entity-d" {
			pe = &e
		}
	}
	require.NotNil(t, pe)
	require.Equal(t, d.val.String(), pe.Validator)
	require.Equal(t, hex.EncodeToString(kD.PublicKey()), pe.VrfPublicKey)
	require.Equal(t, uint32(3), r.Required, "ceil(2*4/3)")

	// All four submit; D's proof is under the key it offered.
	txs := []signedMsg{{d, submitMsg(d, 2, kD, r.Round.Input)}}
	for i := range c.vals {
		txs = append(txs, signedMsg{c.vals[i], submitMsg(c.vals[i], 2, harnessVrfKey(i), r.Round.Input)})
	}
	for i, res := range c.deliverMany(txs...) {
		require.Equal(t, uint32(0), res.Code, "tx %d: %s", i, res.Log)
	}
	c.runTo(40)
	d2 := c.draw(2)
	require.Equal(t, randomnesstypes.DrawOutcome_DRAW_OUTCOME_DRAWN, d2.Outcome)
	require.Equal(t, uint32(4), d2.Submitted)
	require.Equal(t, uint32(4), d2.Keyed)
	require.Contains(t, d2.Winners, "entity-d")
	requireRecordMatchesTheFormulas(t, d2, kD, harnessVrfKey(0), harnessVrfKey(1), harnessVrfKey(2))
	c.requireAllInvariants()
}

// 5. Review Focus 2, through transactions: a key rotated while a window is open.
// The running round keeps the key copied into its snapshot, the new key is not
// eligible for the next round (its binding is after that round's cutoff), and
// is eligible for the one after.
func TestAKeyRotatedMidWindowCountsLater(t *testing.T) {
	att := newAcct("attestor")
	c := newTestChain(t, withFastRounds(), withAttestor(att))
	a, b, cc := c.vals[0], c.vals[1], c.vals[2]
	k2 := vrfKeyNamed("rotated-a")
	k2Hex := hex.EncodeToString(k2.PublicKey())

	// Round 2's window is open (cutoff 10; A's genesis key is eligible).
	c.runTo(30)
	input2 := c.round().Round.Input

	res := c.deliverTx(a, offerMsg(a, k2)) // block 31
	require.Equal(t, uint32(0), res.Code, res.Log)
	rebind := attestMsg(att, "entity-a", a.val)
	rebind.VrfKeys = []entitytypes.MemberVrfKey{{Validator: a.val.String(), VrfPublicKey: k2Hex}}
	res = c.deliverTx(att, rebind) // block 32
	require.Equal(t, uint32(0), res.Code, res.Log)
	m, _, err := c.app.EntityregistryKeeper.EntityOf(c.ctx(), a.val)
	require.NoError(t, err)
	require.Equal(t, k2Hex, m.VrfPublicKey)
	require.Equal(t, int64(32), m.VrfKeyHeight)

	// The snapshot still holds the genesis key.
	for _, e := range c.round().Pool {
		if e.EntityId == "entity-a" {
			require.Equal(t, hex.EncodeToString(harnessVrfKey(0).PublicKey()), e.VrfPublicKey)
		}
	}
	// The new key does not verify against it; the old one does.
	requireTxError(t, c.deliverTx(a, submitMsg(a, 2, k2, input2)), randomnesstypes.ErrInvalidProof) // 33
	res = c.deliverTx(a, submitMsg(a, 2, harnessVrfKey(0), input2))                                 // 34
	require.Equal(t, uint32(0), res.Code, res.Log)
	closed := c.runTo(40)
	d2 := c.draw(2)
	require.Equal(t, randomnesstypes.DrawOutcome_DRAW_OUTCOME_SKIPPED, d2.Outcome, "only A submitted")
	requireRecordMatchesTheFormulas(t, d2, harnessVrfKey(0))
	requireOnlyOne(t, closed, randomnesstypes.EventTypeDrawSkipped)

	// Round 3: cutoff 30, A's key is bound at 32, so A has no designated key; B
	// and C do. A cannot submit under either key.
	c.runTo(50)
	r := c.round()
	require.Equal(t, uint64(3), r.Round.Number)
	require.Equal(t, int64(30), r.Round.KeyCutoffHeight)
	require.Len(t, r.Pool, 3)
	for _, e := range r.Pool {
		if e.EntityId == "entity-a" {
			require.Empty(t, e.Validator)
			require.Empty(t, e.VrfPublicKey)
		} else {
			require.NotEmpty(t, e.Validator, e.EntityId)
		}
	}
	requireTxError(t, c.deliverTx(a, submitMsg(a, 3, k2, r.Round.Input)), randomnesstypes.ErrNotDesignated)               // 51
	requireTxError(t, c.deliverTx(a, submitMsg(a, 3, harnessVrfKey(0), r.Round.Input)), randomnesstypes.ErrNotDesignated) // 52
	// B and C make the quorum (2 of 3) without it.
	for i, res := range c.deliverMany(
		signedMsg{b, submitMsg(b, 3, harnessVrfKey(1), r.Round.Input)},
		signedMsg{cc, submitMsg(cc, 3, harnessVrfKey(2), r.Round.Input)},
	) {
		require.Equal(t, uint32(0), res.Code, "tx %d: %s", i, res.Log)
	}
	c.runTo(60)
	d3 := c.draw(3)
	require.Equal(t, randomnesstypes.DrawOutcome_DRAW_OUTCOME_DRAWN, d3.Outcome)
	require.Equal(t, uint32(2), d3.Submitted)
	require.Equal(t, uint32(2), d3.Keyed)
	requireRecordMatchesTheFormulas(t, d3, harnessVrfKey(1), harnessVrfKey(2))

	// Round 4: cutoff 50 > 32. A is designated, with the new key.
	c.runTo(70)
	r = c.round()
	require.Equal(t, uint64(4), r.Round.Number)
	require.Equal(t, int64(50), r.Round.KeyCutoffHeight)
	for _, e := range r.Pool {
		if e.EntityId == "entity-a" {
			require.Equal(t, a.val.String(), e.Validator)
			require.Equal(t, k2Hex, e.VrfPublicKey)
		}
	}
	requireTxError(t, c.deliverTx(a, submitMsg(a, 4, harnessVrfKey(0), r.Round.Input)), randomnesstypes.ErrInvalidProof) // 71
	for i, res := range c.deliverMany(
		signedMsg{a, submitMsg(a, 4, k2, r.Round.Input)},
		signedMsg{b, submitMsg(b, 4, harnessVrfKey(1), r.Round.Input)},
		signedMsg{cc, submitMsg(cc, 4, harnessVrfKey(2), r.Round.Input)},
	) {
		require.Equal(t, uint32(0), res.Code, "tx %d: %s", i, res.Log)
	}
	c.runTo(80)
	d4 := c.draw(4)
	require.Equal(t, randomnesstypes.DrawOutcome_DRAW_OUTCOME_DRAWN, d4.Outcome)
	require.Equal(t, uint32(3), d4.Submitted)
	requireRecordMatchesTheFormulas(t, d4, k2, harnessVrfKey(1), harnessVrfKey(2))
	c.requireAllInvariants()
}

// 6. A validator that never submits loses nothing. Over three rounds C stays
// silent while A and B make the quorum: no slash, no jail, the registry status,
// stake, EP, reward decisions and ticket are those of a chain that never ran
// the draw, C's entity stays in every pool, and it can even be named a winner.
func TestNotSubmittingCostsNothing(t *testing.T) {
	c := newTestChain(t, withFastRounds())
	for _, opens := range []int64{30, 50, 70} {
		c.runTo(opens)
		c.submitGenesis(0, 1) // opens+1: A and B, never C
		c.runTo(opens + 10)
	}
	for n := uint64(2); n <= 4; n++ {
		d := c.draw(n)
		require.Equal(t, randomnesstypes.DrawOutcome_DRAW_OUTCOME_DRAWN, d.Outcome, "round %d: %s", n, d.SkipReason)
		require.Equal(t, uint32(2), d.Submitted)
		require.Contains(t, d.Pool, "entity-c", "round %d", n)
		require.Contains(t, d.Winners, "entity-c", "a silent entity is still in the draw, round %d", n)
		requireRecordMatchesTheFormulas(t, d, harnessVrfKey(0), harnessVrfKey(1))
	}

	c.untouchedAgainst(controlChain(t, 80, withFastRounds()), 0, 1, 2)
	// C did nothing on this chain, yet its slashing and staking records are clean.
	val, err := c.app.StakingKeeper.GetValidator(c.ctx(), c.vals[2].val)
	require.NoError(t, err)
	require.False(t, val.Jailed)
	require.Equal(t, scnInt(validatorSelfSCN), val.Tokens)
	c.requireAllInvariants()
}

// withOnlyValidatorA turns the harness genesis into a devnet-shaped one: a
// single bonded validator (A) holding all the stake and all of one entity, the
// entity-cap and validator-cap floors both set to floorSCN (the devnet's
// genesis, and the v0.10.0 migration, set the entity floor equal to the
// validator floor). B and C remain funded accounts.
func withOnlyValidatorA(floorSCN int64) func(cdc codec.Codec, gen map[string]json.RawMessage) {
	return func(cdc codec.Codec, gen map[string]json.RawMessage) {
		a := newAcct("validatorA").val.String()

		var stGen stakingtypes.GenesisState
		cdc.MustUnmarshalJSON(gen[stakingtypes.ModuleName], &stGen)
		stGen.Validators = stGen.Validators[:1]
		stGen.Delegations = stGen.Delegations[:1]
		gen[stakingtypes.ModuleName] = cdc.MustMarshalJSON(&stGen)

		var bankGen banktypes.GenesisState
		cdc.MustUnmarshalJSON(gen[banktypes.ModuleName], &bankGen)
		bonded := authtypes.NewModuleAddress(stakingtypes.BondedPoolName).String()
		supply := sdk.NewCoins()
		for i := range bankGen.Balances {
			if bankGen.Balances[i].Address == bonded {
				bankGen.Balances[i].Coins = sdk.NewCoins(scnCoin(validatorSelfSCN))
			}
			supply = supply.Add(bankGen.Balances[i].Coins...)
		}
		bankGen.Supply = supply
		gen[banktypes.ModuleName] = cdc.MustMarshalJSON(&bankGen)

		var vrGen vrtypes.GenesisState
		cdc.MustUnmarshalJSON(gen[vrtypes.ModuleName], &vrGen)
		vrGen.Validators = vrGen.Validators[:1]
		gen[vrtypes.ModuleName] = cdc.MustMarshalJSON(&vrGen)

		var erGen entitytypes.GenesisState
		cdc.MustUnmarshalJSON(gen[entitytypes.ModuleName], &erGen)
		erGen.Entities = erGen.Entities[:1]
		erGen.Members = erGen.Members[:1]
		erGen.VrfKeyOwners = erGen.VrfKeyOwners[:1]
		if erGen.Members[0].Validator != a {
			panic("the first member is not validator A")
		}
		gen[entitytypes.ModuleName] = cdc.MustMarshalJSON(&erGen)

		var pGen participationtypes.GenesisState
		cdc.MustUnmarshalJSON(gen[participationtypes.ModuleName], &pGen)
		pGen.Scores = pGen.Scores[:1]
		pGen.EmpiricalPoints = pGen.EmpiricalPoints[:1]
		gen[participationtypes.ModuleName] = cdc.MustMarshalJSON(&pGen)

		var seGen stakeenginetypes.GenesisState
		cdc.MustUnmarshalJSON(gen[stakeenginetypes.ModuleName], &seGen)
		seGen.Params.ValidatorCapFloor = scnInt(floorSCN)
		seGen.Params.EntityCapFloor = scnInt(floorSCN)
		gen[stakeenginetypes.ModuleName] = cdc.MustMarshalJSON(&seGen)
	}
}

// 7. On a one-entity, devnet-like chain the 5% entity cap does not empty the
// pool: the entity holds 100% of the stake, but the cap is max(5% of the bonded
// EWMA, entity_cap_floor) and the floor is the validator floor, so it stays in.
// Quorum of one is one: the single designated validator submits and the round
// draws (spec §16.2). Round 1 is the warm-up it is everywhere.
func TestAOneEntityChainStillDraws(t *testing.T) {
	c := newTestChain(t, withFastRounds(), withOnlyValidatorA(testCapFloorSCN))
	a := c.vals[0]

	st := c.standing(a.val)
	require.Equal(t, "entity-a", st.EntityID)
	require.Equal(t, scnInt(validatorSelfSCN), st.Tokens, "the entity holds all the stake")
	require.Equal(t, scnInt(testCapFloorSCN), st.Cap, "the cap is the floor: 5% of the stake would be far lower")
	require.True(t, st.Tokens.LTE(st.Cap))

	c.runTo(20)
	d1 := c.draw(1)
	require.Equal(t, randomnesstypes.DrawOutcome_DRAW_OUTCOME_SKIPPED, d1.Outcome)
	require.Equal(t, []string{"entity-a"}, d1.Pool, "the pool is not emptied by the cap")
	require.Equal(t, "quorum not met: 0 of 1 submitted, 1 required (0 had an eligible key)", d1.SkipReason)

	c.runTo(30)
	r := c.round()
	require.Equal(t, []randomnesstypes.PoolEntry{{
		EntityId:     "entity-a",
		Validator:    a.val.String(),
		VrfPublicKey: hex.EncodeToString(harnessVrfKey(0).PublicKey()),
	}}, r.Pool)
	require.Equal(t, uint32(1), r.Required)

	c.submitGenesis(0) // block 31
	c.runTo(40)
	d2 := c.draw(2)
	require.Equal(t, randomnesstypes.DrawOutcome_DRAW_OUTCOME_DRAWN, d2.Outcome, d2.SkipReason)
	require.Equal(t, []string{"entity-a"}, d2.Winners)
	require.Equal(t, uint32(1), d2.Submitted)
	require.Equal(t, uint32(1), d2.Required)
	requireRecordMatchesTheFormulas(t, d2, harnessVrfKey(0))
	c.requireAllInvariants()
}

// The other side of the same rule: were the floor below the entity's stake, the
// entity would be over its cap and out of the pool, and the round would end
// SKIPPED as "pool is empty" (spec §7.3). Without this the test above could pass
// for the wrong reason.
func TestAnEntityOverTheCapIsOutOfThePool(t *testing.T) {
	c := newTestChain(t, withFastRounds(), withOnlyValidatorA(100_000))
	st := c.standing(c.vals[0].val)
	require.True(t, st.Tokens.GT(st.Cap), "tokens %s cap %s", st.Tokens, st.Cap)

	c.runTo(20)
	d1 := c.draw(1)
	require.Equal(t, randomnesstypes.DrawOutcome_DRAW_OUTCOME_SKIPPED, d1.Outcome)
	require.Empty(t, d1.Pool)
	require.Equal(t, "pool is empty", d1.SkipReason)
	c.requireRandomness()
}

// 8. The randomness state exports and imports without loss, mid-round: a chain
// that holds an OPEN round with submissions and a history of one DRAWN and two
// SKIPPED rounds is exported, imported into a fresh app with InitChain, and
// from there the next submission is accepted (the designated index was
// rebuilt) and the round closes with the record the original chain produces.
func TestRandomnessSurvivesExportAndImport(t *testing.T) {
	c := newTestChain(t, withFastRounds())
	c.runTo(30)
	c.submitGenesis(0, 1, 2) // round 2: DRAWN at 40
	c.runTo(50)
	c.submitGenesis(0) // round 3: SKIPPED at 60
	c.runTo(70)
	c.submitGenesis(0, 1) // round 4: OPEN, two of three submitted
	c.runTo(72)

	want, err := c.app.RandomnessKeeper.ExportGenesis(c.ctx())
	require.NoError(t, err)
	require.Equal(t, uint64(4), want.CurrentRound.Number)
	require.Equal(t, randomnesstypes.RoundStatus_ROUND_STATUS_OPEN, want.CurrentRound.Status)
	require.Len(t, want.Pool, 3)
	require.Len(t, want.Submissions, 2)
	require.Len(t, want.Draws, 3)
	require.Equal(t, randomnesstypes.DrawOutcome_DRAW_OUTCOME_SKIPPED, want.Draws[0].Outcome)
	require.Equal(t, randomnesstypes.DrawOutcome_DRAW_OUTCOME_DRAWN, want.Draws[1].Outcome)
	require.Equal(t, randomnesstypes.DrawOutcome_DRAW_OUTCOME_SKIPPED, want.Draws[2].Outcome)

	exported, err := c.app.ExportAppStateAndValidators(false, nil, nil)
	require.NoError(t, err)
	require.Equal(t, int64(73), exported.Height)

	fresh := New(log.NewNopLogger(), dbm.NewMemDB(), nil, true,
		simtestutil.AppOptionsMap{flags.FlagHome: t.TempDir()}, baseapp.SetChainID(testChainID))
	_, err = fresh.InitChain(&abci.RequestInitChain{
		ChainId:         testChainID,
		Time:            c.time,
		InitialHeight:   exported.Height,
		ConsensusParams: &exported.ConsensusParams,
		AppStateBytes:   exported.AppState,
	})
	require.NoError(t, err)

	// InitChain leaves its state in the finalize-block context, uncommitted.
	ictx := fresh.GetContextForFinalizeBlock(nil)
	got, err := fresh.RandomnessKeeper.ExportGenesis(ictx)
	require.NoError(t, err)
	require.Equal(t, want, got)
	for _, v := range c.vals {
		entity, err := fresh.RandomnessKeeper.Designated.Get(ictx, v.val)
		require.NoError(t, err, "the designated index of %s was not rebuilt", v.val)
		require.NotEmpty(t, entity)
	}
	msg, broken := fresh.RandomnessKeeper.RandomnessInvariant(ictx)
	require.False(t, broken, msg)

	// Both chains go on from block 73 with the same inputs.
	f := &testChain{
		t: t, app: fresh, vals: c.vals, fourth: c.fourth, delegators: c.delegators, consKeys: c.consKeys,
		height: exported.Height - 1, time: c.time,
	}
	for _, ch := range []*testChain{c, f} {
		ch.nextBlock(time.Second) // 73
		require.Equal(t, uint64(4), ch.round().Round.Number)
		require.Len(t, ch.round().Submissions, 2)
		ch.submitGenesis(2) // 74: C, the one that had not submitted
		ch.runTo(80)
	}
	for _, ch := range []*testChain{c, f} {
		d4 := ch.draw(4)
		require.Equal(t, randomnesstypes.DrawOutcome_DRAW_OUTCOME_DRAWN, d4.Outcome, d4.SkipReason)
		require.Equal(t, uint32(3), d4.Submitted)
		requireRecordMatchesTheFormulas(t, d4, harnessVrfKey(0), harnessVrfKey(1), harnessVrfKey(2))
		r := ch.round()
		require.Equal(t, uint64(5), r.Round.Number)
		require.Equal(t, d4.Seed, r.Round.Input)
		ch.requireAllInvariants()
	}
	require.Equal(t, c.draw(4), f.draw(4), "the imported chain closes the round as the original does")
	for n := uint64(1); n <= 3; n++ {
		require.Equal(t, c.draw(n), f.draw(n), "round %d", n)
	}
}

// 9. Gas, measured through deliverTx (limit 3,000,000). The bounds are spec
// §19's; the numbers are logged for the spec's implementation notes (§23).
func TestGasOfTheRandomnessTransactions(t *testing.T) {
	att := newAcct("attestor")
	c := newTestChain(t, withFastRounds(), withAttestor(att))
	a := c.vals[0]
	k2 := vrfKeyNamed("gas-a")

	check := func(name string, res *abci.ExecTxResult, bound int64) {
		t.Helper()
		require.Equal(t, uint32(0), res.Code, "%s: %s", name, res.Log)
		t.Logf("GAS %-58s used %9d  (bound < %d)", name, res.GasUsed, bound)
		require.Less(t, res.GasUsed, bound, name)
	}

	check("MsgOfferVrfKey (A)", c.deliverTx(a, offerMsg(a, k2)), 200_000)

	rebind := attestMsg(att, "entity-a", a.val)
	rebind.VrfKeys = []entitytypes.MemberVrfKey{{Validator: a.val.String(), VrfPublicKey: hex.EncodeToString(k2.PublicKey())}}
	check("MsgAttestEntity, 1 validator, 1 key (re-attest entity-a)", c.deliverTx(att, rebind), 200_000)

	// The submission: A's new key was bound before round 1's window, so round 2
	// designates it.
	c.runTo(30)
	input := c.round().Round.Input
	check("MsgSubmitVrf (A, round 2)", c.deliverTx(a, submitMsg(a, 2, k2, input)), 200_000)

	// Eight members, eight keys: D and seven accounts that are not validators
	// (W1 §6.1: members need not be validators), each operator offering its own.
	d := c.fourth
	members := []acct{d}
	for i := range 7 {
		m := newAcct("vrf-gas-member-" + string(rune('0'+i)))
		c.fundAccount(c.delegators[0], m.addr, 1)
		members = append(members, m)
	}
	c.createValidatorBlock(d, "fourth", 5_000)
	offers := make([]signedMsg, len(members))
	for i, m := range members {
		offers[i] = signedMsg{m, offerMsg(m, vrfKeyNamed("gas-member-"+m.addr.String()))}
	}
	for i, res := range c.deliverMany(offers...) {
		require.Equal(t, uint32(0), res.Code, "offer %d: %s", i, res.Log)
	}
	eight := attestMsg(att, "gas-eight")
	for _, m := range members {
		eight.Validators = append(eight.Validators, m.val.String())
		eight.VrfKeys = append(eight.VrfKeys, entitytypes.MemberVrfKey{
			Validator:    m.val.String(),
			VrfPublicKey: hex.EncodeToString(vrfKeyNamed("gas-member-" + m.addr.String()).PublicKey()),
		})
	}
	require.Len(t, eight.VrfKeys, 8)
	check("MsgAttestEntity, 8 validators, 8 keys (gas-eight)", c.deliverTx(att, eight), 300_000)
	c.requireAllInvariants()
}
