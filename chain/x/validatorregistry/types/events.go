package types

// Event types and attribute keys emitted by the validatorregistry module.
const (
	// EventTypeStatusChanged is emitted by Keeper.ChangeStatus on a successful
	// validator status transition.
	EventTypeStatusChanged = "validator_status_changed"
	// EventTypeRegistryReset is emitted when a stale registry record is discarded
	// because x/staking created a validator at an address the registry still had
	// metadata for (re-onboarding is a fresh registration). It preserves the audit
	// trail of the discarded record, which the deletion itself would otherwise lose.
	EventTypeRegistryReset = "validator_registry_reset"

	AttributeKeyValidator = "validator"
	// AttributeKeyPreviousStatus carries the status of the discarded record.
	AttributeKeyPreviousStatus = "previous_status"
	AttributeKeyFromStatus     = "from_status"
	AttributeKeyToStatus       = "to_status"
	AttributeKeyHeight         = "height"
)
