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

// warp_updated is emitted once per scored validator on each epoch boundary,
// after participation_score_computed, carrying one step of the WARP rule
// (ADR-0009 §2). Its validator and epoch_id attributes reuse the keys above.
const (
	EventTypeWarpUpdated = "warp_updated"

	// AttributeKeyPS is the Participation Score the step read, on 0-100.
	AttributeKeyPS         = "ps"
	AttributeKeyWarpBefore = "warp_before"
	AttributeKeyWarpAfter  = "warp_after"
	AttributeKeyEligible   = "eligible"
)
