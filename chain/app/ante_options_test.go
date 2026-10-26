package app

import (
	"context"
	"testing"
	"time"

	sdk "github.com/cosmos/cosmos-sdk/types"
	"github.com/cosmos/cosmos-sdk/x/auth/ante"
	authtypes "github.com/cosmos/cosmos-sdk/x/auth/types"
	"github.com/stretchr/testify/require"
)

type stubAccountKeeper struct{ ante.AccountKeeper }
type stubBankKeeper struct{ authtypes.BankKeeper }
type stubLockupKeeper struct{}

func (stubLockupKeeper) SelfStakeLockup(context.Context, sdk.ValAddress) (bool, time.Time, error) {
	return false, time.Time{}, nil
}

// A chain whose ante handler silently lacks the stake caps would accept any
// staking message; building it must fail instead.
func TestNewAnteHandlerRefusesAMissingCapsKeeper(t *testing.T) {
	_, err := NewAnteHandler(HandlerOptions{
		HandlerOptions: ante.HandlerOptions{AccountKeeper: stubAccountKeeper{}, BankKeeper: stubBankKeeper{}},
		LockupKeeper:   stubLockupKeeper{},
	})
	require.ErrorContains(t, err, "stake caps keeper is required")
}
