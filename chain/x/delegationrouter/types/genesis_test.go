package types_test

import (
	codectypes "github.com/cosmos/cosmos-sdk/codec/types"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/saicon-network/saicon/chain/x/delegationrouter/types"
)

func TestDefaultGenesisIsValid(t *testing.T) {
	require.NoError(t, types.DefaultGenesis().Validate())
}

func TestGenesisRejectsBadParams(t *testing.T) {
	gs := types.DefaultGenesis()
	gs.Params.MaxRouteLegs = 0
	require.Error(t, gs.Validate())
}

func TestRegisterInterfacesStandsAlone(t *testing.T) {
	// compiles and runs without the module wired into the app
	require.NotPanics(t, func() {
		reg := codectypes.NewInterfaceRegistry()
		types.RegisterInterfaces(reg)
	})
}
