package keeper

import (
	"bytes"
	"context"

	errorsmod "cosmossdk.io/errors"
	sdk "github.com/cosmos/cosmos-sdk/types"

	"github.com/saicon-network/saicon/chain/x/compliance/types"
	vrtypes "github.com/saicon-network/saicon/chain/x/validatorregistry/types"
)

// SubmitEvidence records a non-compliance event (C=0) and triggers the
// quarantine/jail status transition. Authority-gated.
func (k msgServer) SubmitEvidence(ctx context.Context, req *types.MsgSubmitEvidence) (*types.MsgSubmitEvidenceResponse, error) {
	if err := k.assertAuthority(req.Authority); err != nil {
		return nil, err
	}
	if req.Event == types.ComplianceEventType_COMPLIANCE_EVENT_TYPE_UNSPECIFIED ||
		req.Event == types.ComplianceEventType_COMPLIANCE_EVENT_TYPE_RESTORED {
		return nil, errorsmod.Wrapf(types.ErrInvalidEvent, "SubmitEvidence requires a non-compliance event, got %s", req.Event)
	}

	valAddr, err := parseValAddr(req.ValidatorAddress)
	if err != nil {
		return nil, errorsmod.Wrap(types.ErrInvalidValidator, err.Error())
	}

	if err := k.SetCompliance(ctx, valAddr, false, req.Event, req.Reason); err != nil {
		return nil, err
	}
	return &types.MsgSubmitEvidenceResponse{}, nil
}

// ClearFlag restores compliance (C=1) and moves a quarantined validator back
// to active. Authority-gated. JAILED validators are out of scope — they must go
// through MsgUnjail.
func (k msgServer) ClearFlag(ctx context.Context, req *types.MsgClearFlag) (*types.MsgClearFlagResponse, error) {
	if err := k.assertAuthority(req.Authority); err != nil {
		return nil, err
	}

	valAddr, err := parseValAddr(req.ValidatorAddress)
	if err != nil {
		return nil, errorsmod.Wrap(types.ErrInvalidValidator, err.Error())
	}

	// A JAILED validator leaves jail only via MsgUnjail: that path is what
	// enforces the unjail cooldown and the tombstone gate (double-sign is
	// irreversible). Both messages share the same gov-authority gate, and the
	// V-1 table permits JAILED -> ACTIVE generically, so without this check a
	// proposal could call ClearFlag instead and walk around both gates —
	// reinstating even a tombstoned double-signer. Unjail stays the single
	// audited entry point (it is also what emits validator_unjailed).
	status, found, err := k.validatorregistryKeeper.GetValidatorStatus(ctx, valAddr)
	if err != nil {
		return nil, err
	}
	if found && status == vrtypes.Status_STATUS_JAILED {
		return nil, errorsmod.Wrapf(types.ErrJailedRequiresUnjail, "validator %s", valAddr.String())
	}

	if err := k.SetCompliance(ctx, valAddr, true, types.ComplianceEventType_COMPLIANCE_EVENT_TYPE_RESTORED, req.Reason); err != nil {
		return nil, err
	}
	return &types.MsgClearFlagResponse{}, nil
}

// assertAuthority verifies the signer matches the module authority.
func (k msgServer) assertAuthority(signer string) error {
	addr, err := k.addressCodec.StringToBytes(signer)
	if err != nil {
		return errorsmod.Wrap(err, "invalid authority address")
	}
	if !bytes.Equal(k.GetAuthority(), addr) {
		expected, _ := k.addressCodec.BytesToString(k.GetAuthority())
		return errorsmod.Wrapf(types.ErrInvalidSigner, "invalid authority; expected %s, got %s", expected, signer)
	}
	return nil
}

// parseValAddr accepts a bech32 string (valoper- or account-prefixed) and
// returns the operator address bytes. v0.1 leniency: the registry keys on the
// raw 20 bytes regardless of prefix.
func parseValAddr(s string) (sdk.ValAddress, error) {
	if va, err := sdk.ValAddressFromBech32(s); err == nil {
		return va, nil
	}
	acc, err := sdk.AccAddressFromBech32(s)
	if err != nil {
		return nil, err
	}
	return sdk.ValAddress(acc), nil
}
