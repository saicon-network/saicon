package keeper_test

import (
	"bytes"
	"context"
	"encoding/hex"
	"errors"
	"sort"
	"testing"

	"cosmossdk.io/math"
	storetypes "cosmossdk.io/store/types"
	addresscodec "github.com/cosmos/cosmos-sdk/codec/address"
	"github.com/cosmos/cosmos-sdk/runtime"
	"github.com/cosmos/cosmos-sdk/testutil"
	sdk "github.com/cosmos/cosmos-sdk/types"
	moduletestutil "github.com/cosmos/cosmos-sdk/types/module/testutil"
	authtypes "github.com/cosmos/cosmos-sdk/x/auth/types"
	"github.com/stretchr/testify/require"

	"github.com/saicon-network/saicon/chain/crypto/vrf"
	entitytypes "github.com/saicon-network/saicon/chain/x/entityregistry/types"
	"github.com/saicon-network/saicon/chain/x/randomness/keeper"
	"github.com/saicon-network/saicon/chain/x/randomness/types"
	stakeenginetypes "github.com/saicon-network/saicon/chain/x/stakeengine/types"
	vrtypes "github.com/saicon-network/saicon/chain/x/validatorregistry/types"
)

const testChainID = "saicon-test"

func acc(b byte) sdk.AccAddress {
	return sdk.AccAddress([]byte{b, 1, 2, 3, 4, 5, 6, 7, 8, 9, 10, 11, 12, 13, 14, 15, 16, 17, 18, 19})
}

func val(b byte) sdk.ValAddress { return sdk.ValAddress(acc(b)) }

// ---- stubs ------------------------------------------------------------------

type stubEntities struct {
	tickets     []entitytypes.Ticket          // returned as is (W1 sorts by id)
	members     map[string]entitytypes.Member // valoper string → member row
	failTickets bool
}

func (s *stubEntities) Tickets(context.Context) ([]entitytypes.Ticket, error) {
	if s.failTickets {
		return nil, errors.New("stub: tickets read failed")
	}
	return append([]entitytypes.Ticket(nil), s.tickets...), nil
}

func (s *stubEntities) EntityOf(_ context.Context, v sdk.ValAddress) (entitytypes.Member, bool, error) {
	m, ok := s.members[v.String()]
	return m, ok, nil
}

type stubRegistry struct{ status map[string]vrtypes.Status }

func (r *stubRegistry) GetValidatorStatus(_ context.Context, v sdk.ValAddress) (vrtypes.Status, bool, error) {
	s, ok := r.status[v.String()]
	return s, ok, nil
}

type stubStake struct {
	standing map[string]stakeenginetypes.EntityStanding // valoper string → standing
	fail     bool
}

func (s *stubStake) EntityStanding(_ context.Context, v sdk.ValAddress) (stakeenginetypes.EntityStanding, error) {
	if s.fail {
		return stakeenginetypes.EntityStanding{}, errors.New("stub: standing read failed")
	}
	return s.standing[v.String()], nil
}

type stubParticipation struct{ epochLen uint64 }

func (p *stubParticipation) EpochLengthBlocks(context.Context) (uint64, error) {
	return p.epochLen, nil
}

// ---- fixture ----------------------------------------------------------------

type fixture struct {
	t     *testing.T
	ctx   sdk.Context
	k     keeper.Keeper
	ents  *stubEntities
	reg   *stubRegistry
	stake *stubStake
	part  *stubParticipation
}

// newFixture: params {rotation_epochs 2, window_blocks 10}, epoch length 5
// (rotations on multiples of 10), round 1 written unscheduled, chain id
// "saicon-test", block height 1.
func newFixture(t *testing.T) *fixture {
	t.Helper()
	encCfg := moduletestutil.MakeTestEncodingConfig()
	storeKey := storetypes.NewKVStoreKey(types.StoreKey)
	ctx := testutil.DefaultContextWithKeys(
		map[string]*storetypes.KVStoreKey{types.StoreKey: storeKey},
		map[string]*storetypes.TransientStoreKey{},
		nil,
	).WithChainID(testChainID).WithBlockHeight(1)

	f := &fixture{
		t:     t,
		ctx:   ctx,
		ents:  &stubEntities{members: map[string]entitytypes.Member{}},
		reg:   &stubRegistry{status: map[string]vrtypes.Status{}},
		stake: &stubStake{standing: map[string]stakeenginetypes.EntityStanding{}},
		part:  &stubParticipation{epochLen: 5},
	}
	f.k = keeper.NewKeeper(
		runtime.NewKVStoreService(storeKey), encCfg.Codec,
		addresscodec.NewBech32Codec(sdk.GetConfig().GetBech32AccountAddrPrefix()),
		authtypes.NewModuleAddress(types.GovModuleName),
		f.ents, f.reg, f.stake, f.part,
	)
	require.NoError(t, f.k.Params.Set(ctx, types.Params{RotationEpochs: 2, WindowBlocks: 10}))
	require.NoError(t, f.k.CurrentRound.Set(ctx, types.FirstRound(types.DefaultInitialInput())))
	return f
}

// at returns the context at height h (the event manager is shared).
func (f *fixture) at(h int64) sdk.Context { return f.ctx.WithBlockHeight(h) }

// run calls EndBlock at every height in [from, to].
func (f *fixture) run(from, to int64) {
	f.t.Helper()
	for h := from; h <= to; h++ {
		require.NoError(f.t, f.k.EndBlock(f.at(h)), "EndBlock at %d", h)
	}
}

// entity appends a ticket and the member rows (no key, standing within cap).
// Tickets stay sorted by id and members by bytes.
func (f *fixture) entity(id string, members ...sdk.ValAddress) {
	f.t.Helper()
	sorted := append([]sdk.ValAddress(nil), members...)
	sort.Slice(sorted, func(i, j int) bool { return bytes.Compare(sorted[i], sorted[j]) < 0 })
	tk := entitytypes.Ticket{EntityId: id, Tickets: 1}
	for _, v := range sorted {
		tk.Members = append(tk.Members, v.String())
		f.ents.members[v.String()] = entitytypes.Member{Validator: v.String(), EntityId: id, AttestedHeight: 1}
		f.stake.standing[v.String()] = stakeenginetypes.EntityStanding{EntityID: id, Tokens: math.NewInt(1), Cap: math.NewInt(10)}
	}
	f.ents.tickets = append(f.ents.tickets, tk)
	sort.Slice(f.ents.tickets, func(i, j int) bool { return f.ents.tickets[i].EntityId < f.ents.tickets[j].EntityId })
}

// standing overrides the standing reported for every member of an entity.
func (f *fixture) standing(id string, tokens, limit int64, frozen bool) {
	for _, m := range f.ents.members {
		if m.EntityId == id {
			f.stake.standing[m.Validator] = stakeenginetypes.EntityStanding{
				EntityID: id, Frozen: frozen, Tokens: math.NewInt(tokens), Cap: math.NewInt(limit),
			}
		}
	}
}

// key binds a deterministic VRF key to a member (seed byte, bound at height).
func (f *fixture) key(v sdk.ValAddress, seed byte, height int64) vrf.PrivateKey {
	f.t.Helper()
	k, err := vrf.NewKeyFromSeed(bytes.Repeat([]byte{seed}, vrf.SeedSize))
	require.NoError(f.t, err)
	m, ok := f.ents.members[v.String()]
	require.True(f.t, ok, "no member row for %s", v)
	m.VrfPublicKey = hex.EncodeToString(k.PublicKey())
	m.VrfKeyHeight = height
	f.ents.members[v.String()] = m
	return k
}

// active marks validators registry-ACTIVE.
func (f *fixture) active(vs ...sdk.ValAddress) {
	for _, v := range vs {
		f.reg.status[v.String()] = vrtypes.Status_STATUS_ACTIVE
	}
}

// round reads the current round.
func (f *fixture) round() types.Round {
	f.t.Helper()
	r, err := f.k.CurrentRound.Get(f.ctx)
	require.NoError(f.t, err)
	return r
}

// draw reads the closed round n from the unexported history.
func (f *fixture) draw(n uint64) types.Draw {
	f.t.Helper()
	d, err := keeper.Draws(f.k).Get(f.ctx, n)
	require.NoError(f.t, err, "draw %d", n)
	return d
}

// submit writes the submission of the designated validator v made with key k
// at height h, straight into state (Task 6 adds the message server).
func (f *fixture) submit(h int64, v sdk.ValAddress, k vrf.PrivateKey) {
	f.t.Helper()
	r := f.round()
	id, err := f.k.Designated.Get(f.ctx, v)
	require.NoError(f.t, err, "%s is not designated", v)
	pe, err := f.k.Pool.Get(f.ctx, id)
	require.NoError(f.t, err)
	input, err := types.DecodeHex(r.Input, 32)
	require.NoError(f.t, err)
	proof := k.Prove(types.RoundAlpha(testChainID, r.Number, input))
	pk, err := types.DecodeHex(pe.VrfPublicKey, vrf.PublicKeySize)
	require.NoError(f.t, err)
	out, err := vrf.Verify(pk, proof, types.RoundAlpha(testChainID, r.Number, input))
	require.NoError(f.t, err)
	require.NoError(f.t, f.k.Submissions.Set(f.ctx, id, types.Submission{
		EntityId: id, Validator: v.String(),
		Output: hex.EncodeToString(out), Proof: hex.EncodeToString(proof), Height: h,
	}))
}

// requireEvent fails unless an event of type typ carries attribute key=value.
func requireEvent(t *testing.T, ctx sdk.Context, typ, key, value string) {
	t.Helper()
	for _, ev := range ctx.EventManager().Events() {
		if ev.Type != typ {
			continue
		}
		for _, a := range ev.Attributes {
			if a.Key == key && a.Value == value {
				return
			}
		}
	}
	t.Fatalf("no %s event with %s=%s", typ, key, value)
}

// poolIDs lists the pool's entity ids in key order.
func (f *fixture) poolIDs() []string {
	f.t.Helper()
	var ids []string
	require.NoError(f.t, f.k.Pool.Walk(f.ctx, nil, func(id string, _ types.PoolEntry) (bool, error) {
		ids = append(ids, id)
		return false, nil
	}))
	return ids
}

// outputsOf lists the submissions' outputs, decoded, in key order.
func (f *fixture) outputsOf() [][]byte {
	f.t.Helper()
	var outs [][]byte
	require.NoError(f.t, f.k.Submissions.Walk(f.ctx, nil, func(_ string, s types.Submission) (bool, error) {
		b, err := hex.DecodeString(s.Output)
		require.NoError(f.t, err)
		outs = append(outs, b)
		return false, nil
	}))
	return outs
}
