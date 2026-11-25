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
decentralization-weighted influence. Chain: Cosmos SDK v0.53.8, Go, base denom `nscn`
(1 SCN = 100,000,000 nscn, 8 decimals), max supply 21,000,000 SCN (enforced on every mint
and on the genesis allocation — see §2.5). Layer-1 cost principle (ADR-0007): on-chain only what must be; derived
analytics → off-chain backend; programmable app logic → CosmWasm (Q1).

---

## 1. Current state (as-built, v0.10.2 — 2026-11-25)

### 1.1 Modules

| Module | What it does | Status |
|---|---|---|
| `x/validatorregistry` | status machine (BONDING→ACTIVE→QUARANTINED/JAILED→UNBONDING→INACTIVE, table-gated), activation on bond **through the entity registry's gate** (ADR-0004, superseded in part by ADR-0014; a validator with no attested entity waits at BONDING), 60-day lock-up (V-3, ante-enforced), exit lifecycle + 21-day unstake cooldown (V-5) | ✅ |
| `x/compliance` | C-flag (source of truth + mirror), evidence/clear messages, double-sign auto-jail (any positive burn → CRITICAL_VIOLATION → C=0 + JAILED, ADR-0010: the downtime fraction is 0 in the binary and the `v0.10.0` upgrade enforces it, so the only burn x/staking reports to the hook is a double-sign), **governed MsgUnjail** (gov-authority, 43,200-block cooldown, tombstone never resets), ClearFlag refuses JAILED (`ErrJailedRequiresUnjail`, 1108). **The coordinated-attack sanction (S8 W3, ADR-0010 §3, project decision D-S8-2):** every duplicate-vote equivocation that `x/evidence` accepts is recorded under its evidence hash with the entity the validator belonged to at the infraction height (`x/entityregistry` `EntityAt`); an entity is **armed** when offences of at least two distinct validators are linked to it; a **governance proposal** (`MsgFireCoordinatedSanction`, gov authority only) names the entity, the hashes and the fraction and fires it, after an appeal window (default 7 days from the later of the arming and the latest named offence); each named double-signer's **total** burn becomes `coordinated_attack_fraction` of its stake at the infraction height (default 0.30, code bound [0.10, 1.00]), unbonding and redelegation entries included; compliance event 6 (`COORDINATED_ATTACK`), JAILED, tombstone stays, reputation zeroed best-effort, EP untouched (V-8). Only the incarnation that committed the offence is slashed or classified: the `AfterValidatorRemoved` hook records a tombstoned consensus key whose validator x/staking removes, and a target on such a key, or whose key x/staking holds under another operator, is `NOT_SLASHABLE` and not classified, so a validator re-created on the old key loses nothing; a target whose operator has no `x/validatorregistry` record is slashed and not classified (`SanctionTarget.classification` records why). Queries `offence`, `offences`, `case`, `armed`, `sanction`, `sanctions`, `sanction-preview`; V-15 below. Consensus version 2, migrated by the `v0.10.0` upgrade. Added in S8 (the design notes) | ✅ |
| `x/participation` | PS v1 = precommit-uptime (single signal, weight 1.0) per block-epoch (default 100 blocks, `MsgUpdateParams`-tunable); EP accumulator (+1 per active epoch, optional decay-to-floor-1, `ep_decay=0` default) | ✅ (v1) |
| `x/rewards` | EP-gated emission: `ACTIVE & C=1 & EP>0`. **Gas fees offset issuance** (observed, not taken — x/distribution still pays them stake-weighted). **Single mint path** — the epoch pool is minted once, sized by the Register security budget `clip(0.15/7·bonded, 0.005/7·supply, 0.0075/7·supply)/epochs_per_year`, clamped to the headroom below the **21M ceiling**, split among eligible validators, remainder carried forward; kill-switch `enabled` records a reason per validator; RewardSnapshot per validator per epoch with denial reasons | ✅ gate / ✅ budget + cap + gas offset |
| `x/stakeengine` | the three stake limits (ADR-0012): **entry floor 2,000 SCN** of the operator's own self-bond (tier-1 constant); **global cap** `min(0.08·max supply, 0.60·public circulating)`, public circulating computed on-chain each epoch with the bonded pool, not-bonded pool and queue escrow counted as public; **per-validator cap** `max(0.02·EWMA-30(total bonded), floor)`; **entity cap** `max(entity_cap_fraction·EWMA-30(total bonded), entity_cap_floor)` (default 0.05) on the summed stake of an attested entity's validators, enforced on every path the other caps use — ante, staking hooks, queue fill, router (S8). Enforced in the ante handler: up to five nested `authz.MsgExec` layers are unwrapped and checked; a sixth is refused. Net new stake at the global cap goes to an explicit **stake queue** (`MsgEnqueueDelegation`), escrowed and filled every EndBlock by validator quality (participation score, then EP) | ✅ |
| `x/delegationrouter` | `MsgDelegateRouted` (ADR-0005, §2.8): fills the delegator's chosen validator to its cap and spreads the rest over up to `max_route_legs` more (default 2) by quality × room ÷ stake, the part above the global cap joining the stake queue in the same message; `route` preview query; no module account. Shipped in v0.9.0 (S7) | ✅ |
| `x/entityregistry` | entities (id, display name, SHA-256 of an off-chain attestation document, attestor, 1–8 member validators); the attestors are a gov-only list in params, and `MsgAttestEntity` / `MsgRevokeEntity` are signed by one of them (or the governance authority). **The activation gate:** every path into registry `ACTIVE` — activation on bond, `MsgClearFlag`, `MsgUnjail`, the resume after an aborted exit — asks the registry, and a validator that is not a member of an attested entity, or whose entity already holds `max_active_seats` (default 2, bound [1, 3]) ACTIVE seats, stays at BONDING (a bounded EndBlock pass over held validators promotes them when the attestation or a seat arrives). One lottery ticket per attested entity (`tickets` query). **Frozen seats:** revoking an entity, or removing an ACTIVE validator from one, keeps the seat but records it frozen, so the validator takes no new stake and cannot re-activate; while it stays ACTIVE it is still charged to its former entity's seats and aggregate. **The VRF key (S8 W2):** the validator's operator offers a per-validator ECVRF public key with a proof of possession (`MsgOfferVrfKey`, `vrf-key` query), and the attestor binds the offered key inside `MsgAttestEntity` (field 6, `vrf_keys`); the key and the height at which it was bound are written on the member row, and a key belongs to one validator for the chain's lifetime. **Membership history (S8 W3):** a member row carries the height the validator joined its entity, and a departure row keeps, per validator, the last entity it left, when, and whether its seat stayed frozen and charged to that entity (until when), so `EntityAt(validator, height)` answers which entity owned a validator when it signed — after a removal or a revocation — for the coordinated-attack sanction; genesis carries the departures. First registered upgrade handler, `v0.10.0` (§5.1 R1). Added in S8 (ADR-0014 D1–D3) | ✅ |
| `x/randomness` | per-validator VRF randomness for the lottery (option (c) of ADR-0008, B11), in **shadow mode**. Keys are the ones bound in the attestation (`x/entityregistry`); ECVRF-EDWARDS25519-SHA512-ELL2 (RFC 9381) through `curve25519-voi`, wrapped in `chain/crypto/vrf`. A round falls on the participation epoch grid every `rotation_epochs` (7) epochs and accepts submissions in the `window_blocks` (100) blocks before its rotation height. At the window's opening the pool is snapshotted: attested entities with an ACTIVE member whose aggregate stake is within the entity cap, each with **one designated submitter** (the lowest-addressed ACTIVE member holding a seasoned key). `MsgSubmitVrf` is signed by that validator's operator and the proof is verified on-chain against the snapshot key. The seed is chained (`input` of round N+1 is the seed of round N, hashed with a domain tag, the round number and the sorted verified outputs) and is computed every round; a **2/3 quorum** (`3·submitted ≥ 2·pool`, integer arithmetic) is required for a draw, otherwise the round is SKIPPED with a reason and the previous cohort stands. On quorum, a **shadow draw** of `min(22, pool)` entities is stored, emitted and queryable (`round`, `draw`, `draws`, `latest-draw`) and **read by nothing**: no keeper method returns a draw, and a test fails if a module outside `x/randomness`, `app` and `cmd/saicond` imports it. Nothing is written to stake, status, EP, rewards or tickets for not submitting. `saicond vrf keygen` and `saicond vrf show` handle the key file. Added in S8 (W2, the design notes, [ADR-0008](adr/ADR-0008-voting-power-and-selection.md)); ships in `v0.10.0` with `x/entityregistry` | ✅ shadow |
| Stock: staking, slashing, distribution, gov, bank, IBC (**`x/mint` removed**) | Cosmos SDK v0.53.8, CometBFT v0.38.25, ibc-go v10.7.0 (ADR-0016). SDK defaults, except the slashing fractions and the consensus evidence window — relevant consequences: voting power = top-N by stake. **Slashing: downtime 0 and double-sign 10% are set in the binary** (`app/genesis.go`, the default genesis that `saicond init` and the test harness start from; `app/sim_params.json` pins them for the simulations; the genesis scripts write the same two values, and the `v0.10.0` upgrade raises a stored double-sign fraction below 10% to 10%, leaves a higher one alone and sets a positive downtime fraction to 0). **Evidence window = unbonding:** the same upgrade sets `max_age_duration` to the unbonding time (504 h) and `max_age_num_blocks` to floor(U / 6 s) = 302,400, and the genesis templates write the same values (§4 item 7a). `x/mint` is **removed from the app** (B12 executed, 29 Jul): `x/rewards` is the only module holding Minter for `nscn`, so the 21M ceiling guards every path that can create supply | ✅ slashing (§2.6) · ⚠️ other overrides pending (§2.5) |

### 1.1b Off-chain services

| Service | What it does | Status |
|---|---|---|
| `indexer/` | follows blocks for the one fact the chain cannot answer about itself — which validator proposed which block, carried in every header and indexed by no query — and at each epoch close reads the numbers the chain computed for itself. Serves `/v1/scoreboard`, `/v1/validators/{op}`, `/v1/validators/{op}/epochs`, `/v1/status`, `/v1/concentration`, `/v1/concentration/epochs`, `/v1/entities`. Recomputes nothing the chain computes; derives the concentration measures (top-10 share, Nakamoto by validator and by attested entity, curves) at each epoch's last block from the validator set and the registry read at that height, both stored and served (spec the design notes); absence is `NULL`, never `0`; compliance read from `x/compliance`, never the registry mirror (V-4.1). Go + SQLite (`modernc.org/sqlite`, CGO-free like the rest of the repo) | ✅ v1, gated in CI · public at scoreboard.saicon.world; the concentration endpoints (S8 W4) deploy with the `v0.10.0` upgrade; the scoreboard page reads it |
| `faucet/` | DevNet test-token faucet (Go): **100 SCN per request** — the DevNet's minimum delegation, so one request is enough to try delegating — one payout per address and per client network in each 24 h window, **5,000 SCN** daily budget (`FAUCET_AMOUNT`, `FAUCET_DAILY_BUDGET`). A bad address gets a plain instruction (`Enter a full SAiCoN address: it starts with saicon1.`); the parser's reason goes to the log. The page is the explorer's Faucet tab (`/saicon/faucet`), which fills in the connected wallet's address; the API is the explorer's `/faucet-api/` route. Footer links to the support channel below. Validator stake is not handed out | ✅ S9 · `faucet/README.md` |
| the block explorer | ping.pub fork patched at build time by its branding script. The brand colour is a constant in that file (no `THEME_COLOR` build argument: a host `.env` once turned the deployed explorer purple). **Wallet entry:** "Add SAiCoN to wallet" in the header and the sidebar opens one page that registers the chain in Keplr, Leap or MetaMask (Leap Snap), gas price step 0/0/0 for the zero-fee chain; the header Connect button does not register a chain itself (the connect widget only calls `enable`), so its cog and its "no chain info" link lead to the same page | ✅ S9 |

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
The devnet runs **v0.9.1** on the fresh genesis of its S7 reset (v0.9.0 from 22 October, upgraded
in place on 26 October), with one validator; the JAILED and governed-restore paths above are
exercised on neither environment. **v0.10.0 reaches it in place, not by a reset:** the `v0.10.0`
upgrade handler adds the entity registry and the randomness module, attests the active validator
as its own entity, migrates `x/stakeengine` and `x/compliance` to consensus version 2 and sets the
slashing fractions and the evidence window (the devnet operations runbook,
rehearsed natively from the v0.9.1 binary by `./scripts/rehearse-upgrade-v0.10.0.sh`).

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

v0.9.0 took a new fresh genesis on 22 October (the devnet operations runbook). The
default keeps **one validator**: every S7 rule runs on it, and with the 2,000,000 SCN cap floor a routed
delegation lands whole on that validator. The alternative is the three-validator profile —
300,000 SCN self-bond each, `validator_cap_floor` 400,000 SCN — where a delegation of a few
hundred thousand SCN to one validator overflows into the other two. No part of the chain
requires a minimum number of validators.

Public devnet: 1 node (`saicon-devnet-fsn1-01`), explorer at explorer.saicon.world, open REST
at api.saicon.world; the indexer's endpoint at scoreboard.saicon.world.
Operator support channel (S9): GitHub Issues on `github.com/saicon-network/saicon`, with an
*Node operator question* form (version, profile, Docker or binary, chain id, expected vs actual,
log lines, a reminder never to post a key) and a *Bug report* form in `.github/ISSUE_TEMPLATE/`;
linked from `MULTINODE-JOIN.md` §10, the faucet README and the faucet page.

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
| V-12 | entity registry integrity — **halting**: (a) the member table and its by-entity index are the same relation; (b) every member's entity exists and is ATTESTED, and every ATTESTED entity has 1–8 members; (c) no validator is both a member and frozen; (d) every registry-ACTIVE validator is a member of an attested entity or carries a frozen-seat record. Clause (d) is the point: the only ways to an unattested ACTIVE seat are revocation and removal, and both write the record in the same message, so anything else — a status path that bypasses the gate, a bad genesis — halts the chain rather than pays |
| V-13 | entity limits — **checked, not halted**: per attested entity, ACTIVE seats ≤ `max_active_seats` and aggregate stake ≤ the entity cap, both counting the frozen seats still charged to the entity. Not halted, because a governance decrease, a base move or a merge of two entities legitimately leaves an entity above its limit — it is frozen, not shed. Asserted in tests and reported by the `entity-stake` query |
| V-14 | randomness integrity — **checked at every rotation, never halts**: the round's status and heights are consistent (`key_cutoff_height ≤ window_open_height < rotation_height`); the designated submitters are exactly the pool entries that have a validator; every submission belongs to a pool entry, was made inside `[window_open_height, rotation_height)`, and its proof re-verifies against the snapshot key and the round input to the stored output; submissions never outnumber the pool. A break skips that round's draw with the reason `V-14: …`; it does not panic, because nothing that carries coin, a seat or a status reads this module. To be revisited when S10 starts reading the seed |
| V-15 | coordinated-attack integrity — **checked at every firing and every preview, never halts**: (a) every offence is stored under its own evidence hash, has a kind, and `x/evidence` holds that hash; (b) its consensus address is tombstoned in x/slashing; (c) the by-entity index is exactly the duplicate-vote offences that carry an entity, and a light-client-attack offence carries none; (d) every sanction's id is below the sequence, its fraction is inside the code bounds, each target's offence exists with that sanction id and entity and its classification agrees with its outcome, and every fired offence is listed by its sanction; (e) every sanctioned entity is armed; (f) every consensus key recorded as removed is tombstoned. A break refuses the firing (`ErrCoordinatedInvariant`, 1119) and the preview reports it; it does not panic, because nothing in these records carries coin, a seat or a status until a firing reads them, and the firing is where they are checked |
| V-4.1 | metadata `compliance_flag` mirrors `x/compliance.Compliant` — ⚠ **NOT IMPLEMENTED: no invariant, no runtime check.** The mirror is best-effort: written by `x/compliance.SetCompliance`, but hardcoded `true` on the register and auto-create-on-bond paths, which never consult `x/compliance`. It can diverge undetected. The reward gate reads `x/compliance` directly, so the mirror is not load-bearing for eligibility — it IS load-bearing for the re-bond restore decision. |

### 1.4 Known gaps (honest)

PS single-signal, no WARP · voting power is stock stake-weighted (no selection layer) ·
stake caps: the backstop leaves two routes open (below) · delegation router shipped in v0.9.0 (S7),
the weight function replaced when the S-13 run lands · entity registry, VRF randomness (shadow mode) and the coordinated-attack sanction built in S8 (v0.10.0), with the scope below ·
**V-4.1 has no runtime check** (§1.3) · sentry layer designed in S3, adopted before S9: the operator how-to is MULTINODE-JOIN §7 ("Run the validator behind sentries", S9) and `./scripts/test-sentry-layout.sh` passes 22/22 against the sentry overlay, while the DevNet's own validator keeps the SDK's default open peer exchange; moving it behind two sentries is planned for S10 (§2.10.2) · the DevNet runs
v0.10.1 under Cosmovisor on one validator (the node reports the build id `dev-5460e7d3`; v0.9.0 from 22 Oct, upgraded in place on 26 Oct; the in-place upgrade to
v0.10.0 is the devnet operations runbook), so multi-validator behaviour — the
seat cap across validators, the VRF quorum, the coordinated-attack arming — is verified on the
local three-validator testnet and in the app tests, not on the DevNet.

**Entity registry — what the gate does not reach.** The attestation gate and the seat cap decide registry `ACTIVE` — participation score, EP, PoP rewards, router candidacy, queue fills — and not consensus: the CometBFT set is still x/staking's top 75 by stake, so a held or frozen validator that x/staking bonds signs blocks with its full voting power until the selection layer (S10, ADR-0008) removes it. Undeclared entities are bound only per validator (2% cap): an owner who never declares the validators they run earns nothing on the PoP path with them, but is not otherwise stopped; attestation makes declaration the price of income and cannot make lying impossible, and detection is the second line (ADR-0014 D4, planned, B4). The truth of an attestation is off-chain: the chain records that an attestor vouched for a document whose hash it published. The 5% entity cap binds stake intake against EWMA-30 of total bonded; the effective layer, a cap on current eligible bonded for voting power and rewards, comes with the VP layer in S10. A frozen validator that was ACTIVE when its entity was revoked, or when it was removed from it, keeps its PoP rewards until it leaves ACTIVE (spec J2); governance can demote one through the `x/compliance` evidence path. When an entity holds more than two ACTIVE seats, the seats that stay are the first two activated, provisional until ADR-0008 decides (spec J4). The ticket list is exposed by query; the lottery that consumes it is S10 (a shadow draw over the tickets runs since S8, below). The attestation formats, the limits and the upgrade are in [ADR-0014](adr/ADR-0014-entity-registry.md) and the design notes.

**Coordinated-attack sanction — what it does not reach.** (`x/compliance`, S8 W3, `v0.10.0`.) The chain does not detect, prove or infer coordination: it records objective equivocations and an objective registry linkage, and governance ratifies, by a vote that names the evidence hashes, that the linked equivocations were one attack. Its reach, stated in full:
- **Only the named double-signers are slashed.** The entity's other validators signed nothing twice and are untouched; they stay bonded and signing, and removing them from consensus is the selection layer's (S10, ADR-0008), as for the registry gate above. The sanction does not revoke the entity either; a proposal may carry `MsgRevokeEntity` next to the firing.
- **Coordination without equivocation, and undeclared validators, are out of reach.** A cartel that withholds votes or censors without signing two conflicting votes arms nothing, and a validator that belongs to no entity cannot arm; detection is the ladder of ADR-0014 D4 (planned, B4), and attestation is the first line. Two validators of different entities that double-sign in the same block (a shared client bug) arm nothing: each receives the ordinary double-sign treatment and no more.
- **30% is the owner's uncalibrated default** (D-S8-2; ADR-0010 §3 calls it a starting point calibrated by nothing). No study swept it; it is revisited with the B4 dataset and the Q1'27 audit. The 10% floor is itself a convention (ADR-0010 §2).
- **Only stake still slashable is reached, and the deadline is each target's unbonding.** x/staking cannot slash a fully unbonded validator, and unbonding or redelegation entries that matured, or stake that left before the infraction, are out of reach as for every x/slashing slash. The jailed double-signer starts unbonding at its evidence block, so a sanction must execute within about 21 days of each target's evidence (the earliest target binds); a target that has reached `Unbonded` is recorded `NOT_SLASHABLE` and the others are sanctioned. The `sanction-preview` query reports `earliest_execution_time` and `slashable_until`.
- **An offence must be accepted by `x/evidence` to arm**, and `x/evidence` refuses evidence older than the consensus evidence window. The permissionless submitter is CometBFT's evidence gossip and `broadcast_evidence`; `x/evidence`'s own `MsgSubmitEvidence` has no router on this chain and fails. The `v0.10.0` upgrade sets the window to the 21-day unbonding period (302,400 blocks and 1,814,400 s, exact while the average block interval, halts included, is at most 6 s; §4 item 7a), which meets the rule's dependency on it: an offence reported within unbonding is admissible, and one reported later is never recorded.
- **One departure per validator is kept.** A validator that leaves two entities between its offence and the evidence block keeps only the later linkage; evidence normally lands one or two blocks after the vote.
- **Nothing is back-filled.** An equivocation committed before `v0.10.0` had no entity at its height and never arms.
- **On the one-validator DevNet the rule cannot arm**, since arming needs two validators of one entity. It is exercised by the application tests through real misbehaviour lists and real proposals, and becomes observable on the multi-entity testnet (S9 invite-DevNet, then Q1'27).

**Randomness — what the shadow draw is not.** (`x/randomness`, S8 W2, v0.10.0.) It is the construction of ADR-0008 option (c) running on the chain, with the departures below: keys bound in the attestation, outputs verified on-chain, a chained seed, a quorum with a no-draw fail-safe and a published draw anyone can recompute from the stored pool, outputs and seed. It is not a lottery, and it does not close B11.

- **No seats.** No module reads a draw. The CometBFT set is x/staking's top 75 by stake and the PoP active set is the registry's. ADR-0008's interim policy holds until B11 is chosen, implemented *and audited*; S8 delivers the second, and the third is the Q1'27 audit. The draw is the evidence that policy asks for: submission timing inside the window, the skip rate and the participation rate, measured before a seat depends on them. A structural test keeps it that way: the moment a seat depends on a draw, a test fails and the diff that fixes it is reviewed.
- **The residual, stated as ADR-0008 states it and extended.** The VRF removes grinding by the proposer and by the submitter; it leaves a bounded residual: (1) **one bit per late designated submitter** — outputs are public once they are in a block, so an entity that submits last can compute the draw with and without its own output and choose; (2) **coalitions amplify it in full** — *k* cooperating entities choose among 2^*k* outcomes, and the registry makes undeclared coalitions costlier without being able to detect them; (3) **proposer censorship** — the proposer of a window block can leave other entities' pending submissions out of it, choosing among subsets of their outputs, and the last block of the window has no later proposer to include them; an honest client submits from the first block in which it can, so only late submitters are exposed, who hold the bit anyway; a client that polls every `INTERVAL` seconds is exposed for that long after the window opens, so at the loop's default of 30 s the exposure window equals the polling interval and the runbook recommends a low `INTERVAL` (5 s on the DevNet); ADR-0008's residual does not count this and the implementation records it; (4) **key grinding after an input is public** — closed by seasoning (a key counts in round N only if it was bound before round N−1's window opened), except after a round with no outputs from anyone but the binder's coalition; round 1 is such a round, since it admits no key and so has no outputs at all, and round 2's input is `seed_1 = Seed(1, initial_input, [])`, a function of the published initial input alone, known before any key is bound; round 2 is therefore grindable by whoever binds keys before its cutoff, which on the DevNet is the SAiCoN operator and on a network of several entities is every operator; (5) **key custody** — the chain proves that the operator account offered the key and that someone holds its secret, not that the operator alone does; (6) **the choice of the designated member** — the designation is fixed at the snapshot, after the round's input is public, so an entity with *k* seasoned keyed members can pick which of its *k* outputs counts by taking lower-addressed members out of ACTIVE before the snapshot; this buys nothing blind to other entities' outputs and multiplies the choices of a coalition whose outputs are the only ones in the round. The quorum is a fail-safe and not a defence: withholding can push a round below quorum, which skips the draw and keeps the standing cohort for a round — a lever once a draw carries seats, none in shadow mode.
- **The DevNet exercises the machinery and proves nothing about manipulation.** It has one validator, so the quorum of 2/3 of one is one: the single designated validator must submit, its output is the round's only entropy, and with one ticket the draw always returns that ticket. Scheduling, the snapshot, verification, chaining, skips, publication and the upgrade are exercised; resistance to manipulation is not. The evidence comes from a network of many independent entities — observation in shadow mode on the multi-entity testnet, then the audit.
- **The draw is uniform and over entities, until S10.** ADR-0008's pool is `ACTIVE ∧ C=1 ∧ WARP ≥ 65` less the Top-53, weighted by quality²; WARP (ADR-0009), the Top-53 and the quality weight belong to the selection layer (S10). The shadow draw uses attested entities with an ACTIVE member whose aggregate stake is within the entity cap, draws from the whole pool without weight, and names entities rather than the validator that would take the seat. An entity over its cap is out of the pool until it is back under; an entity frozen only for its seat count stays in.
- **A rotation is "weekly" only at the production epoch.** A round falls every `rotation_epochs` (7) participation epochs; on the DevNet's 100-block epoch that is 700 blocks, and the production epoch length is set with B1 (§2.1).
- **Key lifecycle.** A key counts in round N only if it was bound before round N−1's window opened, so a newly bound key waits between one and two rounds. The key file (`saicond vrf keygen`, `config/vrf_key.json`) is a separate secret from the consensus key and the operator key and is needed online once per rotation; it is backed up like the consensus key and never regenerated on a live chain, because the old key stays owned by its validator for life.

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

**Indexers and queue fills.** In SDK v0.53.8 (as in v0.53.6) the `delegate` event is emitted by x/staking's
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
genesis reads 1814400s / 362880 / 1814400000000000 and the chain produces blocks (the block count
became 302,400 with v0.10.0, §4 item 7a) ·
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
  a tip below its cursor. A DevNet reset that keeps the chain-id and grows past the old
  height is detected once a concentration epoch is stored with its block hash: each pass re-reads
  the block hash at the newest measured epoch that has one and stops on a difference; a database
  with no hashed epoch, or a reset whose tip is below that height (caught by the
  tip-below-cursor check), is covered as before; reset the indexer with the chain
  (`chain-reset` does).

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
  reasons from it: commit–reveal (option (b)) costs ≈224 txs per rotation at 112 candidates (two
  per candidate), which is affordable weekly and seven times less so daily; and it treats a
  rotation as a consensus-level churn event, where the lottery alone can replace 22 of 75 seats
  at one height. The ≈224 figure is commit–reveal's and not the VRF's: the VRF as built (option
  (c)) costs **one transaction per keyed pool entity** per rotation, 53,172 gas each (measured).
  As built, a rotation falls every `rotation_epochs` (7) participation epochs, so "weekly" holds
  only at the production epoch (≈24 h, set with B1); on the DevNet's 100-block epoch a rotation
  is 7 × `epoch_length_blocks` = 700 blocks.
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
  the key is registered inside the B3 attestation ceremony, which has to exist anyway).
  ✅ **implemented in shadow mode (S8, v0.10.0)**: `x/randomness`, with the key offered by the
  operator and bound in the attestation (spec the design notes,
  ADR-0008 "Implementation (S8, October 2026)"). There is no VRF module in Cosmos SDK v0.53.8 (nor in v0.53.6); this
  one is ours, and the lottery still must not carry consensus seats until it is audited (Q1'27).
  The draw is shadow: it awards nothing and is read by nothing; what it leaves open is §1.4,
  "Randomness — what the shadow draw is not". 🔒 B1: set size 75 needs a consensus benchmark
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

### 2.4 Entry, caps & entity controls — ✅ caps and queue (S6) · ✅ entity registry (S8)

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
- ✅ **Entity caps: 5% aggregate stake · max 2 active seats · 1 lottery ticket** (S8, `x/entityregistry`
  and `x/stakeengine`, v0.10.0). A validator is a member of at most one entity; an attestor from the
  governed list in `x/entityregistry` params attests the entity (id, display name, hash of the
  off-chain document, 1–8 validators), and only then can a member become ACTIVE. The **5% aggregate** is
  `max(entity_cap_fraction × EWMA-30(total bonded), entity_cap_floor)` — the same base as the 2%
  validator cap — summed over every member in every bond status, and applied on every path stake can
  take into a member (ante, staking hooks, queue fill, router). The **seat cap** is the gate's:
  the third member waits at BONDING and is promoted when a seat frees. A **frozen seat is charged to
  its former entity** — its seat in the count and its tokens in the aggregate — for as long as its
  validator stays ACTIVE, because it keeps earning; so re-attesting an entity with fresh validators,
  or revoking and re-attesting it, does not give one owner a third seat or a second 5%. The charge
  ends when the frozen validator leaves ACTIVE. The **ticket** is a query
  row per attested entity. Parameters: `attestors` (≤ 10, unique; tier 1), `max_active_seats`
  (default 2, bound [1, 3]), `entity_cap_fraction` (default 0.05, bound [0.02, 0.10] and ≥ the
  validator fraction), `entity_cap_floor` (default 0, bound [`validator_cap_floor`, 5,250,000 SCN]).
  Over a limit an entity is frozen — it takes no new stake and its members cannot activate — and
  nobody is shed. Moving stake between two members of one entity is not growth. A delegation into an
  entity costs 178,814 gas for a one-member entity, about 10% under the 200,000 default, and 202,600
  for an 8-member one, which exceeds it (a lower bound: the measurement used seven non-existent
  members); `create-validator` costs 203,389 (197,780 on v0.9.1), also over the default; clients
  should use `--gas auto`, or at least 300,000 (spec §23.2).
  **B3: beneficial-owner attestation BEFORE first activation** — detection is the second
  line: Stage 7 measured 13–84 days of attacker VP above 33% before containment (P95
  detection day 85–128). The on-chain gate, the seat cap and the aggregate are built; their reach
  is stated in §1.4. Delegator-side: split across **max 3 validators**.

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

### 2.6 Slashing — ✅ fractions, V-8, coordinated-attack tier (S8) · 🚧 downtime window and common-outage exemption (ADR-0010 §1)

| Event | Rule (Register) | Today in code | Work |
|---|---|---|---|
| Ordinary downtime | **Phase 1: zero stake slash** — WARP reduction, reward loss, jail/ineligibility; rolling signed-block window; confirmed common outage exempt | **0 — applied** in the genesis templates (ADR-0010, owner 2026-07-28) and, since 2026-10-28, in the binary's default genesis (`app/genesis.go`); the `v0.10.0` upgrade sets a positive stored value to 0 | ✅ done |
| Double-sign | **10% of bonded** + WARP/PS→0 + C=0 + jail + **permanent tombstone** | **0.10 — applied** in the genesis templates and, since the owner's decision of 2026-10-28, **in the binary**: the default genesis carries it and the `v0.10.0` upgrade raises a lower stored fraction to 10% (a higher one is left alone); C=0 + jail + tombstone ✅ | ✅ fraction done; WARP/PS→0 via the **V-8 revision**, implemented 2026-07-28 (ADR-0010 §4: `ZeroReputation`, EP never touched) |
| Proven coordinated attack | separate, heavier evidence-based rule | **Two-key rule, built:** duplicate-vote evidence for ≥ 2 validators linked to one attested entity **arms**; a governance proposal naming the evidence hashes **fires**, after a 7-day appeal window counted from the later of the arming and the latest named offence. Each named double-signer's total burn is `coordinated_attack_fraction` (default 0.30, bound [0.10, 1.00]) of its stake at the infraction height; JAILED, tombstone, event 6, EP untouched. No K-of-W path over unrelated validators. §1.4 states what it does not reach | ✅ S8 (v0.10.0): ADR-0010 §3 + D-S8-2 |

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

Shipped in v0.9.0 (S7); the public DevNet serves it since its reset on 22 October
(the devnet operations runbook).

- ✅ **`MsgDelegateRouted { delegator, validator, amount }`.** The chosen validator is the
  primary and is filled up to `validator_cap − tokens`. The remainder goes to at most
  `max_route_legs` (default 2, bound [1, 4]) other validators that are registry ACTIVE, not
  jailed, carry a score of at least `min_route_score` (default 0.75, bound [0.50, 1.00]) in
  the latest closed epoch, and have room; weight `w = score × room ÷ max(tokens, 1)`,
  ties by operator address, shares by water-filling with the truncation remainder to the top
  leg. If the legs cannot place the amount the message fails with `ErrNoRoute` naming what
  fits. The weight is a **design choice under research**: brief S-13 (the executed
  allocation comparison) has no run in the archive, and the function is one body so a
  measured result replaces it alone. Since S8 a leg's room is the smaller of the validator's and its entity's, a frozen validator has none, the plan places at most one leg per entity (the heaviest candidate), and no overflow leg goes to the primary's own entity, whose room the primary already shares.
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

**Entity registry parameters (S8), by tier.** *Tier 1:* `x/entityregistry.attestors` — at most 10, unique, `saicon1…` addresses; the type bounds it and the content is policy, which makes it a lever on admission: whoever governance lists can admit validators to income. *Tier 2:* `x/entityregistry.max_active_seats` [1, 3]; `x/stakeengine.entity_cap_fraction` [0.02, 0.10], and not below `validator_cap_fraction`; `x/stakeengine.entity_cap_floor` [`validator_cap_floor`, 5,250,000 SCN] — each enforced in the owning module's `Params.Validate()`. *Tier 3:* one ticket per entity, `MaxMembersPerEntity = 8`, and the gate itself (there is no parameter that turns it off). ADR-0013's tables name the older parameters; these are the S8 additions.

**Randomness parameters (S8 W2), by tier.** *Tier 2:* `x/randomness.rotation_epochs` [1, 28] (default 7) and `x/randomness.window_blocks` [10, 200,000] (default 100), each enforced in the module's `Params.Validate()`; a change takes effect from the next round and never moves the running one. *Tier 3:* the quorum (`QuorumNumerator = 2`, `QuorumDenominator = 3`), the 22 shadow seats, the draw history (`MaxDrawHistory = 52` rounds), the verification gas (`VerifyGas = 2,000`), the suite and the formats of the alpha, the seed and the draw. Governance moves the cadence and the window and nothing that decides how much randomness is enough. ADR-0013's tables name the older parameters; these are the S8 additions.

**Coordinated-attack parameters (S8 W3), by tier.** *Tier 2:* `x/compliance.coordinated_attack_fraction` — default 0.30, bound [0.10, 1.00] inclusive, enforced in `Params.Validate()` and again in `MsgFireCoordinatedSanction.ValidateBasic` and the plan (bounds first, then equality with the parameter); it is also refused below the chain's current `slash_fraction_double_sign` (a stateful check, made in `MsgUpdateParams` and in the plan, not in `Validate()`), so a tier-1 raise of the double-sign fraction above it blocks firing until it is raised. `x/compliance.appeal_window_seconds` — default 604,800 (7 days), bound [86,400, 1,209,600]. *Tier 3:* the bounds themselves (`CoordinatedFractionFloor() = 0.10`, `CoordinatedFractionCeiling() = 1.00`, `MinAppealWindowSeconds`, `MaxAppealWindowSeconds`); the arming rule (duplicate votes of at least two distinct validators linked to one entity, nothing else, and no K-of-W path); the evidence hash definition (`x/evidence`'s own key, 64 uppercase hex); `MaxEvidenceHashesPerSanction = 16`. Firing inside code bounds is an application of slashing policy; moving the bounds or forgiving a slash is an override and stays forbidden (ADR-0013 D5). No message in this chain refunds a burn, clears a tombstone or unjails a tombstoned validator. ADR-0013's tables name the older parameters; these are the S8 additions.

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
| `min-retain-blocks` | `302400` (the evidence window, 21 days / 6 s) | `0` (never) |
| `snapshot-interval` | `1000` | `1000` |
| `snapshot-keep-recent` | `2` | `2` |

Two constraints bind those numbers, and neither is enforced by the SDK:

- `pruning-keep-recent` must exceed `snapshot-interval × snapshot-keep-recent`, or the state
  versions a snapshot needs are pruned before it is taken. 100,000 > 2,000 holds with room.
- `min-retain-blocks` cannot go below the evidence window whatever is configured.
  `baseapp.GetBlockRetentionHeight` takes the minimum of the evidence, snapshot and local
  constraints, so consensus safety wins over disk. It is set to the window explicitly rather
  than left implied — **`max_age_num_blocks` of blocks is the floor, not a choice.** Since the
  window rule of §4 item 7a that is 302,400 blocks, about 17.5 days at the measured 5.01 s
  block. Evidence stays verifiable for the full 21 days: CometBFT's block pruning keeps the
  headers, commits and validator sets of every height whose evidence has not expired (both
  limits), and removes only the block bodies older than the floor.

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
versions and 302,400 blocks, so watching a validator's disk stay flat while an archive's grows
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
changes what an invited operator is told to run — so it lands before **S9**. *Update:* the operator how-to shipped in S9 (MULTINODE-JOIN §7, "Run the validator behind sentries"); moving the DevNet's own validator behind two sentries is planned for S10. Written up as
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
| 0002 | Toolchain version pin | accepted (SDK and CometBFT rows amended by ADR-0016) |
| 0003 | Validator status enum | accepted |
| 0004 | Validator activation model | accepted |
| 0005 | Delegation router | accepted (built in S7) |
| 0006 | CosmWasm contracts | proposed → Q1 |
| 0007 | On-chain / off-chain / contract cost split | accepted |
| 0008 | VP formula + selection layer + **randomness (B11)** | **proposed** (written 2026-07-28) |
| 0009 | Multi-signal PS + WARP engine | **proposed** (written 2026-07-28) |
| 0010 | Slashing config + **V-8 revision** + attack rule | **accepted** (written 2026-07-28; §3 decided by D-S8-2, 2026-10-28, and implemented in S8, v0.10.0; V-8 implemented 2026-07-28; §1's window value and common-outage exemption open 🚧) |
| 0011 | Emission: clip budget, gas offset, single mint path, 21M | **proposed** (written 2026-07-28) |
| 0012 | Caps & stakeengine: tiers, queue, cap base, public-supply measurement, B5 | **proposed** (written 2026-07-28) |
| 0013 | Bounded DAO authority: three tiers, and where each bound is enforced | **proposed** (written 2026-08-15) |
| 0014 | Entity registry: attestation before activation, evidence ladder, appeal | **accepted** (D1–D3 implemented in S8, v0.10.0; D4–D5 planned, B4) |
| 0015 | Sentry layout: exposure as an axis of its own, and the signing node off the open internet | **proposed** (written 2026-08-27) |
| 0016 | Cosmos SDK v0.53.8 / CometBFT v0.38.25 / ibc-go v10.7.0 patch, riding the v0.10.0 upgrade | **accepted** (2026-10-28) |

