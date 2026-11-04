package types

// Event types emitted by x/entityregistry.
const (
	EventTypeEntityAttested     = "entity_attested"
	EventTypeMemberRemoved      = "entity_member_removed"
	EventTypeEntityRevoked      = "entity_revoked"
	EventTypeSeatFrozen         = "entity_seat_frozen"
	EventTypeActivationHeld     = "validator_activation_held"
	EventTypeActivationReleased = "validator_activation_released"
	EventTypeVrfKeyOffered      = "vrf_key_offered"
	EventTypeVrfKeyBound        = "vrf_key_bound"
)

// Event attribute keys.
const (
	AttributeKeyEntityID        = "entity_id"
	AttributeKeyAttestor        = "attestor"
	AttributeKeyAttestationHash = "attestation_hash"
	// AttributeKeyMembers is the comma-joined list of member valoper addresses.
	AttributeKeyMembers   = "members"
	AttributeKeyReplaced  = "replaced"
	AttributeKeyValidator = "validator"
	AttributeKeyAuthority = "authority"
	AttributeKeyReason    = "reason"
	// AttributeKeyVrfPublicKey is a VRF public key, 64 lowercase hex characters.
	AttributeKeyVrfPublicKey = "vrf_public_key"
	// AttributeKeyVrfKeys is the comma-joined list of validators whose VRF key an attestation set or changed.
	AttributeKeyVrfKeys = "vrf_keys"
)
