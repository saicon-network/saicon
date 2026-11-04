package keeper_test

import (
	"bytes"
	"sort"
	"strings"
	"testing"
	"time"

	"cosmossdk.io/math"
	sdk "github.com/cosmos/cosmos-sdk/types"
	"github.com/cosmos/cosmos-sdk/types/query"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/saicon-network/saicon/chain/x/compliance/keeper"
	"github.com/saicon-network/saicon/chain/x/compliance/types"
)

// queryEnv is the sanction fixture with the query server on top.
type queryEnv struct {
	*sanctionEnv
	qs types.QueryServer
}

// newQueryEnvOn builds the sanction fixture and the query server on its keeper.
func newQueryEnvOn(t *testing.T) *queryEnv {
	t.Helper()
	e := newSanctionEnv(t)
	return &queryEnv{sanctionEnv: e, qs: keeper.NewQueryServerImpl(e.keeper)}
}

func requireCode(t *testing.T, err error, code codes.Code) {
	t.Helper()
	require.Error(t, err)
	require.Equal(t, code, status.Code(err), "error: %v", err)
}

func TestQueryOffence(t *testing.T) {
	e := newQueryEnvOn(t)

	res, err := e.qs.Offence(e.ctx, &types.QueryOffenceRequest{EvidenceHash: hexes(e.ha)[0]})
	require.NoError(t, err)
	want, err := e.keeper.Offences.Get(e.ctx, e.ha)
	require.NoError(t, err)
	require.Equal(t, want, res.Offence)
	require.Equal(t, "acme", res.Offence.EntityId)

	_, err = e.qs.Offence(e.ctx, &types.QueryOffenceRequest{EvidenceHash: strings.Repeat("AB", 32)})
	requireCode(t, err, codes.NotFound)

	for name, bad := range map[string]string{
		"short":     "ABC",
		"lowercase": strings.ToLower(hexes(e.ha)[0]),
		"not hex":   strings.Repeat("ZZ", 32),
		"empty":     "",
	} {
		t.Run(name, func(t *testing.T) {
			_, err := e.qs.Offence(e.ctx, &types.QueryOffenceRequest{EvidenceHash: bad})
			requireCode(t, err, codes.InvalidArgument)
		})
	}

	_, err = e.qs.Offence(e.ctx, nil)
	requireCode(t, err, codes.InvalidArgument)
}

func TestQueryOffencesPaginateInHashOrder(t *testing.T) {
	e := newQueryEnvOn(t)
	c := e.newActor(3)
	hc := e.putOffence(t, c, 10, "acme", 10, t0, dec010)
	all := [][]byte{e.ha, e.hb, hc}
	sort.Slice(all, func(i, j int) bool { return bytes.Compare(all[i], all[j]) < 0 })

	res, err := e.qs.Offences(e.ctx, &types.QueryOffencesRequest{})
	require.NoError(t, err)
	require.Len(t, res.Offences, 3)
	for i, h := range all {
		require.Equal(t, types.FormatEvidenceHash(h), res.Offences[i].EvidenceHash)
	}

	var got []string
	var key []byte
	for page := 0; page < 3; page++ {
		res, err := e.qs.Offences(e.ctx, &types.QueryOffencesRequest{
			Pagination: &query.PageRequest{Limit: 1, Key: key},
		})
		require.NoError(t, err)
		require.Len(t, res.Offences, 1)
		got = append(got, res.Offences[0].EvidenceHash)
		key = res.Pagination.NextKey
		if page < 2 {
			require.NotEmpty(t, key, "limit 1 leaves a next key")
		}
	}
	require.Empty(t, key, "the last page has no next key")
	require.Equal(t, hexes(all...), got)

	_, err = e.qs.Offences(e.ctx, nil)
	requireCode(t, err, codes.InvalidArgument)
}

func TestQueryCase(t *testing.T) {
	e := newQueryEnvOn(t)

	res, err := e.qs.Case(e.ctx, &types.QueryCaseRequest{EntityId: "acme"})
	require.NoError(t, err)
	want, err := e.keeper.CaseOf(e.ctx, "acme")
	require.NoError(t, err)
	require.Equal(t, want, res.Case)
	require.True(t, res.Case.Armed)
	require.Equal(t, t0, res.Case.ArmedTime.UTC())
	require.ElementsMatch(t, hexes(e.ha, e.hb), res.Case.UnfiredHashes)
	require.Len(t, res.Case.Offences, 2)

	res, err = e.qs.Case(e.ctx, &types.QueryCaseRequest{EntityId: "ghost"})
	require.NoError(t, err)
	require.Equal(t, "ghost", res.Case.EntityId)
	require.False(t, res.Case.Armed)
	require.Empty(t, res.Case.Offences)
	require.Empty(t, res.Case.UnfiredHashes)
	require.Empty(t, res.Case.Validators)

	_, err = e.qs.Case(e.ctx, nil)
	requireCode(t, err, codes.InvalidArgument)
}

func TestQueryArmedCases(t *testing.T) {
	e := newQueryEnvOn(t)

	// "zeta" is armed and written after "acme": the answer is still by entity id.
	z1, z2 := e.newActor(3), e.newActor(4)
	e.putOffence(t, z1, 10, "zeta", 11, t0, dec010)
	e.putOffence(t, z2, 10, "zeta", 11, t0, dec010)

	// "done" is armed, but both of its offences are fired.
	d1, d2 := e.newActor(5), e.newActor(6)
	h1 := e.putOffence(t, d1, 10, "done", 12, t0, dec010)
	h2 := e.putOffence(t, d2, 10, "done", 12, t0, dec010)
	for _, h := range [][]byte{h1, h2} {
		o, err := e.keeper.Offences.Get(e.ctx, h)
		require.NoError(t, err)
		o.SanctionId = 1
		require.NoError(t, e.keeper.Offences.Set(e.ctx, h, o))
	}

	// "half" is armed with one fired and one unfired offence: listed.
	f1, f2 := e.newActor(7), e.newActor(8)
	hf1 := e.putOffence(t, f1, 10, "half", 13, t0, dec010)
	e.putOffence(t, f2, 10, "half", 13, t0, dec010)
	o, err := e.keeper.Offences.Get(e.ctx, hf1)
	require.NoError(t, err)
	o.SanctionId = 1
	require.NoError(t, e.keeper.Offences.Set(e.ctx, hf1, o))

	// "solo" has one offence: not armed.
	s := e.newActor(9)
	e.putOffence(t, s, 10, "solo", 14, t0, dec010)

	res, err := e.qs.ArmedCases(e.ctx, &types.QueryArmedCasesRequest{})
	require.NoError(t, err)
	ids := make([]string, len(res.Cases))
	for i, c := range res.Cases {
		ids[i] = c.EntityId
		require.True(t, c.Armed)
		require.NotEmpty(t, c.UnfiredHashes)
	}
	require.Equal(t, []string{"acme", "half", "zeta"}, ids)

	_, err = e.qs.ArmedCases(e.ctx, nil)
	requireCode(t, err, codes.InvalidArgument)
}

func TestQueryArmedCasesIsEmptyWithoutOffences(t *testing.T) {
	f := initFixture(t)
	qs := keeper.NewQueryServerImpl(f.keeper)
	res, err := qs.ArmedCases(f.ctx, &types.QueryArmedCasesRequest{})
	require.NoError(t, err)
	require.Empty(t, res.Cases)
}

func TestQuerySanctionAndSanctions(t *testing.T) {
	e := newQueryEnvOn(t)
	_, err := e.fire(e.ha)
	require.NoError(t, err)
	_, err = e.fire(e.hb)
	require.NoError(t, err)

	res, err := e.qs.Sanction(e.ctx, &types.QuerySanctionRequest{Id: 2})
	require.NoError(t, err)
	want, err := e.keeper.Sanctions.Get(e.ctx, 2)
	require.NoError(t, err)
	require.Equal(t, want, res.Sanction)
	require.Equal(t, uint64(2), res.Sanction.Id)

	for _, id := range []uint64{0, 3} {
		_, err = e.qs.Sanction(e.ctx, &types.QuerySanctionRequest{Id: id})
		requireCode(t, err, codes.NotFound)
	}

	all, err := e.qs.Sanctions(e.ctx, &types.QuerySanctionsRequest{})
	require.NoError(t, err)
	require.Len(t, all.Sanctions, 2)
	require.Equal(t, uint64(1), all.Sanctions[0].Id)
	require.Equal(t, uint64(2), all.Sanctions[1].Id)

	page, err := e.qs.Sanctions(e.ctx, &types.QuerySanctionsRequest{Pagination: &query.PageRequest{Limit: 1}})
	require.NoError(t, err)
	require.Len(t, page.Sanctions, 1)
	require.Equal(t, uint64(1), page.Sanctions[0].Id)
	require.NotEmpty(t, page.Pagination.NextKey)
	next, err := e.qs.Sanctions(e.ctx, &types.QuerySanctionsRequest{Pagination: &query.PageRequest{Limit: 1, Key: page.Pagination.NextKey}})
	require.NoError(t, err)
	require.Equal(t, uint64(2), next.Sanctions[0].Id)

	_, err = e.qs.Sanction(e.ctx, nil)
	requireCode(t, err, codes.InvalidArgument)
	_, err = e.qs.Sanctions(e.ctx, nil)
	requireCode(t, err, codes.InvalidArgument)
}

func (e *queryEnv) preview(entity string, hashes []string, fraction string) (*types.QuerySanctionPreviewResponse, error) {
	return e.qs.SanctionPreview(e.ctx, &types.QuerySanctionPreviewRequest{
		EntityId: entity, EvidenceHashes: hashes, Fraction: fraction,
	})
}

func TestPreviewReturnsThePlanTheFiringUses(t *testing.T) {
	e := newQueryEnvOn(t)
	hashes := hexes(e.hb, e.ha)

	res, err := e.preview("acme", hashes, dec030.String())
	require.NoError(t, err)
	require.True(t, res.Fireable)
	require.Empty(t, res.Reason)

	want, err := e.plan("acme", hashes, dec030)
	require.NoError(t, err)
	require.Equal(t, want, res.Plan)
	require.Len(t, res.Plan.Targets, 2)
	require.Equal(t, t0.Add(7*day), res.Plan.EarliestExecutionTime.UTC())

	// The plan it shows is what the firing records.
	_, err = e.fire(e.ha, e.hb)
	require.NoError(t, err)
	s, err := e.keeper.Sanctions.Get(e.ctx, 1)
	require.NoError(t, err)
	require.Len(t, s.Targets, len(res.Plan.Targets))
	for i, tg := range s.Targets {
		require.Equal(t, res.Plan.Targets[i].Validator, tg.Validator)
		require.Equal(t, res.Plan.Targets[i].ExtraFraction, tg.ExtraFraction)
		require.Equal(t, res.Plan.Targets[i].Outcome, tg.Outcome)
	}
}

func TestPreviewOfARefusedCaseIsNotAnError(t *testing.T) {
	cases := []struct {
		name   string
		setup  func(e *queryEnv) (string, []string, string)
		want   error
		reason string
	}{
		{"appeal window open", func(e *queryEnv) (string, []string, string) {
			e.atTime(t0.Add(7*day - time.Second))
			return "acme", hexes(e.ha, e.hb), dec030.String()
		}, types.ErrAppealWindowOpen, "appeal window still open"},
		{"fraction differs from the parameter", func(e *queryEnv) (string, []string, string) {
			return "acme", hexes(e.ha, e.hb), "0.40"
		}, types.ErrFractionMismatch, "does not equal the governed"},
		{"entity not armed", func(e *queryEnv) (string, []string, string) {
			c := e.newActor(3)
			hc := e.putOffence(e.t, c, 10, "solo", 10, t0, dec010)
			return "solo", hexes(hc), dec030.String()
		}, types.ErrNotArmed, "entity is not armed"},
		{"unknown offence", func(e *queryEnv) (string, []string, string) {
			return "acme", []string{strings.Repeat("AB", 32)}, dec030.String()
		}, types.ErrUnknownOffence, "unknown offence"},
		{"invariant broken", func(e *queryEnv) (string, []string, string) {
			delete(e.evidence.hashes, string(e.hb))
			return "acme", hexes(e.ha), dec030.String()
		}, types.ErrCoordinatedInvariant, "invariant V-15 broken"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			e := newQueryEnvOn(t)
			entity, hashes, fraction := tc.setup(e)
			res, err := e.preview(entity, hashes, fraction)
			require.NoError(t, err, "a refusal is an answer, not a gRPC error")
			require.False(t, res.Fireable)
			require.Contains(t, res.Reason, tc.reason)
			require.Empty(t, res.Plan.Targets)

			// The reason is the firing's own error text.
			_, planErr := e.plan(entity, hashes, mustDec(t, fraction))
			require.ErrorIs(t, planErr, tc.want)
			require.Equal(t, planErr.Error(), res.Reason)
		})
	}
}

func mustDec(t *testing.T, s string) math.LegacyDec {
	t.Helper()
	d, err := math.LegacyNewDecFromStr(s)
	require.NoError(t, err)
	return d
}

func TestPreviewParsesItsInputsFirst(t *testing.T) {
	e := newQueryEnvOn(t)
	good := hexes(e.ha, e.hb)
	cases := []struct {
		name     string
		entity   string
		hashes   []string
		fraction string
	}{
		{"malformed hash", "acme", []string{"ABC"}, "0.30"},
		{"lowercase hash", "acme", []string{strings.ToLower(good[0])}, "0.30"},
		{"one bad among good", "acme", []string{good[0], "ZZ"}, "0.30"},
		{"repeated hash", "acme", []string{good[0], good[0]}, "0.30"},
		{"no hashes", "acme", nil, "0.30"},
		{"too many hashes", "acme", make([]string, types.MaxEvidenceHashesPerSanction+1), "0.30"},
		{"malformed entity id", "Acme!", good, "0.30"},
		{"empty entity id", "", good, "0.30"},
		{"fraction not a number", "acme", good, "thirty"},
		{"fraction empty", "acme", good, ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := e.preview(tc.entity, tc.hashes, tc.fraction)
			requireCode(t, err, codes.InvalidArgument)
		})
	}

	_, err := e.qs.SanctionPreview(e.ctx, nil)
	requireCode(t, err, codes.InvalidArgument)
}

func TestPreviewWithTheFractionOutOfBoundsIsARefusalNotAnError(t *testing.T) {
	e := newQueryEnvOn(t)
	for _, f := range []string{"0.05", "-0.30", "1.50"} {
		res, err := e.preview("acme", hexes(e.ha), f)
		require.NoError(t, err)
		require.False(t, res.Fireable, f)
		require.Contains(t, res.Reason, "out of bounds", f)
	}
}

// complianceDump is every compliance collection, read through the keeper.
func complianceDump(t *testing.T, e *sanctionEnv) []any {
	t.Helper()
	var out []any
	p, err := e.keeper.Params.Get(e.ctx)
	require.NoError(t, err)
	out = append(out, p)

	cs, err := e.keeper.ComplianceState.Iterate(e.ctx, nil)
	require.NoError(t, err)
	kvs, err := cs.KeyValues()
	require.NoError(t, err)
	out = append(out, kvs)

	os, err := e.keeper.Offences.Iterate(e.ctx, nil)
	require.NoError(t, err)
	okvs, err := os.KeyValues()
	require.NoError(t, err)
	out = append(out, okvs)

	idx, err := e.keeper.OffencesByEntity.Iterate(e.ctx, nil)
	require.NoError(t, err)
	keys, err := idx.Keys()
	require.NoError(t, err)
	out = append(out, keys)

	ss, err := e.keeper.Sanctions.Iterate(e.ctx, nil)
	require.NoError(t, err)
	skvs, err := ss.KeyValues()
	require.NoError(t, err)
	out = append(out, skvs)

	seq, err := e.keeper.SanctionSeq.Peek(e.ctx)
	require.NoError(t, err)
	out = append(out, seq)
	return out
}

func TestPreviewWritesNothing(t *testing.T) {
	for _, name := range []string{"fireable", "refused"} {
		t.Run(name, func(t *testing.T) {
			e := newQueryEnvOn(t)
			if name == "refused" {
				e.atTime(t0.Add(day))
			}
			before := complianceDump(t, e.sanctionEnv)
			eventsBefore := len(sdk.UnwrapSDKContext(e.ctx).EventManager().Events())

			res, err := e.preview("acme", hexes(e.ha, e.hb), dec030.String())
			require.NoError(t, err)
			require.Equal(t, name == "fireable", res.Fireable)

			require.Equal(t, before, complianceDump(t, e.sanctionEnv))
			require.Len(t, sdk.UnwrapSDKContext(e.ctx).EventManager().Events(), eventsBefore, "a preview emits no event")
			require.Empty(t, e.staking.slashes, "a preview slashes nothing")
			require.Zero(t, e.window.opened, "a preview does not open the slash window")
			require.Empty(t, e.participation.zeroed, "a preview zeroes no reputation")
			_, found, err := e.keeper.GetCompliance(e.ctx, e.a.op)
			require.NoError(t, err)
			require.False(t, found, "a preview sets no compliance state")
		})
	}
}

// A read that fails outside this module says nothing about whether the firing
// would execute: the preview answers codes.Internal, not fireable: false.
func TestPreviewOfAFailedReadIsAnInternalError(t *testing.T) {
	e := newQueryEnvOn(t)
	e.staking.failCons[string(e.b.cons)] = true

	_, err := e.plan("acme", hexes(e.ha, e.hb), dec030)
	require.ErrorContains(t, err, "injected lookup failure", "the plan itself fails")
	res, err := e.preview("acme", hexes(e.ha, e.hb), dec030.String())
	requireCode(t, err, codes.Internal)
	require.Nil(t, res)

	// A registered refusal is still an answer.
	e.atTime(t0.Add(day))
	res, err = e.preview("acme", hexes(e.ha, e.hb), dec030.String())
	require.NoError(t, err)
	require.False(t, res.Fireable)
	require.Contains(t, res.Reason, types.ErrAppealWindowOpen.Error())
}
