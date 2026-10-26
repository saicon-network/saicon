package types

const (
	EventTypeStakeEnqueued             = "stake_enqueued"
	EventTypeQueuedDelegationFilled    = "queued_delegation_filled"
	EventTypeQueuedDelegationCancelled = "queued_delegation_cancelled"
	EventTypeStakeCapsComputed         = "stake_caps_computed"

	AttributeKeyID                = "id"
	AttributeKeyDelegator         = "delegator"
	AttributeKeyValidator         = "validator"
	AttributeKeyAmount            = "amount"
	AttributeKeyRemaining         = "remaining"
	AttributeKeyEpoch             = "epoch"
	AttributeKeyGlobalCap         = "global_cap"
	AttributeKeyValidatorCap      = "validator_cap"
	AttributeKeyPublicCirculating = "public_circulating"
)
