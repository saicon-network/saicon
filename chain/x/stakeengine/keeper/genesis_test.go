package keeper_test

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/saicon-network/saicon/chain/x/stakeengine/types"
)

func TestInitGenesis_AFreshGenesisComputesTheCaps(t *testing.T) {
	f := newFixture(t)
	require.NoError(t, f.k.InitGenesis(f.ctx, *types.DefaultGenesis()))
	st, err := f.k.CapState.Get(f.ctx)
	require.NoError(t, err)
	require.Equal(t, uint64(0), st.Epoch)
	require.Equal(t, nscn(1_680_000), st.GlobalCap)
	n, err := f.k.QueueLen.Get(f.ctx)
	require.NoError(t, err)
	require.Equal(t, uint64(0), n)
}

func TestGenesis_ExportImportRoundTrips(t *testing.T) {
	a := newFixture(t)
	a.caps(t)
	// Move the snapshot away from what a recompute on a fresh chain would give:
	// stake appears, and the epoch advances, so Epoch and the EWMA-derived
	// ValidatorCap differ from an epoch-0 recompute on b.
	a.staking.addValidator(val(9), nscn(1_000_000))
	snap, err := a.k.RecomputeCaps(a.ctx, 5, false)
	require.NoError(t, err)
	require.Equal(t, uint64(5), snap.Epoch)
	_, err = a.k.Enqueue(a.ctx, acc(1), val(1), nscn(10))
	require.NoError(t, err)
	_, err = a.k.Enqueue(a.ctx, acc(1), val(1), nscn(20))
	require.NoError(t, err)
	exported, err := a.k.ExportGenesis(a.ctx)
	require.NoError(t, err)
	require.NotNil(t, exported.CapState)
	require.Equal(t, snap, *exported.CapState)
	require.Len(t, exported.Queue, 2)
	require.Equal(t, uint64(2), exported.NextQueueId)

	b := newFixture(t)
	b.bank.set(b.k.EscrowAddress(), nscn(30)) // x/bank's genesis carries the escrow balance
	require.NoError(t, b.k.InitGenesis(b.ctx, *exported))
	restored, err := b.k.CapState.Get(b.ctx)
	require.NoError(t, err)
	require.Equal(t, *exported.CapState, restored, "the stored snapshot is restored, not recomputed")
	again, err := b.k.ExportGenesis(b.ctx)
	require.NoError(t, err)
	require.Equal(t, exported, again)

	id, err := b.k.Enqueue(b.ctx, acc(1), val(1), nscn(1))
	require.NoError(t, err)
	require.Equal(t, uint64(2), id, "the id sequence survives the round trip")
	n, err := b.k.QueueLen.Get(b.ctx)
	require.NoError(t, err)
	require.Equal(t, uint64(3), n)
}

func TestInitGenesis_RefusesAnEscrowThatDoesNotMatchTheQueue(t *testing.T) {
	f := newFixture(t)
	gs := types.DefaultGenesis()
	gs.Queue = []types.QueueEntry{{Id: 0, Delegator: acc(1).String(), Validator: val(1).String(), Amount: nscn(10), EnqueuedHeight: 1}}
	gs.NextQueueId = 1
	err := f.k.InitGenesis(f.ctx, *gs) // no escrow balance
	require.ErrorContains(t, err, "V-10")
}

func TestGenesis_ImportRebuildsTheQueueIndex(t *testing.T) {
	a := newFixture(t)
	a.caps(t)
	a.staking.addValidator(val(2), nscn(1000))
	a.bank.set(acc(9), nscn(10_000))
	for _, v := range []byte{1, 2, 2} {
		_, err := a.k.Enqueue(a.ctx, acc(9), val(v), nscn(10))
		require.NoError(t, err)
	}
	exported, err := a.k.ExportGenesis(a.ctx)
	require.NoError(t, err)

	b := newFixture(t)
	b.bank.set(b.k.EscrowAddress(), nscn(30))
	require.NoError(t, b.k.InitGenesis(b.ctx, *exported))
	require.Equal(t, uint64(1), b.indexCount(t, acc(9), val(1)))
	require.Equal(t, uint64(2), b.indexCount(t, acc(9), val(2)))
	b.requireV10(t)
}
