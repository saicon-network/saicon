# ADR-0012: Entry threshold, staking caps, the queue, and entity limits

Status: **Proposed** (2026-07-28) — target module `x/stakeengine`, Q4 2026 (v0.3), see
[DEVELOPMENT.md](../DEVELOPMENT.md) §4.
Source of the parameter values: **Decision Register v1.0** (adopted 2026-07-24),
[DEVELOPMENT.md](../DEVELOPMENT.md) §2.4 (lines 121–135), evidence §3.2–§3.4, blockers §5.
Related: [ADR-0004](ADR-0004-validator-activation-model.md) (activation), ADR-0005 (delegation
router — **not yet written**), ADR-0008 (selection layer / lottery), ADR-0013 (bounded DAO),
ADR-0014 (entity registry).

This ADR is **proposed, not accepted**, even though the Register adopted most of its numbers
wholesale. Two quantities the decisions are written in terms of — *the base of the per-validator
cap* and *public circulating supply* — have no definition (both 🚧 in §2.4), and the queue's
ordering policy is an open approval gate (Q6, §5 line 382). A cap expressed in terms of an
undefined quantity cannot be enforced, so the decision cannot be called accepted until §"Open
problems" below is closed by the owner.

## Context

SAiCoN's influence model is `VP = min(bonded, validatorCap) × WARP/100 × C` (§2.0, §3.1). The
`min(…)` term is not decoration: without it, voting power is stake-weighted and the whole
Proof-of-Participation story collapses into stock Cosmos behaviour. **Today that term does not
exist** — when this ADR was written, DEVELOPMENT.md §1.4 stated it plainly: *"no caps of any
kind (no stake check at registration at all)"*. S6 built the caps (see Implementation, below).

Four different concentration problems are being solved here, at four different scales, and they
are easy to conflate:

| Scale | Question | Control |
|---|---|---|
| Who may start at all | is this operator committing real capital? | entry threshold |
| The whole network | how much of the token can be bonded at once? | global bonded cap + queue |
| One validator address | how large may one node get? | per-validator cap |
| One real-world owner | how much can one entity control across many nodes? | entity caps |

Stage 4 of the calibration program (§3.2 line 273) is the reason the last row exists: with no
seat cap, a modelled attacker reached **53.8% VP** and `P(VP ≥ 33%) = 100%`; with a 2-seat cap
per entity, **3.2%** and `P(VP ≥ 33%) = 0%`. Stage 3 (line 272) is the reason the third row
exists: a 2% per-validator cap held Top-10 at 22% against 28% at 3%.

The Register set every value on 2026-07-24. This ADR's job is to **record** them with their
calibration basis and flags, and to own the two gaps rather than write around them.

## Decision

### D1 — Entry counts owner self-bond only

Entry to the registry counts **the operator's own self-bond**. Delegations never count toward
the entry threshold. Floor: **2,000 SCN** (project decision 2026-09-22, lowered from the
provisional 3,000), set against the 112-candidate holder
snapshot. The adaptive ladder from v1 is retained under **🔒 governance review** (§2.4 line 124).

Rationale for self-bond-only: if delegations counted, one pool of tokens could be recycled as
delegations across many candidate nodes, manufacturing entry for an entire cartel at the cost of
a single stake. Self-bond is capital the operator cannot lend to himself, and it is already
immobilised for 60 days by V-3 (ante-enforced, §1.3). Entry threshold and lock-up are one gate,
not two.

*Calibration basis (honest):* **weak, and the number has since moved without one.** S-19 swept
the ladder and recommended keeping 3,000; the floor was lowered to 2,000 by project decision
afterwards, so it is a judgement made against the study rather than one the study produced.
No sweep of the entry
threshold against attacker cost appears in §3.2 (Stage 3 swept delegation/concentration, Stage 4
swept entity caps — neither swept entry). It is a number chosen so that a workable share of real
holders clears it, not a security margin. For scale: 112 × 2,000 = 224,000 SCN, 25.7% of the
global cap.

### D2 — Global bonded cap, and a queue at the cap

```
globalBondedCap = min( 0.08 × maxSupply , 0.60 × publicCirculatingSupply )
```

At the current snapshot ≈ **870,000 SCN**, where the **60% guardrail binds**: `0.08 × 21,000,000
= 1,680,000`, so the public-circulating arm is the smaller and therefore the operative one
(§2.4 lines 125–127).

At the cap:
- net new stake is **queued**, not rejected;
- **redelegation stays allowed** (it moves stake, it does not add net stake);
- **replacement of excluded validators stays allowed** (an exit frees capacity; the queue fills it).

*Calibration basis:* the formula is a policy choice, not a swept result — it does not appear in
the Stage 1–7 headline results (§3.2). The `0.08 × maxSupply` arm follows the v1 8–10% band
(§2.11 line 209). 🚧 **The second arm is undefined** — see Open problem 2.

🔒 **Queue ordering policy is an open approval gate (Q6, §5 line 382).** The Register decided
*that* there is a queue, not *how it orders*. FIFO, pro-rata, and priority-by-quality have
materially different concentration outcomes and are not compared anywhere in §3.

### D3 — Per-validator cap = 2% of total bonded

`validatorCap = 0.02 × totalBonded`. This is the `min(bonded, validatorCap)` term of VP.

*Calibration basis:* Stage 3 (§3.2 line 272) — at 2%, Top-10 = 22% and delegation acceptance
falls 41.5% → 24%; at 3%, Top-10 = 28%. *Honest:* §3.4 line 306 records that **no comparison was
run against the superseded absolute cap (~10,000 SCN)**; the ratio form was adopted without that
head-to-head. At the global cap the ratio yields 0.02 × 870,000 = **17,400 SCN**, i.e. the same
order of magnitude as the absolute value it replaced.

🚧 **The base is not pinned** — see Open problem 1. This is not a detail: §3.3 line 285 records
that this exact ambiguity produced the `stake_exit` stress failure.

### D4 — Entity caps

Per **verified entity** (an entity being one beneficial owner, however many validator addresses
it operates):

| Limit | Value | Basis |
|---|---|---|
| Aggregate stake | **5%** | Stage 4 sweep {5, 7.5, 10, none}% → attacker VP {3.2, 3.2, 4.0, 4.4}% |
| Active seats | **max 2** | Stage 4: max attacker VP **53.8% → 3.2%**, `P(VP≥33%)` **100% → 0%** |
| Lottery tickets | **1** | §2.3 line 111; constraint exported to ADR-0008 |

Entity caps are applied **before** both the deterministic Top-K ranking and the lottery (§2.3
line 112, §3.1 line 249).

*Honest reading of the evidence:* the **seat cap is the load-bearing control, not the 5%**. The
stake-cap sweep moves attacker VP only from 4.4% (uncapped) to 3.2% (5%), and §3.4 line 305
records 5% vs 7.5% as a **near-tie**; the seat cap moves it from 53.8% to 3.2%. Both, however,
are conditional on knowing which addresses share an owner — which is B3, and which does not
exist. The measured effectiveness of the entire anti-cartel layer is an artefact of the
simulation being told the ground truth.

### D5 — Delegator-side limit

A delegator may split across **at most 3 validators** (§2.4 line 135, §2.8 line 180).

*Honest:* §2.8 line 183 and §3.4 line 307 both record "≤3" as an **asserted knee point with no
chart**. Adopted as baseline anyway.

### Placement

All of the above is consensus-critical and money-determining, so by the ADR-0007 placement rule
(step 1, which names "delegation caps/overflow" explicitly) it is an **on-chain native Go
module**: `x/stakeengine`. No part of cap enforcement may live off-chain or in a contract.

## Open problem 1 (🚧) — what is the base of the 2% cap?

"2% of total bonded" has two readings, and §3.3 line 285 records that leaving it unresolved cost
a stress-test failure: a 30% delegation exit drove **Top-10 VP to 36.2%**, above the 35%
guardrail.

**Reading A — 2% of *current* total bonded** (`cap_t = 0.02 × totalBonded_t`).
Failure modes:
1. *Self-reference and procyclicality.* Bonded falls 30% ⇒ the cap falls 30% ⇒ every validator
   at the ceiling is retroactively over-cap. Both resolutions are bad: enforce continuously and
   the protocol force-sheds stake at the exact moment the network is already losing it (a
   margin-call cascade); grandfather existing positions and the ratio the cap was meant to hold
   silently stops holding — which is the `stake_exit` outcome.
2. *Adversarial headroom.* Every 1 SCN bonded anywhere raises **every** validator's ceiling by
   0.02 SCN. An entity that bonds through extra identities enlarges its own per-node ceiling.
   The control moves in the attacker's favour under attack.
3. *Non-determinism at check time.* The ceiling a delegation is validated against changes
   between simulation and execution as other transactions land in the same block. Ordering-
   dependent failures, poor UX, awkward ante logic.

**Reading B — 2% of a *target*** (naturally the global cap: `0.02 × 870,000 = 17,400 SCN`).
Stable, deterministic, queryable, ante-friendly, no self-reference. Failure modes:
1. *Structurally slack.* The queue guarantees `totalBonded ≤ globalCap`, so 2%-of-target is
   **always ≥** 2%-of-current. The cap is loosest exactly in the bootstrap regime where
   concentration is worst: at 300,000 SCN bonded, a validator at 17,400 SCN holds **5.8%** of
   the network while nominally "within the 2% cap". It reaches its stated meaning only at full
   subscription.
2. *Chained undefined quantities.* The target derives from the global cap, which derives from
   public circulating supply — Open problem 2. Two undefined quantities in series.

**✅ DECIDED (owner, 2026-07-28): Reading A, pinned to an epoch-frozen base with a downward ratchet.** Concretely: measure `totalBonded` once at the epoch boundary, store it, and use the
stored value unchanged for the whole epoch (kills failure A3); allow the stored base to rise
freely between epochs but to **fall by at most a governed percentage per epoch** (kills the A1
cascade); and never force-shed a position that went over-cap because the base moved — freeze it
instead (no new stake in, excluded from the lottery until back under cap) and let it drain. This
keeps the cap proportional to real bonded, so it binds during bootstrap, which Reading B does not.

**Failure mode of the recommendation, stated plainly:** during the ratchet window the effective
cap sits above 2% of the then-current bonded. It converts an instantaneous cascade into a
bounded window of elevated concentration; it does not eliminate the window. Sizing the ratchet
is a calibration task, not a design one.

**🔬 Stage 11 (2026-07-29) refines this with data — needs owner confirmation.** The decision above
applies one ratcheted base to everything. Stage 11 ran the shock explicitly (15,000 exit runs,
seed 20260729) and found the base should be split in two:

| Layer | Base | Purpose |
|---|---|---|
| Delegation **acceptance** | 2% × **EWMA-30 of total bonded** | smooths intake, kills the churn the ratchet also targets |
| **Effective** VP and reward cap | 2% × **current eligible bonded** | preserves concentration protection while stake is leaving |

The point is the second row. Under a 30% peripheral bonded exit at active set 75, using the
smoothed base as the *effective* cap gives Top-10 VP p95 = **34.7%**, worse than letting the
effective cap track current bonded at **33.7%** — because a lagging base keeps a stale, larger
ceiling alive exactly while the denominator shrinks. Smoothing is right for intake and wrong for
the ceiling that VP is measured against. The epoch-frozen ratchet is a smoothed base, so applying
it to both layers reproduces the worse number.

Above-cap treatment matches this ADR: no slash, no forced unbond — stop new delegation, cap the
counted stake, drain via redelegation or the 21-day unbonding.

Same-denominator rule: the **entity** cap must use the same two bases, or the two caps can be
satisfied simultaneously against different denominators.

Stage 11 also found a **source discrepancy worth fixing before implementation**: Stage 3's code
computed the cap from `target_bonded = min(globalCap, stakeDemand)` while the Register described
it as a percentage of total bonded. Stake demand silently substituted for realized bonded stake.
The production spec must state the denominator explicitly.

**And a caveat that neither reading fixes:** a cap on *stake* does not bound a share of *VP*.
`VP = min(stake, cap) × WARP/100 × C`, then normalized — when validators are excluded (WARP < 65,
C = 0, jailed) or exit, the denominator `Σ VP` shrinks and the capped top cohort's *share* rises
without any of them gaining a token. That is the arithmetic gap between Stage 3's cap-only model
(Top-10 = 22%) and Stage 7's integrated run (36.2%). **Pinning the base is necessary and not
sufficient; B5 needs a VP-side control.**

## Open problem 2 (🚧) — "public circulating supply" is not defined anywhere

The global cap's binding arm is `0.60 × publicCirculatingSupply`, and no on-chain definition of
that quantity exists. Working backwards from the Register's own figure: `870,000 / 0.60 =
1,450,000 SCN`, which is **8.35%** of the `CurrentSupply ≈ 17,375,600` reported by Stage 5 (§3.2
line 274). Whatever produced the snapshot excluded roughly **15.9 million SCN (91.7% of supply)**
by a rule that is recorded nowhere.

**Option A — a governance parameter.** Trivial to implement, auditable through proposal history,
available today. Rejected as the primary mechanism: it makes a *measurement* into a governance
knob, and thereby hands governance a lever on the global bonded cap. §2.10 (line 198) forbids the
DAO from lowering the entry floor precisely to keep PoP non-capturable; letting it hand-enter the
number that sets the cap re-opens that door through a side entrance, and the value is stale
between proposals.

**Option B — computed on-chain (recommended), over *ownership*, not custody:**

```
publicCirculating = totalSupply(nscn)
                  − Σ balances of module accounts
                      (bonded_tokens_pool, not_bonded_tokens_pool, distribution/community pool,
                       fee_collector, gov, mint, the x/rewards pool, IBC escrow accounts,
                       and the D2 queue escrow)
                  − Σ unvested( vesting accounts )        // OriginalVesting − vested(blockTime)
                  − Σ holdings of governance-listed treasury / foundation / team / ecosystem
                      accounts, counting their delegations at the owner (shares → tokens)
                  + Σ delegations of non-excluded owners   // staked-but-publicly-owned tokens
```

The last line is not a refinement, it is the load-bearing correction. **The naive formula
"total supply minus module accounts" is self-defeating**, because bonding moves tokens into
`bonded_tokens_pool`, a module account. If bonding shrinks public circulating, it shrinks the cap
that limits bonding. Solving `B = 0.60 × (S − B)` gives `B = 0.375 S` — the "60% of public
circulating" rule silently becomes "37.5% of total supply" (≈ 6.5M SCN at today's supply, ~7.5×
the intended 870,000). The same trap applies to the queue escrow this ADR introduces: queued
stake is publicly owned and must keep counting, or the queue shrinks the cap that created it.

Option B does not remove governance from the loop — it **narrows** governance input from *a
number* to *a set of addresses*. That is the argument for it: anyone can independently recompute
the balances; only membership is a decision, and membership is monotone-checkable and diffable
across upgrades.

**Recommendation:** Option B, exposed as a query endpoint so the cap is externally verifiable,
with the exclusion address set governed under ADR-0013 bounds. Until this is defined, **D2 is
recorded but unenforceable** — there is nothing to compute the cap from.

## Named but not designed

### B5 — the ">35% Top-10" rebalancing trigger is a name, not a mechanism

§2.4 line 131 and §5 line 371 both carry it; no mechanism exists in the Register or in any study.
Designing it is **out of scope for this ADR** (it belongs with the VP/selection layer, ADR-0008,
and the reserve pool, B6). For whoever does design it, the minimum it must answer:

- **What is measured** — Top-10 share of VP or of bonded stake, instantaneous or rolling over an
  epoch window (an instantaneous trigger on a normalized share will flap).
- **Who acts** — protocol-automatic, governance proposal, or alert-only.
- **What the action is** — it cannot confiscate stake. The realistic set is: freeze new
  delegations to over-threshold validators and route them through the delegation router
  (ADR-0005); clamp the top cohort's VP so the *normalized* share returns under 35% without
  touching stake; or widen the denominator by admitting reserve candidates (that is B6).
- **Release condition and hysteresis** — trigger at 35%, release lower, or it oscillates.
- **Interaction with D2** — a rebalance that pushes stake out while the queue pulls stake in is
  self-contradictory; the precedence must be written down.
- **Determinism** — any VP clamp is a consensus-weight change and must be bit-identical on every
  node.

Acceptance criterion is already fixed by §5 line 371: after a 30% delegation exit, Top-10 VP ≤ 35%
with caps still consistent.

### B3 — beneficial-owner attestation before first activation

The Register makes **verified-entity linkage a precondition of activation**, with detection as
the *second* line, not the first (§2.4 lines 133–135). This ADR records that ordering as binding
on D4: entity caps are unenforceable without it, because "5% per entity" and "2 seats per entity"
are statements about a mapping the chain does not have.

The measurement behind the ordering — record it, because it is the whole argument:

- `hidden_cartel_20` (§3.3 line 286): one concealed 20% entity across 10 nodes held **35.4% VP
  for up to 13 days** before detection; **P95 detection day 84.8**; post-containment ≤ 2.3%.
- `combined_severe_33` (§3.3 line 287): attacker VP peaked **58.7%** and stayed **above 33% for
  up to 84 days**.
- Detection is not a reliable backstop at all: §2.7 line 174 / §3.2 line 273 record that a
  **low-signal cartel evades quarantine ~91% of the time within 12 weeks**, and B4 flags the
  current thresholds as synthetic.

So the range of attacker voting power above 33% before containment is **13 to 84 days**, with P95
detection landing at day ~85 and later in the combined scenario. *(Documentation note: §3.3
restates only the 84.8 figure; the Register's detection-latency summary carries a P95 window of
day 85–128 across the cartel scenarios. The discrepancy is in the source material, not resolved
here.)* A chain that can be 33%-controlled for two to twelve weeks before anyone notices does not
have a detection problem, it has an admission problem. Attestation-first is the conclusion the
measurement forces.

Design of the registry, the attestation evidence ladder, and the appeal path is **ADR-0014**.

## Consequences

**None of this exists in code.** Verified on `main`, 2026-07-28:

- **No stake check of any kind at registration.**
  `chain/x/validatorregistry/keeper/msg_register_validator.go:19-74` validates exactly three
  things: (1) the creator address parses (`:20-23`); (2) no metadata already exists for that
  operator (`:26-32`); (3) the requested lock-up is ≥ the 60-day default, with 0 meaning
  "default" (`:40-47`). It then writes `ValidatorMetadata{Status: BONDING, …}` and emits the
  birth event. It never reads a balance and never reads a stake — the keeper *holds* both
  `bankKeeper` and `stakingKeeper` (`keeper/keeper.go:30-31`) and neither is consulted. The
  message type itself has no amount field: `chain/proto/chain/validatorregistry/v1/tx.proto`
  `MsgRegisterValidator` carries only `creator` and `lockup_duration_seconds`. **D1 is
  unenforced, and there is no field to enforce it in.**
- **Activation is equally ungated.** `keeper/activation.go:32-81` promotes on the staking
  `AfterValidatorBonded` hook (ADR-0004) on the basis of *status alone*; the not-found branch
  (`:40-63`) auto-creates and activates a validator that never registered at all. **B3's
  "attestation before first activation" has no hook point today.**
- **No `x/stakeengine`, no entity registry, no queue.** `chain/x/` contains exactly four modules:
  `compliance`, `participation`, `rewards`, `validatorregistry`. `x/validatorregistry`'s `Params`
  is an empty struct (`types/params.go:4-17`), so there is not even a parameter to hold a cap in.
- **No supply or bonded-total measurement.** `grep` for `TotalBondedTokens|GetSupply|BondedRatio|
  StakingTokenSupply` over `chain/**/*.go` returns nothing outside tests. Both cap formulas
  reference quantities the chain never computes.
- **`MinSelfDelegation` is 1 base unit** (1 nscn) in `chain/cmd/saicond/cmd/testnet.go:109` —
  the only stake-ish floor anywhere in the tree, and it is effectively zero. This is a genuine
  contradiction with D1's 2,000 SCN and should be raised to the entry floor when D1 lands, or
  D1's floor will be enforceable only in `x/stakeengine` and trivially bypassed by registering
  directly through `x/staking`.

Consequences of the decision itself:

- **The queue is a new escrow surface.** Queued stake must be held somewhere, must be
  refundable, must survive upgrades, and — per Open problem 2 — must not be subtracted from
  public circulating. Q6 (🔒) governs its ordering.
- **Caps create overflow, and overflow needs a router.** D3 and D4 both produce stake that
  cannot land where the delegator aimed it. Redistribution is **ADR-0005 (delegation router)**,
  which is *still unwritten* — §6 line 395 lists it as "planned → Q4 (Register content)", and
  §2.8 already carries the Register's content for it (decentralization-aware overflow, ≤3 split,
  min delegation 100 SCN). **D3/D4 cannot ship before ADR-0005 exists**, or capped stake has
  nowhere to go.
- **Every cap value in D1–D5 is a DevNet baseline.** §3 line 228 and §7 line 420 are explicit:
  synthetic behaviour over real balances, and only 5 seeds in the integrated Monte Carlo, whose
  own note says that is "enough for integration debugging, not production tail-risk". None of
  these numbers has met a real validator.
- **Two of the four controls are honestly weak on their own.** The 5% entity stake cap moves
  attacker VP by ~1.2 points; the delegator ≤3 limit has no chart behind it. They are kept
  because they are cheap and harmless, not because they were shown to work.
- **The cap parameters are DAO-tunable within bounds** (§2.10 line 197: "cap percentages"), but
  the *base definitions* settled here must not be — a governance-movable cap base would restore
  exactly the capture vector Open problem 2 rejects.

## Alternatives considered

- **No caps; rely on the selection layer and the VP formula alone.** Rejected on Stage 4
  evidence: without a seat cap the modelled attacker reached 53.8% VP with `P(VP≥33%) = 100%`.
  The `min(bonded, cap)` term is not removable.
- **An absolute per-validator cap in SCN (~10,000, the v1 value) instead of a ratio.** Rejected
  by the Register, though §3.4 line 306 records that **no comparison was run**. Worth naming
  because it has precisely the property Open problem 1 is fighting for — a stable, self-reference-
  free base — and precisely the weakness of Reading B: it does not track network growth. It
  should be revisited if the ratchet in Open problem 1's recommendation proves unstable.
- **Reject rather than queue at the global cap.** Simpler (no escrow, no ordering policy, no Q6
  gate), but it turns the cap into a race: transactions fail unpredictably at the boundary and
  the outcome is decided by mempool position. Rejected; the queue makes admission explicit and
  orderable.
- **Count delegations toward the entry threshold.** Rejected under D1: one pool of tokens
  recycled as delegations manufactures entry across an arbitrary number of candidate nodes.
- **Enforce a Top-10 ceiling directly as a constitutional per-block rule.** Attractive, and it is
  what B5 gestures at, but a *stake* cap cannot deliver it (see the VP-denominator argument in
  Open problem 1) — the enforcement point is the VP layer, not this module. Deferred to
  ADR-0008 + B5.
- **Enforce caps inside `x/delegationrouter` only, with no separate `x/stakeengine`.** Fewer
  modules, but the entry threshold and the global cap are not delegation events, and per
  ADR-0007 step 1 both are consensus-critical. Rejected: the router consumes cap decisions, it
  should not own them.

## Implementation (S6, October 2026)

`chain/x/stakeengine`, design in the design notes.
Both open problems are closed by project decision: the base of the 2% cap is a 30-epoch
EWMA of total bonded (open problem 1, 2026-09-28) with a floor for small validator sets
(2026-10-04), and public circulating supply is computed on-chain with bonded, unbonding
and queued stake counted as public (open problem 2, 2026-09-28). The queue orders by
validator quality — participation score, then EP — until WARP exists (S11).

- The EWMA-30 base (project decision 2026-09-28) supersedes the 2026-07-28 "epoch-frozen base
  with a downward ratchet": no ratchet is built.
- The Stage 11 effective layer — the cap on current eligible bonded for VP and rewards — is
  not built here; it belongs to the VP layer (S10).

Known limits of the implementation are listed in DEVELOPMENT.md §1.4: the checks live
on the ante path, so `x/group`, `x/gov` and ICA-host message execution are not covered
until a backstop at the staking-hook / pool level lands (hooks alone do not see
delegation amounts or stake that arrives by bonding); IBC escrow is not subtracted; a new validator
cannot enter while the cap is full.

The global cap is measured on the bonded pool only, so stake not yet bonded takes no
headroom (several `MsgCreateValidator` in one block; a delegation to a jailed,
unbonding or out-of-set validator that later bonds; a redelegation from a non-bonded
source to a bonded destination): bonded can exceed the cap. No funds at risk, no halt;
project decision 2026-10-06 — close it before testnet with the pool-level backstop.

Room freed by an undelegation can be taken in the same block by a direct `MsgDelegate`
before the queue fills at EndBlock, so the quality-ordered queue gets what is left:
accepted for now (project decision 2026-10-06), revisit before testnet.

## References

- [DEVELOPMENT.md](../DEVELOPMENT.md) §2.4 (lines 121–135) — the decisions recorded here;
  §1.4 — the S6 known-gaps paragraph (the earlier "no caps of any kind" wording is gone); §2.8 — delegation/overflow (ADR-0005 content);
  §2.10 — bounded DAO; §2.11 lines 209–211 — superseded v1 values; §3.2 lines 272–274 —
  Stages 3, 4, 5; §3.3 lines 285–287 — the three stress failures; §3.4 lines 305–307 —
  measured vs asserted; §5 lines 369, 371, 382 — B3, B5, Q6; §4 line 334 — Q4 scope.
- `chain/x/validatorregistry/keeper/msg_register_validator.go:19-74` — what registration
  actually validates.
- `chain/x/validatorregistry/keeper/activation.go:32-81` — ungated activation (ADR-0004 hook).
- `chain/x/validatorregistry/types/params.go:4-17` — empty params.
- `chain/cmd/saicond/cmd/testnet.go:109` — `MinSelfDelegation: math.OneInt()`.
- [ADR-0007](ADR-0007-onchain-offchain-contract-cost-split.md) — placement rule (step 1 names
  delegation caps/overflow as on-chain native).
- [ADR-0004](ADR-0004-validator-activation-model.md) — the activation hook B3 would have to gate.
- ADR-0005 (delegation router), ADR-0008 (selection + randomness), ADR-0013 (bounded DAO),
  ADR-0014 (entity registry) — all unwritten; D3/D4 depend on ADR-0005 and ADR-0014.
