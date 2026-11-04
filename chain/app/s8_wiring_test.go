package app

import (
	"encoding/hex"
	"encoding/json"
	"os"
	"regexp"
	"strings"
	"testing"
	"time"

	"cosmossdk.io/math"
	"github.com/cosmos/cosmos-sdk/codec"
	sdk "github.com/cosmos/cosmos-sdk/types"
	authtypes "github.com/cosmos/cosmos-sdk/x/auth/types"
	banktypes "github.com/cosmos/cosmos-sdk/x/bank/types"
	stakingkeeper "github.com/cosmos/cosmos-sdk/x/staking/keeper"
	stakingtypes "github.com/cosmos/cosmos-sdk/x/staking/types"
	"github.com/stretchr/testify/require"

	compliancetypes "github.com/saicon-network/saicon/chain/x/compliance/types"
	entitytypes "github.com/saicon-network/saicon/chain/x/entityregistry/types"
	randomnessmoduletypes "github.com/saicon-network/saicon/chain/x/randomness/types"
	stakeenginetypes "github.com/saicon-network/saicon/chain/x/stakeengine/types"
	vrkeeper "github.com/saicon-network/saicon/chain/x/validatorregistry/keeper"
	vrtypes "github.com/saicon-network/saicon/chain/x/validatorregistry/types"
)

// The concrete keepers the module is wired with must satisfy the interfaces it
// declares: depinject resolves them by concrete type, so a drift would only
// show up as a startup failure.
var (
	_ entitytypes.ValidatorregistryKeeper = vrkeeper.Keeper{}
	_ entitytypes.StakingKeeper           = (*stakingkeeper.Keeper)(nil)
)

// x/entityregistry installs itself as the registry's activation gate through
// depinject's Invoke (ADR-0014 D1). Without it every validator would activate
// ungated, and the whole entity rule would be dead code.
func TestTheActivationGateIsInstalled(t *testing.T) {
	c := newTestChain(t)
	require.True(t, c.app.ValidatorregistryKeeper.HasActivationGate(),
		"x/entityregistry must install itself as the registry's activation gate (ADR-0014 D1)")
}

// The module's place in the runtime lists is load-bearing (spec §7.4): its
// InitGenesis runs before x/staking's and x/genutil's, because the genesis bond
// hooks reach the gate; its EndBlocker runs after x/staking (which fires the bond
// hooks the gate may hold) and x/participation, and before x/stakeengine. Reads
// app_config.go, as s7_wiring_test.go does.
func TestEntityRegistryRunsInItsPlaceInTheBlockOrders(t *testing.T) {
	src, err := os.ReadFile("app_config.go")
	require.NoError(t, err)
	// Drop line comments: they name modules too.
	text := regexp.MustCompile(`(?m)//.*$`).ReplaceAllString(string(src), "")

	order := func(field string) []string {
		m := regexp.MustCompile(field + `:\s*\[\]string\{([^}]*)\}`).FindStringSubmatch(text)
		require.NotNil(t, m, "%s not found in app_config.go", field)
		return regexp.MustCompile(`[A-Za-z0-9_.]+`).FindAllString(m[1], -1)
	}
	index := func(names []string, want string) int {
		for i, n := range names {
			if n == want {
				return i
			}
		}
		return -1
	}
	const er = "entityregistrymoduletypes.ModuleName"

	initOrder := order("InitGenesis")
	e := index(initOrder, er)
	require.GreaterOrEqual(t, e, 0, "InitGenesis must list x/entityregistry")
	for _, later := range []string{"stakingtypes.ModuleName", "genutiltypes.ModuleName"} {
		i := index(initOrder, later)
		require.GreaterOrEqual(t, i, 0)
		require.Less(t, e, i, "x/entityregistry InitGenesis must come before %s", later)
	}

	endOrder := order("EndBlockers")
	e = index(endOrder, er)
	require.GreaterOrEqual(t, e, 0, "EndBlockers must list x/entityregistry")
	for _, earlier := range []string{"stakingtypes.ModuleName", "participationmoduletypes.ModuleName"} {
		i := index(endOrder, earlier)
		require.GreaterOrEqual(t, i, 0)
		require.Greater(t, e, i, "x/entityregistry EndBlocker must come after %s", earlier)
	}
	se := index(endOrder, "stakeenginemoduletypes.ModuleName")
	require.GreaterOrEqual(t, se, 0)
	require.Less(t, e, se, "x/entityregistry EndBlocker must come before x/stakeengine's")
}

// The harness attests its three validators, one entity each, so they are ACTIVE
// and the registry invariant (V-12) holds.
func TestHarnessValidatorsAreAttested(t *testing.T) {
	c := newTestChain(t)
	c.nextBlock(1e9)
	ctx := c.ctx()
	for i, v := range c.vals {
		m, found, err := c.app.EntityregistryKeeper.EntityOf(ctx, v.val)
		require.NoError(t, err)
		require.True(t, found, "validator %d has no entity", i)
		require.Equal(t, "entity-"+string(rune('a'+i)), m.EntityId)

		st, ok, err := c.app.ValidatorregistryKeeper.GetValidatorStatus(ctx, v.val)
		require.NoError(t, err)
		require.True(t, ok)
		require.Equal(t, vrtypes.Status_STATUS_ACTIVE, st)
	}
	msg, broken := c.app.EntityregistryKeeper.RegistryInvariant(ctx)
	require.False(t, broken, msg)
}

// withBondingInsideInitChain makes the three genesis validators bond during
// x/staking's InitGenesis, as gentx validators do, and leaves the registry's
// genesis without records for them, so their status comes only from the bond
// hook meeting the activation gate. A validator left Unbonded in the staking
// genesis is bonded by ApplyAndReturnValidatorSetUpdates inside InitGenesis.
func withBondingInsideInitChain(cdc codec.Codec, gen map[string]json.RawMessage) {
	var stGen stakingtypes.GenesisState
	cdc.MustUnmarshalJSON(gen[stakingtypes.ModuleName], &stGen)
	for i := range stGen.Validators {
		stGen.Validators[i].Status = stakingtypes.Unbonded
	}
	gen[stakingtypes.ModuleName] = cdc.MustMarshalJSON(&stGen)

	// The tokens now sit in the not-bonded pool until the hook-driven bond moves them.
	var bankGen banktypes.GenesisState
	cdc.MustUnmarshalJSON(gen[banktypes.ModuleName], &bankGen)
	bonded := authtypes.NewModuleAddress(stakingtypes.BondedPoolName).String()
	for i := range bankGen.Balances {
		if bankGen.Balances[i].Address == bonded {
			bankGen.Balances[i].Address = authtypes.NewModuleAddress(stakingtypes.NotBondedPoolName).String()
		}
	}
	gen[banktypes.ModuleName] = cdc.MustMarshalJSON(&bankGen)

	var vrGen vrtypes.GenesisState
	cdc.MustUnmarshalJSON(gen[vrtypes.ModuleName], &vrGen)
	vrGen.Validators = nil
	gen[vrtypes.ModuleName] = cdc.MustMarshalJSON(&vrGen)
}

// Genesis validators bond inside InitChain, in x/staking's InitGenesis. The gate
// must already know their entities then, or every one of them is held and the
// chain starts with no ACTIVE validator (spec §7.4). Here the registry has no
// genesis records, so ACTIVE can only come from the hook passing the gate, and
// the state is read straight after InitChain.
func TestGenesisValidatorsBondedInInitChainEndActive(t *testing.T) {
	c := newTestChainAtGenesis(t, withBondingInsideInitChain)
	// The state InitChain left, before any block: a validator held at genesis
	// would only be promoted by the entity registry's EndBlock of block 1.
	ctx := c.app.GetContextForFinalizeBlock(nil)
	for i, v := range c.vals {
		st, ok, err := c.app.ValidatorregistryKeeper.GetValidatorStatus(ctx, v.val)
		require.NoError(t, err)
		require.True(t, ok, "validator %d has no registry record after InitChain", i)
		require.Equal(t, vrtypes.Status_STATUS_ACTIVE, st, "validator %d after InitChain", i)
	}
	pending, err := c.app.EntityregistryKeeper.Pending.Has(ctx, c.vals[0].val)
	require.NoError(t, err)
	require.False(t, pending, "an attested genesis validator must not be on the pending list")
	msg, broken := c.app.EntityregistryKeeper.RegistryInvariant(ctx)
	require.False(t, broken, msg)
}

// withAttestor yields a chain whose params list the attestor, whose account
// exists with the promised balance, and whose supply still adds up (InitChain
// would refuse a bank genesis whose supply disagrees with the balances). The
// attestor's own signature is enough to attest, and the gate then lets the
// attested validator in: the held fourth validator ends ACTIVE.
func TestWithAttestorAddsAFundedListedAttestor(t *testing.T) {
	att := newAcct("attestor")
	c := newTestChain(t, withAttestor(att))
	ctx := c.ctx()
	require.Equal(t, scnInt(1_000), c.balance(att.addr))
	params, err := c.app.EntityregistryKeeper.Params.Get(ctx)
	require.NoError(t, err)
	require.Equal(t, []string{att.addr.String()}, params.Attestors)

	require.NoError(t, c.createFourthValidator(ctx, 5_000))
	c.nextBlock(time.Second)
	ctx = c.ctx()
	st, ok, err := c.app.ValidatorregistryKeeper.GetValidatorStatus(ctx, c.fourth.val)
	require.NoError(t, err)
	require.True(t, ok)
	require.Equal(t, vrtypes.Status_STATUS_BONDING, st, "an unattested validator is held at BONDING")

	_, err = c.app.EntityregistryKeeper.Attest(ctx, att.addr, entitytypes.Attestation{
		EntityID:        "entity-d",
		DisplayName:     "Validator D",
		AttestationHash: strings.Repeat("0a", 32),
		Validators:      []sdk.ValAddress{c.fourth.val},
	})
	require.NoError(t, err)
	c.nextBlock(time.Second)
	st, _, err = c.app.ValidatorregistryKeeper.GetValidatorStatus(c.ctx(), c.fourth.val)
	require.NoError(t, err)
	require.Equal(t, vrtypes.Status_STATUS_ACTIVE, st)
}

// The stake engine's consensus version is 2 and its 1 -> 2 migration is
// registered with the app's configurator, so the v0.10.0 upgrade handler's
// RunMigrations reaches it.
func TestStakeengineMigration1to2IsRegistered(t *testing.T) {
	c := newTestChain(t)
	require.Equal(t, uint64(2), c.app.ModuleManager.GetVersionMap()["stakeengine"])

	ctx := c.ctx()
	// Put the module's state back into its v1 shape, then run the migrations
	// from version 1 as an upgrade handler would.
	p, err := c.app.StakeengineKeeper.Params.Get(ctx)
	require.NoError(t, err)
	p.EntityCapFraction = math.LegacyZeroDec()
	p.EntityCapFloor = math.ZeroInt()
	require.NoError(t, c.app.StakeengineKeeper.Params.Set(ctx, p))

	vm := c.app.ModuleManager.GetVersionMap()
	vm["stakeengine"] = 1
	_, err = c.app.ModuleManager.RunMigrations(ctx, c.app.Configurator(), vm)
	require.NoError(t, err)

	got, err := c.app.StakeengineKeeper.Params.Get(ctx)
	require.NoError(t, err)
	require.Equal(t, math.LegacyNewDecWithPrec(5, 2), got.EntityCapFraction)
	require.Equal(t, p.ValidatorCapFloor, got.EntityCapFloor)
	require.NoError(t, got.Validate())
}

// The compliance module's consensus version is 2 and its 1 -> 2 migration is
// registered with the app's configurator, so the v0.10.0 upgrade handler's
// RunMigrations reaches it: the two coordinated-attack parameters get their
// defaults, the unjail cooldown and every compliance state are kept, and the
// sanction sequence starts at 1.
func TestComplianceMigration1to2IsRegistered(t *testing.T) {
	c := newTestChain(t)
	require.Equal(t, uint64(2), c.app.ModuleManager.GetVersionMap()[compliancetypes.ModuleName])

	ctx := c.ctx()
	k := c.app.ComplianceKeeper
	// A compliance state written before the migration, to be found as it was.
	state := compliancetypes.ComplianceState{
		ValidatorAddress: c.vals[0].val.String(),
		Compliant:        false,
		LastEvent:        compliancetypes.ComplianceEventType_COMPLIANCE_EVENT_TYPE_EXTERNAL_FLAG,
		LastEventTime:    ctx.BlockTime(),
		Reason:           "kept across the migration",
	}
	require.NoError(t, k.ComplianceState.Set(ctx, c.vals[0].val.Bytes(), state))
	wantState, err := c.app.AppCodec().Marshal(&state)
	require.NoError(t, err)

	// The module's state put back into its v1 shape.
	require.NoError(t, k.Params.Set(ctx, compliancetypes.Params{UnjailCooldownBlocks: 1234}))
	require.NoError(t, k.SanctionSeq.Set(ctx, 0))

	vm := c.app.ModuleManager.GetVersionMap()
	vm[compliancetypes.ModuleName] = 1
	after, err := c.app.ModuleManager.RunMigrations(ctx, c.app.Configurator(), vm)
	require.NoError(t, err)
	require.Equal(t, uint64(2), after[compliancetypes.ModuleName])

	got, err := k.Params.Get(ctx)
	require.NoError(t, err)
	require.Equal(t, compliancetypes.NewParams(1234), got)
	require.NoError(t, got.Validate())
	seq, err := k.SanctionSeq.Peek(ctx)
	require.NoError(t, err)
	require.Equal(t, uint64(1), seq)

	kept, err := k.ComplianceState.Get(ctx, c.vals[0].val.Bytes())
	require.NoError(t, err)
	gotState, err := c.app.AppCodec().Marshal(&kept)
	require.NoError(t, err)
	require.Equal(t, wantState, gotState, "the compliance state changed across the migration")
}

// blockOrder reads one of the runtime's name lists out of app_config.go, line
// comments dropped (they name modules too).
func blockOrder(t *testing.T, field string) []string {
	t.Helper()
	src, err := os.ReadFile("app_config.go")
	require.NoError(t, err)
	text := regexp.MustCompile(`(?m)//.*$`).ReplaceAllString(string(src), "")
	m := regexp.MustCompile(field + `:\s*\[\]string\{([^}]*)\}`).FindStringSubmatch(text)
	require.NotNil(t, m, "%s not found in app_config.go", field)
	return regexp.MustCompile(`[A-Za-z0-9_.]+`).FindAllString(m[1], -1)
}

func indexOf(names []string, want string) int {
	for i, n := range names {
		if n == want {
			return i
		}
	}
	return -1
}

// x/randomness runs after every module whose state its snapshot reads. In
// EndBlockers: x/entityregistry has just promoted the validators the snapshot
// counts as ACTIVE, x/participation has just closed the epoch, and x/stakeengine
// has just recomputed the caps the snapshot tests entity stakes against. In
// InitGenesis: the module needs nothing from them (round 1 is scheduled by the
// first EndBlock), but it follows them so the lists read in dependency order.
func TestRandomnessRunsLastInTheBlockOrders(t *testing.T) {
	const rn = "randomnessmoduletypes.ModuleName"
	deps := []string{
		"entityregistrymoduletypes.ModuleName",
		"participationmoduletypes.ModuleName",
		"stakeenginemoduletypes.ModuleName",
	}
	for _, field := range []string{"EndBlockers", "InitGenesis"} {
		order := blockOrder(t, field)
		r := indexOf(order, rn)
		require.GreaterOrEqual(t, r, 0, "%s must list x/randomness", field)
		for _, dep := range deps {
			i := indexOf(order, dep)
			require.GreaterOrEqual(t, i, 0, "%s must list %s", field, dep)
			require.Greater(t, r, i, "%s: x/randomness must come after %s", field, dep)
		}
	}
	// It is the last EndBlocker: nothing may run between its snapshot and the
	// end of the block.
	end := blockOrder(t, "EndBlockers")
	require.Equal(t, rn, end[len(end)-1], "x/randomness is the last EndBlocker")
	// It is not in the Begin/Pre lists: it has no BeginBlocker.
	require.Equal(t, -1, indexOf(blockOrder(t, "BeginBlockers"), rn))
	require.Equal(t, -1, indexOf(blockOrder(t, "PreBlockers"), rn))
}

// The built app wires x/randomness: its keeper is reachable and holds the
// state InitGenesis wrote, the Msg and Query services are registered, the
// store is mounted, and the module is in the version map.
func TestRandomnessIsWiredIntoTheApp(t *testing.T) {
	c := newTestChain(t)
	ctx := c.ctx()

	params, err := c.app.RandomnessKeeper.Params.Get(ctx)
	require.NoError(t, err)
	require.Equal(t, randomnessmoduletypes.DefaultParams(), params)
	round, err := c.app.RandomnessKeeper.CurrentRound.Get(ctx)
	require.NoError(t, err)
	require.Equal(t, uint64(1), round.Number)

	require.NotNil(t, c.app.MsgServiceRouter().Handler(&randomnessmoduletypes.MsgSubmitVrf{}),
		"MsgSubmitVrf has no handler")
	require.NotNil(t, c.app.MsgServiceRouter().Handler(&randomnessmoduletypes.MsgUpdateParams{}),
		"MsgUpdateParams has no handler")
	require.NotNil(t, c.app.GRPCQueryRouter().Route("/chain.randomness.v1.Query/Round"),
		"the Round query is not registered")

	require.NotNil(t, c.app.GetKey(randomnessmoduletypes.StoreKey), "the randomness store is not mounted")
	require.Equal(t, uint64(1), c.app.ModuleManager.GetVersionMap()[randomnessmoduletypes.ModuleName])
}

// The harness binds a deterministic VRF key to each of its three validators from
// genesis, so a test can sign a submission for any of them. The genesis round is
// 1 with cutoff 0, and the first EndBlock schedules it (default params: epoch 100
// blocks, rotation every 7 epochs, so R = 700 and the window opens at 600).
func TestTheHarnessValidatorsCarryVrfKeys(t *testing.T) {
	c := newTestChain(t)
	ctx := c.ctx()
	for i, v := range c.vals {
		m, found, err := c.app.EntityregistryKeeper.EntityOf(ctx, v.val)
		require.NoError(t, err)
		require.True(t, found)
		want := hex.EncodeToString(harnessVrfKey(i).PublicKey())
		require.Equal(t, want, m.VrfPublicKey, "validator %d", i)
		require.Zero(t, m.VrfKeyHeight)
		require.Len(t, m.VrfPublicKey, 64)
	}
	require.NotEqual(t, harnessVrfKey(0).PublicKey(), harnessVrfKey(1).PublicKey())

	round, err := c.app.RandomnessKeeper.CurrentRound.Get(ctx)
	require.NoError(t, err)
	require.Equal(t, uint64(1), round.Number)
	require.Zero(t, round.KeyCutoffHeight)
	require.Equal(t, int64(700), round.RotationHeight)
	require.Equal(t, int64(600), round.WindowOpenHeight)
	require.Equal(t, randomnessmoduletypes.RoundStatus_ROUND_STATUS_SCHEDULED, round.Status)

	msg, broken := c.app.EntityregistryKeeper.RegistryInvariant(ctx)
	require.False(t, broken, msg)
}

// withFastRounds shortens the cadence: epoch 5, a rotation every 2 epochs (so on
// multiples of 10) and a 10-block window. Round 1 is scheduled by block 1's
// EndBlock: the smallest multiple of 10 that is at least 1 + 10 + 1 is 20.
func TestFastRoundsSchedule(t *testing.T) {
	c := newTestChain(t, withFastRounds())
	round, err := c.app.RandomnessKeeper.CurrentRound.Get(c.ctx())
	require.NoError(t, err)
	require.Equal(t, uint64(1), round.Number)
	require.Equal(t, int64(20), round.RotationHeight)
	require.Equal(t, int64(10), round.WindowOpenHeight)
	require.Equal(t, randomnessmoduletypes.RoundStatus_ROUND_STATUS_SCHEDULED, round.Status)
}

// Caps exist from the start of a fresh chain: x/stakeengine's InitGenesis
// computes them, so the entity standing the snapshot reads never reports
// ErrCapsNotComputed, long before round 1's window opens, and the harness's
// first window (block 9's EndBlock) snapshots all three entities.
func TestCapsExistWhenRoundOneOpens(t *testing.T) {
	c := newTestChainAtGenesis(t, withFastRounds())
	// The state InitChain left, before any block.
	ctx := c.app.GetContextForFinalizeBlock(nil)
	for i, v := range c.vals {
		st, err := c.app.StakeengineKeeper.EntityStanding(ctx, v.val)
		require.NoError(t, err, "validator %d", i)
		require.True(t, st.Cap.IsPositive())
	}

	for c.height < 8 {
		c.nextBlock(time.Second)
	}
	r, err := c.app.RandomnessKeeper.CurrentRound.Get(c.ctx())
	require.NoError(t, err)
	require.Equal(t, randomnessmoduletypes.RoundStatus_ROUND_STATUS_SCHEDULED, r.Status, "still scheduled after block 8")

	c.nextBlock(time.Second) // block 9: the snapshot, one block before the window opens at 10
	r, err = c.app.RandomnessKeeper.CurrentRound.Get(c.ctx())
	require.NoError(t, err)
	require.Equal(t, randomnessmoduletypes.RoundStatus_ROUND_STATUS_OPEN, r.Status)
	require.Equal(t, 3, c.poolSize())
}

// If the caps were missing at the snapshot (a state with no cap snapshot), the
// snapshot would fail and the round would stay SCHEDULED: logged, nothing
// written, EndBlock returning nil so the chain keeps going. Block 10 is an epoch
// boundary (epoch 5): x/stakeengine's EndBlock recomputes the caps and, running
// before x/randomness, the snapshot taken in that same block finds them. That
// is the stake-engine half of the order, observed rather than read off a list.
func TestMissingCapsLeaveTheRoundScheduledUntilTheCapsAreComputed(t *testing.T) {
	c := newTestChain(t, withFastRounds())
	for c.height < 8 {
		c.nextBlock(time.Second)
	}
	require.NoError(t, c.app.StakeengineKeeper.CapState.Remove(c.ctx()))

	c.nextBlock(time.Second) // block 9: not an epoch boundary, no caps, the snapshot fails
	require.Equal(t, int64(9), c.height)
	r, err := c.app.RandomnessKeeper.CurrentRound.Get(c.ctx())
	require.NoError(t, err)
	require.Equal(t, randomnessmoduletypes.RoundStatus_ROUND_STATUS_SCHEDULED, r.Status)
	require.Zero(t, c.poolSize(), "a failed snapshot writes nothing")
	_, err = c.app.StakeengineKeeper.EntityCap(c.ctx())
	require.ErrorIs(t, err, stakeenginetypes.ErrCapsNotComputed)

	c.nextBlock(time.Second) // block 10: the caps are recomputed, then the snapshot succeeds
	require.Equal(t, int64(10), c.height)
	_, err = c.app.StakeengineKeeper.EntityCap(c.ctx())
	require.NoError(t, err)
	r, err = c.app.RandomnessKeeper.CurrentRound.Get(c.ctx())
	require.NoError(t, err)
	require.Equal(t, randomnessmoduletypes.RoundStatus_ROUND_STATUS_OPEN, r.Status)
	require.Equal(t, 3, c.poolSize())
}

// The order is load-bearing, not just listed: a validator the registry
// promotes in the snapshot block's own EndBlock is in the pool. Were
// x/randomness before x/entityregistry, the fourth entity would be missing.
func TestSnapshotSeesTheValidatorPromotedInTheSameBlock(t *testing.T) {
	att := newAcct("attestor")
	c := newTestChain(t, withFastRounds(), withAttestor(att))
	require.NoError(t, c.createFourthValidator(c.ctx(), 5_000))
	for c.height < 8 {
		c.nextBlock(time.Second)
	}
	st, ok, err := c.app.ValidatorregistryKeeper.GetValidatorStatus(c.ctx(), c.fourth.val)
	require.NoError(t, err)
	require.True(t, ok)
	require.Equal(t, vrtypes.Status_STATUS_BONDING, st, "held until attested")

	// Attested between blocks; the registry's EndBlock promotes it in block 9,
	// the same block whose EndBlock takes the snapshot.
	_, err = c.app.EntityregistryKeeper.Attest(c.ctx(), att.addr, entitytypes.Attestation{
		EntityID:        "entity-d",
		DisplayName:     "Validator D",
		AttestationHash: strings.Repeat("0a", 32),
		Validators:      []sdk.ValAddress{c.fourth.val},
	})
	require.NoError(t, err)
	c.nextBlock(time.Second)
	require.Equal(t, int64(9), c.height)

	st, _, err = c.app.ValidatorregistryKeeper.GetValidatorStatus(c.ctx(), c.fourth.val)
	require.NoError(t, err)
	require.Equal(t, vrtypes.Status_STATUS_ACTIVE, st)
	r, err := c.app.RandomnessKeeper.CurrentRound.Get(c.ctx())
	require.NoError(t, err)
	require.Equal(t, randomnessmoduletypes.RoundStatus_ROUND_STATUS_OPEN, r.Status)
	require.Equal(t, 4, c.poolSize(), "entity-d, promoted in this very block, is in the pool")
}

// poolSize is the number of entities in the current round's snapshot.
func (c *testChain) poolSize() int {
	c.t.Helper()
	n := 0
	require.NoError(c.t, c.app.RandomnessKeeper.Pool.Walk(c.ctx(), nil,
		func(string, randomnessmoduletypes.PoolEntry) (bool, error) { n++; return false, nil }))
	return n
}
