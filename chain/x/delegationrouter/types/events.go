package types

const (
	EventTypeDelegationRouted = "delegation_routed"

	AttributeKeyDelegator = "delegator"
	AttributeKeyPrimary   = "primary"
	AttributeKeyAmount    = "amount"
	// AttributeKeyLegs is "valoper:amount,…".
	AttributeKeyLegs = "legs"
	// AttributeKeyQueued is "valoper:amount:id,…".
	AttributeKeyQueued = "queued"
)
