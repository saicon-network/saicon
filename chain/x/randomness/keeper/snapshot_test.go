package keeper_test

import (
	"bytes"
	"encoding/hex"
	"strings"
	"testing"

	sdk "github.com/cosmos/cosmos-sdk/types"
	"github.com/stretchr/testify/require"

	entitytypes "github.com/saicon-network/saicon/chain/x/entityregistry/types"
	"github.com/saicon-network/saicon/chain/x/randomness/types"
	vrtypes "github.com/saicon-network/saicon/chain/x/validatorregistry/types"
)

func TestWindowOpensAtTheEndBlockBeforeO(t *testing.T) {
	f := newFixture(t)
	f.run(1, 8)
	require.Equal(t, types.RoundStatus_ROUND_STATUS_SCHEDULED, f.round().Status)

	f.run(9, 9)
	require.Equal(t, types.RoundStatus_ROUND_STATUS_OPEN, f.round().Status)
	requireEvent(t, f.ctx, types.EventTypeWindowOpened, types.AttributeKeyRound, "1")
}

func TestThePool(t *testing.T) {
	f := newFixture(t)
	f.entity("acme", val(1), val(2))
	f.entity("beta", val(3))
	f.entity("gamma", val(4))
	f.entity("delta", val(5))
	f.entity("eps", val(6))
	f.active(val(1), val(2), val(4), val(5), val(6))
	f.reg.status[val(3).String()] = vrtypes.Status_STATUS_BONDING
	f.standing("gamma", 11, 10, false)
	f.standing("eps", 5, 10, true)
	// Round 2's cutoff is 10: keys bound at height 0 are eligible.
	require.NoError(t, f.k.CurrentRound.Set(f.ctx, types.Round{
		Number: 2, Input: types.DefaultInitialInput(), KeyCutoffHeight: 10,
		WindowOpenHeight: 30, RotationHeight: 40, Status: types.RoundStatus_ROUND_STATUS_SCHEDULED,
	}))
	f.key(val(1), 1, 0)
	f.key(val(2), 2, 0)
	f.key(val(4), 4, 0)

	f.run(29, 29)

	require.Equal(t, []string{"acme", "delta", "eps"}, f.poolIDs())
	acme, err := f.k.Pool.Get(f.ctx, "acme")
	require.NoError(t, err)
	lower := val(1)
	if bytes.Compare(val(2), lower) < 0 {
		lower = val(2)
	}
	require.Equal(t, lower.String(), acme.Validator)
	require.Equal(t, f.ents.members[lower.String()].VrfPublicKey, acme.VrfPublicKey)
	delta, err := f.k.Pool.Get(f.ctx, "delta")
	require.NoError(t, err)
	require.Empty(t, delta.Validator)
	require.Empty(t, delta.VrfPublicKey)

	var designated []string
	require.NoError(t, f.k.Designated.Walk(f.ctx, nil, func(k []byte, id string) (bool, error) {
		designated = append(designated, id)
		return false, nil
	}))
	require.Equal(t, []string{"acme"}, designated)
	id, err := f.k.Designated.Get(f.ctx, lower)
	require.NoError(t, err)
	require.Equal(t, "acme", id)
	requireEvent(t, f.ctx, types.EventTypeWindowOpened, types.AttributeKeyPoolSize, "3")
	requireEvent(t, f.ctx, types.EventTypeWindowOpened, types.AttributeKeyKeyed, "1")
	requireEvent(t, f.ctx, types.EventTypeWindowOpened, types.AttributeKeyRequired, "2")
}

func TestSeasoning(t *testing.T) {
	f := newFixture(t)
	f.entity("acme", val(1))
	f.entity("beta", val(2))
	f.active(val(1), val(2))
	f.key(val(1), 1, 9)
	f.key(val(2), 2, 10)
	require.NoError(t, f.k.CurrentRound.Set(f.ctx, types.Round{
		Number: 2, Input: types.DefaultInitialInput(), KeyCutoffHeight: 10,
		WindowOpenHeight: 30, RotationHeight: 40, Status: types.RoundStatus_ROUND_STATUS_SCHEDULED,
	}))

	f.run(29, 29)

	acme, err := f.k.Pool.Get(f.ctx, "acme")
	require.NoError(t, err)
	require.Equal(t, val(1).String(), acme.Validator, "height 9 < cutoff 10")
	beta, err := f.k.Pool.Get(f.ctx, "beta")
	require.NoError(t, err)
	require.Empty(t, beta.Validator, "height 10 is not < cutoff 10")
}

func TestRoundOneDesignatesNobody(t *testing.T) {
	f := newFixture(t)
	f.entity("acme", val(1))
	f.active(val(1))
	f.key(val(1), 1, 0)

	f.run(1, 9)

	require.Equal(t, types.RoundStatus_ROUND_STATUS_OPEN, f.round().Status)
	acme, err := f.k.Pool.Get(f.ctx, "acme")
	require.NoError(t, err)
	require.Empty(t, acme.Validator, "round 1 has cutoff 0: no key is eligible")
	n, err := f.k.Designated.Has(f.ctx, val(1))
	require.NoError(t, err)
	require.False(t, n)
}

func TestDesignationSkipsInactiveMembers(t *testing.T) {
	f := newFixture(t)
	f.entity("acme", val(1), val(2))
	f.reg.status[val(1).String()] = vrtypes.Status_STATUS_QUARANTINED
	f.active(val(2))
	f.key(val(1), 1, 0)
	f.key(val(2), 2, 0)
	require.NoError(t, f.k.CurrentRound.Set(f.ctx, types.Round{
		Number: 2, Input: types.DefaultInitialInput(), KeyCutoffHeight: 10,
		WindowOpenHeight: 30, RotationHeight: 40, Status: types.RoundStatus_ROUND_STATUS_SCHEDULED,
	}))

	f.run(29, 29)

	acme, err := f.k.Pool.Get(f.ctx, "acme")
	require.NoError(t, err)
	require.Equal(t, val(2).String(), acme.Validator)
}

func TestAMemberWithAMalformedKeyIsNotDesignated(t *testing.T) {
	f := newFixture(t)
	f.entity("acme", val(1), val(2))
	f.active(val(1), val(2))
	f.key(val(2), 2, 0)
	m := f.ents.members[val(1).String()]
	m.VrfPublicKey = hex.EncodeToString(bytes.Repeat([]byte{0}, 32)) // the identity: small order
	f.ents.members[val(1).String()] = m
	require.NoError(t, f.k.CurrentRound.Set(f.ctx, types.Round{
		Number: 2, Input: types.DefaultInitialInput(), KeyCutoffHeight: 10,
		WindowOpenHeight: 30, RotationHeight: 40, Status: types.RoundStatus_ROUND_STATUS_SCHEDULED,
	}))

	f.run(29, 29)

	acme, err := f.k.Pool.Get(f.ctx, "acme")
	require.NoError(t, err)
	require.Equal(t, val(2).String(), acme.Validator)
}

func TestSnapshotErrorIsRetried(t *testing.T) {
	f := newFixture(t)
	f.entity("acme", val(1))
	f.active(val(1))
	f.run(1, 8)

	f.stake.fail = true
	f.run(9, 9) // run requires a nil error from EndBlock
	require.Equal(t, types.RoundStatus_ROUND_STATUS_SCHEDULED, f.round().Status)
	require.Empty(t, f.poolIDs(), "a failed snapshot writes nothing")

	f.stake.fail = false
	f.run(10, 10)
	require.Equal(t, types.RoundStatus_ROUND_STATUS_OPEN, f.round().Status)
	require.Equal(t, []string{"acme"}, f.poolIDs())
}

func TestTicketsErrorIsRetried(t *testing.T) {
	f := newFixture(t)
	f.entity("acme", val(1))
	f.active(val(1))
	f.run(1, 8)

	f.ents.failTickets = true
	f.run(9, 9)
	require.Equal(t, types.RoundStatus_ROUND_STATUS_SCHEDULED, f.round().Status)

	f.ents.failTickets = false
	f.run(10, 10)
	require.Equal(t, types.RoundStatus_ROUND_STATUS_OPEN, f.round().Status)
}

func TestThePoolIsFixedForTheRound(t *testing.T) {
	f := newFixture(t)
	f.entity("acme", val(1))
	f.entity("beta", val(2))
	f.active(val(1), val(2))
	f.key(val(1), 1, 0)
	require.NoError(t, f.k.CurrentRound.Set(f.ctx, types.Round{
		Number: 2, Input: types.DefaultInitialInput(), KeyCutoffHeight: 10,
		WindowOpenHeight: 30, RotationHeight: 40, Status: types.RoundStatus_ROUND_STATUS_SCHEDULED,
	}))
	f.run(29, 29)
	before, err := f.k.Pool.Get(f.ctx, "acme")
	require.NoError(t, err)
	require.Equal(t, val(1).String(), before.Validator)

	// The entity is revoked and the member's key changes during the window.
	f.ents.tickets = []entitytypes.Ticket{f.ents.tickets[1]}
	f.key(val(1), 9, 31)
	f.run(30, 39)

	require.Equal(t, []string{"acme", "beta"}, f.poolIDs())
	after, err := f.k.Pool.Get(f.ctx, "acme")
	require.NoError(t, err)
	require.Equal(t, before, after)
	id, err := f.k.Designated.Get(f.ctx, val(1))
	require.NoError(t, err)
	require.Equal(t, "acme", id)
}

// A pool, a designation or a submission that a SCHEDULED round carries (an
// imported genesis can hold anything) is dropped by the snapshot: the stale
// designee cannot submit, and nothing stale survives into the round.
func TestTheSnapshotDropsAStaleDesignation(t *testing.T) {
	f := newFixture(t)
	f.entity("acme", val(1))
	f.active(val(1))
	require.NoError(t, f.k.Pool.Set(f.ctx, "ghost", types.PoolEntry{Validator: val(9).String(), VrfPublicKey: hex.EncodeToString(bytes.Repeat([]byte{1}, 32))}))
	require.NoError(t, f.k.Designated.Set(f.ctx, val(9), "ghost"))
	require.NoError(t, f.k.Submissions.Set(f.ctx, "ghost", types.Submission{EntityId: "ghost", Validator: val(9).String()}))

	f.run(1, 9)
	r := f.round()
	require.Equal(t, types.RoundStatus_ROUND_STATUS_OPEN, r.Status)

	require.Equal(t, []string{"acme"}, f.poolIDs())
	has, err := f.k.Designated.Has(f.ctx, val(9))
	require.NoError(t, err)
	require.False(t, has, "the stale designation is gone")
	has, err = f.k.Submissions.Has(f.ctx, "ghost")
	require.NoError(t, err)
	require.False(t, has, "the stale submission is gone")

	_, err = f.k.SubmitVrf(f.at(r.WindowOpenHeight), sdk.AccAddress(val(9)), r.Number, strings.Repeat("00", 80))
	require.ErrorIs(t, err, types.ErrNotDesignated)
	msg, broken := f.k.RandomnessInvariant(f.ctx)
	require.False(t, broken, msg)
}
