# SAiCoN — Development Guide

> How the chain is built: current state, parameters and formulas, algorithms, evidence
> and calibration, and the index of architecture decision records. Known limitations are in
> §1.4.

**Status legend:** ✅ built · 🟡 partial · 🔜 to build · 🔒 approval gate · 🚧 open design gap.

---

## 0. What SAiCoN is

A Cosmos-SDK / CometBFT Layer-1 where **participation pays**: validators earn by measured
participation — signed blocks, honesty, compliance — not by stake size alone.
**Proof-of-Participation (PoP)** is the umbrella: proof-of-stake bonding + participation- and
decentralization-weighted influence. Chain: Cosmos SDK v0.53.6, Go, base denom `nscn`
(1 SCN = 100,000,000 nscn, 8 decimals), max supply 21,000,000 SCN (enforced on every mint
and on the genesis allocation — see §2.5). Layer-1 cost principle (ADR-0007): on-chain only what must be; derived
analytics → off-chain backend; programmable app logic → CosmWasm (Q1).

---

## 1. Current state (as-built, v0.9.1 — 25 October 2026)

### 1.1 Modules

| Module | What it does | Status |
|---|---|---|
| `x/validatorregistry` | status machine (BONDING→ACTIVE→QUARANTINED/JAILED→UNBONDING→INACTIVE, table-gated), auto-activation on bond (ADR-0004), 60-day lock-up (V-3, ante-enforced), exit lifecycle + 21-day unstake cooldown (V-5) | ✅ |
| `x/compliance` | C-flag (source of truth + mirror), evidence/clear messages, double-sign auto-jail (slash fraction ≥0.025 → CRITICAL_VIOLATION → C=0 + JAILED), **governed MsgUnjail** (gov-authority, 43,200-block cooldown, tombstone never resets), ClearFlag refuses JAILED (`ErrJailedRequiresUnjail`, 1108) | ✅ |
| `x/participation` | PS v1 = precommit-uptime (single signal, weight 1.0) per block-epoch (default 100 blocks, `MsgUpdateParams`-tunable); EP accumulator (+1 per active epoch, optional decay-to-floor-1, `ep_decay=0` default) | ✅ (v1) |
| `x/rewards` | EP-gated emission: `ACTIVE & C=1 & EP>0`. **Gas fees offset issuance** (observed, not taken — x/distribution still pays them stake-weighted). **Single mint path** — the epoch pool is minted once, sized by the Register security budget `clip(0.15/7·bonded, 0.005/7·supply, 0.0075/7·supply)/epochs_per_year`, clamped to the headroom below the **21M ceiling**, split among eligible validators, remainder carried forward; kill-switch `enabled` records a reason per validator; RewardSnapshot per validator per epoch with denial reasons | ✅ gate / ✅ budget + cap + gas offset |
| `x/stakeengine` | the three stake limits (ADR-0012): **entry floor 2,000 SCN** of the operator's own self-bond (tier-1 constant); **global cap** `min(0.08·max supply, 0.60·public circulating)`, public circulating computed on-chain each epoch with the bonded pool, not-bonded pool and queue escrow counted as public; **per-validator cap** `max(0.02·EWMA-30(total bonded), floor)`. Enforced in the ante handler: up to five nested `authz.MsgExec` layers are unwrapped and checked; a sixth is refused. Net new stake at the global cap goes to an explicit **stake queue** (`MsgEnqueueDelegation`), escrowed and filled every EndBlock by validator quality (participation score, then EP) | ✅ |
| `x/delegationrouter` | `MsgDelegateRouted` (ADR-0005, §2.8): fills the delegator's chosen validator to its cap and spreads the rest over up to `max_route_legs` more (default 2) by quality × room ÷ stake, the part above the global cap joining the stake queue in the same message; `route` preview query; no module account. Shipped in v0.9.0 (S7) | ✅ |
| Stock: staking, slashing, distribution, gov, bank, IBC (**`x/mint` removed**) | unmodified SDK defaults — relevant consequences: voting power = top-N by stake; downtime slash 1% flat; double-sign 5%. `x/mint` is **removed from the app** (B12 executed, 29 Jul): `x/rewards` is the only module holding Minter for `nscn`, so the 21M ceiling guards every path that can create supply | ⚠️ overrides pending (§2.5, §2.6) |

### 1.1b Off-chain services

| Service | What it does | Status |
|---|---|---|
| `indexer/` | follows blocks for the one fact the chain cannot answer about itself — which validator proposed which block, carried in every header and indexed by no query — and at each epoch close reads the numbers the chain computed for itself. Serves `/v1/scoreboard`, `/v1/validators/{op}`, `/v1/validators/{op}/epochs`, `/v1/status`. Recomputes nothing; absence is `NULL`, never `0`; compliance read from `x/compliance`, never the registry mirror (V-4.1). Go + SQLite (`modernc.org/sqlite`, CGO-free like the rest of the repo) | ✅ v1, gated in CI · public endpoint goes up with the devnet reset (the devnet operations runbook); the scoreboard page reads it |

It reads a node, it does not extend the chain, so nothing here is consensus-critical.
Two things about it are operationally load-bearing: it must not point at a validator
past the single-node devnet (ADR-0015), and it can only count blocks the node still
holds — against a pruned node it starts at the oldest block retained and says so, and
those proposal counts cannot be recovered from that node afterwards.

### 1.2 The live PoP cycle

register → auto-ACTIVE on bond → sign blocks → PS per epoch → EP accrues → gated reward
minted → (negative path) evidence → C=0 → QUARANTINED/JAILED → reward denied (reason
recorded) → governed restore.

**Verification status — stated precisely, because this claim has drifted twice.** The
positive and quarantine paths were green on `chain/scripts/demo-pop-cycle.sh` on localnet as
of 2026-07-05. The JAILED branch and the governed restores (`MsgUnjail`, `MsgClearFlag`) have
never been exercised live on either environment, and the explicit `MsgRegisterValidator` path
is not exercised anywhere — both environments onboard through auto-create-on-bond (ADR-0004).
The devnet runs **v0.8.0** (`dev-88f10aaf`) on the fresh genesis of its S6 reset: the stake
caps and the queue are live there (global cap 1,680,000 SCN), with one validator; the JAILED and
governed-restore paths above are exercised on neither environment.

**One fresh-genesis reset carries v0.7.0 and v0.8.0 together**
(the devnet operations runbook). On a stand built from the v0.7.0 release
commit: `max_validators` 75, lock-up 60 days, both history queries `200`, epoch 1 reward
denied on `EP=0` and epoch 2 paid, and the indexer matching the chain on every field. After
the reset all earlier addresses, balances and heights are gone and the validator has a new
address.

The reset wipes the chain volume and the indexer volume, because a new module needs a fresh
genesis, and the entrypoint writes the S6 genesis — `validator_cap_floor` 2,000,000 SCN
included, the security-budget rates at the Register values ÷7 (about 29 SCN a day on the
devnet). On a fresh genesis the caps read `global_cap` 168,000,000,000,000 nscn (1,680,000 SCN)
and `validator_cap` 200,000,000,000,000 nscn (2,000,000 SCN). The public scoreboard page reads
them from `/saicon/chain/stakeengine/v1/caps`.

v0.9.0 needs a new fresh genesis (the devnet operations runbook). The default
keeps **one validator**: every S7 rule runs on it, and with the 2,000,000 SCN cap floor a routed
delegation lands whole on that validator. The alternative is the three-validator profile —
300,000 SCN self-bond each, `validator_cap_floor` 400,000 SCN — where a delegation of a few
hundred thousand SCN to one validator overflows into the other two. No part of the chain
requires a minimum number of validators. Until the reset the public node serves the earlier build.

Public devnet: 1 node (`saicon-devnet-fsn1-01`), explorer at explorer.saicon.world, open REST
at api.saicon.world; the indexer's endpoint, scoreboard.saicon.world, goes up with the reset.

### 1.3 Runtime invariants (EndBlock assertions that panic — fail-loud)

| ID | Statement |
|---|---|
| V-1 | every validator has a valid status; transitions follow the legal table |
| V-2 | `EP == 0 ⇒ reward == 0` |
| V-3 | no self-stake withdrawal during the 60-day lock-up (ante, fail-closed); a validator may commit to longer, up to 10 years at registration; a stored or genesis record is honoured as written up to the longest computable lock-up (~292 years), and beyond that is refused at startup or, if stored, read as that value, so the expiry never overflows into the past |
| V-5 | no INACTIVE before the unstake cooldown elapsed |
| V-7 | PS snapshots exist only for ACTIVE validators |
| V-8 | EP changes only via participation (`ComputeEpoch`); punishment never mutates EP. Punishment MAY zero WARP and the **in-flight** epoch's PS snapshot — never EP, never a closed epoch's snapshot. **Revised and implemented 2026-07-28** (ADR-0010 §4): the single punishment write path is `x/participation.ZeroReputation`, and `x/compliance` structurally cannot reach `SetEP` because its `ParticipationKeeper` interface does not expose it. WARP zeroing is a no-op until the engine exists (ADR-0009). |
| V-10 | the `stakeengine` escrow balance equals the sum of the stake queue's entries, exactly. The escrow is a blocked address, so no transfer can reach it |
| V-4.1 | metadata `compliance_flag` mirrors `x/compliance.Compliant` — ⚠ **NOT IMPLEMENTED: no invariant, no runtime check.** The mirror is best-effort: written by `x/compliance.SetCompliance`, but hardcoded `true` on the register and auto-create-on-bond paths, which never consult `x/compliance`. It can diverge undetected. The reward gate reads `x/compliance` directly, so the mirror is not load-bearing for eligibility — it IS load-bearing for the re-bond restore decision. |

### 1.4 Known gaps (honest)

PS single-signal, no WARP · voting power is stock stake-weighted (no selection layer) ·
stake caps: the backstop leaves two routes open (below) · delegation router shipped in v0.9.0 (S7),
the weight function replaced when the S-13 run lands · no entity registry ·
**V-4.1 has no runtime check** (§1.3) · sentry layer designed in S3, adopted before S9, so
validators currently run with the SDK's default open peer exchange (§2.10.2) · the DevNet runs
v0.9.0 on one validator since 22 Oct, so multi-validator behaviour is verified on the local
three-validator testnet, not on the DevNet.

**Stake caps — what the S7 backstop does not cover.** S6 checked the caps in the ante
handler only, so messages executed by `x/group`, `x/gov` and an ICA host passed around
them, and so did the V-3 lock-up. S7 removed that: the caps, the entry floor, the V-3
lock-up and the three-validator limit are now enforced in staking hooks too (§2.8), and
an app-level test runs each attack through a transaction, a passed `x/gov` proposal, an
executed `x/group` proposal and `authz` `MsgExec`. **C1 remains, alone:** the global cap
is measured on the bonded pool, and stake that becomes bonded without a delegation
operation passes no hook — a validator entering the active set at rotation or after
unjail carries the stake delegated while it was out, and several `MsgCreateValidator` in
one block each see the same headroom (their stake waits in the not-bonded pool until
x/staking's EndBlock). Bonded can therefore exceed the cap — no funds at risk, no halt.
Project decision 2026-10-06: recorded, to be closed before testnet. *(A redelegation from a
non-bonded source into a bonded validator, the third S6 route, is closed by the hook.)*
**Accepted, S7:** the 100 SCN minimum is enforced in the ante and the message handlers,
not in the hook, so a dust delegation made through governance, group or ICA passes; it is
not a cap breach, revisit if abused · a governance decrease of
`max_validators_per_delegator` leaves existing delegators above it frozen, not shed ·
room freed by an undelegation
can be taken in the same block by a direct `MsgDelegate` before the queue fills at
EndBlock, so the quality-ordered queue gets what is left (accepted for now, owner
decision 2026-10-06; revisit before testnet) · IBC transfer escrow is not subtracted from public
circulating supply: the transfer keeper is wired outside depinject, and the devnet has
no IBC channels · a new validator has no way in while the global cap is full — the
queue only feeds existing validators (project decision before the cap binds on a real
network) · the production `validator_cap_floor` and a tighter bound on it (a large
floor turns the 2% rule off) · `max_fills_per_block` counts fill attempts, not entries
examined · EndBlock reads the whole queue each block while there is headroom, and V-10
walks it every block — fine at the 10,000-entry default, to be measured before
`max_queue_entries` is raised toward its 100,000 bound.

**Indexers and queue fills.** In SDK v0.53.6 the `delegate` event is emitted by x/staking's
`MsgDelegate` message server, not by `Keeper.Delegate`. A queue fill calls the keeper
directly, so it emits `queued_delegation_filled` and **no** `delegate` event: an indexer
that tracks delegations must read both.

**Found 2026-08-15 auditing the code against the Register** to answer the application
team (§5.2, evidence in the research archive):
**no commission policy, and `min_commission_rate` is the SDK's 0%** — nothing raises it,
so a validator may run at 0% the day external operators are invited in S9 ·
**`staking.params.unbonding_time` is pinned in both genesis paths** (1814400s, V-5) —
until 2026-08-15 it was the SDK v0.53.6 default that happened to agree, while four
scripts described it in comments as ours. Fixed together with the evidence window, which
the three-node genesis had never carried at all: it ran CometBFT's stock 100,000 blocks /
48h, the same hole DevOps is closing on the live chain. Both stands verified live —
genesis reads 1814400s / 362880 / 1814400000000000 and the chain produces blocks ·
**the reward split is equal, not reputation-weighted** — `emission.go:174` divides the
epoch pool by the count of validators passing a binary gate, so PS changes selection and
governance but not income until brief S-18 settles the split function. Stage 5 calibrated
the pool's size; nothing has ever studied its division.

**Found and closed 2026-08-15 — the simulation suite's first real run.** The defect
class R3 promised: **`x/participation` and `x/rewards` exported only their params.**
`TestAppImportExport` compared a simulated chain against the same state exported and
re-imported and found **99 key/value pairs in the participation store against 1**. Eight
collections between the two modules had no genesis representation, so any
export-and-restart — a coordinated upgrade, a state migration, a genesis rebuild —
silently erased every score snapshot, every accumulated EP, the whole payment record,
and the two accumulators that size the next mint. Since eligibility is
`ACTIVE ∧ compliant ∧ EP > 0`, the entire set would have come back unable to earn with
nothing reporting why. `x/validatorregistry` had fixed exactly this for itself once; the
other two never had.

Both modules now carry their full state through genesis, with round-trip tests beside
the fix. Two details the work turned up and pinned:
**signed and missed block counts are separate collections** — the chain writes one side
per block, so a validator with no misses has no entry there, and merging them into one
genesis record created keys on import the exported chain never had (the store came back
*larger*: 147 against 99) · **`last_status_change_height` was written raw at creation
while InitGenesis clamped it**, so the field diverged across a round trip; both creation
sites and the status-transition path now normalise it the way `bonding_start_height`
already did, and a validator created during InitChain no longer stores the height 0 that
`LockStartHeight` exists to prevent.

**Closed on 2026-07-28/29** (B8, B12, ADR-0010, ADR-0011): the epoch pool is minted once,
clamped to the headroom below the 21,000,000 SCN ceiling, with the remainder carried and an
auditable kill switch; the pool is sized by the Register's security budget rather than a flat
mock; gas fees offset issuance (§2.5, observed not taken); slashing follows the Register
(downtime 0, double-sign 10% + tombstone) instead of the SDK 1%/5% defaults; and `x/mint` is
**removed from the app entirely**, so `x/rewards` is the only module that can create SCN and
the ceiling guards all of it. A wiring test asserts no second `Minter` permission exists —
it caught `x/validatorregistry` still carrying `Minter + Burner + Staking` from the scaffold.
**The genesis must fund less than the ceiling**: the shipped default previously funded
4,762× it, which left zero headroom and denied every reward with `"max supply reached"`.
A test now reads the shipped defaults so the two cannot drift apart again.
Since v0.9.1 the ceiling also binds the allocation itself: `x/rewards` InitGenesis refuses
to start a chain whose initial `nscn` supply is above 21M, where before such a genesis started
and simply never paid.

Removing `x/mint` is a **state-breaking change**: the DevNet restart needs a fresh genesis,
it cannot be brought up over existing state.

**Confirmed by the public-release audit (25 Oct 2026), open:**
- **PoP `JAILED` is a registry status, not consensus removal.** A governance
  `CRITICAL_VIOLATION` sets the registry to JAILED and stops PoP rewards, but the validator
  stays bonded in `x/staking` and keeps signing. Double-sign is different: the SDK jails and
  tombstones it. Joining the two is part of the selection layer (ADR-0008, ADR-0010).
- **Emission is split equally among eligible validators and paid to the operator account.**
  No voting-power weighting, no delegator share, no commission on the PoP path; transaction
  fees still go out stake-weighted through `x/distribution`. The white paper's §8.5 describes
  the target design (ADR-0011 D6).
- **Indexer and a same-chain-id reset.** The indexer checks the chain-id at start and refuses
  a tip below its cursor, but a DevNet reset that keeps the chain-id and grows past the old
  height is not detected; reset the indexer with the chain (`chain-reset` does).

---

## 2. The model — Decision Register v1.0 baseline (adopted 2026-07-24)

> All economic/consensus parameters were set by the **Validator Parameter Decision Register
> v1.0** (24 Jul 2026), the outcome of a seven-stage calibration program (grid sweeps + Monte
> Carlo over real stake balances with synthetic behaviour; integrated stress test passed 6/9
> scenarios). Adopted wholesale by project decision; the Register itself is a **DevNet/TestNet
> baseline — it does not authorize real MainNet payouts** (blockers, §5). Cross-verification
> of every value against the studies is archived (see §7); the headline results are §3. DevNet genesis restart under the
> new parameters is acceptable (pre-production).

### 2.0 Terminology

| Term | Meaning | Status |
|---|---|---|
| **PS** | raw per-epoch quality 0–100 = `0.50·Uptime + 0.35·BlockQuality + 0.15·Governance` | 🟡 v1 = uptime only |
| **WARP** | smoothed reputation: `drop 0.65 / recovery 0.10`; eligibility threshold 65; initial 80 | 🔜 |
| **EP** | Empirical Points — long-run accumulator; **reward gate** `ACTIVE & C=1 & EP>0` (V-2) | ✅ (unchanged) |
| **VP** | Voting Power = `min(bonded stake, validator cap) × WARP/100 × C` | 🔜 |

VP replaces the old EVP concept; the concentration adjustment is dropped — concentration is
handled by caps. Compliance, anti-cartel and green are **separate layers**, never PS weights.

### 2.1 Epoch

≈24h defined by a **fixed block count** (never wall-clock). Already block-based in code
(default 100 blocks — devnet-fast; production count set after DevNet block-time + EndBlock
cost benchmarking: `ComputeEpoch` iterates the full validator set in one block).

### 2.2 Participation Score & WARP — 🔜

- PS weights **0.50 / 0.35 / 0.15** (uptime / block-production quality / governance).
- WARP update per epoch: `WARP ← WARP − 0.65·(WARP − PS)` when PS < WARP (fast fall),
  `WARP ← WARP + 0.10·(PS − WARP)` when PS > WARP (slow recovery). Eligibility requires
  WARP ≥ **65**; new validators start at **80**.
- Telemetry policy: confirmed network-wide outage **freezes** WARP (no false punishment);
  first missing-telemetry epoch gets **grace**, continued missing telemetry scores 0.
- Calibration basis (synthetic, Stage 1): selective-participation false-acceptance 43.8%→0%,
  weak 2.67%→0%; cost = slower recovery (3→10 epochs after a 3-day outage).
  🔒 recalibrate after 90–180 real epochs (B2). *Honesty: governance 0.15 re-entered without
  recorded justification; initial 80 was outside the sweep — adopted as baseline anyway.*

### 2.3 Voting power & selection — 🔜 (largest build)

- **Active set 75** (project decision **2026-09-03**, adopting the Stage 8 recommendation over
  Register v1.0's 60): **53 seats deterministic Top-K ranked by WARP**
  (project decision 2026-07-28, ADR-0008 — reputation, not voting power) +
  **22 seats entity-aware lottery**, ticket weight ∝ **quality²**, **one ticket per verified
  entity**; entity caps applied **before** both. The 70/30 split carries over unchanged; it was
  inherited rather than calibrated, and 53 + 22 is its arithmetic at 75, not a measured optimum.
  🔒 **B1 can still move the size** — it is the benchmark that settles it before MainNet.
- **Rotation cadence: weekly** — decided in ADR-0008 (status *Proposed*), not inherited. The ADR
  reasons from it: VRF commit-reveal costs ≈224 txs per rotation at 112 candidates, which is
  affordable weekly and seven times less so daily; and it treats a rotation as a consensus-level
  churn event, where the lottery alone can replace 22 of 75 seats at one height.
- ⚠ **But the cadence is a gap in the Register, and that gap is load-bearing.** No Q or A row in
  Decision Register v1.0 fixes it, so the number lives only in our own ADR and was never
  reconciled back into the baseline. Two consequences:
  - Stage 8 compares active-set sizes (60 vs 75) and Top-K/lottery splits partly on `turnover`.
    Turnover is a *rate*, so those comparisons are implicitly conditioned on a cadence the studies
    never pinned. Stage 8's screening samples the active set at *"52 weekly checkpoints"* while its
    confirmation runs use **365 daily epochs** — the notebook points both ways.
  - Cadence sets how long a degraded validator keeps its seat and how fast a recovered one returns.
    At weekly, a validator that fails on day 1 holds a consensus seat for six more days; the WARP
    eligibility gate is evaluated far more often than the set is redrawn.
  Action: carry "rotation cadence = weekly" into the Register as an explicit row, so the Stage 7
  integrated re-run that Stage 8 and Stage 11 both require is run against a stated cadence rather
  than an implied one.
- `VP = min(bonded, validator cap) × WARP/100 × ComplianceFlag` → normalized → ABCI
  `ValidatorUpdate`. **Rewards ∝ final VP** — no tiers, no second KPI multiplier.
- ✅ **B11: randomness source chosen — per-validator VRF** (project decision 2026-07-28, ADR-0008;
  the key is registered inside the B3 attestation ceremony, which has to exist anyway). Still to
  build: there is no VRF module in Cosmos SDK v0.53.6, so the lottery still must not carry
  consensus seats until it exists and is audited. 🔒 B1: set size 75 needs a consensus benchmark
  before MainNet.
  *Honesty: the 70/30 split was inherited, not calibrated (Stage 2's own sensitivity favoured
  80/20); the Stage 2 grid falls monotonically with size (50→94.2 quality, 60→93.4, 75→91.9,
  90→90.2), so 75 is a decentralization judgement paid for in quality, not a grid optimum.*

#### 2.3.1 Review round — Stage 8/10/11 + Response v2 (2026-07-29) 🔬

A second research round answered the reviewer criticism that several numbers were presented as
simulation-selected when the experiments had not isolated their effects. **Nothing below changes
the adopted baseline yet** — Register v1.0 stands until the integrated Stage 7 stress suite is
re-run with the new candidates, which both Stage 8 and Stage 11 state as a precondition.

| Parameter | Register v1.0 | Review round | Status |
|---|---|---|---|
| Active-set size | 60 | **75** — Top-10 VP p95 34.2% vs 38.3%, better Nakamoto and coverage | ✅ **adopted 2026-09-03** (owner); size still gated on B1 |
| Per-validator cap | 2% of total bonded | **two-layer** (see ADR-0012) | candidate, re-run required |
| Lottery weight | ∝ quality² (γ=2) | **γ=1.0** balanced; no γ dominates all objectives | candidate |
| PS governance weight | 0.15 | a policy compromise inside a **10–20% band**, not an optimum | wording change now, no numeric change |
| Top-K / lottery split | 70/30 | stays 70/30, but as an explicit **access/rotation policy**, not a calibrated optimum | wording change now |
| Entity cap | 5% | stays 5%, explicitly **not** a unique optimum vs 7.5% / 10% | unchanged |
| Anti-cartel thresholds | warn ≥0.60×2, quarantine ≥0.75×2 + 2 evidence classes | **confirmed** — 0.90 buys negligible false-positive gain for a much longer attack window (high-signal detection by week 12 falls 95.1% → 28.1%) | confirmed |
| WARP threshold | eligibility 65, start 80 | **on the recalibration list** (60/70/80/90 sweep) | open |

Three things worth carrying rather than filing:

- **Stage 9 (delegation economics) was never run.** The notebook is a stub — three comment lines
  and an "expected outputs" list, no manifest, no results. Stages 8, 10 and 11 are complete with
  fixed seeds and run manifests. Do not cite Stage 9 as evidence for anything, and do not let it
  travel in a set that implies it was executed.
- **Gas fees are structurally zero today** (Response v2 §8: minimum gas price is 0). Our gas-offset
  code (§2.5) is correct and currently offsets nothing. The site says "at high enough fee volume"
  and is accurate; the honest answer to "how much has it offset" is zero, by construction.
- **The cadence gap above is what conditions the size comparison.** Turnover is a rate, so 60-vs-75
  was compared without a stated rotation cadence.

### 2.4 Entry, caps & entity controls — ✅ caps and queue (S6) · 🔜 entity registry

- **Entry counts owner self-bond only** (delegations never count). Floor **2,000 SCN**
  at 112 candidates; adaptive ladder retained under 🔒 governance review.
- **Global bonded cap = min(8% × max supply, 60% × public circulating)** ≈ 870,000 SCN at
  the current snapshot (60% guardrail binds). At the cap: **queue** net new stake;
  redelegation and replacement of excluded validators stay allowed.
  Public circulating supply is **computed on-chain** each epoch (project decision
  2026-09-28): total supply less the distribution, fee collector, gov and rewards
  accounts, less everything owned by `excluded_addresses`. Bonded and queued stake
  count as public — subtracting them is the 37.5% trap ADR-0012 describes.
- **Per-validator cap = max(2% × EWMA-30 of total bonded, `validator_cap_floor`)**
  (project decisions 2026-09-28 and 2026-10-04, ADR-0012). The base is a 30-epoch EWMA,
  recomputed once per epoch with the other caps, so it moves slowly and a check reads
  one value for the whole epoch. A validator pushed over-cap by a base move is frozen —
  it stops accepting new stake — rather than force-shed. The floor keeps delegation
  possible while the set is small; 0 in code, 2,000,000 SCN on the devnet.
  🔒 B5: the >35% Top-10 rebalancing trigger exists as a name only — mechanism to design.
- **Entity caps: 5% aggregate stake · max 2 active seats · 1 lottery ticket.**
  🔒 **B3: beneficial-owner attestation BEFORE first activation** — detection is the second
  line: Stage 7 measured 13–84 days of attacker VP above 33% before containment (P95
  detection day 85–128). Delegator-side: split across **max 3 validators**.

### 2.5 Emission, rewards & gas — 🔜 (Q3 priority; MainNet-blocked)

- ✅ **AnnualSecurityBudget = clip(0.15 × TotalBonded, 0.005 × CurrentSupply, 0.0075 ×
  CurrentSupply)**, paid per epoch (`/epochs-per-year`): **238 SCN/day** medium, **357** max
  at the current snapshot. (Old 1,100/day rejected: 46–70% APR, 9-year pool runway.)
  Shipped 28 Jul as real params — the flat mock is gone.
- ✅ **S6 (24 Sep – 7 Oct), shipped in v0.8.0: security budget ÷7.** Requested by the application team,
  accepted 24 Sep. All three clip rates divided by seven, so the formula keeps its shape:
  on the Stage-5 snapshot the floor/ceiling become **34 / 51 per day** (were 238 / 357);
  on DevNet, where the ceiling binds, issuance goes from **205 to ~29 SCN/day** — the 205 is
  measured, not modelled: supply grew 205.79 SCN over the 24.09 h ending 24 Sep 14:17 UTC,
  against 204.94 from the formula on live supply and bonded. Runway to the 21M ceiling at zero
  fees ~25 → ~177 years; the fee volume at which minting stops falls by the same factor
  (130,317 → 18,617 per year on the snapshot).
  *How:* the defaults carry it, so a chain started from a fresh genesis starts at ÷7 (v0.8.0's
  devnet reset does exactly that; a running network would take it by a governance
  `MsgUpdateParams` on `x/rewards`, no code change and no restart — `Validate()` in
  `x/rewards/types/params.go` accepts the new values): each is computed as `Register value .QuoInt64(7)`,
  i.e. 0.021428571428571428 / 0.000714285714285714 / 0.001071428571428571, pinned by
  `TestParams_DefaultsAreTheRegisterRatesDividedBySeven`.
  *Stated plainly:* gross bonded yield falls to **~2.1%** on the paper's basis (15% today) and
  **~1.1%** on DevNet's (7.5% today, ceiling-bound), both before commission. No study covers
  validator operating cost at either figure — White Paper §12.3 readiness criterion #7 is the
  open item this makes pressing, and a research brief should go out with the slot. The
  papers still carry 238 / 357 (the constants are now fixed, ÷7 in the defaults) and
  must be updated in one pass.
- ✅ **Gas fees offset issuance:** `Emission = max(0, Target − GasFees)`;
  `TotalRewardPool = Emission + GasFees`. At ~100k SCN/year of fees minting stops entirely.
  Shipped 29 Jul. Fees are **observed, not taken** — `x/distribution` sweeps the collector
  every block, so moving them would pay the same fees twice. Consequence, stated plainly:
  the fee half of a validator's income is stake-weighted and **not** under the PoP gate.
  Closing that is the open F1/F3 decision, which depends on the funding-source answer below.
- ✅ **A10/B8/B12: single authoritative mint path** — the epoch pool is minted **once**, and
  `x/mint` is **removed from the app** (29 Jul), so no second module can mint;
  **21M cap enforced + kill switch**; a wiring test asserts no second `Minter` permission,
  and a live run showed supply growing by exactly the two epochs' emission and nothing else.
- Distribution ∝ final VP among the eligible active set; EP gate (V-2) stays underneath.
- Commission **10%** default, governance range 5–15% (🔒 "not economically proven" — B7).
- **Rounding remainder: carried forward** (base units), never to the validator owner.
  Refined by ADR-0011 D5 and shipped that way: the carry **offsets issuance** rather than
  enlarging the pool, so an epoch always pays exactly its target and never more. Adding it on
  top would let a rounding artefact raise the security budget, which is a ceiling on what an
  epoch is worth. Same shape as the gas-fee offset above. (Register status: APPROVE NOW.)
- 🔒 **A13/B9 unresolved — terminal economics:** at zero gas, max supply ≈ year 26, then
  gross APR → 0. Fee target / reserves / terminal issuance must be defined before MainNet.
- ⚠ Owner sign-off needed on Q2: the management questionnaire answered "pre-funded pool";
  the Register (and all studies) assume issuance+fees. Conflict must be closed explicitly.

### 2.6 Slashing — 🔜 (config overrides + ADR-0010)

| Event | Rule (Register) | Today in code | Work |
|---|---|---|---|
| Ordinary downtime | **Phase 1: zero stake slash** — WARP reduction, reward loss, jail/ineligibility; rolling signed-block window; confirmed common outage exempt | **0 — applied** in the genesis templates (ADR-0010, owner 2026-07-28) | ✅ done |
| Double-sign | **10% of bonded** + WARP/PS→0 + C=0 + jail + **permanent tombstone** | **0.10 — applied**; C=0 + jail + tombstone ✅ | ✅ fraction done; WARP/PS→0 still needs the **V-8 revision** (ADR-0010 §4, proposed) |
| Proven coordinated attack | separate, heavier evidence-based rule | — | ADR-0010 |

### 2.7 Compliance ladder & appeal — 🟡

Built ✅: C-flag, governed unjail (gov proposal + 1-day cooldown + tombstone-never-resets),
ClearFlag JAILED-refusal. To build 🔜: **soft compliance auto-restores** after remediation +
**7 consecutive compliant epochs**; risk ladder — **warning ≥0.60 ×2 epochs** (monitoring
only) / **quarantine review ≥0.75 ×2 epochs + ≥2 independent evidence classes** (≤72h,
published evidence hash, appeal path, 14-day review SLA); **risk score alone never slashes**.
🚧 reconcile the 14-day review SLA with the shipped 1-day unjail cooldown (different things;
state them together). 🔒 B4: detector calibration needs a real entity-labelled dataset
(current thresholds are synthetic; a low-signal cartel evades quarantine ~91% within 12 weeks).

### 2.8 Delegation — ✅ (`x/delegationrouter`, S7; ADR-0005)

Shipped in v0.9.0 (S7); the public DevNet serves it after
its reset (the devnet operations runbook), not before.

- ✅ **`MsgDelegateRouted { delegator, validator, amount }`.** The chosen validator is the
  primary and is filled up to `validator_cap − tokens`. The remainder goes to at most
  `max_route_legs` (default 2, bound [1, 4]) other validators that are registry ACTIVE, not
  jailed, carry a score of at least `min_route_score` (default 0.75, bound [0.50, 1.00]) in
  the latest closed epoch, and have room; weight `w = score × room ÷ max(tokens, 1)`,
  ties by operator address, shares by water-filling with the truncation remainder to the top
  leg. If the legs cannot place the amount the message fails with `ErrNoRoute` naming what
  fits. The weight is a **design choice under research**: brief S-13 (the executed
  allocation comparison) has no run in the archive, and the function is one body so a
  measured result replaces it alone. `entityAllows` returns true until S8's entity registry.
- ✅ **The global cap is honoured in the same message.** Legs execute in order, each taking
  what headroom allows directly; the part above headroom becomes a queue entry on that leg's
  validator (the S6 queue, without its entry floor for routed remainders). All or nothing.
- ✅ **`route` preview query** (`/saicon/chain/delegationrouter/v1/route?delegator=&validator=&amount=`)
  returns the plan the message would execute now, or the refusal as `reason`. Same function
  as execution.
- ✅ **At most 3 validators per delegator, on every path** (`MsgDelegate`, redelegation,
  cancel-unbonding, the router, the queue). A slot is a delegation or a queue entry; an
  operator's own self-bond and unbonding entries are not slots. Governance parameter
  `max_validators_per_delegator` (3, bound [2, 5]).
- ✅ **Minimum delegation 100 SCN** on `MsgDelegate`, `MsgDelegateRouted` and
  `MsgEnqueueDelegation` (`min_delegation`, bound [1, 10,000 SCN]); `min_queue_entry`'s
  default moves to 100 SCN.
- ✅ **The staking backstop.** The S6 caps, the entry floor and V-3 are enforced in
  `x/staking` delegation hooks as well as the ante handler, so `x/gov`, `x/group`, an ICA
  host and `authz` cannot pass around them. The hook is inactive in the BeginBlock phase
  (slashing runs there and must never be blocked) and until the cap state exists. What it
  does not see is in §1.4 (C1).
- ✅ 21-day unbonding (V-5); direct redelegation where protocol-safe.
- *Gas:* a routed delegation needs about 1,000,000 gas; the 200,000 default fails.
- *Under research:* `decentralization_aware` was chosen qualitatively over
  `equal_under_cap` (near-tie HHI, coverage 0.581 vs 0.694); "≤3" is an asserted knee point;
  the weights wait on S-13. Rebalancing delegations that are already concentrated is not
  designed (B5).
- *Verification:* in-process app tests drive the router, every backstop path, a real gov
  proposal and the slash window; the localnet demo `chain/scripts/demo-delegationrouter.sh`
  runs the story on the three-validator compose. The simulator does not exercise these rules
  (its staking operations are weighted 0 in `sim_params.json`).

### 2.9 Green / Kilowatt — Phase 1 information-only

**GreenFactor = 1.0 for everyone**: no gate, no VP bonus, no reward multiplier, no mandatory
RWA holding — data collection + reputation badge only. Best-grounded decision of the program
(30-seed MC: every economic variant → 32.4% pay-to-play exposure; gates → up to 90.6%
regional zero-power; info-only → 0/0/0). Future pilot (only after 🔒 B10 verifier economics:
FP/FN ≤2%, regional coverage, no pay-to-play): at most **2% exemption-aware penalty**
(verified-or-exempt 1.00 vs accessible-unverified 0.98). v1's "mandatory RVA, 25% CRKPI" is
dropped.

### 2.10 Governance bounds (bounded DAO — ADR-0013)

DAO **may** tune: PS weights, WARP params, epoch length, cap percentages, commission range —
versioned, within bounds. DAO may **not**: lower the entry floor, bypass/disable the
participation layer, override slashing, reset a tombstone. PoP is not governance-capturable.

**Three tier-1 parameters contradict the sentence above** — found 2026-08-15 while writing
ADR-0013, by reading the parameter surface rather than the Register. `epoch_length_blocks = 0`
disables the epoch tick, and with it every score, all EP accrual and all emission;
`rewards.enabled = false` is the deliberate emission kill switch; `unjail_cooldown_blocks = 0`
turns the jail delay into a formality. All three pass `Params.Validate()` and are reachable by
an ordinary `MsgUpdateParams`. None is a code defect — each behaves as written — but the
Register forbids by prose what the parameter surface permits by construction. ADR-0013 Open
problem 1 carries the recommendation; the cooldown minimum is a one-line change with no open
question behind it.

### 2.10.1 Data retention — decided, in three parts ✅ policy / 🚧 verification

**Decided and in the entrypoints.** Both node images apply the policy on every start —
`chain/docker/entrypoint-singlenode.sh` and `chain/docker/testnet/entrypoint-node.sh`:

| Setting | Validator (default) | Archive (`NODE_PROFILE=archive`) |
|---|---|---|
| `pruning` | `custom` | `nothing` |
| `pruning-keep-recent` | `100000` | — |
| `pruning-interval` | `10` | — |
| `min-retain-blocks` | `362880` (the evidence window) | `0` (never) |
| `snapshot-interval` | `1000` | `1000` |
| `snapshot-keep-recent` | `2` | `2` |

Two constraints bind those numbers, and neither is enforced by the SDK:

- `pruning-keep-recent` must exceed `snapshot-interval × snapshot-keep-recent`, or the state
  versions a snapshot needs are pruned before it is taken. 100,000 > 2,000 holds with room.
- `min-retain-blocks` cannot go below the evidence window whatever is configured.
  `baseapp.GetBlockRetentionHeight` takes the minimum of the evidence, snapshot and local
  constraints, so consensus safety wins over disk. It is set to the window explicitly rather
  than left implied — **21 days of blocks is the floor, not a choice.**

**Applied on every start, deliberately.** These are `app.toml` settings, not genesis, so a node
that already has a chain directory adopts the policy by restarting — no state reset, no
migration. Until 2026-08-12 the block sat inside the entrypoint's first-init branch, which meant
every node created before the policy existed kept the stock configuration for ever; the running
devnet is one of them. The three-node testnet entrypoint had no retention block at all, so the
environment closest to what an invited operator meets ran the defaults in full.

**What is still open** is verification, not the decision: nobody has confirmed the policy in
force on the deployed devnet (that needs a restart and a look at the node's own `app.toml`,
which is DevOps's side), no archive node is known to be running, and the two decentralisation
mitigations below — published state hashes and independent snapshot sources — are named but not
built.

**What the defaults were, and why they were wrong:**

| Setting | Value | Effect |
|---|---|---|
| `pruning` | `default` | keeps the last **362,880** state versions — at our 5s blocks, exactly **21 days** |
| `min-retain-blocks` | `0` | blocks are **never** deleted |
| `snapshot-interval` | `0` | state-sync snapshots are **off** |

That combination is incoherent. We discard state older than three weeks — so the chain cannot
answer a question about last month — while storing every block forever to support a replay
nobody can perform quickly, because snapshots are off. It is the worst of both: the disk cost of
an archive with the query surface of a pruned node. The sizing model puts the bill at ~5.4 TB
over two years at 75 validators, against ~91 GB with block pruning on (`chain/scripts/hardware-model.py`).

**The three parts, and why they are one decision:**

1. **Turn on block pruning** on ordinary validator nodes. Alone: cuts disk ~60×.
2. **Turn on state-sync snapshots** (`snapshot-interval > 0`). Without this, part 1 is a trap: a
   new node's only route is replaying from genesis, and if every node has pruned, there is
   nothing to replay from. Doing 1 without 2 quietly makes the network unjoinable.
3. **Run a read tier** — nodes that are neither validators nor part of consensus, serving the
   explorer, incident forensics and anyone auditing history. Needed whether or not 1 and 2
   happen. Note that this bullet used to say "run archive nodes", which glued two jobs with very
   different load profiles onto one machine: serving an explorer is a high request rate over
   almost entirely recent data, while forensics is a rare query that goes deep. §2.10.2 splits
   them.

**Deadline pressure:** part 2 gates **S9 (invite DevNet, Nov 5–18)**. An invited operator who
must replay the whole chain from genesis waits days before producing a block, and first-run
experience decides whether they stay. It also feeds the node-requirements item below: the
published minimum cannot be lowered honestly until the retention policy behind it is stated.

**Verified locally on 2026-08-12, both profiles at once.** A fourth node was
added to the testnet bench (`chain/docker-compose.testnet.archive.yml`, `init-archive.sh`) and
`chain/scripts/test-node-profiles.sh` now checks the pair: **35 checks, 0 failures**. What it
established — the archive joins as an ordinary full node with no gentx and no stake, carries
voting power 0 against a validator set of exactly 3, tracks node0 with 0 blocks of lag, serves
block 1, and each profile's `app.toml` carries the values the table above specifies. Restarting
the archive re-applied the policy without a resync, which is the claim this section makes about
applying on every start.

**Planned next on the same bench:** the long-run behaviour. A validator keeps 100,000 state
versions and 362,880 blocks, so watching a validator's disk stay flat while an archive's grows
needs a chain past the retention floor — a soak run alongside the DevNet sizing window rather
than a minutes-long one. The check was confirmed to fail when a value is broken by hand, so its
passing is evidence rather than silence.

**The decentralisation question, answered rather than reassured.** Validators pruning while the
project runs the archives does *not* weaken consensus: every validator still independently
verifies every new block against the rules, which is where the chain's safety lives. Storing the
past is a different property from validating the future. But two real risks come with it:

- **Single-source history.** If the project runs the only archive, it holds the only complete
  record, and anyone auditing the past must trust it. Mitigation is not a promise — it is
  publishing periodic state hashes on-chain so any independent archive can be checked against
  them, keeping the archive role permissionless and documented, and naming independent archive
  operators as a wanted role rather than assuming none will appear.
- **Snapshot trust.** A node bootstrapping from a snapshot verifies it against a header the
  operator supplies, so a monoculture of project-run snapshot providers is a weak point. This is
  the known Cosmos trade-off; the mitigation is several independent snapshot sources with
  published hashes, and keeping full replay available for anyone who prefers it.

Neither risk is a reason to keep the current setup, which has the costs of an archive and the
guarantees of neither. Both are reasons to write the policy down publicly instead of letting it
be an accident of default config.

### 2.10.2 Node roles — the standard vocabulary, and where SAiCoN sits in it

Retention is one axis. **Exposure** is a different one, and keeping them apart is what stops a
signing node from ending up behind public traffic. Nothing here is ours to invent: Cosmos,
Ethereum and Polkadot all use the same set of roles. Two of the five are in the plan rather than
in the tree, each with a slot against it.

| Role | Keeps | Public surface | Status |
|---|---|---|---|
| **seed** | nothing — serves peer addresses, then disconnects | p2p | not required yet |
| **sentry** | `full` retention (ADR-0015 D4) | p2p, faces the internet | ADR-0015, adopted S4 |
| **validator** | consensus + evidence minimum | none — peers only | running |
| **full** | all blocks, pruned state | RPC/REST behind a limiter | running (S4) |
| **archive** | everything from genesis | restricted | running |

**Cosmos keeps node types out of the protocol** — they are conventions expressed in config. The
SDK's own generated `app.toml` already names the far end of the range: *"nothing: all historic
states will be saved, nothing will be deleted (i.e. archiving node)"*. It also keeps **state
pruning** (`pruning-*`) and **block retention** (`min-retain-blocks`) as two independent knobs,
so "keep every block, prune state" is a combination the SDK supports by design rather than a
profile anyone has to invent.

**Substrate makes the same split explicit** as `--state-pruning` and `--blocks-pruning`; its
default node keeps state for the last 256 blocks only, and archive is the opt-in. Its own docs
split RPC nodes into archive and pruned, calling the pruned one suitable for most day-to-day
operations. **Ethereum** is the same axis with numbers attached — roughly 1.2 TiB for a full node
against about 2 TB for a path-based archive since geth's January 2026 change — which is why
public RPC providers run many full nodes and very few archives.

**The middle profile — shipped S4.** `NODE_PROFILE` carried two values until then, so a node
that was not a validator took the whole archive profile. An explorer needs blocks
and transactions far more than historical state, and state is the part that grows fastest: a node
with `min-retain-blocks = 0` and a moderate `pruning-keep-recent` answers nearly every explorer
query at a fraction of an archive's disk.

The window did not need inventing. `full` is **the validator's state window with the archive's
block retention** — `pruning-keep-recent = 100000`, `min-retain-blocks = 0`. That number is
already defended: it must exceed `snapshot-interval x snapshot-keep-recent`, and 100,000 >
1,000 x 2 holds with room. Inheriting a justified number is what let this ship before the
measurement instead of after it, without the extrapolation the node requirements took once.
**What still waits for the DevNet measurement is the disk figure, not the window** — how much
"every block, pruned state" costs on this chain, published for all three profiles at once when
task 6 of the v0.4.0 handover returns.

**The sentry layout — ADR-0015, adopted S4.** A validator's job is to sign, and public
query load on a signing node is an availability and attack-surface question independent of what
that node stores: an explorer that gets popular, or one bot in a loop, would otherwise reach the
same machine that produces blocks. The standard Cosmos answer is the Sentry Node Architecture —
the validator runs `pex = false` with `persistent_peers` pointing only at its own sentries, and
the sentries run `pex = true` while listing the validator in `private_peer_ids`, so the
validator's address is never gossiped. Two or more sentries, in different regions and at
different providers.

Today the nodes run `pex = true` with `private_peer_ids = ""`, which is the SDK default and
appropriate for a single-operator devnet. **This is the higher priority of the two and it is
ADR-level:** the middle profile is a disk question, the sentry layout is a liveness one, and it
changes what an invited operator is told to run — so it lands before **S9**. Written up as
[ADR-0015](adr/ADR-0015-sentry-layout-and-node-exposure.md), which also decides that a sentry
takes the `full` profile: retention and exposure are separate axes, so that is a choice rather
than a consequence.

### 2.11 Superseded v1 values (record)

> **These are TestNet candidates, not MainNet constants**, and the Stages 8-11
> community report (August 2026) has already moved one of them. Read §2.2.1
> below before quoting any number here as settled.

| Parameter | v1 (founder spec) | Register v1.0 |
|---|---|---|
| PS weights | 4-signal .45/.25/.2/.1 | 3-signal **0.50/0.35/0.15** |
| Smoothing | EMA α≈0.3, β/δ | **WARP 0.65/0.10**, threshold 65, initial 80 |
| Voting power | EVP = stake×M_PS×P_conc×C | **VP = min(stake,cap)×WARP/100×C** |
| Active set | 50–300, ~75 | **75 = 53 + 22 lottery** (v1.0 adopted 60; raised 2026-09-03) |
| Global cap | 8–10% max supply (open) | **min(8% max, 60% public)**, queue |
| Validator cap | ~10,000 SCN absolute | **2% of bonded** |
| Entity | ≤2 validators; top-10 ≤40% | **5% stake · 2 seats · 1 ticket**; rebalance >35% |
| Emission | inflation 2%, tiers 0.5/1/1.5 | **clip(15% bonded, 0.5–0.75% supply)**, ∝VP, gas offset |
| Downtime slash | proportional | **zero (Phase 1)** |
| Double-sign | 10% target | 10% + WARP/PS=0 + tombstone |
| Green | mandatory RVA, 25% CRKPI | **info-only**, future ≤2% pilot |
| Entry | adaptive ladder | **self-bond only**, 2,000 |
| Remainder | (undiscussed) | **carry to next epoch** |

### 2.2.1 What Stages 8-11 changed, and what is still open

The community report *Detailed report on experiments Stages 8-11* (August 2026)
re-examined the Register's numbers against a numerical sweep rather than against
the reasoning that produced them. Its own status line is the important part:
**TestNet candidates, not immutable MainNet constants.**

| Parameter | Register v1.0 | After Stages 8-11 | What it would take to fix it |
|---|---|---|---|
| Active set | **60 = 42 + 18 lottery** | **75 preferred** — ✅ **adopted 2026-09-03** | Consensus performance benchmark across 50/60/75/90 — latency, throughput, block propagation, consensus stability on a real network. B1 can still move it |
| Top-K / lottery | 70/30 | 70/30 held, as a policy compromise rather than a score optimum | Governance confirmation |
| Lottery gamma | 2.0 | 1.0 | Integrated rerun |
| PS weights | 0.50/0.35/0.15 | unchanged; all five candidate models sit on the Pareto frontier, so 15% governance is not a unique optimum | Real TestNet sensitivity |
| Warning threshold | 0.60 | 0.60 × 2 epochs, non-punitive | Measure review workload |
| Quarantine | 0.75 | 0.75 × 2 epochs + 2 independent evidence classes | Real entity-labelled dataset |
| Validator cap | 2% of bonded | two-layer 2% — see the denominators below | State-transition implementation |
| Entity cap | 5% | 5% + 2 seats + 1 lottery ticket | Entity verification operations |

**The two-layer cap, written out.** "2% of bonded" is not a specification — Stage 11's whole
point is that the denominator was never fixed, and the two layers use *different* ones:

| Layer | Cap | Denominator | Why this one |
|---|---|---|---|
| Delegation acceptance | 2% | **30-epoch EWMA of total bonded** | intake capacity must not jump the epoch after a single move |
| Voting power and rewards | 2% | **current eligible bonded** | concentration protection must not lag during an exit |
| Known entity | 5% | the same two bases as above | a different base would reopen the ambiguity one level up |

Above the cap the validator is **not slashed and not force-unbonded**: it stops accepting new
delegation, its reward and voting stake are capped, and the delegated excess leaves through direct
redelegation or the 21-day unbonding path. The 30-epoch window is provisional and needs TestNet
calibration. Note what the window means: at the intended ~24h epoch it is a month; the devnet's
100-block epoch would make it four hours, which is one more reason the production epoch length is
not a cosmetic setting.

**A correctness finding worth carrying forward.** Stage 11 reconciled the simulation against the
Register and found they had been computing different things: the Stage 3 code derived the cap from
`target_bonded = min(global cap, stake demand)`, while the Register described it as a percentage of
*total bonded stake*. Those diverge whenever demand is below the global cap. The production
specification must state which one it means — **stake demand must not silently substitute for
realised bonded stake** — and the entity cap must use the same bases as the validator cap.

**Stage 9 is not a result.** Its notebook contains the plan and the expected outputs, and no
executed computation at all — the delegation comparison (Equal Under Cap / Quality Weighted /
Hybrid) rests on the report's prose, not on a run anyone can reproduce. Hybrid is a provisional
Pareto compromise, and the report says so; treat it as undecided rather than as the weakest of
three measured options.

Why 75 rather than 60: it lowers worst-world P95 Top-10 voting power from about
38.3% to 34.2% and raises the P05 Nakamoto coefficient from roughly 9 to 10.2,
at the cost of mean technical quality (95.42 → 94.60). Active set 90 buys almost
nothing further on concentration and keeps degrading quality.

**Nothing in the table above is settled.** The report is explicit that the single
integrated rerun — all Stages 8-11 parameters applied together over 104+ epochs
— has not been performed, and that Decision Register v2 is the artifact that will
fix these values. Stage 9's delegation package is the weakest link and needs a
reproducible rerun before Hybrid is finalised.

**What the chain runs today.** `max_validators = 75` — Register v1.0 adopted 60, and the owner
raised it on 2026-09-03 — set in `chain/docker/entrypoint-singlenode.sh` and
`chain/docker/testnet/gen-genesis.sh`. It is the SDK's `x/staking` parameter, so
moving it later needs **no chain restart and no migration** — a governance
`MsgUpdateParams` changes it on a running chain, admitting more validators at the
next EndBlocker or dropping the lowest-ranked into unbonding. The explorer's
honeycomb already reads `max_validators` from the chain rather than assuming a
size, so the picture follows the parameter on its own.

Most of the rest of that table is unimplemented: the selection layer, the lottery and
the cartel detector are Q4 2026 / Q1 2027 work (§4). The stake caps and the stake queue
are built (S6, `x/stakeengine`, §1.1); entity caps and a path for new validators when
the global cap is full remain later. Today
voting power is stock stake-weighted top-N, so `max_validators` decides only how
many validators the set holds — the 53 + 22 split is 70/30 of 75 and does not
exist in code.

---

## 3. Evidence & calibration

> The Register's values are the output of a **seven-stage calibration program** — grid sweeps
> and Monte Carlo over the **real SCN holder distribution** (112 candidate validators) with
> **synthetic** validator behaviour, incidents and attacker models. The program's own words:
> it *"validates declared formulas and scenarios, not the correctness of production
> implementation code."* Every number below is a **DevNet baseline** pending recalibration on
> real telemetry (B2). This section is the load-bearing evidence; the caveats are kept next to
> the results, not hidden — a value's strength is stated with it.

### 3.1 Core formulas & algorithms

```text
Participation Score (per epoch, each signal normalized 0–100):
    PS = 0.50·Uptime + 0.35·BlockProductionQuality + 0.15·GovernanceParticipation

WARP (smoothed reputation, asymmetric EWMA):
    WARP ← WARP + α·(PS − WARP),   α = 0.65 if PS < WARP   (fast fall)
                                   α = 0.10 if PS ≥ WARP   (slow recovery)
    eligible ⇔ WARP ≥ 65 ;  new validator starts at WARP = 80
    confirmed network-wide outage ⇒ WARP frozen ;  1st missing-telemetry epoch ⇒ grace

Voting Power (consensus weight AND reward share):
    VP = min(bondedStake, validatorCap) · WARP/100 · complianceFlag        (C ∈ {0,1})

Active-set selection (75 seats, weekly):
    pool     = { v : status(v)=ACTIVE ∧ C(v)=1 ∧ WARP(v) ≥ 65 }
    pool     = applyEntityCaps(pool)          # ≤2 active seats & ≤5% agg. stake per entity
    top53    = rank(pool) [0:53]              # deterministic Top-K
    rest     = pool \ top53
    lottery22 = draw(rest, weight ∝ ranking², one ticket per verified entity)  # ← RNG = VRF (B11 decided)
    activeSet = top53 ∪ lottery22

Emission (per epoch):
    budget   = clip(0.15·TotalBonded, 0.005·CurrentSupply, 0.0075·CurrentSupply) / epochsPerYear
    minted   = max(0, budget − gasFees)       # gas fees offset issuance
    pool     = minted + gasFees               # single authoritative mint path; enforce 21M cap
    reward(v)= pool · VP(v) / Σ VP            # ∝ VP; remainder carries to next epoch
    gate:      only ACTIVE ∧ C=1 ∧ EP>0 receives (invariant V-2)

Delegation overflow (stake above any cap):
    redistribute to eligible v, weight ∝ f(quality, remainingCapacity, 1/currentStake)
    exclude jailed / quarantined / below-threshold / entity-capped ;  delegator ≤ 3 validators
```

### 3.2 The seven-stage program

| Stage | Study | Search space | Headline result |
|---|---|---|---|
| 1 | KPI / WARP | 384 weight×speed combos → 44 admissible → 1 Pareto | drop 0.65 / recovery 0.10 vs 0.8/0.2: selective-participation false-acceptance **43.8% → 0%**, weak **2.67% → 0%**, WARP volatility 0.359 → 0.189; cost = 3-day-outage recovery **3 → 10 epochs** |
| 2 | Active set & VP | 1,200 rows → 300 configs → 276 pass → Pareto 82 | mean active WARP by size: 50→94.2, **60→93.4**, 75→91.9, 90→90.2; `stake_warp_linear` VP model; worst Top-10 VP **33.5%**, worst Nakamoto-33 = 7 |
| 3 | Delegation & concentration | 864×216 → 28 pass → Pareto 10 | per-validator cap **2%**: Top-10 22% (vs 3%→28%), acceptance 41.5%→24%; overflow `decentralization_aware` HHI 0.0119; worst Top-10 **21.7%** |
| 4 | Anti-cartel / multi-entity | attacker {5,10,20,33}% × {3,5,10,20} nodes | cap sweep {5,7.5,10,none}% → attacker VP {3.2, 3.2, 4.0, 4.4}%; **seat cap 2**: max attacker VP **53.8% → 3.2%**, P(VP≥33%) **100% → 0%**; low-signal cartel still evades quarantine ~91% in 12 wks |
| 5 | Rewards & emission | 4 bonded scenarios × commission × gas × pool | clip → **238 SCN/day** (med) / **357** (max); 1,100/day rejected (**46–70% APR**, 9-yr runway); gas 100k/yr offset ⇒ **infinite runway**; CurrentSupply ≈ 17,375,600 |
| 6 | Green / kilowatt | 6 policies × cost × asset-lock × error × 30 seeds | **information-only** = the only policy with **0% pay-to-play, 0% regional harm**; every economic variant = **32.4%** pay-to-play; gate-no-exemption = **90.6%** regional zero-power; 2% penalty break-even ≈ 12 SCN/yr |
| 7 | Full-protocol Monte Carlo | 9 scenarios × 5 seeds × 365 epochs = **16,425 epochs** | **6 pass / 3 fail** (§3.3); accounting error **< 3·10⁻⁸ SCN**, reward conservation **< 1.5·10⁻¹¹**, 21M cap never breached |

### 3.3 Integrated stress test (Stage 7) — the three failures

The value of the run is the **declared failures** — they became MainNet blockers, not swept
under the rug.

| Scenario | What happened | Blocker |
|---|---|---|
| `stake_exit` | a 30% delegation exit pushed **Top-10 VP to 36.2%** (limit 35%); the 2%-cap base is ambiguous (current vs target bonded) | B5 — rebalancing trigger (mechanism still to design) |
| `hidden_cartel_20` | one concealed 20% entity across 10 nodes reached **35.4% VP for up to 13 days** before detection (P95 detection day 84.8); post-containment ≤ 2.3% | B3 — beneficial-owner attestation **before** activation; detector is the 2nd line |
| `combined_severe_33` | stacked shock (33% entity + provider outage + telemetry loss + 25% exit + double-sign): fill fell to **86.7%** (52/60 seats), attacker VP peaked **58.7%**, above 33% for up to 84 days | B6 — emergency reserve candidate pool |

Six scenarios passed cleanly (`normal`, `global_outage`, `provider_outage`, `telemetry_failure`,
`stake_inflow`, `double_sign`) — e.g. a correctly-classified global outage caused **0% false
exclusion** of good validators, and double-sign → immediate compliance exclusion. Long-horizon
supply: at zero gas, max supply is reached ≈ **year 26** and gross APR → 0 (**A13/B9**, terminal
economics — unresolved); at 100k SCN/yr of fees it is not reached by year 60.

### 3.4 What is measured vs asserted (honesty)

Adopting the Register wholesale was an explicit project decision. For the record, the calibration
strength is uneven — presented here so no reader mistakes a convention for a proof:

- **Quantitatively grounded:** emission rejection & APR math · WARP false-exclusion tradeoff ·
  Green information-only (30-seed, dominates on every harm axis) · gas-offset supply horizon ·
  accounting/conservation/21M in-model.
- **Asserted or inherited (accepted as baseline anyway):** the 70/30 split (Stage 2's own
  sensitivity favoured 80/20); active-set size (a qualitative compromise at 60, and a
  decentralization judgement at 75); **governance 0.15**
  (re-included with no recorded justification); entity 5% vs 7.5% (near-tie); quarantine
  threshold 0.75 (no comparison to the earlier 0.90); per-validator cap base 2% (no comparison
  to the earlier absolute ~10,000 SCN); delegator ≤3 (asserted "knee point", no chart);
  `decentralization_aware` overflow (near-Pareto-tie with `equal_under_cap`, worse coverage
  0.581 vs 0.694).
- **Genuinely open (our additions):** ~~lottery randomness source (B11)~~ **decided 2026-07-28: VRF** — was absent from every
  study) · Register↔code mapping (B12 — "current implementation 10%" describes a different
  codebase; the chain runs SDK 5% double-sign / 1% downtime, unmodified).
- **Whole-program caveat:** synthetic behaviour over real balances; only Green (30) and the
  integrated MC (5) used seeds, and the MC itself flags "5 seeds is enough for integration
  debugging, not production tail-risk." Full cross-verification against the seven studies is
  archived in the owner's analysis file (`Validator-Module-13-Questions-MERGED.md`).

---

## 4. ADR index

| ADR | Topic | Status |
|---|---|---|
| 0001 | Use Cosmos SDK | accepted |
| 0002 | Toolchain version pin | accepted |
| 0003 | Validator status enum | accepted |
| 0004 | Validator activation model | accepted |
| 0005 | Delegation router | accepted (built in S7) |
| 0006 | CosmWasm contracts | proposed → Q1 |
| 0007 | On-chain / off-chain / contract cost split | accepted |
| 0008 | VP formula + selection layer + **randomness (B11)** | **proposed** (written 2026-07-28) |
| 0009 | Multi-signal PS + WARP engine | **proposed** (written 2026-07-28) |
| 0010 | Slashing config + **V-8 revision** + attack rule | **proposed** (written 2026-07-28) |
| 0011 | Emission: clip budget, gas offset, single mint path, 21M | **proposed** (written 2026-07-28) |
| 0012 | Caps & stakeengine: tiers, queue, cap base, public-supply measurement, B5 | **proposed** (written 2026-07-28) |
| 0013 | Bounded DAO authority: three tiers, and where each bound is enforced | **proposed** (written 2026-08-15) |
| 0014 | Entity registry: attestation before activation, evidence ladder, appeal | **proposed** (written 2026-08-15) |
| 0015 | Sentry layout: exposure as an axis of its own, and the signing node off the open internet | **proposed** (written 2026-08-27) |

