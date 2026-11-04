package types

import "cosmossdk.io/math"

// EntityStanding is a validator's position under the entity aggregate cap
// (ADR-0012 D4). The zero standing (EntityID == "", not frozen) leaves only the
// per-validator cap in force.
type EntityStanding struct {
	// EntityID is "" when the validator belongs to no attested entity.
	EntityID string
	// Frozen means the validator takes no new stake.
	Frozen bool
	// Reason says why Frozen is set.
	Reason string
	// Tokens is the aggregate over the entity's members, every bond status
	// (zero if EntityID == "").
	Tokens math.Int
	// Cap is CapState.EntityCap (zero if EntityID == "").
	Cap math.Int
}

// Room is Cap - Tokens. It may be negative, and is meaningful only when
// EntityID != "".
func (s EntityStanding) Room() math.Int { return s.Cap.Sub(s.Tokens) }

// RoomFor is the room a leg may take given the validator's own room: zero when
// frozen; with an entity, the smaller of validatorRoom and the entity's room
// (never below zero); otherwise validatorRoom. It is the one place the router
// computes a leg's room.
func (s EntityStanding) RoomFor(validatorRoom math.Int) math.Int {
	if s.Frozen {
		return math.ZeroInt()
	}
	if s.EntityID == "" {
		return validatorRoom
	}
	return math.MinInt(validatorRoom, math.MaxInt(math.ZeroInt(), s.Room()))
}
