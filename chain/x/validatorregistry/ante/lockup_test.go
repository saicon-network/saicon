package ante_test

import (
	"context"
	"testing"
	"time"

	sdk "github.com/cosmos/cosmos-sdk/types"
	"github.com/cosmos/cosmos-sdk/x/authz"
	banktypes "github.com/cosmos/cosmos-sdk/x/bank/types"
	stakingtypes "github.com/cosmos/cosmos-sdk/x/staking/types"
	"github.com/stretchr/testify/require"
	protov2 "google.golang.org/protobuf/proto"

	vrante "github.com/saicon-network/saicon/chain/x/validatorregistry/ante"
	vrtypes "github.com/saicon-network/saicon/chain/x/validatorregistry/types"
)

// nestExec wraps inner in `depth` layers of authz.MsgExec (grantee == self is
// irrelevant to the decorator; it recurses regardless). Returns the outermost
// *authz.MsgExec.
func nestExec(grantee sdk.AccAddress, inner sdk.Msg, depth int) sdk.Msg {
	m := inner
	for i := 0; i < depth; i++ {
		e := authz.NewMsgExec(grantee, []sdk.Msg{m})
		m = &e
	}
	return m
}

// stub LockupKeeper: locks exactly the configured valoper string.
type stubKeeper struct{ lockedVal string }

func (s stubKeeper) SelfStakeLockup(_ context.Context, valAddr sdk.ValAddress) (bool, time.Time, error) {
	if valAddr.String() == s.lockedVal {
		return true, time.Unix(1<<40, 0), nil
	}
	return false, time.Time{}, nil
}

func noopNext(ctx sdk.Context, _ sdk.Tx, _ bool) (sdk.Context, error) { return ctx, nil }

// minimal sdk.Tx wrapper around a message slice.
type msgsTx struct{ msgs []sdk.Msg }

func (t msgsTx) GetMsgs() []sdk.Msg                    { return t.msgs }
func (t msgsTx) GetMsgsV2() ([]protov2.Message, error) { return nil, nil }

func TestLockupDecorator(t *testing.T) {
	valA := sdk.ValAddress([]byte("validatorAAAAAAAAAAA")) // 20 bytes
	valB := sdk.ValAddress([]byte("validatorBBBBBBBBBBB"))
	selfAccA := sdk.AccAddress(valA.Bytes()) // self-delegator of valA
	other := sdk.AccAddress([]byte("someotherdelegator00"))

	dec := vrante.NewLockupDecorator(stubKeeper{lockedVal: valA.String()})
	run := func(msgs ...sdk.Msg) error {
		_, err := dec.AnteHandle(sdk.Context{}.WithBlockTime(time.Now()), msgsTx{msgs}, false, noopNext)
		return err
	}

	// self-undelegate of a LOCKED validator -> rejected with ErrLockupActive
	require.ErrorIs(t, run(&stakingtypes.MsgUndelegate{DelegatorAddress: selfAccA.String(), ValidatorAddress: valA.String()}), vrtypes.ErrLockupActive)
	// self-undelegate of an UNLOCKED validator (valB) -> ok
	require.NoError(t, run(&stakingtypes.MsgUndelegate{DelegatorAddress: sdk.AccAddress(valB.Bytes()).String(), ValidatorAddress: valB.String()}))
	// non-self (third-party delegator) undelegate from locked valA -> ok
	require.NoError(t, run(&stakingtypes.MsgUndelegate{DelegatorAddress: other.String(), ValidatorAddress: valA.String()}))
	// self-redelegate away from locked valA -> rejected with ErrLockupActive
	require.ErrorIs(t, run(&stakingtypes.MsgBeginRedelegate{DelegatorAddress: selfAccA.String(), ValidatorSrcAddress: valA.String(), ValidatorDstAddress: valB.String()}), vrtypes.ErrLockupActive)
	// non-staking msg -> ok
	require.NoError(t, run(&banktypes.MsgSend{}))
	// multi-msg tx: a benign MsgSend followed by a locked self-undelegate -> whole
	// tx rejected (the decorator must scan every msg, not just msgs[0]).
	require.ErrorIs(t, run(
		&banktypes.MsgSend{},
		&stakingtypes.MsgUndelegate{DelegatorAddress: selfAccA.String(), ValidatorAddress: valA.String()},
	), vrtypes.ErrLockupActive)
	// authz-wrapped self-undelegate of locked valA -> rejected (recursive unwrap)
	inner := &stakingtypes.MsgUndelegate{DelegatorAddress: selfAccA.String(), ValidatorAddress: valA.String()}
	exec := authz.NewMsgExec(other, []sdk.Msg{inner})
	require.ErrorIs(t, run(&exec), vrtypes.ErrLockupActive)

	// --- authz depth: fail-CLOSED regression (V-3 T5 review finding #1) ---
	// A self-undelegate of locked valA wrapped exactly at the cap (maxAuthzDepth
	// layers) is still fully unwrapped and caught by the real self-check.
	require.ErrorIs(t, run(nestExec(other, inner, 5)), vrtypes.ErrLockupActive)
	// Nested one layer BEYOND the cap must NOT slip through: the decorator can no
	// longer see the inner self-undelegate, so it must fail closed (reject),
	// not fail open (allow). This is the bypass the review proved.
	require.ErrorIs(t, run(nestExec(other, inner, 6)), vrtypes.ErrLockupActive)
	require.ErrorIs(t, run(nestExec(other, inner, 9)), vrtypes.ErrLockupActive)
	// A NON-self (third-party) undelegate wrapped beyond the cap is also rejected
	// (fail-closed is content-blind past the cap) — acceptable: no legitimate tx
	// nests authz this deep. Documents the intended over-rejection.
	deepOther := &stakingtypes.MsgUndelegate{DelegatorAddress: other.String(), ValidatorAddress: valA.String()}
	require.ErrorIs(t, run(nestExec(other, deepOther, 6)), vrtypes.ErrLockupActive)
}
