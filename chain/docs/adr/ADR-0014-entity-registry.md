# ADR-0014: Entity registry — attestation before activation, the evidence ladder, and appeal

Status: **Accepted (D1–D3 implemented in S8, v0.10.0; D4–D5 planned, B4)** — written 2026-08-15;
module `x/entityregistry`, see [DEVELOPMENT.md](../DEVELOPMENT.md) §1.1 and §2.4.
Source of the parameter values: **Decision Register v1.0** §2.4 and §2.7; evidence from Stage 4
and Stage 10 of the calibration program (both executed — see the research archive).
Related: [ADR-0012](ADR-0012-caps-and-stake-engine.md) (caps this registry makes enforceable),
[ADR-0010](ADR-0010-slashing-policy.md) §3 (the coordinated-attack rule, decided by D-S8-2 and implemented in S8),
[ADR-0013](ADR-0013-bounded-dao-authority.md) (governance bounds), ADR-0008 (the lottery whose
ticket allocation this changes).

## Context

One entity running many validators is the classic proof-of-stake failure mode: the network
counts nodes, the attacker counts wallets, and every per-validator limit is satisfied while one
party holds a share none of them was meant to allow.

Stage 4 measured how much this matters here. With no entity limits, a single hidden entity
running five high-scoring nodes reached **53.8% maximum voting power**, and the probability of
it holding at least a third was **100%**. With the seat cap at two, the same attacker reached
**3.2%**, and the probability of a third fell to **0%**. That is the largest single effect any
of the eleven studies measured.

The difficulty is that *coordination is an inference*. Equivocation is objective — the
consensus engine produces cryptographic evidence of a duplicate vote for a specific validator.
"These five validators are one party" is a conclusion drawn from behaviour, and the same
program that measured the seat cap's value also measured how weak that conclusion is: a
low-signal cartel evades quarantine **~91% of the time over twelve weeks**, and Stage 7's
`hidden_cartel_20` scenario had a P95 detection day of **84.8**.

Those two findings, taken together, decide the shape of this ADR. Detection cannot be the
primary control. Attestation before activation is.

## Decision

### D1 — An entity is a declared beneficial owner, attested before first activation

A validator declares the entity that beneficially owns it, and the declaration is attested
**before** its first activation — not after, not on suspicion. This is blocker B3 in the
Register and it is the load-bearing decision in this document: every limit below is enforceable
only against entities the chain knows about, and a limit that depends on catching a liar is a
limit that works 9% of the time on the population that matters.

Attestation gates activation only. It does not gate registration, bonding, or the right to
operate a node — an unattested validator can exist and can be observed; it cannot enter the
active set.

### D2 — Three limits per entity, and they compose deliberately

| Limit | Value | Source |
|---|---|---|
| Aggregate stake across all of an entity's validators | **≤ 5%** | Register §2.4; Stage 4 swept `{none, 5%, 7.5%, 10%}` |
| Active seats | **≤ 2** | Stage 4 swept `{none, 1, 2, 3}` |
| Lottery tickets | **1** | Stage 4, and see D3 |

Read honestly: Stage 4 **swept** the range and the adopted triple was **chosen at the practical
knee with a stated reason** — two seats permit legitimate redundancy, one ticket removes the
linear Sybil advantage — rather than emitted by an optimiser. It is a defensible selection, not
a measured optimum, and this ADR does not upgrade it into one.

The 5% aggregate is under re-measurement against the post-migration stake distribution (brief
S-14, not started). The seat cap of 2 is not: Stage 4 ran, and the 53.8% → 3.2% result is what
it produced.

**Why the aggregate is needed when the per-node cap already exists.** The per-validator cap
(2%, ADR-0012) and the seat cap (2) do not by themselves bound an entity: seats limit how many
of its validators are *active*, while an entity may hold more validators than it has seats. The
aggregate is what closes that, and it is the rule whose absence lets a per-node design permit
double what it appears to.

### D3 — One lottery ticket per entity, not per validator

ADR-0008 allocates 22 of 75 active seats by lottery. Per-validator tickets make the lottery
linearly Sybil-able: an entity's expected seats grow with the number of validators it registers,
which is precisely the resource an attacker has most of. One ticket per **attested entity**
removes that gradient. An unattested validator holds no ticket, which is the second reason
attestation gates activation rather than merely labelling it.

### D4 — The evidence ladder: monitoring, then review, and never a slash

Two thresholds, both from Stage 10, which tested them against a stricter alternative:

| Level | Trigger | Effect |
|---|---|---|
| **Warning** | risk ≥ **0.60** sustained over **2 epochs** | monitoring only; nothing is restricted |
| **Quarantine review** | risk ≥ **0.75** sustained over **2 epochs** **and** ≥ **2 independent evidence classes** | isolated from consensus pending review; ≤72h to open, published evidence hash, appeal path, 14-day review SLA |

**A risk score never slashes.** Slashing follows cryptographic evidence of a protocol offence
(ADR-0010) and nothing else. The ladder restricts participation; it does not take stake.

Stage 10 tested 0.75 against a stricter 0.90 and the stricter setting lost: it bought almost no
reduction in false positives while cutting high-signal detection within twelve weeks from
**95% to 28%**. The two-evidence-class requirement is what carries the false-positive burden
instead, and it is the part that must not be relaxed to make a detector look better.

### D5 — Appeal, and the two clocks that are not the same clock

An entity under quarantine review may appeal. The review has a **14-day SLA**; the governed
unjail path already shipped carries a **1-day cooldown** (`x/compliance.unjail_cooldown_blocks`,
ADR-0010). These are different clocks for different things — one bounds how long a review may
take, the other how soon a jailed validator may petition — and §2.7 flags that they have been
stated separately often enough to be confused. They are stated together here for that reason.

### D6 — The coordinated-attack slash tier: decided elsewhere, on this registry

The **coordinated-attack slash tier** (ADR-0010 §3) is not decided by this ADR; it was decided by the
owner on 2026-10-28 (D-S8-2) and implemented in S8 (ADR-0010 §3, Decision and Implementation). That
ADR records why it had to wait: no fraction, no evidence standard, no submitter and no governance
path existed in the Register or in any of the eleven studies, and slashing on an inference that is
wrong 91% of the time against a careful adversary is a liability rather than a defence. The registry
was the precondition — the first point at which "these validators act as one" can be *stated* rather
than guessed — and the rule uses it exactly that way: it arms on duplicate-vote evidence for at least
two validators the registry links to one entity, and governance ratifies. It does not turn D4 around:
a risk score still never slashes, and the rule's reach is as wide as attestation's honesty (D1).

## Open problem (🚧) — the thresholds are calibrated on synthetic labels

Blocker B4. The 0.60 and 0.75 thresholds, and the two-class rule, were calibrated on
synthetically generated entity labels because no real entity-labelled dataset exists. Stage 10
is honest about this and so is this ADR: the numbers are the best available and they have not
met an adversary. They must be re-calibrated against real labels once the invite-DevNet (S9) has
produced any, and until then the ladder's monitoring level is worth more than its review level.

This is also why D1 is written the way it is. If the detector were trustworthy, attestation
could be a formality. It is not, so it is not.

## Consequences

**Attestation is an operational burden on honest operators**, and it is the price of the seat
cap being meaningful. Anything cheaper — self-declaration without attestation, detection alone —
is what Stage 4 and Stage 10 together show does not hold.

**The registry becomes a dependency of three other pieces.** The entity caps in ADR-0012, the
lottery in ADR-0008 and the coordinated-attack rule in ADR-0010 §3 all need it. It is not an
optional hardening layer that can slip; it is the thing they are written against.

**A published ladder tells an attacker the thresholds.** Accepted deliberately: the two-class
requirement, not the secrecy of a number, is what makes evasion expensive, and a threshold
nobody outside the team can audit is a threshold nobody outside the team can trust.

**Quarantine is reversible and slashing is not**, which is why the ladder stops short of the
latter. An entity wrongly quarantined loses participation for a review period. An entity
wrongly slashed loses stake, and no appeal returns it.

## Alternatives considered

**Detection without attestation.** Rejected on measurement: ~91% evasion over twelve weeks for
a low-signal cartel, and 13–84 days of attacker voting power above a third before containment
in Stage 7. A control that arrives on day 85 does not control anything.

**Attestation without a stake aggregate — seats only.** Rejected: seats bound how many of an
entity's validators are active, not how much stake it holds. See D2.

**A stricter quarantine threshold at 0.90.** Rejected on Stage 10's measurement: negligible
false-positive gain, high-signal detection within twelve weeks falling from 95% to 28%.

**Slashing on the risk score.** Rejected as a category error — see D4 and ADR-0010 §3.

## Implementation (S8, October 2026)

`chain/x/entityregistry`, with the aggregate cap in `chain/x/stakeengine` and the first upgrade
handler in `chain/app/upgrades.go`; design in
the design notes.
D1–D3 are built; D4 (the evidence ladder) and D5 (appeal) are planned work under blocker B4.

- **The module.** An entity is an id, a display name, the SHA-256 of an off-chain attestation
  document, the attestor and one to eight member validators; nothing personal is on-chain. The
  attestors are a governed list in params, and `MsgAttestEntity` / `MsgRevokeEntity` are signed by
  one of them or by the governance authority. A validator belongs to at most one entity.
- **The gate (D1).** Every path into registry `ACTIVE` — activation on bond, `MsgClearFlag`,
  `MsgUnjail` and the resume after an aborted exit — asks the registry. A validator without an
  attested entity stays at BONDING and is promoted by a bounded EndBlock pass over held validators
  once it is attested; a refusal never halts the chain (the bond hook returns nil on a hold).
  Invariant V-12 halts if an ACTIVE validator is neither attested nor recorded as a frozen seat.
- **The limits (D2, D3).** At most `max_active_seats` (default 2) ACTIVE seats per entity, enforced
  by the gate; an aggregate stake cap `max(entity_cap_fraction × EWMA-30, entity_cap_floor)`
  (default 5%), enforced by `x/stakeengine` on the ante path, the staking hooks, the queue fill and
  the router; one ticket per attested entity, exposed by query.
- **The freeze.** Over a limit an entity takes no new stake and its members cannot (re-)activate;
  nobody is demoted or force-unbonded. A revoked entity's ACTIVE validators keep their seats,
  recorded as frozen, until they leave ACTIVE; while they stay ACTIVE they still count against
  the entity they were frozen out of, its seats and its aggregate stake. Which two seats stay when an entity holds more is
  "first activated", provisional until ADR-0008 decides.
- **The upgrade.** `v0.10.0` adds the store, migrates `x/stakeengine` to consensus version 2 and
  attests every ACTIVE validator as its own one-member entity, so no earning validator is left
  without a seat.
- **Membership history (S8 W3).** For the coordinated-attack rule a member row carries the height the
  validator joined its entity, and a departure row keeps, per validator, the last entity it left, the
  height it left, and whether its seat stayed frozen and charged to that entity, and until when.
  `Keeper.EntityAt(validator, height)` answers which entity owned a validator when it signed, by the
  convention that a vote at height `h` is judged on the registry as committed at `h − 1`: a removal or
  a revocation after the offence still links, a frozen seat still charged at `h` links (to a revoked
  entity too), a validator that joined at or after `h` does not. Only the last departure per validator
  is kept. Departures are in genesis; V-12 does not read them.
- **The limitation (spec §3).** The gate decides the PoP active set, not CometBFT's: consensus is
  still x/staking's top 75 by stake, so an unattested or frozen validator that x/staking bonds signs
  with full voting power. Consensus-level enforcement is the selection layer's (S10, ADR-0008);
  DEVELOPMENT.md §1.4 states the scope in full.

## References

- [DEVELOPMENT.md](../DEVELOPMENT.md) §2.4 (entity caps), §2.7 (ladder and appeal), §3.2 (the
  seven-stage program), §5 (blockers B3, B4)
- The research archive — Stage 4 (seat cap, executed), Stage 10 (thresholds, executed),
  Stage 7 (integrated stress test, executed)
- [ADR-0012](ADR-0012-caps-and-stake-engine.md) D4 — the entity caps this registry enforces
- [ADR-0010](ADR-0010-slashing-policy.md) §3 — the coordinated-attack tier this registry made
  possible, decided by D-S8-2 and implemented in S8
- [ADR-0013](ADR-0013-bounded-dao-authority.md) — which of these values a vote may move
