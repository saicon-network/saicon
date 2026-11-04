package types

import (
	"fmt"

	"github.com/saicon-network/saicon/chain/crypto/vrf"
	entitytypes "github.com/saicon-network/saicon/chain/x/entityregistry/types"
)

// DefaultGenesis returns the default parameters, the published initial input and
// no current round: InitGenesis then creates round 1.
func DefaultGenesis() *GenesisState {
	return &GenesisState{
		Params:       DefaultParams(),
		InitialInput: DefaultInitialInput(),
		Pool:         []PoolEntry{},
		Submissions:  []Submission{},
		Draws:        []Draw{},
	}
}

// Validate performs the stateless genesis checks (spec §14). Addresses are
// checked with the explicit saiconvaloper prefix; the keeper decodes them with
// the global bech32 configuration.
func (gs GenesisState) Validate() error {
	if err := gs.Params.Validate(); err != nil {
		return err
	}
	if _, err := DecodeHex(gs.InitialInput, InputHexLen/2); err != nil {
		return fmt.Errorf("initial_input: %w", err)
	}

	// The next round to be created is 1 when no round is present.
	current := uint64(1)
	open := false
	if gs.CurrentRound != nil {
		if err := ValidateRound(*gs.CurrentRound); err != nil {
			return err
		}
		current = gs.CurrentRound.Number
		open = gs.CurrentRound.Status == RoundStatus_ROUND_STATUS_OPEN
	}
	if !open && (len(gs.Pool) > 0 || len(gs.Submissions) > 0) {
		return fmt.Errorf("pool and submissions require an OPEN current round")
	}

	pool, err := gs.validatePool()
	if err != nil {
		return err
	}
	if err := gs.validateSubmissions(pool); err != nil {
		return err
	}
	return gs.validateDraws(current)
}

// validatePool checks the pool entries and returns each entity's designated
// validator bytes (empty when it has none).
func (gs GenesisState) validatePool() (map[string]string, error) {
	pool := make(map[string]string, len(gs.Pool))
	designated := make(map[string]bool, len(gs.Pool))
	for _, e := range gs.Pool {
		if err := entitytypes.ValidateEntityID(e.EntityId); err != nil {
			return nil, fmt.Errorf("pool: %w", err)
		}
		if _, dup := pool[e.EntityId]; dup {
			return nil, fmt.Errorf("pool entity %q appears twice", e.EntityId)
		}
		switch {
		case e.Validator == "" && e.VrfPublicKey == "":
			pool[e.EntityId] = ""
		case e.Validator == "" || e.VrfPublicKey == "":
			return nil, fmt.Errorf("pool entity %q must carry both a validator and a key, or neither", e.EntityId)
		default:
			v, err := entitytypes.DecodeValidator(e.Validator)
			if err != nil {
				return nil, fmt.Errorf("pool entity %q: %w", e.EntityId, err)
			}
			if designated[string(v)] {
				return nil, fmt.Errorf("validator %s is designated twice", e.Validator)
			}
			designated[string(v)] = true
			pk, err := DecodeHex(e.VrfPublicKey, vrf.PublicKeySize)
			if err != nil {
				return nil, fmt.Errorf("pool entity %q key: %w", e.EntityId, err)
			}
			if err := vrf.ValidatePublicKey(pk); err != nil {
				return nil, fmt.Errorf("pool entity %q key: %w", e.EntityId, err)
			}
			pool[e.EntityId] = string(v)
		}
	}
	return pool, nil
}

func (gs GenesisState) validateSubmissions(pool map[string]string) error {
	seen := make(map[string]bool, len(gs.Submissions))
	for _, s := range gs.Submissions {
		want, ok := pool[s.EntityId]
		if !ok {
			return fmt.Errorf("submission names entity %q, which is not in the pool", s.EntityId)
		}
		if seen[s.EntityId] {
			return fmt.Errorf("entity %q has two submissions", s.EntityId)
		}
		seen[s.EntityId] = true
		v, err := entitytypes.DecodeValidator(s.Validator)
		if err != nil {
			return fmt.Errorf("submission of %q: %w", s.EntityId, err)
		}
		if want == "" || want != string(v) {
			return fmt.Errorf("submission of %q is by %s, not the designated validator", s.EntityId, s.Validator)
		}
		if _, err := DecodeHex(s.Output, vrf.OutputSize); err != nil {
			return fmt.Errorf("submission of %q output: %w", s.EntityId, err)
		}
		if _, err := DecodeHex(s.Proof, vrf.ProofSize); err != nil {
			return fmt.Errorf("submission of %q proof: %w", s.EntityId, err)
		}
		if s.Height < 0 {
			return fmt.Errorf("submission of %q has negative height %d", s.EntityId, s.Height)
		}
	}
	return nil
}

func (gs GenesisState) validateDraws(current uint64) error {
	if uint64(len(gs.Draws)) > MaxDrawHistory {
		return fmt.Errorf("%d draws, at most %d are kept", len(gs.Draws), MaxDrawHistory)
	}
	rounds := make(map[uint64]bool, len(gs.Draws))
	for _, d := range gs.Draws {
		if d.Round < 1 || d.Round >= current {
			return fmt.Errorf("draw round %d must be within [1, %d)", d.Round, current)
		}
		if rounds[d.Round] {
			return fmt.Errorf("draw round %d appears twice", d.Round)
		}
		rounds[d.Round] = true
		if err := validateDraw(d); err != nil {
			return fmt.Errorf("draw %d: %w", d.Round, err)
		}
	}
	return nil
}

func validateDraw(d Draw) error {
	if _, err := DecodeHex(d.Input, InputHexLen/2); err != nil {
		return fmt.Errorf("input: %w", err)
	}
	if _, err := DecodeHex(d.Seed, InputHexLen/2); err != nil {
		return fmt.Errorf("seed: %w", err)
	}
	if d.WindowOpenHeight < 0 || d.RotationHeight < 0 {
		return fmt.Errorf("negative height")
	}
	inPool := make(map[string]bool, len(d.Pool))
	for _, id := range d.Pool {
		if err := entitytypes.ValidateEntityID(id); err != nil {
			return fmt.Errorf("pool: %w", err)
		}
		if inPool[id] {
			return fmt.Errorf("pool entity %q appears twice", id)
		}
		inPool[id] = true
	}
	for _, o := range d.Outputs {
		if _, err := DecodeHex(o, vrf.OutputSize); err != nil {
			return fmt.Errorf("output: %w", err)
		}
	}

	switch d.Outcome {
	case DrawOutcome_DRAW_OUTCOME_DRAWN:
		if len(d.Pool) == 0 {
			return fmt.Errorf("drawn from an empty pool")
		}
		want := uint64(ShadowLotterySeats)
		if uint64(len(d.Pool)) < want {
			want = uint64(len(d.Pool))
		}
		if uint64(len(d.Winners)) != want {
			return fmt.Errorf("%d winners, want %d", len(d.Winners), want)
		}
		won := make(map[string]bool, len(d.Winners))
		for _, w := range d.Winners {
			if !inPool[w] {
				return fmt.Errorf("winner %q is not in the pool", w)
			}
			if won[w] {
				return fmt.Errorf("winner %q appears twice", w)
			}
			won[w] = true
		}
		if d.SkipReason != "" {
			return fmt.Errorf("a drawn round has skip_reason %q", d.SkipReason)
		}
		if d.CohortRound != d.Round {
			return fmt.Errorf("cohort_round is %d, want %d", d.CohortRound, d.Round)
		}
	case DrawOutcome_DRAW_OUTCOME_SKIPPED:
		if len(d.Winners) != 0 {
			return fmt.Errorf("a skipped round has %d winners", len(d.Winners))
		}
		if d.SkipReason == "" {
			return fmt.Errorf("a skipped round needs a skip_reason")
		}
		if d.CohortRound >= d.Round {
			return fmt.Errorf("cohort_round %d is not before the skipped round", d.CohortRound)
		}
	default:
		return fmt.Errorf("outcome %s", d.Outcome)
	}
	return nil
}
