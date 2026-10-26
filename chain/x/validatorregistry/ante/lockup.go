package ante

import (
	"bytes"
	"context"
	"time"

	errorsmod "cosmossdk.io/errors"
	sdk "github.com/cosmos/cosmos-sdk/types"
	"github.com/cosmos/cosmos-sdk/x/authz"
	stakingtypes "github.com/cosmos/cosmos-sdk/x/staking/types"

	"github.com/saicon-network/saicon/chain/x/validatorregistry/types"
)

// LockupKeeper is the minimal validatorregistry surface the decorator needs.
type LockupKeeper interface {
	SelfStakeLockup(ctx context.Context, valAddr sdk.ValAddress) (locked bool, unlockAt time.Time, err error)
}

// LockupDecorator rejects a validator withdrawing its own self-stake
// (MsgUndelegate / MsgBeginRedelegate) before its lock-up window expires.
// It recurses into authz.MsgExec so wrapping cannot bypass the check.
type LockupDecorator struct{ k LockupKeeper }

func NewLockupDecorator(k LockupKeeper) LockupDecorator { return LockupDecorator{k: k} }

func (d LockupDecorator) AnteHandle(ctx sdk.Context, tx sdk.Tx, simulate bool, next sdk.AnteHandler) (sdk.Context, error) {
	if err := d.check(ctx, tx.GetMsgs(), 0); err != nil {
		return ctx, err
	}
	return next(ctx, tx, simulate)
}

const maxAuthzDepth = 5 // bounded recursion for nested authz.MsgExec

func (d LockupDecorator) check(ctx sdk.Context, msgs []sdk.Msg, depth int) error {
	for _, m := range msgs {
		switch msg := m.(type) {
		case *stakingtypes.MsgUndelegate:
			if err := d.reject(ctx, msg.DelegatorAddress, msg.ValidatorAddress); err != nil {
				return err
			}
		case *stakingtypes.MsgBeginRedelegate:
			if err := d.reject(ctx, msg.DelegatorAddress, msg.ValidatorSrcAddress); err != nil {
				return err
			}
		case *authz.MsgExec:
			if depth >= maxAuthzDepth {
				// Fail CLOSED: we refuse to unwrap authz.MsgExec nested beyond the
				// cap, so we cannot prove the inner messages are not a locked
				// self-stake withdrawal. Rejecting is safe — no legitimate tx nests
				// authz this deep; a `continue` here would fail OPEN and let a
				// deeply-wrapped self-undelegate/redelegate bypass the lock-up.
				return errorsmod.Wrapf(types.ErrLockupActive,
					"authz.MsgExec nested beyond depth %d; cannot verify self-stake lock-up", maxAuthzDepth)
			}
			inner, err := msg.GetMessages()
			if err != nil {
				return err
			}
			if err := d.check(ctx, inner, depth+1); err != nil {
				return err
			}
		}
	}
	return nil
}

// reject returns ErrLockupActive when (delegator == validator) is a self-stake
// withdrawal and the validator is still within its lock-up window.
func (d LockupDecorator) reject(ctx sdk.Context, delegator, validator string) error {
	acc, err := sdk.AccAddressFromBech32(delegator)
	if err != nil {
		return nil // malformed address: let the staking msg server reject it
	}
	val, err := sdk.ValAddressFromBech32(validator)
	if err != nil {
		return nil
	}
	if !bytes.Equal(acc.Bytes(), val.Bytes()) {
		return nil // not a self-delegation
	}
	locked, unlockAt, err := d.k.SelfStakeLockup(ctx, val)
	if err != nil {
		return err
	}
	if locked {
		return errorsmod.Wrapf(types.ErrLockupActive,
			"validator %s self-stake locked until %s", val.String(), unlockAt.UTC().Format(time.RFC3339))
	}
	return nil
}
