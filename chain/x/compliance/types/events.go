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

	// EventTypeOffenceRecorded is emitted when BeginBlock records an equivocation x/evidence accepted.
	EventTypeOffenceRecorded = "coordinated_offence_recorded"
	// EventTypeEntityArmed is emitted when an entity's second distinct validator offence is recorded.
	EventTypeEntityArmed = "coordinated_entity_armed"
	// EventTypeSanctionFired is emitted once per executed coordinated-attack sanction.
	EventTypeSanctionFired = "coordinated_sanction_fired"
	// EventTypeSanctionTarget is emitted once per target of an executed sanction.
	EventTypeSanctionTarget = "coordinated_sanction_target"

	AttributeKeyValidator = "validator"
	AttributeKeyEvent     = "event"
	AttributeKeyReason    = "reason"
	AttributeKeyHeight    = "height"

	AttributeKeyEvidenceHash     = "evidence_hash"
	AttributeKeyKind             = "kind"
	AttributeKeyInfractionHeight = "infraction_height"
	AttributeKeyEntityID         = "entity_id"
	AttributeKeyValidators       = "validators"
	AttributeKeyArmedHeight      = "armed_height"
	AttributeKeySanctionID       = "sanction_id"
	AttributeKeyFraction         = "fraction"
	AttributeKeyEvidenceHashes   = "evidence_hashes"
	AttributeKeyAuthority        = "authority"
	AttributeKeyExtraFraction    = "extra_fraction"
	AttributeKeyValidatorBurned  = "validator_burned"
	AttributeKeyOutcome          = "outcome"
	AttributeKeyClassification   = "classification"
)
