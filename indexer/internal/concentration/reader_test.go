package concentration_test

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"sort"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/saicon-network/saicon/indexer/internal/chain"
	"github.com/saicon-network/saicon/indexer/internal/concentration"
	"github.com/saicon-network/saicon/indexer/internal/store"
)

// The demo's three validators (the localnet captured in internal/chain/testdata).
const (
	opFirst  = "saiconvaloper128d6d2qq4g5ej57vhuzseudffp4pxy4ln7red8"
	opSecond = "saiconvaloper106p86dmlwqgg0u90lnkc8fvg06a2x2gk29wvyf"
	opThird  = "saiconvaloper17jn6zrjaxkp6kg4mnsuw30n2g5ex2fyujwma9n"

	regFirst  = "saicon128d6d2qq4g5ej57vhuzseudffp4pxy4ltes056"
	regSecond = "saicon106p86dmlwqgg0u90lnkc8fvg06a2x2gkjza6a5"
	regThird  = "saicon17jn6zrjaxkp6kg4mnsuw30n2g5ex2fyu2fgtuw"

	consFirst  = "BF3F818695ADA407C1BA003CDB15F482CCB8BABB"
	consSecond = "D47D35B94A871E0214EE2985944F8BCA547326FD"
	consThird  = "E65E6D5E277998A8DA7B5E4E59646A856FEBE0E7"

	keyFirst  = "RbK0ReEqsjRKmzXAKtwApFYQRHnZsoAxq60Tn5m3pmk="
	keySecond = "yykY0OPEUKCcscoJr3QwCXnMFIeQSGzKysrRxx3Ngsk="
	keyThird  = "3336v4li8ltbd2FY7QgXf8G6rrV7wfcgUZ8af0dTZn8="

	operatorEntity = "saicon-devnet-operator"
	revokedEntity  = "demo-third"
)

// fakeSource answers from maps and records every call as "Method" or "Method:arg".
// errs injects an error for a method name (the same name the call log uses).
type fakeSource struct {
	set    []chain.SetMember
	hash   string
	vals   []chain.Validator
	reg    []chain.RegistryEntry
	capNum string
	seats  uint32
	ents   []chain.EntityRecord
	detail map[string]chain.EntityDetail
	stake  map[string]*chain.EntityStake
	ve     map[string]chain.ValidatorEntity

	errs  map[string]error
	calls []string
}

func (f *fakeSource) enter(method, arg string) error {
	name := method
	if arg != "" {
		name += ":" + arg
	}
	f.calls = append(f.calls, name)
	if err := f.errs[method]; err != nil {
		return err
	}
	return f.errs[name]
}

func (f *fakeSource) called(prefix string) int {
	n := 0
	for _, c := range f.calls {
		if c == prefix || (len(c) > len(prefix) && c[:len(prefix)+1] == prefix+":") {
			n++
		}
	}
	return n
}

func (f *fakeSource) ValidatorSetAt(context.Context, int64) ([]chain.SetMember, error) {
	return f.set, f.enter("ValidatorSetAt", "")
}
func (f *fakeSource) BlockHashAt(context.Context, int64) (string, error) {
	return f.hash, f.enter("BlockHashAt", "")
}
func (f *fakeSource) ValidatorsAt(context.Context, int64) ([]chain.Validator, error) {
	return f.vals, f.enter("ValidatorsAt", "")
}
func (f *fakeSource) RegistryValidatorsAt(context.Context, int64) ([]chain.RegistryEntry, error) {
	return f.reg, f.enter("RegistryValidatorsAt", "")
}
func (f *fakeSource) EntityCapAt(context.Context, int64) (string, error) {
	return f.capNum, f.enter("EntityCapAt", "")
}
func (f *fakeSource) MaxActiveSeatsAt(context.Context, int64) (uint32, error) {
	return f.seats, f.enter("MaxActiveSeatsAt", "")
}
func (f *fakeSource) EntitiesAt(context.Context, int64) ([]chain.EntityRecord, error) {
	return f.ents, f.enter("EntitiesAt", "")
}
func (f *fakeSource) EntityAt(_ context.Context, _ int64, id string) (chain.EntityDetail, error) {
	return f.detail[id], f.enter("EntityAt", id)
}
func (f *fakeSource) EntityStakeAt(_ context.Context, _ int64, id string) (*chain.EntityStake, error) {
	return f.stake[id], f.enter("EntityStakeAt", id)
}
func (f *fakeSource) ValidatorEntityAt(_ context.Context, _ int64, op string) (chain.ValidatorEntity, error) {
	return f.ve[op], f.enter("ValidatorEntityAt", op)
}

// demoSource is the demo's end state at its last epoch: three validators, the
// operator entity with the first two as members, the revoked demo-third whose
// frozen seat is still charged to it on the third validator.
func demoSource() *fakeSource {
	return &fakeSource{
		set: []chain.SetMember{
			{Address: consFirst, PubKey: keyFirst, VotingPower: 100_000_000},
			{Address: consSecond, PubKey: keySecond, VotingPower: 200_000},
			{Address: consThird, PubKey: keyThird, VotingPower: 200_000},
		},
		hash: "AB" + fmt.Sprintf("%062d", 1),
		vals: []chain.Validator{
			{Operator: opFirst, Tokens: "100000000000000", ConsensusPubKey: keyFirst},
			{Operator: opSecond, Tokens: "200000000000", ConsensusPubKey: keySecond},
			{Operator: opThird, Tokens: "200000000000", ConsensusPubKey: keyThird},
		},
		reg: []chain.RegistryEntry{
			{Operator: regFirst, Status: "STATUS_ACTIVE"},
			{Operator: regSecond, Status: "STATUS_ACTIVE"},
			{Operator: regThird, Status: "STATUS_ACTIVE"},
		},
		capNum: "30000000000000",
		seats:  4,
		ents: []chain.EntityRecord{
			{ID: operatorEntity, DisplayName: "SAiCoN DevNet operator", Status: "ENTITY_STATUS_ATTESTED", AttestedHeight: 31},
			{ID: revokedEntity, DisplayName: "Demo third", Status: "ENTITY_STATUS_REVOKED", AttestedHeight: 36},
		},
		detail: map[string]chain.EntityDetail{
			operatorEntity: {Members: []string{opFirst, opSecond}, ActiveSeats: 2},
			revokedEntity:  {Members: []string{}, ActiveSeats: 1},
		},
		stake: map[string]*chain.EntityStake{
			operatorEntity: {Tokens: "100200000000000", EntityCap: "30000000000000", Room: "0", ActiveSeats: 2, MaxActiveSeats: 4},
		},
		ve: map[string]chain.ValidatorEntity{
			opThird: {Frozen: &chain.FrozenSeat{FormerEntityID: revokedEntity, Reason: "entity demo-third revoked: demo finished", Height: 38}},
		},
		errs: map[string]error{},
	}
}

// The two real clients together are a Source.
var _ concentration.Source = struct {
	*chain.RPC
	*chain.REST
}{}

func newStore(t *testing.T) *store.Store {
	t.Helper()
	s, err := store.Open(filepath.Join(t.TempDir(), "c.db"))
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, s.Close()) })
	require.NoError(t, s.SetChain(context.Background(), "c1", time.Now()))
	return s
}

// read runs ReadEpoch for epoch 41 (H = 410) with the entity layer present from height 100.
func read(t *testing.T, s *store.Store, src concentration.Source) error {
	t.Helper()
	return concentration.NewReader(s, src, 10, 100, true).ReadEpoch(context.Background(), "c1", 41)
}

func str(p *string) string {
	if p == nil {
		return "<nil>"
	}
	return *p
}

func i64(t *testing.T, p *int64) int64 {
	t.Helper()
	require.NotNil(t, p)
	return *p
}

func entityByID(t *testing.T, es []store.EntityRow, id string) store.EntityRow {
	t.Helper()
	for _, e := range es {
		if e.EntityID == id {
			return e
		}
	}
	require.Failf(t, "entity not listed", "%s", id)
	return store.EntityRow{}
}

func TestReadsAFullEpoch(t *testing.T) {
	ctx := context.Background()
	s := newStore(t)
	src := demoSource()
	require.NoError(t, read(t, s, src))

	row, powers, err := s.Concentration(ctx, "c1", nil)
	require.NoError(t, err)
	require.NotNil(t, row)
	require.Equal(t, uint64(41), row.Epoch)
	require.Equal(t, int64(410), row.Height)
	require.Equal(t, src.hash, str(row.BlockHash))
	require.Nil(t, row.PowerUnavailable)
	require.Nil(t, row.EntitiesUnavailable)
	require.Equal(t, int64(3), i64(t, row.SetSize))
	require.Equal(t, int64(100_400_000), i64(t, row.TotalPower))
	require.Equal(t, int64(100_400_000), i64(t, row.Top10Power))
	require.Equal(t, int64(1), i64(t, row.NakamotoValidators))
	require.Equal(t, int64(2), i64(t, row.UnitCount))
	require.Equal(t, int64(1), i64(t, row.NakamotoEntities))
	require.Equal(t, int64(2), i64(t, row.EntityCount))
	require.Equal(t, "30000000000000", str(row.EntityCap))
	require.Equal(t, int64(4), i64(t, row.MaxActiveSeats))
	require.NotEmpty(t, row.ReadAt)

	require.Len(t, powers, 3)
	units := map[string]string{}
	ops := map[string]string{}
	for _, p := range powers {
		units[p.ConsAddr] = str(p.Unit)
		ops[p.ConsAddr] = str(p.Operator)
	}
	require.Equal(t, map[string]string{
		consFirst:  "entity:" + operatorEntity,
		consSecond: "entity:" + operatorEntity,
		consThird:  "entity:" + revokedEntity,
	}, units)
	require.Equal(t, map[string]string{consFirst: opFirst, consSecond: opSecond, consThird: opThird}, ops)

	// Only the validator that is ACTIVE and in no attested entity is asked about.
	require.Equal(t, 1, src.called("ValidatorEntityAt"))
	require.Equal(t, 1, src.called("ValidatorEntityAt:"+opThird))

	// Re-reading the same epoch replaces it rather than adding to it.
	require.NoError(t, read(t, s, src))
	_, again, err := s.Concentration(ctx, "c1", nil)
	require.NoError(t, err)
	require.Len(t, again, 3)
}

func TestRevokedEntityWithAChargedSeatIsListed(t *testing.T) {
	ctx := context.Background()
	s := newStore(t)
	src := demoSource()
	require.NoError(t, read(t, s, src))

	row, ents, err := s.Entities(ctx, "c1", nil)
	require.NoError(t, err)
	require.NotNil(t, row)
	require.Len(t, ents, 2)

	rev := entityByID(t, ents, revokedEntity)
	require.Equal(t, "ENTITY_STATUS_REVOKED", rev.Status)
	require.Nil(t, rev.Tokens)
	require.Nil(t, rev.EntityCap)
	require.Nil(t, rev.Room)
	require.Nil(t, rev.Frozen)
	require.Len(t, rev.Seats, 1)
	seat := rev.Seats[0]
	require.Equal(t, opThird, seat.Operator)
	require.Equal(t, "charged_frozen", seat.Role)
	require.Equal(t, "STATUS_ACTIVE", str(seat.RegistryStatus))
	require.Equal(t, "200000000000", str(seat.Tokens))
	require.Equal(t, int64(200_000), i64(t, seat.Power))
	require.Equal(t, "entity demo-third revoked: demo finished", str(seat.FrozenReason))
	require.Equal(t, int64(38), i64(t, seat.FrozenHeight))

	op := entityByID(t, ents, operatorEntity)
	require.Equal(t, "ENTITY_STATUS_ATTESTED", op.Status)
	require.Equal(t, "100200000000000", str(op.Tokens))
	require.Equal(t, "30000000000000", str(op.EntityCap))
	require.Equal(t, int64(31), i64(t, op.AttestedHeight))
	require.Equal(t, int64(2), i64(t, op.ActiveSeats))
	require.Equal(t, int64(4), i64(t, op.MaxActiveSeats))
	require.Len(t, op.Seats, 2)
	for _, st := range op.Seats {
		require.Equal(t, "member", st.Role)
		require.Equal(t, "STATUS_ACTIVE", str(st.RegistryStatus))
	}

	// The stake engine has no record for a revoked entity, so it is never asked.
	require.Zero(t, src.called("EntityStakeAt:"+revokedEntity))
	require.Equal(t, 1, src.called("EntityStakeAt:"+operatorEntity))
}

func TestPrunedPowerWritesAnUnavailableRow(t *testing.T) {
	ctx := context.Background()
	s := newStore(t)
	src := demoSource()
	src.errs["ValidatorSetAt"] = fmt.Errorf("%w: x", chain.ErrHeightUnavailable)
	require.NoError(t, read(t, s, src))

	rows, err := s.ConcentrationSeries(ctx, "c1", 41, 41)
	require.NoError(t, err)
	require.Len(t, rows, 1)
	r := rows[0]
	require.Equal(t, int64(410), r.Height)
	require.Equal(t, concentration.Pruned, str(r.PowerUnavailable))
	require.Equal(t, concentration.Pruned, str(r.EntitiesUnavailable))
	require.Nil(t, r.BlockHash)
	require.Nil(t, r.SetSize)
	require.Nil(t, r.TotalPower)
	require.Nil(t, r.NakamotoValidators)
	require.Nil(t, r.EntityCount)
	require.Nil(t, r.EntityCap)
	require.Equal(t, []string{"ValidatorSetAt"}, src.calls, "nothing else is asked of a pruned height")

	hi, ok, err := s.HighestConcentrationEpoch(ctx, "c1")
	require.NoError(t, err)
	require.True(t, ok)
	require.Equal(t, uint64(41), hi)
}

func TestAFutureHeightIsAnErrorNotARow(t *testing.T) {
	ctx := context.Background()
	s := newStore(t)
	src := demoSource()
	src.errs["ValidatorSetAt"] = fmt.Errorf("%w: x", chain.ErrHeightAhead)
	err := read(t, s, src)
	require.ErrorIs(t, err, chain.ErrHeightAhead)

	_, ok, err := s.HighestConcentrationEpoch(ctx, "c1")
	require.NoError(t, err)
	require.False(t, ok)
}

func TestAPrunedBlockHashIsNull(t *testing.T) {
	ctx := context.Background()
	s := newStore(t)
	src := demoSource()
	src.errs["BlockHashAt"] = fmt.Errorf("%w: x", chain.ErrHeightUnavailable)
	require.NoError(t, read(t, s, src))

	row, _, err := s.Concentration(ctx, "c1", nil)
	require.NoError(t, err)
	require.NotNil(t, row)
	require.Nil(t, row.BlockHash)
	require.Nil(t, row.EntitiesUnavailable)
	require.Equal(t, int64(3), i64(t, row.SetSize))
}

func TestBeforeTheEntityLayerNothingIsAsked(t *testing.T) {
	ctx := context.Background()
	s := newStore(t)
	src := demoSource()
	require.NoError(t, concentration.NewReader(s, src, 10, 500, true).ReadEpoch(ctx, "c1", 41))

	row, powers, err := s.Concentration(ctx, "c1", nil)
	require.NoError(t, err)
	require.NotNil(t, row)
	require.Nil(t, row.PowerUnavailable)
	require.Equal(t, concentration.BeforeEntityLayer, str(row.EntitiesUnavailable))
	require.Equal(t, int64(3), i64(t, row.SetSize))
	require.Equal(t, int64(1), i64(t, row.NakamotoValidators))
	require.Nil(t, row.UnitCount)
	require.Nil(t, row.NakamotoEntities)
	require.Nil(t, row.EntityCount)
	require.Nil(t, row.EntityCap)
	require.Nil(t, row.MaxActiveSeats)

	require.Len(t, powers, 3)
	for _, p := range powers {
		require.Nil(t, p.Unit)
		require.NotNil(t, p.Operator, "operators resolve through x/staking at every height")
	}
	for _, m := range []string{"RegistryValidatorsAt", "EntityCapAt", "MaxActiveSeatsAt", "EntitiesAt", "EntityAt", "EntityStakeAt", "ValidatorEntityAt"} {
		require.Zerof(t, src.called(m), "%s was called", m)
	}
}

func TestNoEntityLayer(t *testing.T) {
	ctx := context.Background()
	s := newStore(t)
	src := demoSource()
	require.NoError(t, concentration.NewReader(s, src, 10, 0, false).ReadEpoch(ctx, "c1", 41))

	row, _, err := s.Concentration(ctx, "c1", nil)
	require.NoError(t, err)
	require.NotNil(t, row)
	require.Equal(t, concentration.NoEntityLayer, str(row.EntitiesUnavailable))
	require.Equal(t, int64(1), i64(t, row.NakamotoValidators))
	require.Zero(t, src.called("EntitiesAt"))
	require.Zero(t, src.called("RegistryValidatorsAt"))
}

func TestAnUnchargedFrozenSeatIsItsOwnUnit(t *testing.T) {
	ctx := context.Background()
	s := newStore(t)
	src := demoSource()
	src.reg[2].Status = "STATUS_UNBONDING"
	src.detail[revokedEntity] = chain.EntityDetail{Members: []string{}, ActiveSeats: 0}
	require.NoError(t, read(t, s, src))

	row, powers, err := s.Concentration(ctx, "c1", nil)
	require.NoError(t, err)
	require.NotNil(t, row)
	require.Nil(t, row.EntitiesUnavailable, "no mismatch")
	require.Zero(t, src.called("ValidatorEntityAt"), "an unbonding validator cannot hold a charged seat")

	units := map[string]string{}
	for _, p := range powers {
		units[p.ConsAddr] = str(p.Unit)
	}
	require.Equal(t, "val:"+opThird, units[consThird])
	require.Equal(t, int64(2), i64(t, row.UnitCount))
	require.Equal(t, int64(1), i64(t, row.EntityCount), "the revoked entity has nothing charged and is not listed")

	_, ents, err := s.Entities(ctx, "c1", nil)
	require.NoError(t, err)
	require.Len(t, ents, 1)
	require.Equal(t, operatorEntity, ents[0].EntityID)
}

func TestAttestedEntityWithNilStake(t *testing.T) {
	ctx := context.Background()
	s := newStore(t)
	src := demoSource()
	src.stake = map[string]*chain.EntityStake{} // the chain answers NotFound
	require.NoError(t, read(t, s, src))

	_, ents, err := s.Entities(ctx, "c1", nil)
	require.NoError(t, err)
	op := entityByID(t, ents, operatorEntity)
	require.Nil(t, op.Tokens)
	require.Nil(t, op.EntityCap)
	require.Nil(t, op.Room)
	require.Nil(t, op.Frozen)
	require.Nil(t, op.MaxActiveSeats)
	require.Len(t, op.Seats, 2)
	require.Equal(t, 1, src.called("EntityStakeAt:"+operatorEntity))
}

func TestFrozenCountMismatchIsRecordedNotFatal(t *testing.T) {
	ctx := context.Background()
	s := newStore(t)
	src := demoSource()
	// Third is a plain validator: nothing is charged to anyone.
	src.ve = map[string]chain.ValidatorEntity{}
	src.detail[revokedEntity] = chain.EntityDetail{Members: []string{}, ActiveSeats: 0}
	src.detail[operatorEntity] = chain.EntityDetail{Members: []string{opFirst, opSecond}, ActiveSeats: 3}
	require.NoError(t, read(t, s, src))

	rows, err := s.ConcentrationSeries(ctx, "c1", 41, 41)
	require.NoError(t, err)
	require.Len(t, rows, 1)
	r := rows[0]
	require.Equal(t, "frozen_count_mismatch: saicon-devnet-operator: chain 1, found 0", str(r.EntitiesUnavailable))
	require.Nil(t, r.PowerUnavailable)
	require.Equal(t, int64(3), i64(t, r.SetSize))
	require.Equal(t, int64(1), i64(t, r.NakamotoValidators))
	require.Nil(t, r.UnitCount)
	require.Nil(t, r.NakamotoEntities)
	require.Nil(t, r.EntityCount)

	_, powers, err := s.Concentration(ctx, "c1", nil)
	require.NoError(t, err)
	require.Len(t, powers, 3)
	for _, p := range powers {
		require.Nil(t, p.Unit)
	}
	e, ents, err := s.Entities(ctx, "c1", nil)
	require.NoError(t, err)
	require.Nil(t, e, "an epoch with unavailable entities has no entity view")
	require.Nil(t, ents)
}

func TestTheFirstMismatchInIdOrderIsReported(t *testing.T) {
	ctx := context.Background()
	s := newStore(t)
	src := demoSource()
	src.ve = map[string]chain.ValidatorEntity{}
	// Both entities disagree; "demo-third" sorts before "saicon-devnet-operator".
	src.detail[operatorEntity] = chain.EntityDetail{Members: []string{opFirst, opSecond}, ActiveSeats: 3}
	require.NoError(t, read(t, s, src))

	rows, err := s.ConcentrationSeries(ctx, "c1", 41, 41)
	require.NoError(t, err)
	require.Equal(t, "frozen_count_mismatch: demo-third: chain 1, found 0", str(rows[0].EntitiesUnavailable))
}

func TestATransientErrorWritesNothing(t *testing.T) {
	ctx := context.Background()
	s := newStore(t)
	src := demoSource()
	boom := errors.New("connection reset")
	src.errs["EntitiesAt"] = boom
	err := read(t, s, src)
	require.ErrorIs(t, err, boom)

	_, ok, err := s.HighestConcentrationEpoch(ctx, "c1")
	require.NoError(t, err)
	require.False(t, ok)
}

func TestATransientErrorLateInTheEntityPartWritesNothing(t *testing.T) {
	ctx := context.Background()
	s := newStore(t)
	src := demoSource()
	boom := errors.New("connection reset")
	src.errs["ValidatorEntityAt"] = boom
	require.ErrorIs(t, read(t, s, src), boom)
	_, ok, err := s.HighestConcentrationEpoch(ctx, "c1")
	require.NoError(t, err)
	require.False(t, ok)
}

func TestAnErrorInTheStakingReadWritesNothing(t *testing.T) {
	ctx := context.Background()
	s := newStore(t)
	src := demoSource()
	boom := errors.New("connection reset")
	src.errs["ValidatorsAt"] = boom
	require.ErrorIs(t, read(t, s, src), boom)
	_, ok, err := s.HighestConcentrationEpoch(ctx, "c1")
	require.NoError(t, err)
	require.False(t, ok)
}

func TestPrunedEntityPart(t *testing.T) {
	ctx := context.Background()
	s := newStore(t)
	src := demoSource()
	src.errs["RegistryValidatorsAt"] = fmt.Errorf("%w: x", chain.ErrHeightUnavailable)
	require.NoError(t, read(t, s, src))

	row, powers, err := s.Concentration(ctx, "c1", nil)
	require.NoError(t, err)
	require.NotNil(t, row)
	require.Equal(t, concentration.Pruned, str(row.EntitiesUnavailable))
	require.Nil(t, row.PowerUnavailable)
	require.Equal(t, int64(3), i64(t, row.SetSize))
	require.Equal(t, int64(1), i64(t, row.NakamotoValidators))
	require.Len(t, powers, 3)
	for _, p := range powers {
		require.Nil(t, p.Unit)
	}
}

func TestPrunedStakingStateMarksTheEntityPart(t *testing.T) {
	ctx := context.Background()
	s := newStore(t)
	src := demoSource()
	src.errs["ValidatorsAt"] = fmt.Errorf("%w: x", chain.ErrHeightUnavailable)
	require.NoError(t, read(t, s, src))

	row, powers, err := s.Concentration(ctx, "c1", nil)
	require.NoError(t, err)
	require.NotNil(t, row)
	require.Nil(t, row.PowerUnavailable)
	require.Equal(t, concentration.Pruned, str(row.EntitiesUnavailable))
	require.Len(t, powers, 3)
	for _, p := range powers {
		require.Nil(t, p.Operator)
		require.Nil(t, p.Unit)
	}
	require.Zero(t, src.called("EntitiesAt"))
}

func TestAKeyStakingDoesNotKnowIsItsOwnConsUnit(t *testing.T) {
	ctx := context.Background()
	s := newStore(t)
	src := demoSource()
	src.vals = src.vals[:2] // the third key has no x/staking validator at H
	src.reg = src.reg[:2]
	src.detail[revokedEntity] = chain.EntityDetail{Members: []string{}, ActiveSeats: 0}
	require.NoError(t, read(t, s, src))

	_, powers, err := s.Concentration(ctx, "c1", nil)
	require.NoError(t, err)
	got := map[string]string{}
	for _, p := range powers {
		got[p.ConsAddr] = str(p.Operator) + "|" + str(p.Unit)
	}
	require.Equal(t, "<nil>|cons:"+consThird, got[consThird])
}

func TestEveryEntityMemberRowIsWrittenOnce(t *testing.T) {
	ctx := context.Background()
	s := newStore(t)
	src := demoSource()
	require.NoError(t, read(t, s, src))
	_, ents, err := s.Entities(ctx, "c1", nil)
	require.NoError(t, err)
	var seen []string
	for _, e := range ents {
		for _, st := range e.Seats {
			seen = append(seen, e.EntityID+"/"+st.Operator)
		}
	}
	sort.Strings(seen)
	require.Equal(t, []string{
		revokedEntity + "/" + opThird,
		operatorEntity + "/" + opSecond, // sorted as strings: valoper106… before valoper128…
		operatorEntity + "/" + opFirst,
	}, seen)
}

// The layer gates are facts about the chain and come before the node's
// retention: an epoch on a chain without the layer, or before it, says so even
// when x/staking's state at the height is pruned.
func TestLayerGatesComeBeforePrunedStaking(t *testing.T) {
	ctx := context.Background()
	for name, tc := range map[string]struct {
		from    int64
		present bool
		want    string
	}{
		"no entity layer":     {0, false, concentration.NoEntityLayer},
		"before entity layer": {500, true, concentration.BeforeEntityLayer},
	} {
		t.Run(name, func(t *testing.T) {
			s := newStore(t)
			src := demoSource()
			src.errs["ValidatorsAt"] = fmt.Errorf("%w: x", chain.ErrHeightUnavailable)
			require.NoError(t, concentration.NewReader(s, src, 10, tc.from, tc.present).ReadEpoch(ctx, "c1", 41))
			row, _, err := s.Concentration(ctx, "c1", nil)
			require.NoError(t, err)
			require.Equal(t, tc.want, str(row.EntitiesUnavailable))
		})
	}
}

// The same validator wears two addresses (saiconvaloper… in x/staking,
// saicon1… elsewhere); the grouping joins on the bytes underneath, so a member
// list in the account form still groups the validator under its entity.
func TestMembersJoinByAddressBytes(t *testing.T) {
	ctx := context.Background()
	s := newStore(t)
	src := demoSource()
	src.detail[operatorEntity] = chain.EntityDetail{Members: []string{regFirst, regSecond}, ActiveSeats: 2}
	require.NoError(t, read(t, s, src))

	row, powers, err := s.Concentration(ctx, "c1", nil)
	require.NoError(t, err)
	require.Nil(t, row.EntitiesUnavailable)
	units := map[string]string{}
	for _, p := range powers {
		units[p.ConsAddr] = str(p.Unit)
	}
	require.Equal(t, "entity:"+operatorEntity, units[consFirst])
	require.Equal(t, "entity:"+operatorEntity, units[consSecond])
	require.Equal(t, 0, src.called("ValidatorEntityAt:"+opFirst), "a member is not scanned for a frozen seat")

	_, ents, err := s.Entities(ctx, "c1", nil)
	require.NoError(t, err)
	for _, st := range entityByID(t, ents, operatorEntity).Seats {
		require.NotNil(t, st.Tokens, "%s: tokens join by address bytes", st.Operator)
		require.NotNil(t, st.Power, "%s: power joins by address bytes", st.Operator)
	}
}

// The scan finds a frozen seat charged to an entity the chain does not list at
// all: the chain counts zero seats for it, the scan one.
func TestAChargedSeatOfAnUnlistedEntityIsAMismatch(t *testing.T) {
	ctx := context.Background()
	s := newStore(t)
	src := demoSource()
	src.ve[opThird] = chain.ValidatorEntity{Frozen: &chain.FrozenSeat{FormerEntityID: "ghost", Reason: "r", Height: 9}}
	src.detail[revokedEntity] = chain.EntityDetail{Members: []string{}, ActiveSeats: 0}
	require.NoError(t, read(t, s, src))
	rows, err := s.ConcentrationSeries(ctx, "c1", 41, 41)
	require.NoError(t, err)
	require.Equal(t, "frozen_count_mismatch: ghost: chain 0, found 1", str(rows[0].EntitiesUnavailable))
}

// The other direction from TestFrozenCountMismatchIsRecordedNotFatal: the scan
// finds more charged seats than the chain counts.
func TestFoundMoreThanTheChainCountsIsAMismatch(t *testing.T) {
	ctx := context.Background()
	s := newStore(t)
	src := demoSource()
	src.detail[revokedEntity] = chain.EntityDetail{Members: []string{}, ActiveSeats: 0}
	require.NoError(t, read(t, s, src))
	rows, err := s.ConcentrationSeries(ctx, "c1", 41, 41)
	require.NoError(t, err)
	require.Equal(t, "frozen_count_mismatch: demo-third: chain 0, found 1", str(rows[0].EntitiesUnavailable))
}

// writeNoLayer measures epoch 41 as a chain without the entity layer would.
func writeNoLayer(t *testing.T, s *store.Store, src concentration.Source) {
	t.Helper()
	require.NoError(t, concentration.NewReader(s, src, 10, 0, false).ReadEpoch(context.Background(), "c1", 41))
}

// Once the layer is known, an epoch written no_entity_layer is read again
// through the normal write path and gains its entity figures.
func TestRereadGainsTheEntityPart(t *testing.T) {
	ctx := context.Background()
	s := newStore(t)
	src := demoSource()
	writeNoLayer(t, s, src)

	r := concentration.NewReader(s, src, 10, 0, false)
	r.SetEntityLayer(100, true)
	require.NoError(t, r.RereadEntities(ctx, "c1", 41))

	row, ents, err := s.Entities(ctx, "c1", ptr(uint64(41)))
	require.NoError(t, err)
	require.NotNil(t, row)
	require.Nil(t, row.EntitiesUnavailable)
	require.Equal(t, int64(2), i64(t, row.UnitCount))
	require.Len(t, ents, 2)
}

// A height the node has since pruned cannot be re-read; the power figures
// already stored stay, and only the entity code changes to pruned.
func TestRereadOfAPrunedHeightKeepsThePowerFigures(t *testing.T) {
	ctx := context.Background()
	for _, method := range []string{"ValidatorSetAt", "ValidatorsAt", "RegistryValidatorsAt"} {
		t.Run(method, func(t *testing.T) {
			s := newStore(t)
			src := demoSource()
			writeNoLayer(t, s, src)
			src.errs[method] = fmt.Errorf("%w: x", chain.ErrHeightUnavailable)

			r := concentration.NewReader(s, src, 10, 100, true)
			require.NoError(t, r.RereadEntities(ctx, "c1", 41))
			row, powers, err := s.Concentration(ctx, "c1", ptr(uint64(41)))
			require.NoError(t, err)
			require.NotNil(t, row)
			require.Equal(t, concentration.Pruned, str(row.EntitiesUnavailable))
			require.Equal(t, int64(100_400_000), i64(t, row.TotalPower))
			require.Equal(t, src.hash, str(row.BlockHash))
			require.Len(t, powers, 3)
			for _, p := range powers {
				require.NotNil(t, p.Operator, "operators read earlier are kept")
			}
		})
	}
}

// A transient error during a re-read writes nothing: the epoch keeps its row.
func TestRereadErrorKeepsTheOldRow(t *testing.T) {
	ctx := context.Background()
	s := newStore(t)
	src := demoSource()
	writeNoLayer(t, s, src)
	boom := errors.New("connection reset")
	src.errs["EntityStakeAt"] = boom
	r := concentration.NewReader(s, src, 10, 100, true)
	require.ErrorIs(t, r.RereadEntities(ctx, "c1", 41), boom)
	row, _, err := s.Concentration(ctx, "c1", ptr(uint64(41)))
	require.NoError(t, err)
	require.Equal(t, concentration.NoEntityLayer, str(row.EntitiesUnavailable))
}

// A re-read is only for an epoch the layer covers; anything else is refused
// rather than rewritten with the same code again.
func TestRereadRefusesAnEpochTheLayerDoesNotCover(t *testing.T) {
	ctx := context.Background()
	s := newStore(t)
	src := demoSource()
	writeNoLayer(t, s, src)
	require.Error(t, concentration.NewReader(s, src, 10, 0, false).RereadEntities(ctx, "c1", 41))
	require.Error(t, concentration.NewReader(s, src, 10, 500, true).RereadEntities(ctx, "c1", 41))
}

func ptr[T any](v T) *T { return &v }

// The node may have dropped the block (and its hash) while still answering the
// set and app state; the re-read then keeps the hash already stored, which the
// reset check depends on.
func TestRereadKeepsTheStoredHash(t *testing.T) {
	ctx := context.Background()
	s := newStore(t)
	src := demoSource()
	writeNoLayer(t, s, src)
	src.errs["BlockHashAt"] = fmt.Errorf("%w: x", chain.ErrHeightUnavailable)

	require.NoError(t, concentration.NewReader(s, src, 10, 100, true).RereadEntities(ctx, "c1", 41))
	row, _, err := s.Concentration(ctx, "c1", ptr(uint64(41)))
	require.NoError(t, err)
	require.Nil(t, row.EntitiesUnavailable, "the entity part was read")
	require.Equal(t, src.hash, str(row.BlockHash))
}

// A stake answer whose tokens are not an integer is a bad answer, not data:
// the epoch is retried and nothing is stored.
func TestNonIntegerStakeTokensAreAnError(t *testing.T) {
	ctx := context.Background()
	for _, bad := range []string{"", "12x", "1.5", "-"} {
		t.Run(fmt.Sprintf("%q", bad), func(t *testing.T) {
			s := newStore(t)
			src := demoSource()
			src.stake[operatorEntity].Tokens = bad
			require.Error(t, read(t, s, src))
			_, ok, err := s.HighestConcentrationEpoch(ctx, "c1")
			require.NoError(t, err)
			require.False(t, ok, "nothing was written")
		})
	}
}
