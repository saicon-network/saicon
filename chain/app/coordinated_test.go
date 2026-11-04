package app

import (
	"bytes"
	"encoding/json"
	gomath "math"
	"sort"
	"strings"
	"testing"
	"time"

	"cosmossdk.io/collections"
	"cosmossdk.io/core/comet"
	"cosmossdk.io/math"
	storetypes "cosmossdk.io/store/types"
	evidencetypes "cosmossdk.io/x/evidence/types"
	abci "github.com/cometbft/cometbft/abci/types"
	cmtproto "github.com/cometbft/cometbft/proto/tendermint/types"
	"github.com/cosmos/cosmos-sdk/codec"
	sdk "github.com/cosmos/cosmos-sdk/types"
	govv1 "github.com/cosmos/cosmos-sdk/x/gov/types/v1"
	slashingtypes "github.com/cosmos/cosmos-sdk/x/slashing/types"
	stakingtypes "github.com/cosmos/cosmos-sdk/x/staking/types"
	"github.com/stretchr/testify/require"

	compliancekeeper "github.com/saicon-network/saicon/chain/x/compliance/keeper"
	compliancetypes "github.com/saicon-network/saicon/chain/x/compliance/types"
	entitytypes "github.com/saicon-network/saicon/chain/x/entityregistry/types"
	stakeenginetypes "github.com/saicon-network/saicon/chain/x/stakeengine/types"
	vrtypes "github.com/saicon-network/saicon/chain/x/validatorregistry/types"
)

// The coordinated-attack rule on the full app (S8 W3, spec §16 "App level"):
// duplicate votes go through FinalizeBlock as the block's Misbehavior list, so
// the unmodified x/evidence -> x/slashing -> x/staking path runs, and x/compliance
// records what x/evidence stored.

// genesisValidators is the number of validators the harness starts with (A, B, C).
const genesisValidators = 3

// withDoubleSignReady writes one x/slashing signing info per genesis validator
// and nothing else. The harness writes x/staking directly, so no hook creates
// the signing infos x/evidence needs ("expected signing info for validator …",
// a panic otherwise). The slashing parameters stay as the binary's default
// genesis has them: double-sign 0.10, downtime 0 (app/slashing_policy_test.go).
func withDoubleSignReady() func(codec.Codec, map[string]json.RawMessage) {
	return func(cdc codec.Codec, gen map[string]json.RawMessage) {
		var sg slashingtypes.GenesisState
		cdc.MustUnmarshalJSON(gen[slashingtypes.ModuleName], &sg)
		for i := 0; i < genesisValidators; i++ {
			consAddr := sdk.ConsAddress(newConsKey(string(rune('A' + i))).PubKey().Address())
			sg.SigningInfos = append(sg.SigningInfos, slashingtypes.SigningInfo{
				Address: consAddr.String(),
				ValidatorSigningInfo: slashingtypes.NewValidatorSigningInfo(
					consAddr, 0, 0, time.Unix(0, 0).UTC(), false, 0),
			})
		}
		gen[slashingtypes.ModuleName] = cdc.MustMarshalJSON(&sg)
	}
}

// withSharedEntity puts the named genesis validators (0 = A, 1 = B, 2 = C) in
// one attested entity, legal under W1 from genesis: it deletes their own
// entities (an ATTESTED entity with no member breaks V-12), adds the entity,
// moves their member rows to it, lifts the validator and entity cap floors so
// the shared entity (up to 900,000 SCN) holds its own stake (V-13), and sets the
// seat limit to the member count (at least 2, at most 3).
func withSharedEntity(id string, idx ...int) func(codec.Codec, map[string]json.RawMessage) {
	return func(cdc codec.Codec, gen map[string]json.RawMessage) {
		moved := map[string]bool{}
		for _, i := range idx {
			moved["entity-"+strings.ToLower(string(rune('A'+i)))] = true
		}
		var er entitytypes.GenesisState
		cdc.MustUnmarshalJSON(gen[entitytypes.ModuleName], &er)
		var kept []entitytypes.Entity
		var template entitytypes.Entity
		for _, e := range er.Entities {
			if moved[e.Id] {
				if template.Id == "" {
					template = e
				}
				continue
			}
			kept = append(kept, e)
		}
		template.Id = id
		template.DisplayName = id
		er.Entities = append(kept, template)
		for m := range er.Members {
			if moved[er.Members[m].EntityId] {
				er.Members[m].EntityId = id
				er.Members[m].JoinedHeight = 0
			}
		}
		er.Params.MaxActiveSeats = uint32(max(2, len(idx)))
		gen[entitytypes.ModuleName] = cdc.MustMarshalJSON(&er)

		var se stakeenginetypes.GenesisState
		cdc.MustUnmarshalJSON(gen[stakeenginetypes.ModuleName], &se)
		se.Params.ValidatorCapFloor = scnInt(1_000_000)
		se.Params.EntityCapFloor = scnInt(2_000_000)
		gen[stakeenginetypes.ModuleName] = cdc.MustMarshalJSON(&se)
	}
}

// offence is a genesis validator's duplicate vote at a height, with the
// consensus power it had then.
type offence struct {
	idx    int
	height int64
	time   time.Time
	power  int64
	// cons overrides the genesis validator idx's consensus address, for a
	// validator the test created (validator D).
	cons []byte
}

// consBytes is the consensus address of the offender.
func (c *testChain) consBytes(o offence) []byte {
	if o.cons != nil {
		return o.cons
	}
	return c.consKeys[o.idx].PubKey().Address()
}

// offenceNow is validator idx's duplicate vote at the last committed block,
// with its tokens now as the power.
func (c *testChain) offenceNow(idx int) offence {
	c.t.Helper()
	ctx := c.ctx()
	return offence{
		idx:    idx,
		height: c.height,
		time:   c.time,
		power:  c.tokens(ctx, c.vals[idx].val).Quo(c.app.StakingKeeper.PowerReduction(ctx)).Int64(),
	}
}

// misbehave finalizes and commits one block, one second on, whose Misbehavior
// holds one DUPLICATE_VOTE per offence.
func (c *testChain) misbehave(offs ...offence) *abci.ResponseFinalizeBlock {
	c.t.Helper()
	ctx := c.ctx()
	bonded, err := c.app.StakingKeeper.TotalBondedTokens(ctx)
	require.NoError(c.t, err)
	total := bonded.Quo(c.app.StakingKeeper.PowerReduction(ctx))
	var mb []abci.Misbehavior
	for _, o := range offs {
		mb = append(mb, abci.Misbehavior{
			Type:             abci.MisbehaviorType_DUPLICATE_VOTE,
			Validator:        abci.Validator{Address: c.consBytes(o), Power: o.power},
			Height:           o.height,
			Time:             o.time,
			TotalVotingPower: total.Int64(),
		})
	}
	return c.finalizeBlock(time.Second, mb)
}

// doubleSign is misbehave of each validator's offenceNow.
func (c *testChain) doubleSign(idx ...int) *abci.ResponseFinalizeBlock {
	c.t.Helper()
	offs := make([]offence, len(idx))
	for n, i := range idx {
		offs[n] = c.offenceNow(i)
	}
	return c.misbehave(offs...)
}

// cometEvidence adapts an offence to comet.Evidence, so the test computes the
// hash through x/evidence's own FromABCIEvidence.
type cometEvidence struct {
	o    offence
	addr []byte
}

func (e cometEvidence) Type() comet.MisbehaviorType { return comet.DuplicateVote }
func (e cometEvidence) Validator() comet.Validator  { return cometValidator{e.addr, e.o.power} }
func (e cometEvidence) Height() int64               { return e.o.height }
func (e cometEvidence) Time() time.Time             { return e.o.time }
func (e cometEvidence) TotalVotingPower() int64     { return 0 }

type cometValidator struct {
	addr  []byte
	power int64
}

func (v cometValidator) Address() []byte { return v.addr }
func (v cometValidator) Power() int64    { return v.power }

// hashOfBytes is the 32-byte key x/evidence stores the offence under.
func (c *testChain) hashOfBytes(o offence) []byte {
	c.t.Helper()
	e := cometEvidence{o: o, addr: c.consBytes(o)}
	return evidencetypes.FromABCIEvidence(e, c.app.StakingKeeper.ConsensusAddressCodec()).Hash()
}

// hashOf is the spec §5.3 evidence hash of the offence: 64 uppercase hex.
func (c *testChain) hashOf(o offence) string {
	c.t.Helper()
	return compliancetypes.FormatEvidenceHash(c.hashOfBytes(o))
}

func (c *testChain) consAddr(idx int) sdk.ConsAddress {
	return sdk.ConsAddress(c.consKeys[idx].PubKey().Address())
}

// allOffences lists every Offence x/compliance recorded, in hash order.
func (c *testChain) allOffences() []compliancetypes.Offence {
	c.t.Helper()
	var out []compliancetypes.Offence
	it, err := c.app.ComplianceKeeper.Offences.Iterate(c.ctx(), nil)
	require.NoError(c.t, err)
	defer func() { _ = it.Close() }()
	for ; it.Valid(); it.Next() {
		o, err := it.Value()
		require.NoError(c.t, err)
		out = append(out, o)
	}
	return out
}

func TestDoubleSignReadyHarness(t *testing.T) {
	policy := math.LegacyNewDecWithPrec(10, 2)

	bare := newTestChain(t)
	ctx := bare.ctx()
	require.False(t, bare.app.SlashingKeeper.HasValidatorSigningInfo(ctx, bare.consAddr(0)),
		"the harness writes x/staking directly, so no signing info exists without the mutate")
	got, err := bare.app.SlashingKeeper.SlashFractionDoubleSign(ctx)
	require.NoError(t, err)
	require.Equal(t, policy.String(), got.String(), "the harness inherits the binary's default genesis")

	ready := newTestChain(t, withDoubleSignReady())
	ctx = ready.ctx()
	for i := 0; i < genesisValidators; i++ {
		require.True(t, ready.app.SlashingKeeper.HasValidatorSigningInfo(ctx, ready.consAddr(i)), "validator %d", i)
	}
	got, err = ready.app.SlashingKeeper.SlashFractionDoubleSign(ctx)
	require.NoError(t, err)
	require.Equal(t, policy.String(), got.String())
}

func TestADoubleSignIsRecordedUnderXEvidencesHash(t *testing.T) {
	c := newTestChain(t, withDoubleSignReady(), withSharedEntity("entity-ab", 0, 1))
	c.requireInvariants()
	a, b := c.vals[0].val, c.vals[1].val
	d := math.LegacyNewDecWithPrec(10, 2)

	// A double-signs.
	oa := c.offenceNow(0)
	before := c.tokens(c.ctx(), a)
	res := c.misbehave(oa)

	burn := d.MulInt(sdk.TokensFromConsensusPower(oa.power, c.app.StakingKeeper.PowerReduction(c.ctx()))).TruncateInt()
	require.True(t, burn.IsPositive())
	require.Equal(t, before.Sub(burn), c.tokens(c.ctx(), a), "10% of the power, truncated")
	require.True(t, c.app.SlashingKeeper.IsTombstoned(c.ctx(), c.consAddr(0)))
	require.Equal(t, vrtypes.Status_STATUS_JAILED, c.regStatus(a))

	offences := c.allOffences()
	require.Len(t, offences, 1)
	o := offences[0]
	require.Equal(t, c.hashOf(oa), o.EvidenceHash)
	stored, err := c.app.EvidenceKeeper.Evidences.Has(c.ctx(), c.hashOfBytes(oa))
	require.NoError(t, err)
	require.True(t, stored, "x/evidence stored exactly this hash")
	require.Equal(t, a.String(), o.Validator)
	require.Equal(t, compliancetypes.MisbehaviorKind_MISBEHAVIOR_KIND_DUPLICATE_VOTE, o.Kind)
	require.Equal(t, oa.height, o.InfractionHeight)
	require.Equal(t, oa.power, o.Power)
	require.Equal(t, d.String(), o.DoubleSignFraction.String())
	require.Equal(t, "entity-ab", o.EntityId)
	require.Equal(t, c.height, o.RecordedHeight)

	rec := eventsOf(res, compliancetypes.EventTypeOffenceRecorded)
	require.Len(t, rec, 1)
	require.Equal(t, c.hashOf(oa), attrOf(rec[0], compliancetypes.AttributeKeyEvidenceHash))
	require.Empty(t, eventsOf(res, compliancetypes.EventTypeEntityArmed))

	cs, err := c.app.ComplianceKeeper.CaseOf(c.ctx(), "entity-ab")
	require.NoError(t, err)
	require.False(t, cs.Armed)
	c.requireInvariants()

	// B, of the same entity, double-signs: the entity is armed, in that block.
	ob := c.offenceNow(1)
	res = c.misbehave(ob)
	require.Len(t, c.allOffences(), 2)
	armed := eventsOf(res, compliancetypes.EventTypeEntityArmed)
	require.Len(t, armed, 1, "the armed event is in the block's FinalizeBlock events")
	require.Equal(t, "entity-ab", attrOf(armed[0], compliancetypes.AttributeKeyEntityID))
	// sorted by operator address bytes, which the keys' derivation does not order
	pair := []sdk.ValAddress{a, b}
	if bytes.Compare(pair[0], pair[1]) > 0 {
		pair[0], pair[1] = pair[1], pair[0]
	}
	require.Equal(t, pair[0].String()+","+pair[1].String(), attrOf(armed[0], compliancetypes.AttributeKeyValidators))

	cs, err = c.app.ComplianceKeeper.CaseOf(c.ctx(), "entity-ab")
	require.NoError(t, err)
	require.True(t, cs.Armed)
	require.Equal(t, c.height, cs.ArmedHeight)
	want := []string{c.hashOf(oa), c.hashOf(ob)}
	sort.Strings(want)
	require.Equal(t, want, cs.UnfiredHashes, "both unfired, sorted")
	c.requireInvariants()
}

// A third validator of an already armed entity records its offence and arms
// nothing again; the three-member entity is legal from genesis too.
func TestAThirdDoubleSignOfAnArmedEntityDoesNotArmAgain(t *testing.T) {
	c := newTestChain(t, withDoubleSignReady(), withSharedEntity("entity-abc", 0, 1, 2))
	c.requireInvariants()

	require.Empty(t, eventsOf(c.doubleSign(0), compliancetypes.EventTypeEntityArmed))
	require.Len(t, eventsOf(c.doubleSign(1), compliancetypes.EventTypeEntityArmed), 1)
	armedAt := c.height
	res := c.doubleSign(2)
	require.Len(t, eventsOf(res, compliancetypes.EventTypeOffenceRecorded), 1)
	require.Empty(t, eventsOf(res, compliancetypes.EventTypeEntityArmed))

	cs, err := c.app.ComplianceKeeper.CaseOf(c.ctx(), "entity-abc")
	require.NoError(t, err)
	require.True(t, cs.Armed)
	require.Equal(t, armedAt, cs.ArmedHeight)
	require.Len(t, cs.Validators, 3)
	require.Len(t, cs.Offences, 3)
	c.requireInvariants()
}

// ---------------------------------------------------------------------------
// Task 6: the sanction through real evidence and real proposals.

var (
	dec010 = math.LegacyNewDecWithPrec(10, 2)
	dec020 = math.LegacyNewDecWithPrec(20, 2)
	dec030 = math.LegacyNewDecWithPrec(30, 2)
	dec040 = math.LegacyNewDecWithPrec(40, 2)
	dec050 = math.LegacyNewDecWithPrec(50, 2)
)

// tallyStep is the step of the block that ends a proposal's voting period (the
// harness votes for 10 seconds).
const tallyStep = 30 * time.Second

// requireEarly requires that no participation epoch can have closed, so the
// supply moves only by burns.
func (c *testChain) requireEarly() {
	c.t.Helper()
	require.Less(c.t, c.height, int64(100), "the supply measurements assume no epoch closed")
}

func (c *testChain) supply() math.Int {
	c.t.Helper()
	return c.app.BankKeeper.GetSupply(c.ctx(), stakeenginetypes.BondDenom).Amount
}

// requireCoordinated requires V-15.
func (c *testChain) requireCoordinated() {
	c.t.Helper()
	msg, broken := c.app.ComplianceKeeper.CoordinatedAttackInvariant(c.ctx())
	require.False(c.t, broken, msg)
}

// ctxAfter is a context at the next height with the block time of a block
// stepped by step: the state a slash in that block will see.
func (c *testChain) ctxAfter(step time.Duration) sdk.Context {
	return c.app.NewUncachedContext(false, cmtproto.Header{
		ChainID: testChainID,
		Height:  c.height + 1,
		Time:    c.time.Add(step),
	})
}

func (c *testChain) complianceQuery() compliancetypes.QueryServer {
	return compliancekeeper.NewQueryServerImpl(c.app.ComplianceKeeper)
}

// armed is the ArmedCases query: the armed entities with unfired offences.
func (c *testChain) armed() []compliancetypes.CoordinatedCase {
	c.t.Helper()
	res, err := c.complianceQuery().ArmedCases(c.ctx(), &compliancetypes.QueryArmedCasesRequest{})
	require.NoError(c.t, err)
	return res.Cases
}

// armedHashes is the unfired evidence hashes of an armed entity, nil when the
// entity is not in the armed list.
func (c *testChain) armedHashes(entity string) []string {
	c.t.Helper()
	for _, cs := range c.armed() {
		if cs.EntityId == entity {
			return cs.UnfiredHashes
		}
	}
	return nil
}

// fireMsg is a coordinated-attack sanction with the gov account as authority.
func (c *testChain) fireMsg(entity string, fraction math.LegacyDec, hashes ...string) *compliancetypes.MsgFireCoordinatedSanction {
	return &compliancetypes.MsgFireCoordinatedSanction{
		Authority:      c.govAddr().String(),
		EntityId:       entity,
		EvidenceHashes: hashes,
		Fraction:       fraction,
		Reason:         "ratified in the open",
	}
}

// updateParamsMsg is a MsgUpdateParams from gov that changes only the two
// coordinated-attack parameters.
func (c *testChain) updateParamsMsg(fraction math.LegacyDec, appealSeconds uint64) *compliancetypes.MsgUpdateParams {
	c.t.Helper()
	p, err := c.app.ComplianceKeeper.Params.Get(c.ctx())
	require.NoError(c.t, err)
	p.CoordinatedAttackFraction = fraction
	p.AppealWindowSeconds = appealSeconds
	return &compliancetypes.MsgUpdateParams{Authority: c.govAddr().String(), Params: p}
}

// waitAppeal steps one block over the appeal window and a minute.
func (c *testChain) waitAppeal() {
	c.t.Helper()
	p, err := c.app.ComplianceKeeper.Params.Get(c.ctx())
	require.NoError(c.t, err)
	c.nextBlock(time.Duration(p.AppealWindowSeconds)*time.Second + time.Minute)
}

// deliverOK sends a signed transaction in a block of its own and requires it to succeed.
func (c *testChain) deliverOK(signer acct, msgs ...sdk.Msg) {
	c.t.Helper()
	res := c.deliverTx(signer, msgs...)
	require.Equal(c.t, uint32(0), res.Code, res.Log)
}

// proposeAndVote submits msgs as a proposal (the next proposal id), has every
// operator the harness holds vote YES, and stops one block before the tally:
// the next block, stepped by tallyStep, ends the voting period and executes.
func (c *testChain) proposeAndVote(msgs ...sdk.Msg) uint64 {
	c.t.Helper()
	id, err := c.app.GovKeeper.ProposalID.Peek(c.ctx())
	require.NoError(c.t, err)
	params, err := c.app.GovKeeper.Params.Get(c.ctx())
	require.NoError(c.t, err)

	submit, err := govv1.NewMsgSubmitProposal(msgs, params.MinDeposit, c.delegators[0].addr.String(),
		"", "S8 test proposal", "S8 test proposal", false)
	require.NoError(c.t, err)
	c.deliverOK(c.delegators[0], submit)

	voters := append([]acct{c.fourth}, c.vals[:]...)
	var votes [][]byte
	for _, v := range voters {
		votes = append(votes, c.signedTx(v, govv1.NewMsgVote(v.addr, id, govv1.OptionYes, "")))
	}
	vres := c.nextBlock(time.Second, votes...)
	for _, r := range vres.TxResults {
		require.Equal(c.t, uint32(0), r.Code, r.Log)
	}
	return id
}

// finishProposal runs the block that tallies and executes the proposal, and
// returns its final status and failure reason.
func (c *testChain) finishProposal(id uint64) (govv1.ProposalStatus, string) {
	c.t.Helper()
	c.nextBlock(tallyStep)
	p, err := c.app.GovKeeper.Proposals.Get(c.ctx(), id)
	require.NoError(c.t, err)
	return p.Status, p.FailedReason
}

// runNextProposal is runProposal for the next proposal id.
func (c *testChain) runNextProposal(msgs ...sdk.Msg) (govv1.ProposalStatus, string) {
	c.t.Helper()
	return c.finishProposal(c.proposeAndVote(msgs...))
}

// slashPlan is what x/staking.Slash will do, computed from the state in a
// context without writing, one piece at a time.
type slashPlan struct {
	unbonding     []unbondingPiece
	redelegations []redelegationPiece
	// validator is the validator's own part: the remainder.
	validator math.Int
	// total is every token burned.
	total math.Int
}

// unbondingPiece is one unbonding-delegation entry's loss.
type unbondingPiece struct {
	delegator sdk.AccAddress
	balance   math.Int // before the slash
	burned    math.Int
}

// redelegationPiece is one redelegation entry's loss, taken from the
// destination validator's delegation.
type redelegationPiece struct {
	delegator sdk.AccAddress
	dst       sdk.ValAddress
	shares    math.LegacyDec // unbonded from the destination delegation
	burned    math.Int
}

// slashPlanOf follows x/staking/keeper/slash.go step by step. The base is the
// power at the infraction in tokens; every entry created at or after the
// distribution height and not yet mature counts its theoretical share
// trunc(f * initial) against the validator's part, whatever it really loses;
// the validator loses the remainder, capped by its tokens.
func (c *testChain) slashPlanOf(ctx sdk.Context, consAddr sdk.ConsAddress, distributionHeight, power int64, f math.LegacyDec) slashPlan {
	c.t.Helper()
	sk := c.app.StakingKeeper
	val, err := sk.GetValidatorByConsAddr(ctx, consAddr)
	require.NoError(c.t, err)
	require.False(c.t, val.IsUnbonded(), "x/staking refuses to slash an unbonded validator")
	op, err := sdk.ValAddressFromBech32(val.GetOperator())
	require.NoError(c.t, err)
	now := ctx.BlockHeader().Time

	base := sdk.TokensFromConsensusPower(power, sk.PowerReduction(ctx))
	remaining := f.MulInt(base).TruncateInt()
	plan := slashPlan{total: math.ZeroInt()}

	if distributionHeight < ctx.BlockHeight() {
		uds, err := sk.GetUnbondingDelegationsFromValidator(ctx, op)
		require.NoError(c.t, err)
		for _, ud := range uds {
			delegator := sdk.MustAccAddressFromBech32(ud.DelegatorAddress)
			for _, e := range ud.Entries {
				if e.CreationHeight < distributionHeight || (e.IsMature(now) && !e.OnHold()) {
					continue
				}
				theoretical := f.MulInt(e.InitialBalance).TruncateInt()
				remaining = remaining.Sub(theoretical)
				burned := math.MinInt(theoretical, e.Balance)
				plan.unbonding = append(plan.unbonding, unbondingPiece{delegator: delegator, balance: e.Balance, burned: burned})
				plan.total = plan.total.Add(burned)
			}
		}

		// The destination validators and delegations change entry by entry
		// (each unbond moves tokens and shares), so they are tracked locally: a
		// destination by its address, since every delegator's unbond moves it,
		// and a delegation by (delegator, destination).
		dsts := map[string]stakingtypes.Validator{}
		shares := map[string]math.LegacyDec{}
		reds, err := sk.GetRedelegationsFromSrcValidator(ctx, op)
		require.NoError(c.t, err)
		for _, red := range reds {
			delegator := sdk.MustAccAddressFromBech32(red.DelegatorAddress)
			dstAddr, err := sdk.ValAddressFromBech32(red.ValidatorDstAddress)
			require.NoError(c.t, err)
			_, err = sk.GetUnbondingDelegation(ctx, delegator, dstAddr)
			require.Error(c.t, err, "an undelegation of redelegated stake is not modelled")
			key := red.DelegatorAddress + "/" + red.ValidatorDstAddress
			for _, e := range red.Entries {
				if e.CreationHeight < distributionHeight || (e.IsMature(now) && !e.OnHold()) {
					continue
				}
				theoretical := f.MulInt(e.InitialBalance).TruncateInt()
				remaining = remaining.Sub(theoretical)

				unbond := f.Mul(e.SharesDst)
				if unbond.IsZero() || theoretical.IsZero() {
					continue
				}
				if _, ok := dsts[red.ValidatorDstAddress]; !ok {
					d, err := sk.GetValidator(ctx, dstAddr)
					require.NoError(c.t, err)
					dsts[red.ValidatorDstAddress] = d
				}
				if _, ok := shares[key]; !ok {
					del, err := sk.GetDelegation(ctx, delegator, dstAddr)
					if err != nil {
						shares[key] = math.LegacyZeroDec()
					} else {
						shares[key] = del.Shares
					}
				}
				if !shares[key].IsPositive() {
					continue // the delegation is gone: nothing left to unbond
				}
				if unbond.GT(shares[key]) {
					unbond = shares[key]
				}
				d, tokens := dsts[red.ValidatorDstAddress].RemoveDelShares(unbond)
				dsts[red.ValidatorDstAddress] = d
				shares[key] = shares[key].Sub(unbond)
				plan.redelegations = append(plan.redelegations, redelegationPiece{delegator: delegator, dst: dstAddr, shares: unbond, burned: tokens})
				plan.total = plan.total.Add(tokens)
			}
		}
	}

	plan.validator = math.MaxInt(math.MinInt(remaining, val.Tokens), math.ZeroInt())
	plan.total = plan.total.Add(plan.validator)
	return plan
}

// expectSlash is the total x/staking.Slash will burn, computed without writing.
func (c *testChain) expectSlash(ctx sdk.Context, consAddr sdk.ConsAddress, distributionHeight, power int64, f math.LegacyDec) math.Int {
	c.t.Helper()
	return c.slashPlanOf(ctx, consAddr, distributionHeight, power, f).total
}

// slashPlanAt is the plan for the offence o at fraction f on the state in ctx.
func (c *testChain) slashPlanAt(ctx sdk.Context, o offence, f math.LegacyDec) slashPlan {
	c.t.Helper()
	return c.slashPlanOf(ctx, sdk.ConsAddress(c.consBytes(o)), o.height-sdk.ValidatorUpdateDelay, o.power, f)
}

func (c *testChain) unbondingBalance(delegator sdk.AccAddress, v sdk.ValAddress) math.Int {
	c.t.Helper()
	ud, err := c.app.StakingKeeper.GetUnbondingDelegation(c.ctx(), delegator, v)
	require.NoError(c.t, err)
	require.Len(c.t, ud.Entries, 1)
	return ud.Entries[0].Balance
}

func (c *testChain) delegationShares(delegator sdk.AccAddress, v sdk.ValAddress) math.LegacyDec {
	c.t.Helper()
	d, err := c.app.StakingKeeper.GetDelegation(c.ctx(), delegator, v)
	require.NoError(c.t, err)
	return d.Shares
}

// burnScenario is the non-round stake history of TestTotalBurnIsTheFractionOf
// TheInfractionStake: stake that leaves A after the infraction, by an
// undelegation and by a redelegation to C, so the infraction-height stake is
// spread over A, an unbonding entry and a redelegation.
type burnScenario struct {
	oa, ob offence
}

func (c *testChain) burnScenario() burnScenario {
	c.t.Helper()
	a, third := c.vals[0].val, c.vals[2].val
	d0, d1 := c.delegators[0], c.delegators[1]

	c.deliverOK(d0, msgDelegate(d0.addr.String(), a.String(), scnInt(123_457).AddRaw(7)))
	c.deliverOK(d1, msgDelegate(d1.addr.String(), a.String(), scnInt(98_765).AddRaw(3)))
	// The infraction is at this height, with the powers before anything leaves.
	s := burnScenario{oa: c.offenceNow(0), ob: c.offenceNow(1)}

	c.deliverOK(d0, stakingtypes.NewMsgUndelegate(d0.addr.String(), a.String(),
		sdk.NewCoin(stakeenginetypes.BondDenom, scnInt(37_091).AddRaw(1))))
	c.deliverOK(d1, stakingtypes.NewMsgBeginRedelegate(d1.addr.String(), a.String(), third.String(),
		sdk.NewCoin(stakeenginetypes.BondDenom, scnInt(41_003).AddRaw(9))))
	return s
}

// The reference arithmetic is checked against x/evidence's own 10% burn before
// any other test relies on it.
func TestExpectSlashMatchesXEvidence(t *testing.T) {
	c := newTestChain(t, withDoubleSignReady(), withSharedEntity("entity-ab", 0, 1))
	s := c.burnScenario()
	a, third := c.vals[0].val, c.vals[2].val
	d0, d1 := c.delegators[0], c.delegators[1]
	c.requireEarly()

	plan := c.slashPlanAt(c.ctx(), s.oa, dec010)
	require.Len(t, plan.unbonding, 1)
	require.Len(t, plan.redelegations, 1)
	require.True(t, plan.unbonding[0].burned.IsPositive())
	require.True(t, plan.redelegations[0].burned.IsPositive())
	require.True(t, plan.validator.IsPositive())
	require.Equal(t, plan.total, c.expectSlash(c.ctx(), c.consAddr(0), s.oa.height-1, s.oa.power, dec010))

	ub0 := c.unbondingBalance(d0.addr, a)
	sh0 := c.delegationShares(d1.addr, third)
	tokens0 := c.tokens(c.ctx(), a)
	before := c.supply()

	c.misbehave(s.oa)

	require.Equal(t, plan.total.String(), before.Sub(c.supply()).String(), "x/evidence burned exactly what expectSlash computed")
	require.Equal(t, ub0.Sub(plan.unbonding[0].burned).String(), c.unbondingBalance(d0.addr, a).String())
	require.Equal(t, sh0.Sub(plan.redelegations[0].shares).String(), c.delegationShares(d1.addr, third).String())
	require.Equal(t, tokens0.Sub(plan.validator).String(), c.tokens(c.ctx(), a).String(), "the validator lost the remainder")
	c.requireInvariants()
	c.requireCoordinated()
}

// requireAlive requires the chain to keep producing blocks.
func (c *testChain) requireAlive() {
	c.t.Helper()
	before := c.height
	c.nextBlock(time.Second)
	c.nextBlock(time.Second)
	require.Equal(c.t, before+2, c.app.LastBlockHeight())
}

func (c *testChain) sanction(id uint64) compliancetypes.Sanction {
	c.t.Helper()
	s, err := c.app.ComplianceKeeper.Sanctions.Get(c.ctx(), id)
	require.NoError(c.t, err)
	return s
}

func (c *testChain) requireNoSanction() {
	c.t.Helper()
	n := 0
	require.NoError(c.t, c.app.ComplianceKeeper.Sanctions.Walk(c.ctx(), nil, func(uint64, compliancetypes.Sanction) (bool, error) {
		n++
		return false, nil
	}))
	require.Zero(c.t, n, "no sanction was recorded")
	for _, o := range c.allOffences() {
		require.Zero(c.t, o.SanctionId, "offence %s is unfired", o.EvidenceHash)
	}
}

func (c *testChain) hashesOf(offs ...offence) []string {
	c.t.Helper()
	out := make([]string, len(offs))
	for i, o := range offs {
		out[i] = c.hashOf(o)
	}
	return out
}

// 1. The whole path: a real double-sign, the armed list, the appeal window, a
// real proposal, the exact burn, the records, and a second firing refused.
func TestTheWholePathThroughARealProposal(t *testing.T) {
	c := newTestChain(t, withDoubleSignReady(), withSharedEntity("entity-ab", 0, 1))
	a, b := c.vals[0].val, c.vals[1].val
	c.requireInvariants()

	oa, ob := c.offenceNow(0), c.offenceNow(1)
	c.misbehave(oa, ob)
	hashes := c.hashesOf(oa, ob)

	cases := c.armed()
	require.Len(t, cases, 1)
	require.Equal(t, "entity-ab", cases[0].EntityId)
	want := append([]string(nil), hashes...)
	sort.Strings(want)
	require.Equal(t, want, c.armedHashes("entity-ab"))
	c.requireInvariants()
	c.requireCoordinated()

	c.waitAppeal()
	c.requireEarly()

	msg := c.fireMsg("entity-ab", dec030, hashes...)
	id := c.proposeAndVote(msg)

	// The state just before the proposal's last block.
	ctx := c.ctxAfter(tallyStep)
	planA := c.slashPlanAt(ctx, oa, dec020)
	planB := c.slashPlanAt(ctx, ob, dec020)
	wantBurn := c.expectSlash(ctx, c.consAddr(0), oa.height-1, oa.power, dec020).Add(
		c.expectSlash(ctx, c.consAddr(1), ob.height-1, ob.power, dec020))
	require.True(t, wantBurn.IsPositive())
	require.Equal(t, planA.total.Add(planB.total), wantBurn)
	tokensA, tokensB := c.tokens(c.ctx(), a), c.tokens(c.ctx(), b)
	before := c.supply()

	status, reason := c.finishProposal(id)
	require.Equal(t, govv1.StatusPassed, status, "reason: %s", reason)
	require.Equal(t, wantBurn.String(), before.Sub(c.supply()).String(), "the sanction burned exactly 20% of each infraction-height stake")
	require.Equal(t, tokensA.Sub(planA.validator).String(), c.tokens(c.ctx(), a).String())
	require.Equal(t, tokensB.Sub(planB.validator).String(), c.tokens(c.ctx(), b).String())

	s := c.sanction(1)
	require.Equal(t, "entity-ab", s.EntityId)
	require.Equal(t, dec030.String(), s.Fraction.String())
	require.Equal(t, c.govAddr().String(), s.Authority)
	require.Len(t, s.Targets, 2)
	byValidator := map[string]compliancetypes.SanctionTarget{}
	for _, tg := range s.Targets {
		byValidator[tg.Validator] = tg
		require.Equal(t, compliancetypes.TargetOutcome_TARGET_OUTCOME_SLASHED, tg.Outcome)
		require.Equal(t, dec020.String(), tg.ExtraFraction.String(), "F minus the 10% x/evidence applied")
	}
	require.Equal(t, planA.validator.String(), byValidator[a.String()].ValidatorBurned.String())
	require.Equal(t, planB.validator.String(), byValidator[b.String()].ValidatorBurned.String())

	for i, v := range []sdk.ValAddress{a, b} {
		st, found, err := c.app.ComplianceKeeper.GetCompliance(c.ctx(), v)
		require.NoError(t, err)
		require.True(t, found)
		require.Equal(t, compliancetypes.ComplianceEventType_COMPLIANCE_EVENT_TYPE_COORDINATED_ATTACK, st.LastEvent, "validator %d", i)
		require.False(t, st.Compliant)
		require.Equal(t, vrtypes.Status_STATUS_JAILED, c.regStatus(v))
		require.True(t, c.app.SlashingKeeper.IsTombstoned(c.ctx(), c.consAddr(i)))
	}
	require.Empty(t, c.armedHashes("entity-ab"), "both offences are fired")
	for _, o := range c.allOffences() {
		require.Equal(t, uint64(1), o.SanctionId)
	}
	c.requireInvariants()
	c.requireCoordinated()

	// The same firing again: the offences are spent.
	status, reason = c.runNextProposal(msg)
	require.Equal(t, govv1.StatusFailed, status)
	require.Contains(t, reason, compliancetypes.ErrAlreadySanctioned.Error())
	c.requireAlive()
}

// 2. Review Focus 5: non-round stakes, an unbonding entry and a redelegation
// created after the infraction, each slash against the supply to the unit.
func TestTotalBurnIsTheFractionOfTheInfractionStake(t *testing.T) {
	c := newTestChain(t, withDoubleSignReady(), withSharedEntity("entity-ab", 0, 1))
	a, third := c.vals[0].val, c.vals[2].val
	d0, d1 := c.delegators[0], c.delegators[1]
	s := c.burnScenario()
	c.requireInvariants()
	hashes := c.hashesOf(s.oa, s.ob)

	// pre is the state and the expected pieces of a slash, taken just before it.
	type pre struct {
		planA, planB slashPlan
		ub           math.Int
		shares       math.LegacyDec
		tokensA      math.Int
		supply       math.Int
	}
	snapshot := func(ctx sdk.Context, f math.LegacyDec) pre {
		return pre{
			planA:   c.slashPlanAt(ctx, s.oa, f),
			planB:   c.slashPlanAt(ctx, s.ob, f),
			ub:      c.unbondingBalance(d0.addr, a),
			shares:  c.delegationShares(d1.addr, third),
			tokensA: c.tokens(c.ctx(), a),
			supply:  c.supply(),
		}
	}
	check := func(name string, p pre) {
		t.Helper()
		c.requireEarly()
		require.Len(t, p.planA.unbonding, 1, name)
		require.Len(t, p.planA.redelegations, 1, name)
		require.True(t, p.planB.total.IsPositive(), name)
		require.Equal(t, p.planA.total.Add(p.planB.total).String(), p.supply.Sub(c.supply()).String(),
			"%s: the supply fell by the sum of the two expected burns", name)
		require.Equal(t, p.ub.Sub(p.planA.unbonding[0].burned).String(), c.unbondingBalance(d0.addr, a).String(),
			"%s: the unbonding entry", name)
		require.Equal(t, p.shares.Sub(p.planA.redelegations[0].shares).String(), c.delegationShares(d1.addr, third).String(),
			"%s: the redelegated delegation at C", name)
		require.Equal(t, p.tokensA.Sub(p.planA.validator).String(), c.tokens(c.ctx(), a).String(),
			"%s: A lost the remainder", name)
	}

	// x/evidence's 10%, in the evidence block.
	p1 := snapshot(c.ctx(), dec010)
	c.misbehave(s.oa, s.ob)
	check("x/evidence at 10%", p1)

	// The sanction's 20%, in the proposal's block.
	c.waitAppeal()
	id := c.proposeAndVote(c.fireMsg("entity-ab", dec030, hashes...))
	p2 := snapshot(c.ctxAfter(tallyStep), dec020)
	status, reason := c.finishProposal(id)
	require.Equal(t, govv1.StatusPassed, status, "reason: %s", reason)
	check("the sanction at 20%", p2)

	// A sanity band for the reader: the two slashes together are the single
	// 30% slash of the infraction-height stake, up to truncation.
	bA := sdk.TokensFromConsensusPower(s.oa.power, c.app.StakingKeeper.PowerReduction(c.ctx()))
	bB := sdk.TokensFromConsensusPower(s.ob.power, c.app.StakingKeeper.PowerReduction(c.ctx()))
	single := dec030.MulInt(bA.Add(bB)).TruncateInt()
	got := p1.planA.total.Add(p1.planB.total).Add(p2.planA.total).Add(p2.planB.total)
	require.True(t, got.GTE(single.SubRaw(3)) && got.LTE(single.AddRaw(3)),
		"total %s outside the band around the single 30%% slash %s", got, single)
	t.Logf("two slashes burned %s, one 30%% slash would burn %s", got, single)

	for _, tg := range c.sanction(1).Targets {
		require.Equal(t, dec020.String(), tg.ExtraFraction.String())
		if tg.Validator == a.String() {
			require.Equal(t, p2.planA.validator.String(), tg.ValidatorBurned.String(), "x/staking returned the validator's remainder")
		}
	}
	c.requireInvariants()
	c.requireCoordinated()
}

// 3. Review Focus 2: a redelegation into a locked-up operator's own validator.
func TestTheSanctionSlashesARedelegationIntoALockedOperator(t *testing.T) {
	c := newTestChain(t, withDoubleSignReady(), withSharedEntity("entity-ab", 0, 1))
	a, third := c.vals[0].val, c.vals[2]

	c.deliverOK(third, msgDelegate(third.addr.String(), a.String(), scnInt(10_000)))
	oa, ob := c.offenceNow(0), c.offenceNow(1)
	c.deliverOK(third, stakingtypes.NewMsgBeginRedelegate(third.addr.String(), a.String(), third.val.String(), scnCoin(10_000)))
	require.Equal(t, vrtypes.Status_STATUS_ACTIVE, c.regStatus(third.val))
	c.requireInvariants()

	p1 := c.slashPlanAt(c.ctx(), oa, dec010)
	require.Len(t, p1.redelegations, 1)
	require.Equal(t, third.val, p1.redelegations[0].dst)
	sh0 := c.delegationShares(third.addr, third.val)
	c.misbehave(oa, ob)
	require.Equal(t, sh0.Sub(p1.redelegations[0].shares).String(), c.delegationShares(third.addr, third.val).String(),
		"x/evidence slashed the redelegation inside the BeginBlock window")

	c.waitAppeal()
	id := c.proposeAndVote(c.fireMsg("entity-ab", dec030, c.hashesOf(oa, ob)...))
	p2 := c.slashPlanAt(c.ctxAfter(tallyStep), oa, dec020)
	require.Len(t, p2.redelegations, 1)
	require.True(t, p2.redelegations[0].shares.IsPositive())
	sh1 := c.delegationShares(third.addr, third.val)
	status, reason := c.finishProposal(id)
	require.Equal(t, govv1.StatusPassed, status, "the sanction opens the slash window itself; reason: %s", reason)
	require.Equal(t, sh1.Sub(p2.redelegations[0].shares).String(), c.delegationShares(third.addr, third.val).String(),
		"the sanction unbonded the redelegation piece from the locked-up operator's delegation")
	c.requireInvariants()
	c.requireCoordinated()
}

// 4. Review Focus 1: unrelated validators double-sign in one block.
func TestUnrelatedDoubleSignsNeverArm(t *testing.T) {
	c := newTestChain(t, withDoubleSignReady())
	oa, oc := c.offenceNow(0), c.offenceNow(2)
	ctx := c.ctx()
	want := c.expectSlash(ctx, c.consAddr(0), oa.height-1, oa.power, dec010).Add(
		c.expectSlash(ctx, c.consAddr(2), oc.height-1, oc.power, dec010))
	before := c.supply()

	c.misbehave(oa, oc)
	c.requireEarly()
	require.Equal(t, want.String(), before.Sub(c.supply()).String(), "each slashed by 10% of its stake")
	for _, i := range []int{0, 2} {
		require.True(t, c.app.SlashingKeeper.IsTombstoned(c.ctx(), c.consAddr(i)))
	}
	require.Len(t, c.allOffences(), 2)
	require.Empty(t, c.armed())
	cs, err := c.app.ComplianceKeeper.CaseOf(c.ctx(), "entity-a")
	require.NoError(t, err)
	require.False(t, cs.Armed)

	res, err := c.complianceQuery().SanctionPreview(c.ctx(), &compliancetypes.QuerySanctionPreviewRequest{
		EntityId: "entity-a", EvidenceHashes: []string{c.hashOf(oa)}, Fraction: dec030.String()})
	require.NoError(t, err)
	require.False(t, res.Fireable)
	require.Contains(t, res.Reason, compliancetypes.ErrNotArmed.Error())

	status, reason := c.runNextProposal(c.fireMsg("entity-a", dec030, c.hashOf(oa)))
	require.Equal(t, govv1.StatusFailed, status)
	require.Contains(t, reason, compliancetypes.ErrNotArmed.Error())
	c.requireNoSanction()
	c.requireInvariants()
	c.requireCoordinated()
}

func TestALoneDoubleSignNeverArms(t *testing.T) {
	c := newTestChain(t, withDoubleSignReady(), withSharedEntity("entity-ab", 0, 1))
	oa := c.offenceNow(0)
	c.misbehave(oa)
	require.Empty(t, c.armed())

	status, reason := c.runNextProposal(c.fireMsg("entity-ab", dec030, c.hashOf(oa)))
	require.Equal(t, govv1.StatusFailed, status)
	require.Contains(t, reason, compliancetypes.ErrNotArmed.Error())
	c.requireNoSanction()
	c.requireInvariants()
	c.requireCoordinated()
}

// 5. Review Focus 3: the state moves during the vote.
func TestTooEarlyAndParamChangeAndUnbondedTarget(t *testing.T) {
	t.Run("the appeal window is still open", func(t *testing.T) {
		c := newTestChain(t, withDoubleSignReady(), withSharedEntity("entity-ab", 0, 1))
		oa, ob := c.offenceNow(0), c.offenceNow(1)
		c.misbehave(oa, ob)
		hashes := c.hashesOf(oa, ob)
		msg := c.fireMsg("entity-ab", dec030, hashes...)

		before := c.supply()
		status, reason := c.runNextProposal(msg)
		require.Equal(t, govv1.StatusFailed, status)
		require.Contains(t, reason, compliancetypes.ErrAppealWindowOpen.Error())
		require.Equal(t, before.String(), c.supply().String(), "a refused firing burns nothing")
		c.requireNoSanction()

		c.waitAppeal()
		status, reason = c.runNextProposal(msg)
		require.Equal(t, govv1.StatusPassed, status, "reason: %s", reason)
		require.Len(t, c.sanction(1).Targets, 2)
		c.requireInvariants()
		c.requireCoordinated()
	})

	t.Run("the parameter moves under the firing", func(t *testing.T) {
		c := newTestChain(t, withDoubleSignReady(), withSharedEntity("entity-ab", 0, 1))
		oa, ob := c.offenceNow(0), c.offenceNow(1)
		c.misbehave(oa, ob)
		hashes := c.hashesOf(oa, ob)
		c.waitAppeal()
		params, err := c.app.ComplianceKeeper.Params.Get(c.ctx())
		require.NoError(t, err)
		require.Equal(t, dec030.String(), params.CoordinatedAttackFraction.String())

		// x/gov executes the two messages in order in one cache: the update
		// lands, the firing at the old fraction is refused, and both are undone.
		before := c.supply()
		status, reason := c.runNextProposal(
			c.updateParamsMsg(dec050, params.AppealWindowSeconds),
			c.fireMsg("entity-ab", dec030, hashes...))
		require.Equal(t, govv1.StatusFailed, status)
		require.Contains(t, reason, "does not equal the governed")
		got, err := c.app.ComplianceKeeper.Params.Get(c.ctx())
		require.NoError(t, err)
		require.Equal(t, dec030.String(), got.CoordinatedAttackFraction.String(), "the parameter change was undone with the firing")
		require.Equal(t, before.String(), c.supply().String())
		c.requireNoSanction()
		require.Len(t, c.armedHashes("entity-ab"), 2, "both offences are still unfired")

		// The same bundle with the new fraction in the firing passes, and each
		// target burns the 50% in all: 10% from x/evidence, 40% here.
		id := c.proposeAndVote(
			c.updateParamsMsg(dec050, params.AppealWindowSeconds),
			c.fireMsg("entity-ab", dec050, hashes...))
		ctx := c.ctxAfter(tallyStep)
		planA, planB := c.slashPlanAt(ctx, oa, dec040), c.slashPlanAt(ctx, ob, dec040)
		before = c.supply()
		status, reason = c.finishProposal(id)
		require.Equal(t, govv1.StatusPassed, status, "reason: %s", reason)
		require.Equal(t, planA.total.Add(planB.total).String(), before.Sub(c.supply()).String())
		got, err = c.app.ComplianceKeeper.Params.Get(c.ctx())
		require.NoError(t, err)
		require.Equal(t, dec050.String(), got.CoordinatedAttackFraction.String())
		s := c.sanction(1)
		require.Equal(t, dec050.String(), s.Fraction.String())
		for _, tg := range s.Targets {
			require.Equal(t, dec040.String(), tg.ExtraFraction.String())
		}
		c.requireInvariants()
		c.requireCoordinated()
	})

	t.Run("a target unbonds before the burn", func(t *testing.T) {
		c := newTestChain(t, withDoubleSignReady(), withSharedEntity("entity-ab", 0, 1))
		a, b := c.vals[0].val, c.vals[1].val
		ob := c.offenceNow(1)
		c.misbehave(ob)
		c.nextBlock(20 * 24 * time.Hour)
		oa := c.offenceNow(0)
		c.misbehave(oa) // the entity arms now
		require.Len(t, c.armedHashes("entity-ab"), 2)
		c.waitAppeal()

		valA, err := c.app.StakingKeeper.GetValidator(c.ctx(), a)
		require.NoError(t, err)
		valB, err := c.app.StakingKeeper.GetValidator(c.ctx(), b)
		require.NoError(t, err)
		require.True(t, valA.IsUnbonding(), "A's 21 days have not passed")
		require.True(t, valB.IsUnbonded(), "B's 21 days have")
		c.requireEarly()

		id := c.proposeAndVote(c.fireMsg("entity-ab", dec030, c.hashesOf(oa, ob)...))
		planA := c.slashPlanAt(c.ctxAfter(tallyStep), oa, dec020)
		tokensA, tokensB := c.tokens(c.ctx(), a), c.tokens(c.ctx(), b)
		before := c.supply()
		status, reason := c.finishProposal(id)
		require.Equal(t, govv1.StatusPassed, status, "reason: %s", reason)

		require.Equal(t, planA.total.String(), before.Sub(c.supply()).String(), "only A is burned")
		require.Equal(t, tokensA.Sub(planA.validator).String(), c.tokens(c.ctx(), a).String())
		require.Equal(t, tokensB.String(), c.tokens(c.ctx(), b).String(), "B's tokens are unchanged")
		outcomes := map[string]compliancetypes.TargetOutcome{}
		for _, tg := range c.sanction(1).Targets {
			outcomes[tg.Validator] = tg.Outcome
		}
		require.Equal(t, compliancetypes.TargetOutcome_TARGET_OUTCOME_SLASHED, outcomes[a.String()])
		require.Equal(t, compliancetypes.TargetOutcome_TARGET_OUTCOME_NOT_SLASHABLE, outcomes[b.String()])
		c.requireInvariants()
		c.requireCoordinated()
	})
}

// 6. Review Focus 4: membership changes after the offence still link.
func TestMembershipChangesAfterTheOffenceStillLink(t *testing.T) {
	att := newAcct("attestor")
	fresh := func(t *testing.T) *testChain {
		return newTestChain(t, withDoubleSignReady(), withSharedEntity("entity-ab", 0, 1), withAttestor(att))
	}
	requireLinked := func(c *testChain, offs ...offence) {
		t.Helper()
		for _, o := range offs {
			rec, err := c.app.ComplianceKeeper.Offences.Get(c.ctx(), c.hashOfBytes(o))
			require.NoError(t, err)
			require.Equal(t, "entity-ab", rec.EntityId)
		}
		require.Len(t, c.armedHashes("entity-ab"), len(offs), "the entity is armed")
	}

	t.Run("removed from the entity between the offence and the evidence", func(t *testing.T) {
		c := fresh(t)
		oa, ob := c.offenceNow(0), c.offenceNow(1)
		c.attest(att, "entity-ab", c.vals[0].val) // B leaves
		require.False(t, c.where(c.vals[1].val).Attested)
		c.misbehave(oa, ob)
		requireLinked(c, oa, ob)
		c.requireInvariants()
		c.requireCoordinated()
	})

	t.Run("the entity is revoked between the offence and the evidence", func(t *testing.T) {
		c := fresh(t)
		oa, ob := c.offenceNow(0), c.offenceNow(1)
		c.revoke(att, "entity-ab")
		c.misbehave(oa, ob)
		requireLinked(c, oa, ob)
		c.requireInvariants()
		c.requireCoordinated()
	})

	t.Run("a frozen seat of an entity revoked before the offence", func(t *testing.T) {
		c := fresh(t)
		c.revoke(att, "entity-ab")
		require.NotNil(t, c.where(c.vals[0].val).Frozen)
		oa, ob := c.offenceNow(0), c.offenceNow(1)
		c.misbehave(oa, ob)
		requireLinked(c, oa, ob)

		// The revoked entity can still be sanctioned.
		c.waitAppeal()
		status, reason := c.runNextProposal(c.fireMsg("entity-ab", dec030, c.hashesOf(oa, ob)...))
		require.Equal(t, govv1.StatusPassed, status, "reason: %s", reason)
		require.Len(t, c.sanction(1).Targets, 2)
		c.requireInvariants()
		c.requireCoordinated()
	})

	t.Run("a validator that joined the entity after the offence is not linked", func(t *testing.T) {
		c := fresh(t)
		require.Equal(t, uint32(0), c.createValidatorTx(c.fourth, "fourth", 2_000).Code)
		d := c.fourth.val
		oa := c.offenceNow(0)
		od := offence{idx: -1, cons: newConsKey("fourth").PubKey().Address(), height: c.height, time: c.time,
			power: c.tokens(c.ctx(), d).Quo(c.app.StakingKeeper.PowerReduction(c.ctx())).Int64()}
		require.False(t, c.where(d).Attested, "D was in no entity at the offence")

		c.attest(att, "entity-ab", c.vals[0].val, c.vals[1].val, d)
		require.Equal(t, "entity-ab", c.where(d).EntityId)
		c.misbehave(oa, od)

		recA, err := c.app.ComplianceKeeper.Offences.Get(c.ctx(), c.hashOfBytes(oa))
		require.NoError(t, err)
		require.Equal(t, "entity-ab", recA.EntityId)
		recD, err := c.app.ComplianceKeeper.Offences.Get(c.ctx(), c.hashOfBytes(od))
		require.NoError(t, err)
		require.Equal(t, d.String(), recD.Validator)
		require.Empty(t, recD.EntityId, "D joined after the offence")
		require.Empty(t, c.armed(), "A and D do not arm")
		c.requireInvariants()
		c.requireCoordinated()
	})
}

// 7. The bounds are code: governance moves a parameter only inside them.
func TestGovernanceCannotMoveTheBounds(t *testing.T) {
	c := newTestChain(t, withDoubleSignReady())
	params, err := c.app.ComplianceKeeper.Params.Get(c.ctx())
	require.NoError(t, err)
	unchanged := func() {
		t.Helper()
		got, err := c.app.ComplianceKeeper.Params.Get(c.ctx())
		require.NoError(t, err)
		require.Equal(t, params, got, "the failed proposal changed nothing")
	}
	tooHigh, err := math.LegacyNewDecFromStr("1.000000000000000001")
	require.NoError(t, err)

	cases := []struct {
		name     string
		fraction math.LegacyDec
		appeal   uint64
		want     string
	}{
		{"fraction below the floor", math.LegacyNewDecWithPrec(5, 2), params.AppealWindowSeconds, compliancetypes.ErrInvalidFraction.Error()},
		{"fraction above the ceiling", tooHigh, params.AppealWindowSeconds, compliancetypes.ErrInvalidFraction.Error()},
		{"appeal window below a day", params.CoordinatedAttackFraction, 3_600, compliancetypes.ErrInvalidParams.Error()},
		{"appeal window above two weeks", params.CoordinatedAttackFraction, compliancetypes.MaxAppealWindowSeconds + 1, compliancetypes.ErrInvalidParams.Error()},
	}
	for _, tc := range cases {
		status, reason := c.runNextProposal(c.updateParamsMsg(tc.fraction, tc.appeal))
		require.Equal(t, govv1.StatusFailed, status, tc.name)
		require.Contains(t, reason, tc.want, tc.name)
		unchanged()
	}

	// The ceiling is inclusive.
	status, reason := c.runNextProposal(c.updateParamsMsg(math.LegacyOneDec(), compliancetypes.MinAppealWindowSeconds))
	require.Equal(t, govv1.StatusPassed, status, "reason: %s", reason)
	got, err := c.app.ComplianceKeeper.Params.Get(c.ctx())
	require.NoError(t, err)
	require.Equal(t, math.LegacyOneDec().String(), got.CoordinatedAttackFraction.String())
	require.Equal(t, compliancetypes.MinAppealWindowSeconds, got.AppealWindowSeconds, "the floor is inclusive too")
	c.requireAlive()
	c.requireInvariants()
	c.requireCoordinated()
}

// withoutRegistryMetadata deletes the x/validatorregistry record of genesis
// validator idx and keeps it in x/staking: a validator the registry never saw.
func withoutRegistryMetadata(idx int) func(codec.Codec, map[string]json.RawMessage) {
	return func(cdc codec.Codec, gen map[string]json.RawMessage) {
		drop := newAcct("validator" + string(rune('A'+idx))).val.String()
		var vr vrtypes.GenesisState
		cdc.MustUnmarshalJSON(gen[vrtypes.ModuleName], &vr)
		var kept []vrtypes.ValidatorMetadata
		for _, md := range vr.Validators {
			if md.ValidatorAddress != drop {
				kept = append(kept, md)
			}
		}
		vr.Validators = kept
		gen[vrtypes.ModuleName] = cdc.MustMarshalJSON(&vr)
	}
}

// 8. A target whose operator has no x/validatorregistry record is slashed as
// planned and not classified: the registry would refuse the C-flag write, and
// that refusal must not fail the firing of the other targets (spec §8.6).
func TestATargetWithoutARegistryRecordIsSlashedButNotClassified(t *testing.T) {
	c := newTestChain(t, withDoubleSignReady(), withSharedEntity("entity-ab", 0, 1), withoutRegistryMetadata(1))
	a, b := c.vals[0].val, c.vals[1].val
	_, found, err := c.app.ValidatorregistryKeeper.GetValidatorStatus(c.ctx(), b)
	require.NoError(t, err)
	require.False(t, found, "B has no registry record")

	oa, ob := c.offenceNow(0), c.offenceNow(1)
	c.misbehave(oa, ob)
	require.True(t, c.app.SlashingKeeper.IsTombstoned(c.ctx(), c.consAddr(1)), "x/evidence still punished B")
	require.Len(t, c.armedHashes("entity-ab"), 2, "and the entity is armed")
	c.waitAppeal()
	c.requireEarly()

	res, err := c.complianceQuery().SanctionPreview(c.ctx(), &compliancetypes.QuerySanctionPreviewRequest{
		EntityId: "entity-ab", EvidenceHashes: c.hashesOf(oa, ob), Fraction: dec030.String()})
	require.NoError(t, err)
	require.True(t, res.Fireable, res.Reason)

	_, foundB, err := c.app.ComplianceKeeper.GetCompliance(c.ctx(), b)
	require.NoError(t, err)
	require.False(t, foundB, "the double-sign hook skipped B too")
	id := c.proposeAndVote(c.fireMsg("entity-ab", dec030, c.hashesOf(oa, ob)...))
	ctx := c.ctxAfter(tallyStep)
	want := c.slashPlanAt(ctx, oa, dec020).total.Add(c.slashPlanAt(ctx, ob, dec020).total)
	before := c.supply()
	status, reason := c.finishProposal(id)
	require.Equal(t, govv1.StatusPassed, status, "reason: %s", reason)
	require.Equal(t, want.String(), before.Sub(c.supply()).String(), "both targets are slashed")

	byValidator := map[string]compliancetypes.SanctionTarget{}
	for _, tg := range c.sanction(1).Targets {
		byValidator[tg.Validator] = tg
		require.Equal(t, compliancetypes.TargetOutcome_TARGET_OUTCOME_SLASHED, tg.Outcome)
	}
	require.Equal(t, compliancetypes.TargetClassification_TARGET_CLASSIFICATION_CLASSIFIED, byValidator[a.String()].Classification)
	require.Equal(t, compliancetypes.TargetClassification_TARGET_CLASSIFICATION_SKIPPED_NO_REGISTRY_RECORD, byValidator[b.String()].Classification)
	stA, _, err := c.app.ComplianceKeeper.GetCompliance(c.ctx(), a)
	require.NoError(t, err)
	require.Equal(t, compliancetypes.ComplianceEventType_COMPLIANCE_EVENT_TYPE_COORDINATED_ATTACK, stA.LastEvent)
	_, foundB, err = c.app.ComplianceKeeper.GetCompliance(c.ctx(), b)
	require.NoError(t, err)
	require.False(t, foundB, "B is not classified")
	c.requireInvariants()
	c.requireCoordinated()
	c.requireAlive()
}

// 9. A sanction fails as one unit. The proposal fires both offences and then
// fires A's again: the second message is refused after the first one burned
// both targets in x/gov's cache, the proposal ends FAILED, and nothing of the
// first message survives. The same firing alone then passes on the unchanged
// state, which shows the first message did burn before it was undone.
func TestASanctionIsAtomicWhenALaterMessageFails(t *testing.T) {
	c := newTestChain(t, withDoubleSignReady(), withSharedEntity("entity-ab", 0, 1))
	a, b := c.vals[0].val, c.vals[1].val
	oa, ob := c.offenceNow(0), c.offenceNow(1)
	c.misbehave(oa, ob)
	c.waitAppeal()
	c.requireEarly()

	stateA, _, err := c.app.ComplianceKeeper.GetCompliance(c.ctx(), a)
	require.NoError(t, err)
	tokensA, tokensB := c.tokens(c.ctx(), a), c.tokens(c.ctx(), b)
	before := c.supply()
	fire := c.fireMsg("entity-ab", dec030, c.hashesOf(oa, ob)...)
	status, reason := c.runNextProposal(fire, c.fireMsg("entity-ab", dec030, c.hashOf(oa)))
	require.Equal(t, govv1.StatusFailed, status)
	require.Contains(t, reason, compliancetypes.ErrAlreadySanctioned.Error())

	require.Equal(t, before.String(), c.supply().String(), "no burn survived, on either target")
	require.Equal(t, tokensA.String(), c.tokens(c.ctx(), a).String())
	require.Equal(t, tokensB.String(), c.tokens(c.ctx(), b).String())
	after, _, err := c.app.ComplianceKeeper.GetCompliance(c.ctx(), a)
	require.NoError(t, err)
	require.Equal(t, stateA, after, "A's compliance record is untouched")
	require.Equal(t, compliancetypes.ComplianceEventType_COMPLIANCE_EVENT_TYPE_CRITICAL_VIOLATION, after.LastEvent)
	c.requireNoSanction()
	c.requireInvariants()
	c.requireCoordinated()

	id := c.proposeAndVote(fire)
	ctx := c.ctxAfter(tallyStep)
	want := c.slashPlanAt(ctx, oa, dec020).total.Add(c.slashPlanAt(ctx, ob, dec020).total)
	require.True(t, want.IsPositive())
	before = c.supply()
	status, reason = c.finishProposal(id)
	require.Equal(t, govv1.StatusPassed, status, "reason: %s", reason)
	require.Equal(t, want.String(), before.Sub(c.supply()).String())
	c.requireAlive()
}

// 10. The Critical case of the final review (spec §8.3): A double-signs, fully
// exits and x/staking removes it; a validator is then created on A's tombstoned
// consensus key, by A's own operator or by another; B double-signs much later,
// which arms the entity. The firing burns nothing on the new incarnation and
// classifies nothing, and B is sanctioned as usual.
func TestARecreatedValidatorOnTheOldKeyIsNotSanctioned(t *testing.T) {
	for _, sameOperator := range []bool{true, false} {
		name := "another operator"
		if sameOperator {
			name = "the same operator"
		}
		t.Run(name, func(t *testing.T) {
			c := newTestChain(t, withDoubleSignReady(), withSharedEntity("entity-ab", 0, 1))
			a, b := c.vals[0], c.vals[1].val
			oa := c.offenceNow(0)
			c.misbehave(oa)
			require.True(t, c.app.SlashingKeeper.IsTombstoned(c.ctx(), c.consAddr(0)))
			require.Empty(t, c.armed(), "one offender does not arm")

			// Past the 60-day lock-up and the 21-day unbonding, A's operator takes
			// everything out; the unbonded validator with no shares is removed.
			c.nextBlock(61 * 24 * time.Hour)
			old, err := c.app.StakingKeeper.GetValidator(c.ctx(), a.val)
			require.NoError(t, err)
			require.True(t, old.IsUnbonded())
			c.deliverOK(a, stakingtypes.NewMsgUndelegate(a.addr.String(), a.val.String(),
				sdk.NewCoin(stakeenginetypes.BondDenom, old.Tokens)))
			_, err = c.app.StakingKeeper.GetValidator(c.ctx(), a.val)
			require.ErrorIs(t, err, stakingtypes.ErrNoValidatorFound, "x/staking removed A")
			removed, err := c.app.ComplianceKeeper.RemovedConsensus.Has(c.ctx(), c.consAddr(0))
			require.NoError(t, err)
			require.True(t, removed, "the removal hook recorded A's tombstoned key")

			// A validator on A's old consensus key.
			creator := a
			if !sameOperator {
				creator = c.delegators[2]
			}
			require.Equal(t, uint32(0), c.createValidatorTx(creator, "A", 20_000).Code)
			c.nextBlock(time.Second)
			reborn, err := c.app.StakingKeeper.GetValidatorByConsAddr(c.ctx(), c.consAddr(0))
			require.NoError(t, err)
			require.Equal(t, creator.val.String(), reborn.GetOperator())
			require.True(t, reborn.IsBonded(), "the new incarnation is bonded: there is stake a slash would burn")
			require.True(t, c.app.SlashingKeeper.IsTombstoned(c.ctx(), c.consAddr(0)), "and its key is still tombstoned")

			ob := c.offenceNow(1)
			c.misbehave(ob)
			require.Len(t, c.armedHashes("entity-ab"), 2, "B's late offence arms the entity")
			c.waitAppeal()
			c.requireEarly()
			c.requireInvariants()
			c.requireCoordinated()

			hashes := c.hashesOf(oa, ob)
			res, err := c.complianceQuery().SanctionPreview(c.ctx(), &compliancetypes.QuerySanctionPreviewRequest{
				EntityId: "entity-ab", EvidenceHashes: hashes, Fraction: dec030.String()})
			require.NoError(t, err)
			require.True(t, res.Fireable, res.Reason)

			regBefore := c.regStatus(creator.val)
			stBefore, foundBefore, err := c.app.ComplianceKeeper.GetCompliance(c.ctx(), creator.val)
			require.NoError(t, err)
			tokensReborn := c.tokens(c.ctx(), creator.val)
			id := c.proposeAndVote(c.fireMsg("entity-ab", dec030, hashes...))
			wantB := c.slashPlanAt(c.ctxAfter(tallyStep), ob, dec020).total
			before := c.supply()
			status, reason := c.finishProposal(id)
			require.Equal(t, govv1.StatusPassed, status, "reason: %s", reason)

			require.Equal(t, wantB.String(), before.Sub(c.supply()).String(), "only B is burned")
			require.Equal(t, tokensReborn.String(), c.tokens(c.ctx(), creator.val).String(), "the new incarnation keeps its stake")
			require.Equal(t, regBefore, c.regStatus(creator.val), "and its registry status")
			stAfter, foundAfter, err := c.app.ComplianceKeeper.GetCompliance(c.ctx(), creator.val)
			require.NoError(t, err)
			require.Equal(t, foundBefore, foundAfter)
			require.Equal(t, stBefore, stAfter, "and its compliance record")

			byValidator := map[string]compliancetypes.SanctionTarget{}
			for _, tg := range c.sanction(1).Targets {
				byValidator[tg.Validator] = tg
			}
			require.Equal(t, compliancetypes.TargetOutcome_TARGET_OUTCOME_NOT_SLASHABLE, byValidator[a.val.String()].Outcome)
			require.True(t, byValidator[a.val.String()].ValidatorBurned.IsZero())
			require.Equal(t, compliancetypes.TargetClassification_TARGET_CLASSIFICATION_SKIPPED_KEY_REMOVED, byValidator[a.val.String()].Classification)
			require.Equal(t, compliancetypes.TargetOutcome_TARGET_OUTCOME_SLASHED, byValidator[b.String()].Outcome)
			stB, _, err := c.app.ComplianceKeeper.GetCompliance(c.ctx(), b)
			require.NoError(t, err)
			require.Equal(t, compliancetypes.ComplianceEventType_COMPLIANCE_EVENT_TYPE_COORDINATED_ATTACK, stB.LastEvent)
			c.requireInvariants()
			c.requireCoordinated()

			exported, err := c.app.ComplianceKeeper.ExportGenesis(c.ctx())
			require.NoError(t, err)
			require.Equal(t, []string{c.consAddr(0).String()}, exported.RemovedConsensusAddresses, "the record travels in genesis")
			require.NoError(t, exported.Validate())
		})
	}
}

// 11. Gas, measured and bounded (spec §19). Validator D keeps a bonded validator
// after A, B and C are jailed, so the proposals can still be voted.
func TestGasOfTheCoordinatedPath(t *testing.T) {
	att := newAcct("attestor")
	c := newTestChain(t, withDoubleSignReady(), withSharedEntity("entity-abc", 0, 1, 2), withAttestor(att))
	require.Equal(t, uint32(0), c.createValidatorTx(c.fourth, "fourth", 2_000).Code)
	c.attest(att, "entity-d", c.fourth.val)
	require.Equal(t, vrtypes.Status_STATUS_ACTIVE, c.regStatus(c.fourth.val))

	oa, ob, oc := c.offenceNow(0), c.offenceNow(1), c.offenceNow(2)
	c.misbehave(oa, ob, oc)
	offs := []offence{oa, ob, oc}
	hashes := c.hashesOf(offs...)
	require.Len(t, c.armedHashes("entity-abc"), 3)

	log := func(name string, used uint64, bound uint64) {
		t.Helper()
		t.Logf("GAS %-58s used %9d  (bound < %d)", name, used, bound)
		require.Less(t, used, bound, name)
	}
	fresh := func(ctx sdk.Context) sdk.Context {
		return ctx.WithGasMeter(storetypes.NewGasMeter(gomath.MaxUint64))
	}

	// Recording one offence: delete its record in a cache, rebuild the comet
	// info of the evidence block and run the BeginBlock recorder again.
	var worst uint64
	for i, o := range offs {
		cache, _ := c.ctx().CacheContext()
		hash := c.hashOfBytes(o)
		rec, err := c.app.ComplianceKeeper.Offences.Get(cache, hash)
		require.NoError(t, err)
		require.NoError(t, c.app.ComplianceKeeper.Offences.Remove(cache, hash))
		require.NoError(t, c.app.ComplianceKeeper.OffencesByEntity.Remove(cache, collections.Join(rec.EntityId, hash)))
		cache = fresh(cache).WithCometInfo(cometBlock{ev: []comet.Evidence{
			cometEvidence{o: o, addr: c.consBytes(o)}}})
		c.app.ComplianceKeeper.RecordOffences(cache)
		again, err := c.app.ComplianceKeeper.Offences.Has(cache, hash)
		require.NoError(t, err)
		require.True(t, again, "offence %d was recorded again", i)
		worst = max(worst, cache.GasMeter().GasConsumed())
	}
	log("record one offence (BeginBlock, worst of three)", worst, 200_000)

	c.waitAppeal()
	for _, n := range []int{2, 3} {
		cache, _ := c.ctx().CacheContext()
		cache = fresh(cache)
		require.NoError(t, c.deliverViaRouter(cache, c.fireMsg("entity-abc", dec030, hashes[:n]...)))
		log("MsgFireCoordinatedSanction handler, "+string(rune('0'+n))+" targets", cache.GasMeter().GasConsumed(), 1_500_000)
		_, err := c.app.ComplianceKeeper.Sanctions.Get(cache, 1)
		require.NoError(t, err, "the firing really executed")
	}

	// The preview query, through the gRPC query router.
	req, err := (&compliancetypes.QuerySanctionPreviewRequest{
		EntityId: "entity-abc", EvidenceHashes: hashes, Fraction: dec030.String()}).Marshal()
	require.NoError(t, err)
	route := c.app.GRPCQueryRouter().Route("/chain.compliance.v1.Query/SanctionPreview")
	require.NotNil(t, route)
	qctx := fresh(c.ctx())
	raw, err := route(qctx, &abci.RequestQuery{Data: req})
	require.NoError(t, err)
	var preview compliancetypes.QuerySanctionPreviewResponse
	require.NoError(t, preview.Unmarshal(raw.Value))
	require.True(t, preview.Fireable, preview.Reason)
	require.Len(t, preview.Plan.Targets, 3)
	log("SanctionPreview query, 3 hashes", qctx.GasMeter().GasConsumed(), 300_000)

	// MsgSubmitProposal carrying a firing of two hashes.
	params, err := c.app.GovKeeper.Params.Get(c.ctx())
	require.NoError(t, err)
	submit, err := govv1.NewMsgSubmitProposal([]sdk.Msg{c.fireMsg("entity-abc", dec030, hashes[:2]...)},
		params.MinDeposit, c.delegators[0].addr.String(), "", "gas", "gas", false)
	require.NoError(t, err)
	res := c.deliverTx(c.delegators[0], submit)
	require.Equal(t, uint32(0), res.Code, res.Log)
	log("MsgSubmitProposal carrying a 2-hash firing", uint64(res.GasUsed), 300_000)
}

// cometBlock is the comet.BlockInfo of a BeginBlock whose Misbehavior list is ev.
type cometBlock struct{ ev []comet.Evidence }

func (b cometBlock) GetEvidence() comet.EvidenceList { return cometEvidenceList(b.ev) }
func (b cometBlock) GetValidatorsHash() []byte       { return nil }
func (b cometBlock) GetProposerAddress() []byte      { return nil }
func (b cometBlock) GetLastCommit() comet.CommitInfo { return nil }

type cometEvidenceList []comet.Evidence

func (l cometEvidenceList) Len() int                 { return len(l) }
func (l cometEvidenceList) Get(i int) comet.Evidence { return l[i] }
