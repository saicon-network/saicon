// Package epoch reads, once per epoch close, the numbers the chain computed
// for itself. It does not recompute any of them: a display that disagrees with
// the protocol is worse than one that is late.
package epoch

import (
	"context"
	"fmt"

	"github.com/saicon-network/saicon/indexer/internal/chain"
	"github.com/saicon-network/saicon/indexer/internal/store"
)

// Source is the slice of the REST client this package needs.
type Source interface {
	Validators(ctx context.Context) ([]chain.Validator, error)
	RegistryValidators(ctx context.Context) ([]chain.RegistryEntry, error)
	EP(ctx context.Context, operator string) (*chain.EP, error)
	Snapshot(ctx context.Context, operator string, epoch uint64) (*chain.Snapshot, error)
	RewardSnapshot(ctx context.Context, operator string, epoch uint64) (*chain.Reward, error)
	Compliance(ctx context.Context, operator string) (*bool, error)
}

type Reader struct {
	s        *store.Store
	src      Source
	epochLen uint64
	consOf   func(chain.Validator) (string, error)
}

func New(s *store.Store, src Source, epochLen uint64) *Reader {
	// The default resolver is the real derivation: a block header carries the
	// consensus ADDRESS, and staking reports the consensus PUBKEY. For ed25519
	// the address is the first 20 bytes of the key's SHA-256, upper-case hex —
	// verified against the localnet header at height 50. Get this wrong and the
	// proposal count quietly reads zero for everyone.
	return &Reader{s: s, src: src, epochLen: epochLen, consOf: func(v chain.Validator) (string, error) {
		// A key that does not decode is a malformed staking response, not chain
		// silence: it arrived in the same object as tokens, moniker and status.
		// Returning an error rather than an empty address, because an empty one
		// would land in the column where an honest absence lands as a zero.
		return chain.ConsAddrFromPubKey(v.ConsensusPubKey)
	}}
}

// SetConsResolver overrides how a validator's consensus address is derived, so a
// test can supply the address directly instead of a key.
func (r *Reader) SetConsResolver(f func(chain.Validator) (string, error)) { r.consOf = f }

// ReadEpoch writes one snapshot row per validator for the epoch just closed,
// and refreshes the current-state row while it is there.
//
// "The chain closed epoch N" and "this service has indexed every block of
// epoch N" are different facts. ReadEpoch checks the second itself before it
// will write a proposal count: reading ahead of the follower would otherwise
// write a count that is simply short, with no second pass to correct it,
// because each epoch is read once.
func (r *Reader) ReadEpoch(ctx context.Context, chainID string, ep uint64) error {
	// The last height belonging to this epoch. Below it, the proposal counts for
	// the epoch are still being gathered and are not a fact yet.
	covered := false
	if r.epochLen > 0 {
		indexed, err := r.s.IndexedHeight(ctx, chainID)
		if err != nil {
			return err
		}
		from, err := r.s.FirstWalkedHeight(ctx, chainID)
		if err != nil {
			return err
		}
		// Both ends, not just the far one. Against a pruned node the cursor is
		// seeded forward without a single block being read, so "indexed >= the
		// epoch's last height" is satisfied by epochs nobody ever walked — and
		// every validator in them would get a measured zero. The first height of
		// this epoch has to be at or above where the walk actually began.
		first := int64((ep-1)*r.epochLen) + 1
		covered = from <= first && indexed >= int64(ep*r.epochLen)
	}

	vals, err := r.src.Validators(ctx)
	if err != nil {
		return err
	}
	reg, err := r.src.RegistryValidators(ctx)
	if err != nil {
		return err
	}
	// Keyed on the decoded bytes, not the string: the registry names this validator
	// `saicon1…` while staking names it `saiconvaloper1…`, and the two never compare
	// equal. Keying on the string is a join that silently matches nothing.
	regBy := make(map[string]chain.RegistryEntry, len(reg))
	for _, e := range reg {
		k, err := chain.AddrKey(e.Operator)
		if err != nil {
			return fmt.Errorf("registry address %q: %w", e.Operator, err)
		}
		regBy[k] = e
	}

	for _, v := range vals {
		epRec, err := r.src.EP(ctx, v.Operator)
		if err != nil {
			return err
		}
		compliant, err := r.src.Compliance(ctx, v.Operator)
		if err != nil {
			return err
		}
		row := store.ValidatorRow{
			Operator:       v.Operator,
			Moniker:        v.Moniker,
			StakingStatus:  v.Status,
			Jailed:         v.Jailed,
			Tokens:         v.Tokens,
			CommissionRate: v.CommissionRate,
			ComplianceFlag: compliant,
		}
		valKey, err := chain.AddrKey(v.Operator)
		if err != nil {
			return fmt.Errorf("operator address %q: %w", v.Operator, err)
		}
		// The registry fields are pointers because a validator can be in the staking
		// set with no registry record at all: x/validatorregistry creates it from the
		// AfterValidatorBonded hook only (keeper/hooks.go), while this service lists
		// staking validators with no status filter. A validator created but never
		// bonded, or since unbonded, has none. Written as zeroes, LockupSeconds = 0
		// would read as "no lock-up" on a chain whose V-3 invariant is the lock-up.
		if e, ok := regBy[valKey]; ok {
			status, start := e.Status, e.BondingStart
			height, lockup := e.BondingStartHeight, e.LockupSeconds
			row.RegistryStatus, row.BondingStart = &status, &start
			row.BondingStartHeight, row.LockupSeconds = &height, &lockup
		}
		if epRec != nil {
			acc, last := epRec.AccumulatedEP, epRec.LastEpochUpdate
			row.AccumulatedEP, row.LastEpochUpdate = &acc, &last
		}
		if err := r.s.UpsertValidator(ctx, chainID, row); err != nil {
			return err
		}

		snap, err := r.src.Snapshot(ctx, v.Operator, ep)
		if err != nil {
			return err
		}
		rew, err := r.src.RewardSnapshot(ctx, v.Operator, ep)
		if err != nil {
			return err
		}
		out := store.SnapshotRow{Epoch: ep, Operator: v.Operator}
		if snap != nil {
			score, at := snap.ComputedScore, snap.ComputedAt
			signed, missed := snap.BlocksSigned, snap.BlocksMissed
			out.ComputedScore, out.ComputedAt = &score, &at
			out.BlocksSigned, out.BlocksMissed = &signed, &missed
		}
		if rew != nil {
			amt, elig, reason := rew.Amount, rew.Eligible, rew.Reason
			out.RewardAmount, out.RewardEligible, out.RewardReason = &amt, &elig, &reason
		}
		// A count is written only when the follower has walked the whole epoch.
		// Otherwise Proposals stays nil and the column stays NULL: "we have not
		// finished counting" is a different fact from "this validator proposed
		// nothing", and zero cannot carry both.
		if covered {
			cons, err := r.consOf(v)
			if err != nil {
				return fmt.Errorf("validator %s: consensus address: %w", v.Operator, err)
			}
			n, err := r.s.ProposalsFor(ctx, chainID, ep, cons)
			if err != nil {
				return err
			}
			out.Proposals = &n
		}
		if err := r.s.UpsertSnapshot(ctx, chainID, out); err != nil {
			return err
		}
	}
	// Only now is the epoch finished. Marking it any earlier — or inferring it
	// from the snapshots that exist — would call the epoch done as soon as the
	// first validator's row landed, and the loop would move on without the rest.
	return r.s.MarkEpochRead(ctx, chainID, ep)
}
