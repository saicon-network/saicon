# SAiCoN

**A Proof-of-Participation Blockchain**

SAiCoN Blockchain Development Team

---

*This document describes the SAiCoN protocol as designed. The section
[Implementation Status](#12-implementation-status) records what runs on
the public developer network at the time of writing, and the parameter appendix marks the phase
in which each value takes effect.*

## Table of Contents

1. [Introduction](#1-introduction)
2. [Background](#2-background)
3. [Network Architecture](#3-network-architecture)
4. [Validators](#4-validators)
5. [Participation](#5-participation)
6. [Active Set Selection](#6-active-set-selection)
7. [Stake Limits and Delegation](#7-stake-limits-and-delegation)
8. [Issuance and Incentives](#8-issuance-and-incentives)
9. [Security](#9-security)
10. [Governance](#10-governance)
11. [Calibration](#11-calibration)
12. [Implementation Status](#12-implementation-status)
13. [Related Work](#13-related-work)
14. [Conclusion](#14-conclusion)
- [Appendix A: Protocol Parameters](#appendix-a-protocol-parameters)
- [Appendix B: Notation and Formulas](#appendix-b-notation-and-formulas)
- [Appendix C: Public Interfaces](#appendix-c-public-interfaces)
- [References](#references)

---

## Abstract

SAiCoN is a Layer-1 blockchain in which a validator is rewarded for measured participation
rather than for the size of its stake. Every epoch the protocol computes, for each validator,
a participation score from the blocks it signed and the proposer turns it took, smooths that
score into a reputation that falls quickly and recovers slowly, and pays only those validators
that are active, compliant and have accumulated participation. Stake retains two roles — it
admits a validator and it weights its reward — but it does not determine membership of the
active set, which is decided by reputation. Concentration is bounded by hard limits on
validators, on the real-world entities that operate them and on the network as a whole; compliance,
cartel resistance and energy accounting are enforced as separate layers rather than folded into
the score. The native token, SCN, has a fixed ceiling of 21,000,000 enforced before every mint by
a single issuing module, and emission is derived from a security budget that transaction fees
displace. The protocol is built on the Cosmos SDK and CometBFT, runs on a public developer
network, and is calibrated by a programme of executed simulations whose principal results this
paper reports.

---

## 1. Introduction

Proof-of-stake secures a network by making misbehaviour expensive: a validator posts capital,
and the protocol destroys some of it when the validator equivocates or disappears. The design
has proven sound. It also has a structural consequence that every mature proof-of-stake network
exhibits. Consensus weight is proportional to capital, rewards are proportional to consensus
weight, and rewards are reinvested — so the distribution of stake drifts toward its largest
holders. The typical outcome is a validator set in which ten operators control a third of the
voting power and the Nakamoto coefficient [7] sits in the single digits, on a network that
nominally has a hundred validators.

A second consequence is quieter. On such a network, running a validator *well* and running one
*at all* pay the same. Uptime and honesty are enforced by penalties, and a validator that avoids
the penalties earns its stake-proportional share whether it is exemplary or merely adequate. The
protocol measures capital precisely and participation only at the margin.

SAiCoN starts from a different allocation of roles. **Capital admits a validator; participation
decides its influence.** A validator posts a self-bond and locks it, which establishes that a
real operator with real exposure stands behind the node. From that point its standing in the
network is computed from what it does — the blocks it signs, the proposer turns it takes, its
conduct over time — and the active set is selected on that standing. Stake continues to weight
the reward a selected validator receives, so capital is not ignored; it is confined to the roles
in which it is informative.

Three commitments follow from this and shape the rest of the design.

**Participation is measured, not asserted.** A validator's score is computed in consensus from
data every node can verify: commit signatures and proposer identities in the block headers.
Nothing in the score depends on self-report.

**Every control lives in its own layer.** Compliance is a gate: a validator that fails it is
excluded, whatever else it signals. Decentralization is a property of the network rather than
of any participant, so it is enforced as limits rather than turned into points. Energy
accounting is informational until a verifier exists that cannot be paid to lie. Folding any of
these into the participation score would let a validator buy its way past them with uptime.

**Parameters come from experiments.** Every numerical parameter in this paper was selected or
confirmed by a simulation whose code and outputs are published, and the calibration section
states what those simulations measured and what they did not.

The paper proceeds as follows. Section 2 summarises the consensus stack SAiCoN is built on.
Section 3 describes the network architecture. Sections 4 through 7 specify the validator
lifecycle, the participation mechanism, active-set selection and the stake limits. Section 8
specifies issuance and incentives. Section 9 covers slashing, compliance, cartel resistance and
the protocol's runtime invariants; Section 10, governance. Section 11 reports the calibration
programme, Section 12 the implementation status and roadmap, and Section 13 related work.

---

## 2. Background

SAiCoN is an application-specific blockchain built with the Cosmos SDK [8] on the CometBFT
consensus engine [4, 9]. The choice is deliberate and worth stating.

CometBFT — the successor to Tendermint Core — provides Byzantine fault-tolerant consensus with
instant finality: a block committed by more than two thirds of the voting power is final, and
the protocol produces cryptographic evidence of equivocation that the application can act on.
The Cosmos SDK provides the application layer as a set of composable modules — staking,
slashing, distribution, governance, bank, upgrades — that have secured a large family of
production networks. Inter-Blockchain Communication (IBC) [10] provides trust-minimised
transfer between chains that share this stack.

SAiCoN keeps the consensus engine and the standard modules unchanged. Its contribution is a set
of four application modules that sit beside them and, at the boundary where the SDK hands the
validator set to the consensus engine, replace stake-weighted selection with participation-based
selection. This placement matters: the participation layer is not a modification of consensus,
which remains a well-studied BFT protocol, but an application-level policy over which validators
enter consensus and with what weight.

Two properties of the stack are used directly. First, every block header carries the identity
of its proposer and the previous block's commit, so participation can be computed from data that
is already part of consensus and needs no oracle. Second, the SDK exposes hooks at the beginning
and end of every block, which is where SAiCoN's epoch tick, its participation accounting and its
runtime invariants execute.

---

## 3. Network Architecture

### 3.1 Consensus and Block Production

Blocks are produced by CometBFT at a target interval of approximately five seconds. The active
set — the validators whose signatures count toward the two-thirds threshold — is selected by
the protocol described in Section 6 and handed to the consensus engine as a validator update at
the end of each rotation. Within a rotation, block proposers are chosen by CometBFT's own
proposer selection, weighted by voting power.

Time in the protocol is measured in **epochs**, defined as a fixed number of blocks. The choice
of block count over wall-clock time is deliberate: every node observes the same block height,
whereas clocks drift, and a parameter expressed in blocks is deterministic across the network.

### 3.2 Application Modules

Four modules implement the participation protocol. They are listed here and specified in the
sections that follow.

| Module | Responsibility |
|---|---|
| Validator Registry | validator identity, the status machine, admission, the self-bond lock-up, exit |
| Compliance | the compliance flag, evidence handling, jail and governed restoration |
| Participation | the per-epoch participation score, reputation smoothing, the accumulator that gates rewards |
| Rewards | the single issuing module: emission, the supply ceiling, fee displacement, distribution |

Two further modules complete the design: a **stake engine** that enforces the admission floor
and the stake limits of Section 7, and a **delegation router** that places delegations and
routes overflow. An **entity registry** records the beneficial owner behind each validator and
enforces the entity-level limits.

The SDK's own inflation module is not present in the application. Section 8 explains why.

### 3.3 Node Topology

A SAiCoN network distinguishes node roles along two independent axes — what a node *retains*
and what it *exposes*.

| Role | Retains | Exposes |
|---|---|---|
| Validator | consensus state and evidence window | nothing — connects only to its own sentries |
| Sentry | every block, pruned state | peer-to-peer, facing the internet |
| Full node | every block, pruned state | RPC and REST, rate-limited |
| Archive | every block and every historical state | restricted query access |

A validator never faces the open internet. It runs with peer exchange disabled and persistent
connections only to sentries it operates; each sentry lists the validator as a private peer, so
its address is never gossiped. Two sentries in different regions and at different providers are
the minimum. Application traffic — wallets, explorers, indexers — is served by full and archive
nodes, never by validators.

### 3.4 Public Interfaces

The network exposes the standard Cosmos REST and RPC interfaces — blocks, transactions,
validators, delegations, supply and staking parameters — together with query endpoints for each
SAiCoN module: the validator registry, participation records by epoch, reward records by epoch
and compliance records. Any wallet that implements the Cosmos chain-suggestion flow connects
without modification; SAiCoN uses the standard Cosmos derivation path. Appendix C lists the
endpoints.

---

## 4. Validators

### 4.1 States

A validator is always in exactly one of six states, and transitions between them are constrained
to a fixed table enforced by a runtime invariant.

```
BONDING ──► ACTIVE ──► QUARANTINED ──► UNBONDING ──► INACTIVE
                └─────► JAILED ───────┘
```

| State | Meaning |
|---|---|
| Bonding | registered; the self-bond lock-up has started; awaiting activation |
| Active | eligible for selection; signing when selected |
| Quarantined | isolated from consensus pending a compliance review, with an appeal open |
| Jailed | excluded following a severe violation |
| Unbonding | exit requested; the unbonding period is running |
| Inactive | fully exited |

### 4.2 Admission

A validator is admitted on the strength of its operator's **own stake**. The self-bond floor is
2,000 SCN. Delegated stake never counts toward admission: if it did, one pool of tokens could be
recycled as delegations across many candidate nodes and manufacture admission for an entire
group at the cost of a single stake. The self-bond is capital the operator cannot lend to itself.

The self-bond is **locked for 60 days** from registration. The lock is enforced at transaction
admission: any attempt by the operator to unbond or redelegate its own stake inside the window
is rejected before execution, including attempts routed through authorization grants. The lock-up
floor is a protocol constant; registration rejects a shorter period and no governance action can
lower it.

### 4.3 Exit

Leaving the network takes the chain's unbonding period of **21 days**. The completion time is
fixed when the exit begins and cannot subsequently move, and a runtime invariant forbids a
validator from becoming inactive before it has elapsed. A partial unbond of self-stake outside
the lock-up window releases only the stated amount; the remainder continues to participate.

### 4.4 Jail and Restoration

A double-sign is detected by the consensus engine's evidence mechanism. The protocol responds
automatically: the validator is slashed (Section 9.1), its compliance flag is cleared, it moves
to *Jailed*, and its consensus key receives a permanent tombstone that no message in the protocol
can remove. Restoration from jail is a **governance action**, subject to a cooldown, and a
compliance clearance never bypasses the restoration gate.

---

## 5. Participation

### 5.1 Participation Score

At the close of every epoch, each active validator receives a raw score on a 0–100 scale:

```
PS = 0.50 · Uptime + 0.35 · BlockQuality + 0.15 · Governance
```

*Uptime* is the fraction of blocks in the epoch that carried the validator's signature in the
commit. *BlockQuality* is the fraction of the validator's expected proposer turns that it took,
where the expectation is accumulated block by block from the validator's share of voting power
so that changes to the set within an epoch are handled exactly. *Governance* is the validator's
participation in on-chain votes over a trailing window, with epochs in which no proposal was
open scoring full marks; it enters the score once the protocol records votes at the time they
are cast, and until then its weight is renormalised across the two measurable signals.

The participation score measures participation quality and nothing else. Compliance, cartel
signals and energy accounting are not inputs to it.

### 5.2 Reputation

The raw score is smoothed into **WARP**, the validator's reputation, by an asymmetric filter:

```
if PS <  WARP :  WARP ← WARP − 0.65 · (WARP − PS)
if PS ≥  WARP :  WARP ← WARP + 0.10 · (PS − WARP)
```

Reputation falls at a rate of 0.65 and recovers at 0.10. The asymmetry is the central decision
of the participation layer: it makes reputation cheap to lose and expensive to regain, so that
the rational strategy for an operator is continuous participation rather than periodic
minimum-effort recovery. A new validator begins at a reputation of 80. A validator is
**eligible** for selection when its reputation is at least 65.

The cost of the asymmetry is concrete. A validator that misses three consecutive epochs falls
from any starting reputation to below 5, and then needs ten epochs of perfect participation to
cross the eligibility threshold again.

Two policies protect the score from punishing what a validator did not cause. A **confirmed
network-wide outage freezes reputation** for every validator — no update in either direction —
because penalising all validators for an event none of them caused is a false positive the
protocol must not produce. And the first epoch for which a validator's telemetry is missing is
treated as absent evidence rather than adverse evidence; continued silence scores zero, so that
silence is never a cheaper strategy than reporting.

### 5.3 Empirical Points and the Reward Gate

Alongside reputation, each validator accumulates **Empirical Points**: one per active epoch,
optionally decaying toward a floor of one. Empirical Points are the long-run record of
participation and they are the **reward gate**. The protocol pays a validator in an epoch only if

```
status = ACTIVE   ∧   compliant   ∧   EP > 0
```

and a runtime invariant asserts that a validator with no Empirical Points receives nothing.
Empirical Points change only through the participation module's epoch computation. A penalty
may zero a validator's reputation and the in-flight epoch's score; it can never alter Empirical
Points or a closed epoch's record, and the compliance module's interface to the participation
module structurally cannot reach them.

### 5.4 Attacks

**Selective participation.** An operator that signs only enough blocks to stay eligible, and
saves cost by missing the rest, is the profile the asymmetric filter was calibrated against.
Under the selected parameters the simulated selective profile is admitted in 0% of epochs,
against 43.8% under a symmetric filter (Section 11).

**Reputation farming.** An operator cannot bank reputation and spend it: the filter's slow
recovery caps the rate at which surplus reputation accrues, and the fast fall spends it in a
single epoch of absence.

**Outage exploitation.** A validator cannot be pushed below the threshold by another validator's
failure, since the score depends only on its own signatures and turns; and it cannot exploit a
network-wide outage, since reputation is frozen for everyone during one.

---

## 6. Active Set Selection

### 6.1 Eligibility

A validator is eligible for selection when it is active, compliant and its reputation meets the
threshold:

```
pool = { v : status(v) = ACTIVE ∧ C(v) = 1 ∧ WARP(v) ≥ 65 }
```

Entity limits (Section 7.2) are applied to the pool **before** selection. A draw that had to be
corrected afterwards would not be a draw.

### 6.2 The Active Set

The active set is designed for **75 seats**, filled in two stages and redrawn every **7 epochs**.
The set size is a governed staking parameter — changing it requires neither a restart nor a
migration — and the final value is fixed by the consensus benchmark of Section 6.5. The
developer network, whose genesis predates the decision, still carries the framework default
and will be moved by an ordinary parameter change.

**Seventy percent of the seats — 53 at 75, 42 at 60 — are deterministic** and go to the eligible
validators with the highest reputation.
The ranking key is reputation alone — not voting power, and not stake. The reason is
arithmetic. Inside the eligible pool, reputation spans at most 1.54× (from 65 to 100), whereas
admitted stake spans roughly 8.7× (from the admission floor to the per-validator cap); a ranking
that included stake would be a stake ordering with a reputation tint, and simulation confirms it
— a multiplicative stake term reproduces pure stake sort with 98.6% overlap (Section 11).

**The remaining thirty percent — 22 at 75, 18 at 60 — are lottery seats**, drawn from the
remaining eligible validators without replacement, with ticket weight superlinear in quality, so that a lottery seat is earned rather
than raffled — and with **one ticket per verified entity**, so that the number of identities an
operator controls does not change its odds.

The lottery exists to keep the set open. A purely deterministic ranking would converge on the
same operators; the lottery admits well-performing validators from outside the top ranks every
rotation and keeps the cost of a permanent seat from becoming a barrier to entry.

### 6.3 Randomness

The lottery is seeded by a **verifiable random function** [15] evaluated by validators over the
rotation's inputs. The alternatives were considered and rejected: block-hash randomness is
grindable, because the proposer of a height is known in advance and controls transaction
inclusion; commit–reveal is unbiased but costs on the order of two hundred transactions per
rotation at the expected candidate count. A VRF output is a deterministic function of the key
and the input, so its holder cannot produce an alternative for the same input, and it is
verifiable by every node. VRF keys are registered in the entity attestation ceremony
(Section 9.3).

### 6.4 Voting Power

Each selected validator's voting power is

```
VP = min(bonded, validatorCap) × WARP / 100 × C
```

normalised across the set and written to the consensus engine as its validator update. Voting
power weights both consensus and reward. There is no concentration term inside it: concentration
is handled by the limits of Section 7, so a validator's power never moves because someone else
bonded or unbonded.

### 6.5 Set Size

Decision Register v1.0 set 60 seats, chosen as the point at which the decentralization benefit of a
larger set was judged to outweigh a measured decline in mean participation quality. The review
round that followed preferred **75**: it lowers the worst-case top-ten share of voting power from
about 38% to 34% and raises the Nakamoto coefficient from roughly 9 to 10, at a cost of under one
point of mean participation quality, while 90 buys almost nothing further and keeps degrading
quality (Section 11). Seventy-five is therefore the design target. The final size is fixed by a
consensus benchmark on the public testnet — block time, finality, resource use and churn recovery
measured at 50, 60, 75 and 90 seats — before MainNet.

### 6.6 Attacks

**Stake capture of the ranking.** Excluded by construction: stake is not an input to the
deterministic ranking.

**Grinding the lottery.** Excluded by the VRF.

**Identity splitting.** An operator that divides its stake across many validators to collect
lottery tickets or deterministic seats is bounded by the entity limits: two seats and one ticket
per verified entity, regardless of node count. The limit depends on knowing who owns what, which
is the subject of Section 9.3.

**Hidden coordination.** A set of technically healthy validators that act as one cannot be
distinguished by any participation score, and no ranking function resolves it. The defence is
attestation before activation and the detection ladder of Section 9.3.

---

## 7. Stake Limits and Delegation

Concentration is controlled by limits that truncate, not by penalties that discount. The
distinction is not stylistic. A penalty lowers the marginal return on concentration without
bounding it; a cap bounds it. Caps were measured in simulation; no penalty curve was.

### 7.1 Per-Validator Cap

No validator's effective stake may exceed **2% of total bonded stake**. The base is read once
per epoch and ratchets: it may rise freely between epochs but may fall only by a governed
percentage per epoch, and a position pushed over the cap by a falling base is frozen rather
than force-shed. Stake above the cap remains bonded and slashable but earns no voting power and
no reward, and new delegations to a validator at its cap are routed elsewhere.

### 7.2 Entity Limits

The unit of concentration control is the **entity** — the real-world operator behind one or
more validators. Per verified entity:

- aggregate stake across all its validators is limited to **5%** of total bonded stake;
- at most **2** of its validators may hold active-set seats;
- it holds **1** lottery ticket.

The seat limit is the lever. In simulation, a modelled attacker with no seat limit reached 53.8%
of voting power with certainty of exceeding one third; with two seats per entity, its maximum
was 3.2% and the probability of exceeding one third was zero. Varying the stake limit between
5% and no limit moved the attacker's power only between 3.2% and 4.4%.

### 7.3 Global Bonded Cap and Queue

Total bonded stake is limited to **the lesser of 8% of the supply ceiling and 60% of public
circulating supply**. At the cap, net new stake enters a **queue**; redelegation and the
replacement of exited validators remain allowed, so the cap constrains the total without
freezing its composition. The cap bounds the share of the token that can be locked in consensus
at once, and with it the share of the economy exposed to slashing.

### 7.4 Delegation

A delegator chooses a primary validator and may split across at most **3**. Delegations that
would push a validator past its cap **overflow**, and overflow is routed by the delegation router
among eligible validators with remaining capacity, weighting quality, expected activity and the
inverse of current stake, and excluding validators that are jailed, quarantined, below the
reputation threshold or at an entity limit. The router is decentralization-aware by design: it
sends overflow away from concentration rather than toward the highest-yielding validator.

Delegated stake is exposed to slashing on the same terms as self-bond.

### 7.5 Attacks

**Delegation recycling.** Bounded by the admission rule of Section 4.2: delegations do not admit.

**Many small nodes.** Bounded by the entity limits: however many nodes an entity runs, it holds
two seats, one ticket and 5% of stake.

**Stake shock.** A large delegation exit lowers the cap base; the ratchet limits the fall per
epoch and freezes over-cap positions, so the exit cannot be used to force other validators to
shed stake in a single epoch.

---

## 8. Issuance and Incentives

### 8.1 The SCN Token

The native token of SAiCoN is **SCN**. It is the only staking token: a validator's self-bond and
a delegator's stake are denominated in it, rewards are paid in it, and transaction fees are
collected in it. The base unit — `nscn`, one hundred-millionth of a token — and the eight-decimal
precision are the same on the test network and on MainNet, and every parameter in this paper
carries over unchanged.

### 8.2 Supply and the Single Mint Path

The supply is capped at **21,000,000 SCN**. The ceiling is a protocol constant, not a
parameter, and it is enforced as a **precondition** of every mint: the issuing module computes
the headroom below the ceiling and clamps issuance to it, so the ceiling prevents a breach
rather than detecting one.

There is exactly **one module that can mint**. The SDK's inflation module is removed from the
application entirely — not disabled by configuration but absent — so that no second code path
holds the permission to create supply, and a wiring test asserts that none does. The epoch's
reward pool is minted **once per epoch**, not per validator, so the total issued is a single
computed quantity rather than the emergent sum of a loop.

An issuance kill switch, controllable by governance, halts emission and records a reason against
every validator that would otherwise have been paid.

### 8.3 The Security Budget

Emission is not a fixed inflation rate. It is derived each epoch from an **annual security
budget** — the amount the network commits to paying its validators — defined as

```
AnnualSecurityBudget = clip( 0.15 × TotalBonded,  0.005 × Supply,  0.0075 × Supply )
```

that is, fifteen percent of bonded stake per year, bounded below by half a percent and above by
three quarters of a percent of current supply. The epoch's target is the annual budget divided
by the number of epochs in a year. At the current network snapshot this is approximately
238 SCN per day at median bonding and 357 at full participation, a gross bonded yield of about
15%. A flat emission of 1,100 per day was evaluated and rejected: it implied a yield of 46–70%
and exhausted the ceiling within nine years.

### 8.4 Transaction Fees

Transaction fees **displace issuance**. The epoch's fresh emission is the security-budget target
less the fees already collected:

```
Emission   = max(0, EpochTarget − GasFees − CarriedRemainder)
RewardPool = Emission + GasFees
```

An epoch therefore always pays its target and never more. As fee revenue grows, new issuance
falls, and at roughly 100,000 SCN of annual fees minting stops entirely and validators are paid
from fees alone. The rounding remainder of an epoch's distribution is carried forward in base
units and offsets the next epoch's issuance; it is never paid to any party as dust. The
distribution satisfies the conservation identity

```
minted + carried_in = paid_out + carried_out
```

which the protocol's tests assert directly.

### 8.5 Distribution

The reward pool is distributed among the validators of the active set that pass the reward gate
of Section 5.3, each validator's share proportional to its voting power. Because voting power
embeds reputation, the distribution rewards participation continuously rather than in tiers: a
validator's income rises with its reputation and with its capped stake, and no threshold creates
a cliff. Within a validator, the reward is divided between the operator's self-bond and its
delegators in proportion to effective stake, less the operator's commission.

### 8.6 Commission

An operator sets a commission rate on its delegators' rewards, subject to a protocol minimum and
a governed range, with a bounded rate of change. The minimum exists to prevent a race to zero
fees that would leave operators unable to cover the cost of running a node. The range and the
default are calibrated on testnet data (Section 11).

### 8.7 Long-Term Security

At zero fee revenue the ceiling is reached in roughly the network's twenty-sixth year, after which
the security budget is funded by fees alone. The protocol's fee-displacement rule is designed for
this transition: as usage grows, the share of the budget paid from fees grows with it, and the
network's security spend becomes a function of its economic activity rather than of a
depleting reserve. The post-ceiling funding rule — fees alone, a reserve accumulated in advance,
or a bounded terminal issuance — is fixed before MainNet.

### 8.8 Transition from the Existing Base Token

The project's community holds an earlier token on the Base network today. The two environments
run in parallel while the chain proves itself: the Base token remains the economic token there,
SCN is the chain's own unit, and no balance moves between them. That parallel period is the
one being built now, and during it there is no migration.

The transition out of it is a single approved event rather than a drift. When the chain is judged
to be past its launch risk — which means after the external security audit, the public testnet
and MainNet genesis — the exchange into SCN is opened, circulation of the Base token is
closed, and the remaining Base-side supply is burned. From that point SCN is the only token, and
the two-token period is over.

**The exchange is funded from tokens set aside for it, never from new issuance.** This follows
from the supply design rather than from policy: the ceiling is a constant checked before every
mint, and only one module can mint at all. A migration that issued against burned Base tokens would be a
second issuing path, and the protocol does not have one. The reserve is therefore fixed at
genesis — before the transition it funds, not at the moment of it.

Because the transition follows the audit rather than preceding it, balances that become slashable
stake land on a network whose slashing has been independently reviewed.

---

## 9. Security

### 9.1 Slashing

| Event | Consequence |
|---|---|
| Ordinary downtime | no stake is slashed. The validator loses reputation, loses reward for the epoch, and may be jailed. A confirmed network-wide outage exempts every validator. |
| Double-sign | **10%** of bonded stake — self-bond and delegations proportionally — the compliance flag cleared, jail, and a permanent tombstone on the consensus key. |
| Proven coordinated attack | a separate, heavier, evidence-based rule, decided by governance on published evidence. |

Downtime is deliberately not a stake penalty. Reputation already carries its cost — an outage
costs ten epochs of recovery — and a stake penalty for downtime punishes the honest operator
with a hardware failure exactly as it punishes the negligent one.

### 9.2 Compliance

Every validator carries a **compliance flag**, maintained by the compliance module and read
directly by the reward gate. Evidence submitted against a validator, or a double-sign, clears the
flag; clearance is restored by a governed process, never by the validator itself. The flag is
binary. Compliance is a gate, not a score: placed inside the participation score at any weight,
a validator that failed it could recover the loss through uptime and remain in the set.

### 9.3 Cartel Resistance and Entity Attestation

Every concentration limit in Section 7 is expressed per entity, and an entity limit is only as
good as the protocol's knowledge of who owns what. SAiCoN therefore requires **beneficial-owner
attestation before first activation**: a validator is linked to a verified entity before it can
be selected, and its VRF key is registered in the same ceremony. Attestation is the first line
of defence, and it is the reason entity limits are enforceable rather than nominal.

Detection is the second line. A risk score computed from behavioural correlation between
validators feeds a ladder:

| Signal | Action |
|---|---|
| risk ≥ 0.60 for two epochs | warning; monitoring only |
| risk ≥ 0.75 for two epochs and at least two independent classes of evidence | quarantine review — within 72 hours, with the evidence hash published, an appeal path and a 14-day review commitment |

A risk score **alone never slashes**. Quarantine removes a validator from consensus pending
review; it does not touch stake, and it is reversible on appeal.

The limit of detection is stated plainly, because the design depends on knowing it. In
simulation, a group of validators that coordinate while remaining technically healthy evades
quarantine roughly nine times in ten over twelve weeks. This is why attestation precedes
activation rather than following detection, and why the anti-cartel design is built on the
entity limits rather than on the detector.

### 9.4 Energy Accounting

SAiCoN records validators' energy sourcing and publishes it. It does not, in the present design,
attach any economic consequence to it — no gate, no voting-power adjustment, no reward
multiplier. The reason is measured. Across six candidate policies, every variant that paid for
green attestation created an incentive to purchase the attestation rather than the energy —
a pay-to-play exposure of 32.4% of validators in simulation — and every variant that gated on it
excluded, in the worst region, 90.6% of validators who could not obtain it. The informational
policy was the only one with no pay-to-play exposure and no regional harm. An economic pilot,
limited to a two-percent exemption-aware adjustment, is conditional on a verifier with measured
false-positive and false-negative rates below two percent and regional coverage.

### 9.5 Runtime Invariants

The protocol asserts its own consistency at the end of every block. The assertions are not
logged — they **halt the chain**, on the principle that a network whose accounting has silently
diverged is worse than a network that has stopped.

| Invariant | Statement |
|---|---|
| V-1 | every validator holds a valid status, and every transition is in the legal table |
| V-2 | a validator with no Empirical Points receives no reward |
| V-3 | no self-stake leaves during the self-bond lock-up |
| V-5 | no validator becomes inactive before its unbonding period has elapsed |
| V-7 | participation records exist only for active validators |
| V-8 | Empirical Points change only through participation; a penalty never alters them |

Beyond the invariants, the protocol's tests assert the absence of a second mint path, exact
conservation of every epoch's pool, the ceiling as a mint precondition, the kill switch, the
lock-up's resistance to authorization-grant routing, and the export–import round trip of every
module's state. The SDK's standard simulation suite — full application, state determinism,
import/export and simulation after import — runs against the application with a pinned seed.

---

## 10. Governance

SAiCoN's governance is **bounded**. Token holders vote through the SDK's governance module on
parameter changes, upgrades and the restoration of jailed validators. What a vote can reach is
limited by where each parameter's bound is enforced.

| Tier | Meaning | Enforced by |
|---|---|---|
| Free | governance may set any value of the type | the type alone |
| Bounded | governance may set a value within a range the module encodes | validation at delivery: an out-of-range value is rejected when the proposal executes, not when it is voted |
| Unreachable | not a parameter; a code change | protocol constants and runtime invariants |

The supply ceiling, the single mint path, the self-bond lock-up floor, the runtime invariants
and the tombstone are unreachable: no vote can move them. The validator registry carries no
governable parameters at all. Governance may tune the participation-score weights, the reputation
filter, the epoch length, the cap percentages and the commission range, within bounds, and
every such change is versioned. It may not lower the admission floor, bypass or disable the
participation layer, override a slashing outcome, or clear a tombstone. The participation
protocol is not capturable by a vote.

---

## 11. Calibration

Every numerical parameter in this paper was selected or confirmed by simulation, and the
simulations are published with their code, inputs and raw outputs [13, 14]. Two rules govern
what is reported here. A simulation counts only if it executed. And each result is stated with
what it does not show.

### 11.1 The Calibration Programme

A seven-stage programme selected the parameters of the Parameter Decision Register [11]:

**Reputation dynamics.** Of 384 combinations of filter weights and thresholds, 44 met the
admission criteria and one was Pareto-optimal: a fall rate of 0.65 and a recovery rate of 0.10.
Against a symmetric 0.8/0.2 filter, it reduced false acceptance of the selective-participation
profile from 43.8% to 0% and of the weak profile from 2.67% to 0%, and reduced reputation
volatility from 0.359 to 0.189, at the cost of a longer recovery from a three-day outage — ten
epochs against three.

**Active set and voting power.** Across 300 configurations, mean active reputation falls
monotonically with set size — 94.2 at 50 seats, 93.4 at 60, 91.9 at 75, 90.2 at 90. Sixty was
chosen at that stage as a compromise between quality and decentralization; the review round
revisited it. A stake-times-reputation voting
power model bounded the worst-case top-ten share at 33.5%.

**Delegation and concentration.** A 2% per-validator cap held the top-ten share at 22% against
28% at 3%, at the cost of lower acceptance of incoming delegations.

**Cartel resistance.** The entity seat limit was decisive, as reported in Section 7.2.

**Emission.** The security budget of Section 8.3, and the rejection of flat emission.

**Energy accounting.** Six policies over cost, asset-lock and verifier-error assumptions; the
informational policy was the only one with no pay-to-play and no regional harm.

**Integrated stress test.** Nine scenarios over five seeds and 365 epochs — 16,425 simulated
epochs in total. Six scenarios passed; three failed, and the failures became MainNet readiness
criteria rather than being tuned away. Accounting error over the run was below 3 × 10⁻⁸ tokens,
reward conservation error below 1.5 × 10⁻¹¹, and the supply ceiling was never breached.

### 11.2 Review and Follow-Up

A review round re-ran four contested parts of the programme, and a set of eight follow-up
studies extended it to the real, anonymised distribution of the pool that will migrate at
MainNet — 2,063 holders — and to questions the original programme had not isolated. The
principal results:

**Set size.** Seventy-five was preferred over sixty: worst-world P95 top-ten voting power
38.3% → 34.2%, P05 Nakamoto coefficient 9 → 10.2, mean technical quality 95.42 → 94.60; the
entity-aware confirmation run gave 32.3% and 11. Ninety adds almost nothing on concentration and
keeps degrading quality.

**The ranking key.** Deterministic ranking by reputation alone was confirmed. A multiplicative
stake term at unit exponent reproduced pure stake sort with 98.6% overlap; at squared exponent,
94.4%. Reputation-only ranking kept mean active reputation at 87.8 against 85.9 for stake sort,
with the top-ten voting-power share unchanged.

**Admission.** A rising admission ladder — 2,000 to 50 validators, 2,500 to 100, 3,000 to 200,
4,000 to 300 — was evaluated and not adopted: two of its steps exceeded its own limit on the
rate of change, and the final step removed small operators from the admissible pool. A single
floor was kept; it now stands at 2,000, set after this study rather than by it.

**The per-validator cap on the real distribution.** At a one-to-one conversion, 3% is the
smallest cap that admits essentially the whole migrating pool on day one; 5% adds no
acceptance and worsens concentration. The 2% cap's day-one queue arises from the epoch-start
denominator and disappears against the post-migration base, which locates the question in the
intake rule rather than in the cap.

**Reputation scoring while governance is unmeasurable.** With the governance signal
unavailable, renormalising its weight across uptime and block quality still admits the
selective profile in 9.5% of epochs at threshold 65; an equal 50/50 weighting at the same
threshold admits it in none, detects a seven-day block-quality degradation within two epochs,
and adds no recovery delay. Starting new validators at the eligibility threshold rather than
above it preserves immediate entry while removing the bootstrap grace a weak profile would
otherwise enjoy.

**Reward distribution.** Nine distribution functions were compared on a fixed pool. Founder-era
reward tiers broke pool conservation and were rejected. A bounded reputation gradient — a
validator at reputation 95 earning 1.11× one at 75 on the same stake — held the top-ten reward
share at 21.9%, delegator yield dispersion low and weekly payout variance under one percent.

**Commission.** A five-percent floor was proposed as a guard against a zero-fee race, with a
governed range of 5–15% and a default of 10%; the study did not find a case for delaying
commission updates.

**The migrating distribution.** Gini 0.799, Herfindahl index 0.0057, the largest holder at
2.9% and the top ten at 16.3%: concentrated but not extreme. Twenty-nine holders reach one third
of the pool. The global bonded cap, not the per-validator cap, is the first limit the migration
meets.

### 11.3 What Remains to Be Measured

Every study used synthetic validator behaviour over real or referenced balances. None observed a
production network. Before any of these parameters gates real payouts, the reputation and
scoring parameters are re-calibrated on at least ninety days of real epochs from the public
testnet, with the false-exclusion rate measured rather than simulated; and the set size is fixed
by the consensus benchmark of Section 6.5.

---

## 12. Implementation Status

### 12.1 What Runs

The public developer network runs the four participation modules beside two that keep stake
spread out — the stake engine (admission floor, per-validator and global caps, and a queue for
stake past the global cap) and the delegation router (a delegation that reaches a validator's
cap continues to other validators with room). With them run the complete validator lifecycle —
admission, lock-up, exit, jail and governed restoration — the participation gate, the single
mint path with the supply ceiling, the security-budget emission with fee displacement and
remainder carry, the runtime invariants, and the public explorer, scoreboard and read interfaces
of Appendix C. Participation scoring is in its first version, with uptime as its single signal;
voting power is still stake-weighted, and participation-based selection is the next layer.

### 12.2 MainNet Readiness Criteria

MainNet — and with it real reward payouts — follows the closure and independent verification
of the following criteria:

1. a consensus benchmark fixing the active-set size;
2. re-calibration of the participation parameters on at least ninety days of real epochs;
3. beneficial-owner attestation before activation for every validator;
4. calibration of the cartel detector on a ground-truth dataset, with an appeal workflow;
5. a stake-shock rebalancing rule holding the top-ten share at or below 35% after a 30% exit;
6. an emergency liveness reserve keeping at least 90% of seats filled — 68 of 75 — through a
   combined quarantine and outage;
7. an operating-cost and liquidity assessment confirming the security budget supports real
   operations;
8. the single mint path and the ceiling under integration test — complete;
9. the post-ceiling funding rule;
10. energy-verifier economics, if an economic pilot is to be enabled;
11. the VRF implementation — source chosen, implementation scheduled;
12. a maintained line-by-line mapping from the Parameter Decision Register to code — complete.

### 12.3 Parameters Finalised on Testnet Data

The following values are design values in this paper and are finalised on public-testnet
evidence before MainNet: the active-set size (60 or 75); the split between deterministic and
lottery seats; the lottery ticket weight; the reward gradient; the commission floor and range;
the form of the global cap (hard cap or target ratio) and the on-chain definition of circulating
supply; the migration intake rule; the destination of slashed stake; and the post-ceiling
funding rule.

---

## 13. Related Work

**Cosmos Hub** [3] selects its active set as the top validators by bonded stake and weights
consensus and rewards by stake. SAiCoN inherits its consensus engine, its module framework and
its delegation mechanics, and differs at the selection boundary, where it substitutes
participation for stake.

**Polkadot** [6] selects validators from nominators' stake through an election method designed
to distribute stake as evenly as possible across the elected set, which bounds concentration
through the election rather than through caps. SAiCoN bounds it through explicit limits and
selects on measured participation rather than on stake backing.

**Ethereum** [2] fixes the stake per validator at a constant and selects committees at random,
which makes selection independent of an operator's capital only to the extent that it runs one
validator per unit of stake; an operator with more capital runs more validators. SAiCoN's entity
limits address the same question at the level of the beneficial owner.

**Solana** [5] weights leader selection by stake over a proof-of-history clock. Its
contribution is to throughput; its selection is stake-weighted.

Reputation as an input to validator selection has been proposed in the literature on delegated
and reputation-based consensus. SAiCoN's specific contributions are the asymmetric reputation
filter calibrated against a selective-participation adversary, the separation of compliance,
concentration and energy accounting into layers outside the score, the entity as the unit of
concentration control, and a mint path that is single by construction rather than by
configuration.

---

## 14. Conclusion

SAiCoN reallocates the roles that proof-of-stake assigns to capital. Stake admits a validator and
weights its reward; measured participation decides whether it is selected and how much its vote
counts. Concentration is bounded by limits on validators, on the entities behind them and on the
network, and the controls that a score cannot honestly carry — compliance, cartel resistance,
energy accounting — are enforced as layers of their own. The token has a fixed ceiling enforced
before every mint by the only module that can mint, and its emission is a security budget that
transaction fees displace over time.

The design is implemented on a public developer network, calibrated by executed simulation, and
scheduled through public testnet and external audit to MainNet. The parameters that remain open
are named, and each is fixed by evidence from the testnet rather than by preference.

---

## Appendix A: Protocol Parameters

*Phase* indicates where a value takes effect: **Live** on the developer network now; **TestNet**
when the corresponding layer ships; **MainNet** when finalised at genesis.

| Parameter | Value | Phase |
|---|---|---|
| Supply ceiling | 21,000,000 SCN | Live |
| Mint path | single issuing module; inflation module absent | Live |
| Base unit · precision | `nscn` · 8 decimals | Live |
| Block interval | ≈ 5 s | Live |
| Epoch | fixed block count | Live |
| Self-bond lock-up | 60 days | TestNet |
| Unbonding period | 21 days | Live |
| Admission floor | 2,000 SCN self-bond | TestNet |
| Participation-score weights | 0.50 uptime · 0.35 block quality · 0.15 governance | TestNet |
| Reputation filter | fall 0.65 · recovery 0.10 | TestNet |
| Eligibility threshold | 65 | TestNet |
| Initial reputation | 80 | TestNet |
| Active set | 75 seats: 70% deterministic + 30% lottery — 53 + 22 (Register v1.0 set 60 = 42 + 18; raised 3 September 2026) | TestNet; size confirmed MainNet |
| Rotation | 7 epochs | TestNet |
| Ranking key | reputation | TestNet |
| Lottery randomness | per-validator VRF | TestNet |
| Voting power | min(bonded, cap) × reputation / 100 × compliance | TestNet |
| Per-validator cap | 2% of total bonded; epoch-frozen base with downward ratchet | TestNet |
| Entity limits | 5% stake · 2 seats · 1 lottery ticket | TestNet |
| Global bonded cap | min(8% × ceiling, 60% × circulating) | TestNet |
| Delegator split | ≤ 3 validators | TestNet |
| Overflow routing | decentralization-aware | TestNet |
| Security budget | clip(0.15 × bonded, 0.005 × supply, 0.0075 × supply) per year | Live |
| Fee displacement | fees reduce issuance | Live |
| Remainder | carried forward, offsets issuance | Live |
| Distribution | proportional to voting power, among gate-passing validators | TestNet |
| Commission | protocol minimum and governed range | MainNet |
| Downtime slash | 0 | Live |
| Double-sign slash | 10% of bonded, permanent tombstone | Live |
| Cartel ladder | warn ≥ 0.60 × 2 epochs · review ≥ 0.75 × 2 epochs + 2 evidence classes | TestNet |
| Energy accounting | informational | Live |

## Appendix B: Notation and Formulas

| Symbol | Meaning |
|---|---|
| PS | participation score for an epoch, 0–100 |
| WARP | reputation, the smoothed participation score |
| EP | Empirical Points, the participation accumulator |
| C | compliance flag, 0 or 1 |
| VP | voting power |

```
PS      = 0.50 · Uptime + 0.35 · BlockQuality + 0.15 · Governance

Uptime  = signed / (signed + missed)                        over the epoch
BlockQ  = min(1, proposed / Σ_blocks power_i / totalPower)   accumulated per block

WARP    ← WARP − 0.65 · (WARP − PS)      if PS < WARP
WARP    ← WARP + 0.10 · (PS − WARP)      if PS ≥ WARP

eligible ⇔ status = ACTIVE ∧ C = 1 ∧ WARP ≥ 65
paid     ⇔ status = ACTIVE ∧ C = 1 ∧ EP > 0

VP      = min(bonded, 0.02 · TotalBonded) · WARP / 100 · C

AnnualSecurityBudget = clip(0.15 · TotalBonded, 0.005 · Supply, 0.0075 · Supply)
EpochTarget          = AnnualSecurityBudget / EpochsPerYear
Emission             = max(0, EpochTarget − GasFees − CarriedRemainder)
Emission             = min(Emission, 21,000,000 − Supply)
RewardPool           = Emission + GasFees
```

## Appendix C: Public Interfaces

| | |
|---|---|
| Chain identifier | `saicon-devnet-1` |
| REST | `https://api.saicon.world` |
| RPC | `https://rpc.saicon.world` |
| Explorer | `https://explorer.saicon.world` |
| Address prefix | `saicon` · `saiconvaloper` · `saiconvalcons` |
| Derivation path | coin type 118 |

Module queries are served under `/saicon/chain/{module}/v1/…` for the validator registry,
participation, rewards and compliance. Amounts are transported as strings in base units.

---

## References

1. Nakamoto, S. *Bitcoin: A Peer-to-Peer Electronic Cash System.* 2008.
2. Buterin, V. *Ethereum: A Next-Generation Smart Contract and Decentralized Application
   Platform.* 2014.
3. Kwon, J., Buchman, E. *Cosmos: A Network of Distributed Ledgers.*
4. Buchman, E., Kwon, J., Milosevic, Z. *The latest gossip on BFT consensus.* arXiv:1807.04938, 2018.
5. Yakovenko, A. *Solana: A new architecture for a high performance blockchain.*
6. Burdges, J., et al. *Overview of Polkadot and its Design Considerations.* arXiv:2005.13456, 2020.
7. Srinivasan, B., Lee, L. *Quantifying Decentralization.* 2017.
8. *Cosmos SDK documentation.* docs.cosmos.network.
9. *CometBFT documentation.* docs.cometbft.com.
10. *Inter-Blockchain Communication Protocol — Interchain Standards.* github.com/cosmos/ibc.
11. SAiCoN. *Parameter Decision Register v1.0.* 2026. Internal record, not published; the
    values it fixes are given in this paper and its parameter appendix.
12. SAiCoN. *Architecture Decision Records 0001–0015.* 2026.
13. SAiCoN. *Calibration Studies 1–11: KPI and Reputation; Active Set and Voting Power;
    Delegation and Concentration; Anti-Cartel and Multi-Entity; Rewards and Emission; Green
    Accounting; Full-Protocol Monte Carlo; Review Round 8–11.* 2026. Not published; §11
    summarises their results.
14. SAiCoN. *Follow-up Studies: Migration Distribution and Cap Impact; Delegation Allocation;
    Per-Validator Cap Reconciliation; Score Model Reconciliation; Commission Policy; Reward
    Distribution Function; Entry Stake Ladder; Ranking and Sybil Resistance.* 2026. Not
    published; §11 summarises their results.
15. Micali, S., Rabin, M., Vadhan, S. *Verifiable Random Functions.* FOCS, 1999.
