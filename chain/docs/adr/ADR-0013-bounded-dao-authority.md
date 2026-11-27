# ADR-0013: Bounded DAO authority — what governance may tune, and what it may never reach

Status: **Proposed** (2026-08-15) — see [DEVELOPMENT.md](../DEVELOPMENT.md) §2.10. **D5 accepted and
implemented** in S8 (`v0.10.0`) with the coordinated-attack sanction (ADR-0010 §3, project decision
D-S8-2); the open problems below are not. The parameters S8 added — entity registry, entity cap,
randomness, coordinated attack — are placed in the D2 tables as `v0.10.0` enforces them.
Source: **Decision Register v1.0** (adopted 2026-07-24), §2.10; every tier assignment below
was read from the source at `44a83fe`, not from the Register's prose.
Related: [ADR-0011](ADR-0011-emission-and-single-mint-path.md) (single mint path, supply
ceiling), [ADR-0010](ADR-0010-slashing-policy.md) (slashing, tombstone),
[ADR-0012](ADR-0012-caps-and-stake-engine.md) (caps), [ADR-0014](ADR-0014-entity-registry.md) (entity registry).

## Context

The Register states the principle in two sentences: the DAO may tune participation weights,
epoch length, cap percentages and the commission range within bounds; it may not lower the
entry floor, disable the participation layer, override slashing or reset a tombstone. *"PoP is
not governance-capturable."*

That is a sentence, not a mechanism. This ADR turns it into three tiers, assigns every
parameter the chain actually has to one of them, and names where each bound is enforced —
because a bound kept by convention is not a bound. A governance system whose limits live only
in a design document has no limits: the vote that removes them is the same vote that uses them.

Two things make this urgent rather than tidy. External operators are invited in S9, which is
the first time the validator set is not the team. And R1 — a registered upgrade handler — is
what makes a tier-3 change possible at all; without it every such change needs a fresh genesis,
so the tiers below are also a statement about which emergencies are cheap and which are not.

## Decision

### D1 — Three tiers, distinguished by where the limit lives

| Tier | Meaning | Where the limit is enforced |
|---|---|---|
| **1 — free** | governance sets any value the type permits | the proto type only |
| **2 — bounded** | governance sets a value inside a range encoded in the module | `Params.Validate()`, which `MsgUpdateParams` calls before writing |
| **3 — unreachable** | not a parameter; changing it is a code change | Go constants and `EndBlock` invariants |

The distinction that matters is **tier 2 against tier 1**. A tier-2 bound rejects the proposal
when it is delivered, not when it is voted on: `msg_update_params.go` calls `Params.Validate()`
and returns an error, so an out-of-range value never reaches state even if every validator
voted for it. That is enforcement. A tier-1 parameter has none beyond the width of its integer.

### D2 — Tier assignments, as the chain stands today

**Tier 3 — unreachable by any vote.** These are Go constants or invariants, and the only way
to move them is to ship a new binary:

| What | Where |
|---|---|
| Supply ceiling, 21,000,000 | `x/rewards/types/params.go` — `MaxSupplyNscn`, a constant, checked before every mint |
| Single mint path | structural: `x/mint` is removed from the app, so no second module holds `Minter` |
| Self-stake lock-up floor, 60 days | `x/validatorregistry/types/keys.go` — `DefaultLockupDurationSeconds`; registration rejects anything shorter |
| Runtime invariants V-1…V-9 | `EndBlock` assertions that panic; a breach halts the chain rather than logging |
| Tombstone | the SDK's, and no message in this chain clears it |
| Coordinated-attack bounds (S8) | `x/compliance/types/params.go` — `CoordinatedFractionFloor() = 0.10`, `CoordinatedFractionCeiling() = 1.00`, `MinAppealWindowSeconds = 86,400`, `MaxAppealWindowSeconds = 1,209,600`; `MaxEvidenceHashesPerSanction = 16` in the same file; `types/hash.go` — the evidence hash format (64 uppercase hex) |
| Entity registry structure (S8) | `x/entityregistry/types/keys.go` — `MaxMembersPerEntity = 8`; one lottery ticket per attested entity; the activation gate itself, which no parameter turns off |
| Randomness constants (S8) | `x/randomness/types/keys.go` — the 2/3 quorum (`QuorumNumerator = 2`, `QuorumDenominator = 3`), `ShadowLotterySeats = 22`, `MaxDrawHistory = 52`; the verification gas, the suite (ECVRF-EDWARDS25519-SHA512-ELL2) and the formats of the alpha, the seed and the draw |
| Coordinated-attack arming rule (S8) | `x/compliance/keeper/offences.go` (`recordOffence`, `CaseOf`) — duplicate votes of at least two distinct validators linked to one entity, nothing else: no K-of-W path, no window, no count across entities, no input from a risk score |

Worth stating plainly, because it is the strongest fact in this document: **`x/validatorregistry`
has no `Params` fields at all.** The entry floor and the lock-up the Register says governance may
not lower are not parameters that happen to be left alone — there is no message that could reach
them. That is tier 3 by construction rather than by discipline.

**Tier 2 — bounded, and the bound is code.**

| Parameter | Bound | Enforced in |
|---|---|---|
| `x/participation.ep_decay` | `[0, 1)` | `x/participation/types/params.go` |
| `x/rewards.budget_bonded_rate` | `[0, 1]` | `x/rewards/types/params.go` |
| `x/rewards.budget_floor_rate` | `[0, 1]` | same |
| `x/rewards.budget_ceiling_rate` | `[0, 1]`, and **≥ floor** | same — an inverted clip would emit the floor forever |
| `x/compliance.coordinated_attack_fraction` (S8) | `[0.10, 1.00]` inclusive, default 0.30; and not below the chain's current `slash_fraction_double_sign` | `x/compliance/types/params.go` `Validate()` for the bounds; `MsgUpdateParams` and the firing plan for the double-sign floor (a stateful check, read from x/slashing); `MsgFireCoordinatedSanction.ValidateBasic` for the bounds again |
| `x/compliance.appeal_window_seconds` (S8) | `[86,400, 1,209,600]` (1 to 14 days), default 604,800 | `x/compliance/types/params.go` `Validate()` |
| `x/entityregistry.max_active_seats` (S8) | `[1, 3]`, default 2 | `x/entityregistry/types/params.go` `Validate()` |
| `x/stakeengine.entity_cap_fraction` (S8) | `[0.02, 0.10]`, default 0.05, and not below `validator_cap_fraction` | `x/stakeengine/types/params.go` `Validate()` |
| `x/stakeengine.entity_cap_floor` (S8) | `[validator_cap_floor, 5,250,000 SCN]`, default 0 (the genesis scripts set it to `validator_cap_floor`) | same |
| `x/randomness.rotation_epochs` (S8) | `[1, 28]`, default 7 | `x/randomness/types/params.go` `Validate()`; a change applies from the next round |
| `x/randomness.window_blocks` (S8) | `[10, 200,000]`, default 100 | same |

**Tier 1 — free within the type.** `x/entityregistry.attestors` (S8: at most 10, unique — a bound on
the list's size, not on who is on it), `x/participation.epoch_length_blocks`,
`x/rewards.blocks_per_year`, `x/rewards.enabled`, `x/compliance.unjail_cooldown_blocks`, and
the SDK's own `staking.max_validators`, `staking.unbonding_time`, the slashing fractions, the
consensus evidence window and the governance voting periods.

`unjail_cooldown_blocks` does carry a `>= 0` check, which is type sanity for a signed integer
rather than a policy bound — zero passes, and zero means no cooldown.

Three of those do not belong in tier 1, which is Open problem 1.

### D3 — A tier-2 bound is written where the parameter is, not in a policy module

Bounds live in the owning module's `Params.Validate()`. The alternative — a central governance
policy module that vets every parameter change — was rejected: it duplicates knowledge of what
each parameter means in a module that does not own it, and the duplicate is what goes stale.
The cost is that a reader must open four files to see every bound; §2.10 and this table are the
index that makes that cheap.

### D4 — Moving a parameter between tiers is itself a tier-3 change

Adding a bound, removing one, or moving a value out of `Params` is a code change and a binary
upgrade. Governance cannot widen its own authority by proposal. This is the property that makes
the word "bounded" mean anything.

### D5 — Applying a bounded sanction is not an override

DEVELOPMENT.md §2.10 says governance may not override slashing, and the coordinated-attack rule
(ADR-0010 §3, S8) has governance fire a sanction. Without a sentence that separates the two, the
rules read as contradictory. Firing a sanction **inside** the code-fixed bounds is an *application* of
slashing policy: the policy is the rule, its bounds and its parameter, all set before the case, and
the vote applies them to named evidence — a proposal that carries the evidence hashes, waits out the
appeal window and executes at a fraction equal to the governed parameter. **Changing the bounds**, or
**forgiving** a slash after the fact (a refund, a tombstone reset, an unjail of a tombstoned
validator), is an *override* and stays forbidden; no message in this chain does either. The bounds are
tier 3 (D4: moving them is a code change and a binary upgrade), and the two parameters that sit
between them are tier 2, enforced where the parameter is (D3). Tests prove it: `Validate` refuses
0.099999999999999999 and 1.000000000000000001, and real governance proposals that set the fraction
below or above its bounds, or the appeal window to one hour, fail at execution and leave the
parameters as they were.

## Open problem 1 (🚧) — three tier-1 parameters can switch off what the Register protects

The Register says governance may **not** "bypass/disable the participation layer". Two
parameters do exactly that today, both reachable by an ordinary `MsgUpdateParams`:

**`x/participation.epoch_length_blocks = 0`.** `Params.Validate()` tolerates zero deliberately
— it is documented as "epoch tick disabled" and the storage scaffolding uses it — and
`module.go` EndBlock returns early on it. No epoch ever closes, so no participation score is
ever written, no EP ever accrues, and `EpochTarget` returns zero: emission stops with it. The
handling is safe (no division by zero anywhere) but the effect is a silent, total switch-off of
the mechanism the network is named after.

**`x/rewards.enabled = false`.** An explicit kill switch, and a deliberate one (ADR-0011): it
exists so a discovered emission fault can be stopped without halting the chain. That is a good
reason for it to exist and a bad reason for it to sit in tier 1 unqualified.

**`x/compliance.unjail_cooldown_blocks = 0`.** The same shape, one layer down. The cooldown is
what stops a governed unjail from following a jailing immediately; `Validate()` rejects only
negatives, so a proposal setting it to zero passes and the gate the Register describes as a
delay becomes a formality. This one is the cheapest to fix — a minimum has no fixture
depending on it — and it is listed with the others because the pattern, not the individual
parameter, is the finding.

None of the three is a bug in the code — all behave exactly as written. The gap is that the Register
forbids by prose what the parameter surface permits by construction. **Recommendation:** move all
three to tier 2 — give `unjail_cooldown_blocks` a minimum, reject `epoch_length_blocks = 0` in
`Validate()` once the scaffold tests no longer need it, and record `enabled` as a bounded
emergency control with a stated expectation that it is re-enabled by a following proposal
rather than left off. Not decided here: the first is a one-line change with no open question,
the second changes test fixtures, and the third is a policy question about who may pull an
emergency brake and for how long.

## Open problem 2 (🚧) — the tiers say nothing about who proposes

Everything above bounds *what* a passing proposal may set. Nothing here bounds *who* may
propose, what quorum a tier-2 change needs against a tier-1 one, or whether the same threshold
should govern the emission rates and the epoch length. The SDK's `x/gov` treats them
identically. Differentiated thresholds are a real option and are out of scope for this ADR;
they belong with the governance parameters themselves, which are currently tier 1.

## Consequences

**An emergency that needs a tier-3 change needs a coordinated upgrade.** That is the intended
cost — the ceiling and the invariants should be expensive to move — but it is only survivable
if the upgrade path is rehearsed. R1 (a registered upgrade handler, slot S9) is therefore a
prerequisite for this ADR being livable rather than merely strict.

**Tier 2 is the tier that needs work.** Four parameters are bounded today. Cap percentages, the
commission range and the PS weights are all named as bounded in the Register and none of them
exists as a parameter yet — they arrive with `x/stakeengine` (ADR-0012) and the WARP engine
(ADR-0009). Each must land with its bound, not after it. *2026-11-24 (S10):* the four WARP
parameters landed with theirs, enforced in `Params.Validate` on every `MsgUpdateParams`:
`0 < warp_rise < warp_fall ≤ 1` and `0 ≤ warp_threshold ≤ warp_initial ≤ 100`. The PS weights
did not land: PS is still single-signal.

**A published tier table is an attack surface map.** Anyone can now read which values a
majority could move and which it could not. That is deliberate: a governance system whose
limits are only discoverable by reading Go is not a governance system anyone can trust.

## Alternatives considered

**A single `x/governance` policy module holding every bound.** Rejected — see D3. It centralises
a table that must track four modules it does not own.

**Bounds as governance parameters themselves.** Rejected outright: a bound a vote can widen is a
comment. This is the whole point of D4.

**No tiers; rely on the Register's prose.** This is the status quo and it is what this ADR
replaces. The prose has been correct since July and, as Open problem 1 shows, two parameters
have contradicted it the entire time without anyone noticing — which is the argument for
writing the table down against the source rather than against the design.

## References

- [DEVELOPMENT.md](../DEVELOPMENT.md) §2.10 (the Register's two sentences), §1.3 (invariants),
  §5.1 R1 (upgrade handler)
- [ADR-0011](ADR-0011-emission-and-single-mint-path.md) — supply ceiling, single mint path, the
  kill switch
- [ADR-0010](ADR-0010-slashing-policy.md) — slashing configuration and the tombstone
- Source read at `44a83fe`: `x/*/types/params.go`, `x/*/keeper/msg_update_params.go`,
  `x/rewards/types/budget.go`, `x/participation/module/module.go`
