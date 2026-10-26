package types

// Event types and attribute keys emitted by the rewards module.
const (
	// EventTypeRewardMinted is emitted by DistributeEpochRewards when an
	// eligible validator is minted its epoch reward.
	EventTypeRewardMinted = "reward_minted"
	// EventTypeRewardDenied is emitted when a validator fails the gate
	// (status/compliance/EP); the reason attribute carries which one.
	EventTypeRewardDenied = "reward_denied"

	AttributeKeyValidator = "validator"
	AttributeKeyEpochID   = "epoch_id"
	AttributeKeyAmount    = "amount"
	AttributeKeyReason    = "reason"
)
