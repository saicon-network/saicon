package app

import (
	errorsmod "cosmossdk.io/errors"
	sdk "github.com/cosmos/cosmos-sdk/types"
	sdkerrors "github.com/cosmos/cosmos-sdk/types/errors"
	"github.com/cosmos/cosmos-sdk/x/auth/ante"

	seante "github.com/saicon-network/saicon/chain/x/stakeengine/ante"
	vrante "github.com/saicon-network/saicon/chain/x/validatorregistry/ante"
)

// HandlerOptions extends the SDK ante options with the keepers the two SAiCoN
// decorators read: the V-3 self-stake lock-up and the S6 stake caps.
type HandlerOptions struct {
	ante.HandlerOptions
	LockupKeeper vrante.LockupKeeper
	CapsKeeper   seante.CapsKeeper
}

// NewAnteHandler runs the standard SDK ante chain FIRST, then the V-3
// LockupDecorator, then the S6 CapsDecorator. Running last (not first) means
// these checks only touch authenticated, fee-paying txs and their state reads
// are gas-metered (SetUpContext has installed the real gas meter by then) —
// they must not do unmetered KV reads on unauthenticated input on a money
// chain. Enforcement is identical either way: the whole ante chain runs before
// the staking msg server. We wrap the SDK default handler (built via
// ante.NewAnteHandler) rather than re-listing the decorator chain, so fee/sig/
// sequence handling — including feegrant — is inherited unchanged.
func NewAnteHandler(options HandlerOptions) (sdk.AnteHandler, error) {
	if options.AccountKeeper == nil {
		return nil, errorsmod.Wrap(sdkerrors.ErrLogic, "account keeper is required for ante builder")
	}
	if options.BankKeeper == nil {
		return nil, errorsmod.Wrap(sdkerrors.ErrLogic, "bank keeper is required for ante builder")
	}
	if options.LockupKeeper == nil {
		return nil, errorsmod.Wrap(sdkerrors.ErrLogic, "lockup keeper is required for ante builder")
	}
	if options.CapsKeeper == nil {
		return nil, errorsmod.Wrap(sdkerrors.ErrLogic, "stake caps keeper is required for ante builder")
	}

	stdAnte, err := ante.NewAnteHandler(options.HandlerOptions)
	if err != nil {
		return nil, err
	}
	lockup := vrante.NewLockupDecorator(options.LockupKeeper)
	caps := seante.NewCapsDecorator(options.CapsKeeper)
	noopNext := func(ctx sdk.Context, _ sdk.Tx, _ bool) (sdk.Context, error) { return ctx, nil }
	return func(ctx sdk.Context, tx sdk.Tx, simulate bool) (sdk.Context, error) {
		ctx, err := stdAnte(ctx, tx, simulate)
		if err != nil {
			return ctx, err
		}
		if ctx, err = lockup.AnteHandle(ctx, tx, simulate, noopNext); err != nil {
			return ctx, err
		}
		return caps.AnteHandle(ctx, tx, simulate, noopNext)
	}, nil
}
