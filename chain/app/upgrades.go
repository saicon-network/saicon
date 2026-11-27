package app

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"time"
	"unicode/utf8"

	storetypes "cosmossdk.io/store/types"
	upgradetypes "cosmossdk.io/x/upgrade/types"
	cmtproto "github.com/cometbft/cometbft/proto/tendermint/types"
	cmttypes "github.com/cometbft/cometbft/types"
	sdk "github.com/cosmos/cosmos-sdk/types"
	"github.com/cosmos/cosmos-sdk/types/module"
	authtypes "github.com/cosmos/cosmos-sdk/x/auth/types"
	consensuskeeper "github.com/cosmos/cosmos-sdk/x/consensus/keeper"
	govtypes "github.com/cosmos/cosmos-sdk/x/gov/types"
	slashingkeeper "github.com/cosmos/cosmos-sdk/x/slashing/keeper"
	stakingkeeper "github.com/cosmos/cosmos-sdk/x/staking/keeper"

	compliancekeeper "github.com/saicon-network/saicon/chain/x/compliance/keeper"
	entityregistrykeeper "github.com/saicon-network/saicon/chain/x/entityregistry/keeper"
	entityregistrytypes "github.com/saicon-network/saicon/chain/x/entityregistry/types"
	randomnesstypes "github.com/saicon-network/saicon/chain/x/randomness/types"
	validatorregistrykeeper "github.com/saicon-network/saicon/chain/x/validatorregistry/keeper"
	vrtypes "github.com/saicon-network/saicon/chain/x/validatorregistry/types"
)

// UpgradeV0100 is the first upgrade handler this chain registers (spec §17,
// J11): S8's entity registry (W1) and per-validator VRF randomness (W2),
// upgraded into the live DevNet in place by one release. The handler is W1's;
// RunMigrations initialises x/randomness's round 1 from its default genesis.
const UpgradeV0100 = "v0.10.0"

// UpgradeV0110 is the second in-place upgrade. Its S10 part: x/participation
// goes to consensus version 2 (the WARP engine, whose Migrate1to2 writes the
// four WARP params), and x/rewards pays the PoP share through x/distribution
// (behaviour only, no store change). No store is added, so no store loader.
// What S10 checks after the migrations is in upgrades_s10.go.
const UpgradeV0110 = "v0.11.0"

// legacyAttestationDomain prefixes the fixed attestation hash every legacy
// entity carries: the hash of a sentence nobody signed, so it names the upgrade
// and the validator and nothing else (spec J12).
const legacyAttestationDomain = "saicon/entityregistry/v0.10.0-upgrade/"

// legacyDisplayNameFallback is the display name of a legacy entity whose
// validator has no usable moniker.
const legacyDisplayNameFallback = "legacy validator"

// storeUpgradesV0100 lists the stores v0.10.0 adds: x/entityregistry's and
// x/randomness's.
func storeUpgradesV0100() *storetypes.StoreUpgrades {
	return &storetypes.StoreUpgrades{Added: []string{entityregistrytypes.StoreKey, randomnesstypes.StoreKey}}
}

// registerUpgrades must run before app.Load: the store loader decides which
// stores exist when the multistore mounts.
func (app *App) registerUpgrades() {
	app.UpgradeKeeper.SetUpgradeHandler(UpgradeV0100,
		func(ctx context.Context, plan upgradetypes.Plan, fromVM module.VersionMap) (module.VersionMap, error) {
			vm, err := app.ModuleManager.RunMigrations(ctx, app.Configurator(), fromVM)
			if err != nil {
				return nil, err
			}
			return vm, applyV0100(ctx, plan, app.EntityregistryKeeper, app.StakingKeeper,
				app.ValidatorregistryKeeper, app.SlashingKeeper, app.ComplianceKeeper,
				app.ConsensusParamsKeeper, authtypes.NewModuleAddress(govtypes.ModuleName))
		})

	app.UpgradeKeeper.SetUpgradeHandler(UpgradeV0110,
		func(ctx context.Context, _ upgradetypes.Plan, fromVM module.VersionMap) (module.VersionMap, error) {
			vm, err := app.ModuleManager.RunMigrations(ctx, app.Configurator(), fromVM)
			if err != nil {
				return nil, err
			}
			logS10State(sdk.UnwrapSDKContext(ctx), app.ParticipationKeeper)
			return vm, nil
		})

	info, err := app.UpgradeKeeper.ReadUpgradeInfoFromDisk()
	if err != nil {
		panic(fmt.Errorf("reading upgrade info: %w", err))
	}
	if info.Name == UpgradeV0100 && !app.UpgradeKeeper.IsSkipHeight(info.Height) {
		app.SetStoreLoader(upgradetypes.UpgradeStoreLoader(info.Height, storeUpgradesV0100()))
	}
}

// applyV0100 is the v0.10.0 state change after the module migrations: it writes
// the registry params from the plan's info and attests every registry-ACTIVE
// validator as its own one-member entity, with the gov authority as attestor
// (spec §17, J12). No status changes: the set that was earning keeps earning.
// It also brings x/slashing's two fractions to the policy (ADR-0010 §1-2): the
// double-sign fraction is raised to 10% when the stored value is lower (owner
// 2026-10-28), and the downtime fraction is set to 0 when it is positive (owner
// 2026-07-28), so an upgraded chain meets the policy: a chain that `saicond
// init` made on v0.9.x holds the SDK's 5% and 1%. A double-sign value at or above
// 10%, and a downtime value of 0, are left alone. It then sets the consensus
// evidence window to the unbonding period (applyEvidenceWindow, owner
// 2026-10-29). Last, it lifts x/compliance's
// coordinated-attack fraction to the double-sign fraction in force when that is
// the higher of the two (alignCoordinatedFraction), so the C20 rule holds the
// moment the upgrade ends. x/compliance's own 1 -> 2 migration has already run
// in RunMigrations and left the defaults.
//
// It runs in PreBlock, outside any revertible transaction, so it is atomic by
// itself: everything is written into a cache context that is written through
// only if the whole attestation succeeded. A bad attestor list is logged and
// the defaults kept (governance can still attest, J9); a failure to attest a
// validator that is earning is returned, which halts the upgrade. A legacy id
// that is already taken is replaced and logged as a warning, never an error.
//
// Nothing here writes x/staking, so no staking hook (the x/stakeengine entity
// cap, the frozen rule) can refuse it: x/entityregistry's Attest reads and
// writes its own store only and sets no registry status.
func applyV0100(
	ctx context.Context,
	plan upgradetypes.Plan,
	er entityregistrykeeper.Keeper,
	sk *stakingkeeper.Keeper,
	vr validatorregistrykeeper.Keeper,
	sl slashingkeeper.Keeper,
	ck compliancekeeper.Keeper,
	cp consensuskeeper.Keeper,
	authority sdk.AccAddress,
) error {
	cache, write := sdk.UnwrapSDKContext(ctx).CacheContext()
	logger := cache.Logger()

	params := entityregistrytypes.DefaultParams()
	var info struct {
		Attestors []string `json:"attestors"`
	}
	err := json.Unmarshal([]byte(plan.Info), &info)
	if err == nil {
		candidate := params
		candidate.Attestors = info.Attestors
		if candidate.Attestors == nil {
			candidate.Attestors = []string{}
		}
		if err = candidate.Validate(); err == nil {
			params = candidate
		}
	}
	if err != nil {
		logger.Error("v0.10.0: plan info carries no valid attestor list; governance can add one", "error", err)
	}
	if err := er.Params.Set(cache, params); err != nil {
		return err
	}
	// Written before the attestations so that a failure in them rolls it back
	// with the rest (the cache context is not written through on an error).
	if err := applySlashingPolicy(cache, sl); err != nil {
		return err
	}
	if err := applyEvidenceWindow(cache, cp, sk); err != nil {
		return err
	}

	// Collect first: attesting while iterating would write under the iterator.
	var active []sdk.ValAddress
	if err := vr.IterateValidatorMetadata(cache, func(addr sdk.ValAddress, md vrtypes.ValidatorMetadata) bool {
		if md.Status == vrtypes.Status_STATUS_ACTIVE {
			active = append(active, addr)
		}
		return false
	}); err != nil {
		return err
	}
	sort.Slice(active, func(i, j int) bool { return string(active[i]) < string(active[j]) })

	for _, v := range active {
		moniker := ""
		if val, err := sk.GetValidator(cache, v); err == nil {
			moniker = val.Description.Moniker
		}
		id := legacyEntityID(v)
		// Read before attesting, so a replacement can name whom it displaced.
		previous, err := er.MemberValidators(cache, id)
		if err != nil {
			return err
		}
		sum := sha256.Sum256([]byte(legacyAttestationDomain + v.String()))
		replaced, err := er.Attest(cache, authority, entityregistrytypes.Attestation{
			EntityID:        id,
			DisplayName:     legacyDisplayName(moniker),
			AttestationHash: hex.EncodeToString(sum[:]),
			Validators:      []sdk.ValAddress{v},
		})
		if err != nil {
			return fmt.Errorf("v0.10.0: attesting validator %s: %w", v, err)
		}
		// A taken id — two validators whose 48-bit legacy ids collide, or an
		// entity of that id already in the state — is replaced, not refused: an
		// error here would halt the upgrade. A displaced validator that is ACTIVE
		// keeps its seat frozen (charged to this id); the operator is told.
		if replaced {
			names := make([]string, len(previous))
			for i, p := range previous {
				names[i] = p.String()
			}
			logger.Warn("v0.10.0: a legacy entity id replaced an existing entity; its former members are frozen if ACTIVE",
				"entity_id", id, "validator", v.String(), "replaced_members", strings.Join(names, ","))
		}
	}
	// Last, and after applySlashingPolicy: it must see the double-sign fraction
	// the chain is left with. An error here discards the whole cache.
	if err := alignCoordinatedFraction(cache, ck, sl); err != nil {
		return err
	}
	write()
	logger.Info(fmt.Sprintf("v0.10.0: attested %d active validators as their own entities", len(active)))
	return nil
}

// applySlashingPolicy brings x/slashing's fractions to the Register's policy: a
// double-sign fraction below 10% is raised to 10%, and a positive downtime
// fraction is set to 0 (x/compliance's BeforeValidatorSlashed reads any positive
// burn as a double-sign, which is exact only while downtime burns nothing). A
// double-sign fraction at or above 10% and a downtime fraction of 0 are left
// alone, and so is every other parameter. The caller's cache context makes it
// atomic with the rest of the handler.
func applySlashingPolicy(ctx sdk.Context, sl slashingkeeper.Keeper) error {
	params, err := sl.GetParams(ctx)
	if err != nil {
		return fmt.Errorf("v0.10.0: reading the slashing params: %w", err)
	}
	changed := false
	if floor := DoubleSignSlashFraction(); params.SlashFractionDoubleSign.LT(floor) {
		ctx.Logger().Info("v0.10.0: raising the double-sign slash fraction to the policy",
			"from", params.SlashFractionDoubleSign.String(), "to", floor.String())
		params.SlashFractionDoubleSign = floor
		changed = true
	}
	if params.SlashFractionDowntime.IsPositive() {
		zero := DowntimeSlashFraction()
		ctx.Logger().Info("v0.10.0: setting the downtime slash fraction to the policy",
			"from", params.SlashFractionDowntime.String(), "to", zero.String())
		params.SlashFractionDowntime = zero
		changed = true
	}
	if !changed {
		return nil
	}
	// SetParams does not validate.
	if err := params.Validate(); err != nil {
		return fmt.Errorf("v0.10.0: the slashing params are invalid: %w", err)
	}
	return sl.SetParams(ctx, params)
}

// evidenceMaxBlockTime is the largest average block interval at which the
// evidence window applyEvidenceWindow sets is still exactly the unbonding period.
// CometBFT (evidence/pool.go isExpired) and x/evidence treat evidence as expired
// only when it is older than max_age_num_blocks AND older than max_age_duration.
// With max_age_duration = U and max_age_num_blocks = floor(U / 6 s), evidence
// expires at age U exactly when more than U / 6 s blocks were produced in the U
// that followed the infraction, that is when the AVERAGE interval over that span,
// chain halts included, is at most 6 s. Otherwise the block limit governs and
// evidence outlives U. The DevNet measured 5.01 s (2026-08-04): 302,400 blocks
// then take 17.535 days of production, so up to about 3.465 days of cumulative
// halt inside a U-long span is absorbed, and a longer halt keeps evidence alive
// past U by the excess (halt - 3.465 days). Blocks slower than 6 s on average
// stretch the window to U x (interval / 6 s).
const evidenceMaxBlockTime = 6 * time.Second

// evidenceWindow returns the evidence age limits for an unbonding period:
// max_age_num_blocks = floor(unbonding / evidenceMaxBlockTime) and
// max_age_duration = unbonding. The genesis scripts apply the same rule
// (app/genesis_scripts_test.go pins them to it).
func evidenceWindow(unbonding time.Duration) (numBlocks int64, age time.Duration) {
	return int64(unbonding / evidenceMaxBlockTime), unbonding
}

// applyEvidenceWindow sets x/consensus's evidence age limits to the unbonding
// period (owner 2026-10-29, DEVELOPMENT.md §4 item 7a). A shorter window lets a
// double-sign reported late go unslashed, and never arm the coordinated-attack
// rule; a longer one lets x/evidence reach a validator re-created on a key whose
// unbonding has completed. So the limits are set to evidenceWindow(U) whether
// the stored ones are lower or higher, and left alone only when they are equal.
//
// The window must never exceed the unbonding period. x/compliance records a
// consensus address as removed only when its key was tombstoned, which is exact
// only while no evidence can be accepted after the old incarnation is removed. A
// removal comes no earlier than U after the unbond started (EndBlock of some
// height H). The validator can still sign at H+1, since its removal from the
// CometBFT set takes effect at H+2, so an infraction is at most one block
// interval later than the unbond start. With these limits, and the average
// interval at most 6 s as above, the evidence of an infraction at H+1 can
// therefore stay admissible after the removal for at most the interval between
// blocks H and H+1: about one block, unbounded only if the chain halted between
// those two blocks. Raising either limit beyond U widens that into the
// re-created-key gap, in x/evidence upstream and in the W3 removal marker. A
// governance change to the staking unbonding_time desynchronises the window
// too (a shorter U leaves the window above it); ./scripts/gov-evidence-window.sh
// detects it.
//
// x/consensus stores its params as one record, so the record is read, only the
// evidence age limits are changed, and it is written back: evidence max_bytes
// and the block, validator, version and abci groups keep their bytes. The result
// is validated as x/consensus validates a MsgUpdateParams (ValidateBasic, then
// ValidateUpdate at this height), so the upgrade never writes params the node
// would refuse; a failure is returned and halts the upgrade. The caller's cache
// context makes it atomic with the rest of the handler.
func applyEvidenceWindow(ctx sdk.Context, cp consensuskeeper.Keeper, sk *stakingkeeper.Keeper) error {
	unbonding, err := sk.UnbondingTime(ctx)
	if err != nil {
		return fmt.Errorf("v0.10.0: reading the unbonding time: %w", err)
	}
	stored, err := cp.ParamsStore.Get(ctx)
	if err != nil {
		return fmt.Errorf("v0.10.0: reading the consensus params: %w", err)
	}
	if stored.Block == nil || stored.Evidence == nil || stored.Validator == nil {
		return fmt.Errorf("v0.10.0: the stored consensus params lack a block, evidence or validator group")
	}
	numBlocks, age := evidenceWindow(unbonding)
	old := *stored.Evidence
	if old.MaxAgeNumBlocks == numBlocks && old.MaxAgeDuration == age {
		return nil
	}

	next := stored
	next.Evidence = &cmtproto.EvidenceParams{MaxAgeNumBlocks: numBlocks, MaxAgeDuration: age, MaxBytes: old.MaxBytes}
	// x/consensus's UpdateParams fills a missing version group with zero before
	// converting; the same is done here for the check only, so the written record
	// keeps its version group as it was.
	check := next
	if check.Version == nil {
		check.Version = &cmtproto.VersionParams{}
	}
	if err := cmttypes.ConsensusParamsFromProto(check).ValidateBasic(); err != nil {
		return fmt.Errorf("v0.10.0: the consensus params with the new evidence window are invalid: %w", err)
	}
	current := stored
	if current.Version == nil {
		current.Version = &cmtproto.VersionParams{}
	}
	if err := cmttypes.ConsensusParamsFromProto(current).ValidateUpdate(&check, ctx.BlockHeight()); err != nil {
		return fmt.Errorf("v0.10.0: the consensus params update is refused: %w", err)
	}

	ctx.Logger().Info("v0.10.0: setting the evidence window to the unbonding period",
		"unbonding", unbonding.String(),
		"max_age_num_blocks_from", old.MaxAgeNumBlocks, "max_age_num_blocks_to", numBlocks,
		"max_age_duration_from", old.MaxAgeDuration.String(), "max_age_duration_to", age.String())
	if err := cp.ParamsStore.Set(ctx, next); err != nil {
		return fmt.Errorf("v0.10.0: writing the consensus params: %w", err)
	}
	return nil
}

// alignCoordinatedFraction keeps x/compliance's coordinated_attack_fraction at or
// above x/slashing's double-sign fraction (spec §9, C20; §14). The migrated
// default 0.30 is above the policy's 0.10, so on the shipped values nothing moves;
// a chain whose governance had set the double-sign fraction higher than 0.30
// would otherwise start the new rule with a sanction fraction below the
// double-sign one, which MsgUpdateParams would have refused. The fraction is
// raised to the double-sign fraction and never lowered, and no other compliance
// parameter is touched. It reads the double-sign fraction as it stands, so it
// must run after applySlashingPolicy. The caller's cache context makes it
// atomic with the rest of the handler.
func alignCoordinatedFraction(ctx sdk.Context, ck compliancekeeper.Keeper, sl slashingkeeper.Keeper) error {
	slashing, err := sl.GetParams(ctx)
	if err != nil {
		return fmt.Errorf("v0.10.0: reading the slashing params: %w", err)
	}
	params, err := ck.Params.Get(ctx)
	if err != nil {
		return fmt.Errorf("v0.10.0: reading the compliance params: %w", err)
	}
	doubleSign := slashing.SlashFractionDoubleSign
	if !params.CoordinatedAttackFraction.LT(doubleSign) {
		return nil
	}
	ctx.Logger().Info("v0.10.0: raising the coordinated-attack fraction to the double-sign fraction in force",
		"from", params.CoordinatedAttackFraction.String(), "to", doubleSign.String())
	params.CoordinatedAttackFraction = doubleSign
	// Params.Set does not validate; the raised value must stay inside the bounds.
	if err := params.Validate(); err != nil {
		return fmt.Errorf("v0.10.0: the compliance params are invalid: %w", err)
	}
	if err := ck.Params.Set(ctx, params); err != nil {
		return fmt.Errorf("v0.10.0: writing the compliance params: %w", err)
	}
	return nil
}

// legacyEntityID is the id of the entity the upgrade makes of one validator:
// "legacy-" and the first 12 hex digits of the SHA-256 of its operator bytes.
func legacyEntityID(val sdk.ValAddress) string {
	sum := sha256.Sum256(val)
	return "legacy-" + hex.EncodeToString(sum[:])[:12]
}

// legacyDisplayName turns a staking moniker into a display name the registry
// accepts: cut to 64 bytes on a rune boundary, and replaced by a fixed name when
// what is left is empty, not valid UTF-8 or holds a control character. A
// validator must never go unattested because of how it spelled its moniker.
func legacyDisplayName(moniker string) string {
	cut := min(len(moniker), entityregistrytypes.MaxDisplayNameBytes)
	for cut > 0 && cut < len(moniker) && !utf8.RuneStart(moniker[cut]) {
		cut--
	}
	name := moniker[:cut]
	if entityregistrytypes.ValidateDisplayName(name) != nil {
		return legacyDisplayNameFallback
	}
	return name
}
