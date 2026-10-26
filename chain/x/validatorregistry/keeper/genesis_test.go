package keeper_test

import (
	"testing"
	"time"

	sdk "github.com/cosmos/cosmos-sdk/types"
	"github.com/stretchr/testify/require"

	"github.com/saicon-network/saicon/chain/x/validatorregistry/types"
)

func TestGenesis(t *testing.T) {
	genesisState := types.GenesisState{
		Params: types.DefaultParams(),
	}

	f := initFixture(t)
	err := f.keeper.InitGenesis(f.ctx, genesisState)
	require.NoError(t, err)
	got, err := f.keeper.ExportGenesis(f.ctx)
	require.NoError(t, err)
	require.NotNil(t, got)

	require.EqualExportedValues(t, genesisState.Params, got.Params)
}

// D2 (HIGH) regression: the registry must survive an export/import cycle.
//
// Before this fix GenesisState carried only Params, so a routine coordinated
// upgrade (`saicond export` -> edit -> restart) silently erased every record:
// participation and rewards then saw "not found" for every validator and nobody
// ever earned again, AND the V-3 self-stake lock-up stopped being enforced —
// the ante decorator reads this same metadata, so a missing record reads as
// "not locked" and a 180-day commitment evaporated on restart.
func TestGenesis_RoundTripPreservesEveryField(t *testing.T) {
	f := initFixture(t)

	bondingStart := time.Date(2026, 3, 1, 9, 30, 0, 0, time.UTC)
	unbondingStart := time.Date(2026, 7, 1, 12, 0, 0, 0, time.UTC)
	completion := time.Date(2026, 7, 22, 12, 0, 0, 0, time.UTC)

	addrA := sdk.ValAddress([]byte("genesis-validator-aa"))[:20]
	addrB := sdk.ValAddress([]byte("genesis-validator-bb"))[:20]

	// A: mid-exit, with every timing field populated — the hardest case.
	mdA := types.ValidatorMetadata{
		Status:                 types.Status_STATUS_UNBONDING,
		BondingStart:           bondingStart,
		BondingStartHeight:     4211,
		LockupDurationSeconds:  5 * 365 * 24 * 60 * 60, // a frozen custom lock-up
		UnbondingStart:         &unbondingStart,
		UnbondingCompleteTime:  completion,
		ComplianceFlag:         false,
		LastStatusChangeHeight: 9902,
	}
	// B: an ordinary active validator.
	mdB := types.ValidatorMetadata{
		Status:                 types.Status_STATUS_ACTIVE,
		BondingStart:           bondingStart,
		BondingStartHeight:     10,
		LockupDurationSeconds:  types.DefaultLockupDurationSeconds,
		ComplianceFlag:         true,
		LastStatusChangeHeight: 12,
	}
	require.NoError(t, f.keeper.SetValidatorMetadata(f.ctx, addrA, mdA))
	require.NoError(t, f.keeper.SetValidatorMetadata(f.ctx, addrB, mdB))

	exported, err := f.keeper.ExportGenesis(f.ctx)
	require.NoError(t, err)
	require.Len(t, exported.Validators, 2, "every record must be exported")
	require.NoError(t, exported.Validate())

	// Import into a pristine keeper, exactly as an ORDINARY restart would: the new
	// chain resumes above the exported heights, so nothing is rebased.
	fresh := initFixture(t)
	fresh.ctx = sdk.UnwrapSDKContext(fresh.ctx).WithBlockHeight(20000)
	require.NoError(t, fresh.keeper.InitGenesis(fresh.ctx, *exported))

	for _, tc := range []struct {
		addr sdk.ValAddress
		want types.ValidatorMetadata
	}{{addrA, mdA}, {addrB, mdB}} {
		got, found, gerr := fresh.keeper.GetValidatorMetadata(fresh.ctx, tc.addr)
		require.NoError(t, gerr)
		require.True(t, found, "validator %s must survive the restart", tc.addr)

		require.Equal(t, tc.want.Status, got.Status)
		require.Equal(t, tc.want.BondingStart.UTC(), got.BondingStart.UTC())
		require.Equal(t, tc.want.BondingStartHeight, got.BondingStartHeight)
		require.Equal(t, tc.want.LockupDurationSeconds, got.LockupDurationSeconds,
			"a frozen custom lock-up must not silently revert to the default")
		require.Equal(t, tc.want.ComplianceFlag, got.ComplianceFlag,
			"a sanction must survive the restart")
		require.Equal(t, tc.want.LastStatusChangeHeight, got.LastStatusChangeHeight)
		require.Equal(t, tc.want.UnbondingCompleteTime.UTC(), got.UnbondingCompleteTime.UTC(),
			"the frozen cooldown completion must survive — V-5 verifies against it")
		if tc.want.UnbondingStart == nil {
			require.Nil(t, got.UnbondingStart)
		} else {
			require.NotNil(t, got.UnbondingStart)
			require.Equal(t, tc.want.UnbondingStart.UTC(), got.UnbondingStart.UTC())
		}
	}
}

// A zero-height export restarts the chain near height 1 and rewrites every
// staking unbonding/redelegation entry to CreationHeight 0 (app/export.go).
// Carrying the registry's absolute heights across verbatim breaks two guards in
// opposite directions:
//
//   - V-3 stops firing. Its invariant flags a self-stake exit only when the
//     entry's CreationHeight >= bonding_start_height; with entries at 0 and an
//     imported height in the millions, nothing ever matches again — the fail-loud
//     lock-up backstop is silently inert.
//   - Jail recovery bricks. MsgUnjail computes now - last_status_change_height;
//     imported from the old chain that is negative forever, so the cooldown never
//     clears — the same permanent-brick class as the D1 blocker.
func TestGenesis_RebasesHeightsOntoTheNewChain(t *testing.T) {
	f := initFixture(t)
	addr := sdk.ValAddress([]byte("zero-height-restart!"))[:20]
	require.NoError(t, f.keeper.SetValidatorMetadata(f.ctx, addr, types.ValidatorMetadata{
		Status:                 types.Status_STATUS_JAILED,
		BondingStart:           time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC),
		BondingStartHeight:     12_000_000,
		LastStatusChangeHeight: 12_000_500,
		LockupDurationSeconds:  types.DefaultLockupDurationSeconds,
	}))

	exported, err := f.keeper.ExportGenesis(f.ctx)
	require.NoError(t, err)

	// The new chain begins at height 1.
	fresh := initFixture(t)
	fresh.ctx = sdk.UnwrapSDKContext(fresh.ctx).WithBlockHeight(1)
	require.NoError(t, fresh.keeper.InitGenesis(fresh.ctx, *exported))

	got, found, err := fresh.keeper.GetValidatorMetadata(fresh.ctx, addr)
	require.NoError(t, err)
	require.True(t, found)

	require.Equal(t, int64(1), got.BondingStartHeight,
		"a height from the old chain would make the V-3 guard match nothing")
	require.Equal(t, int64(1), got.LastStatusChangeHeight,
		"a height ahead of the chain makes the unjail cooldown negative forever")
	require.Positive(t, got.BondingStartHeight,
		"zero is the V-3 false-positive-halt primitive and must never be stored")

	// The sanction and the lock-up clock itself are untouched — only the height
	// space is rebased.
	require.Equal(t, types.Status_STATUS_JAILED, got.Status)
	require.Equal(t, time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC), got.BondingStart.UTC())
}

// A record that would break invariant V-5 on the very first block must be
// refused at STARTUP, not accepted and then panicked on.
//
// GenesisState.Validate cannot carry this: module.Manager.InitGenesis never calls
// it — it is reachable only from the `validate-genesis` and `gentx` CLI commands.
// A hand-edited genesis (the very export → edit → restart ceremony this feature
// exists for) would otherwise arm a deterministic, network-wide, restart-surviving
// halt on block one.
func TestGenesis_RefusesRecordThatWouldArmTheV5Panic(t *testing.T) {
	f := initFixture(t)
	genesisTime := time.Date(2026, 7, 1, 0, 0, 0, 0, time.UTC)
	f.ctx = sdk.UnwrapSDKContext(f.ctx).WithBlockTime(genesisTime)

	addr := sdk.AccAddress([]byte("armed-v5-panic-recrd"))[:20]
	gs := types.GenesisState{
		Params: types.DefaultParams(),
		Validators: []types.ValidatorMetadata{{
			ValidatorAddress:      addr.String(),
			Status:                types.Status_STATUS_INACTIVE,
			BondingStart:          genesisTime,
			LockupDurationSeconds: types.DefaultLockupDurationSeconds,
			// exited, yet its cooldown ends in the future -> V-5 broken -> panic
			UnbondingCompleteTime: genesisTime.Add(24 * time.Hour),
		}},
	}

	err := f.keeper.InitGenesis(f.ctx, gs)
	require.Error(t, err, "a genesis that would halt the chain must fail to start it")
	require.ErrorContains(t, err, "V-5")

	// Proof the refusal is what protects us: the same record, accepted, breaks it.
	require.NoError(t, f.keeper.SetValidatorMetadata(f.ctx, sdk.ValAddress(addr), gs.Validators[0]))
	_, broken := f.keeper.UnstakeCooldownInvariant(f.ctx)
	require.True(t, broken, "the refused record does break V-5 — the guard is not vacuous")
}

// validate-genesis is not on the start path, so InitGenesis itself refuses a
// lock-up too long to express in nanoseconds; one above the ten-year registration
// cap but representable imports unchanged, since it may predate the cap.
func TestGenesis_LockupBounds(t *testing.T) {
	genesisTime := time.Date(2026, 7, 1, 0, 0, 0, 0, time.UTC)
	record := func(lockup int64) types.GenesisState {
		return types.GenesisState{
			Params: types.DefaultParams(),
			Validators: []types.ValidatorMetadata{{
				ValidatorAddress:      sdk.AccAddress([]byte("long-lockup-recordxx"))[:20].String(),
				Status:                types.Status_STATUS_ACTIVE,
				BondingStart:          genesisTime,
				LockupDurationSeconds: lockup,
			}},
		}
	}

	f := initFixture(t)
	f.ctx = sdk.UnwrapSDKContext(f.ctx).WithBlockTime(genesisTime)
	err := f.keeper.InitGenesis(f.ctx, record(types.MaxRepresentableLockupSeconds+1))
	require.ErrorContains(t, err, "overflows the expiry")

	f = initFixture(t)
	f.ctx = sdk.UnwrapSDKContext(f.ctx).WithBlockTime(genesisTime)
	require.NoError(t, f.keeper.InitGenesis(f.ctx, record(types.MaxLockupDurationSeconds+1)))
}

// The canonical valoper form must import too: the module's own export uses the
// account prefix (its injected codec is the account codec), but a hand-edited
// genesis may reasonably use valoper. Accepting only one form means a genesis
// that passes `validate-genesis` and then fails to start.
func TestGenesis_AcceptsBothAddressForms(t *testing.T) {
	raw := []byte("both-address-forms!!")[:20]

	for _, form := range []string{
		sdk.AccAddress(raw).String(),
		sdk.ValAddress(raw).String(),
	} {
		t.Run(form[:12], func(t *testing.T) {
			f := initFixture(t)
			gs := types.GenesisState{
				Params: types.DefaultParams(),
				Validators: []types.ValidatorMetadata{{
					ValidatorAddress:      form,
					Status:                types.Status_STATUS_ACTIVE,
					BondingStart:          time.Unix(0, 0).UTC(),
					BondingStartHeight:    1,
					LockupDurationSeconds: types.DefaultLockupDurationSeconds,
				}},
			}
			require.NoError(t, gs.Validate())
			require.NoError(t, f.keeper.InitGenesis(f.ctx, gs))

			_, found, err := f.keeper.GetValidatorMetadata(f.ctx, sdk.ValAddress(raw))
			require.NoError(t, err)
			require.True(t, found, "both forms decode to the same validator")
		})
	}
}

// Export order must be deterministic — genesis is consensus-critical state and
// two nodes exporting the same store must produce identical output.
func TestGenesis_ExportIsDeterministic(t *testing.T) {
	f := initFixture(t)

	// Insert out of key order.
	for _, name := range []string{"val-zzz-deterministic", "val-aaa-deterministic", "val-mmm-deterministic"} {
		addr := sdk.ValAddress(name)[:20]
		require.NoError(t, f.keeper.SetValidatorMetadata(f.ctx, addr, types.ValidatorMetadata{
			Status:                types.Status_STATUS_ACTIVE,
			BondingStart:          time.Unix(0, 0).UTC(),
			LockupDurationSeconds: types.DefaultLockupDurationSeconds,
			ComplianceFlag:        true,
		}))
	}

	first, err := f.keeper.ExportGenesis(f.ctx)
	require.NoError(t, err)
	require.Len(t, first.Validators, 3)

	for i := 0; i < 5; i++ {
		again, aerr := f.keeper.ExportGenesis(f.ctx)
		require.NoError(t, aerr)
		require.Equal(t, first.Validators, again.Validators, "export order must be stable")
	}
}

// A genesis with no validators (a brand-new chain) must import cleanly.
func TestGenesis_EmptyValidatorsIsFine(t *testing.T) {
	f := initFixture(t)
	gen := types.DefaultGenesis()
	require.Empty(t, gen.Validators)
	require.NoError(t, gen.Validate())
	require.NoError(t, f.keeper.InitGenesis(f.ctx, *gen))
}

func TestGenesisState_ValidateRejectsBadRecords(t *testing.T) {
	good := types.ValidatorMetadata{
		// Written the way the keeper normalises it — see the note in Validate:
		// the module's injected codec is the ACCOUNT codec, so stored validator
		// addresses carry the account prefix.
		ValidatorAddress:      sdk.AccAddress([]byte("validate-genesis-val"))[:20].String(),
		Status:                types.Status_STATUS_ACTIVE,
		BondingStart:          time.Unix(0, 0).UTC(),
		LockupDurationSeconds: types.DefaultLockupDurationSeconds,
	}

	t.Run("duplicate address", func(t *testing.T) {
		gs := types.GenesisState{Params: types.DefaultParams(), Validators: []types.ValidatorMetadata{good, good}}
		require.ErrorContains(t, gs.Validate(), "duplicate")
	})

	t.Run("unparseable address", func(t *testing.T) {
		bad := good
		bad.ValidatorAddress = "not-a-bech32-address"
		gs := types.GenesisState{Params: types.DefaultParams(), Validators: []types.ValidatorMetadata{bad}}
		require.Error(t, gs.Validate())
	})

	t.Run("empty address", func(t *testing.T) {
		bad := good
		bad.ValidatorAddress = ""
		gs := types.GenesisState{Params: types.DefaultParams(), Validators: []types.ValidatorMetadata{bad}}
		require.Error(t, gs.Validate())
	})

	t.Run("unspecified status", func(t *testing.T) {
		bad := good
		bad.Status = types.Status_STATUS_UNSPECIFIED
		gs := types.GenesisState{Params: types.DefaultParams(), Validators: []types.ValidatorMetadata{bad}}
		require.ErrorContains(t, gs.Validate(), "status")
	})

	t.Run("negative lockup duration", func(t *testing.T) {
		bad := good
		bad.LockupDurationSeconds = -1
		gs := types.GenesisState{Params: types.DefaultParams(), Validators: []types.ValidatorMetadata{bad}}
		require.Error(t, gs.Validate())
	})

	t.Run("valid genesis passes", func(t *testing.T) {
		gs := types.GenesisState{Params: types.DefaultParams(), Validators: []types.ValidatorMetadata{good}}
		require.NoError(t, gs.Validate())
	})
}
