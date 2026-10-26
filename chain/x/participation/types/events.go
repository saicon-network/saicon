package types

// Event types and attribute keys emitted by the participation module.
const (
	// EventTypeScoreComputed is emitted once per bonded validator on each epoch
	// boundary by Keeper.ComputeEpoch, after the snapshot and EP are written.
	EventTypeScoreComputed = "participation_score_computed"

	AttributeKeyValidator    = "validator"
	AttributeKeyEpochID      = "epoch_id"
	AttributeKeyBlocksSigned = "blocks_signed"
	AttributeKeyBlocksMissed = "blocks_missed"
	AttributeKeyUptime       = "uptime"
	AttributeKeyScore        = "score"
	AttributeKeyEPAfter      = "ep_after"
)
