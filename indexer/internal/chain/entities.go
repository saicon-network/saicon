package chain

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"strconv"
)

// EntityRecord is one row of the entity registry.
type EntityRecord struct {
	ID, DisplayName, Status string // Status: "ENTITY_STATUS_ATTESTED" | "ENTITY_STATUS_REVOKED"
	AttestedHeight          int64
}

// EntityDetail is one entity with its members.
type EntityDetail struct {
	Members     []string // operator addresses (saiconvaloper…)
	ActiveSeats uint32
}

// EntityStake is the stake engine's own aggregate for an entity, verbatim.
type EntityStake struct {
	Tokens, EntityCap, Room     string
	ActiveSeats, MaxActiveSeats uint32
	Frozen                      bool
	Reason                      string
}

// FrozenSeat is a validator's seat kept after its entity was revoked.
type FrozenSeat struct {
	FormerEntityID, Reason string
	Height                 int64
}

// ValidatorEntity is where a validator stands in the entity registry.
type ValidatorEntity struct {
	Attested bool
	EntityID string
	Frozen   *FrozenSeat // nil unless the validator holds a frozen seat
	Pending  bool
}

const (
	entityRegistryPath = "/saicon/chain/entityregistry/v1"
	stakeEnginePath    = "/saicon/chain/stakeengine/v1"
)

// EntityCapAt returns cap_state.entity_cap at height, as the chain writes it. A body
// without it is an error, never "0".
func (r *REST) EntityCapAt(ctx context.Context, height int64) (string, error) {
	var body struct {
		CapState struct {
			EntityCap string `json:"entity_cap"`
		} `json:"cap_state"`
	}
	if err := getJSONAt(ctx, r.c, r.base+stakeEnginePath+"/caps", height, &body); err != nil {
		return "", fmt.Errorf("entity cap at %d: %w", height, err)
	}
	if body.CapState.EntityCap == "" {
		return "", fmt.Errorf("entity cap at %d: answer carries no entity_cap", height)
	}
	return body.CapState.EntityCap, nil
}

// MaxActiveSeatsAt returns params.max_active_seats at height. A body without it is an
// error, never 0.
func (r *REST) MaxActiveSeatsAt(ctx context.Context, height int64) (uint32, error) {
	var body struct {
		Params struct {
			MaxActiveSeats *uint32 `json:"max_active_seats"`
		} `json:"params"`
	}
	if err := getJSONAt(ctx, r.c, r.base+entityRegistryPath+"/params", height, &body); err != nil {
		return 0, fmt.Errorf("max active seats at %d: %w", height, err)
	}
	if body.Params.MaxActiveSeats == nil {
		return 0, fmt.Errorf("max active seats at %d: answer carries no max_active_seats", height)
	}
	return *body.Params.MaxActiveSeats, nil
}

// EntitiesAt lists every entity at height, following pagination.next_key.
func (r *REST) EntitiesAt(ctx context.Context, height int64) ([]EntityRecord, error) {
	var out []EntityRecord
	seen := map[string]bool{}
	key := ""
	for {
		u := r.base + entityRegistryPath + "/entities?pagination.limit=500"
		if key != "" {
			u += "&pagination.key=" + url.QueryEscape(key)
		}
		var body struct {
			Entities []struct {
				ID             string `json:"id"`
				DisplayName    string `json:"display_name"`
				Status         string `json:"status"`
				AttestedHeight string `json:"attested_height"`
			} `json:"entities"`
			Pagination struct {
				NextKey *string `json:"next_key"`
			} `json:"pagination"`
		}
		if err := getJSONAt(ctx, r.c, u, height, &body); err != nil {
			return nil, fmt.Errorf("entities at %d: %w", height, err)
		}
		for _, e := range body.Entities {
			h, err := parseInt64(e.AttestedHeight)
			if err != nil {
				return nil, fmt.Errorf("entities at %d: %s: attested_height %q: %w", height, e.ID, e.AttestedHeight, err)
			}
			out = append(out, EntityRecord{ID: e.ID, DisplayName: e.DisplayName, Status: e.Status, AttestedHeight: h})
		}
		if body.Pagination.NextKey == nil || *body.Pagination.NextKey == "" {
			return out, nil
		}
		key = *body.Pagination.NextKey
		if seen[key] {
			return nil, fmt.Errorf("entities at %d: next_key %q repeats", height, key)
		}
		seen[key] = true
	}
}

// EntityAt returns an entity's members and the chain's own seat count at height.
func (r *REST) EntityAt(ctx context.Context, height int64, id string) (EntityDetail, error) {
	var body struct {
		Members []struct {
			Validator string `json:"validator"`
		} `json:"members"`
		ActiveSeats uint32 `json:"active_seats"`
	}
	u := r.base + entityRegistryPath + "/entities/" + url.PathEscape(id)
	if err := getJSONAt(ctx, r.c, u, height, &body); err != nil {
		return EntityDetail{}, fmt.Errorf("entity %s at %d: %w", id, height, err)
	}
	d := EntityDetail{ActiveSeats: body.ActiveSeats, Members: make([]string, 0, len(body.Members))}
	for _, m := range body.Members {
		d.Members = append(d.Members, m.Validator)
	}
	return d, nil
}

// EntityStakeAt returns the stake engine's aggregate for an entity at height; nil when the
// chain has no record (a revoked entity has no members to aggregate).
func (r *REST) EntityStakeAt(ctx context.Context, height int64, id string) (*EntityStake, error) {
	var body struct {
		Tokens         string `json:"tokens"`
		EntityCap      string `json:"entity_cap"`
		Room           string `json:"room"`
		ActiveSeats    uint32 `json:"active_seats"`
		MaxActiveSeats uint32 `json:"max_active_seats"`
		Frozen         bool   `json:"frozen"`
		Reason         string `json:"reason"`
	}
	u := r.base + stakeEnginePath + "/entities/" + url.PathEscape(id) + "/stake"
	if err := getJSONAt(ctx, r.c, u, height, &body); err != nil {
		var nr noRecordError
		if errors.As(err, &nr) {
			return nil, nil
		}
		return nil, fmt.Errorf("entity stake %s at %d: %w", id, height, err)
	}
	return &EntityStake{
		Tokens: body.Tokens, EntityCap: body.EntityCap, Room: body.Room,
		ActiveSeats: body.ActiveSeats, MaxActiveSeats: body.MaxActiveSeats,
		Frozen: body.Frozen, Reason: body.Reason,
	}, nil
}

// ValidatorEntityAt returns where a validator (by operator address) stands in the entity
// registry at height. It is the only query that exposes a frozen seat.
func (r *REST) ValidatorEntityAt(ctx context.Context, height int64, operator string) (ValidatorEntity, error) {
	var body struct {
		Attested bool   `json:"attested"`
		EntityID string `json:"entity_id"`
		Pending  bool   `json:"pending"`
		Frozen   *struct {
			FormerEntityID string `json:"former_entity_id"`
			Reason         string `json:"reason"`
			Height         string `json:"height"`
		} `json:"frozen"`
	}
	u := r.base + entityRegistryPath + "/validators/" + url.PathEscape(operator) + "/entity"
	if err := getJSONAt(ctx, r.c, u, height, &body); err != nil {
		return ValidatorEntity{}, fmt.Errorf("validator entity %s at %d: %w", operator, height, err)
	}
	ve := ValidatorEntity{Attested: body.Attested, EntityID: body.EntityID, Pending: body.Pending}
	if body.Frozen != nil {
		h, err := strconv.ParseInt(body.Frozen.Height, 10, 64)
		if err != nil {
			return ValidatorEntity{}, fmt.Errorf("validator entity %s at %d: frozen height %q: %w", operator, height, body.Frozen.Height, err)
		}
		ve.Frozen = &FrozenSeat{FormerEntityID: body.Frozen.FormerEntityID, Reason: body.Frozen.Reason, Height: h}
	}
	return ve, nil
}
