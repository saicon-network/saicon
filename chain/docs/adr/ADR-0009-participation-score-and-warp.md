# ADR-0009: Multi-signal Participation Score and the WARP reputation engine

Status: **Proposed** (2026-07-28). The parameter values recorded here are the adopted
**Validator Parameter Decision Register v1.0** baseline (2026-07-24 —
[DEVELOPMENT.md](../DEVELOPMENT.md) §2.2, lines 95–106; §3.1, lines 234–241); the engine is
unbuilt (roadmap Q4 2026 → v0.3, §4 line 334) and the *Open questions* below are genuinely
undecided, so the ADR as a whole is **not** accepted. Acceptance requires closing them.
Related: [ADR-0007](ADR-0007-onchain-offchain-contract-cost-split.md) (where computation runs),
ADR-0008 (VP + selection — the consumer of WARP), ADR-0010 (slashing, **V-8 revision**),
ADR-0013 (bounded DAO authority).

## Context

`x/participation` ships a **single-signal** Participation Score: `computed_score =
uptimeWeight · uptime`, with `uptimeWeight = 1.0`
(`chain/x/participation/keeper/epoch.go:18`, `:118`) and `uptime = signed/(signed+missed)` over
per-epoch precommit counters (`chain/x/participation/keeper/signing.go:17-53`, `:72-106`). PS
feeds EP, EP gates rewards (V-2). That is enough to demonstrate the PoP cycle and nothing more:

- **Precommit uptime alone is cheap to fake.** A node can sign every block while contributing
  nothing else — no proposals of usable quality, no governance presence. Stage 1 of the
  calibration program measured the consequence directly: under the v1 smoothing settings,
  **43.8%** of *selective-participation* validators (high uptime, otherwise passive) were
  falsely accepted, and **2.67%** of plainly weak validators (DEVELOPMENT.md §3.2, line 270).
- **A raw per-epoch score is too noisy to gate consensus seats.** One bad day should not evict
  a good validator; one good day should not rehabilitate a bad one. v1 proposed a symmetric
  EMA (α ≈ 0.3) — symmetric smoothing gives an attacker the same recovery speed as an honest
  operator, which is exactly the wrong incentive.
- **The v1 four-signal split was superseded.** v1 specified `0.45/0.25/0.20/0.10`; the Register
  replaced it with a three-signal split (DEVELOPMENT.md §2.11, lines 205–206).

Reputation is also load-bearing beyond rewards: the Register makes WARP a factor of voting
power (`VP = min(bonded, cap) · WARP/100 · C`, §2.3 line 113) and the eligibility predicate of
the active-set pool (§3.1 line 247). This ADR owns **producing** PS and WARP; ADR-0008 owns
consuming them.

## Decision

### 1. Participation Score (per epoch, raw)

```text
PS = 0.50·Uptime + 0.35·BlockQuality + 0.15·Governance        each signal normalized 0–100
```

- Weights **0.50 / 0.35 / 0.15**, scale **0–100**, computed once per block-based epoch
  (§2.1 lines 91–93 — epoch is a fixed block count, never wall-clock).
- Supersedes the v1 four-signal `0.45/0.25/0.20/0.10` (§2.11 line 205).
- Compliance, anti-cartel and green are **not** PS signals — they are separate layers
  (§2.0 line 87). PS measures participation quality only.

### 2. WARP — asymmetric smoother

```text
PS <  WARP :  WARP ← WARP − 0.65·(WARP − PS)      fast fall
PS ≥  WARP :  WARP ← WARP + 0.10·(PS − WARP)      slow recovery
eligible ⇔ WARP ≥ 65 ;  a new validator starts at WARP = 80
```

The asymmetry **is** the decision, not an implementation detail. A symmetric filter treats
losing and regaining standing as equally expensive; SAiCoN wants reputation to be **cheap to
lose and expensive to regain**, so that a validator's rational strategy is continuous
participation rather than periodic minimum-effort recovery. The 0.65/0.10 pair was the single
Pareto point of Stage 1 (384 weight×speed combinations → 44 admissible → 1 selected).

### 3. Telemetry policy

- A **confirmed network-wide outage freezes WARP** — no update at all, in either direction.
  Punishing every validator for an event none of them caused is a false positive that the
  protocol must not produce (Stage 7's `global_outage` scenario passed with **0% false
  exclusion** of good validators, §3.3 line 289).
- The **first** epoch with missing telemetry gets **grace** (treated as no evidence, not as
  bad evidence). **Continued** missing telemetry scores **0** — silence must not be a cheaper
  strategy than reporting.

### 4. Governance surface

PS weights and WARP parameters are DAO-tunable **within bounds** and versioned; the DAO may not
bypass or disable the participation layer (§2.10 lines 197–199, ADR-0013). Today
`x/participation` Params carry only `epoch_length_blocks` and `ep_decay`
(`chain/proto/chain/participation/v1/params.proto:15-20`), so this ADR implies a Params
extension, not just new keeper code.

## Calibration basis — and what it does not prove

| Claim | Evidence | Strength |
|---|---|---|
| 0.65/0.10 beats 0.8/0.2 | selective-participation false acceptance **43.8% → 0%**, weak **2.67% → 0%**, WARP volatility **0.359 → 0.189** (Stage 1, §3.2 line 270) | measured (synthetic behaviour, real balances) |
| Cost of the asymmetry | recovery after a 3-day outage **3 → 10 epochs** (§2.2 line 104) | measured; see the recomputation below |
| Weight **0.15** on governance | *none recorded* — "re-included with no recorded justification" (§3.4 line 304) | **asserted** |
| Initial value **80** | **outside the swept space**; adopted as baseline anyway (§2.2 lines 105–106) | **asserted** |
| Threshold **65** | Register value; no sweep recorded next to it | **asserted** |

The whole program used **synthetic validator behaviour over real stake balances** and, in its
own words, "validates declared formulas and scenarios, not the correctness of production
implementation code" (§3.4 lines 313–316). Every value here is a **DevNet/TestNet baseline**.
🔒 **B2** (§5 line 368) requires a rerun over **≥90–180 real daily epochs** with measured
false-exclusion before these numbers may gate MainNet payouts.

Recomputing the published cost directly from the adopted formula: three
consecutive epochs at PS = 0 take WARP from 80/95/100 down to **3.4 / 4.1 / 4.3**, and exactly
**10** epochs at PS = 100 are then needed to cross 65 again — the "10" reproduces. The
comparator "3" does **not** reproduce under the same assumptions (the 0.8/0.2 pair yields 5),
so treat *3 → 10* as directional and **10** as the load-bearing figure.

## Open questions (must be closed before this ADR can be accepted)

1. **BlockQuality is a name, not a definition.** Neither the Register nor the studies specify
   what is measured. Options: (a) proposer-turn success — proposed/expected turns, derivable
   in consensus from ABCI data; (b) precommit timeliness/latency, which is *not* deterministic
   across nodes and cannot be computed in consensus; (c) an off-chain telemetry composite.
   **Recommendation:** v1 = (a), strictly in-consensus and deterministic; anything needing
   telemetry enters only through the Tier-1 anchored path of ADR-0007.
2. **Governance signal has no data source.** SDK v0.53.6 `x/gov` **deletes votes at tally**
   (`x/gov/keeper/tally.go:94`, `x/gov/keeper/vote.go:63`), so per-epoch participation cannot
   be reconstructed after the fact. Options: record voters at vote time into an epoch-scoped
   set; or compute off-chain Tier-1 and anchor. **Recommendation:** record at vote time
   on-chain; and if that is not shipped by v0.3, **renormalize to 0.59/0.41 over the two
   measurable signals** rather than feed the term a fabricated constant — a weight with no
   input is worse than no weight.
3. **Who confirms a "network-wide outage"?** The freeze is a protective override with obvious
   abuse potential. Options: a deterministic in-consensus rule (freeze when the share of the
   active set below an uptime floor in the same epoch exceeds a param); a governance/multisig
   switch; an off-chain oracle. **Recommendation:** the deterministic rule — governance is too
   slow for a liveness event and an oracle adds a trusted third party to a consensus-critical
   path.
4. **Where WARP is computed.** ADR-0007 routes "heavy PS/reputation computation" to off-chain
   Tier-1, but WARP gates seats and money. **Recommendation:** the gating value is computed
   on-chain natively; only signals that cannot be derived in consensus are imported, anchored
   and ratified per Tier-1.
5. **WARP under non-ACTIVE status.** V-7 produces no PS for a non-ACTIVE validator, so WARP has
   no input during quarantine. Freeze, or decay? **Recommendation:** freeze — consistent with
   lazy EP decay (`epoch.go:98-110`); punishment belongs to the compliance/status layer and to
   ADR-0010, not to the smoother.
6. **Threshold 65 and initial 80** are asserted (above). Keep as baseline, mark
   governance-tunable within bounds, revisit at B2.

## Consequences

### The gap between this ADR and the running code

- **PS is single-signal today.** `uptimeWeight = 1.0` (`epoch.go:18`) and
  `score := uptimeWeight.Mul(uptime)` (`epoch.go:118`). Two of the three Register signals have
  no producer at all. The weighted-sum structure and the snapshot proto were left open for
  exactly this (`epoch.go:56-59`), so this is an extension, not a rewrite.
- **Scale mismatch.** `computed_score` and `uptime_fraction` are `LegacyDec` on **0–1**
  (`chain/proto/chain/participation/v1/types.proto:19-28`); the Register's PS and the WARP
  threshold 65 are on **0–100**. Either the snapshot changes scale (state-breaking) or the
  conversion is pinned in one place; the choice must be explicit, not incidental.
- **WARP does not exist anywhere in the chain.** No type, no store, no param, no query — the
  only occurrences in the repo are historical mission notes
  (the engineering organisation's records, "WARP deferred").
  Everything that reads WARP today (VP, the selection pool, the Register's double-sign rule)
  is therefore also unbuilt.
- **The snapshot proto has no room for WARP or per-signal values.** `ParticipationScoreSnapshot`
  carries `blocks_signed/missed`, `votes_participated`, `uptime_fraction`, `computed_score`
  (`types.proto:13-33`). Recording per-signal contributions plus the post-update WARP requires
  new fields (append-only) and a genesis/export path for the WARP store.
- **Params carry no weights.** `DefaultParams()` is `(epoch_length_blocks=100, ep_decay=0)`
  (`chain/x/participation/types/params.go:19`). The DAO cannot tune what does not exist; §2.10
  is aspirational until Params grow.
- **The EP decay engine ships but is off.** `ep_decay = 0` by default (`params.go:19`), and
  `decayEP` no-ops on zero (`epoch.go:28-39`). WARP and EP decay are two different
  time-discounting mechanisms; if both are ever enabled, the combined incentive has never been
  simulated — Stage 1 swept WARP only.

### Interaction with V-7 and V-8

- **V-7 propagates to WARP for free.** PS snapshots exist only for ACTIVE validators — enforced
  by construction (`epoch.go:98-110`) and by a fail-loud EndBlock panic
  (`chain/x/participation/keeper/invariants.go:27`, `chain/x/participation/module/module.go:198-201`).
  A WARP update driven by PS inherits that gate: a quarantined validator simply stops receiving
  updates. This is the freeze recommended in open question 5, and it means a validator can
  emerge from quarantine with its pre-quarantine WARP intact — deliberate, but it must be
  stated, because it is the case ADR-0010 will want to override.
- **V-8 blocks the Register's double-sign rule.** V-8 says punishment never mutates EP
  (DEVELOPMENT.md §1.3 line 55); the Register requires **WARP/PS → 0 on double-sign**
  (§2.6 line 162). Those are incompatible as stated for PS, and WARP is not covered by V-8 at
  all. **ADR-0010 owns that revision**; this ADR deliberately does not perform it and does not
  write a punishment path into `x/participation`.
- **V-8 is weaker in code than the docs claim.** §1.3 describes the invariants as "EndBlock
  assertions that panic", but there is no `V-8` runtime assertion: the five invariant functions
  in the tree are V-1, V-2, V-3, V-5, V-7
  (`chain/x/validatorregistry/keeper/invariants.go:25`, `:74`, `:188`,
  `chain/x/rewards/keeper/invariants.go:28`, `chain/x/participation/keeper/invariants.go:27`).
  V-8 holds by **single-writer construction** — `SetEP` has exactly one non-test caller,
  `ComputeEpoch` (`chain/x/participation/keeper/scores.go:34` ← `epoch.go:163`) — plus one unit
  test (`chain/x/participation/keeper/ep_v8_test.go:21`). Implementation debt to record, and a
  constraint on ADR-0010: revising V-8 means revising a convention and a test, not a runtime
  check.

### Operational consequences of the asymmetry

- **A single half-bad epoch can deactivate a validator.** From WARP = 90, one epoch at PS = 50
  gives `90 − 0.65·40 = 64` — below the eligibility threshold immediately — and **13** epochs
  of perfect participation to return to 90. Operators must understand
  that the eligibility line is roughly "one bad day away" at any WARP below ~100.
- **The filter is downward-biased under noise.** With PS = 100 and one dead epoch every 30
  (mean PS 96.7), long-run mean WARP is **78.9** with troughs at **33.9** — i.e. a validator
  with 96.7% mean quality spends part of every month ineligible. This is intended pressure, but
  it is also the mechanism most likely to be misread as a bug by honest operators.
- **New validators are exposed.** Starting at 80, a single dead first epoch drops WARP to
  **28** and costs **7** clean epochs before first eligibility. Today's "benefit of the doubt"
  default (`signing.go:92-95`: `total == 0 ⇒ uptime = 1`) covers a validator that is not yet in
  the set, but not one that is in the set and misses everything.
- **Outage exposure is asymmetric by design.** A 3-day outage costs ~10 epochs of ineligibility
  (≈10 days at a 24h epoch) — acceptable for a chain that pays for participation, expensive for
  a validator with a hardware failure. The telemetry-freeze rule is what keeps this from firing
  on *network-wide* events; it does nothing for single-operator outages, which is the point.

### Cost and scope

- `ComputeEpoch` already iterates the full validator set in one block
  (DEVELOPMENT.md §2.1 line 93). Adding two signals plus a WARP read/write per validator makes
  the epoch-boundary block heavier — this feeds 🔒 **B1** (consensus benchmark for set 60) and
  must be measured before the production epoch length is chosen.
- Adding WARP to the reward path is **not** in this ADR. Today's gate is three predicates —
  ACTIVE, C = 1, EP > 0 (`chain/x/rewards/keeper/emission.go:108-134`). Rewards become ∝ VP
  (and therefore ∝ WARP) only via ADR-0008/ADR-0011.

## Alternatives considered

- **Keep uptime-only PS (status quo).** Cheapest; rejected on measured grounds — 43.8% false
  acceptance of selective participants (§3.2 line 270). It also leaves "participation pays"
  meaning "signing pays".
- **Symmetric EMA (v1, α ≈ 0.3).** Simpler, one parameter, no fairness argument about
  asymmetry; rejected because equal recovery speed removes the deterrent — a validator can
  alternate cheating and repairing at no net cost.
- **Symmetric-fast 0.8/0.2.** The Stage 1 comparator: faster recovery (5 epochs by our
  recomputation vs 10), but 43.8%/2.67% false acceptance and WARP volatility 0.359 vs 0.189.
  Rejected: the false-acceptance rate is the thing the score exists to prevent.
- **Hard threshold with no smoothing** (evict whenever PS < 65 in any epoch). Maximum
  responsiveness, no state to carry; rejected — a single network hiccup would evict honest
  validators, and the metric would be pure noise at epoch granularity.
- **Retain the v1 four-signal split.** Rejected with the rest of the v1 economic model
  (§2.11); the fourth signal had no defined producer either, and three signals with two
  measurable is already one too many (open question 2).
- **Start new validators at WARP = 65 (at the line) or 100 (clean slate).** 65 makes the first
  epoch decisive and punishes cold starts; 100 hands newcomers maximum voting power before any
  evidence exists. 80 is the Register's compromise — asserted, not swept, and recorded here as
  such.

## References

- [DEVELOPMENT.md](../DEVELOPMENT.md) — §2.0 (lines 79–87), **§2.2** (95–106), §2.6 (162),
  §2.10 (197–199), §2.11 (205–206), **§3.1** (234–241), **§3.2 Stage 1** (270), §3.3 (289–291),
  **§3.4 honesty** (300–316), §4 Q4 (333–340), §5 **B2** (368).
- Code: `chain/x/participation/keeper/epoch.go`, `signing.go`, `invariants.go`, `scores.go`,
  `chain/x/participation/module/module.go`, `chain/x/participation/types/params.go`,
  `chain/proto/chain/participation/v1/{params,types}.proto`,
  `chain/x/rewards/keeper/emission.go`, `chain/x/compliance/keeper/hooks.go`.
- [ADR-0007](ADR-0007-onchain-offchain-contract-cost-split.md) — on-chain / off-chain Tier-1
  placement rule for scoring computation.
- ADR-0008 (VP + selection, consumes WARP), ADR-0010 (slashing + **V-8 revision**),
  ADR-0011 (emission), ADR-0013 (bounded DAO authority) — to be written.
