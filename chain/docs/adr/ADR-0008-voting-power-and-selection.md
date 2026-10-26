# ADR-0008: Voting power, active-set selection, and the lottery randomness source

Status: **Proposed** (2026-07-28). The VP formula and the shape of the selection layer are
*recorded* from the adopted Decision Register v1.0 ([DEVELOPMENT.md](../DEVELOPMENT.md) §2.0,
§2.3 — lines 84–87, 108–119). The **lottery randomness source is undecided (blocker B11)** and
this ADR only *recommends* one. Because 22 of 75 consensus seats depend on that undecided
source, the ADR cannot be accepted as a whole.
Origin: Register v1.0 (2026-07-24, adopted wholesale by project decision) + the code-review
finding that opened B11 (DEVELOPMENT.md:310–312, 377).
Related: [ADR-0004](ADR-0004-validator-activation-model.md) (activation), ADR-0009 (PS + WARP —
supplies the `WARP` input), ADR-0011 (emission — owns the pool VP divides), ADR-0012 (caps —
supplies `validatorCap` and the entity caps), ADR-0014 (entity registry — supplies "verified
entity", **not yet written**), ADR-0005 (delegation router — **not yet written**; owns part of
the B5 hand-off below).

## Context

SAiCoN's premise is that **participation, not stake size, decides influence**
(DEVELOPMENT.md:17–18). At the consensus layer the chain currently contradicts that premise:
voting power is stock stake-weighted top-N from `x/staking`, with no selection layer at all
(DEVELOPMENT.md:61). Making the premise real requires two things defined together:

1. a **scalar per validator** that consensus weight and reward share both read, so that
   "influence" and "pay" cannot drift apart; and
2. a rule for **which validators hold seats at all** — a BFT chain cannot run the 112 candidate
   validators of the calibration snapshot as an active set, so seats are scarce and must be
   awarded by a stated rule rather than by stake ordering alone.

The founder spec v1 answered with `EVP = stake × M_PS × P_conc × C` and an active set anywhere
in 50–300 (DEVELOPMENT.md:207–208). Stages 2, 3 and 4 of the calibration program replaced both.

A purely deterministic Top-K set ossifies: a validator outside the top seats never signs blocks,
therefore never accrues the participation record that ranking is computed from, therefore never
enters. The lottery is the mobility mechanism that stops that loop. But a lottery needs an
entropy source that its own beneficiaries cannot steer — and SAiCoN has none. The Register
decided every parameter of the lottery except the one property that makes it a lottery.

## Decision

### 1. Voting power

Recorded from DEVELOPMENT.md:84 and :113–114:

```text
VP = min(bondedStake, validatorCap) · WARP/100 · complianceFlag        (C ∈ {0,1})
```

- VP is **normalized** across the selected active set and written as the ABCI
  `ValidatorUpdate` power.
- **Rewards ∝ final VP** — the same scalar. **No tiers, no second KPI multiplier.**
- The EP gate stays underneath: only `ACTIVE ∧ C=1 ∧ EP>0` receives anything (invariant V-2,
  DEVELOPMENT.md:259).
- Pool membership additionally requires `WARP ≥ 65` (DEVELOPMENT.md:247); `WARP` starts at 80
  for a new validator and moves by the asymmetric rule in ADR-0009.

🔒/🚧 flags carried from the Register: the whole of §2.3 is **🔜 (largest build)**; **🔒 B1** —
set size 60 needs a consensus benchmark before MainNet; **🚧 B11** — the lottery randomness
source is undefined. The Register is a **DevNet/TestNet baseline and does not authorize real
MainNet payouts** (DEVELOPMENT.md:72–73).

### 2. VP replaces EVP, and the concentration adjustment is deliberately dropped

The Register states it in one line: *"VP replaces the old EVP concept; the concentration
adjustment is dropped — concentration is handled by caps"* (DEVELOPMENT.md:86–87), under the
standing rule that *"compliance, anti-cartel and green are separate layers, never PS weights"*.

Why that is the better factoring:

1. **Hard control beats soft control.** `P_conc` is a soft penalty: it lowers the marginal
   return on concentration without bounding it. `min(bonded, validatorCap)` truncates. The hard
   control is also the one that has measurements behind it — Stage 3: per-validator cap 2% puts
   Top-10 VP at 22% against 28% at 3% (DEVELOPMENT.md:272); Stage 4: an entity seat cap of 2
   takes max attacker VP from **53.8% → 3.2%** and `P(VP ≥ 33%)` from **100% → 0%**
   (DEVELOPMENT.md:273). No comparable measurement exists for any `P_conc` curve.
2. **VP stays local.** A concentration term depends on *other* validators' stakes, so a
   validator's power — and, since rewards ∝ VP, its income — would move when someone else bonds
   or unbonds. That is neither explainable to an operator nor stable enough to underwrite pay.
3. **It double-counted.** Caps truncate the same quantity `P_conc` penalized, so tuning either
   one silently changed the other's effective strength.
4. **Layer separation.** Concentration control now lives with caps and entity rules (ADR-0012,
   ADR-0014) where it can be measured, bounded and governed on its own, instead of being an
   unauditable term inside the consensus weight.

The cost of the factoring, stated plainly: with `P_conc` gone, **nothing inside VP resists
concentration** — the entire defence is the cap layer. That layer is not closed. The cap base is
still ambiguous (current vs target bonded, DEVELOPMENT.md:129–130), and that ambiguity is exactly
what produced the `stake_exit` stress failure (Top-10 VP **36.2%** against a 35% limit,
DEVELOPMENT.md:285); the >35% rebalancing trigger *"exists as a name only"* (B5,
DEVELOPMENT.md:131). This ADR's factoring is only as good as ADR-0012.

### 3. Active-set selection — 75 seats = 53 deterministic + 22 lottery

*Recorded here as 60 = 42 + 18, the Register v1.0 value at the time of writing; the owner
raised the set to 75 on 2026-09-03 (see §4). The 70/30 split and every argument below carry
over unchanged.*

Recorded from DEVELOPMENT.md:110–112 and the pseudocode at :246–252:

```text
pool      = { v : status(v)=ACTIVE ∧ C(v)=1 ∧ WARP(v) ≥ 65 }
pool      = applyEntityCaps(pool)         # ≤2 active seats and ≤5% aggregate stake per entity
top53     = rank(pool)[0:53]              # deterministic Top-K by ranking
rest      = pool \ top53
lottery22 = draw(rest, weight ∝ quality², one ticket per verified entity)   # RNG = B11, OPEN
activeSet = top42 ∪ lottery18
```

- **Rotation cadence: weekly.**
- **Entity caps are applied to the pool BEFORE both stages** (DEVELOPMENT.md:112) — 5% aggregate
  stake · max 2 active seats · 1 lottery ticket per entity (DEVELOPMENT.md:132). They are not a
  post-filter on the drawn set; a draw that had to be corrected afterwards would not be a draw.
  🚧 **But pre-application needs a tie-break that nobody has written.** "Max 2 active seats per
  entity" applied *to the pool* requires a rule for **which 2** of an entity's validators stay in
  the pool; the 5% aggregate-stake cap requires a rule for **which addresses absorb the
  truncation**. Neither rule exists: DEVELOPMENT.md:112 and :132 give only the ordering and the
  three numbers, and ADR-0012 D4 restates the same numbers and exports the ticket constraint back
  here without supplying it. The argument in this bullet makes the gap sharper rather than softer
  — by ruling out post-hoc correction it forces the choice *before* the draw, where it is a hidden
  selection decision rather than a bookkeeping one. Highest VP? Highest WARP? Entity-nominated?
  Deterministic hash of the operator address? Whichever is chosen **is part of the selection
  algorithm** and must be deterministic and bit-identical across nodes. This is an explicit
  **ADR-0008 / ADR-0012 boundary item**: neither ADR may assume the other covers it.
- **Ticket weight is superlinear in quality** (`quality²`), so a lottery seat is still earned
  rather than raffled; **one ticket per verified entity** means the number of identities an
  entity controls does not change its odds. "Verified entity" is ADR-0014's and B3's
  deliverable — attestation *before* first activation (DEVELOPMENT.md:369).

🚧 **Internal discrepancy to pin before implementation:** §2.3 says ticket weight ∝ **quality²**
(DEVELOPMENT.md:111) while the §3.1 pseudocode says ∝ **ranking²** (DEVELOPMENT.md:251). A
quality score and a rank index are different quantities, and `rank²` is monotone in the *wrong*
direction unless the rank is inverted first. Neither form was swept in any study. The
implementation must pin one; this ADR records `quality²` (the §2.3 wording) and flags the
conflict rather than silently choosing.

✅ **DECIDED (owner, 2026-07-28): the ranking key is WARP.** The deterministic seats are ordered by
the smoothed reputation score, not by voting power. The arithmetic below is why: ranking by VP is
stake ordering with a reputation tint (8.7× versus 1.54×), which contradicts this ADR's own
premise that participation rather than stake size decides influence. Stake keeps its two other
roles — the entry floor admits, and rewards stay proportional to VP — it simply stops deciding
who is in the set. The original gap is recorded below for provenance.

~~🚧 The ranking key itself is undefined — and it decides 42 of the 60 seats.~~ The pseudocode is
reproduced above as recorded, but `rank(pool)` is never defined anywhere in the Register:
DEVELOPMENT.md:110 ("42 seats deterministic Top-K **by ranking**") and :249 (`rank(pool) [0:42]`)
are its only two mentions, and neither names the quantity ranked — VP, WARP, bonded stake and EP
are all live candidates and they are **not interchangeable**. Ranking by **VP** is stake-dominated
by construction: inside the pool `C=1` and `WARP ∈ [65,100]`, so the `WARP/100` factor spans at
most **1.54×**, while `min(bonded, validatorCap)` spans the 2,000 SCN entry floor to the
per-validator cap of ≈17,400 SCN (2% of the ≈870,000 SCN global bonded cap at the current
snapshot, DEVELOPMENT.md:123, 125, 129) — **≈8.7×** since the floor moved to 2,000 on
2026-09-22; it was 5.8× at the 3,000 floor, so the lower floor widens the stake span and
strengthens rather than weakens the case below. Rank-by-VP is therefore stake ordering with a
reputation tint, which sits badly against this ADR's own opening premise that participation rather
than stake size decides influence; rank-by-**WARP** yields a different set entirely. This is the
same class of gap as **B11** but on **more than twice as many seats**, and it must be pinned in
the same work item. Until it is, the "deterministic" half of the selection layer is deterministic
in form only.

### 4. Calibration status of the recorded values — honest

| Value | Basis | Strength |
|---|---|---|
| **42/18 split (70/30)** | **inherited**, not calibrated; **Stage 2's own sensitivity favoured 80/20** (DEVELOPMENT.md:118, 304) | not validated — adopted as baseline anyway |
| **set size 60** | qualitative compromise; grid mean active WARP: 50→**94.2**, 60→**93.4**, 75→**91.9**, 90→**90.2** (DEVELOPMENT.md:119, 271) | not validated — see note below |
| VP model shape | Stage 2 `stake_warp_linear`: 300 configs → 276 pass → Pareto 82; worst Top-10 VP 33.5%, worst Nakamoto-33 = 7 (DEVELOPMENT.md:271) | grounded in-model |
| per-validator cap 2% | Stage 3 (DEVELOPMENT.md:272); *but* no comparison to the earlier absolute ~10,000 SCN (DEVELOPMENT.md:307) | partially grounded |
| entity seat cap 2 | Stage 4 (DEVELOPMENT.md:273) | strongest result feeding this ADR |
| `quality²` ticket weight | not swept in any stage | asserted |
| **ranking key for the deterministic Top-42** | **WARP** — project decision 2026-07-28; the Register never named the quantity (DEVELOPMENT.md:110, 249) | **decided** |
| **lottery randomness source** | **absent from every study and from the Register** (DEVELOPMENT.md:310–311) | **undecided — B11** |

The 60-seat grid deserves an explicit reading, because it is easy to misreport: mean active
quality falls **monotonically** as the set grows (94.2 at 50 → 90.2 at 90). The grid therefore
argues for the *smallest* set and contains **no interior optimum at 60**. 60 is the point at
which a decentralization argument was judged to outweigh 0.8 quality points against 50 — a
judgement, not a measurement, and the judgement itself is not recorded in the studies. What can
still overturn a chosen size is **B1**: block time, finality, CPU/RAM and churn recovery
measured at 50/60/75/90 (DEVELOPMENT.md:367). Until B1 runs, the size is a proposal, not a
parameter.

**Superseded 2026-09-03.** The owner raised the active set from 60 to **75**, adopting the
Stage 8 recommendation: worst-world P95 Top-10 voting power 38.3% → 34.2%, P05 Nakamoto ~9 →
~10.2, at a cost of mean technical quality 95.42 → 94.60; the entity-aware confirmation run
gave 32.3% and 11. The reading above still holds and is the reason the decision is recorded as
a judgement rather than a measurement: the grid has no interior optimum, so 75 buys
decentralization and pays for it in quality. The 70/30 split carries over as **53 + 22**. B1
remains the gate that can move the number before MainNet.

## The open decision — B11: the lottery randomness source

22 of 75 seats — **30% of the consensus set, every week** — are awarded by a draw whose entropy
source is undefined. It is absent from all seven calibration studies and from the Register
itself (DEVELOPMENT.md:115–116, 310–311). §5's acceptance criterion: *"VRF/commit-reveal chosen,
implemented, manipulation-resistance argued (ADR-0008)"* (DEVELOPMENT.md:377).

The structural problem: **the population the lottery selects is the population that produces
blocks.** Any source that a validator can observe and influence before the draw resolves is a
source the beneficiaries control.

| Criterion | (a) block-/app-hash | (b) commit–reveal | (c) per-validator VRF | (d) external beacon (drand via IBC / oracle) |
|---|---|---|---|---|
| **Proposer can bias?** | **Yes — grindable.** CometBFT's proposer for a height is known in advance; it controls tx inclusion and can force a round change, so it can *search* over candidate seeds. Bounded only by the cost of missed proposals. | No grinding — commitments fix the inputs before anything is visible | No grinding — a VRF output is a deterministic function of (key, input); a holder cannot produce an alternative for the same input | No — the beacon neither knows nor cares about SAiCoN |
| **Participant can bias?** | n/a | **Yes — last revealer.** Sees others' reveals, withholds its own to flip the aggregate: one binary choice per withholder, amplifiable by a coalition | **Residual: one bit per late submitter.** An output is public once submitted, so the last submitter still chooses submit/withhold. Grinding is gone; the binary choice is not — and it is **amplifiable by a coalition on exactly the same terms as (b)**: *k* coordinated late submitters choose among 2^*k* combinations | No (threshold BLS: output unknown to any participant until the threshold is reached) |
| **If the source stalls** | never stalls | reveal shortfall ⇒ no seed ⇒ rotation stalls, or falls back — and **the fallback becomes the real source** | submission shortfall ⇒ same; needs an explicit quorum + fail-safe | beacon or relayer outage ⇒ rotation stalls; a **foreign liveness dependency inside the consensus path** |
| **Cost per rotation** (weekly) | zero | 2 txs × participants (≈224 txs/week at 112 candidates) + a non-reveal penalty rule | 1 tx + 1 verification × participants; at weekly cadence, negligible | relayer operation + 1 verification |
| **New keys / dependencies** | none | none | **new per-validator VRF keypair**: generation, registration, backup, rotation, loss recovery | new trusted group public key (governed, rotatable) + IBC path + relayer |
| **New consensus rules** | none | **a slashing/penalty rule for non-reveal**, plus its griefing surface (an honest crash between commit and reveal is punished) | quorum + fail-safe rule | fallback rule + key-rotation governance |

**(a) header-derived — rejected outright.** It is the only option where manipulation is
*unbounded search* rather than a single binary choice, and the search is available precisely to
validators. The usual mitigations (take a header N blocks back; mix several headers) narrow the
window without changing the structure, because the same set proposes every block being mixed.
For a **weekly** draw with high latency tolerance and negligible cost pressure, picking the
cheapest source is not a defensible trade.

**(b) commit–reveal — viable, not recommended.** It is the honest baseline: no external
dependency, no new keys, well-understood. Two costs decide against it. First, the last-revealer
bias is the same order as (c)'s residual, so it buys nothing there. Second, it only works if
non-reveal is penalized — a **new stake-touching penalty**, introduced in the same release wave
in which SAiCoN *removes* stake slashing for ordinary downtime (Phase 1 zero, DEVELOPMENT.md:161).
Punishing a validator that crashed between commit and reveal, while not punishing one that was
simply offline, is a policy inconsistency that would need its own justification.

**(c) per-validator VRF — ✅ SELECTED (owner, 2026-07-28).** The decisive argument is operational, not
cryptographic: **the expensive part of a VRF is per-validator key management, and SAiCoN is
building that ceremony anyway.** B3 requires beneficial-owner attestation *before first
activation* (DEVELOPMENT.md:369) — a per-validator registration step that does not exist today
and must exist before selection goes live. Registering a VRF public key inside that same
ceremony is marginal work; standing up a key ceremony solely for randomness would not be. There
is **no stock VRF module in Cosmos SDK v0.53.6**, so this is new code and new audit scope for
the Q1'27 external audit, whose scope already names selection (DEVELOPMENT.md:346–347).

Construction recommended for the implementation work item:

1. **Chained input, never proposer-supplied.** The input to rotation *N*'s VRF evaluations is
   the seed produced at rotation *N−1* (rotation 0's input fixed in genesis). No header from the
   current window feeds the seed — this removes the (a) attack even from a validator that is
   proposer at the submission height.
2. **Fixed submission window.** Every member of the post-cap pool submits `VRF_sk(input)` plus
   proof inside a block window in the epoch preceding the rotation. Late submissions are ignored.
3. **Aggregate:** `seed = H(sorted(verified outputs))`. Only verified outputs count.
4. **No penalty for not submitting.** A pool member that stays silent simply does not contribute
   — deliberately avoiding the new slashing rule that (b) requires.
5. **Quorum + fail-safe.** If fewer than a defined quorum submit, **the rotation does not draw**:
   the previous lottery cohort holds over for one rotation and an event is emitted. The fail-safe
   direction is always "award no new seats on weak randomness", never "draw anyway".

**Honest residual — do not describe (c) as eliminating manipulation.** As constructed above it
still leaves one binary choice to whoever submits last: having seen the partial aggregate, it can
compute the outcome its own output would produce and decide to submit or withhold. Three things
bound the severity — each with a caveat, and all of it to be *measured* rather than assumed:

- one ticket per verified entity plus a 2-seat entity cap means a successful bias is worth **at
  most one seat, for one rotation, to one entity** — but the caps bound the payoff *per entity*,
  not the size of a colluding group: *k* cooperating entities multiply that bound by *k*;
- the choice is binary, not a search — expected gain per rotation is a fraction of a seat, versus
  (a) where the gain scales with grinding budget. **The coalition amplification charged against
  (b) above applies to (c) in full** and is stated here rather than quietly dropped: (c) removes
  grinding, it does not remove coalitions, and pricing that property into (b) while omitting it
  from the recommended option would be exactly the kind of asymmetric accounting this section
  exists to avoid;
- a chained input prevents planning several rotations ahead.

**(d) external beacon — rejected for now, kept as the documented upgrade path.** A t-of-n
threshold beacon is the only option that removes even the one-bit residual, because no
participant learns the output before the threshold is reached; consuming drand over IBC buys
that without SAiCoN running a DKG. The price is a foreign liveness dependency in the consensus
path of an L1 whose whole premise is that participation is measured on-chain — and it does not
even remove the need for a local fail-safe, since whatever the chain does when the beacon is
unreachable *is* the randomness source for those rotations. Make that trade if the (c) residual
is measured and found material; not pre-emptively.

### Interim policy — proposed, binding on acceptance

This ADR is *proposed* and binds nothing until accepted; what follows is binding **on acceptance**.

**Until B11 is chosen, implemented and audited, the lottery must not award consensus seats.**
"and audited" is this ADR's own addition: B11's recorded criterion is *"VRF/commit-reveal chosen,
implemented, manipulation-resistance argued (ADR-0008)"* (DEVELOPMENT.md:377) and carries no audit
clause. The tightening is proposed openly rather than slipped in — the Q1'27 external audit
already scopes *"mint path, selection, entity gates"* (DEVELOPMENT.md:346–347), the randomness
construction is new code with no stock SDK module behind it, and a manipulation-resistance
*argument* that no independent party has checked is precisely the class of claim this ADR declines
to accept elsewhere. Accepting this ADR means accepting that addition to B11.

The selection layer's first shipped form is either deterministic Top-K over the post-cap pool
(no draw), or the deterministic seats alone with the lottery seats run in **shadow mode** on the
instrumented testnet — draws computed and published, no seat awarded. Shadow mode is preferred:
it is what produces the evidence B11's acceptance criterion asks for (a manipulation-resistance
argument backed by observation instead of assertion), and it feeds B1's churn measurement
without putting seats at risk.

### B5 — the >35% rebalancing trigger: the clamp is accepted here, the rest is re-exported

ADR-0012 hands B5 to this ADR by name — *"Designing it is out of scope for this ADR (it belongs
with the VP/selection layer, ADR-0008, and the reserve pool, B6)"* — and until now this ADR
neither designed it nor declined it. Silence in both is the one outcome that loses the item, so it
takes a position.

**Accepted here: the VP clamp.** Of the three actions ADR-0012 enumerates, clamping the top
cohort's VP so the *normalized* share returns under 35% without touching stake is a
consensus-weight operation — it changes the number written into `ValidatorUpdate`, which is this
ADR's surface and nobody else's, and it is the only one of the three that can act inside a single
rotation. Constraints it inherits from this ADR, all of which must be discharged by whoever
implements it: it must be deterministic and bit-identical on every node; it must compose with
normalization, because clamping and renormalizing can push a *different* validator over the
threshold — the fixed point must be shown to exist and to be reached in bounded iterations; and it
must not collide with the rounding rule and minimum power floor that are still unpinned
(Consequences below).

**Re-exported, with owners named.** Freezing new delegations to over-threshold validators and
routing them elsewhere is **ADR-0005 (delegation router — not yet written)**. Widening the
denominator by admitting reserve candidates is **B6**. The measurement and trigger policy ADR-0012
lists — Top-10 share of VP or of bonded stake, instantaneous or rolling, who acts, release
condition and hysteresis, precedence against the D2 queue — is cap-layer policy and stays with
**ADR-0012**; this ADR does not answer it.

**Status: still designed nowhere.** This subsection assigns the clamp and states its constraints;
it does not discharge B5, whose acceptance criterion remains DEVELOPMENT.md:371 — after a 30%
delegation exit, Top-10 VP ≤ 35% with caps still consistent.

## Consequences

### Implementation status: none of this exists

Verified in this repository on branch `main` at the time of writing.

- **No voting-power computation, no selection layer, no lottery.** grep over `chain/x`,
  `chain/app`, `chain/proto` for `voting_power` / `ValidatorUpdate` / `lottery` / `active_set` /
  `WARP` (excluding generated `*.pb.go`) returns exactly two hits, neither of them an
  implementation: a comment at `chain/x/validatorregistry/keeper/hooks.go:31` explaining why a
  hook error must not propagate into staking's `BlockValidatorUpdates`, and a stock staking store
  key listed in `chain/app/sim_test.go:249`. There is no VP type, no VP state, no VP query.
- **No SAiCoN module is even able to change the validator set.** All four modules declare
  `appmodule.HasEndBlocker` (e.g. `chain/x/participation/module/module.go:29`, EndBlock at
  `:177`), whose `EndBlock` returns only `error`; none implements the ABCI variant returning
  `[]abci.ValidatorUpdate`, and `abci.ValidatorUpdate` does not appear in `chain/x` or
  `chain/app` at all. Consensus weight is produced solely by stock `x/staking`, wired with **no
  configuration** at `chain/app/app_config.go:202` (`stakingmodulev1.Module{}`) and running in the
  EndBlocker chain *ahead* of the SAiCoN modules (`chain/app/app_config.go:135–141`). That is
  top-N by bonded stake at the SDK default `MaxValidators`; `chain/config.yml` (26 lines) sets no
  staking genesis params.
- **The `WARP/100` factor has no input.** `x/participation` Params carry only
  `epoch_length_blocks` and `ep_decay` (`chain/proto/chain/participation/v1/params.proto:15–20`);
  the per-epoch snapshot carries `uptime_fraction` and `computed_score`
  (`chain/proto/chain/participation/v1/types.proto:19–28`). There is no smoothed reputation
  state anywhere. **ADR-0009 is a hard prerequisite for this ADR's formula.**
- **No caps and no entity registry**, so both `min(bonded, validatorCap)` and
  `applyEntityCaps(pool)` have nothing to call — there is no stake check at registration at all
  (DEVELOPMENT.md:62). **ADR-0012 and ADR-0014 are hard prerequisites for the pseudocode** — and
  ADR-0014 is **not yet written** (`chain/docs/adr/` holds 0001–0004, 0006–0012 only), so the
  prerequisite chain is open at its far end.
- **No randomness primitive of any kind.** grep for `vrf`, `drand`, `commit-reveal`,
  `randomness` over `chain/x`, `chain/app`, `chain/proto` returns nothing.

### Where the code contradicts the Register

- **Rewards are flat, not ∝ VP.** The Register: *"Rewards ∝ final VP — no tiers, no second KPI
  multiplier"* (DEVELOPMENT.md:114). The chain mints an **identical flat amount** to every
  eligible validator — `rewardAmt := math.NewIntFromUint64(params.RewardPerEpochAmount)` at
  `chain/x/rewards/keeper/emission.go:98`, documented at `:76` and in
  `chain/proto/chain/rewards/v1/params.proto:19–22`. Stake, WARP and quality have **zero**
  influence on reward size today. This is not merely "not built yet": the shipped behaviour is a
  *different economic policy* — equal pay per active validator — running on devnet now. Debt
  owned jointly by this ADR (the VP scalar) and ADR-0011 (the pool).
- **The reward-eligible population is already the stock stake-weighted set.**
  `chain/x/rewards/keeper/emission.go:101` iterates `IterateBondedValidatorsByPower`, i.e. exactly
  the set this ADR proposes to replace. Introducing selection changes the iteration source, not
  just the amounts.
- **Active-set size is the SDK default, not 60.** Nothing in the repo sets `MaxValidators` to 60.
  Changing it belongs to B12's SDK-override enumeration (DEVELOPMENT.md:378) — and it must not be
  set to 60 in isolation, because with no selection layer that merely yields "top 60 by stake",
  which is not what §2.3 decided.
- **Consensus-power granularity is unpinned.** `chain/app/config.go:8` sets the bond denom to
  `nscn` (8 decimals) and does **not** override `sdk.DefaultPowerReduction` (10⁶) — so under
  *stock* behaviour one unit of consensus power = 0.01 SCN. That constant governs staking's own
  `tokens → power` conversion, which **Decision §1 bypasses**: once normalized VP is written
  directly as the `ValidatorUpdate` power, granularity is set by the normalization scale and by
  nothing else, and no headroom argument can be borrowed from `PowerReduction`. Three quantities
  must therefore be pinned, and the Register specifies **none** of them: the **normalization
  target scale** (what Σ power sums to, equivalently what one power unit is worth in VP), the
  **rounding rule** for normalized VP → int64 power, and a **minimum power floor**. Without all
  three, a small validator's VP can truncate to 0 and it is silently dropped from the very set it
  was selected into. **This ADR names them as required implementation details; they are decided
  nowhere.**

### Operational and design consequences

- **Compliance becomes a consensus lever.** `C=0 ⇒ VP=0 ⇒ power 0 ⇒ ejection in the same
  rotation`. A false positive in `x/compliance` stops being a lost reward and becomes a liveness
  event for that validator. The evidence ladder and appeal path (ADR-0014, B4) are therefore on
  the critical path for selection, not adjacent to it — and DEVELOPMENT.md:174 already records
  that the current detector thresholds are synthetic.
- **Weekly churn is a consensus event, and the lottery is the floor, not the ceiling.** It
  alone can replace 22 of 75 seats — 30% of the set at one rotation height. But the deterministic
  Top-K is **re-ranked at every rotation too**, so seats change hands across the last-seat boundary
  as well, and the pool underneath both stages moves as validators cross the `WARP ≥ 65` and `C=1`
  gates and as the entity caps re-bind. Nothing in §2.3 bounds total per-rotation turnover below
  the full set. B1's "churn recovery" criterion (DEVELOPMENT.md:367) must therefore measure
  **observed total turnover** at 50/60/75/90 — not the lottery's share of it — alongside block
  time, finality and CPU/RAM.
- **Concentration control is now wholly delegated to the cap layer** (Decision §2). The
  `stake_exit` failure — Top-10 VP 36.2% > 35% (DEVELOPMENT.md:285) — is a failure of that
  delegated defence and is open as B5; the VP-clamp half of it is accepted into this ADR's scope
  above, the rest re-exported.
- **This ADR carries the Register's two weakest parameters and its one undecided one.** 70/30 and
  60 are both uncalibrated, and the randomness source is undecided. It is the ADR in the Q3 wave
  most likely to need reopening once B1 (consensus benchmark) and B2 (≥90 real epochs of
  telemetry) produce evidence.

## Alternatives considered

### Active-set shape

- **Deterministic Top-60, no lottery.** Simplest; B11 disappears entirely. Rejected as the end
  state: the set ossifies, and a validator outside the top 60 has no path to accumulate the
  participation record that ranking is computed from — which contradicts the chain's premise.
  Retained as the **interim** shape until B11 closes.
- **80/20 (48 + 12).** Stage 2's own sensitivity favoured this split (DEVELOPMENT.md:118, 304).
  Not chosen only because the Register inherited 70/30 and was adopted wholesale. Recorded here
  because *the study that produced the shape disagrees with the shape*; first candidate for
  revision under B2.
- **Larger set (75 / 90).** Better decentralization surface, measurably lower mean active WARP
  (91.9 / 90.2 vs 93.4) and higher consensus cost. Deferred to B1, which is the only thing that
  can decide it on evidence.
- **Smaller set (50).** Highest measured quality (94.2). Rejected qualitatively on
  decentralization grounds; that reasoning is not in the studies.

### Voting-power shape

- **Keep EVP with the concentration factor.** Rejected — see Decision §2: soft vs hard control,
  non-locality of VP, double counting against caps, and layer separation.
- **A second KPI multiplier on rewards** (consensus weight from VP, pay from VP × KPI). Rejected
  by the Register (DEVELOPMENT.md:114): two multipliers make pay opaque and hand governance a
  second lever over income that bypasses the bounded-DAO limits on the VP formula itself (§2.10).
- **Tiered rewards** (v1's 0.5 / 1 / 1.5 tiers, DEVELOPMENT.md:212). Rejected: tier boundaries
  are income cliffs — a marginal WARP move changes pay by 50% — and every boundary is another
  uncalibrated parameter.

### Randomness

Covered in the B11 section: (a) header-derived — **rejected**; (b) commit–reveal — viable,
rejected on the last-revealer bias plus the new penalty rule it requires; (c) per-validator VRF
with a chained input and a no-draw fail-safe — **recommended**, with a stated one-bit residual;
(d) external threshold beacon — rejected for now, documented upgrade path if the residual proves
material.

## References

- [DEVELOPMENT.md](../DEVELOPMENT.md) — §2.0 VP definition and the EVP supersession (84–87);
  §2.3 the subject of this ADR (108–119); §2.4 caps applied before selection (121–135); §3.1
  formulas and selection pseudocode (231–264); §3.2 Stages 2/3/4 (271–273); §3.3 the `stake_exit`
  and `hidden_cartel_20` failures (285–286); §3.4 measured vs asserted (296–316); §5 blockers B1,
  B3, B5, B11, B12 (365–378); §6 ADR index (398).
- *SAiCoN Validator Parameter Decision Register v1.0* (2026-07-24), adopted wholesale by owner
  decision; cross-verification against the seven studies archived in the owner's
  `Validator-Module-13-Questions-MERGED.md` (DEVELOPMENT.md:412–417).
- [ADR-0004](ADR-0004-validator-activation-model.md) — activation is staking-hook driven; this
  ADR changes who holds a *seat*, not who is *registered*.
- ADR-0009 (PS + WARP engine), ADR-0012 (caps / `x/stakeengine`), ADR-0014 (entity registry,
  attestation, appeal — **not yet written**) — all three are prerequisites, not siblings.
- ADR-0005 (delegation router — **not yet written**) — owns the delegation-freeze half of the B5
  hand-off re-exported above.
- ADR-0011 (emission) — owns the reward pool that VP divides.
- Code, branch `main`: `chain/app/app_config.go:202` and `:135–141`, `chain/app/config.go:8`,
  `chain/x/rewards/keeper/emission.go:76,98,101`,
  `chain/x/participation/module/module.go:29,177`,
  `chain/proto/chain/participation/v1/params.proto:15–20`,
  `chain/proto/chain/participation/v1/types.proto:19–28`,
  `chain/x/validatorregistry/keeper/hooks.go:31`.
