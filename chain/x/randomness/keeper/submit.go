package keeper

import (
	"context"
	"encoding/hex"
	"errors"
	"strconv"

	"cosmossdk.io/collections"
	errorsmod "cosmossdk.io/errors"
	sdk "github.com/cosmos/cosmos-sdk/types"

	"github.com/saicon-network/saicon/chain/crypto/vrf"
	"github.com/saicon-network/saicon/chain/x/randomness/types"
)

// SubmitVrf accepts the VRF proof of the validator designated for its entity in
// the current round (spec §8) and returns the verified output as hex. The
// checks run in the spec's order and every one precedes any write, so the first
// failing check names the refusal:
//
//  1. round is the current round's number;
//  2. the round is OPEN and window_open_height <= height < rotation_height;
//  3. the operator is the designated submitter of a pool entity;
//  4. that entity has not submitted;
//  5. the proof is 160 lowercase hex characters, then verification is charged
//     (vrf.VerifyGas, flat, before the work so an invalid proof still pays) and
//     the proof must verify against the key copied into the snapshot, over
//     RoundAlpha(chain id, round, input).
//
// The snapshot is the only authority: a key rebound or an entity revoked after
// the window opened changes nothing for this round.
func (k Keeper) SubmitVrf(ctx context.Context, operator sdk.AccAddress, round uint64, proofHex string) (string, error) {
	sdkCtx := sdk.UnwrapSDKContext(ctx)
	height := sdkCtx.BlockHeight()

	r, err := k.CurrentRound.Get(ctx)
	if err != nil {
		return "", err
	}
	if round != r.Number {
		return "", errorsmod.Wrapf(types.ErrWrongRound, "round %d is not the current round %d", round, r.Number)
	}
	switch {
	case r.RotationHeight == 0:
		return "", errorsmod.Wrapf(types.ErrWindowClosed, "round %d is not scheduled yet", r.Number)
	case height >= r.RotationHeight:
		return "", errorsmod.Wrapf(types.ErrWindowClosed, "late: the window closed at height %d", r.RotationHeight-1)
	case r.Status != types.RoundStatus_ROUND_STATUS_OPEN || height < r.WindowOpenHeight:
		return "", errorsmod.Wrapf(types.ErrWindowClosed, "the window opens at height %d", r.WindowOpenHeight)
	}

	entity, err := k.Designated.Get(ctx, sdk.ValAddress(operator))
	if err != nil {
		if errors.Is(err, collections.ErrNotFound) {
			return "", errorsmod.Wrapf(types.ErrNotDesignated, "%s", sdk.ValAddress(operator))
		}
		return "", err
	}
	submitted, err := k.Submissions.Has(ctx, entity)
	if err != nil {
		return "", err
	}
	if submitted {
		return "", errorsmod.Wrapf(types.ErrAlreadySubmitted, "entity %s", entity)
	}

	proof, err := types.DecodeHex(proofHex, vrf.ProofSize)
	if err != nil {
		return "", errorsmod.Wrap(types.ErrInvalidProof, err.Error())
	}
	entry, err := k.Pool.Get(ctx, entity)
	if err != nil {
		return "", err
	}
	key, err := types.DecodeHex(entry.VrfPublicKey, vrf.PublicKeySize)
	if err != nil {
		return "", err
	}
	input, err := types.DecodeHex(r.Input, types.InputHexLen/2)
	if err != nil {
		return "", err
	}

	sdkCtx.GasMeter().ConsumeGas(vrf.VerifyGas, "ecvrf verify")
	output, err := vrf.Verify(key, proof, types.RoundAlpha(sdkCtx.ChainID(), r.Number, input))
	if err != nil {
		return "", errorsmod.Wrap(types.ErrInvalidProof, err.Error())
	}
	outputHex := hex.EncodeToString(output)

	if err := k.Submissions.Set(ctx, entity, types.Submission{
		EntityId:  entity,
		Validator: sdk.ValAddress(operator).String(),
		Output:    outputHex,
		Proof:     proofHex,
		Height:    height,
	}); err != nil {
		return "", err
	}
	sdkCtx.EventManager().EmitEvent(sdk.NewEvent(
		types.EventTypeOutputSubmitted,
		sdk.NewAttribute(types.AttributeKeyRound, strconv.FormatUint(r.Number, 10)),
		sdk.NewAttribute(types.AttributeKeyEntityID, entity),
		sdk.NewAttribute(types.AttributeKeyValidator, sdk.ValAddress(operator).String()),
		sdk.NewAttribute(types.AttributeKeyOutput, outputHex),
	))
	return outputHex, nil
}
