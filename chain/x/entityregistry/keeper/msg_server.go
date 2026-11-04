package keeper

import (
	"context"
	"fmt"

	errorsmod "cosmossdk.io/errors"
	sdk "github.com/cosmos/cosmos-sdk/types"
	sdkerrors "github.com/cosmos/cosmos-sdk/types/errors"

	"github.com/saicon-network/saicon/chain/x/entityregistry/types"
)

type msgServer struct {
	Keeper
}

// NewMsgServerImpl returns an implementation of the MsgServer interface
// for the provided Keeper.
func NewMsgServerImpl(keeper Keeper) types.MsgServer {
	return &msgServer{Keeper: keeper}
}

var _ types.MsgServer = msgServer{}

// AttestEntity records or replaces an entity's attestation (spec §6.1).
func (k msgServer) AttestEntity(ctx context.Context, msg *types.MsgAttestEntity) (*types.MsgAttestEntityResponse, error) {
	attestor, err := k.addressCodec.StringToBytes(msg.Attestor)
	if err != nil {
		return nil, errorsmod.Wrap(sdkerrors.ErrInvalidAddress, fmt.Sprintf("invalid attestor address: %s", err))
	}
	validators := make([]sdk.ValAddress, len(msg.Validators))
	for i, s := range msg.Validators {
		v, err := sdk.ValAddressFromBech32(s)
		if err != nil {
			return nil, errorsmod.Wrapf(types.ErrInvalidEntity, "validators[%d] %q: %s", i, s, err)
		}
		validators[i] = v
	}
	bindings := make([]types.VrfKeyBinding, len(msg.VrfKeys))
	for i, b := range msg.VrfKeys {
		v, err := sdk.ValAddressFromBech32(b.Validator)
		if err != nil {
			return nil, errorsmod.Wrapf(types.ErrInvalidEntity, "vrf_keys[%d] %q: %s", i, b.Validator, err)
		}
		pk, err := types.DecodeVrfPublicKey(b.VrfPublicKey)
		if err != nil {
			return nil, errorsmod.Wrapf(err, "vrf_keys[%d]", i)
		}
		bindings[i] = types.VrfKeyBinding{Validator: v, PublicKey: pk}
	}
	replaced, err := k.Attest(ctx, attestor, types.Attestation{
		EntityID:        msg.EntityId,
		DisplayName:     msg.DisplayName,
		AttestationHash: msg.AttestationHash,
		Validators:      validators,
		VrfKeys:         bindings,
	})
	if err != nil {
		return nil, err
	}
	return &types.MsgAttestEntityResponse{Replaced: replaced}, nil
}

// RevokeEntity withdraws an entity's attestation (spec §6.2). The response
// lists the validators whose active seat was frozen.
func (k msgServer) RevokeEntity(ctx context.Context, msg *types.MsgRevokeEntity) (*types.MsgRevokeEntityResponse, error) {
	authority, err := k.addressCodec.StringToBytes(msg.Authority)
	if err != nil {
		return nil, errorsmod.Wrap(sdkerrors.ErrInvalidAddress, fmt.Sprintf("invalid authority address: %s", err))
	}
	frozen, err := k.Revoke(ctx, authority, msg.EntityId, msg.Reason)
	if err != nil {
		return nil, err
	}
	res := &types.MsgRevokeEntityResponse{}
	for _, v := range frozen {
		res.Frozen = append(res.Frozen, v.String())
	}
	return res, nil
}

// OfferVrfKey records a VRF key the validator's operator offers (spec §6.1).
func (k msgServer) OfferVrfKey(ctx context.Context, msg *types.MsgOfferVrfKey) (*types.MsgOfferVrfKeyResponse, error) {
	operator, err := k.addressCodec.StringToBytes(msg.Operator)
	if err != nil {
		return nil, errorsmod.Wrap(sdkerrors.ErrInvalidAddress, fmt.Sprintf("invalid operator address: %s", err))
	}
	if err := k.Keeper.OfferVrfKey(ctx, operator, msg.VrfPublicKey, msg.ProofOfPossession); err != nil {
		return nil, err
	}
	return &types.MsgOfferVrfKeyResponse{}, nil
}
