package keeper_test

import (
	"bytes"
	"context"
	"errors"
	"sort"
	"strings"
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
	"github.com/stretchr/testify/require"

	"github.com/saicon-network/saicon/chain/x/entityregistry/keeper"
	"github.com/saicon-network/saicon/chain/x/entityregistry/types"
	vrtypes "github.com/saicon-network/saicon/chain/x/validatorregistry/types"
)

func acc(b byte) sdk.AccAddress {
	return sdk.AccAddress([]byte{b, 1, 2, 3, 4, 5, 6, 7, 8, 9, 10, 11, 12, 13, 14, 15, 16, 17, 18, 19})
}

func val(b byte) sdk.ValAddress { return sdk.ValAddress(acc(b)) }

// ---- registry ---------------------------------------------------------------

type stubRegistry struct {
	status       map[string]vrtypes.Status // valoper string → status
	gate         vrtypes.ActivationGate    // set by the fixture to the keeper under test
	failActivate bool
	failStatus   bool // GetValidatorStatus returns a store error
	failIterate  bool // IterateValidatorMetadata returns a store error
}

// ActivateOnBond emulates x/validatorregistry: BONDING or UNBONDING → ACTIVE
// through the gate; a hold leaves the status and returns nil.
func (r *stubRegistry) ActivateOnBond(ctx context.Context, v sdk.ValAddress) error {
	if r.failActivate {
		return errors.New("stub: activation failed")
	}
	s := r.status[v.String()]
	if s != vrtypes.Status_STATUS_BONDING && s != vrtypes.Status_STATUS_UNBONDING {
		return nil
	}
	if r.gate != nil {
		if err := r.gate.MayActivate(ctx, v); err != nil {
			if errors.Is(err, vrtypes.ErrActivationHeld) {
				return nil
			}
			return err
		}
	}
	r.status[v.String()] = vrtypes.Status_STATUS_ACTIVE
	return nil
}

func (r *stubRegistry) GetValidatorStatus(_ context.Context, v sdk.ValAddress) (vrtypes.Status, bool, error) {
	if r.failStatus {
		return 0, false, errors.New("stub: status read failed")
	}
	s, ok := r.status[v.String()]
	return s, ok, nil
}

// IterateValidatorMetadata walks the validators sorted by operator bytes.
func (r *stubRegistry) IterateValidatorMetadata(_ context.Context, cb func(sdk.ValAddress, vrtypes.ValidatorMetadata) bool) error {
	if r.failIterate {
		return errors.New("stub: iteration failed")
	}
	addrs := make([]sdk.ValAddress, 0, len(r.status))
	for s := range r.status {
		a, err := sdk.ValAddressFromBech32(s)
		if err != nil {
			return err
		}
		addrs = append(addrs, a)
	}
	sort.Slice(addrs, func(i, j int) bool { return bytes.Compare(addrs[i], addrs[j]) < 0 })
	for _, a := range addrs {
		if cb(a, vrtypes.ValidatorMetadata{ValidatorAddress: a.String(), Status: r.status[a.String()]}) {
			return nil
		}
	}
	return nil
}

// ---- staking ----------------------------------------------------------------

type stubStaking struct {
	vals map[string]stakingtypes.Validator // valoper string → validator
}

func (s *stubStaking) GetValidator(_ context.Context, v sdk.ValAddress) (stakingtypes.Validator, error) {
	got, ok := s.vals[v.String()]
	if !ok {
		return stakingtypes.Validator{}, stakingtypes.ErrNoValidatorFound
	}
	return got, nil
}

func (s *stubStaking) bond(v sdk.ValAddress, tokens math.Int, moniker string) {
	s.vals[v.String()] = stakingtypes.Validator{
		OperatorAddress: v.String(),
		Tokens:          tokens,
		DelegatorShares: math.LegacyNewDecFromInt(tokens),
		Status:          stakingtypes.Bonded,
		Description:     stakingtypes.Description{Moniker: moniker},
	}
}

func (s *stubStaking) unbond(v sdk.ValAddress) {
	got := s.vals[v.String()]
	got.Status = stakingtypes.Unbonding
	s.vals[v.String()] = got
}

// ---- fixture ----------------------------------------------------------------

type fixture struct {
	ctx      sdk.Context
	k        keeper.Keeper
	reg      *stubRegistry
	staking  *stubStaking
	attestor sdk.AccAddress
}

// newFixture: params {one attestor, 2 seats}, block height 10, the keeper as
// the registry stub's activation gate.
func newFixture(t *testing.T) *fixture {
	t.Helper()
	encCfg := moduletestutil.MakeTestEncodingConfig()
	storeKey := storetypes.NewKVStoreKey(types.StoreKey)
	ctx := testutil.DefaultContextWithKeys(
		map[string]*storetypes.KVStoreKey{types.StoreKey: storeKey},
		map[string]*storetypes.TransientStoreKey{},
		nil,
	).WithBlockHeight(10)

	reg := &stubRegistry{status: map[string]vrtypes.Status{}}
	staking := &stubStaking{vals: map[string]stakingtypes.Validator{}}
	k := keeper.NewKeeper(
		runtime.NewKVStoreService(storeKey), encCfg.Codec,
		addresscodec.NewBech32Codec(sdk.GetConfig().GetBech32AccountAddrPrefix()),
		authtypes.NewModuleAddress(types.GovModuleName),
		reg, staking,
	)
	attestor := acc(200)
	attestorStr, err := sdk.Bech32ifyAddressBytes(types.AccountPrefix, attestor)
	require.NoError(t, err)
	require.NoError(t, k.Params.Set(ctx, types.Params{Attestors: []string{attestorStr}, MaxActiveSeats: 2}))
	reg.gate = k
	return &fixture{ctx: ctx, k: k, reg: reg, staking: staking, attestor: attestor}
}

// att builds an attestation for entity id with the given members.
func att(id string, vals ...sdk.ValAddress) types.Attestation {
	return types.Attestation{
		EntityID:        id,
		DisplayName:     "Entity " + id,
		AttestationHash: strings.Repeat("ab", 32),
		Validators:      vals,
	}
}

// attWithKeys is att with VRF key bindings.
func attWithKeys(id string, vals []sdk.ValAddress, bindings ...types.VrfKeyBinding) types.Attestation {
	a := att(id, vals...)
	a.VrfKeys = bindings
	return a
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
