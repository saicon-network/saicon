package types

// Event types and attribute keys emitted by the compliance module.
const (
	// EventTypeComplianceFlagged is emitted by SetCompliance when C flips to 0
	// (covers MsgSubmitEvidence AND the double-sign auto-jail hook).
	EventTypeComplianceFlagged = "compliance_flagged"
	// EventTypeComplianceCleared is emitted by SetCompliance when C flips to 1.
	EventTypeComplianceCleared = "compliance_cleared"
	// EventTypeValidatorUnjailed is emitted by MsgUnjail on a successful unjail.
	EventTypeValidatorUnjailed = "validator_unjailed"

	AttributeKeyValidator = "validator"
	AttributeKeyEvent     = "event"
	AttributeKeyReason    = "reason"
	AttributeKeyHeight    = "height"
)
