package app

import (
	"encoding/json"
	"math/rand"
	"testing"
	"time"

	"cosmossdk.io/log"
	"cosmossdk.io/math"
	abci "github.com/cometbft/cometbft/abci/types"
	cmtproto "github.com/cometbft/cometbft/proto/tendermint/types"
	dbm "github.com/cosmos/cosmos-db"
	"github.com/cosmos/cosmos-sdk/baseapp"
	"github.com/cosmos/cosmos-sdk/codec"
	codectypes "github.com/cosmos/cosmos-sdk/codec/types"
	"github.com/cosmos/cosmos-sdk/crypto/keys/ed25519"
	"github.com/cosmos/cosmos-sdk/crypto/keys/secp256k1"
	cryptotypes "github.com/cosmos/cosmos-sdk/crypto/types"
	simtestutil "github.com/cosmos/cosmos-sdk/testutil/sims"
	sdk "github.com/cosmos/cosmos-sdk/types"
	authtypes "github.com/cosmos/cosmos-sdk/x/auth/types"
	banktypes "github.com/cosmos/cosmos-sdk/x/bank/types"
	govtypes "github.com/cosmos/cosmos-sdk/x/gov/types"
	govv1 "github.com/cosmos/cosmos-sdk/x/gov/types/v1"
	stakingtypes "github.com/cosmos/cosmos-sdk/x/staking/types"
	"github.com/stretchr/testify/require"

	participationtypes "github.com/saicon-network/saicon/chain/x/participation/types"
	stakeenginetypes "github.com/saicon-network/saicon/chain/x/stakeengine/types"
	vrtypes "github.com/saicon-network/saicon/chain/x/validatorregistry/types"
)

// The in-process harness of the S7 tests: the REAL app (every module, the real
// ante handler, the real message router), built from a hand-assembled genesis.
//
// Genesis: x/staking is written directly rather than through
// simtestutil.GenesisStateWithValSet, which gives every validator one fixed
// 1,000,000-unit stake delegated by genAccs[0] and an operator address derived
// from the consensus key — no operator we hold a spending key for and no
// self-delegation of a chosen size. Here three validators have secp256k1
// operators we hold the keys of, each self-delegating 300,000 SCN.

const scn int64 = 100_000_000 // nscn per SCN

func scnInt(n int64) math.Int { return math.NewInt(n * scn) }

func scnCoin(n int64) sdk.Coin { return sdk.NewCoin(stakeenginetypes.BondDenom, scnInt(n)) }

const (
	testChainID      = "saicon-test"
	lockupSeconds    = 60 * 24 * 3600
	validatorSelfSCN = 300_000
	delegatorSCN     = 1_000_000
	testCapFloorSCN  = 400_000
)

var genesisTime = time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)

// acct is a deterministic account: the key is derived from its name.
type acct struct {
	priv cryptotypes.PrivKey
	addr sdk.AccAddress
	val  sdk.ValAddress
}

func newAcct(name string) acct {
	priv := secp256k1.GenPrivKeyFromSecret([]byte("s7-test/" + name))
	addr := sdk.AccAddress(priv.PubKey().Address())
	return acct{priv: priv, addr: addr, val: sdk.ValAddress(addr)}
}

func newConsKey(name string) cryptotypes.PrivKey {
	return ed25519.GenPrivKeyFromSecret([]byte("s7-test/cons" + name))
}

// testChain is one fresh chain: three genesis validators (A, B, C), a fourth
// operator (D) who has funds but no validator yet, five delegators, and the
// gov module account is funded by fundGov.
type testChain struct {
	t   *testing.T
	app *App

	vals       [3]acct
	fourth     acct
	delegators [5]acct

	consKeys [3]cryptotypes.PrivKey

	height int64
	time   time.Time
}

func (c *testChain) govAddr() sdk.AccAddress { return authtypes.NewModuleAddress(govtypes.ModuleName) }

// newTestChain builds the app, runs InitChain, finalizes and commits block 1.
func newTestChain(t *testing.T, mutate ...func(cdc codec.Codec, gen map[string]json.RawMessage)) *testChain {
	t.Helper()
	c := &testChain{t: t, fourth: newAcct("fourth")}
	for i := range c.vals {
		c.vals[i] = newAcct("validator" + string(rune('A'+i)))
		c.consKeys[i] = ed25519.GenPrivKeyFromSecret([]byte("s7-test/cons" + string(rune('A'+i))))
	}
	for i := range c.delegators {
		c.delegators[i] = newAcct("delegator" + string(rune('0'+i)))
	}

	app := New(log.NewNopLogger(), dbm.NewMemDB(), nil, true, simtestutil.AppOptionsMap{}, baseapp.SetChainID(testChainID))
	c.app = app
	cdc := app.AppCodec()
	gen := app.DefaultGenesis()

	// accounts and balances
	var accounts []authtypes.GenesisAccount
	var balances []banktypes.Balance
	var number uint64
	add := func(a sdk.AccAddress, n int64) {
		accounts = append(accounts, authtypes.NewBaseAccount(a, nil, number, 0))
		number++
		balances = append(balances, banktypes.Balance{Address: a.String(), Coins: sdk.NewCoins(scnCoin(n))})
	}
	for _, v := range c.vals {
		add(v.addr, 100_000)
	}
	add(c.fourth.addr, 20_000)
	for _, d := range c.delegators {
		add(d.addr, delegatorSCN)
	}
	balances = append(balances,
		banktypes.Balance{
			Address: authtypes.NewModuleAddress(stakingtypes.BondedPoolName).String(),
			Coins:   sdk.NewCoins(scnCoin(3 * validatorSelfSCN)),
		},
	)
	supply := sdk.NewCoins()
	for _, b := range balances {
		supply = supply.Add(b.Coins...)
	}
	var authGen authtypes.GenesisState
	cdc.MustUnmarshalJSON(gen[authtypes.ModuleName], &authGen)
	packed, err := authtypes.PackAccounts(accounts)
	require.NoError(t, err)
	authGen.Accounts = packed
	gen[authtypes.ModuleName] = cdc.MustMarshalJSON(&authGen)

	var bankGen banktypes.GenesisState
	cdc.MustUnmarshalJSON(gen[banktypes.ModuleName], &bankGen)
	bankGen.Balances = balances
	bankGen.Supply = supply
	gen[banktypes.ModuleName] = cdc.MustMarshalJSON(&bankGen)

	// staking: three bonded validators, each self-delegated 300,000 SCN
	var stGen stakingtypes.GenesisState
	cdc.MustUnmarshalJSON(gen[stakingtypes.ModuleName], &stGen)
	for i, v := range c.vals {
		pkAny, err := codectypes.NewAnyWithValue(c.consKeys[i].PubKey())
		require.NoError(t, err)
		stGen.Validators = append(stGen.Validators, stakingtypes.Validator{
			OperatorAddress:   v.val.String(),
			ConsensusPubkey:   pkAny,
			Status:            stakingtypes.Bonded,
			Tokens:            scnInt(validatorSelfSCN),
			DelegatorShares:   math.LegacyNewDecFromInt(scnInt(validatorSelfSCN)),
			UnbondingTime:     time.Unix(0, 0).UTC(),
			Commission:        stakingtypes.NewCommission(math.LegacyZeroDec(), math.LegacyOneDec(), math.LegacyOneDec()),
			MinSelfDelegation: math.OneInt(),
		})
		stGen.Delegations = append(stGen.Delegations, stakingtypes.NewDelegation(
			v.addr.String(), v.val.String(), math.LegacyNewDecFromInt(scnInt(validatorSelfSCN))))
	}
	gen[stakingtypes.ModuleName] = cdc.MustMarshalJSON(&stGen)

	// the registry: ACTIVE, locked for 60 days from genesis
	var vrGen vrtypes.GenesisState
	cdc.MustUnmarshalJSON(gen[vrtypes.ModuleName], &vrGen)
	for _, v := range c.vals {
		vrGen.Validators = append(vrGen.Validators, vrtypes.ValidatorMetadata{
			ValidatorAddress:      v.val.String(),
			Status:                vrtypes.Status_STATUS_ACTIVE,
			BondingStart:          genesisTime,
			LockupDurationSeconds: lockupSeconds,
			ComplianceFlag:        true,
		})
	}
	gen[vrtypes.ModuleName] = cdc.MustMarshalJSON(&vrGen)

	// participation: a snapshot of the latest closed epoch (0) with score 1
	var pGen participationtypes.GenesisState
	cdc.MustUnmarshalJSON(gen[participationtypes.ModuleName], &pGen)
	for _, v := range c.vals {
		pGen.Scores = append(pGen.Scores, participationtypes.ParticipationScoreSnapshot{
			ValidatorAddress: v.val.String(),
			EpochId:          0,
			UptimeFraction:   math.LegacyOneDec(),
			ComputedScore:    math.LegacyOneDec(),
			ComputedAt:       genesisTime,
		})
		pGen.EmpiricalPoints = append(pGen.EmpiricalPoints, participationtypes.EmpiricalPointsState{
			ValidatorAddress: v.val.String(),
			AccumulatedEp:    math.OneInt(),
		})
	}
	gen[participationtypes.ModuleName] = cdc.MustMarshalJSON(&pGen)

	// the stake engine: a 400,000 SCN validator cap floor
	var seGen stakeenginetypes.GenesisState
	cdc.MustUnmarshalJSON(gen[stakeenginetypes.ModuleName], &seGen)
	seGen.Params.ValidatorCapFloor = scnInt(testCapFloorSCN)
	gen[stakeenginetypes.ModuleName] = cdc.MustMarshalJSON(&seGen)

	// gov: a voting period of a few seconds
	var govGen govv1.GenesisState
	cdc.MustUnmarshalJSON(gen[govtypes.ModuleName], &govGen)
	vp := 10 * time.Second
	govGen.Params.VotingPeriod = &vp
	gen[govtypes.ModuleName] = cdc.MustMarshalJSON(&govGen)

	for _, m := range mutate {
		m(cdc, gen)
	}

	state, err := json.Marshal(gen)
	require.NoError(t, err)
	_, err = app.InitChain(&abci.RequestInitChain{
		ChainId:         testChainID,
		Time:            genesisTime,
		InitialHeight:   1,
		ConsensusParams: simtestutil.DefaultConsensusParams,
		AppStateBytes:   state,
	})
	require.NoError(t, err)

	c.height, c.time = 0, genesisTime
	c.nextBlock(time.Second)
	return c
}

// nextBlock finalizes and commits one empty block, one step after the last.
func (c *testChain) nextBlock(step time.Duration, txs ...[]byte) *abci.ResponseFinalizeBlock {
	c.t.Helper()
	c.height++
	c.time = c.time.Add(step)
	res, err := c.app.FinalizeBlock(&abci.RequestFinalizeBlock{
		Height: c.height,
		Time:   c.time,
		Txs:    txs,
		// the proposer is validator A
		ProposerAddress: c.consKeys[0].PubKey().Address(),
	})
	require.NoError(c.t, err)
	_, err = c.app.Commit()
	require.NoError(c.t, err)
	return res
}

// ctx is a context over the committed state, one block ahead, for the tests
// that call the message router directly. Writes go straight to the store; the
// tests that need isolation use the cache context deliverViaRouter makes.
func (c *testChain) ctx() sdk.Context {
	return c.app.NewUncachedContext(false, cmtproto.Header{
		ChainID: testChainID,
		Height:  c.height + 1,
		Time:    c.time.Add(time.Second),
	})
}

// deliverViaRouter executes msg exactly as x/gov, x/group and an ICA host do:
// the handler the message router holds for it, no ante handler. The state is
// written only if the handler succeeds.
func (c *testChain) deliverViaRouter(ctx sdk.Context, msg sdk.Msg) error {
	c.t.Helper()
	handler := c.app.MsgServiceRouter().Handler(msg)
	require.NotNil(c.t, handler, "no handler for %T", msg)
	cache, write := ctx.CacheContext()
	res, err := handler(cache, msg)
	if err != nil {
		return err
	}
	write()
	ctx.EventManager().EmitEvents(res.GetEvents())
	return nil
}

// deliverTx puts a signed transaction through FinalizeBlock, so the ante handler
// runs, and commits the block. It returns the transaction's result.
func (c *testChain) deliverTx(signer acct, msgs ...sdk.Msg) *abci.ExecTxResult {
	c.t.Helper()
	res := c.nextBlock(time.Second, c.signedTx(signer, msgs...))
	require.Len(c.t, res.TxResults, 1)
	return res.TxResults[0]
}

func (c *testChain) signedTx(signer acct, msgs ...sdk.Msg) []byte {
	c.t.Helper()
	acc := c.app.AuthKeeper.GetAccount(c.ctx(), signer.addr)
	require.NotNil(c.t, acc, "no account %s", signer.addr)
	tx, err := simtestutil.GenSignedMockTx(rand.New(rand.NewSource(1)), c.app.TxConfig(), msgs,
		sdk.NewCoins(), 3_000_000, testChainID,
		[]uint64{acc.GetAccountNumber()}, []uint64{acc.GetSequence()}, signer.priv)
	require.NoError(c.t, err)
	bz, err := c.app.TxConfig().TxEncoder()(tx)
	require.NoError(c.t, err)
	return bz
}

// fundGov sends the gov module account 500,000 SCN from delegator 4 in a real
// block. Not in genesis: x/gov's InitGenesis panics unless the module account
// holds exactly the deposits it was given.
func (c *testChain) fundGov() {
	c.t.Helper()
	res := c.deliverTx(c.delegators[4], banktypes.NewMsgSend(c.delegators[4].addr, c.govAddr(), sdk.NewCoins(scnCoin(500_000))))
	require.Equal(c.t, uint32(0), res.Code, res.Log)
}

func (c *testChain) balance(a sdk.AccAddress) math.Int {
	return c.app.BankKeeper.GetBalance(c.ctx(), a, stakeenginetypes.BondDenom).Amount
}

func (c *testChain) delegate(ctx sdk.Context, from acct, to sdk.ValAddress, amount math.Int) error {
	return c.deliverViaRouter(ctx, stakingtypes.NewMsgDelegate(from.addr.String(), to.String(),
		sdk.NewCoin(stakeenginetypes.BondDenom, amount)))
}

// createFourthValidator creates validator D with the given self-bond, through
// the message router.
func (c *testChain) createFourthValidator(ctx sdk.Context, selfBondSCN int64) error {
	msg, err := stakingtypes.NewMsgCreateValidator(
		c.fourth.val.String(),
		newConsKey("fourth").PubKey(),
		scnCoin(selfBondSCN),
		stakingtypes.Description{Moniker: "D"},
		stakingtypes.NewCommissionRates(math.LegacyZeroDec(), math.LegacyOneDec(), math.LegacyOneDec()),
		math.OneInt(),
	)
	require.NoError(c.t, err)
	return c.deliverViaRouter(ctx, msg)
}

func (c *testChain) tokens(ctx sdk.Context, v sdk.ValAddress) math.Int {
	val, err := c.app.StakingKeeper.GetValidator(ctx, v)
	require.NoError(c.t, err)
	return val.Tokens
}

func (c *testChain) hasDelegation(ctx sdk.Context, d sdk.AccAddress, v sdk.ValAddress) bool {
	_, err := c.app.StakingKeeper.GetDelegation(ctx, d, v)
	return err == nil
}

// A chain that boots is the first thing worth knowing.
func TestHarnessBootsTheRealApp(t *testing.T) {
	c := newTestChain(t)
	ctx := c.ctx()
	require.Equal(t, int64(2), c.height+1)
	require.Equal(t, scnInt(validatorSelfSCN), c.tokens(ctx, c.vals[0].val))
	vc, err := c.app.StakeengineKeeper.ValidatorCap(ctx)
	require.NoError(t, err)
	require.Equal(t, scnInt(testCapFloorSCN), vc)
	h, err := c.app.StakeengineKeeper.Headroom(ctx)
	require.NoError(t, err)
	require.True(t, h.IsPositive(), "headroom %s", h)
}
