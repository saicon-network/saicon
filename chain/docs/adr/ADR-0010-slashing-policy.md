# ADR-0010: Slashing policy, the V-8 revision, and the coordinated-attack rule

Status: **Accepted** — written 2026-07-28 (Q3 work item (3), [DEVELOPMENT.md](../DEVELOPMENT.md) §4); the coordinated-attack
rule (§3) decided by the owner on 2026-10-28 (D-S8-2) and implemented in S8 (v0.10.0). The v0.10.0 upgrade also
enforces the two fractions (double-sign 10%, downtime 0) and sets the evidence window to the unbonding period, 302,400
blocks and 504 h (§3). §1's window value and its common-outage exemption stay open (🚧).
Baseline: *Validator Parameter Decision Register v1.0* §2.6 ([DEVELOPMENT.md](../DEVELOPMENT.md):157–163),
adopted 2026-07-24. Related: [ADR-0003](ADR-0003-validator-status-enum.md) (status enum),
[ADR-0004](ADR-0004-validator-activation-model.md) (activation on bond),
[ADR-0009](ADR-0009-participation-score-and-warp.md) (PS/WARP engine, proposed),
[ADR-0014](ADR-0014-entity-registry.md) (entity registry + evidence ladder), [ADR-0013](ADR-0013-bounded-dao-authority.md) D5
(applying a bounded sanction is not an override), design the design notes.

### Why this ADR was proposed first, and what accepted covers

It carries four separable parts with different provenance:

| Part | Register basis | Status of this part |
|---|---|---|
| Downtime = zero stake slash (Phase 1) | adopted wholesale, DEVELOPMENT.md:161 | **recorded and APPLIED** — approval gate opened by the owner 2026-07-28; the value is in the genesis templates and, since 2026-10-28, in the binary. The rolling window's value and the common-outage exemption are open (🚧) |
| Double-sign = 10% of bonded + tombstone | adopted wholesale, DEVELOPMENT.md:162 | **recorded and APPLIED** — same 🔒 gate; in the binary since 2026-10-28 |
| Proven coordinated attack | named only, no rule stated, DEVELOPMENT.md:163 | **accepted** (owner D-S8-2, 2026-10-28) and **implemented** (S8, v0.10.0) — the Decision (D-S8-2) block in §3 |
| Revision of invariant V-8 | implied, not stated; flagged "slated for revision by ADR-0010", DEVELOPMENT.md:55 | **accepted and implemented** (owner sign-off 2026-07-28) — §4 |

Until 2026-10-28 two of the four were not project decisions, so the document as a whole stood as
**proposed**; with D-S8-2 every part has an project decision, and the document is **accepted**. The text
of the options and the recommendation in §3 is kept as written on 2026-07-28; where it differs from the
Decision block, the Decision block governs. Nothing below should be read as authorising a config change
to a live network other than through the upgrade handler and governance paths it names.

---

## Context

### What the chain actually does today

*As written on 2026-07-28. Since then the Register targets are in force: the genesis templates set
both fractions (2026-07-28), the binary's default genesis carries them (2026-10-28), and the
`v0.10.0` upgrade enforces them on a stored state (see the status table above).*

SAiCoN runs the **unmodified Cosmos SDK v0.53.6 slashing defaults**. There is no slashing section
in `chain/config.yml`, and `chain/docker/entrypoint-singlenode.sh` patches only `bank.denom_metadata`,
the `x/mint` inflation fields (zeroed for the single mint path, B8),
`participation.params.epoch_length_blocks` and `gov.params.voting_period` — it never touches
`app_state.slashing`. The values in force are therefore
`cosmos-sdk@v0.53.6/x/slashing/types/params.go:12–19`:

| Param | Value in force | Register target |
|---|---|---|
| `slash_fraction_downtime` | `1/100` = **0.01** | **0** |
| `slash_fraction_double_sign` | `1/20` = **0.05** | **0.10** |
| `signed_blocks_window` | 100 blocks | "rolling signed-block window" — **no value given** |
| `min_signed_per_window` | 0.5 | not addressed |
| `downtime_jail_duration` | 600s (10 min) | not addressed |

This is exactly blocker **B12** (DEVELOPMENT.md:378, and the honesty note at DEVELOPMENT.md:310–312:
the Register's "current implementation 10%" describes a different codebase). §1.4 already lists
"SDK slashing defaults unmodified (1%/5%)" as a known gap (DEVELOPMENT.md:62–63).

Two distinct "jails" exist and the Register's wording collapses them. `x/slashing` sets a *jailed
flag* on the staking validator; SAiCoN has its own `STATUS_JAILED` in the registry enum (ADR-0003).
A downtime jail today produces the former: staking unbonds the validator, which fires
`AfterValidatorBeginUnbonding` → `chain/x/validatorregistry/keeper/unbonding.go:75`, moving
ACTIVE/QUARANTINED → **UNBONDING**, not JAILED. `STATUS_JAILED` is reached only through
`x/compliance` on a `CRITICAL_VIOLATION` (`chain/x/compliance/keeper/compliance_state.go:84`).

### Why downtime slashing is the wrong instrument here

Stake-slashing for liveness punishes **infrastructure luck**, and infrastructure luck is not evenly
distributed. A large operator runs redundant power, transit and hardware and absorbs a regional
incident; a solo operator on one uplink does not. Charging both a percentage of bonded stake for the
same hour of downtime transfers capital from small operators to nobody (it is burnt) and compounds
the advantage of scale — the opposite of what a participation-weighted chain is for.

The participation layer already removes the income, and does it proportionally. Missed blocks lower
Uptime → lower PS → WARP falls at α = 0.65 → below the eligibility threshold 65 → no seat → no reward
(DEVELOPMENT.md:82, 98–100). For a validator whose economics are the reward stream, that is a
better-shaped penalty than 1% of bonded — and probably a larger one, though **nothing measured it**:
no stage swept a downtime slash fraction (§1 below), so the two magnitudes were never put side by
side and the comparison is a design argument, not a result. What is not in doubt is the shape: unlike
a burn it is **reversible by reclassification**. A confirmed network-wide outage freezes WARP
(DEVELOPMENT.md:101–102), and Stage 7's `global_outage` scenario measured 0% false exclusion for a
*correctly-classified* outage (DEVELOPMENT.md:289–291) — a 5-seed synthetic run the program itself
calls "not production tail-risk" (DEVELOPMENT.md:313–315), and the classification step it
presupposes is the one this ADR records as unimplemented (§1, "confirmed common outage exempt").
Burnt tokens cannot be un-burnt, and §2.10 forbids governance from overriding slashing to refund
them (DEVELOPMENT.md:198–199) — so a misclassified correlated outage under a slashing regime is a
permanent, uncorrectable loss.

### What breaks

The Register's double-sign row says "**WARP/PS → 0**". Invariant **V-8** currently reads
"EP changes only via participation; punishment never mutates EP" (DEVELOPMENT.md:55). Its letter
survives — WARP and PS are not EP — but its *spirit*, as it has been read and enforced, does not:
today the participation module has **no writer at all** outside `ComputeEpoch`. Verified: the only
non-test caller of `SetEP` (`chain/x/participation/keeper/scores.go:34`) is
`chain/x/participation/keeper/epoch.go:163`, and the only non-test caller of `SetSnapshot`
(`scores.go:15`) is `epoch.go:130`. `chain/x/participation/keeper/ep_v8_test.go:21` asserts the
property, and its own comment states the construction argument. Introducing a punishment-driven write
into participation-owned state is a real change to that boundary and must be stated, not left implied.

---

## Decision

### 1. Ordinary downtime — Phase 1 is a **zero stake slash** (recorded from the Register)

`slash_fraction_downtime = 0`. The punishment for ordinary downtime is:

- **WARP reduction** — the fast-fall arm (α = 0.65) drives WARP below 65 and out of the eligible pool;
- **reward loss** — the validator is not ACTIVE, so the reward gate `ACTIVE ∧ C=1 ∧ EP>0` (V-2,
  stated at `chain/x/rewards/keeper/emission.go:74–76`, enforced at `emission.go:209–235`) denies
  it, with the reason recorded in its RewardSnapshot;
- **jail / ineligibility** — the `x/slashing` jailed flag and the resulting loss of the seat;
- measured over a **rolling signed-block window**, with a **confirmed common outage exempt**.

Calibration basis, stated honestly: **none of the seven calibration stages swept a downtime slash
fraction.** §3.4's "quantitatively grounded" list does not include it (DEVELOPMENT.md:300–302). The
value is a design/policy argument (the centralisation argument above), plus the observation that
Stage 7's outage scenarios passed on the *classification* mechanism rather than on any slash size.
It is a DevNet/TestNet baseline like every other Register value, recalibratable under B2.

Two parts of this row are **not defined by the Register and remain 🚧**:

- **The window.** "Rolling signed-block window" is the SDK's `signed_blocks_window` /
  `min_signed_per_window` pair, which is already rolling (a missed-block bitmap over the last N
  blocks). The Register gives no N. The default 100/0.5 — jail after missing 51 of 100 blocks — was
  never chosen for SAiCoN and is far tighter than production Cosmos chains. Needs a value; the
  natural moment is the B1 consensus benchmark.
- **"Confirmed common outage exempt" has no implementation and no design.** The WARP freeze
  (DEVELOPMENT.md:101–102) exempts the *reputation* layer, not the `x/slashing` jail. A total network
  outage is self-exempting (no blocks are produced, so none are missed); the dangerous case is a
  **partial correlated** outage — one cloud region down while the chain keeps producing — where the
  affected validators are jailed by `x/slashing` with nothing in the code aware that the cause was
  common-mode. Who confirms an outage, on what evidence, and what the confirmation suppresses, is
  undecided. Recommendation: scope it to WARP only in Phase 1 (jail-and-recover is cheap once the
  slash is zero) and revisit if B2 telemetry shows correlated jails materially damaging seat fill.

### 2. Double-sign — **10% of bonded**, WARP/PS → 0, C = 0, jail, permanent tombstone (recorded)

`slash_fraction_double_sign = 0.10`, plus the SAiCoN-side consequences already shipped:
`C = 0` and `STATUS_JAILED` via `x/compliance` (`compliance_state.go:84`), and a permanent
`x/slashing` tombstone. The tombstone is genuinely permanent in code, and that rests on **two**
message-level gates plus a hook that never reactivates a jailed validator. Every route into ACTIVE,
enumerated — because this property is what §4 rests on:

- the governed unjail path refuses a tombstoned validator regardless of authority
  (`chain/x/compliance/keeper/msg_unjail.go:68`, `ErrValidatorTombstoned`);
- `MsgClearFlag` — the *other* authority-gated way to C=1, and the only other caller of
  `SetCompliance(true)` — refuses a JAILED validator outright
  (`chain/x/compliance/keeper/msg_evidence.go:56–62`, `ErrJailedRequiresUnjail`), so a proposal
  cannot use it to walk around the tombstone check;
- the remaining entry to ACTIVE is the staking bond hook, which skips JAILED
  (`chain/x/validatorregistry/keeper/activation.go:73–80`) and restores from UNBONDING only when the
  pre-unbonding status was ACTIVE *and* the compliance mirror is clean (`activation.go:106–120`) —
  neither holds for a double-signer at C=0, and a tombstoned validator cannot re-enter the bonded
  set for the hook to fire in the first place.

The second of those gates is younger than the first (see Consequences).

Calibration basis, stated honestly: **10% was not calibrated either.** §2.11 records it as the v1
founder-spec target that the Register carried forward unchanged (DEVELOPMENT.md:214). Stage 7's
`double_sign` scenario passed, but what it validated was the *response* — immediate compliance
exclusion — not the magnitude of the burn (DEVELOPMENT.md:289–291). 10% is a convention doubled from
the SDK's 5%; it should be labelled as such and not as a derived value.

### 3. Proven coordinated attack — ✅ **ACCEPTED** (owner D-S8-2, 2026-10-28) and **IMPLEMENTED** (S8, v0.10.0)

The Register defines this row as "separate, heavier evidence-based rule" and nothing more
(DEVELOPMENT.md:163). **No fraction, no evidence standard, no submitter and no governance path exist
in the Register or in any of the seven studies.** When this section was written (2026-07-28) it
therefore recorded no decision, only the options and a recommendation; the owner's decision of
2026-10-28 follows the recommendation, below.

What "evidence-based" has to mean here, and why it is hard: equivocation itself is objective —
CometBFT produces cryptographic duplicate-vote evidence per validator. **Coordination is not.** It is
an inference over a set of validators, and the seven-stage program's own finding is that this
inference is weak: a low-signal cartel evades quarantine ~91% of the time within 12 weeks
(DEVELOPMENT.md:173–174, B4), and Stage 7's `hidden_cartel_20` had a P95 detection day of 84.8
(DEVELOPMENT.md:286). A rule that slashes on inference at this error rate is a liability.

| Option | Trigger | Trade-off |
|---|---|---|
| A. Consensus-detected only | `x/evidence` duplicate-vote evidence, ≥K validators within window W | Objective and deterministic, but CometBFT evidence is per-validator; "coordinated" is never proven by it, only suggested. Also punishes a correlated *bug* (one client version double-signing across an operator fleet) as an attack. |
| B. Governance proposal alone | gov-authority `MsgSubmitEvidence` with a published evidence hash | Exists today (`chain/x/compliance/keeper/msg_evidence.go:16–34` — authority-gated, already yields C=0 + JAILED) and inherits §2.7's ladder (published hash, appeal path, 14-day review SLA). But a purely political trigger for a heavy burn is the capture risk §2.10 exists to prevent. |
| C. Automated cartel detector | risk score ≥ threshold | **Ruled out by the Register**: "risk score alone never slashes" (DEVELOPMENT.md:171). May trigger quarantine review only. |
| D. **Two-key (recommended)** | objective evidence **arms**; governance ratification **fires** | Neither key alone is sufficient: no burn without cryptographic equivocation evidence, and no burn without a ratifying vote carrying a published evidence hash and an appeal window. Cost: slow (a gov voting period), and it needs the entity registry (ADR-0014) to express "same beneficial owner". |

**Recommendation (D), with the parameters explicitly undecided:**

- *Arming condition:* duplicate-vote evidence for ≥K distinct validators at heights within a window W,
  or ≥2 validators linked to one verified entity (entity registry, ADR-0014). **K and W are
  undefined.** They cannot be picked from the existing studies; a wrong K converts a shared client bug
  into a 30%+ confiscation.
- *Standard of proof:* the equivocation must be on-chain and cryptographic. The coordination element
  is ratified, never inferred — the rule must be unable to fire on a risk score.
- *Submitter:* anyone may submit the equivocation evidence (permissionless; the path that works is not
  `x/evidence`'s `MsgSubmitEvidence` — see the Decision block); only the gov authority may fire the
  coordinated-attack sanction, matching the existing authority gate.
- *Sanction:* heavier than 10%, permanent tombstone. A governance-set fraction with a **code-fixed
  floor (the double-sign fraction) and ceiling (≤ 1.0)**; the bounds themselves are not governable.
  A **30%** default is *suggested as a starting point and is not calibrated by anything* — record it
  as a placeholder for project decision, not as a result.
- *§2.10 reconciliation:* governance "may not override slashing" (DEVELOPMENT.md:198–199). Firing a
  sanction **within** code-fixed bounds is an *application* of the policy, not an override; changing
  the bounds, or forgiving a slash after the fact, is an override and stays forbidden. This
  distinction must be written into ADR-0013 when it is drafted, or the two rules will read as
  contradictory.

#### Decision (D-S8-2) — registry path only, two keys

Decided by the owner on 2026-10-28 and built in S8 (`x/compliance`, release `v0.10.0`). The
recommendation (D) above stands, narrowed as follows:

- **Arming.** The rule **arms** when on-chain duplicate-vote evidence exists for **at least two distinct
  validators linked to one attested entity** (ADR-0014). The "≥K validators within window W" clause is
  **struck, not deferred**: there is no K-of-W path, no time window between the two offences, and no
  code path that counts offences across entities. Two validators of different entities that double-sign
  in one block (a shared client bug across unrelated operators) arm nothing; each receives the ordinary
  double-sign treatment and no more. The entity is the one the validator belonged to **at the infraction
  height**, so a removal or revocation between the offence and the evidence does not launder the
  linkage (`x/entityregistry` `EntityAt`, which keeps the height each validator joined and the last
  entity it left). Offences committed before `v0.10.0` never arm: no entity existed then, and nothing is
  back-filled.
- **Standard of proof.** Unchanged and now mechanical. `x/compliance` records an equivocation only after
  `x/evidence` has accepted and stored it, under `x/evidence`'s own hash (SHA-256 of the `Equivocation`
  it built, 64 uppercase hex). The chain never detects, proves or infers coordination: it records
  objective equivocations and an objective registry linkage, and the vote **ratifies** that they were one
  attack. A risk score can neither arm nor fire the rule (ADR-0014 D4).
- **Submitter.** Anyone, through CometBFT: duplicate-vote evidence gossiped between nodes, or injected
  through the `broadcast_evidence` RPC, reaches the application as the block's misbehaviour list. The
  text of this section named `x/evidence`'s `MsgSubmitEvidence`; on this chain it has no router (depinject
  wires `x/evidence` without one): `x/evidence`'s keeper dereferences the nil router and panics, which
  a transaction, x/gov and x/group recover into a failure, so nothing slashes and nothing halts
  (`app/backstop_test.go` `TestSubmittedEvidenceHasNoHandler`). CometBFT's misbehaviour list does not
  use that message at all: it reaches `x/evidence` in BeginBlock. Only the gov
  authority fires: `MsgFireCoordinatedSanction`, carried in a governance proposal, naming the entity, one
  to sixteen evidence hashes, the fraction and a reason (at most 256 bytes). There is no transaction
  command; the `sanction-preview` query runs the same planning function as the execution.
- **Appeal window.** A code-bounded floor between arming and the earliest burn: `appeal_window_seconds`,
  default **7 days**, bound **[1 day, 14 days]** (tier 2), counted from the later of the arming and the
  latest named offence, in block time. It does not depend on the gov voting period, which is tier 1. A
  proposal that passes before the window ends fails with `ErrAppealWindowOpen` and can be resubmitted.
  There is deliberately no cancel message after a passed vote.
- **Sanction.** The fraction `F` is `x/compliance.coordinated_attack_fraction`, set by governance,
  default **0.30**, code-fixed bounds **[0.10, 1.00]** and never below the chain's current
  `slash_fraction_double_sign`; the message restates it and it must equal the parameter at execution.
  The default is the owner's placeholder and is **not calibrated** by anything (B4 dataset, Q1'27 audit);
  the 10% floor is itself a convention (§2). Tombstone stays; the validator stays JAILED with compliance
  event **6** (`COORDINATED_ATTACK`), which only the firing writes; reputation is zeroed best-effort and
  EP is never touched (§4).
- **Total, not additive.** `F` is the **total** burn of a target: `x/evidence` has already burned `d`
  (the `slash_fraction_double_sign` it applied, stored on the offence, never assumed to be 0.10), and the
  sanction burns `F − d` more on the same base and the same infraction height, unbonding and
  redelegation entries included. A target with `F = d` has nothing to add.
- **Only the double-signers are slashed.** The proposal names offences, and each named validator is
  slashed. The entity's other validators signed nothing twice and are not touched; removing them from
  consensus belongs to the selection layer (S10, ADR-0008).
- **The deadline.** x/staking cannot slash a fully unbonded validator, so a sanction must execute within
  about 21 days of each target's evidence. A target that has reached `Unbonded` is recorded
  `NOT_SLASHABLE` and the other targets are sanctioned.
- **Only the incarnation that committed the offence.** Once x/staking removes a fully unbonded
  validator, a new one may be created on the same consensus key, by the same operator or another.
  `x/evidence` does not slash that key again (upstream behaviour: it ignores evidence against a
  tombstoned key), but a sanction that resolved its target by consensus address would find the new
  incarnation and burn it, entries of delegators who joined after the offence included. The
  `AfterValidatorRemoved` hook of `x/compliance` records each removed tombstoned key, and a target on
  such a key, or on a key x/staking holds under another operator, is `NOT_SLASHABLE` and is not
  classified. A target whose operator has no `x/validatorregistry` record is slashed but not
  classified, so one such target cannot fail the firing of the others.
- **§2.10 reconciliation.** Written into ADR-0013 as D5: firing inside the code bounds applies the
  policy; moving the bounds or forgiving a slash is an override and stays forbidden.

What the rule does not catch: coordination without equivocation, and validators no entity declares;
detection stays the ladder of ADR-0014 D4.

#### Implementation (S8, October 2026)

`chain/x/compliance` (consensus version 1 → 2, migrated by the `v0.10.0` upgrade handler), with the
membership history in `chain/x/entityregistry`; design in
the design notes.

- **Recording.** After `x/evidence` has run in a block's BeginBlock, `x/compliance` (ordered after it,
  with no reordering) reads the same misbehaviour list and writes an `Offence` for each equivocation
  `x/evidence` stored: validator, infraction height, time and power, the fraction applied, and the
  entity at that height. Only duplicate votes are linked to an entity; a light-client attack is
  recorded unlinked. A failure is logged and never returned.
- **Arming and firing.** An entity is armed with offences of two distinct validators. Queries:
  `offence`, `offences`, `case`, `armed`, `sanction`, `sanctions` and `sanction-preview` (fireable or
  not, the reason, each target's extra fraction and planned outcome, the earliest execution time and
  the unbonding deadline). Events `coordinated_offence_recorded`, `coordinated_entity_armed`,
  `coordinated_sanction_fired`, `coordinated_sanction_target`, and `compliance_flagged` with event 6.
- **Classification.** The sanction's own slash re-enters x/staking's `BeforeValidatorSlashed`; a context
  marker keeps the double-sign hook silent so the burn is recorded as event 6 and not as a second
  double-sign. `MsgSubmitEvidence` refuses event 6. The slash runs inside `x/stakeengine`'s slash window
  because it executes in x/gov's EndBlock, where slashing a redelegation into a locked-up operator would
  otherwise fail the proposal.
- **Invariant V-15** (checked at every firing and preview, never halts): offence, index, sanction and
  removed-key records agree with each other and with `x/evidence` and `x/slashing`; a break refuses
  the firing.
- **Measured.** Recording one offence 26,857 gas (one that does not arm); the firing handler 226,232
  gas with 2 targets and 321,616 with 3; `SanctionPreview` 39,766; a `MsgSubmitProposal` carrying a
  firing with 2 hashes 173,848. Every burn in the application tests is computed from x/staking's own arithmetic on the state
  just before the slash and compared with the change in the `nscn` supply.
- **Evidence window.** An offence arms only if `x/evidence` accepts it, and it refuses evidence older than
  the consensus evidence window. The live DevNet's window (100,000 blocks / 48 h, about 5.8 days) is
  shorter than unbonding (21 days). The rule's dependency on it is met by the `v0.10.0` upgrade itself
  (owner 2026-10-29, DEVELOPMENT.md §4 item 7a): `applyEvidenceWindow` sets `max_age_duration` to the
  staking unbonding time U and `max_age_num_blocks` to floor(U / 6 s), 302,400 blocks for 21 days, so the
  window is exactly U while the average block interval over a U-long span, halts included, is at most 6 s
  (measured 5.01 s; about 3.465 days of cumulative halt are absorbed, a longer one keeps evidence alive past U
  by the excess); the genesis templates carry the same rule. The window must never exceed U: the removed-key
  record of this sanction is exact only while no evidence can be accepted after the old incarnation was
  removed. A removal comes no earlier than U after the unbond started in EndBlock H, and the validator can
  still sign at H+1, so evidence of an infraction at H+1 can stay admissible after the removal for at most
  the interval between blocks H and H+1: about one block, unbounded only if the chain halted between them.
  Raising either limit beyond U, or lowering the staking `unbonding_time` by governance, widens that
  residual into the re-created-key gap, in `x/evidence` upstream and in this record. The rule cannot arm
  on a one-validator chain.

### 4. The V-8 revision — ✅ **ACCEPTED and IMPLEMENTED** (owner sign-off 2026-07-28)

**Signed off by the owner on 2026-07-28 and shipped.** `x/participation.ZeroReputation` is the single punishment write path, called from the double-sign hook; `x/compliance` cannot reach `SetEP` because its `ParticipationKeeper` interface does not expose it. Reputation zeroing is best-effort — the stake burn, C=0 and the jail have already landed when it runs, and this executes inside x/staking's slash path where a returned error would halt the chain. The original framing follows.

~~No project decision exists for this part.~~ The Register implies a change (double-sign row:
"WARP/PS → 0") but never states one, and §1.3 only flags V-8 as "slated for revision by ADR-0010"
(DEVELOPMENT.md:55) — the shape of the revision below is this ADR's engineering judgement and needs
sign-off exactly as §3 does. The recommendation: **narrow it, keep the number, do not touch EP.**

**V-8 is narrowed, not replaced.** The number is referenced from `ep_v8_test.go`, from
DEVELOPMENT.md §1.3, and from ADR-0007's "invariants V-1…V-9" list; renumbering churns those for no
correctness gain, and the testable core — the EP write-monopoly — is unchanged.

**A double-sign zeroes PS and WARP. It does not zero EP.** The argument:

- EP is the long-run accumulator and the reward gate (V-2). Zeroing it adds **no marginal deterrence**
  for a double-signer, because C=0 + `STATUS_JAILED` + a permanent tombstone already make the
  validator permanently reward-ineligible: the gate needs `ACTIVE ∧ C=1 ∧ EP>0`, and **every** route
  back to ACTIVE is closed to it — `MsgUnjail` refuses a tombstoned validator forever
  (`msg_unjail.go:68`), `MsgClearFlag` refuses a JAILED one (`msg_evidence.go:56–62`), and the bond
  hook skips JAILED (`activation.go:73–80`). This enumeration is load-bearing for the whole bullet:
  one uncovered route and EP-zeroing regains its deterrence argument (see Consequences).
- Zeroing EP **destroys the audit record**. EP is the only long-run participation quantity we keep;
  a punishment that rewrites it makes "how much did this validator actually participate?"
  unanswerable after the fact and makes the participation ledger non-monotonic.
- It is heavier than it looks. `decayEP` (`chain/x/participation/keeper/epoch.go:28–39`) refuses to
  take a positive EP below a floor of 1 — the participation layer's own design says EP never reaches
  zero once earned. Punishment would become the single code path violating that floor.
- The obvious counter-argument does not hold: EP is keyed by operator address, so an operator
  re-entering at a fresh valoper address starts at EP = 0 regardless. EP-zeroing buys nothing against
  Sybil re-entry — that is B3's job (beneficial-owner attestation before activation), not slashing's.

**PS zeroing is scoped, because PS is not a mutable live value.** PS snapshots are per-epoch immutable
records written once by `ComputeEpoch` (`epoch.go:130`). "PS → 0" therefore means: the **in-flight
epoch's** snapshot for the punished validator is forced to 0 (or, equivalently, suppressed). Closed
epochs are never rewritten. From the following epoch on, V-7 already guarantees no snapshot exists for
a non-ACTIVE validator, so PS is zero by absence. **WARP → 0 is the load-bearing half**, because WARP
is persistent smoothed state that would otherwise recover at 0.10/epoch.

Honest note on the value of the WARP→0 clause for double-sign specifically: given a permanent
tombstone, the validator can never return, so WARP→0 is largely symbolic in that case. It becomes
load-bearing for any future critical violation that does *not* tombstone, and for a coordinated-attack
rule if that rule ever admits a recoverable outcome. The Register did not distinguish these cases.

**Exact revised invariant, in §1.3 style:**

| ID | Statement |
|---|---|
| V-8 | EP changes only via participation (`ComputeEpoch`); punishment never mutates EP. Punishment MAY zero WARP and the in-flight epoch's PS snapshot — never EP, never a closed epoch's snapshot |

Enforcement stays **by construction plus test**, and this ADR does not pretend otherwise. Once a
punishment write path exists it must be a **single named participation keeper method** (e.g.
`ZeroReputation`) called only from `x/compliance`, structurally unable to reach `SetEP`, with
`ep_v8_test.go` extended to cover the JAILED / double-sign path in addition to the QUARANTINED path it
covers today.

---

## Consequences

- *(2026-07-28; resolved — both rows now follow the Register, as the note in Context says.)*
  **The chain currently contradicts the Register on both live rows, and the ADR does not change that.**
  In force: `slash_fraction_downtime = 0.01`, `slash_fraction_double_sign = 0.05`
  (`cosmos-sdk@v0.53.6/x/slashing/types/params.go:18–19`; no override in `chain/config.yml` or
  `chain/docker/entrypoint-singlenode.sh`). Closing this is the B12 work item and needs the 🔒
  approval gate at DEVELOPMENT.md:381 plus a DevNet genesis restart (DEVELOPMENT.md:330).

- **The permanence argument rests on per-handler gates, and one of them was missing on `main` until
  2026-07-21.** §2 and §4 both lean on "a tombstoned validator can never be ACTIVE again". That was
  false in shipped code until commit `16aaeac`: `MsgClearFlag` shared `MsgUnjail`'s gov-authority
  gate but carried neither its cooldown nor its tombstone check, and since the V-1 table permits
  JAILED → ACTIVE generically and `ChangeStatus` is purely table-driven, a proposal could reinstate
  even a tombstoned double-signer simply by calling `ClearFlag` instead. It was reproduced before
  being fixed (`TestClearFlag_RejectsJailedValidator` failed on all three subtests, including the
  tombstoned one), so this was a real bypass, not a theoretical one. The lesson for this ADR: the
  gate is **per-handler**, not structural — V-1 will not catch the next omission. Any future message
  that restores compliance or status (a coordinated-attack appeal, an entity-registry
  reinstatement, the soft-compliance auto-restore of §2.7) must carry the same tombstone and
  cooldown checks, or §4's "EP-zeroing adds no marginal deterrence" quietly stops being true.

- **`DoubleSignFractionThreshold = 0.025` loses its justification and must be revisited.**
  `chain/x/compliance/types/keys.go:34` sets it, and its own comment (`keys.go:29–33`) justifies it as
  the midpoint between the SDK's 0.01 downtime and 0.05 double-sign. With downtime at 0 that band is
  empty, and with double-sign at 0.10 the known miss described at
  `chain/x/compliance/keeper/hooks.go:33–38` gets worse: the hook receives the *effective* burn
  fraction (`tokensToBurn / validator.Tokens`), which diverges further from a larger configured
  fraction when bonded tokens changed between infraction and slash. **Recommended change:** once
  downtime is 0, replace the predicate at `hooks.go:44` with "any positive fraction". This is safe by
  construction — SDK staking skips the hook entirely when `tokensToBurn` is zero
  (`cosmos-sdk@v0.53.6/x/staking/keeper/slash.go:148–151`), so the hook can only ever be reached with
  a positive burn, and after the override the only configured positive-burn infraction is double-sign.
  *Done:* the predicate is "any positive burn" (`hooks.go`), and the sanction's own slash is the one
  burn the hook is told to ignore.

- **A third slash tier needs a SAiCoN-native slash path.** `x/slashing` carries exactly two fractions.
  A coordinated-attack sanction must call `stakingKeeper.SlashWithInfractionReason` from a SAiCoN
  module at a SAiCoN-computed fraction rather than forking the SDK — and it must classify itself
  explicitly, because hook inference cannot tell an attack burn from a double-sign burn.
  `ComplianceEventType` has no value for it; the next free enum number is **6**
  (`chain/proto/chain/compliance/v1/types.proto:13–23`). *Built in S8:* the sanction calls
  `SlashWithInfractionReason` from `x/compliance` and classifies itself as event 6 (§3, Implementation).

- **DEVELOPMENT.md §1.3 over-claims enforcement for V-8.** The section is headed "EndBlock assertions
  that panic (fail-loud)" (DEVELOPMENT.md:46), but only five invariants are actually registered:
  `ValidatorStatusInvariant`, `SelfStakeLockupInvariant`, `UnstakeCooldownInvariant`
  (`chain/x/validatorregistry/keeper/invariants.go:25, 74, 188`), `RewardGateInvariant`
  (`chain/x/rewards/keeper/invariants.go:28`) and `PSActiveOnlyInvariant`
  (`chain/x/participation/keeper/invariants.go:27`). **V-8 and V-4.1 have no runtime assertion** —
  V-8 has a unit test only, and `chain/x/validatorregistry/keeper/unbonding.go:66` states in a code
  comment that the V-4.1 invariant "is not enforced". Documentation debt to fix when §1.3 is updated
  for the revised V-8.

- **Downtime → 0 does not leave downtime unpunished today, but the punishment is thinner than the
  Register's row implies.** `x/slashing` jails unconditionally after the slash call
  (`cosmos-sdk@v0.53.6/x/slashing/keeper/infractions.go:148`), so with a zero fraction the jail, the
  loss of the seat and the reward loss all still happen; the SAiCoN status goes ACTIVE → UNBONDING
  (`unbonding.go:75`) and recovery is the ordinary re-bond path (ADR-0004). What does **not** exist is
  the WARP arm — the part the Register leans on. Until the WARP engine ships
  ([ADR-0009](ADR-0009-participation-score-and-warp.md) is written but proposed, and the engine is a
  Q4 build), the deterrent for chronic unavailability is jail-and-rejoin with no capital cost and no
  reputation memory.

- **Rejected sequencing risk, recorded:** shipping downtime → 0 before WARP exists is acceptable *only*
  because the jail path is real. It would not be acceptable if the config override also relaxed the
  jail (e.g. widening `signed_blocks_window` at the same time). Do not change the window and the
  fraction in the same change without measuring both.

- **Found in passing (one line, not fixed here):** `chain/x/compliance/keeper/hooks.go:51–52` returns
  an error although the function's own doc says the hook "must never error or halt the chain"; the
  outcome is currently safe only because SDK staking logs and swallows hook errors
  (`slash.go:170–172`) — the safety is the SDK's, not ours, and would silently disappear if that
  contract changed.

- **Follow-ups this ADR does not perform** (it modifies no other file): **once this ADR is
  accepted**, update DEVELOPMENT.md §1.3 row V-8 to the revised statement (§4 is proposed — §1.3
  must keep its "slated for revision" mark until then) and §6 ADR-index row 0010; mirror the
  slashing policy onto the roadmap site per the two-surface rule (DEVELOPMENT.md:410–411).

---

## Alternatives considered

- **Keep a small non-zero downtime slash (e.g. 0.1%) as a "token" cost.** Rejected: it retains every
  structural objection — irreversible, correlated with infrastructure luck, uncorrectable after a
  misclassified common-mode outage — while being too small to deter anything. If a capital cost for
  liveness is ever wanted, the honest version is a bonded-stake requirement, not a burn.

- **Keep the SDK 5% for double-sign.** Rejected by the Register (10%, DEVELOPMENT.md:162). Recorded
  here mainly to note that neither number is calibrated; the argument for 10% is that a permanent
  tombstone plus a doubled burn makes the expected cost of equivocation clearly exceed any plausible
  single-block gain, which is an assertion, not a measurement.

- **Zero EP on double-sign (extend V-8 rather than narrow it).** Rejected — see Decision §4: no
  marginal deterrence on top of the tombstone, destroys the audit record, breaches the participation
  layer's own EP floor of 1, and buys nothing against re-entry at a fresh address.

- **Replace V-8 with a split V-8a (EP write-monopoly) / V-8b (punishment-reachable state).** More
  precise, and worth revisiting if a third writer of participation state ever appears. Rejected for
  now: it renumbers a referenced invariant to express the same two clauses that fit in one row.

- **Define the coordinated-attack fraction now (e.g. 100% confiscation).** Rejected: the ADR would be
  inventing a decision the owner has not made, on a rule whose *trigger* is undefined. A confiscation
  rule with an uncalibrated trigger is strictly worse than no rule, because the failure mode is
  destroying an honest operator over a correlated client bug.

- **Let the anti-cartel detector slash directly.** Rejected — forbidden by the Register
  (DEVELOPMENT.md:171) and unsupportable at the measured detection error rates (B4).

### Implementation note (2026-10-28): the fractions are set in the binary

Until this date the two fractions lived only in the genesis templates
(`chain/docker/entrypoint-singlenode.sh`, `chain/docker/testnet/gen-genesis.sh`); a chain built
by plain `saicond init` and the in-process test harness ran the SDK's 1% / 5%. On the owner's
decision of 2026-10-28 the binary carries the policy:

- the app's default genesis sets `slash_fraction_double_sign = 0.10` and
  `slash_fraction_downtime = 0` (`app/genesis.go`, a custom x/slashing basic module supplied in
  `AppConfig`), so `saicond init`, `saicond testnet`, `App.DefaultGenesis` and the harness start
  from the policy. The simulations draw their slashing genesis at random and bypass the default;
  `app/sim_params.json` pins the same two values for them;
- the `v0.10.0` upgrade handler raises a stored `slash_fraction_double_sign` below 0.10 to 0.10 and
  sets a positive `slash_fraction_downtime` to 0 (x/compliance's hook reads any positive burn as a
  double-sign, so a chain with a positive downtime fraction would jail and tombstone validators
  for downtime), inside the handler's own write-through. A double-sign value at or above 0.10
  stays (so a value governance raised stays), and so does a downtime value of 0. The live DevNet
  already carries both values from its genesis template, and the upgrade changes nothing there;
- tests pin both: `app/slashing_policy_test.go` (the binary's default, its agreement with the two
  genesis scripts, and `sim_params.json`) and the upgrade tests in `app/upgrade_test.go`;
- governance can still move either value afterwards with `x/slashing`'s `MsgUpdateParams`; the
  upgrade runs once.

The first Consequences bullet above describes the state before this change.

---

## References

- [DEVELOPMENT.md](../DEVELOPMENT.md) §2.6 slashing table (157–163), §1.3 invariants (46–56, V-8 at 55),
  §2.10 governance bounds (195–199), §2.7 ladder + "risk score alone never slashes" (165–174),
  §3.3 stress-test failures (278–293), §3.4 measured-vs-asserted (295–316), §5 blockers B12 (378) and
  approval gates (380–383), §4 Q3 items (3) (329) and (4) devnet restart (330).
- Code: `chain/x/compliance/types/keys.go:34` · `chain/x/compliance/keeper/hooks.go:33–52` ·
  `chain/x/compliance/keeper/compliance_state.go:84` · `chain/x/compliance/keeper/msg_unjail.go:68` ·
  `chain/x/compliance/keeper/msg_evidence.go:16–34,56–62` · `chain/x/participation/keeper/scores.go:15,34` ·
  `chain/x/participation/keeper/epoch.go:28–39,130,163` · `chain/x/participation/keeper/ep_v8_test.go:21` ·
  `chain/x/validatorregistry/keeper/unbonding.go:66,75` ·
  `chain/x/validatorregistry/keeper/activation.go:73–80,106–120` ·
  `chain/x/rewards/keeper/emission.go:74–76,209–235` ·
  `chain/proto/chain/compliance/v1/types.proto:13–23` · `chain/config.yml` ·
  `chain/docker/entrypoint-singlenode.sh`.
- SDK: `cosmos-sdk@v0.53.6/x/slashing/types/params.go:12–19` (defaults in force) ·
  `x/slashing/keeper/infractions.go:118–170` (jail is unconditional) ·
  `x/staking/keeper/slash.go:148–172` (hook skipped on zero burn; hook errors logged, not propagated).
