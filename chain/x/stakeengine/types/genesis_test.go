package types_test

import (
	"testing"

	"cosmossdk.io/math"
	sdk "github.com/cosmos/cosmos-sdk/types"
	"github.com/stretchr/testify/require"

	"github.com/saicon-network/saicon/chain/x/stakeengine/types"
)

func valoper(b byte) string {
	return sdk.MustBech32ifyAddressBytes(types.ValidatorPrefix, []byte{b, 9, 8, 7, 6, 5, 4, 3, 2, 1, 0, 1, 2, 3, 4, 5, 6, 7, 8, 9})
}

func entry(id uint64) types.QueueEntry {
	return types.QueueEntry{Id: id, Delegator: saiconAddr(1), Validator: valoper(1), Amount: math.NewInt(100 * scn), EnqueuedHeight: 10}
}

func TestDefaultGenesisIsValid(t *testing.T) {
	require.NoError(t, types.DefaultGenesis().Validate())
}

func TestGenesisValidation(t *testing.T) {
	ok := types.GenesisState{Params: types.DefaultParams(), Queue: []types.QueueEntry{entry(0), entry(1)}, NextQueueId: 2}
	require.NoError(t, ok.Validate())

	cases := map[string]types.GenesisState{
		"duplicate id":           {Params: types.DefaultParams(), Queue: []types.QueueEntry{entry(0), entry(0)}, NextQueueId: 1},
		"id not below next":      {Params: types.DefaultParams(), Queue: []types.QueueEntry{entry(2)}, NextQueueId: 2},
		"zero amount":            {Params: types.DefaultParams(), Queue: []types.QueueEntry{{Id: 0, Delegator: saiconAddr(1), Validator: valoper(1), Amount: math.ZeroInt()}}, NextQueueId: 1},
		"wrong delegator prefix": {Params: types.DefaultParams(), Queue: []types.QueueEntry{{Id: 0, Delegator: valoper(1), Validator: valoper(1), Amount: math.NewInt(scn)}}, NextQueueId: 1},
		"wrong validator prefix": {Params: types.DefaultParams(), Queue: []types.QueueEntry{{Id: 0, Delegator: saiconAddr(1), Validator: saiconAddr(1), Amount: math.NewInt(scn)}}, NextQueueId: 1},
		"negative cap state":     {Params: types.DefaultParams(), CapState: &types.CapState{GlobalCap: math.NewInt(-1), PublicCirculating: math.ZeroInt(), ValidatorCap: math.ZeroInt(), EwmaBonded: math.LegacyZeroDec()}},
		"bad params":             {Params: types.Params{}},
		"amount below min entry": {Params: types.DefaultParams(), Queue: []types.QueueEntry{{Id: 0, Delegator: saiconAddr(1), Validator: valoper(1), Amount: math.NewInt(scn - 1)}}, NextQueueId: 1},
		"negative height":        {Params: types.DefaultParams(), Queue: []types.QueueEntry{{Id: 0, Delegator: saiconAddr(1), Validator: valoper(1), Amount: math.NewInt(scn), EnqueuedHeight: -1}}, NextQueueId: 1},
		"queue over max entries": {Params: overMax(), Queue: []types.QueueEntry{entry(0), entry(1)}, NextQueueId: 2},
	}
	for name, gs := range cases {
		t.Run(name, func(t *testing.T) { require.Error(t, gs.Validate()) })
	}
}

// overMax is the default params with room for a single queue entry.
func overMax() types.Params {
	p := types.DefaultParams()
	p.MaxQueueEntries = 1
	return p
}

func TestGenesisQueueAtTheLimitsIsValid(t *testing.T) {
	p := overMax()
	p.MinQueueEntry = math.NewInt(100 * scn)
	e := entry(0) // amount is exactly min_queue_entry, height 10
	e.EnqueuedHeight = 0
	gs := types.GenesisState{Params: p, Queue: []types.QueueEntry{e}, NextQueueId: 1}
	require.NoError(t, gs.Validate(), "exactly max_queue_entries entries, amount == min_queue_entry, height 0")
}
