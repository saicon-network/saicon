package types

import "fmt"

// DefaultGenesis carries no cap state: InitGenesis computes it from the chain.
func DefaultGenesis() *GenesisState {
	return &GenesisState{
		Params: DefaultParams(),
		Queue:  []QueueEntry{},
	}
}

// Validate performs basic genesis state validation.
func (gs GenesisState) Validate() error {
	if err := gs.Params.Validate(); err != nil {
		return err
	}
	if uint64(len(gs.Queue)) > gs.Params.MaxQueueEntries {
		return fmt.Errorf("queue holds %d entries, more than max_queue_entries %d", len(gs.Queue), gs.Params.MaxQueueEntries)
	}
	seen := make(map[uint64]bool, len(gs.Queue))
	for _, e := range gs.Queue {
		if seen[e.Id] {
			return fmt.Errorf("queue entry id %d appears twice", e.Id)
		}
		seen[e.Id] = true
		if e.Id >= gs.NextQueueId {
			return fmt.Errorf("queue entry id %d is not below next_queue_id %d", e.Id, gs.NextQueueId)
		}
		if _, err := DecodeAccount(e.Delegator); err != nil {
			return fmt.Errorf("queue entry %d delegator: %w", e.Id, err)
		}
		if _, err := DecodeValidator(e.Validator); err != nil {
			return fmt.Errorf("queue entry %d validator: %w", e.Id, err)
		}
		if e.Amount.IsNil() || !e.Amount.IsPositive() {
			return fmt.Errorf("queue entry %d amount must be positive", e.Id)
		}
		if e.Amount.LT(gs.Params.MinQueueEntry) {
			return fmt.Errorf("queue entry %d amount %s is below min_queue_entry %s", e.Id, e.Amount, gs.Params.MinQueueEntry)
		}
		if e.EnqueuedHeight < 0 {
			return fmt.Errorf("queue entry %d enqueued_height %d is negative", e.Id, e.EnqueuedHeight)
		}
	}
	if cs := gs.CapState; cs != nil {
		for _, f := range []struct {
			name string
			bad  bool
		}{
			{"public_circulating", cs.PublicCirculating.IsNil() || cs.PublicCirculating.IsNegative()},
			{"global_cap", cs.GlobalCap.IsNil() || cs.GlobalCap.IsNegative()},
			{"validator_cap", cs.ValidatorCap.IsNil() || cs.ValidatorCap.IsNegative()},
			{"ewma_bonded", cs.EwmaBonded.IsNil() || cs.EwmaBonded.IsNegative()},
		} {
			if f.bad {
				return fmt.Errorf("cap_state.%s must be set and non-negative", f.name)
			}
		}
	}
	return nil
}
