package concentration

import (
	"context"
	"errors"
	"fmt"
	"math"
	"math/big"
	"sort"
	"strings"
	"time"

	"github.com/saicon-network/saicon/indexer/internal/chain"
	"github.com/saicon-network/saicon/indexer/internal/store"
)

// Source is what the reader asks the chain; *chain.RPC and *chain.REST together satisfy it.
type Source interface {
	ValidatorSetAt(ctx context.Context, height int64) ([]chain.SetMember, error)
	BlockHashAt(ctx context.Context, height int64) (string, error)
	ValidatorsAt(ctx context.Context, height int64) ([]chain.Validator, error)
	RegistryValidatorsAt(ctx context.Context, height int64) ([]chain.RegistryEntry, error)
	EntityCapAt(ctx context.Context, height int64) (string, error)
	MaxActiveSeatsAt(ctx context.Context, height int64) (uint32, error)
	EntitiesAt(ctx context.Context, height int64) ([]chain.EntityRecord, error)
	EntityAt(ctx context.Context, height int64, id string) (chain.EntityDetail, error)
	EntityStakeAt(ctx context.Context, height int64, id string) (*chain.EntityStake, error)
	ValidatorEntityAt(ctx context.Context, height int64, operator string) (chain.ValidatorEntity, error)
}

// Unavailability codes stored in concentration_epochs (spec §5). An unavailable
// part is a fact about the node or the chain's history, never a measurement.
const (
	// Pruned: the node no longer holds the epoch's last height.
	Pruned = "pruned"
	// BeforeEntityLayer: the epoch ends before x/entityregistry existed.
	BeforeEntityLayer = "before_entity_layer"
	// NoEntityLayer: the chain runs no x/entityregistry.
	NoEntityLayer = "no_entity_layer"
	// FrozenMismatch prefixes the code recorded when the frozen-seat scan
	// disagrees with the chain's own seat count: "frozen_count_mismatch: <id>: chain <a>, found <b>".
	FrozenMismatch = "frozen_count_mismatch"
)

const (
	statusActive    = "STATUS_ACTIVE"
	entityAttested  = "ENTITY_STATUS_ATTESTED"
	roleMember      = "member"
	roleChargedSeat = "charged_frozen"
)

// Reader measures one closed epoch at its last block and stores the result.
// It is used from one goroutine: SetEntityLayer and the reads are not
// synchronised.
type Reader struct {
	s             *store.Store
	src           Source
	epochLen      uint64
	entityFrom    int64
	entityPresent bool
	now           func() time.Time
}

// NewReader returns a reader for a chain with the given epoch length. entityFrom
// and entityPresent are the answer of chain.REST.EntityLayerFrom: the first
// height at which the entity stores exist, and whether the chain has them at all.
func NewReader(s *store.Store, src Source, epochLen uint64, entityFrom int64, entityPresent bool) *Reader {
	return &Reader{s: s, src: src, epochLen: epochLen, entityFrom: entityFrom, entityPresent: entityPresent, now: time.Now}
}

// SetEntityLayer replaces the entity layer the reader gates on, for a layer
// that was absent when the reader was made and has since appeared.
func (r *Reader) SetEntityLayer(from int64, present bool) {
	r.entityFrom, r.entityPresent = from, present
}

// measured is one epoch as read, before it is written.
type measured struct {
	row      store.ConcentrationRow
	powers   []store.PowerRow
	entities []store.EntityRow
}

// ReadEpoch reads epoch ep at its last height H = ep·epochLen and writes exactly
// one row set for it, replacing any earlier one.
//
// A height the node has pruned, a height before the entity layer, a chain
// without one and a seat-count disagreement are recorded as unavailable parts
// with their reason; every other failure (including a height the node has not
// reached yet) is returned and nothing is written, so the epoch is read again.
func (r *Reader) ReadEpoch(ctx context.Context, chainID string, ep uint64) error {
	m, err := r.measure(ctx, ep)
	if err != nil {
		return err
	}
	return r.s.WriteConcentration(ctx, chainID, m.row, m.powers, m.entities)
}

// RereadEntities reads again an epoch that was written no_entity_layer before
// the entity layer was known, and writes it through the same path as
// ReadEpoch. The epoch must end at or after the layer's first height.
//
// A height the node has pruned since leaves the power figures already stored
// alone: only the entity code changes, to pruned, which is what a fresh read
// of that epoch would now record for its entity part. Either way the epoch no
// longer carries no_entity_layer, so it is not read again.
func (r *Reader) RereadEntities(ctx context.Context, chainID string, ep uint64) error {
	if !r.entityPresent || r.epochLen == 0 || ep > math.MaxInt64/r.epochLen || int64(ep*r.epochLen) < r.entityFrom {
		return fmt.Errorf("concentration: epoch %d is not covered by the entity layer, nothing to re-read", ep)
	}
	m, err := r.measure(ctx, ep)
	if err != nil {
		return err
	}
	if m.row.PowerUnavailable != nil || (m.row.EntitiesUnavailable != nil && *m.row.EntitiesUnavailable == Pruned) {
		_, err := r.s.MarkEntitiesUnavailable(ctx, chainID, ep, NoEntityLayer, Pruned)
		return err
	}
	if m.row.BlockHash == nil {
		// The hash is only the reset check, but losing one already stored would
		// weaken it; keep the old one.
		old, _, err := r.s.Concentration(ctx, chainID, &ep)
		if err != nil {
			return err
		}
		if old != nil {
			m.row.BlockHash = old.BlockHash
		}
	}
	return r.s.WriteConcentration(ctx, chainID, m.row, m.powers, m.entities)
}

// measure reads epoch ep at its last height, without writing anything.
func (r *Reader) measure(ctx context.Context, ep uint64) (*measured, error) {
	if r.epochLen == 0 || ep == 0 || ep > math.MaxInt64/r.epochLen {
		return nil, fmt.Errorf("concentration: epoch %d with epoch length %d has no last height", ep, r.epochLen)
	}
	h := int64(ep * r.epochLen)
	row := store.ConcentrationRow{Epoch: ep, Height: h, ReadAt: r.now().UTC().Format(time.RFC3339Nano)}

	set, err := r.src.ValidatorSetAt(ctx, h)
	if errors.Is(err, chain.ErrHeightUnavailable) {
		row.PowerUnavailable, row.EntitiesUnavailable = ptr(Pruned), ptr(Pruned)
		return &measured{row: row}, nil
	}
	if err != nil {
		return nil, fmt.Errorf("epoch %d: %w", ep, err)
	}

	hash, err := r.src.BlockHashAt(ctx, h)
	switch {
	case err == nil:
		row.BlockHash = &hash
	case errors.Is(err, chain.ErrHeightUnavailable):
		// The hash is only the reset check; the measures do not need it.
	default:
		return nil, fmt.Errorf("epoch %d: %w", ep, err)
	}

	vals, err := r.src.ValidatorsAt(ctx, h)
	stakingPruned := errors.Is(err, chain.ErrHeightUnavailable)
	if stakingPruned {
		vals = nil
	} else if err != nil {
		return nil, fmt.Errorf("epoch %d: %w", ep, err)
	}

	holders, err := holdersOf(set, vals)
	if err != nil {
		return nil, fmt.Errorf("epoch %d: %w", ep, err)
	}

	// The two layer gates come first: they are facts about the chain at H, and
	// they stay true whatever the node retains. Pruned is a fact about this node.
	var part *entityPart
	switch {
	case !r.entityPresent:
		row.EntitiesUnavailable = ptr(NoEntityLayer)
	case h < r.entityFrom:
		row.EntitiesUnavailable = ptr(BeforeEntityLayer)
	case stakingPruned:
		row.EntitiesUnavailable = ptr(Pruned)
	default:
		part, err = r.readEntities(ctx, h, vals, holders)
		switch {
		case errors.Is(err, chain.ErrHeightUnavailable):
			row.EntitiesUnavailable = ptr(Pruned)
		case err != nil:
			return nil, fmt.Errorf("epoch %d: %w", ep, err)
		case part.mismatch != "":
			row.EntitiesUnavailable = ptr(part.mismatch)
			part = nil
		}
	}

	if part != nil {
		// UnitOf looks a holder up by its operator string; the part's maps are
		// keyed by address bytes, so translate them for this set's operators.
		memberOf := make(map[string]string, len(holders))
		chargedTo := make(map[string]string, len(holders))
		for _, hd := range holders {
			if hd.Operator == "" {
				continue
			}
			k, err := chain.AddrKey(hd.Operator)
			if err != nil {
				return nil, fmt.Errorf("epoch %d: validator %s: %w", ep, hd.Operator, err)
			}
			if id, ok := part.memberOf[k]; ok {
				memberOf[hd.Operator] = id
			}
			if id, ok := part.chargedTo[k]; ok {
				chargedTo[hd.Operator] = id
			}
		}
		for i := range holders {
			holders[i].Unit = UnitOf(holders[i].Operator, holders[i].ConsAddr, memberOf, chargedTo)
		}
	}
	m, err := Measure(holders)
	if err != nil {
		return nil, fmt.Errorf("epoch %d: %w", ep, err)
	}
	row.SetSize = ptr(int64(m.SetSize))
	row.TotalPower = ptr(m.TotalPower)
	row.Top10Power = ptr(m.Top10Power)
	row.NakamotoValidators = ptr(int64(m.NakamotoValidators))

	var entities []store.EntityRow
	if part != nil {
		entities = part.rows
		row.UnitCount = ptr(int64(m.UnitCount))
		row.NakamotoEntities = ptr(int64(m.NakamotoEntities))
		row.EntityCount = ptr(int64(len(entities)))
		row.EntityCap = ptr(part.entityCap)
		row.MaxActiveSeats = ptr(int64(part.maxSeats))
	}

	powers := make([]store.PowerRow, len(holders))
	for i, hd := range holders {
		powers[i] = store.PowerRow{ConsAddr: hd.ConsAddr, Power: hd.Power}
		if hd.Operator != "" {
			powers[i].Operator = ptr(hd.Operator)
		}
		if hd.Unit != "" {
			powers[i].Unit = ptr(hd.Unit)
		}
	}
	return &measured{row: row, powers: powers, entities: entities}, nil
}

// holdersOf joins the consensus set to x/staking by consensus address. vals is
// nil when x/staking's state at the height is pruned, which leaves every
// operator empty.
func holdersOf(set []chain.SetMember, vals []chain.Validator) ([]Holder, error) {
	opByCons := make(map[string]string, len(vals))
	for _, v := range vals {
		cons, err := chain.ConsAddrFromPubKey(v.ConsensusPubKey)
		if err != nil {
			return nil, fmt.Errorf("validator %s: %w", v.Operator, err)
		}
		opByCons[cons] = v.Operator
	}
	holders := make([]Holder, len(set))
	for i, sm := range set {
		cons := strings.ToUpper(sm.Address)
		holders[i] = Holder{ConsAddr: cons, Operator: opByCons[cons], Power: sm.VotingPower}
	}
	return holders, nil
}

// entityPart is everything the entity side of an epoch yields.
type entityPart struct {
	entityCap string
	maxSeats  uint32
	rows      []store.EntityRow
	// memberOf and chargedTo are keyed by chain.AddrKey of the operator: the
	// registry, the entity and x/staking may name one validator with different
	// prefixes (J5).
	memberOf, chargedTo map[string]string
	// mismatch is the frozen_count_mismatch code; "" when the scan agrees with the chain.
	mismatch string
}

// chargedSeat is a frozen seat found by the scan that still counts against its former entity.
type chargedSeat struct {
	operator string
	seat     chain.FrozenSeat
}

// readEntities reads the registry, the entities, their stake and the frozen
// seats at h. Every error is returned as is; the caller decides what a pruned
// height means.
func (r *Reader) readEntities(ctx context.Context, h int64, vals []chain.Validator, holders []Holder) (*entityPart, error) {
	reg, err := r.src.RegistryValidatorsAt(ctx, h)
	if err != nil {
		return nil, err
	}
	regStatus := make(map[string]string, len(reg))
	for _, e := range reg {
		k, err := chain.AddrKey(e.Operator)
		if err != nil {
			return nil, fmt.Errorf("registry: %w", err)
		}
		regStatus[k] = e.Status
	}
	statusOf := func(operator string) (string, bool, error) {
		k, err := chain.AddrKey(operator)
		if err != nil {
			return "", false, err
		}
		s, ok := regStatus[k]
		return s, ok, nil
	}
	// keys holds chain.AddrKey of every x/staking operator at h.
	keys := make(map[string]string, len(vals))
	for _, v := range vals {
		k, err := chain.AddrKey(v.Operator)
		if err != nil {
			return nil, fmt.Errorf("validator %s: %w", v.Operator, err)
		}
		keys[v.Operator] = k
	}

	part := &entityPart{memberOf: map[string]string{}, chargedTo: map[string]string{}}
	if part.entityCap, err = r.src.EntityCapAt(ctx, h); err != nil {
		return nil, err
	}
	if part.maxSeats, err = r.src.MaxActiveSeatsAt(ctx, h); err != nil {
		return nil, err
	}
	listed, err := r.src.EntitiesAt(ctx, h)
	if err != nil {
		return nil, err
	}
	ents := append([]chain.EntityRecord(nil), listed...)
	sort.Slice(ents, func(i, j int) bool { return ents[i].ID < ents[j].ID })

	details := make(map[string]chain.EntityDetail, len(ents))
	stakes := make(map[string]*chain.EntityStake, len(ents))
	activeMembers := make(map[string]int64, len(ents))
	for _, e := range ents {
		d, err := r.src.EntityAt(ctx, h, e.ID)
		if err != nil {
			return nil, err
		}
		details[e.ID] = d
		for _, m := range d.Members {
			st, ok, err := statusOf(m)
			if err != nil {
				return nil, fmt.Errorf("entity %s member: %w", e.ID, err)
			}
			if ok && st == statusActive {
				activeMembers[e.ID]++
			}
			if e.Status == entityAttested {
				k, err := chain.AddrKey(m)
				if err != nil {
					return nil, fmt.Errorf("entity %s member: %w", e.ID, err)
				}
				part.memberOf[k] = e.ID
			}
		}
		if e.Status == entityAttested {
			if stakes[e.ID], err = r.src.EntityStakeAt(ctx, h, e.ID); err != nil {
				return nil, err
			}
			// The tokens are stored verbatim and read back as an exact integer:
			// an answer that is not one is a fault to retry, not a figure.
			if st := stakes[e.ID]; st != nil {
				if _, ok := new(big.Int).SetString(st.Tokens, 10); !ok {
					return nil, fmt.Errorf("entity %s stake: tokens %q is not an integer", e.ID, st.Tokens)
				}
			}
		}
	}

	// A charged frozen seat can only sit on a registry-ACTIVE validator that is
	// not a member of an attested entity, so only those are asked about.
	var charged []chargedSeat
	for _, v := range vals {
		st, ok, err := statusOf(v.Operator)
		if err != nil {
			return nil, fmt.Errorf("validator %s: %w", v.Operator, err)
		}
		if _, member := part.memberOf[keys[v.Operator]]; member || !ok || st != statusActive {
			continue
		}
		ve, err := r.src.ValidatorEntityAt(ctx, h, v.Operator)
		if err != nil {
			return nil, err
		}
		if ve.Frozen != nil {
			part.chargedTo[keys[v.Operator]] = ve.Frozen.FormerEntityID
			charged = append(charged, chargedSeat{operator: v.Operator, seat: *ve.Frozen})
		}
	}

	chargedCount := map[string]int64{}
	for _, c := range charged {
		chargedCount[c.seat.FormerEntityID]++
	}
	// The chain's own count of charged seats per entity must equal what the scan found.
	ids := make(map[string]bool, len(ents))
	for _, e := range ents {
		ids[e.ID] = true
	}
	for id := range chargedCount {
		ids[id] = true
	}
	sorted := make([]string, 0, len(ids))
	for id := range ids {
		sorted = append(sorted, id)
	}
	sort.Strings(sorted)
	for _, id := range sorted {
		want := int64(details[id].ActiveSeats) - activeMembers[id]
		if want != chargedCount[id] {
			part.mismatch = fmt.Sprintf("%s: %s: chain %d, found %d", FrozenMismatch, id, want, chargedCount[id])
			return part, nil
		}
	}

	// Seats are looked up by address bytes too, so a member named in another
	// prefix still finds its tokens and power.
	tokens := make(map[string]string, len(vals))
	for _, v := range vals {
		tokens[keys[v.Operator]] = v.Tokens
	}
	powerOf := make(map[string]int64, len(holders))
	for _, hd := range holders {
		if hd.Operator != "" {
			powerOf[keys[hd.Operator]] = hd.Power
		}
	}

	for _, e := range ents {
		attested := e.Status == entityAttested
		if !attested && chargedCount[e.ID] == 0 {
			continue // a revoked entity nothing is charged to is not part of the picture (J6)
		}
		d := details[e.ID]
		er := store.EntityRow{
			EntityID: e.ID, DisplayName: e.DisplayName, Status: e.Status,
			AttestedHeight: ptr(e.AttestedHeight), ActiveSeats: ptr(int64(d.ActiveSeats)),
		}
		if s := stakes[e.ID]; s != nil {
			er.Tokens, er.EntityCap, er.Room = ptr(s.Tokens), ptr(s.EntityCap), ptr(s.Room)
			er.MaxActiveSeats = ptr(int64(s.MaxActiveSeats))
			er.Frozen = ptr(s.Frozen)
			if s.Reason != "" {
				er.FrozenReason = ptr(s.Reason)
			}
		}
		if attested {
			for _, m := range d.Members {
				er.Seats = append(er.Seats, seatOf(m, roleMember, statusPtr(statusOf, m), tokens, powerOf))
			}
		}
		for _, c := range charged {
			if c.seat.FormerEntityID != e.ID {
				continue
			}
			st := seatOf(c.operator, roleChargedSeat, ptr(statusActive), tokens, powerOf)
			if c.seat.Reason != "" {
				st.FrozenReason = ptr(c.seat.Reason)
			}
			st.FrozenHeight = ptr(c.seat.Height)
			er.Seats = append(er.Seats, st)
		}
		part.rows = append(part.rows, er)
	}
	return part, nil
}

// statusPtr is the registry status of operator, nil when the registry has no
// record. Its error is dropped on purpose: statusOf fails only on an address
// that does not decode, and every member address was already decoded by the
// same function while counting activeMembers, which returns that error, so
// none can reach here.
func statusPtr(statusOf func(string) (string, bool, error), operator string) *string {
	if s, ok, err := statusOf(operator); err == nil && ok {
		return &s
	}
	return nil
}

// seatOf builds one seat row; tokens and power stay nil when x/staking or the
// consensus set did not have the operator at the height. Both maps are keyed by
// chain.AddrKey; an operator that does not decode finds neither.
func seatOf(operator, role string, status *string, tokens map[string]string, powerOf map[string]int64) store.SeatRow {
	st := store.SeatRow{Operator: operator, Role: role, RegistryStatus: status}
	k, err := chain.AddrKey(operator)
	if err != nil {
		return st
	}
	if t, ok := tokens[k]; ok {
		st.Tokens = ptr(t)
	}
	if p, ok := powerOf[k]; ok {
		st.Power = ptr(p)
	}
	return st
}

func ptr[T any](v T) *T { return &v }
