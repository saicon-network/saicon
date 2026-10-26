package epoch_test

import (
	"context"
	"errors"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/saicon-network/saicon/indexer/internal/chain"
	"github.com/saicon-network/saicon/indexer/internal/epoch"
	"github.com/saicon-network/saicon/indexer/internal/store"
)

type fakeREST struct {
	vals   []chain.Validator
	reg    []chain.RegistryEntry
	ep     map[string]*chain.EP
	snap   map[string]*chain.Snapshot
	reward map[string]*chain.Reward
	compl  map[string]*bool

	// epErr, keyed by operator, simulates a node error partway through a pass:
	// ReadEpoch calls EP first for each validator, so failing it for one
	// operator is the cheapest way to reproduce a real REST failure landing
	// after some validators' rows are already written and before others'.
	epErr map[string]error
}

func (f *fakeREST) Validators(context.Context) ([]chain.Validator, error) { return f.vals, nil }
func (f *fakeREST) RegistryValidators(context.Context) ([]chain.RegistryEntry, error) {
	return f.reg, nil
}
func (f *fakeREST) EP(_ context.Context, op string) (*chain.EP, error) {
	if err := f.epErr[op]; err != nil {
		return nil, err
	}
	return f.ep[op], nil
}
func (f *fakeREST) Snapshot(_ context.Context, op string, _ uint64) (*chain.Snapshot, error) {
	return f.snap[op], nil
}
func (f *fakeREST) RewardSnapshot(_ context.Context, op string, _ uint64) (*chain.Reward, error) {
	return f.reward[op], nil
}
func (f *fakeREST) Compliance(_ context.Context, op string) (*bool, error) { return f.compl[op], nil }

func newStore(t *testing.T) *store.Store {
	t.Helper()
	s, err := store.Open(filepath.Join(t.TempDir(), "e.db"))
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, s.Close()) })
	require.NoError(t, s.SetChain(context.Background(), "c1", time.Now()))
	return s
}

// The chain's silence must reach the database as NULL. Writing 0 would turn
// "no record" into "measured zero" — two different facts, and the second is a
// claim we have no basis for.
func TestAbsentRecordsBecomeNullNotZero(t *testing.T) {
	ctx := context.Background()
	s := newStore(t)

	// A real, decodable bech32 operator address (from the localnet, see
	// addr_test.go) — the brief's placeholder "saiconvaloper1a" is too short
	// for chain.AddrKey's checksum-trailer check and fails to decode at all.
	const testOperator = "saiconvaloper1yh0ceztycl23zyuy67776u5dtfjawnrfytvjsd"

	f := &fakeREST{
		vals:   []chain.Validator{{Operator: testOperator, Moniker: "one", Status: "BOND_STATUS_BONDED", Tokens: "100"}},
		reg:    []chain.RegistryEntry{{Operator: testOperator, Status: "STATUS_ACTIVE"}},
		ep:     map[string]*chain.EP{},       // no EP record
		snap:   map[string]*chain.Snapshot{}, // no snapshot
		reward: map[string]*chain.Reward{},
		compl:  map[string]*bool{},
	}
	// No blocks were ever recorded for "c1", so the follower has not covered
	// epoch 7 regardless of epochLen: proposals must stay NULL too, not just
	// the fields this test asserts on directly.
	require.NoError(t, epoch.New(s, f, 100).ReadEpoch(ctx, "c1", 7))

	var score, reward any
	require.NoError(t, s.DB().QueryRowContext(ctx,
		`SELECT computed_score, reward_amount FROM epoch_snapshots WHERE epoch=7`).Scan(&score, &reward))
	require.Nil(t, score, "an absent snapshot is NULL, not an empty string or zero")
	require.Nil(t, reward, "an absent reward is NULL")

	var ep any
	require.NoError(t, s.DB().QueryRowContext(ctx,
		`SELECT accumulated_ep FROM validators WHERE operator=?`, testOperator).Scan(&ep))
	require.Nil(t, ep)

	var proposals any
	require.NoError(t, s.DB().QueryRowContext(ctx,
		`SELECT proposals FROM epoch_snapshots WHERE epoch=7 AND operator=?`, testOperator).Scan(&proposals))
	require.Nil(t, proposals, "the follower has not covered this epoch: proposals must stay NULL, not 0")
}

func TestProposalsAreAttachedToTheOperator(t *testing.T) {
	ctx := context.Background()
	s := newStore(t)

	const (
		testOperator = "saiconvaloper1yh0ceztycl23zyuy67776u5dtfjawnrfytvjsd"
		// The registry names the SAME validator in the account form. If the join
		// were on the string instead of chain.AddrKey this fixture would stop
		// matching and the registry fields would come out empty — which is the
		// whole point of this test.
		testAccountForm = "saicon1yh0ceztycl23zyuy67776u5dtfjawnrfuvlyfs"
	)

	// epochLen=4 and 4 recorded heights land exactly on the epoch boundary
	// (indexed height 4 == epoch 1's last height with epochLen 4), so the
	// coverage gate in ReadEpoch is satisfied at the edge, not with slack.
	const epochLen = 4
	for h := int64(1); h <= 4; h++ {
		require.NoError(t, s.RecordBlock(ctx, "c1", h, 1, "CONSHEX"))
	}

	f := &fakeREST{
		vals: []chain.Validator{{Operator: testOperator, ConsensusPubKey: "CONSHEX", Status: "BOND_STATUS_BONDED"}},
		reg:  []chain.RegistryEntry{{Operator: testAccountForm, Status: "STATUS_ACTIVE"}},
		ep:   map[string]*chain.EP{}, snap: map[string]*chain.Snapshot{},
		reward: map[string]*chain.Reward{}, compl: map[string]*bool{},
	}
	r := epoch.New(s, f, epochLen)
	r.SetConsResolver(func(v chain.Validator) (string, error) { return v.ConsensusPubKey, nil })
	require.NoError(t, r.ReadEpoch(ctx, "c1", 1))

	var n int64
	require.NoError(t, s.DB().QueryRowContext(ctx,
		`SELECT proposals FROM epoch_snapshots WHERE epoch=1 AND operator=?`, testOperator).Scan(&n))
	require.Equal(t, int64(4), n)

	var registryStatus any
	require.NoError(t, s.DB().QueryRowContext(ctx,
		`SELECT registry_status FROM validators WHERE operator=?`, testOperator).Scan(&registryStatus))
	require.Equal(t, "STATUS_ACTIVE", registryStatus,
		"the join must match across the two address forms, keyed on chain.AddrKey")
}

// Nothing before this test held the code against cross-iteration pointer
// aliasing: every prior test had exactly one validator, so a bug that made
// every row share the last validator's pointers would still pass. Two
// validators, distinct in every field, each asserted independently.
func TestTwoValidatorsKeepTheirOwnValues(t *testing.T) {
	ctx := context.Background()
	s := newStore(t)

	const (
		op1 = "saiconvaloper1yh0ceztycl23zyuy67776u5dtfjawnrfytvjsd"
		// A second, distinct, decodable bech32 address. chain.AddrKey does not
		// verify the checksum, so any string with a valid-charset tail of at
		// least 6 characters after the delimiter decodes; the exact bytes don't
		// matter here, only that op1 and op2 differ.
		op2 = "saiconvaloper1zx2vfg8pdwn054tj3s6kcehmua7lry9"
	)

	// epochLen=1, epoch=1: any indexed height >= 1 covers it.
	const epochLen = 1
	for h := int64(1); h <= 3; h++ {
		require.NoError(t, s.RecordBlock(ctx, "c1", h, 1, "CONS1"))
	}
	for h := int64(4); h <= 8; h++ {
		require.NoError(t, s.RecordBlock(ctx, "c1", h, 1, "CONS2"))
	}

	trueVal, falseVal := true, false
	f := &fakeREST{
		vals: []chain.Validator{
			{Operator: op1, ConsensusPubKey: "CONS1", Moniker: "alpha", Status: "BOND_STATUS_BONDED", Jailed: false, Tokens: "111", CommissionRate: "0.010000000000000000"},
			{Operator: op2, ConsensusPubKey: "CONS2", Moniker: "bravo", Status: "BOND_STATUS_UNBONDING", Jailed: true, Tokens: "222", CommissionRate: "0.020000000000000000"},
		},
		reg: []chain.RegistryEntry{
			{Operator: op1, Status: "STATUS_ACTIVE", BondingStart: "2026-01-01T00:00:00Z", BondingStartHeight: 10, LockupSeconds: 1000},
			{Operator: op2, Status: "STATUS_PENDING", BondingStart: "2026-02-02T00:00:00Z", BondingStartHeight: 20, LockupSeconds: 2000},
		},
		ep: map[string]*chain.EP{
			op1: {AccumulatedEP: "5", LastEpochUpdate: 1},
			op2: {AccumulatedEP: "9", LastEpochUpdate: 1},
		},
		snap: map[string]*chain.Snapshot{
			op1: {ComputedScore: "0.500000000000000000", ComputedAt: "2026-01-01T01:00:00Z", BlocksSigned: 30, BlocksMissed: 1},
			op2: {ComputedScore: "0.900000000000000000", ComputedAt: "2026-01-01T02:00:00Z", BlocksSigned: 50, BlocksMissed: 2},
		},
		reward: map[string]*chain.Reward{
			op1: {Amount: "111nscn", Eligible: true, Reason: "eligible"},
			op2: {Amount: "222nscn", Eligible: false, Reason: "jailed"},
		},
		compl: map[string]*bool{
			op1: &trueVal,
			op2: &falseVal,
		},
	}

	r := epoch.New(s, f, epochLen)
	r.SetConsResolver(func(v chain.Validator) (string, error) { return v.ConsensusPubKey, nil })
	require.NoError(t, r.ReadEpoch(ctx, "c1", 1))

	type row struct {
		moniker, stakingStatus, tokens, commissionRate, registryStatus string
		jailed, complianceFlag                                         bool
		bondingStartHeight, lockupSeconds                              int64
		accumulatedEP                                                  string
		lastEpochUpdate                                                int64
		computedScore, rewardAmount, rewardReason                      string
		blocksSigned, blocksMissed, proposals                          int64
		rewardEligible                                                 bool
	}
	get := func(operator string) row {
		var out row
		require.NoError(t, s.DB().QueryRowContext(ctx,
			`SELECT moniker, staking_status, jailed, tokens, commission_rate, registry_status,
			    compliance_flag, bonding_start_height, lockup_seconds, accumulated_ep, last_epoch_update
			 FROM validators WHERE operator=?`, operator).Scan(
			&out.moniker, &out.stakingStatus, &out.jailed, &out.tokens, &out.commissionRate,
			&out.registryStatus, &out.complianceFlag, &out.bondingStartHeight, &out.lockupSeconds,
			&out.accumulatedEP, &out.lastEpochUpdate))
		require.NoError(t, s.DB().QueryRowContext(ctx,
			`SELECT computed_score, blocks_signed, blocks_missed, reward_amount, reward_eligible,
			    reward_reason, proposals
			 FROM epoch_snapshots WHERE epoch=1 AND operator=?`, operator).Scan(
			&out.computedScore, &out.blocksSigned, &out.blocksMissed, &out.rewardAmount,
			&out.rewardEligible, &out.rewardReason, &out.proposals))
		return out
	}

	r1, r2 := get(op1), get(op2)

	require.Equal(t, "alpha", r1.moniker)
	require.Equal(t, "bravo", r2.moniker)
	require.Equal(t, "BOND_STATUS_BONDED", r1.stakingStatus)
	require.Equal(t, "BOND_STATUS_UNBONDING", r2.stakingStatus)
	require.False(t, r1.jailed)
	require.True(t, r2.jailed)
	require.Equal(t, "111", r1.tokens)
	require.Equal(t, "222", r2.tokens)
	require.Equal(t, "0.010000000000000000", r1.commissionRate)
	require.Equal(t, "0.020000000000000000", r2.commissionRate)
	require.Equal(t, "STATUS_ACTIVE", r1.registryStatus)
	require.Equal(t, "STATUS_PENDING", r2.registryStatus)
	require.True(t, r1.complianceFlag)
	require.False(t, r2.complianceFlag)
	require.Equal(t, int64(10), r1.bondingStartHeight)
	require.Equal(t, int64(20), r2.bondingStartHeight)
	require.Equal(t, int64(1000), r1.lockupSeconds)
	require.Equal(t, int64(2000), r2.lockupSeconds)
	require.Equal(t, "5", r1.accumulatedEP)
	require.Equal(t, "9", r2.accumulatedEP)

	require.Equal(t, "0.500000000000000000", r1.computedScore)
	require.Equal(t, "0.900000000000000000", r2.computedScore)
	require.Equal(t, int64(30), r1.blocksSigned)
	require.Equal(t, int64(50), r2.blocksSigned)
	require.Equal(t, int64(1), r1.blocksMissed)
	require.Equal(t, int64(2), r2.blocksMissed)
	require.Equal(t, "111nscn", r1.rewardAmount)
	require.Equal(t, "222nscn", r2.rewardAmount)
	require.True(t, r1.rewardEligible)
	require.False(t, r2.rewardEligible)
	require.Equal(t, "eligible", r1.rewardReason)
	require.Equal(t, "jailed", r2.rewardReason)
	require.Equal(t, int64(3), r1.proposals)
	require.Equal(t, int64(5), r2.proposals)
}

// The bug this reproduces: SeedCursor moves the cursor forward without a
// single block being walked. ReadEpoch's coverage check used to read only
// "indexed >= this epoch's last height", which a seeded cursor satisfies for
// every epoch below the retention floor — turning "we never looked" into a
// measured zero for every validator. That is exactly the confusion NULL
// exists to prevent, and it happens silently: nothing else about the pass
// fails.
func TestSeededCursorDoesNotFakeCoverageForUnwalkedEpochs(t *testing.T) {
	ctx := context.Background()
	s := newStore(t)

	// Simulates a fresh database pointed at a node pruned to height 5000: the
	// cursor is seeded forward without a single block having been read.
	require.NoError(t, s.SeedCursor(ctx, "c1", 4999))

	const testOperator = "saiconvaloper1yh0ceztycl23zyuy67776u5dtfjawnrfytvjsd"
	f := &fakeREST{
		vals:   []chain.Validator{{Operator: testOperator, Moniker: "one", Status: "BOND_STATUS_BONDED", Tokens: "100"}},
		reg:    []chain.RegistryEntry{{Operator: testOperator, Status: "STATUS_ACTIVE"}},
		ep:     map[string]*chain.EP{},
		snap:   map[string]*chain.Snapshot{},
		reward: map[string]*chain.Reward{},
		compl:  map[string]*bool{},
	}

	// Epoch 1 (heights 1-100) is entirely below the walk floor (5000): the
	// seeded cursor alone must not be read as proof those blocks were counted.
	require.NoError(t, epoch.New(s, f, 100).ReadEpoch(ctx, "c1", 1))
	var proposals any
	require.NoError(t, s.DB().QueryRowContext(ctx,
		`SELECT proposals FROM epoch_snapshots WHERE epoch=1 AND operator=?`, testOperator).Scan(&proposals))
	require.Nil(t, proposals, "a seeded cursor must not fake coverage for an epoch nobody walked")

	// Now walk forward past an epoch that is fully inside the walked range
	// (heights 5001-5100, epoch 51 with epochLen 100) and confirm it DOES get
	// a real count.
	f.vals[0].ConsensusPubKey = "CONSHEX"
	for h := int64(5000); h <= 5100; h++ {
		e := uint64((h-1)/100 + 1)
		require.NoError(t, s.RecordBlock(ctx, "c1", h, e, "CONSHEX"))
	}
	r := epoch.New(s, f, 100)
	r.SetConsResolver(func(v chain.Validator) (string, error) { return v.ConsensusPubKey, nil })
	require.NoError(t, r.ReadEpoch(ctx, "c1", 51))
	var n int64
	require.NoError(t, s.DB().QueryRowContext(ctx,
		`SELECT proposals FROM epoch_snapshots WHERE epoch=51 AND operator=?`, testOperator).Scan(&n))
	require.Equal(t, int64(100), n, "an epoch fully inside the walked range still gets a real count")
}

// ReadEpoch is not atomic across validators: on 75 validators it is roughly
// 300 REST calls, and a failure landing after the first validator's row is
// written must not leave the epoch looking finished. Otherwise
// HighestEpochRead — and the binary's loop, which trusts it to decide what to
// retry — would move past an epoch some validators have no row for, forever.
func TestPartiallyWrittenEpochIsNotMarkedRead(t *testing.T) {
	ctx := context.Background()
	s := newStore(t)

	const (
		op1 = "saiconvaloper1yh0ceztycl23zyuy67776u5dtfjawnrfytvjsd"
		op2 = "saiconvaloper1zx2vfg8pdwn054tj3s6kcehmua7lry9"
	)
	const epochLen = 1
	require.NoError(t, s.RecordBlock(ctx, "c1", 1, 7, "CONS1"))
	require.NoError(t, s.RecordBlock(ctx, "c1", 2, 7, "CONS2"))

	boom := errors.New("node unreachable")
	f := &fakeREST{
		vals: []chain.Validator{
			{Operator: op1, ConsensusPubKey: "CONS1", Status: "BOND_STATUS_BONDED"},
			{Operator: op2, ConsensusPubKey: "CONS2", Status: "BOND_STATUS_BONDED"},
		},
		reg:    []chain.RegistryEntry{},
		ep:     map[string]*chain.EP{},
		snap:   map[string]*chain.Snapshot{},
		reward: map[string]*chain.Reward{},
		compl:  map[string]*bool{},
		epErr:  map[string]error{op2: boom},
	}
	r := epoch.New(s, f, epochLen)
	r.SetConsResolver(func(v chain.Validator) (string, error) { return v.ConsensusPubKey, nil })

	err := r.ReadEpoch(ctx, "c1", 7)
	require.Error(t, err, "the second validator's REST call must fail the whole pass")

	var written int64
	require.NoError(t, s.DB().QueryRowContext(ctx,
		`SELECT COUNT(*) FROM epoch_snapshots WHERE epoch=7`).Scan(&written))
	require.Equal(t, int64(1), written, "exactly the first validator's row landed before the failure")

	_, ok, err := s.HighestEpochRead(ctx, "c1")
	require.NoError(t, err)
	require.False(t, ok, "a partially written epoch must not be marked read")

	// A healthy retry completes the epoch and only then marks it.
	f.epErr = nil
	require.NoError(t, r.ReadEpoch(ctx, "c1", 7))
	require.NoError(t, s.DB().QueryRowContext(ctx,
		`SELECT COUNT(*) FROM epoch_snapshots WHERE epoch=7`).Scan(&written))
	require.Equal(t, int64(2), written)

	highest, ok, err := s.HighestEpochRead(ctx, "c1")
	require.NoError(t, err)
	require.True(t, ok)
	require.Equal(t, uint64(7), highest, "the retry finished the epoch and it is now marked read")
}

// The one arithmetic audit available for the number nothing else can audit:
// every block has exactly one proposer, and a covered epoch is exactly
// epochLen blocks, so SUM(proposals) across that epoch's snapshot rows must
// equal epochLen. Every other test here checks one validator in isolation —
// exactly the shape that cannot see a sum go wrong. This single assertion
// would catch, all at once: a departed validator's blocks vanishing from the
// staking list (sum short by however many it proposed), a wrong
// consensus-address derivation (everyone reads 0, sum 0), a double count
// surviving the replay guard (sum > epochLen), or a missed block (sum <
// epochLen).
func TestProposalsSumToTheEpochLength(t *testing.T) {
	ctx := context.Background()
	s := newStore(t)

	const (
		op1 = "saiconvaloper1yh0ceztycl23zyuy67776u5dtfjawnrfytvjsd"
		op2 = "saiconvaloper1zx2vfg8pdwn054tj3s6kcehmua7lry9"
	)
	const epochLen = 4
	// op1 proposes 3 of the epoch's 4 blocks, op2 the other 1.
	require.NoError(t, s.RecordBlock(ctx, "c1", 1, 1, "CONS1"))
	require.NoError(t, s.RecordBlock(ctx, "c1", 2, 1, "CONS1"))
	require.NoError(t, s.RecordBlock(ctx, "c1", 3, 1, "CONS1"))
	require.NoError(t, s.RecordBlock(ctx, "c1", 4, 1, "CONS2"))

	f := &fakeREST{
		vals: []chain.Validator{
			{Operator: op1, ConsensusPubKey: "CONS1", Status: "BOND_STATUS_BONDED"},
			{Operator: op2, ConsensusPubKey: "CONS2", Status: "BOND_STATUS_BONDED"},
		},
		reg:    []chain.RegistryEntry{},
		ep:     map[string]*chain.EP{},
		snap:   map[string]*chain.Snapshot{},
		reward: map[string]*chain.Reward{},
		compl:  map[string]*bool{},
	}
	r := epoch.New(s, f, epochLen)
	r.SetConsResolver(func(v chain.Validator) (string, error) { return v.ConsensusPubKey, nil })
	require.NoError(t, r.ReadEpoch(ctx, "c1", 1))

	var sum int64
	require.NoError(t, s.DB().QueryRowContext(ctx,
		`SELECT COALESCE(SUM(proposals), 0) FROM epoch_snapshots WHERE epoch=1`).Scan(&sum))
	require.Equal(t, int64(epochLen), sum,
		"every block in a covered epoch has exactly one proposer: the sum of proposals must equal the epoch length")
}
