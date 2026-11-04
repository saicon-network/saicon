package api

import (
	"fmt"
	"math"
	"net/http"
	"strconv"

	"github.com/saicon-network/saicon/indexer/internal/concentration"
	"github.com/saicon-network/saicon/indexer/internal/store"
)

// parseEpoch reads the optional ?epoch=N. ok is false for a malformed value,
// which the caller answers with 400. A value past MaxInt64 is clamped: the SQL
// driver refuses a uint64 with the high bit set, and no epoch comes near it.
func parseEpoch(r *http.Request) (epoch *uint64, ok bool) {
	raw := r.URL.Query().Get("epoch")
	if raw == "" {
		return nil, true
	}
	n, err := strconv.ParseUint(raw, 10, 64)
	if err != nil {
		return nil, false
	}
	if n > uint64(math.MaxInt64) {
		n = uint64(math.MaxInt64)
	}
	return &n, true
}

// notMeasured is the 404 for a request that found no readable epoch.
func notMeasured(w http.ResponseWriter, epoch *uint64) {
	msg := "no measured epoch"
	if epoch != nil {
		msg = fmt.Sprintf("epoch %d was not measured", *epoch)
	}
	writeJSON(w, http.StatusNotFound, map[string]string{"error": msg})
}

// i64s renders an exact integer as a decimal string, nil staying nil. Powers
// and totals are strings on the wire so no client reads them through a float.
func i64s(n *int64) *string {
	if n == nil {
		return nil
	}
	s := strconv.FormatInt(*n, 10)
	return &s
}

// top10Share is the truncated share of the ten largest, nil when either figure
// is absent or the pair is not a share.
func top10Share(top10, total *int64) *string {
	if top10 == nil || total == nil {
		return nil
	}
	if s := concentration.Share(*top10, *total); s != "" {
		return &s
	}
	return nil
}

type validatorJSON struct {
	ConsAddr string  `json:"cons_addr"`
	Operator *string `json:"operator"`
	Moniker  *string `json:"moniker"`
	Power    string  `json:"power"`
	Unit     *string `json:"unit"`
}

type curveJSON struct {
	Validators []string `json:"validators"`
	Entities   []string `json:"entities"`
}

func (s *server) concentration(w http.ResponseWriter, r *http.Request) {
	epoch, ok := parseEpoch(r)
	if !ok {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "epoch must be an unsigned integer"})
		return
	}
	row, powers, err := s.s.Concentration(r.Context(), s.chainID, epoch)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	if row == nil {
		notMeasured(w, epoch)
		return
	}

	// An epoch that was read always had a non-empty set, so no powers means the
	// detail rows were pruned: validators and curve are then absent, not empty.
	var validators []validatorJSON
	var curve *curveJSON
	if len(powers) > 0 {
		holders := make([]concentration.Holder, len(powers))
		for i, p := range powers {
			hd := concentration.Holder{ConsAddr: p.ConsAddr, Power: p.Power}
			if p.Operator != nil {
				hd.Operator = *p.Operator
			}
			if p.Unit != nil {
				hd.Unit = *p.Unit
			}
			holders[i] = hd
		}
		// Store order is power descending then cons address, the same order as
		// concentration.Ranked; the list is served in it and the curve ranks alike.
		validators = make([]validatorJSON, len(powers))
		for i, p := range powers {
			validators[i] = validatorJSON{ConsAddr: p.ConsAddr, Operator: p.Operator, Moniker: p.Moniker,
				Power: strconv.FormatInt(p.Power, 10), Unit: p.Unit}
		}
		cv, ce := concentration.Curve(holders)
		curve = &curveJSON{Validators: cv, Entities: ce}
		if row.EntitiesUnavailable != nil {
			curve.Entities = nil
		}
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"epoch": row.Epoch, "height": row.Height, "block_hash": row.BlockHash,
		"set_size": row.SetSize, "total_power": i64s(row.TotalPower), "top10_power": i64s(row.Top10Power),
		"top10_share":          top10Share(row.Top10Power, row.TotalPower),
		"nakamoto_validators":  row.NakamotoValidators,
		"entities_unavailable": row.EntitiesUnavailable, "unit_count": row.UnitCount,
		"nakamoto_entities": row.NakamotoEntities, "entity_count": row.EntityCount,
		"entity_cap": row.EntityCap, "max_active_seats": row.MaxActiveSeats,
		"validators": validators, "curve": curve,
	})
}

func (s *server) concentrationEpochs(w http.ResponseWriter, r *http.Request) {
	from := parseUint(r.URL.Query().Get("from"), 0)
	to := parseUint(r.URL.Query().Get("to"), uint64(math.MaxInt64))
	rows, err := s.s.ConcentrationSeries(r.Context(), s.chainID, from, to)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	out := make([]map[string]any, len(rows))
	for i, row := range rows {
		out[i] = map[string]any{
			"epoch": row.Epoch, "height": row.Height, "power_unavailable": row.PowerUnavailable,
			"set_size": row.SetSize, "total_power": i64s(row.TotalPower), "top10_power": i64s(row.Top10Power),
			"top10_share":          top10Share(row.Top10Power, row.TotalPower),
			"nakamoto_validators":  row.NakamotoValidators,
			"entities_unavailable": row.EntitiesUnavailable, "unit_count": row.UnitCount,
			"nakamoto_entities": row.NakamotoEntities, "entity_count": row.EntityCount,
		}
	}
	writeJSON(w, http.StatusOK, map[string]any{"epochs": out})
}

type seatJSON struct {
	Operator       string  `json:"operator"`
	Moniker        *string `json:"moniker"`
	RegistryStatus *string `json:"registry_status"`
	Tokens         *string `json:"tokens"`
	Power          *string `json:"power"`
}

type frozenSeatJSON struct {
	seatJSON
	FrozenReason *string `json:"frozen_reason"`
	FrozenHeight *int64  `json:"frozen_height"`
}

type entityJSON struct {
	EntityID       string           `json:"entity_id"`
	DisplayName    string           `json:"display_name"`
	Status         string           `json:"status"`
	AttestedHeight *int64           `json:"attested_height"`
	Tokens         *string          `json:"tokens"`
	EntityCap      *string          `json:"entity_cap"`
	Room           *string          `json:"room"`
	ActiveSeats    *int64           `json:"active_seats"`
	MaxActiveSeats *int64           `json:"max_active_seats"`
	Frozen         *bool            `json:"frozen"`
	FrozenReason   *string          `json:"frozen_reason"`
	Members        []seatJSON       `json:"members"`
	ChargedFrozen  []frozenSeatJSON `json:"charged_frozen"`
}

func (s *server) entities(w http.ResponseWriter, r *http.Request) {
	epoch, ok := parseEpoch(r)
	if !ok {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "epoch must be an unsigned integer"})
		return
	}
	row, ents, err := s.s.Entities(r.Context(), s.chainID, epoch)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	if row == nil {
		notMeasured(w, epoch)
		return
	}

	// The store answers a pruned epoch with its row and an empty list, which is
	// indistinguishable from "no entities". An empty list is trusted as a real
	// zero only when the row counted none and the epoch is inside the detail
	// window; otherwise the detail is gone and the list is absent.
	var out []entityJSON
	if len(ents) > 0 {
		out = make([]entityJSON, len(ents))
		for i, e := range ents {
			ej := entityJSON{EntityID: e.EntityID, DisplayName: e.DisplayName, Status: e.Status,
				AttestedHeight: e.AttestedHeight, Tokens: e.Tokens, EntityCap: e.EntityCap, Room: e.Room,
				ActiveSeats: e.ActiveSeats, MaxActiveSeats: e.MaxActiveSeats, Frozen: e.Frozen,
				FrozenReason: e.FrozenReason, Members: []seatJSON{}, ChargedFrozen: []frozenSeatJSON{}}
			for _, st := range e.Seats {
				sj := seatJSON{Operator: st.Operator, Moniker: st.Moniker, RegistryStatus: st.RegistryStatus,
					Tokens: st.Tokens, Power: i64s(st.Power)}
				if st.Role == "charged_frozen" {
					ej.ChargedFrozen = append(ej.ChargedFrozen, frozenSeatJSON{seatJSON: sj,
						FrozenReason: st.FrozenReason, FrozenHeight: st.FrozenHeight})
				} else {
					ej.Members = append(ej.Members, sj)
				}
			}
			out[i] = ej
		}
	} else {
		pruned := row.EntityCount != nil && *row.EntityCount > 0
		if !pruned {
			highest, found, err := s.s.HighestConcentrationEpoch(r.Context(), s.chainID)
			if err != nil {
				writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
				return
			}
			pruned = found && highest >= store.DetailRetentionEpochs && row.Epoch <= highest-store.DetailRetentionEpochs
		}
		if !pruned {
			out = []entityJSON{}
		}
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"epoch": row.Epoch, "height": row.Height, "entity_cap": row.EntityCap,
		"max_active_seats": row.MaxActiveSeats, "entities": out,
	})
}
