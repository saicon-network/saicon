package app

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"reflect"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"
	"unsafe"

	"cosmossdk.io/log"
	"cosmossdk.io/math"
	upgradetypes "cosmossdk.io/x/upgrade/types"
	abci "github.com/cometbft/cometbft/abci/types"
	dbm "github.com/cosmos/cosmos-db"
	"github.com/cosmos/cosmos-sdk/baseapp"
	"github.com/cosmos/cosmos-sdk/client/flags"
	simtestutil "github.com/cosmos/cosmos-sdk/testutil/sims"
	sdk "github.com/cosmos/cosmos-sdk/types"
	"github.com/stretchr/testify/require"

	compliancetypes "github.com/saicon-network/saicon/chain/x/compliance/types"
	entitytypes "github.com/saicon-network/saicon/chain/x/entityregistry/types"
	randomnesskeeper "github.com/saicon-network/saicon/chain/x/randomness/keeper"
	randomnesstypes "github.com/saicon-network/saicon/chain/x/randomness/types"
	stakeenginetypes "github.com/saicon-network/saicon/chain/x/stakeengine/types"
	vrtypes "github.com/saicon-network/saicon/chain/x/validatorregistry/types"
)

func TestTheV0100HandlerIsRegistered(t *testing.T) {
	c := newTestChain(t)
	require.True(t, c.app.UpgradeKeeper.HasHandler(UpgradeV0100))
	require.Equal(t, []string{entitytypes.StoreKey, randomnesstypes.StoreKey}, storeUpgradesV0100().Added)
}

// emptyEntityRegistry makes the state look like v0.9.x: no entities at all.
func emptyEntityRegistry(t *testing.T, c *testChain, ctx sdk.Context) {
	t.Helper()
	k := c.app.EntityregistryKeeper
	require.NoError(t, k.Entities.Clear(ctx, nil))
	require.NoError(t, k.Members.Clear(ctx, nil))
	require.NoError(t, k.MembersByEntity.Clear(ctx, nil))
	require.NoError(t, k.Frozen.Clear(ctx, nil))
	require.NoError(t, k.Pending.Clear(ctx, nil))
	require.NoError(t, k.Params.Set(ctx, entitytypes.DefaultParams()))
}

// setRegistryStatus rewrites a validator's registry status directly, the way a
// v0.9.x state could hold any of them.
func setRegistryStatus(t *testing.T, c *testChain, ctx sdk.Context, v sdk.ValAddress, st vrtypes.Status) {
	t.Helper()
	vr := c.app.ValidatorregistryKeeper
	md, err := vr.ValidatorMetadata.Get(ctx, v.Bytes())
	require.NoError(t, err)
	md.Status = st
	require.NoError(t, vr.ValidatorMetadata.Set(ctx, v.Bytes(), md))
}

func (c *testChain) applyV0100(ctx sdk.Context, plan upgradetypes.Plan) error {
	return applyV0100(ctx, plan, c.app.EntityregistryKeeper, c.app.StakingKeeper,
		c.app.ValidatorregistryKeeper, c.app.SlashingKeeper, c.app.ComplianceKeeper,
		c.app.ConsensusParamsKeeper, c.govAddr())
}

func attestorInfo(a ...sdk.AccAddress) string {
	list := make([]string, len(a))
	for i := range a {
		list[i] = a[i].String()
	}
	bz, err := json.Marshal(struct {
		Attestors []string `json:"attestors"`
	}{list})
	if err != nil {
		panic(err)
	}
	return string(bz)
}

func (c *testChain) requireAttestedAsLegacy(ctx sdk.Context, v sdk.ValAddress) {
	c.t.Helper()
	m, found, err := c.app.EntityregistryKeeper.EntityOf(ctx, v)
	require.NoError(c.t, err)
	require.True(c.t, found, "%s has no entity", v)
	require.Equal(c.t, legacyEntityID(v), m.EntityId)
	e, err := c.app.EntityregistryKeeper.Entities.Get(ctx, m.EntityId)
	require.NoError(c.t, err)
	sum := sha256.Sum256([]byte("saicon/entityregistry/v0.10.0-upgrade/" + v.String()))
	require.Equal(c.t, hex.EncodeToString(sum[:]), e.AttestationHash)
	require.Equal(c.t, c.govAddr().String(), e.Attestor)
	require.Equal(c.t, entitytypes.EntityStatus_ENTITY_STATUS_ATTESTED, e.Status)
	st, _, err := c.app.ValidatorregistryKeeper.GetValidatorStatus(ctx, v)
	require.NoError(c.t, err)
	require.Equal(c.t, vrtypes.Status_STATUS_ACTIVE, st, "%s stopped earning", v)
}

func TestUpgradeAttestsEveryActiveValidatorAndNobodyStopsEarning(t *testing.T) {
	c := newTestChain(t)
	ctx := c.ctx()
	emptyEntityRegistry(t, c, ctx)
	attestor := newAcct("devnet-attestor")
	plan := upgradetypes.Plan{Name: UpgradeV0100, Height: ctx.BlockHeight(), Info: attestorInfo(attestor.addr)}
	require.NoError(t, c.applyV0100(ctx, plan))
	for _, v := range c.vals {
		c.requireAttestedAsLegacy(ctx, v.val)
		e, err := c.app.EntityregistryKeeper.Entities.Get(ctx, legacyEntityID(v.val))
		require.NoError(t, err)
		require.Equal(t, "legacy validator", e.DisplayName, "the harness validators carry no moniker")
		m, _, err := c.app.EntityregistryKeeper.EntityOf(ctx, v.val)
		require.NoError(t, err)
		require.Equal(t, plan.Height, m.JoinedHeight, "%s: a legacy attestation joins at the upgrade height U", v.val)
	}
	p, err := c.app.EntityregistryKeeper.Params.Get(ctx)
	require.NoError(t, err)
	require.Equal(t, []string{attestor.addr.String()}, p.Attestors)
	require.Equal(t, uint32(2), p.MaxActiveSeats)
	_, broken := c.app.EntityregistryKeeper.RegistryInvariant(ctx)
	require.False(t, broken)
}

func TestUpgradeUsesTheStakingMoniker(t *testing.T) {
	c := newTestChain(t)
	ctx := c.ctx()
	emptyEntityRegistry(t, c, ctx)
	setMoniker := func(v sdk.ValAddress, moniker string) {
		val, err := c.app.StakingKeeper.GetValidator(ctx, v)
		require.NoError(t, err)
		val.Description.Moniker = moniker
		require.NoError(t, c.app.StakingKeeper.SetValidator(ctx, val))
	}
	// A: over 64 bytes of multi-byte text, whose 64-byte cut falls inside a rune.
	// B: a plain one. C: not UTF-8 at all (staking does not validate what it stores).
	long := strings.Repeat("\u0401\u043b\u043a\u0430 ", 20)
	require.Greater(t, len(long), 64)
	setMoniker(c.vals[0].val, long)
	setMoniker(c.vals[1].val, "\u0401\u043b\u043a\u0430 validator")
	setMoniker(c.vals[2].val, "\xff\xfe")

	require.NoError(t, c.applyV0100(ctx, upgradetypes.Plan{Name: UpgradeV0100, Info: attestorInfo()}))
	name := func(i int) string {
		e, err := c.app.EntityregistryKeeper.Entities.Get(ctx, legacyEntityID(c.vals[i].val))
		require.NoError(t, err)
		return e.DisplayName
	}
	require.Equal(t, strings.Repeat("\u0401\u043b\u043a\u0430 ", 7), name(0), "cut on a rune boundary, 63 bytes")
	require.Equal(t, "\u0401\u043b\u043a\u0430 validator", name(1))
	require.Equal(t, "legacy validator", name(2), "a non-UTF-8 moniker falls back")

	// Review Focus 3: whatever the moniker, every validator was attested as a
	// legacy entity and is still ACTIVE — nobody stopped earning over a spelling.
	for _, v := range c.vals {
		c.requireAttestedAsLegacy(ctx, v.val)
	}
	msg, broken := c.app.EntityregistryKeeper.RegistryInvariant(ctx)
	require.False(t, broken, msg)
}

func TestUpgradeWithAMalformedInfoKeepsGoing(t *testing.T) {
	for name, info := range map[string]string{
		"not json":       "not json",
		"empty":          "",
		"wrong prefix":   `{"attestors":["cosmos1qypqxpq9qcrsszg2pvxq6rs0zqg3yyc5lzv7xu"]}`,
		"not an address": `{"attestors":["nope"]}`,
		"wrong shape":    `{"attestors":"saicon1"}`,
		"duplicate":      attestorInfo(newAcct("dup").addr, newAcct("dup").addr),
	} {
		t.Run(name, func(t *testing.T) {
			c := newTestChain(t)
			ctx := c.ctx()
			emptyEntityRegistry(t, c, ctx)
			require.NoError(t, c.applyV0100(ctx, upgradetypes.Plan{Name: UpgradeV0100, Info: info}))
			p, err := c.app.EntityregistryKeeper.Params.Get(ctx)
			require.NoError(t, err)
			require.Empty(t, p.Attestors)
			require.Equal(t, uint32(2), p.MaxActiveSeats)
			for _, v := range c.vals {
				c.requireAttestedAsLegacy(ctx, v.val)
			}
		})
	}
}

func TestUpgradeWithNoActiveValidator(t *testing.T) {
	c := newTestChain(t)
	ctx := c.ctx()
	emptyEntityRegistry(t, c, ctx)
	for _, v := range c.vals {
		setRegistryStatus(t, c, ctx, v.val, vrtypes.Status_STATUS_QUARANTINED)
	}
	require.NoError(t, c.applyV0100(ctx, upgradetypes.Plan{Name: UpgradeV0100, Info: attestorInfo(newAcct("a").addr)}))
	ids, err := c.app.EntityregistryKeeper.AttestedEntityIDs(ctx)
	require.NoError(t, err)
	require.Empty(t, ids)
	n := 0
	require.NoError(t, c.app.EntityregistryKeeper.Entities.Walk(ctx, nil, func(string, entitytypes.Entity) (bool, error) {
		n++
		return false, nil
	}))
	require.Zero(t, n)
	p, err := c.app.EntityregistryKeeper.Params.Get(ctx)
	require.NoError(t, err)
	require.Len(t, p.Attestors, 1, "the attestor list is written even when there is nobody to attest")
	_, broken := c.app.EntityregistryKeeper.RegistryInvariant(ctx)
	require.False(t, broken)
}

// Only registry-ACTIVE validators are attested; the others need an attestation
// to (re-)activate (spec §17).
func TestUpgradeAttestsOnlyTheActiveOnes(t *testing.T) {
	c := newTestChain(t)
	ctx := c.ctx()
	emptyEntityRegistry(t, c, ctx)
	setRegistryStatus(t, c, ctx, c.vals[1].val, vrtypes.Status_STATUS_BONDING)
	setRegistryStatus(t, c, ctx, c.vals[2].val, vrtypes.Status_STATUS_JAILED)
	require.NoError(t, c.applyV0100(ctx, upgradetypes.Plan{Name: UpgradeV0100, Info: attestorInfo()}))

	c.requireAttestedAsLegacy(ctx, c.vals[0].val)
	for _, v := range c.vals[1:] {
		_, found, err := c.app.EntityregistryKeeper.EntityOf(ctx, v.val)
		require.NoError(t, err)
		require.False(t, found)
	}
	_, broken := c.app.EntityregistryKeeper.RegistryInvariant(ctx)
	require.False(t, broken)
}

// Review Focus 3 and the controller's requirement 1: a failure to attest a
// validator that is earning aborts the upgrade, and leaves nothing half done —
// not even the attestor list, which is written first.
func TestUpgradeFailingMidWayReturnsAnErrorAndWritesNothing(t *testing.T) {
	c := newTestChain(t)
	ctx := c.ctx()
	emptyEntityRegistry(t, c, ctx)

	// The last validator in operator-byte order already belongs to another
	// entity, so the third attestation is refused after two succeeded.
	ordered := []sdk.ValAddress{c.vals[0].val, c.vals[1].val, c.vals[2].val}
	sort.Slice(ordered, func(i, j int) bool { return string(ordered[i]) < string(ordered[j]) })
	_, err := c.app.EntityregistryKeeper.Attest(ctx, c.govAddr(), entitytypes.Attestation{
		EntityID: "other-owner", DisplayName: "Other owner",
		AttestationHash: strings.Repeat("0b", 32), Validators: []sdk.ValAddress{ordered[2]},
	})
	require.NoError(t, err)

	err = c.applyV0100(ctx, upgradetypes.Plan{Name: UpgradeV0100, Info: attestorInfo(newAcct("a").addr)})
	require.ErrorIs(t, err, entitytypes.ErrMemberOfAnotherEntity)

	for _, v := range ordered[:2] {
		_, found, err := c.app.EntityregistryKeeper.EntityOf(ctx, v)
		require.NoError(t, err)
		require.False(t, found, "%s was left attested by a failed upgrade", v)
	}
	p, err := c.app.EntityregistryKeeper.Params.Get(ctx)
	require.NoError(t, err)
	require.Empty(t, p.Attestors, "the attestor list was written by a failed upgrade")
}

// setSlashFractions rewrites the two fractions the way a v0.9.x state could hold
// them (`saicond init` there wrote 5% and 1%).
func setSlashFractions(t *testing.T, c *testChain, ctx sdk.Context, doubleSign, downtime math.LegacyDec) {
	t.Helper()
	p, err := c.app.SlashingKeeper.GetParams(ctx)
	require.NoError(t, err)
	p.SlashFractionDoubleSign = doubleSign
	p.SlashFractionDowntime = downtime
	require.NoError(t, c.app.SlashingKeeper.SetParams(ctx, p))
}

// The upgrade makes a chain meet the slashing policy (ADR-0010; project decisions
// 2026-07-28 and 2026-10-28), in the same write-through as the rest of the
// handler: a stored double-sign fraction below 10% is raised to it, and a
// positive downtime fraction is set to zero (x/compliance reads any positive
// burn as a double-sign). A chain already at or above 10% (the DevNet, which got
// it from its genesis template, and any chain governance raised further) and
// with downtime 0 is left alone. Nothing else in x/slashing moves.
func TestUpgradeBringsTheSlashingFractionsToThePolicy(t *testing.T) {
	for _, tc := range []struct{ name, ds, dt, wantDS, wantDT string }{
		{"the SDK defaults 5% and 1%", "0.05", "0.01", "0.10", "0"},
		{"just below, 9.9%", "0.099", "0", "0.10", "0"},
		{"zero double-sign", "0", "0", "0.10", "0"},
		{"the devnet's own values stay", "0.10", "0", "0.10", "0"},
		{"above 10% stays, 30%", "0.30", "0", "0.30", "0"},
		{"the ceiling stays, 100%", "1", "0", "1", "0"},
		{"downtime alone is zeroed", "0.10", "0.02", "0.10", "0"},
		{"a tiny downtime is zeroed", "0.30", "0.000000000000000001", "0.30", "0"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			c := newTestChain(t)
			ctx := c.ctx()
			emptyEntityRegistry(t, c, ctx)
			setSlashFractions(t, c, ctx, math.LegacyMustNewDecFromStr(tc.ds), math.LegacyMustNewDecFromStr(tc.dt))
			before, err := c.app.SlashingKeeper.GetParams(ctx)
			require.NoError(t, err)

			require.NoError(t, c.applyV0100(ctx, upgradetypes.Plan{Name: UpgradeV0100, Info: attestorInfo(newAcct("a").addr)}))

			after, err := c.app.SlashingKeeper.GetParams(ctx)
			require.NoError(t, err)
			require.True(t, math.LegacyMustNewDecFromStr(tc.wantDS).Equal(after.SlashFractionDoubleSign),
				"double-sign from %s want %s, got %s", tc.ds, tc.wantDS, after.SlashFractionDoubleSign)
			require.True(t, math.LegacyMustNewDecFromStr(tc.wantDT).Equal(after.SlashFractionDowntime),
				"downtime from %s want %s, got %s", tc.dt, tc.wantDT, after.SlashFractionDowntime)
			// Only those two fields may move.
			after.SlashFractionDoubleSign, after.SlashFractionDowntime = before.SlashFractionDoubleSign, before.SlashFractionDowntime
			require.Equal(t, before, after)
		})
	}
}

// The slashing and evidence-window steps run before the attestations, so a
// failed attestation is a real rollback test: the aborted upgrade writes
// nothing, the fractions and the consensus params included.
func TestUpgradeFailingMidWayRollsBackTheSlashingChange(t *testing.T) {
	c := newTestChain(t)
	ctx := c.ctx()
	emptyEntityRegistry(t, c, ctx)
	setSlashFractions(t, c, ctx, math.LegacyNewDecWithPrec(5, 2), math.LegacyNewDecWithPrec(1, 2))
	setConsensusParams(t, c, ctx, oldConsensusParams())

	_, err := c.app.EntityregistryKeeper.Attest(ctx, c.govAddr(), entitytypes.Attestation{
		EntityID: "other-owner", DisplayName: "Other owner",
		AttestationHash: strings.Repeat("0b", 32), Validators: []sdk.ValAddress{c.vals[0].val},
	})
	require.NoError(t, err)

	err = c.applyV0100(ctx, upgradetypes.Plan{Name: UpgradeV0100, Info: attestorInfo(newAcct("a").addr)})
	require.ErrorIs(t, err, entitytypes.ErrMemberOfAnotherEntity)
	p, err := c.app.SlashingKeeper.GetParams(ctx)
	require.NoError(t, err)
	require.Equal(t, "0.050000000000000000", p.SlashFractionDoubleSign.String(), "the raise was written by a failed upgrade")
	require.Equal(t, "0.010000000000000000", p.SlashFractionDowntime.String(), "the downtime change was written by a failed upgrade")
	requireOnlyEvidenceAgeMoved(t, oldConsensusParams(), consensusParams(t, c, ctx), v091EvidenceBlocks, v091EvidenceAge)
}

// complianceAfterTheMigration puts x/compliance in the state RunMigrations
// leaves it: the defaults, with the unjail cooldown kept.
func complianceAfterTheMigration(t *testing.T, c *testChain, ctx sdk.Context) {
	t.Helper()
	require.NoError(t, c.app.ComplianceKeeper.Params.Set(ctx, compliancetypes.NewParams(legacyUnjailCooldown)))
}

// The handler's last step keeps the C20 rule true the moment the upgrade ends:
// the coordinated-attack fraction is never below the double-sign fraction in
// force, and that fraction is read after the policy raised it. With the shipped
// values nothing moves (0.30 >= 0.10); a chain whose governance set the
// double-sign fraction above 0.30 gets the coordinated fraction lifted to it.
func TestUpgradeAlignsTheCoordinatedFractionWithDoubleSign(t *testing.T) {
	for _, tc := range []struct{ name, ds, wantDS, wantCoordinated string }{
		{"the SDK default is raised to 10% and 0.30 stays", "0.05", "0.10", "0.30"},
		{"the policy value, 0.30 stays", "0.10", "0.10", "0.30"},
		{"equal to the coordinated fraction stays", "0.30", "0.30", "0.30"},
		{"above 0.30, the coordinated fraction follows", "0.40", "0.40", "0.40"},
		{"the ceiling, 100%", "1", "1", "1"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			c := newTestChain(t)
			ctx := c.ctx()
			emptyEntityRegistry(t, c, ctx)
			complianceAfterTheMigration(t, c, ctx)
			setSlashFractions(t, c, ctx, math.LegacyMustNewDecFromStr(tc.ds), math.LegacyZeroDec())

			require.NoError(t, c.applyV0100(ctx, upgradetypes.Plan{Name: UpgradeV0100, Info: attestorInfo(newAcct("a").addr)}))

			sp, err := c.app.SlashingKeeper.GetParams(ctx)
			require.NoError(t, err)
			require.True(t, math.LegacyMustNewDecFromStr(tc.wantDS).Equal(sp.SlashFractionDoubleSign), "double-sign %s", sp.SlashFractionDoubleSign)
			cp, err := c.app.ComplianceKeeper.Params.Get(ctx)
			require.NoError(t, err)
			require.True(t, math.LegacyMustNewDecFromStr(tc.wantCoordinated).Equal(cp.CoordinatedAttackFraction),
				"coordinated fraction %s", cp.CoordinatedAttackFraction)
			require.True(t, cp.CoordinatedAttackFraction.GTE(sp.SlashFractionDoubleSign), "C20: the sanction fraction is below the double-sign fraction")
			require.Equal(t, uint64(604_800), cp.AppealWindowSeconds, "only the fraction may move")
			require.Equal(t, legacyUnjailCooldown, cp.UnjailCooldownBlocks, "only the fraction may move")
			require.NoError(t, cp.Validate())
		})
	}
}

// A failure of the alignment halts the upgrade with nothing of the handler
// written: no attestation, no registry params, and not the double-sign raise
// that came before it.
func TestUpgradeFailingToAlignTheCoordinatedFractionWritesNothing(t *testing.T) {
	c := newTestChain(t)
	ctx := c.ctx()
	emptyEntityRegistry(t, c, ctx)
	setSlashFractions(t, c, ctx, math.LegacyNewDecWithPrec(5, 2), math.LegacyNewDecWithPrec(1, 2))
	// The compliance params the alignment reads are missing.
	require.NoError(t, c.app.ComplianceKeeper.Params.Remove(ctx))

	err := c.applyV0100(ctx, upgradetypes.Plan{Name: UpgradeV0100, Info: attestorInfo(newAcct("a").addr)})
	require.Error(t, err)
	require.ErrorContains(t, err, "v0.10.0")

	sp, err := c.app.SlashingKeeper.GetParams(ctx)
	require.NoError(t, err)
	require.Equal(t, "0.050000000000000000", sp.SlashFractionDoubleSign.String(), "the raise was written by a failed upgrade")
	require.Equal(t, "0.010000000000000000", sp.SlashFractionDowntime.String(), "the downtime change was written by a failed upgrade")
	for _, v := range c.vals {
		_, found, err := c.app.EntityregistryKeeper.EntityOf(ctx, v.val)
		require.NoError(t, err)
		require.False(t, found, "%s was attested by a failed upgrade", v.val)
	}
	p, err := c.app.EntityregistryKeeper.Params.Get(ctx)
	require.NoError(t, err)
	require.Empty(t, p.Attestors, "the attestor list was written by a failed upgrade")
}

// A legacy id that is already taken — two ACTIVE validators whose ids collide,
// or an entity of that id already in the state — makes Attest replace it. That
// must not halt the upgrade (the replaced validator, if ACTIVE, keeps its seat
// frozen), but it must not be silent either: a warning names both validators.
func TestUpgradeWarnsWhenALegacyIDReplacesAnEntity(t *testing.T) {
	c := newTestChain(t)
	var logs strings.Builder
	ctx := c.ctx().WithLogger(log.NewLogger(&logs, log.ColorOption(false)))
	emptyEntityRegistry(t, c, ctx)

	// The id the upgrade will give A is held by an entity of B; B is not ACTIVE,
	// so the upgrade does not attest it and the replacement is the only effect.
	a, b := c.vals[0].val, c.vals[1].val
	setRegistryStatus(t, c, ctx, b, vrtypes.Status_STATUS_BONDING)
	_, err := c.app.EntityregistryKeeper.Attest(ctx, c.govAddr(), entitytypes.Attestation{
		EntityID: legacyEntityID(a), DisplayName: "Squatter",
		AttestationHash: strings.Repeat("0c", 32), Validators: []sdk.ValAddress{b},
	})
	require.NoError(t, err)

	require.NoError(t, c.applyV0100(ctx, upgradetypes.Plan{Name: UpgradeV0100, Info: attestorInfo()}))

	c.requireAttestedAsLegacy(ctx, a)
	out := logs.String()
	require.Contains(t, out, "replaced an existing entity")
	require.Contains(t, out, a.String())
	require.Contains(t, out, b.String())
	_, broken := c.app.EntityregistryKeeper.RegistryInvariant(ctx)
	require.False(t, broken)
}

// The same failure through the handler the app registered: ApplyUpgrade returns
// it, so the PreBlocker fails and the node halts instead of committing.
func TestRegisteredHandlerPropagatesAnAttestationFailure(t *testing.T) {
	c := newTestChain(t)
	ctx := c.ctx()
	emptyEntityRegistry(t, c, ctx)
	_, err := c.app.EntityregistryKeeper.Attest(ctx, c.govAddr(), entitytypes.Attestation{
		EntityID: "other-owner", DisplayName: "Other owner",
		AttestationHash: strings.Repeat("0b", 32), Validators: []sdk.ValAddress{c.vals[0].val, c.vals[1].val, c.vals[2].val},
	})
	require.NoError(t, err)
	cache, _ := ctx.CacheContext()
	err = c.app.UpgradeKeeper.ApplyUpgrade(cache, upgradetypes.Plan{Name: UpgradeV0100, Height: ctx.BlockHeight()})
	require.ErrorIs(t, err, entitytypes.ErrMemberOfAnotherEntity)
}

func TestLegacyDisplayName(t *testing.T) {
	const fallback = "legacy validator"
	cases := map[string]struct{ in, want string }{
		"empty":                  {"", fallback},
		"short ascii":            {"Node One", "Node One"},
		"exactly 64 bytes":       {strings.Repeat("a", 64), strings.Repeat("a", 64)},
		"100 ascii bytes":        {strings.Repeat("a", 100), strings.Repeat("a", 64)},
		"cyrillic":               {"\u0401\u043b\u043a\u0430", "\u0401\u043b\u043a\u0430"},
		"rune ends at the cut":   {strings.Repeat("a", 62) + "\u0401" + "tail", strings.Repeat("a", 62) + "\u0401"},
		"cut inside a rune":      {strings.Repeat("a", 63) + "\u0401tail", strings.Repeat("a", 63)},
		"cut inside a 3-byte":    {strings.Repeat("a", 62) + "€tail", strings.Repeat("a", 62)},
		"cut inside a 4-byte":    {strings.Repeat("a", 61) + "😀tail", strings.Repeat("a", 61)},
		"nul byte":               {"a\x00b", fallback},
		"control character":      {"a\tb", fallback},
		"invalid utf-8":          {"\xff\xfe", fallback},
		"invalid utf-8 mid-name": {"abc\xffdef", fallback},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			got := legacyDisplayName(tc.in)
			require.Equal(t, tc.want, got)
			require.NoError(t, entitytypes.ValidateDisplayName(got), "the result must always be attestable")
		})
	}
}

func TestLegacyEntityIDIsValid(t *testing.T) {
	c := newTestChain(t)
	for _, v := range c.vals {
		id := legacyEntityID(v.val)
		require.NoError(t, entitytypes.ValidateEntityID(id))
		sum := sha256.Sum256(v.val)
		require.Equal(t, "legacy-"+hex.EncodeToString(sum[:])[:12], id)
	}
}

// removeNewModulesFromTheVersionMap puts the two modules v0.10.0 adds back to
// the state a v0.9.x chain has: no entry in the upgrade store's version map, and
// no randomness state. SetModuleVersionMap only adds entries, so the entries are
// deleted from the upgrade store itself; without that the upgrade's
// RunMigrations sees both modules as already at version 1 and never runs their
// InitGenesis. It also puts x/compliance back at consensus version 1 with the
// pre-v0.10.0 params shape (a cooldown and nothing else, no sanction sequence),
// so the upgrade's RunMigrations reaches Migrate1to2 as it does on the DevNet.
func removeNewModulesFromTheVersionMap(t *testing.T, c *testChain, ctx sdk.Context) {
	t.Helper()
	store := ctx.KVStore(c.app.GetKey(upgradetypes.StoreKey))
	for _, name := range []string{entitytypes.ModuleName, randomnesstypes.ModuleName} {
		store.Delete(append([]byte{upgradetypes.VersionMapByte}, name...))
	}
	rk := c.app.RandomnessKeeper
	require.NoError(t, rk.CurrentRound.Remove(ctx))
	require.NoError(t, rk.Params.Remove(ctx))

	vm, err := c.app.UpgradeKeeper.GetModuleVersionMap(ctx)
	require.NoError(t, err)
	vm[compliancetypes.ModuleName] = 1
	require.NoError(t, c.app.UpgradeKeeper.SetModuleVersionMap(ctx, vm))
	require.NoError(t, c.app.ComplianceKeeper.Params.Set(ctx, compliancetypes.Params{UnjailCooldownBlocks: legacyUnjailCooldown}))
	require.NoError(t, c.app.ComplianceKeeper.SanctionSeq.Set(ctx, 0))
}

// legacyUnjailCooldown is the cooldown the pre-v0.10.0 compliance params carry
// in the upgrade tests: not the default, so a migration that reset it is seen.
const legacyUnjailCooldown int64 = 1234

// upgradeThroughABlock builds a chain in the state v0.9.x leaves — no entities,
// stakeengine at its version 1 with no entity cap — schedules v0.10.0 at the next
// height and finalizes that block, so the handler runs where it does on a live
// node: the upgrade PreBlocker, before x/stakeengine's PreBlock opens the window
// in which its staking hooks sleep.
func upgradeThroughABlock(t *testing.T, attestor acct) *testChain {
	t.Helper()
	c := newTestChain(t)
	ctx := c.ctx()
	emptyEntityRegistry(t, c, ctx)

	// stakeengine as v0.9.x left it: no entity cap.
	se := c.app.StakeengineKeeper
	params, err := se.Params.Get(ctx)
	require.NoError(t, err)
	params.EntityCapFraction = math.LegacyZeroDec()
	params.EntityCapFloor = math.ZeroInt()
	require.NoError(t, se.Params.Set(ctx, params))
	st, err := se.CapState.Get(ctx)
	require.NoError(t, err)
	st.EntityCap = math.ZeroInt()
	require.NoError(t, se.CapState.Set(ctx, st))

	// A chain `saicond init`-ed by v0.9.x carries the SDK's 5% double-sign fraction.
	setSlashFractions(t, c, ctx, math.LegacyNewDecWithPrec(5, 2), math.LegacyNewDecWithPrec(1, 2))
	// ...and the DevNet's consensus params: CometBFT's stock evidence window.
	setConsensusParams(t, c, ctx, oldConsensusParams())

	// The module version map of a v0.9.x chain: no entityregistry, no randomness,
	// stakeengine at 1; randomness holds no state at all.
	removeNewModulesFromTheVersionMap(t, c, ctx)
	vm, err := c.app.UpgradeKeeper.GetModuleVersionMap(ctx)
	require.NoError(t, err)
	require.NotContains(t, vm, entitytypes.ModuleName)
	require.NotContains(t, vm, randomnesstypes.ModuleName)
	vm[stakeenginetypes.ModuleName] = 1
	require.NoError(t, c.app.UpgradeKeeper.SetModuleVersionMap(ctx, vm))

	plan := upgradetypes.Plan{Name: UpgradeV0100, Height: c.height + 1, Info: attestorInfo(attestor.addr)}
	require.NoError(t, c.app.UpgradeKeeper.ScheduleUpgrade(ctx, plan))
	c.nextBlock(time.Second)
	return c
}

// The whole upgrade as a node runs it (controller requirements 2 and 3): the
// real PreBlock order, EndBlock with V-12 halting, the stakeengine migration, and
// the staking hooks awake afterwards.
func TestUpgradeThroughARealBlock(t *testing.T) {
	attestor := newAcct("devnet-attestor")
	c := upgradeThroughABlock(t, attestor)
	ctx := c.ctx()

	// The plan ran and cleared; both modules are at their versions.
	_, err := c.app.UpgradeKeeper.GetUpgradePlan(ctx)
	require.Error(t, err, "the plan must be cleared once applied")
	vm, err := c.app.UpgradeKeeper.GetModuleVersionMap(ctx)
	require.NoError(t, err)
	require.Equal(t, uint64(1), vm[entitytypes.ModuleName])
	require.Equal(t, uint64(2), vm[stakeenginetypes.ModuleName])
	require.Equal(t, uint64(2), vm[compliancetypes.ModuleName])

	// Every validator that was ACTIVE still is, as its own entity, joined at the
	// upgrade height: the legacy attestations are the first membership there is,
	// so the vote-height convention (J < h) counts them from the next block.
	for _, v := range c.vals {
		c.requireAttestedAsLegacy(ctx, v.val)
		m, found, err := c.app.EntityregistryKeeper.EntityOf(ctx, v.val)
		require.NoError(t, err)
		require.True(t, found)
		require.Equal(t, c.height, m.JoinedHeight, "%s: a legacy attestation joins at the upgrade height", v.val)
		require.Equal(t, c.height, m.AttestedHeight, "%s", v.val)
	}
	p, err := c.app.EntityregistryKeeper.Params.Get(ctx)
	require.NoError(t, err)
	require.Equal(t, []string{attestor.addr.String()}, p.Attestors)

	// The 5% and 1% a v0.9.x chain held become the policy's 10% and 0.
	sp, err := c.app.SlashingKeeper.GetParams(ctx)
	require.NoError(t, err)
	require.Equal(t, "0.100000000000000000", sp.SlashFractionDoubleSign.String())
	require.Equal(t, "0.000000000000000000", sp.SlashFractionDowntime.String())

	// The DevNet's 100,000 blocks and 48h become the 21-day unbonding period:
	// 302,400 blocks (21 days at 6 s) and 504h, every other group as it was.
	requireOnlyEvidenceAgeMoved(t, oldConsensusParams(), consensusParams(t, c, ctx), 302_400, 21*day)
	// CometBFT is handed them: FinalizeBlock returns the params in force.
	res := c.nextBlock(time.Second)
	require.Equal(t, int64(302_400), res.ConsensusParamUpdates.Evidence.MaxAgeNumBlocks)
	require.Equal(t, 21*day, res.ConsensusParamUpdates.Evidence.MaxAgeDuration)
	ctx = c.ctx()

	// x/compliance was migrated from 1: the cooldown is kept, the two new
	// parameters are at their defaults (0.30 is not below the double-sign 0.10,
	// so the handler's alignment left it), the sanction sequence starts at 1 and
	// the new collections are empty.
	cp, err := c.app.ComplianceKeeper.Params.Get(ctx)
	require.NoError(t, err)
	require.Equal(t, compliancetypes.NewParams(legacyUnjailCooldown), cp)
	require.Equal(t, "0.300000000000000000", cp.CoordinatedAttackFraction.String())
	require.Equal(t, uint64(604_800), cp.AppealWindowSeconds)
	seq, err := c.app.ComplianceKeeper.SanctionSeq.Peek(ctx)
	require.NoError(t, err)
	require.Equal(t, uint64(1), seq)
	requireNoComplianceOffencesOrSanctions(t, c, ctx)

	// V-12 (halting, so the block itself proves it) and V-13.
	msg, broken := c.app.EntityregistryKeeper.RegistryInvariant(ctx)
	require.False(t, broken, msg)
	msg, broken = c.app.StakeengineKeeper.EntityLimitsInvariant(ctx)
	require.False(t, broken, msg)

	// What the handler leaves of the entity cap is the migration's: floor = the
	// validator floor, so a one-validator entity is not over its cap.
	params, err := c.app.StakeengineKeeper.Params.Get(ctx)
	require.NoError(t, err)
	require.Equal(t, math.LegacyNewDecWithPrec(5, 2), params.EntityCapFraction)
	require.Equal(t, params.ValidatorCapFloor, params.EntityCapFloor)
	st, err := c.app.StakeengineKeeper.CapState.Get(ctx)
	require.NoError(t, err)
	require.True(t, st.EntityCap.GTE(params.EntityCapFloor), "entity cap %s", st.EntityCap)

	// The staking hooks are awake now (the window is closed by BeginBlock): growth
	// inside the cap goes through the real hook against the new entity.
	require.NoError(t, c.delegate(ctx, c.delegators[0], c.vals[0].val, scnInt(10)))
	// ...and the stake engine sees the validator inside its legacy entity, not frozen.
	standing, err := c.app.StakeengineKeeper.EntityStanding(c.ctx(), c.vals[0].val)
	require.NoError(t, err)
	require.Equal(t, legacyEntityID(c.vals[0].val), standing.EntityID)
	require.False(t, standing.Frozen, standing.Reason)
	require.Equal(t, scnInt(validatorSelfSCN+10), standing.Tokens)
	require.Equal(t, st.EntityCap, standing.Cap)
}

// requireNoComplianceOffencesOrSanctions fails unless x/compliance holds no
// offence, no sanction and no by-entity index row.
func requireNoComplianceOffencesOrSanctions(t *testing.T, c *testChain, ctx sdk.Context) {
	t.Helper()
	k := c.app.ComplianceKeeper
	offences, err := k.Offences.Iterate(ctx, nil)
	require.NoError(t, err)
	defer func() { _ = offences.Close() }()
	require.False(t, offences.Valid(), "the upgrade invented an offence")
	index, err := k.OffencesByEntity.Iterate(ctx, nil)
	require.NoError(t, err)
	defer func() { _ = index.Close() }()
	require.False(t, index.Valid(), "the upgrade invented an offence index row")
	sanctions, err := k.Sanctions.Iterate(ctx, nil)
	require.NoError(t, err)
	defer func() { _ = sanctions.Close() }()
	require.False(t, sanctions.Valid(), "the upgrade invented a sanction")
}

// Controller requirement 4: the post-upgrade state, entityregistry data included,
// exports and imports into a fresh app without loss.
func TestPostUpgradeStateSurvivesExportAndInitGenesis(t *testing.T) {
	c := upgradeThroughABlock(t, newAcct("devnet-attestor"))

	exported, err := c.app.ExportAppStateAndValidators(false, nil, nil)
	require.NoError(t, err)
	var gen map[string]json.RawMessage
	require.NoError(t, json.Unmarshal(exported.AppState, &gen))
	var erGen entitytypes.GenesisState
	c.app.AppCodec().MustUnmarshalJSON(gen[entitytypes.ModuleName], &erGen)
	require.Len(t, erGen.Entities, 3)
	require.Len(t, erGen.Members, 3)

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
	ctx := fresh.GetContextForFinalizeBlock(nil)
	want, err := c.app.EntityregistryKeeper.ExportGenesis(c.ctx())
	require.NoError(t, err)
	got, err := fresh.EntityregistryKeeper.ExportGenesis(ctx)
	require.NoError(t, err)
	require.Equal(t, want, got)
	for _, v := range c.vals {
		m, found, err := fresh.EntityregistryKeeper.EntityOf(ctx, v.val)
		require.NoError(t, err)
		require.True(t, found)
		require.Equal(t, legacyEntityID(v.val), m.EntityId)
		status, _, err := fresh.ValidatorregistryKeeper.GetValidatorStatus(ctx, v.val)
		require.NoError(t, err)
		require.Equal(t, vrtypes.Status_STATUS_ACTIVE, status)
	}
	msg, broken := fresh.EntityregistryKeeper.RegistryInvariant(ctx)
	require.False(t, broken, msg)

	// x/compliance: the new parameters and sequence, and the (empty) new
	// collections, come back unchanged.
	wantCompliance, err := c.app.ComplianceKeeper.ExportGenesis(c.ctx())
	require.NoError(t, err)
	require.Equal(t, compliancetypes.NewParams(legacyUnjailCooldown), wantCompliance.Params)
	require.Equal(t, uint64(1), wantCompliance.NextSanctionId)
	require.Empty(t, wantCompliance.Offences)
	require.Empty(t, wantCompliance.Sanctions)
	gotCompliance, err := fresh.ComplianceKeeper.ExportGenesis(ctx)
	require.NoError(t, err)
	require.Equal(t, wantCompliance, gotCompliance)
}

// A chain upgraded from v0.9.x has no randomness module in its version map, so
// RunMigrations runs the module's default InitGenesis: round 1, scheduled
// nowhere yet, with the published initial input and no key cutoff.
func TestMigrationsInitialiseRoundOne(t *testing.T) {
	c := newTestChain(t)
	ctx := c.ctx()

	fromVM := c.app.ModuleManager.GetVersionMap()
	delete(fromVM, randomnesstypes.ModuleName)
	vm, err := c.app.ModuleManager.RunMigrations(ctx, c.app.Configurator(), fromVM)
	require.NoError(t, err)
	require.Equal(t, uint64(1), vm[randomnesstypes.ModuleName])

	res, err := randomnesskeeper.NewQueryServerImpl(c.app.RandomnessKeeper).
		Round(ctx, &randomnesstypes.QueryRoundRequest{})
	require.NoError(t, err)
	require.Equal(t, uint64(1), res.Round.Number)
	require.Equal(t, randomnesstypes.DefaultInitialInput(), res.Round.Input)
	require.Zero(t, res.Round.KeyCutoffHeight)
	require.Equal(t, randomnesstypes.RoundStatus_ROUND_STATUS_SCHEDULED, res.Round.Status)
	require.Zero(t, res.Round.RotationHeight, "InitGenesis writes the round unscheduled")
	require.Empty(t, res.Pool)
	require.Empty(t, res.Submissions)
}

// The legacy entities the handler attests enter the registry with no VRF key,
// so they hold no seat in any round's pool until their validators register one.
func TestUpgradedLegacyEntitiesAreKeyless(t *testing.T) {
	c := newTestChain(t)
	ctx := c.ctx()
	emptyEntityRegistry(t, c, ctx)

	require.NoError(t, c.applyV0100(ctx, upgradetypes.Plan{Name: UpgradeV0100, Info: attestorInfo()}))

	for _, v := range c.vals {
		c.requireAttestedAsLegacy(ctx, v.val)
		m, found, err := c.app.EntityregistryKeeper.EntityOf(ctx, v.val)
		require.NoError(t, err)
		require.True(t, found)
		require.Empty(t, m.VrfPublicKey, "%s", v.val)
		require.Zero(t, m.VrfKeyHeight, "%s", v.val)
	}
}

// syncBuffer is a log sink the chain can write from any goroutine.
type syncBuffer struct {
	mu  sync.Mutex
	buf strings.Builder
}

func (b *syncBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.Write(p)
}

func (b *syncBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.String()
}

// captureBlockLogs swaps the logger every future block context is built with
// for one that writes into the returned buffer. The harness builds its app with
// a no-op logger and BaseApp has no setter, so the unexported field is set
// through reflection; the test fails loudly if baseapp ever renames it.
func captureBlockLogs(t *testing.T, app *App) *syncBuffer {
	t.Helper()
	out := &syncBuffer{}
	f := reflect.ValueOf(app.BaseApp).Elem().FieldByName("logger")
	require.True(t, f.IsValid(), "baseapp has no logger field to capture")
	reflect.NewAt(f.Type(), unsafe.Pointer(f.UnsafeAddr())).Elem().
		Set(reflect.ValueOf(log.NewLogger(out, log.ColorOption(false))))
	return out
}

// The upgrade as a node runs it, for the second store: a v0.9.x state has no
// randomness state at all and no randomness entry in the version map. The
// upgrade block's PreBlock runs the module's InitGenesis, and the same block's
// EndBlock must find a valid round 1, schedule it, and log no randomness error.
func TestUpgradeThroughARealBlockInitialisesRandomness(t *testing.T) {
	attestor := newAcct("devnet-attestor")
	c := newTestChain(t)
	ctx := c.ctx()
	emptyEntityRegistry(t, c, ctx)

	rk := c.app.RandomnessKeeper
	removeNewModulesFromTheVersionMap(t, c, ctx)
	vm, err := c.app.UpgradeKeeper.GetModuleVersionMap(ctx)
	require.NoError(t, err)
	require.NotContains(t, vm, randomnesstypes.ModuleName)
	require.NoError(t, c.app.UpgradeKeeper.ScheduleUpgrade(ctx,
		upgradetypes.Plan{Name: UpgradeV0100, Height: c.height + 1, Info: attestorInfo(attestor.addr)}))

	logs := captureBlockLogs(t, c.app)
	c.nextBlock(time.Second)
	out := logs.String()
	require.Contains(t, out, "applying upgrade", "the capture must see the upgrade block")
	require.NotContains(t, out, "randomness:", "no randomness line, error or otherwise, in the upgrade block")

	ctx = c.ctx()
	vm, err = c.app.UpgradeKeeper.GetModuleVersionMap(ctx)
	require.NoError(t, err)
	require.Equal(t, uint64(1), vm[randomnesstypes.ModuleName])
	params, err := rk.Params.Get(ctx)
	require.NoError(t, err)
	require.Equal(t, randomnesstypes.DefaultParams(), params)

	round, err := rk.CurrentRound.Get(ctx)
	require.NoError(t, err)
	require.Equal(t, uint64(1), round.Number)
	require.Equal(t, randomnesstypes.DefaultInitialInput(), round.Input)
	_, err = randomnesstypes.DecodeHex(round.Input, randomnesstypes.InputHexLen/2)
	require.NoError(t, err, "the initial input is a valid 32-byte hex string")
	require.Zero(t, round.KeyCutoffHeight)
	require.Equal(t, randomnesstypes.RoundStatus_ROUND_STATUS_SCHEDULED, round.Status)
	require.Greater(t, round.RotationHeight, c.height, "the first EndBlock scheduled round 1")
	require.Greater(t, round.RotationHeight, round.WindowOpenHeight)

	for _, v := range c.vals {
		c.requireAttestedAsLegacy(ctx, v.val)
	}
}
