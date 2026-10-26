package keeper_test

import (
	"context"
	"testing"

	"cosmossdk.io/math"
	storetypes "cosmossdk.io/store/types"
	addresscodec "github.com/cosmos/cosmos-sdk/codec/address"
	"github.com/cosmos/cosmos-sdk/runtime"
	"github.com/cosmos/cosmos-sdk/testutil"
	sdk "github.com/cosmos/cosmos-sdk/types"
	moduletestutil "github.com/cosmos/cosmos-sdk/types/module/testutil"
	authtypes "github.com/cosmos/cosmos-sdk/x/auth/types"
	stakingtypes "github.com/cosmos/cosmos-sdk/x/staking/types"

	"github.com/saicon-network/saicon/chain/x/delegationrouter/keeper"
	"github.com/saicon-network/saicon/chain/x/delegationrouter/types"
	participationtypes "github.com/saicon-network/saicon/chain/x/participation/types"
	vrtypes "github.com/saicon-network/saicon/chain/x/validatorregistry/types"
)

const scn = int64(100_000_000)

func nscn(s int64) math.Int { return math.NewInt(s).MulRaw(scn) }

func acc(b byte) sdk.AccAddress {
	return sdk.AccAddress([]byte{b, 1, 2, 3, 4, 5, 6, 7, 8, 9, 10, 11, 12, 13, 14, 15, 16, 17, 18, 19})
}

func val(b byte) sdk.ValAddress { return sdk.ValAddress(acc(b)) }

// ---- stake engine -------------------------------------------------------------

type enqueued struct {
	delegator sdk.AccAddress
	validator sdk.ValAddress
	amount    math.Int
	id        uint64
}

type stubEngine struct {
	headroom math.Int
	vcap     math.Int
	tokens   map[string]math.Int // key: string(val bytes)
	slots    map[string][]string // key: string(delegator bytes)
	max      uint32
	min      math.Int
	epoch    uint64
	queued   []enqueued
}

func (e *stubEngine) Headroom(context.Context) (math.Int, error)     { return e.headroom, nil }
func (e *stubEngine) ValidatorCap(context.Context) (math.Int, error) { return e.vcap, nil }
func (e *stubEngine) ValidatorTokens(_ context.Context, v sdk.ValAddress) (math.Int, error) {
	return e.tokens[string(v)], nil
}
func (e *stubEngine) DelegatorSlots(_ context.Context, d sdk.AccAddress) ([]string, error) {
	return e.slots[string(d)], nil
}
func (e *stubEngine) MaxValidatorsPerDelegator(context.Context) (uint32, error) { return e.max, nil }
func (e *stubEngine) MinDelegation(context.Context) (math.Int, error)           { return e.min, nil }
func (e *stubEngine) LatestEpoch(context.Context) (uint64, error)               { return e.epoch, nil }
func (e *stubEngine) EnqueueRouted(_ context.Context, d sdk.AccAddress, v sdk.ValAddress, amt math.Int) (uint64, error) {
	id := uint64(len(e.queued) + 1)
	e.queued = append(e.queued, enqueued{delegator: d, validator: v, amount: amt, id: id})
	return id, nil
}

// ---- staking ------------------------------------------------------------------

type delegated struct {
	delegator sdk.AccAddress
	validator sdk.ValAddress
	amount    math.Int
}

type stubStaking struct {
	engine     *stubEngine
	validators map[string]stakingtypes.Validator
	bonded     []sdk.ValAddress // the by-power order
	calls      []delegated
}

func (s *stubStaking) GetValidator(_ context.Context, a sdk.ValAddress) (stakingtypes.Validator, error) {
	v, ok := s.validators[string(a)]
	if !ok {
		return stakingtypes.Validator{}, stakingtypes.ErrNoValidatorFound
	}
	return v, nil
}

func (s *stubStaking) GetBondedValidatorsByPower(context.Context) ([]stakingtypes.Validator, error) {
	var out []stakingtypes.Validator
	for _, a := range s.bonded {
		out = append(out, s.validators[string(a)])
	}
	return out, nil
}

func (s *stubStaking) Delegate(_ context.Context, d sdk.AccAddress, amt math.Int, _ stakingtypes.BondStatus,
	v stakingtypes.Validator, _ bool) (math.LegacyDec, error) {
	va, err := sdk.ValAddressFromBech32(v.OperatorAddress)
	if err != nil {
		return math.LegacyZeroDec(), err
	}
	s.calls = append(s.calls, delegated{delegator: d, validator: va, amount: amt})
	s.engine.tokens[string(va)] = s.engine.tokens[string(va)].Add(amt)
	return math.LegacyNewDecFromInt(amt), nil
}

// ---- participation / registry ---------------------------------------------------

type stubParticipation struct{ scores map[string]math.LegacyDec }

func (p *stubParticipation) GetSnapshot(_ context.Context, _ uint64, a sdk.ValAddress) (participationtypes.ParticipationScoreSnapshot, bool, error) {
	s, ok := p.scores[string(a)]
	if !ok {
		return participationtypes.ParticipationScoreSnapshot{}, false, nil
	}
	return participationtypes.ParticipationScoreSnapshot{ComputedScore: s}, true, nil
}

type stubRegistry struct{ status map[string]vrtypes.Status }

func (r *stubRegistry) GetValidatorStatus(_ context.Context, a sdk.ValAddress) (vrtypes.Status, bool, error) {
	s, ok := r.status[string(a)]
	return s, ok, nil
}

// ---- fixture --------------------------------------------------------------------

type fixture struct {
	ctx    sdk.Context
	k      keeper.Keeper
	engine *stubEngine
	stk    *stubStaking
	part   *stubParticipation
	reg    *stubRegistry
}

// newFixture: validator cap 1,000,000 SCN, headroom 10,000,000 SCN, at most 3
// validators per delegator, a 100 SCN minimum, epoch 7, default params.
func newFixture(t *testing.T) *fixture {
	t.Helper()
	encCfg := moduletestutil.MakeTestEncodingConfig()
	key := storetypes.NewKVStoreKey(types.StoreKey)
	ctx := testutil.DefaultContextWithKeys(map[string]*storetypes.KVStoreKey{types.StoreKey: key}, nil, nil).WithBlockHeight(1)

	engine := &stubEngine{
		headroom: nscn(10_000_000), vcap: nscn(1_000_000),
		tokens: map[string]math.Int{}, slots: map[string][]string{}, max: 3, min: nscn(100), epoch: 7,
	}
	stk := &stubStaking{engine: engine, validators: map[string]stakingtypes.Validator{}}
	part := &stubParticipation{scores: map[string]math.LegacyDec{}}
	reg := &stubRegistry{status: map[string]vrtypes.Status{}}
	k := keeper.NewKeeper(
		runtime.NewKVStoreService(key), encCfg.Codec,
		addresscodec.NewBech32Codec(sdk.GetConfig().GetBech32AccountAddrPrefix()),
		authtypes.NewModuleAddress(types.GovModuleName),
		engine, stk, part, reg,
	)
	if err := k.Params.Set(ctx, types.DefaultParams()); err != nil {
		t.Fatal(err)
	}
	return &fixture{ctx: ctx, k: k, engine: engine, stk: stk, part: part, reg: reg}
}

// addVal adds an ACTIVE, unjailed, bonded validator holding `tokens` SCN with
// the given last-epoch score ("" = no snapshot).
func (f *fixture) addVal(b byte, tokens int64, score string) sdk.ValAddress {
	v := val(b)
	f.stk.validators[string(v)] = stakingtypes.Validator{OperatorAddress: v.String(), Status: stakingtypes.Bonded}
	f.stk.bonded = append(f.stk.bonded, v)
	f.engine.tokens[string(v)] = nscn(tokens)
	f.reg.status[string(v)] = vrtypes.Status_STATUS_ACTIVE
	if score != "" {
		f.part.scores[string(v)] = math.LegacyMustNewDecFromStr(score)
	}
	return v
}

// room sets a validator's tokens so that it has exactly `room` SCN under the cap.
func (f *fixture) room(v sdk.ValAddress, room int64) {
	f.engine.tokens[string(v)] = f.engine.vcap.Sub(nscn(room))
}
