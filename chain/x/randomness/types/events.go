package types

// Event types emitted by x/randomness (spec §12).
const (
	EventTypeRoundScheduled  = "randomness_round_scheduled"
	EventTypeWindowOpened    = "randomness_window_opened"
	EventTypeOutputSubmitted = "vrf_output_submitted"
	EventTypeSeedComputed    = "randomness_seed_computed"
	EventTypeShadowDraw      = "randomness_shadow_draw"
	EventTypeDrawSkipped     = "randomness_draw_skipped"
)

// Event attribute keys.
const (
	AttributeKeyRound            = "round"
	AttributeKeyKeyCutoffHeight  = "key_cutoff_height"
	AttributeKeyWindowOpenHeight = "window_open_height"
	AttributeKeyRotationHeight   = "rotation_height"
	AttributeKeyPoolSize         = "pool_size"
	AttributeKeyKeyed            = "keyed"
	AttributeKeyRequired         = "required"
	AttributeKeyEntityID         = "entity_id"
	AttributeKeyValidator        = "validator"
	// AttributeKeyOutput is a VRF output, 128 lowercase hex characters.
	AttributeKeyOutput = "output"
	// AttributeKeySeed is a seed, 64 lowercase hex characters.
	AttributeKeySeed      = "seed"
	AttributeKeySubmitted = "submitted"
	// AttributeKeyWinners is the comma-joined list of drawn entity ids, in draw order.
	AttributeKeyWinners     = "winners"
	AttributeKeyReason      = "reason"
	AttributeKeyCohortRound = "cohort_round"
)
