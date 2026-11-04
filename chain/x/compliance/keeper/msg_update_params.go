package keeper

import (
	"bytes"
	"context"

	errorsmod "cosmossdk.io/errors"

	"github.com/saicon-network/saicon/chain/x/compliance/types"
)

func (k msgServer) UpdateParams(ctx context.Context, req *types.MsgUpdateParams) (*types.MsgUpdateParamsResponse, error) {
	authority, err := k.addressCodec.StringToBytes(req.Authority)
	if err != nil {
		return nil, errorsmod.Wrap(err, "invalid authority address")
	}

	if !bytes.Equal(k.GetAuthority(), authority) {
		expectedAuthorityStr, _ := k.addressCodec.BytesToString(k.GetAuthority())
		return nil, errorsmod.Wrapf(types.ErrInvalidSigner, "invalid authority; expected %s, got %s", expectedAuthorityStr, req.Authority)
	}

	if err := req.Params.Validate(); err != nil {
		return nil, err
	}

	// C20: the fraction may not sit below the double-sign fraction in force. A
	// stateful check, so not in Params.Validate().
	d, err := k.slashingKeeper.SlashFractionDoubleSign(ctx)
	if err != nil {
		return nil, errorsmod.Wrap(err, "reading the double-sign fraction")
	}
	if req.Params.CoordinatedAttackFraction.LT(d) {
		return nil, types.ErrInvalidFraction.Wrapf("%s is below the double-sign fraction in force, %s",
			req.Params.CoordinatedAttackFraction, d)
	}

	if err := k.Params.Set(ctx, req.Params); err != nil {
		return nil, err
	}

	return &types.MsgUpdateParamsResponse{}, nil
}
