package app

import (
	"encoding/json"
	"math/rand"
	"os"
	"path"
	"regexp"
	"testing"

	"cosmossdk.io/depinject"
	"cosmossdk.io/log"
	"cosmossdk.io/math"
	"github.com/cosmos/cosmos-sdk/codec"
	codectypes "github.com/cosmos/cosmos-sdk/codec/types"
	"github.com/cosmos/cosmos-sdk/types/module"
	simtypes "github.com/cosmos/cosmos-sdk/types/simulation"
	slashingsim "github.com/cosmos/cosmos-sdk/x/slashing/simulation"
	slashingtypes "github.com/cosmos/cosmos-sdk/x/slashing/types"
	"github.com/stretchr/testify/require"
)

// The Register's slashing policy (ADR-0010 §1-2, project decisions 2026-07-28 and
// 2026-10-28) is carried by the binary, not only by the genesis scripts: a chain
// started by plain `saicond init` and the in-process harness begin from the
// app's default genesis, and before this pin they ran the SDK's 5% double-sign
// fraction and 1% downtime fraction. (The simulations draw their own slashing
// genesis; app/sim_params.json pins it for them.)
var (
	policyDoubleSign = math.LegacyNewDecWithPrec(10, 2)
	policyDowntime   = math.LegacyZeroDec()
)

func slashingParamsOf(t *testing.T, cdc codec.JSONCodec, gen GenesisState) slashingtypes.Params {
	t.Helper()
	raw, ok := gen[slashingtypes.ModuleName]
	require.True(t, ok, "the default genesis has no slashing section")
	var sg slashingtypes.GenesisState
	require.NoError(t, cdc.UnmarshalJSON(raw, &sg))
	return sg.Params
}

func requireThePolicy(t *testing.T, p slashingtypes.Params, where string) {
	t.Helper()
	require.Equal(t, policyDoubleSign.String(), p.SlashFractionDoubleSign.String(),
		"%s: double-sign must be slashed at 10%% of bonded stake, not the SDK's 5%%", where)
	require.Equal(t, policyDowntime.String(), p.SlashFractionDowntime.String(),
		"%s: downtime must carry NO stake slash (the compliance hook reads any positive burn as a double-sign)", where)
}

// The app's DefaultGenesis is what the harness starts from.
func TestAppDefaultGenesisCarriesTheSlashingPolicy(t *testing.T) {
	c := newTestChain(t)
	gen := c.app.DefaultGenesis()
	requireThePolicy(t, slashingParamsOf(t, c.app.AppCodec(), gen), "App.DefaultGenesis")
}

// `saicond init` and `saicond testnet` build their genesis from the basic manager
// that depinject hands the root command, a different object from the App's.
func TestSaicondInitDefaultGenesisCarriesTheSlashingPolicy(t *testing.T) {
	var (
		mbm module.BasicManager
		cdc codec.Codec
	)
	require.NoError(t, depinject.Inject(
		depinject.Configs(AppConfig(), depinject.Supply(log.NewNopLogger())),
		&mbm, &cdc))
	requireThePolicy(t, slashingParamsOf(t, cdc, GenesisState(mbm.DefaultGenesis(cdc))), "BasicManager.DefaultGenesis")

	// The custom basic must still validate its own default and refuse nonsense.
	basic := mbm[slashingtypes.ModuleName].(module.HasGenesisBasics)
	raw := mbm.DefaultGenesis(cdc)[slashingtypes.ModuleName]
	require.NoError(t, basic.ValidateGenesis(cdc, nil, raw))

	var sg slashingtypes.GenesisState
	require.NoError(t, cdc.UnmarshalJSON(raw, &sg))
	sg.Params.SlashFractionDoubleSign = math.LegacyNewDecWithPrec(15, 1) // 150%
	require.Error(t, basic.ValidateGenesis(cdc, nil, cdc.MustMarshalJSON(&sg)),
		"a double-sign fraction above 1 must be refused")
}

// The simulations draw their slashing genesis at random and never read the
// default, so app/sim_params.json (passed by `make test-sim`) pins the policy
// for them.
func TestSimParamsPinTheSlashingPolicy(t *testing.T) {
	raw, err := os.ReadFile("sim_params.json")
	require.NoError(t, err)
	var params map[string]any
	require.NoError(t, json.Unmarshal(raw, &params))
	for key, want := range map[string]math.LegacyDec{
		"slash_fraction_double_sign": policyDoubleSign,
		"slash_fraction_downtime":    policyDowntime,
	} {
		got, ok := params[key].(string)
		require.True(t, ok, "sim_params.json does not pin %s, so the simulations draw it at random", key)
		dec, err := math.LegacyNewDecFromStr(got)
		require.NoError(t, err)
		require.True(t, want.Equal(dec), "sim_params.json pins %s to %s, the policy is %s", key, dec, want)
	}

	// ...and the SDK's own generator takes the pinned values, whatever its random source says.
	appParams := make(simtypes.AppParams)
	require.NoError(t, json.Unmarshal(raw, &appParams))
	state := &module.SimulationState{
		AppParams: appParams,
		Cdc:       codec.NewProtoCodec(codectypes.NewInterfaceRegistry()),
		Rand:      rand.New(rand.NewSource(7)),
		GenState:  map[string]json.RawMessage{},
	}
	slashingsim.RandomizedGenState(state)
	requireThePolicy(t, slashingParamsOf(t, state.Cdc, GenesisState(state.GenState)), "the simulations' slashing genesis")
}

// The genesis scripts overwrite the same two values with jq. Two places that say
// the same thing drift; this keeps them equal.
func TestGenesisScriptsAndTheBinaryAgreeOnTheSlashingPolicy(t *testing.T) {
	c := newTestChain(t)
	want := slashingParamsOf(t, c.app.AppCodec(), c.app.DefaultGenesis())

	for _, script := range genesisScripts {
		t.Run(path.Base(script), func(t *testing.T) {
			src, err := os.ReadFile(script)
			require.NoError(t, err)

			for key, binary := range map[string]math.LegacyDec{
				"slash_fraction_double_sign": want.SlashFractionDoubleSign,
				"slash_fraction_downtime":    want.SlashFractionDowntime,
			} {
				m := regexp.MustCompile(key + ` = "([0-9.]+)"`).FindSubmatch(src)
				require.NotNil(t, m, "%s does not set %s", script, key)
				inScript, err := math.LegacyNewDecFromStr(string(m[1]))
				require.NoError(t, err)
				require.True(t, inScript.Equal(binary),
					"%s sets %s to %s, the binary's default genesis carries %s", script, key, inScript, binary)
			}
		})
	}
}
