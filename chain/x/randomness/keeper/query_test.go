package keeper_test

import (
	"testing"

	"github.com/cosmos/cosmos-sdk/types/query"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/saicon-network/saicon/chain/x/randomness/keeper"
	"github.com/saicon-network/saicon/chain/x/randomness/types"
)

func TestQueryParams(t *testing.T) {
	f := newFixture(t)
	q := keeper.NewQueryServerImpl(f.k)
	res, err := q.Params(f.ctx, &types.QueryParamsRequest{})
	require.NoError(t, err)
	require.Equal(t, types.Params{RotationEpochs: 2, WindowBlocks: 10}, res.Params)
	_, err = q.Params(f.ctx, nil)
	require.Equal(t, codes.InvalidArgument, status.Code(err))
}

func TestQueryRound(t *testing.T) {
	rt := newRoundTwo(t)
	q := keeper.NewQueryServerImpl(rt.k)
	_, err := rt.k.SubmitVrf(rt.at(31), acc(1), 2, rt.current(rt.k1))
	require.NoError(t, err)

	res, err := q.Round(rt.ctx, &types.QueryRoundRequest{})
	require.NoError(t, err)
	require.Equal(t, rt.round(), res.Round)
	require.Len(t, res.Pool, 2)
	require.Equal(t, "acme", res.Pool[0].EntityId)
	require.Equal(t, val(1).String(), res.Pool[0].Validator)
	require.Equal(t, "beta", res.Pool[1].EntityId)
	require.Len(t, res.Submissions, 1)
	require.Equal(t, "acme", res.Submissions[0].EntityId)
	require.EqualValues(t, 2, res.Required, "ceil(2*2/3)")
}

func TestQueryDraw(t *testing.T) {
	f := newFixture(t)
	q := keeper.NewQueryServerImpl(f.k)
	f.run(1, 20)

	res, err := q.Draw(f.ctx, &types.QueryDrawRequest{Round: 1})
	require.NoError(t, err)
	require.Equal(t, f.draw(1), res.Draw)

	_, err = q.Draw(f.ctx, &types.QueryDrawRequest{Round: 99})
	require.Equal(t, codes.NotFound, status.Code(err))
	_, err = q.Draw(f.ctx, nil)
	require.Equal(t, codes.InvalidArgument, status.Code(err))
}

func TestQueryDraws(t *testing.T) {
	f := newFixture(t)
	q := keeper.NewQueryServerImpl(f.k)
	f.run(1, 60) // rounds 1, 2 and 3 are closed
	roundsOf := func(ds []types.Draw) []uint64 {
		var out []uint64
		for _, d := range ds {
			out = append(out, d.Round)
		}
		return out
	}

	res, err := q.Draws(f.ctx, &types.QueryDrawsRequest{})
	require.NoError(t, err)
	require.Equal(t, []uint64{1, 2, 3}, roundsOf(res.Draws))

	res, err = q.Draws(f.ctx, &types.QueryDrawsRequest{Pagination: &query.PageRequest{Reverse: true}})
	require.NoError(t, err)
	require.Equal(t, []uint64{3, 2, 1}, roundsOf(res.Draws), "newest first")

	page, err := q.Draws(f.ctx, &types.QueryDrawsRequest{Pagination: &query.PageRequest{Limit: 2, Reverse: true}})
	require.NoError(t, err)
	require.Equal(t, []uint64{3, 2}, roundsOf(page.Draws))
	require.NotEmpty(t, page.Pagination.NextKey)
	rest, err := q.Draws(f.ctx, &types.QueryDrawsRequest{
		Pagination: &query.PageRequest{Key: page.Pagination.NextKey, Limit: 2, Reverse: true},
	})
	require.NoError(t, err)
	require.Equal(t, []uint64{1}, roundsOf(rest.Draws))
}

func TestQueryLatestDraw(t *testing.T) {
	rt := newRoundTwo(t)
	q := keeper.NewQueryServerImpl(rt.k)

	// Round 1 closed SKIPPED at 20: nobody was keyed for its cutoff of 0.
	res, err := q.LatestDraw(rt.ctx, &types.QueryLatestDrawRequest{})
	require.NoError(t, err)
	require.EqualValues(t, 1, res.Latest.Round)
	require.Nil(t, res.LatestDrawn, "no round has been drawn yet")

	empty := newFixture(t)
	res, err = keeper.NewQueryServerImpl(empty.k).LatestDraw(empty.ctx, &types.QueryLatestDrawRequest{})
	require.NoError(t, err)
	require.Nil(t, res.Latest)
	require.Nil(t, res.LatestDrawn)

	// Both entities submit in round 2 (quorum 2 of 2): DRAWN. Round 3 is skipped.
	_, err = rt.k.SubmitVrf(rt.at(31), acc(1), 2, rt.current(rt.k1))
	require.NoError(t, err)
	_, err = rt.k.SubmitVrf(rt.at(31), acc(2), 2, rt.current(rt.k2))
	require.NoError(t, err)
	rt.run(30, 60)

	res, err = q.LatestDraw(rt.ctx, &types.QueryLatestDrawRequest{})
	require.NoError(t, err)
	require.EqualValues(t, 3, res.Latest.Round)
	require.Equal(t, types.DrawOutcome_DRAW_OUTCOME_SKIPPED, res.Latest.Outcome)
	require.NotNil(t, res.LatestDrawn)
	require.EqualValues(t, 2, res.LatestDrawn.Round)
	require.Equal(t, types.DrawOutcome_DRAW_OUTCOME_DRAWN, res.LatestDrawn.Outcome)
}
