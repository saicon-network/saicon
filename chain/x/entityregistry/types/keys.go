package types

import "cosmossdk.io/collections"

const (
	// ModuleName defines the module name.
	ModuleName = "entityregistry"

	// StoreKey defines the primary module store key.
	StoreKey = ModuleName

	// GovModuleName duplicates the gov module's name to avoid a dependency with x/gov.
	GovModuleName = "gov"

	// AccountPrefix is the bech32 prefix of account addresses.
	AccountPrefix = "saicon"
	// ValidatorPrefix is the bech32 prefix of validator operator addresses.
	ValidatorPrefix = "saiconvaloper"

	// MaxMembersPerEntity bounds an entity's validators, and with it the gas of
	// every delegation check that sums an entity's stake (spec J6). Tier 3.
	MaxMembersPerEntity = 8
	// TicketsPerEntity is ADR-0014 D3: one lottery ticket per attested entity. Tier 3.
	TicketsPerEntity uint32 = 1
	// MaxAttestors bounds params.attestors (type sanity, ADR-0013 tier 1).
	MaxAttestors = 10
	// MaxDisplayNameBytes bounds Entity.display_name.
	MaxDisplayNameBytes = 64
	// MaxReasonBytes bounds a revocation reason.
	MaxReasonBytes = 256
)

// Store prefixes. No prefix may be a prefix of another: the collections schema
// builder refuses that.
var (
	ParamsKey          = collections.NewPrefix("p_entityregistry")
	EntitiesKey        = collections.NewPrefix("er_entities")
	MembersKey         = collections.NewPrefix("er_members")
	MembersByEntityKey = collections.NewPrefix("er_by_entity")
	FrozenKey          = collections.NewPrefix("er_frozen")
	PendingKey         = collections.NewPrefix("er_pending")
	// VrfKeyOffersKey holds each validator's standing VRF key offer.
	VrfKeyOffersKey = collections.NewPrefix("er_vrf_offers")
	// VrfKeyOwnersKey maps a VRF public key to the one validator it belongs to; never pruned.
	VrfKeyOwnersKey = collections.NewPrefix("er_vrf_owners")
	// DeparturesKey holds each validator's last departure from an entity.
	DeparturesKey = collections.NewPrefix("er_departures")
)
