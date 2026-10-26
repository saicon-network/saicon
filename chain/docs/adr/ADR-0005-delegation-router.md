# ADR-0005: The delegation router — where stake goes, separate from whether it may go

Status: **Accepted** (S7, 8 – 21 Oct 2026). Spec:
the design notes. See
[DEVELOPMENT.md](../DEVELOPMENT.md) §2.8.
Related: [ADR-0012](ADR-0012-caps-and-stake-engine.md) (the caps and the queue this
builds on), [ADR-0004](ADR-0004-validator-activation-model.md) (renumbered the router
ADR to this number), [ADR-0014](ADR-0014-entity-registry.md) (the entity cap `entityAllows`
waits for).

## Context

Register decision D5 chose `decentralization_aware` overflow: when a delegation would
take a validator above its cap, the excess goes to other validators instead of being
refused. The whitepaper (§7.4) names the inputs — quality, remaining capacity, inverse
current stake — and (§3.2) describes the router and the stake engine as two modules.

S6 shipped the caps and the queue (ADR-0012) but no router: a delegation above a cap is
refused, and the delegator must find another validator by hand. S7 closes that, together
with two delegation-wide rules the Register carries — at most three validators per
delegator, and a 100 SCN minimum — and with the S6 gap that messages executed by
`x/gov`, `x/group` and an ICA host do not pass the ante handler where the caps are
checked.

The choice of D5 was qualitative: a near-Pareto tie with `equal_under_cap` (HHI 0.0119,
coverage 0.581 against 0.694), and "at most three" is an asserted knee point.
Research brief S-13 was to run the allocation comparison; **no run of it is in the
archive**, so the weight function below is a design choice, not a measured result.

## Decision

### D1 — A separate module, and a separate message

`x/delegationrouter` decides *where* stake goes; `x/stakeengine` decides *whether* stake
may go somewhere. The router depends on stakeengine, never the reverse, so the
delegation-wide rules (three validators, the minimum) live in stakeengine and the router
only reads them. The router holds no module account and no coins: every amount it places
moves through `x/staking` or the stakeengine queue escrow.

`MsgDelegateRouted { delegator, validator, amount }` sits beside the stock `MsgDelegate`.
The delegator's `validator` is the **primary**. A plain `MsgDelegate` keeps stock
semantics and is refused at a cap, with an error that names the router.

### D2 — The plan is one pure function

`Plan(delegator, primary, amount)` fills the primary up to its room, then spreads the
remainder over at most `max_route_legs` (default 2) other validators. The message and the
`route` preview query call the same function, so a preview never disagrees with execution
in the same block. A candidate must be registry `STATUS_ACTIVE`, not jailed in staking,
carry a participation score at or above `min_route_score` (default 0.75, the quarantine
threshold) in the latest closed epoch, have room, and pass `entityAllows`. Ties break by
operator address; shares are `LegacyDec` truncated to nscn, the truncation remainder going
to the highest-weight leg with room. Neither the primary nor the delegator's own validator
is a candidate. The primary takes a slot only if it gets a leg, and an overflow leg below
`min_delegation` is folded into the others (the smallest is dropped and the remainder
re-split), so dust does not occupy a validator slot; a lone leg is kept whatever its size.

### D3 — The weight is under research

```
w(v) = score(v) × room(v) / max(tokens(v), 1)
```

Quality × remaining capacity × inverse current stake. This is a **design choice under
research**: S-13 has not been executed (Context). The function is kept as one body so a
measured result replaces it and nothing else. "Expected activity", which the whitepaper
also names, has no on-chain signal beyond the score until WARP (S11); then `score(v)`
here becomes WARP, as in the queue's ordering key.

### D4 — Slots: three validators, the self-bond excluded

A delegator's slots are the distinct validators it has a delegation to or a queue entry
for. An operator's own self-delegation to its own validator is **not** a slot — it is
admission stake, not a delegation choice. Unbonding entries are not slots. A delegation
that would create a fourth slot is refused on every path: `MsgDelegate`, redelegation,
cancel-unbonding, the router and the queue. A full redelegation frees its source slot
first, so moving all stake from one validator to a new one is allowed.
`max_validators_per_delegator` (default 3, bound [2, 5]) is a governance parameter; the slot
list is read up to the bound, not the current value, so lowering the parameter never hides a
slot a delegator already holds. The
queue index `queue_by_delegator` keeps the count off the queue walk.

### D5 — The minimum is 100 SCN, on the entry messages only

`min_delegation` (default 100 SCN, bound [1, 10,000 SCN]) applies to `MsgDelegate`,
`MsgDelegateRouted` and `MsgEnqueueDelegation`, in the ante decorator and the message
handlers. It does not apply to redelegation, cancelling an unbonding, queue fills, or the
router's own queue entries — those move stake already placed, or are legitimately small
remainders of a message that as a whole met the minimum. `min_queue_entry` rises to 100
SCN to match.

### D6 — Global cap: the overflow joins the queue

Legs execute in order, primary first. Each takes what the global headroom allows as a
direct `Delegate`; headroom left over after one leg flows to the next. The part of a leg
above headroom becomes a queue entry on that leg's validator, in the same message. The
whole message is all-or-nothing.

### D7 — The backstop: staking hooks, not only the ante

The S6 caps, the entry floor and the V-3 lock-up are enforced in `x/staking`'s delegation
hooks as well as in the ante handler, so a proposal executed by `x/gov` or `x/group`,
an ICA host, or `authz` cannot pass around them. A before-hook records the validator's
tokens, the bonded pool and the operator's self-bond into a transient store; the
after-hook checks growth against the caps, the floor and the slot limit. Rules fire only
on **growth**, and on an operator's self-bond *shrinking* (self-bond fell and the
validator's tokens did not grow, which tells a real reduction from exchange-rate
truncation on a top-up). The V-3 lock-up fires only when the self-bond shrinks; the entry
floor fires when it shrinks or when the self-delegation is new, exactly as the ante does
(creation and withdrawal). A top-up never meets the floor, so an operator slashed below
2,000 SCN can still add stake. Undelegation, slashing and every EndBlock path stay
unblockable. The ante decorator stays, because it
rejects in `CheckTx` with the better message; both call one set of predicates.

**The backstop sleeps during BeginBlock.** Slashing a redelegation calls `Unbond` on the
destination delegation, so the hooks fire inside a slash; an operator's self-bond slashed
below 2,000 SCN, or slashed inside its lock-up, would make the entry-floor and V-3 rules
return an error out of BeginBlock and halt the chain. On this chain slashing happens only
in BeginBlock, and no user message runs there. stakeengine's PreBlocker opens a window
and its BeginBlocker, ordered last among the BeginBlockers, closes it; the hook does
nothing while it is open. It also does nothing when the cap state has not been computed
yet (a genesis with `initial_height > 1`, where gentx delegations run before stakeengine's
InitGenesis).

## Consequences

- A user of the plain `MsgDelegate` sees no change except the refusals: past a cap, the
  error now points at `MsgDelegateRouted`; a fourth validator or an amount under 100 SCN
  is refused.
- A routed delegation needs a high gas limit (about 1,000,000); the 200,000 default
  fails out of gas.
- The backstop closes the group / gov / ICA bypass recorded at S6. It does **not** see
  stake that becomes bonded without a delegation operation: a validator entering the
  active set at rotation or after unjail with stake delegated while it was not bonded, and
  several same-block `MsgCreateValidator` (their stake sits in the not-bonded pool). That
  remains C1, stated alone, before testnet. As a side effect a redelegation from a
  non-bonded source into a bonded validator is now judged against the global cap.
- The minimum is not enforced by the hook, so a dust delegation made through governance,
  group or ICA passes. It is not a cap breach; accepted, revisit if abused.
- `entityAllows` returns true until S8's entity registry gives it a body; nothing else
  changes then.
- Existing over-concentrated delegations are not rebalanced (B5), and a governance
  decrease of `max_validators_per_delegator` freezes, rather than sheds, delegators already
  above it — the rule ADR-0012 applies to caps.
- The weight function is replaced when S-13 is executed; D3 is the only place that moves.

## Alternatives considered

- **Route inside plain `MsgDelegate`.** Rejected (owner, 2026-10-12): it would change the
  meaning of a stock message wallets already sign, and a delegator could no longer ask for
  exactly one validator.
- **Refuse when the global cap is full.** Rejected: the overflow joins the S6 queue in the
  same message, so no delegator has to retry.
- **Three-validator rule on the router only.** Rejected: a delegator would reach a fourth
  validator through plain staking. The rule holds on every path.
- **Backstop at pool level instead of delegation hooks.** Hooks alone do not see stake
  that arrives by bonding; that is the C1 remainder, kept rather than hidden by a coarser
  check that would also fire on slashing and rotation.
- **`equal_under_cap` weights.** Not chosen at D5 (qualitative near-tie); remains the
  comparison S-13 would run.
