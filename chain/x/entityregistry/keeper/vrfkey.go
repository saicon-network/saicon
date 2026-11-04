package keeper

import (
	"bytes"
	"context"
	"errors"

	"cosmossdk.io/collections"
	errorsmod "cosmossdk.io/errors"
	sdk "github.com/cosmos/cosmos-sdk/types"

	"github.com/saicon-network/saicon/chain/crypto/vrf"
	"github.com/saicon-network/saicon/chain/x/entityregistry/types"
)

// OfferVrfKey records a VRF key the operator of a validator offers (spec §6.1).
// The validator is the operator account's bytes. The proof of possession is
// verified on-chain and charged vrf.VerifyGas; every check comes before the
// first write. An offer replaces the validator's earlier one, and the key's
// owner entry is never pruned, so a key belongs to one validator for the
// chain's lifetime. An offer changes nothing about rounds; only a binding does.
func (k Keeper) OfferVrfKey(ctx context.Context, operator sdk.AccAddress, pkHex, popHex string) error {
	pk, err := types.DecodeVrfPublicKey(pkHex)
	if err != nil {
		return err
	}
	pop, err := types.DecodeProofOfPossession(popHex)
	if err != nil {
		return err
	}
	validator := sdk.ValAddress(operator)
	sdkCtx := sdk.UnwrapSDKContext(ctx)
	sdkCtx.GasMeter().ConsumeGas(vrf.VerifyGas, "ecvrf verify")
	if _, err := vrf.Verify(pk, pop, types.PopAlpha(sdkCtx.ChainID(), validator, pk)); err != nil {
		return errorsmod.Wrap(types.ErrInvalidProofOfPossession, err.Error())
	}
	owner, err := k.VrfKeyOwners.Get(ctx, pk)
	switch {
	case err == nil:
		if !bytes.Equal(owner, validator) {
			return errorsmod.Wrapf(types.ErrVrfKeyInUse, "key %s belongs to %s", pkHex, sdk.ValAddress(owner))
		}
	case !errors.Is(err, collections.ErrNotFound):
		return err
	}
	if err := k.VrfKeyOffers.Set(ctx, validator, types.VrfKeyOffer{
		Validator: validator.String(), VrfPublicKey: pkHex, Height: sdkCtx.BlockHeight(),
	}); err != nil {
		return err
	}
	if err := k.VrfKeyOwners.Set(ctx, pk, validator); err != nil {
		return err
	}
	sdkCtx.EventManager().EmitEvent(sdk.NewEvent(
		types.EventTypeVrfKeyOffered,
		sdk.NewAttribute(types.AttributeKeyValidator, validator.String()),
		sdk.NewAttribute(types.AttributeKeyVrfPublicKey, pkHex),
	))
	return nil
}

// checkOffered refuses a binding whose key is not the validator's standing
// offer (spec §6.2). Keys are compared as canonical bytes.
func (k Keeper) checkOffered(ctx context.Context, b types.VrfKeyBinding) error {
	offer, err := k.VrfKeyOffers.Get(ctx, b.Validator)
	if err != nil {
		if errors.Is(err, collections.ErrNotFound) {
			return errorsmod.Wrapf(types.ErrVrfKeyNotOffered,
				"operator of %s has not offered a key; send MsgOfferVrfKey first", b.Validator)
		}
		return err
	}
	offered, err := types.DecodeVrfPublicKey(offer.VrfPublicKey)
	if err != nil {
		return err
	}
	if !bytes.Equal(offered, b.PublicKey) {
		return errorsmod.Wrapf(types.ErrVrfKeyNotOffered,
			"operator of %s has not offered this key; send MsgOfferVrfKey first", b.Validator)
	}
	return nil
}
