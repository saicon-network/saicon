# ADR-0014: Entity registry — attestation before activation, the evidence ladder, and appeal

Status: **Proposed** (2026-08-15) — target module: the entity registry alongside
`x/stakeengine`, Q4 2026, see [DEVELOPMENT.md](../DEVELOPMENT.md) §4.
Source of the parameter values: **Decision Register v1.0** §2.4 and §2.7; evidence from Stage 4
and Stage 10 of the calibration program (both executed — see the research archive).
Related: [ADR-0012](ADR-0012-caps-and-stake-engine.md) (caps this registry makes enforceable),
[ADR-0010](ADR-0010-slashing-policy.md) §3 (the coordinated-attack rule, still undecided),
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

### D6 — What this ADR does not decide

The **coordinated-attack slash tier** (ADR-0010 §3) is not decided here and cannot be until
this registry exists. That ADR records why: no fraction, no evidence standard, no submitter and
no governance path exists in the Register or in any of the eleven studies, and slashing on an
inference that is wrong 91% of the time against a careful adversary is a liability rather than
a defence. The registry is the precondition — it is the first point at which "these validators
act as one" can be *stated* rather than guessed — not the decision.

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

## References

- [DEVELOPMENT.md](../DEVELOPMENT.md) §2.4 (entity caps), §2.7 (ladder and appeal), §3.2 (the
  seven-stage program), §5 (blockers B3, B4)
- The research archive — Stage 4 (seat cap, executed), Stage 10 (thresholds, executed),
  Stage 7 (integrated stress test, executed)
- [ADR-0012](ADR-0012-caps-and-stake-engine.md) D4 — the entity caps this registry enforces
- [ADR-0010](ADR-0010-slashing-policy.md) §3 — the coordinated-attack tier this ADR unblocks
  but does not decide
- [ADR-0013](ADR-0013-bounded-dao-authority.md) — which of these values a vote may move
