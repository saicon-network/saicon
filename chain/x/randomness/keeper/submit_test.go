package keeper_test

import (
	"encoding/hex"
	"strings"
	"testing"

	storetypes "cosmossdk.io/store/types"
	sdk "github.com/cosmos/cosmos-sdk/types"
	"github.com/stretchr/testify/require"

	"github.com/saicon-network/saicon/chain/crypto/vrf"
	"github.com/saicon-network/saicon/chain/x/randomness/types"
)

// recordingMeter is an infinite gas meter that remembers the descriptors it was
// charged under, so a test can tell the verification charge from the store's.
type recordingMeter struct {
	storetypes.GasMeter
	descriptors []string
}

func newRecordingMeter() *recordingMeter {
	return &recordingMeter{GasMeter: storetypes.NewInfiniteGasMeter()}
}

func (m *recordingMeter) ConsumeGas(amount storetypes.Gas, descriptor string) {
	m.descriptors = append(m.descriptors, descriptor)
	m.GasMeter.ConsumeGas(amount, descriptor)
}

func (m *recordingMeter) chargedVerification() bool {
	for _, d := range m.descriptors {
		if d == "ecvrf verify" {
			return true
		}
	}
	return false
}

// roundTwo builds the submission fixture: round 2 OPEN with window [30, 40),
// entities acme (val 1, keyed K1 at height 0) and beta (val 2, keyed K2).
type roundTwo struct {
	*fixture
	k1, k2 vrf.PrivateKey
}

func newRoundTwo(t *testing.T) *roundTwo {
	t.Helper()
	f := newFixture(t)
	f.entity("acme", val(1))
	f.entity("beta", val(2))
	f.active(val(1), val(2))
	rt := &roundTwo{fixture: f, k1: f.key(val(1), 1, 0), k2: f.key(val(2), 2, 0)}
	f.run(1, 29)
	r := f.round()
	require.EqualValues(t, 2, r.Number)
	require.Equal(t, types.RoundStatus_ROUND_STATUS_OPEN, r.Status)
	require.EqualValues(t, 30, r.WindowOpenHeight)
	require.EqualValues(t, 40, r.RotationHeight)
	return rt
}

// proofHex is k's proof over the alpha of the given round and input.
func proofHex(k vrf.PrivateKey, round uint64, inputHex string) string {
	input, err := hex.DecodeString(inputHex)
	if err != nil {
		panic(err)
	}
	return hex.EncodeToString(k.Prove(types.RoundAlpha(testChainID, round, input)))
}

// current is k's proof for the current round.
func (rt *roundTwo) current(k vrf.PrivateKey) string {
	r := rt.round()
	return proofHex(k, r.Number, r.Input)
}

func (rt *roundTwo) submissionCount() int {
	n := 0
	require.NoError(rt.t, rt.k.Submissions.Walk(rt.ctx, nil, func(string, types.Submission) (bool, error) {
		n++
		return false, nil
	}))
	return n
}

func TestSubmitAcceptsTheDesignatedProof(t *testing.T) {
	rt := newRoundTwo(t)
	proof := rt.current(rt.k1)
	meter := newRecordingMeter()
	ctx := rt.at(31).WithGasMeter(meter)

	out, err := rt.k.SubmitVrf(ctx, acc(1), 2, proof)
	require.NoError(t, err)

	r := rt.round()
	want, err := vrf.Verify(rt.k1.PublicKey(), hexBytes(t, proof, vrf.ProofSize),
		types.RoundAlpha(testChainID, 2, hexBytes(t, r.Input, 32)))
	require.NoError(t, err)
	require.Equal(t, hex.EncodeToString(want), out)

	s, err := rt.k.Submissions.Get(ctx, "acme")
	require.NoError(t, err)
	require.Equal(t, types.Submission{
		EntityId: "acme", Validator: val(1).String(), Output: out, Proof: proof, Height: 31,
	}, s)
	require.True(t, meter.chargedVerification())
	require.GreaterOrEqual(t, meter.GasConsumed(), vrf.VerifyGas)

	requireEvent(t, ctx, types.EventTypeOutputSubmitted, types.AttributeKeyRound, "2")
	requireEvent(t, ctx, types.EventTypeOutputSubmitted, types.AttributeKeyEntityID, "acme")
	requireEvent(t, ctx, types.EventTypeOutputSubmitted, types.AttributeKeyValidator, val(1).String())
	requireEvent(t, ctx, types.EventTypeOutputSubmitted, types.AttributeKeyOutput, out)
}

func TestSubmitRefusals(t *testing.T) {
	tests := []struct {
		name   string
		height int64
		sender sdk.AccAddress
		round  uint64
		proof  func(rt *roundTwo) string
		want   error
		// pre runs a submission first (the duplicate case).
		pre bool
	}{
		{name: "wrong round", height: 31, sender: acc(1), round: 3,
			proof: func(rt *roundTwo) string { return rt.current(rt.k1) }, want: types.ErrWrongRound},
		{name: "late: the rotation height", height: 40, sender: acc(1), round: 2,
			proof: func(rt *roundTwo) string { return rt.current(rt.k1) }, want: types.ErrWindowClosed},
		{name: "not the designated submitter", height: 31, sender: acc(3), round: 2,
			proof: func(rt *roundTwo) string { return rt.current(rt.k1) }, want: types.ErrNotDesignated},
		{name: "second submission of the entity", height: 32, sender: acc(1), round: 2, pre: true,
			proof: func(rt *roundTwo) string { return rt.current(rt.k1) }, want: types.ErrAlreadySubmitted},
		{name: "a proof for another round's input", height: 31, sender: acc(1), round: 2,
			proof: func(rt *roundTwo) string {
				return proofHex(rt.k1, 1, types.DefaultInitialInput())
			}, want: types.ErrInvalidProof},
		{name: "a proof under another validator's key", height: 31, sender: acc(1), round: 2,
			proof: func(rt *roundTwo) string { return rt.current(rt.k2) }, want: types.ErrInvalidProof},
		{name: "158 hex characters", height: 31, sender: acc(1), round: 2,
			proof: func(rt *roundTwo) string { return rt.current(rt.k1)[:158] }, want: types.ErrInvalidProof},
		{name: "uppercase hex", height: 31, sender: acc(1), round: 2,
			proof: func(rt *roundTwo) string { return strings.ToUpper(rt.current(rt.k1)) }, want: types.ErrInvalidProof},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			rt := newRoundTwo(t)
			before := 0
			if tt.pre {
				_, err := rt.k.SubmitVrf(rt.at(31), acc(1), 2, rt.current(rt.k1))
				require.NoError(t, err)
				before = 1
			}
			ctx := rt.at(tt.height).WithEventManager(sdk.NewEventManager())

			out, err := rt.k.SubmitVrf(ctx, tt.sender, tt.round, tt.proof(rt))

			require.ErrorIs(t, err, tt.want)
			require.Empty(t, out)
			require.Equal(t, before, rt.submissionCount(), "nothing written")
			for _, ev := range ctx.EventManager().Events() {
				require.NotEqual(t, types.EventTypeOutputSubmitted, ev.Type)
			}
		})
	}
}

// TestRefusalOrder pins spec §8: the first failing check wins, and the
// verification charge is only reached after the cheap checks.
func TestRefusalOrder(t *testing.T) {
	bad := strings.Repeat("0", 160)
	t.Run("round, window, sender and proof all wrong", func(t *testing.T) {
		rt := newRoundTwo(t)
		meter := newRecordingMeter()
		_, err := rt.k.SubmitVrf(rt.at(40).WithGasMeter(meter), acc(3), 3, bad)
		require.ErrorIs(t, err, types.ErrWrongRound)
		require.False(t, meter.chargedVerification())
		require.Less(t, meter.GasConsumed(), vrf.VerifyGas)
	})
	t.Run("window, sender and proof wrong", func(t *testing.T) {
		rt := newRoundTwo(t)
		meter := newRecordingMeter()
		_, err := rt.k.SubmitVrf(rt.at(40).WithGasMeter(meter), acc(3), 2, bad)
		require.ErrorIs(t, err, types.ErrWindowClosed)
		require.False(t, meter.chargedVerification())
		require.Less(t, meter.GasConsumed(), vrf.VerifyGas)
	})
	t.Run("sender and proof wrong", func(t *testing.T) {
		rt := newRoundTwo(t)
		meter := newRecordingMeter()
		_, err := rt.k.SubmitVrf(rt.at(31).WithGasMeter(meter), acc(3), 2, bad)
		require.ErrorIs(t, err, types.ErrNotDesignated)
		require.False(t, meter.chargedVerification())
	})
	t.Run("already submitted and proof wrong", func(t *testing.T) {
		rt := newRoundTwo(t)
		_, err := rt.k.SubmitVrf(rt.at(31), acc(1), 2, rt.current(rt.k1))
		require.NoError(t, err)
		meter := newRecordingMeter()
		_, err = rt.k.SubmitVrf(rt.at(32).WithGasMeter(meter), acc(1), 2, bad)
		require.ErrorIs(t, err, types.ErrAlreadySubmitted)
		require.False(t, meter.chargedVerification())
	})
	t.Run("an invalid proof still pays for the verification", func(t *testing.T) {
		rt := newRoundTwo(t)
		meter := newRecordingMeter()
		_, err := rt.k.SubmitVrf(rt.at(31).WithGasMeter(meter), acc(1), 2, bad)
		require.ErrorIs(t, err, types.ErrInvalidProof)
		require.True(t, meter.chargedVerification())
		require.GreaterOrEqual(t, meter.GasConsumed(), vrf.VerifyGas)
	})
	t.Run("a malformed proof is refused before the charge", func(t *testing.T) {
		rt := newRoundTwo(t)
		meter := newRecordingMeter()
		_, err := rt.k.SubmitVrf(rt.at(31).WithGasMeter(meter), acc(1), 2, bad[:158])
		require.ErrorIs(t, err, types.ErrInvalidProof)
		require.False(t, meter.chargedVerification())
	})
}

// TestTheWindowsLowerEdge (Review Focus 1): in block O-1 the snapshot does not
// exist yet, so a submission is refused; it is taken by that block's EndBlock
// and a submission in block O succeeds.
func TestTheWindowsLowerEdge(t *testing.T) {
	f := newFixture(t)
	f.entity("acme", val(1))
	f.active(val(1))
	k1 := f.key(val(1), 1, 0)
	f.run(1, 28)
	r := f.round()
	require.EqualValues(t, 2, r.Number)
	require.Equal(t, types.RoundStatus_ROUND_STATUS_SCHEDULED, r.Status)
	proof := proofHex(k1, 2, r.Input)

	_, err := f.k.SubmitVrf(f.at(29), acc(1), 2, proof)
	require.ErrorIs(t, err, types.ErrWindowClosed)

	f.run(29, 29)
	require.Equal(t, types.RoundStatus_ROUND_STATUS_OPEN, f.round().Status)

	// The snapshot now exists, but height 29 is still below O: the bound is
	// the height, not only the status.
	_, err = f.k.SubmitVrf(f.at(29), acc(1), 2, proof)
	require.ErrorIs(t, err, types.ErrWindowClosed)

	_, err = f.k.SubmitVrf(f.at(30), acc(1), 2, proof)
	require.NoError(t, err)
}

// TestAKeyRotatedInTheWindowDoesNotCount (Review Focus 2).
func TestAKeyRotatedInTheWindowDoesNotCount(t *testing.T) {
	rt := newRoundTwo(t)
	k9 := rt.key(val(1), 9, 32) // rebound in the window, at height 32

	_, err := rt.k.SubmitVrf(rt.at(33), acc(1), 2, rt.current(k9))
	require.ErrorIs(t, err, types.ErrInvalidProof, "the snapshot key is the old one")

	_, err = rt.k.SubmitVrf(rt.at(33), acc(1), 2, rt.current(rt.k1))
	require.NoError(t, err)

	// Round 3's cutoff is round 2's window open (30): a key bound at 32 is out.
	rt.run(30, 49)
	require.EqualValues(t, 3, rt.round().Number)
	require.Equal(t, types.RoundStatus_ROUND_STATUS_OPEN, rt.round().Status)
	acme, err := rt.k.Pool.Get(rt.ctx, "acme")
	require.NoError(t, err)
	require.Empty(t, acme.Validator, "round 3 designates nobody for acme")
	beta, err := rt.k.Pool.Get(rt.ctx, "beta")
	require.NoError(t, err)
	require.Equal(t, val(2).String(), beta.Validator)

	// Round 4's cutoff is round 3's window open (50): the key is eligible.
	rt.run(50, 69)
	require.EqualValues(t, 4, rt.round().Number)
	require.Equal(t, types.RoundStatus_ROUND_STATUS_OPEN, rt.round().Status)
	acme, err = rt.k.Pool.Get(rt.ctx, "acme")
	require.NoError(t, err)
	require.Equal(t, val(1).String(), acme.Validator)
	require.Equal(t, hex.EncodeToString(k9.PublicKey()), acme.VrfPublicKey)
}

func TestARevokedEntityStillSubmits(t *testing.T) {
	rt := newRoundTwo(t)
	rt.ents.tickets = rt.ents.tickets[1:] // acme is revoked after the snapshot
	require.Equal(t, "beta", rt.ents.tickets[0].EntityId)

	_, err := rt.k.SubmitVrf(rt.at(31), acc(1), 2, rt.current(rt.k1))
	require.NoError(t, err)
}
