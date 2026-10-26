# ADR-0007: On-chain / off-chain / contract cost-split

Status: **Accepted** (2026-06-12). Origin: planning call 2026-06-10 (Layer-1 cost discipline).
Related: [ADR-0006](ADR-0006-smart-contracts-cosmwasm.md) (smart-contract layer).

## Context

SAiCoN is a **Layer-1** — "there is no retreat": every on-chain action consumes gas and grows
state. If every feature (KPIs, scoring, DAO, analytics) runs as on-chain logic — and especially
if it runs through a smart-contract VM — cost compounds quickly and the chain stops being cheap.

We need a **standing rule** for where any capability runs (native module / smart contract /
off-chain backend) and a **classification** of the current and planned features, so that:

- new work is placed deliberately, not by default-to-on-chain;
- the external front-end team knows what they query from the chain vs from a backend (this rule
  defines the **validator read-surface**);
- "cheapness" is a designed property, not an afterthought.

This ADR codifies the principle agreed on the 2026-06-10 call. It is a placement rule; it does
not itself build the off-chain backend (a separate component).

## Decision

### The placement rule (decision tree for any capability)

1. **Is it consensus-critical, or does it directly determine money/state?**
   → **on-chain, native Go module.** (staking, validator status/lifecycle, compliance flag,
   participation gate + reward emission, invariants, delegation caps/overflow.) Native modules
   pay no VM gas overhead and are where protocol correctness lives.
2. **Else — is it programmable application logic that third parties deploy/extend**, settling in
   `SCN` and reading PoP state read-only?
   → **smart contract (CosmWasm, ADR-0006).** Core protocol does **not** go here (contracts pay
   VM gas; the core stays native).
3. **Else** (derived, presentational, or computationally heavy, and not consensus-critical)
   → **off-chain backend.**

Every **new on-chain action is gas-estimated before merge** — placement on-chain must be
justified, not assumed.

### Trust tiers for the off-chain backend

Off-chain does not mean unaccountable. Tier is set by **whether the output flows back on-chain
and touches money**:

- **Tier-0 — read-only / presentational.** A trusted operator service with no on-chain guarantee:
  analytics, explorer, validator scoreboard, dashboards, activity archive/indexing, notifications.
  If it is wrong, nothing on-chain is corrupted.
- **Tier-1 — output returns on-chain and affects economics.** Must be **verifiable**: the result
  is **anchored on-chain** (signature / commitment / hash), **deterministically reproducible**,
  and **on-chain logic validates or ratifies it** before it can affect rewards or stake. v1
  mechanism: a signed/committed result plus a deterministic recompute check; zero-knowledge
  attestation is a Year-2 research direction. Example: heavy multi-signal / historical scoring
  whose output influences overflow redistribution or rewards.

### Classification (current + planned)

| Capability | Layer |
|---|---|
| Staking / bonding; validator status & lifecycle; compliance flag (C); participation gate (`ACTIVE & C=1 & EP>0`) + reward emission; invariants V-1…V-9; delegation caps / overflow | **on-chain (native module)** |
| Participation Score **result** (the score that gates) + Empirical Points | **on-chain** (recorded) |
| Heavy PS / reputation **computation** (multi-signal, historical) whose output feeds overflow / rewards | **off-chain Tier-1** (anchored + ratified on-chain) |
| Binding DAO vote tally (delegated vote tokens) | **on-chain** |
| KPI: the **gating** part (what decides eligibility/reward) | **on-chain** |
| Programmable dApps; AI-compute marketplace | **smart contract (CosmWasm)** |
| Analytics, validator scoreboard, explorer, dashboards, activity archive, alerts, DAO discussion UI, KPI **display** | **off-chain Tier-0** |

## Consequences

- **Cheap by design.** The core stays native (no per-call VM gas); programmability is opt-in via
  contracts; everything derived or heavy moves off-chain.
- **Clear read-surface for the client team.** What is authoritative on-chain (queryable) vs served
  by a backend is now decided — this directly seeds the **validator read-surface** spec.
- **Scoring split deferred to Q4, but framed.** *How much* scoring computation moves off-chain is a
  Q4 decision; this ADR fixes the rule it must follow (money-touching ⇒ Tier-1 verifiable).
- **Backend is a contract, not yet a build.** The off-chain backend component is a Q4 work item;
  this ADR is the standing rule it (and all later features) must satisfy.

## Alternatives considered

- **Everything money-related on-chain, no Tier-1 backend.** Maximally safe, but heavy scoring /
  history on-chain is expensive in gas and state for a Layer-1 — rejected as the default.
- **All off-chain = trusted operator (no verifiability when it touches money).** Simpler/faster,
  but a money-determining trusted oracle undermines the decentralization story — rejected; allowed
  only at Tier-0 (non-money) use.

## References

- Planning call 2026-06-10 (cost discipline, Layer-1, validator read-surface).
- [ADR-0006](ADR-0006-smart-contracts-cosmwasm.md) — the smart-contract (CosmWasm) layer.
