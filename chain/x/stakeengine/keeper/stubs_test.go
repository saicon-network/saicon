package keeper_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"cosmossdk.io/core/address"
	"cosmossdk.io/math"
	storetypes "cosmossdk.io/store/types"
	addresscodec "github.com/cosmos/cosmos-sdk/codec/address"
	"github.com/cosmos/cosmos-sdk/runtime"
	"github.com/cosmos/cosmos-sdk/testutil"
	sdk "github.com/cosmos/cosmos-sdk/types"
	moduletestutil "github.com/cosmos/cosmos-sdk/types/module/testutil"
	authtypes "github.com/cosmos/cosmos-sdk/x/auth/types"
	stakingtypes "github.com/cosmos/cosmos-sdk/x/staking/types"

	participationtypes "github.com/saicon-network/saicon/chain/x/participation/types"
	"github.com/saicon-network/saicon/chain/x/stakeengine/keeper"
	"github.com/saicon-network/saicon/chain/x/stakeengine/types"
	vrtypes "github.com/saicon-network/saicon/chain/x/validatorregistry/types"
)

const scn = int64(100_000_000)

func nscn(s int64) math.Int { return math.NewInt(s).MulRaw(scn) }

func acc(b byte) sdk.AccAddress {
	return sdk.AccAddress([]byte{b, 1, 2, 3, 4, 5, 6, 7, 8, 9, 10, 11, 12, 13, 14, 15, 16, 17, 18, 19})
}

func val(b byte) sdk.ValAddress { return sdk.ValAddress(acc(b)) }

// ---- auth -------------------------------------------------------------------

type stubAuth struct{}

func (stubAuth) AddressCodec() address.Codec { return addresscodec.NewBech32Codec("cosmos") }
func (stubAuth) GetModuleAddress(name string) sdk.AccAddress {
	return authtypes.NewModuleAddress(name)
}

// ---- bank -------------------------------------------------------------------

// stubBank keeps balances in a KV store of the test context, so a CacheContext
// reverts them as the real bank would. The ctx-less helpers (bal, set, move) act
// on the fixture's base context.
type stubBank struct {
	supply   math.Int
	key      *storetypes.KVStoreKey
	base     sdk.Context
	failSend bool // SendCoinsFromModuleToAccount fails, before mutating anything
}

func newBank(supply math.Int, key *storetypes.KVStoreKey) *stubBank {
	return &stubBank{supply: supply, key: key}
}

func (b *stubBank) balOn(ctx context.Context, a sdk.AccAddress) math.Int {
	bz := sdk.UnwrapSDKContext(ctx).KVStore(b.key).Get(a)
	if bz == nil {
		return math.ZeroInt()
	}
	var v math.Int
	if err := v.Unmarshal(bz); err != nil {
		panic(err)
	}
	return v
}

func (b *stubBank) setOn(ctx context.Context, a sdk.AccAddress, v math.Int) {
	bz, err := v.Marshal()
	if err != nil {
		panic(err)
	}
	sdk.UnwrapSDKContext(ctx).KVStore(b.key).Set(a, bz)
}

func (b *stubBank) moveOn(ctx context.Context, from, to sdk.AccAddress, amt math.Int) error {
	if b.balOn(ctx, from).LT(amt) {
		return errors.New("insufficient funds")
	}
	b.setOn(ctx, from, b.balOn(ctx, from).Sub(amt))
	b.setOn(ctx, to, b.balOn(ctx, to).Add(amt))
	return nil
}

func (b *stubBank) bal(a sdk.AccAddress) math.Int    { return b.balOn(b.base, a) }
func (b *stubBank) set(a sdk.AccAddress, v math.Int) { b.setOn(b.base, a, v) }
func (b *stubBank) move(from, to sdk.AccAddress, amt math.Int) error {
	return b.moveOn(b.base, from, to, amt)
}

func (b *stubBank) GetSupply(_ context.Context, denom string) sdk.Coin {
	return sdk.NewCoin(denom, b.supply)
}
func (b *stubBank) GetBalance(ctx context.Context, a sdk.AccAddress, denom string) sdk.Coin {
	return sdk.NewCoin(denom, b.balOn(ctx, a))
}
func (b *stubBank) SendCoinsFromAccountToModule(ctx context.Context, from sdk.AccAddress, module string, amt sdk.Coins) error {
	return b.moveOn(ctx, from, authtypes.NewModuleAddress(module), amt.AmountOf(types.BondDenom))
}
func (b *stubBank) SendCoinsFromModuleToAccount(ctx context.Context, module string, to sdk.AccAddress, amt sdk.Coins) error {
	if b.failSend {
		return errors.New("send failed")
	}
	return b.moveOn(ctx, authtypes.NewModuleAddress(module), to, amt.AmountOf(types.BondDenom))
}

// ---- staking ----------------------------------------------------------------

type stubStaking struct {
	bank         *stubBank
	bonded       math.Int
	validators   map[string]stakingtypes.Validator    // key: string(val bytes)
	delegations  map[string][]stakingtypes.Delegation // key: string(delegator bytes)
	ubds         map[string][]stakingtypes.UnbondingDelegation
	failDelegate bool // Delegate fails, before mutating any staking state
}

func newStaking(bank *stubBank) *stubStaking {
	return &stubStaking{
		bank:        bank,
		bonded:      math.ZeroInt(),
		validators:  map[string]stakingtypes.Validator{},
		delegations: map[string][]stakingtypes.Delegation{},
		ubds:        map[string][]stakingtypes.UnbondingDelegation{},
	}
}

// addValidator creates a bonded validator whose tokens are all self-bonded, and
// moves that stake into the bonded pool.
func (s *stubStaking) addValidator(v sdk.ValAddress, tokens math.Int) {
	s.validators[string(v)] = stakingtypes.Validator{
		OperatorAddress: v.String(),
		Tokens:          tokens,
		DelegatorShares: math.LegacyNewDecFromInt(tokens),
		Status:          stakingtypes.Bonded,
	}
	self := sdk.AccAddress(v)
	s.delegations[string(self)] = append(s.delegations[string(self)], stakingtypes.Delegation{
		DelegatorAddress: self.String(), ValidatorAddress: v.String(), Shares: math.LegacyNewDecFromInt(tokens),
	})
	s.bonded = s.bonded.Add(tokens)
	pool := authtypes.NewModuleAddress(stakingtypes.BondedPoolName)
	s.bank.set(pool, s.bank.bal(pool).Add(tokens))
}

func (s *stubStaking) TotalBondedTokens(context.Context) (math.Int, error) { return s.bonded, nil }

func (s *stubStaking) GetValidator(_ context.Context, a sdk.ValAddress) (stakingtypes.Validator, error) {
	v, ok := s.validators[string(a)]
	if !ok {
		return stakingtypes.Validator{}, stakingtypes.ErrNoValidatorFound
	}
	return v, nil
}

func (s *stubStaking) GetDelegation(_ context.Context, d sdk.AccAddress, v sdk.ValAddress) (stakingtypes.Delegation, error) {
	for _, del := range s.delegations[string(d)] {
		if del.ValidatorAddress == v.String() {
			return del, nil
		}
	}
	return stakingtypes.Delegation{}, stakingtypes.ErrNoDelegation
}

// GetDelegatorDelegations honours maxRetrieve, as x/staking does.
func (s *stubStaking) GetDelegatorDelegations(_ context.Context, d sdk.AccAddress, maxRetrieve uint16) ([]stakingtypes.Delegation, error) {
	dels := s.delegations[string(d)]
	if len(dels) > int(maxRetrieve) {
		dels = dels[:maxRetrieve]
	}
	return dels, nil
}

func (s *stubStaking) GetUnbondingDelegations(_ context.Context, d sdk.AccAddress, _ uint16) ([]stakingtypes.UnbondingDelegation, error) {
	return s.ubds[string(d)], nil
}

// Delegate mirrors the staking msg server's call: coins leave the delegator's
// account into the bonded pool, the validator's tokens and shares rise 1:1.
func (s *stubStaking) Delegate(ctx context.Context, d sdk.AccAddress, amt math.Int, _ stakingtypes.BondStatus,
	v stakingtypes.Validator, subtract bool) (math.LegacyDec, error) {
	if s.failDelegate {
		return math.LegacyZeroDec(), errors.New("delegate failed")
	}
	va, err := sdk.ValAddressFromBech32(v.OperatorAddress)
	if err != nil {
		return math.LegacyZeroDec(), err
	}
	if subtract {
		if err := s.bank.moveOn(ctx, d, authtypes.NewModuleAddress(stakingtypes.BondedPoolName), amt); err != nil {
			return math.LegacyZeroDec(), err
		}
	}
	cur := s.validators[string(va)]
	cur.Tokens = cur.Tokens.Add(amt)
	cur.DelegatorShares = cur.DelegatorShares.Add(math.LegacyNewDecFromInt(amt))
	s.validators[string(va)] = cur
	s.delegations[string(d)] = append(s.delegations[string(d)], stakingtypes.Delegation{
		DelegatorAddress: d.String(), ValidatorAddress: va.String(), Shares: math.LegacyNewDecFromInt(amt),
	})
	s.bonded = s.bonded.Add(amt)
	return math.LegacyNewDecFromInt(amt), nil
}

// applyDelegation does to the stub's state what x/staking's Delegate does around
// the hooks: the validator's tokens and shares rise, the bonded total rises, and
// the delegation is created or grown.
func (s *stubStaking) applyDelegation(d sdk.AccAddress, v sdk.ValAddress, amt math.Int) {
	cur := s.validators[string(v)]
	cur.Tokens = cur.Tokens.Add(amt)
	cur.DelegatorShares = cur.DelegatorShares.Add(math.LegacyNewDecFromInt(amt))
	s.validators[string(v)] = cur
	s.bonded = s.bonded.Add(amt)
	dels := s.delegations[string(d)]
	for i := range dels {
		if dels[i].ValidatorAddress == v.String() {
			dels[i].Shares = dels[i].Shares.Add(math.LegacyNewDecFromInt(amt))
			return
		}
	}
	s.delegations[string(d)] = append(dels, stakingtypes.Delegation{
		DelegatorAddress: d.String(), ValidatorAddress: v.String(), Shares: math.LegacyNewDecFromInt(amt),
	})
}

// shrinkDelegation is the inverse. x/staking's Unbond removes the delegation
// (or shrinks its shares) BEFORE it removes the validator's tokens; the hooks
// run in between, so the tests call this in two steps.
func (s *stubStaking) shrinkDelegation(d sdk.AccAddress, v sdk.ValAddress, amt math.Int, removed bool) {
	dels := s.delegations[string(d)]
	for i := range dels {
		if dels[i].ValidatorAddress != v.String() {
			continue
		}
		if removed {
			s.delegations[string(d)] = append(dels[:i:i], dels[i+1:]...)
		} else {
			dels[i].Shares = dels[i].Shares.Sub(math.LegacyNewDecFromInt(amt))
		}
		return
	}
}

func (s *stubStaking) removeTokens(v sdk.ValAddress, amt math.Int) {
	cur := s.validators[string(v)]
	cur.Tokens = cur.Tokens.Sub(amt)
	cur.DelegatorShares = cur.DelegatorShares.Sub(math.LegacyNewDecFromInt(amt))
	s.validators[string(v)] = cur
	s.bonded = s.bonded.Sub(amt)
}

// ---- participation ------------------------------------------------------------

type stubParticipation struct {
	epochLen uint64
	scores   map[string]math.LegacyDec // key: string(val bytes)
	eps      map[string]math.Int
}

func (p *stubParticipation) EpochLengthBlocks(context.Context) (uint64, error) {
	return p.epochLen, nil
}

func (p *stubParticipation) GetSnapshot(_ context.Context, _ uint64, a sdk.ValAddress) (participationtypes.ParticipationScoreSnapshot, bool, error) {
	s, ok := p.scores[string(a)]
	if !ok {
		return participationtypes.ParticipationScoreSnapshot{}, false, nil
	}
	return participationtypes.ParticipationScoreSnapshot{ComputedScore: s}, true, nil
}

func (p *stubParticipation) GetEP(_ context.Context, a sdk.ValAddress) (participationtypes.EmpiricalPointsState, bool, error) {
	e, ok := p.eps[string(a)]
	if !ok {
		return participationtypes.EmpiricalPointsState{}, false, nil
	}
	return participationtypes.EmpiricalPointsState{AccumulatedEp: e}, true, nil
}

// ---- registry -----------------------------------------------------------------

type stubRegistry struct {
	inactive map[string]bool // key: string(val bytes); absent means STATUS_ACTIVE
	locked   map[string]bool // key: string(val bytes); the operator's self-stake is in its lock-up
}

func (r *stubRegistry) SelfStakeLockup(_ context.Context, a sdk.ValAddress) (bool, time.Time, error) {
	return r.locked[string(a)], time.Unix(2_000_000_000, 0), nil
}

func (r *stubRegistry) GetValidatorStatus(_ context.Context, a sdk.ValAddress) (vrtypes.Status, bool, error) {
	if r.inactive[string(a)] {
		return vrtypes.Status_STATUS_QUARANTINED, true, nil
	}
	return vrtypes.Status_STATUS_ACTIVE, true, nil
}

// ---- fixture ------------------------------------------------------------------

type fixture struct {
	ctx     sdk.Context
	k       keeper.Keeper
	bank    *stubBank
	staking *stubStaking
	part    *stubParticipation
	reg     *stubRegistry
}

// newFixture: a 10M SCN supply, one validator with 1M SCN self-bonded, a
// 100-block epoch, default params, block height 1.
func newFixture(t *testing.T) *fixture {
	t.Helper()
	// The collections codec only marshals this module's own gogoproto types, so
	// no module needs registering.
	encCfg := moduletestutil.MakeTestEncodingConfig()
	storeKey := storetypes.NewKVStoreKey(types.StoreKey)
	bankKey := storetypes.NewKVStoreKey("stub_bank")
	transientKey := storetypes.NewTransientStoreKey("transient_stakeengine")
	ctx := testutil.DefaultContextWithKeys(
		map[string]*storetypes.KVStoreKey{types.StoreKey: storeKey, "stub_bank": bankKey},
		map[string]*storetypes.TransientStoreKey{"transient_stakeengine": transientKey},
		nil,
	).WithBlockHeight(1)

	bank := newBank(nscn(10_000_000), bankKey)
	bank.base = ctx
	staking := newStaking(bank)
	part := &stubParticipation{epochLen: 100, scores: map[string]math.LegacyDec{}, eps: map[string]math.Int{}}
	reg := &stubRegistry{inactive: map[string]bool{}, locked: map[string]bool{}}

	k := keeper.NewKeeper(
		runtime.NewKVStoreService(storeKey), runtime.NewTransientStoreService(transientKey), encCfg.Codec,
		addresscodec.NewBech32Codec(sdk.GetConfig().GetBech32AccountAddrPrefix()),
		authtypes.NewModuleAddress(types.GovModuleName),
		stubAuth{}, bank, staking, part, reg,
	)
	// The queue tests enqueue small amounts; the 100 SCN production default is
	// covered in types/params_test.go.
	params := types.DefaultParams()
	params.MinQueueEntry = math.NewInt(1e8)
	if err := k.Params.Set(ctx, params); err != nil {
		t.Fatal(err)
	}
	staking.addValidator(val(1), nscn(1_000_000))
	// The rest of supply sits in the validator operator's account.
	bank.set(acc(1), nscn(9_000_000))
	return &fixture{ctx: ctx, k: k, bank: bank, staking: staking, part: part, reg: reg}
}
