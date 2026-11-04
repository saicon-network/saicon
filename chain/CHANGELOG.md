# Changelog

All notable changes to the SAiCoN chain. Versions are git tags; the binary
reports its version via `git describe`.

## Unreleased

## v0.10.0 — 2026-10-29

A new module, `x/entityregistry`, a second, `x/randomness` (per-validator VRF randomness in shadow
mode), and the chain's first upgrade handler. **State-breaking**, applied by the `v0.10.0` upgrade
handler at a halt height: the DevNet is upgraded in place, with no reset. Both modules, the VRF
key fields and the coordinated-attack sanction (`x/compliance` consensus version 2) arrive in the
same `v0.10.0` upgrade.

### Added

- **The entity registry (`x/entityregistry`).** A validator now has to belong to an *attested
  entity* before it can earn. An operator asks an attestor to attest the entity — an id, a display
  name, the SHA-256 of an off-chain attestation document and one to eight validators — with
  `MsgAttestEntity` (or the governance authority does); until then the validator can bond and sign
  blocks, but stays `STATUS_BONDING`, with no score, no Empirical Points and no rewards, and is
  promoted at the end of the block in which it is attested. Three limits apply per entity: at most
  **2 active seats** (`max_active_seats`, bound 1-3), an aggregate stake cap of **5%** of the same
  EWMA-30 base as the validator cap (`entity_cap_fraction`, bound 2-10%, and `entity_cap_floor`)
  enforced on every path that adds stake — transaction, governance, group, authz, the queue and
  the router — and **one lottery ticket** per attested entity. An entity over a limit, or one
  whose attestation is revoked, is frozen: it takes no new stake and its validators cannot
  re-activate, but nothing is demoted or force-unbonded. `MsgRevokeEntity` withdraws an
  attestation. The attestors are a governed list in the module's params.
  Queries: `saicond q entityregistry {params, entities, entity, validator-entity, tickets}` and
  `saicond q stakeengine entity-stake`; messages: `saicond tx entityregistry {attest, revoke}`.
  Invariant V-12 halts the chain if an ACTIVE validator is neither attested nor recorded as a
  frozen seat; V-13 reports an entity above its limits. The gate decides the participation
  active set — scoring, rewards, routed and queued stake — and not CometBFT's voting set, which
  stays x/staking's top 75 by stake until the selection layer (S10).
- **The first upgrade handler, `v0.10.0`.** Adds the `entityregistry` store, migrates
  `x/stakeengine` to consensus version 2 and attests every currently ACTIVE validator as its own
  one-member entity, so no earning validator loses its seat. The attestors for the new list are
  read from the upgrade plan's `info` (`{"attestors":["saicon1…"]}`). Rehearsed from the v0.9.1
  binary by `./scripts/rehearse-upgrade-v0.10.0.sh`; the runbook is
  the devnet operations runbook.
- **A localnet demo**, `./scripts/demo-entityregistry.sh`, and a genesis attestation in the DevNet
  and testnet genesis scripts.
- **A per-validator VRF key, offered by the operator and bound in the attestation.** An operator
  runs `saicond vrf keygen --output <file>` (a separate secret from the consensus key and the
  operator key, mode 0600, never overwritten), then
  `saicond tx entityregistry offer-vrf-key --vrf-key-file <file> --from <operator>`, which sends
  `MsgOfferVrfKey` with a proof of possession bound to the chain id, the validator and the key. The
  attestor binds the offered key with `saicond tx entityregistry attest … --vrf-keys '{"validator":
  "saiconvaloper1…","vrf_public_key":"…"}'`; a key the operator did not offer is refused, and a key
  belongs to one validator for the chain's lifetime. The operator then runs the submitter
  (`./scripts/vrf-submit-loop.sh`, which calls `saicond tx randomness submit-vrf` every 30 s) so the
  entity's output is in each round. Query: `saicond q entityregistry vrf-key <saiconvaloper…>`;
  `x/entityregistry` errors 1107-1110 (`ErrInvalidVrfKey`, `ErrVrfKeyInUse`, `ErrVrfKeyNotOffered`,
  `ErrInvalidProofOfPossession`); events `vrf_key_offered` and `vrf_key_bound`.
  ECVRF-EDWARDS25519-SHA512-ELL2 (RFC 9381), checked against the RFC's Appendix B.4 vectors.
- **`x/randomness`, VRF randomness in shadow mode (ADR-0008 option (c), B11).** A round falls on the
  participation epoch grid every `rotation_epochs` (7) epochs and takes submissions in the
  `window_blocks` (100) blocks before it. The pool is the attested entities with an ACTIVE member
  inside the entity cap, each with one designated submitter; `MsgSubmitVrf` carries an 80-byte proof,
  verified on-chain (2,000 gas) against the key in the round's snapshot. The seed is chained
  from round to round and computed every round; a 2/3 quorum is needed for a draw, otherwise the
  round is skipped with a reason and the previous cohort stands. On quorum a draw of `min(22, pool)`
  entities is stored and published. **The draw awards no seats and nothing reads it**; the chain's
  validator set, the PoP active set, stake, EP and rewards do not depend on it, and not submitting
  costs nothing. Queries: `saicond q randomness {params, round, draw <round>, draws, latest-draw}`
  (REST under `/saicon/chain/randomness/v1/`); transaction: `saicond tx randomness submit-vrf
  --vrf-key-file <file> --from <operator>`, which prints the reason and exits 0 when there is
  nothing to do; events `randomness_round_scheduled`, `randomness_window_opened`,
  `vrf_output_submitted`, `randomness_seed_computed`, `randomness_shadow_draw`,
  `randomness_draw_skipped`; parameters `rotation_epochs` [1, 28] and `window_blocks`
  [10, 200,000] (`MsgUpdateParams`, governance); `x/randomness` errors 1100-1106. Invariant V-14 is checked at
  every rotation and never halts: a break skips that round's draw. The module leaves a bounded
  residual (one bit per late submitter, amplified by coalitions; censorship by a window block's
  proposer; key grinding right after the upgrade) described in ADR-0008 and DEVELOPMENT.md §1.4.
  Gas, measured: `MsgSubmitVrf` 53,172, `MsgOfferVrfKey` 60,671, `MsgAttestEntity` 71,477 with one
  key and 165,833 with eight. Clients submit from the second block of the window: a `--gas auto`
  simulation in the block before it is refused, and the loop retries.
- **The coordinated-attack sanction (`x/compliance`, ADR-0010 §3, project decision D-S8-2).** A third
  slash tier, fired by governance and armed only by evidence. Every duplicate-vote equivocation that
  `x/evidence` accepts is recorded under its evidence hash (64 uppercase hex) with the entity the
  validator belonged to at the infraction height; an entity is **armed** when offences of two or more
  distinct validators are linked to it. A governance proposal carrying `MsgFireCoordinatedSanction`
  (`entity_id`, `evidence_hashes`, `fraction`, `reason`; the signer is the gov module account, there
  is no transaction command) fires it once the appeal window has passed (default 7 days, counted from
  the later of the arming and the latest named offence): each named double-signer's **total** burn becomes
  `coordinated_attack_fraction` of its stake at the infraction height, unbonding and redelegation
  entries included, so the sanction burns the fraction minus what `x/evidence` already burned. The
  validator stays JAILED and tombstoned, its compliance record takes the new event 6, and its
  reputation is zeroed best-effort; EP is never touched. Only the incarnation that committed the
  offence is slashed or classified: a target whose validator x/staking removed after the offence (a
  validator re-created on the old consensus key included, which the new `AfterValidatorRemoved` hook
  records) or whose key another operator holds is `NOT_SLASHABLE` and not classified, and a target
  whose operator has no `x/validatorregistry` record is slashed and not classified; each target's
  `classification` records which. The chain does not detect, prove or infer
  coordination: it records equivocations and registry linkage, and the vote ratifies. Two validators
  of different entities that double-sign together arm nothing, and neither does a lone double-sign.
  Operators and voters see it through `saicond q compliance {armed, case <entity>, offence <hash>,
  offences, sanction <id>, sanctions, sanction-preview <entity> <fraction> <hash>...}` (REST under
  `/saicon/chain/compliance/v1/`): `armed` lists the entities with an unfired offence, and
  `sanction-preview` runs the firing's own planning function and reports `fireable`, the reason, each
  target's extra fraction and outcome (`SLASHED`, `NOTHING_TO_ADD`, `NOT_SLASHABLE`), the earliest
  execution time and the unbonding deadline. A proposal that is not fireable at its end fails with the
  registered error and writes nothing (`x/compliance` errors 1109-1120). Events
  `coordinated_offence_recorded`, `coordinated_entity_armed`, `coordinated_sanction_fired` and
  `coordinated_sanction_target`. Invariant V-15 is checked at every firing and preview and never
  halts: a break refuses the firing. Gas, measured: recording one offence 26,857 (one that does not
  arm: the arming check and event are not in it); the firing handler
  226,232 with two targets and 321,616 with three; `sanction-preview` 39,766; `MsgSubmitProposal`
  carrying a firing with two hashes 173,848. Limits: only the named double-signers are slashed;
  coordination without equivocation is out of reach (ADR-0014 D4); 30% is an uncalibrated default; a
  sanction must execute within about 21 days of each target's evidence (x/staking cannot slash a fully
  unbonded validator); nothing before `v0.10.0` is back-filled; the rule cannot arm on a one-validator
  chain, and an offence arms only inside the consensus evidence window (DEVELOPMENT.md §4 item 7a).
- **`x/entityregistry` keeps a membership history**: a member row carries the height the validator
  joined its entity (`joined_height`, field 6), and a departure row keeps, per validator, the last
  entity it left, when, and whether its seat stayed frozen and charged to it. Both are in genesis.
  The attestation made by the upgrade sets `joined_height` to the upgrade height.
- **`saicond vrf {keygen, show}`**, and two localnet scripts: `./scripts/demo-randomness.sh` (a key
  ceremony, a drawn round, a skipped round with the submitter stopped, a drawn round again) and the
  VRF key ceremony in `./scripts/rehearse-upgrade-v0.10.0.sh` (`SKIP_VRF=1` keeps the earlier path).
  The DevNet and testnet genesis scripts bind a VRF key per validator and draw a random initial
  input.
- **The decentralization panel (indexer and scoreboard).** At each epoch's last block the indexer
  reads the consensus validator set and the entity registry at that height and stores the top-10
  voting-power share, the Nakamoto coefficient by validator and by attested entity, and the two
  concentration curves, computed with exact integers from the voting powers it stores beside them.
  New endpoints `/v1/concentration`, `/v1/concentration/epochs` and `/v1/entities` (each entity's
  stake against its cap and seats against the limit, frozen seats included). The scoreboard draws
  them with the 35% reference line and the published stress-test results to compare against; the
  methodology is a docs page on the site. The indexer also stops on a reset that keeps the chain
  id, by re-reading the block hash of the newest measured epoch. Spec
  the design notes.

### Changed

- **`MsgAttestEntity` gains field 6, `vrf_keys`** (validator and offered key); **`Member` gains
  fields 4-5**, `vrf_public_key` and `vrf_key_height`; the entity-registry genesis gains the key
  offers and the key owners. An attestation that does not name a staying member's key leaves it and
  its height as they were. The upgrade attests the existing validators without keys, so every
  entity is keyless until its operator offers a key and an attestor re-attests with it.
- **Cosmos SDK v0.53.8, CometBFT v0.38.25, ibc-go v10.7.0** (from v0.53.6, v0.38.21, v10.4.0),
  the upstream-labelled security patch (upstream publishes no advisory) inside the same release
  family, applied by the same `v0.10.0` upgrade (ADR-0016). State-breaking upstream: malformed
  signatures and signer infos are refused instead of panicking, a missing historical-rewards
  record is an error instead of a zero payout, an out-of-transaction reward withdrawal to a
  blocked address falls back to the owner and then the community pool, and interchain-account
  packets get stricter validation. CometBFT
  rejects a block timed 60 s or more ahead of the node's clock (`block_time_tolerance`, new in
  `config.toml` with `event_bus_buffer_capacity`; an older `config.toml` takes the defaults).
  **Operators: keep the host clock in sync (NTP).** A new event, `withdraw_addr_redirected`,
  marks a withdrawal that fell back to the owner or the community pool. No `cosmossdk.io` module,
  store or migration changes.
- **The slashing fractions are in the binary: double-sign 10%, downtime 0** (ADR-0010, owner
  decisions of 2026-07-28 and 2026-10-28). Until now only the two genesis scripts set them; a
  chain made by plain `saicond init` and the in-process test harness ran the SDK's 5% and 1%. The
  app's default genesis now carries both values, and `app/sim_params.json` pins them for the
  simulations, which draw their own slashing genesis. The `v0.10.0` upgrade raises a stored
  double-sign fraction below 10% to 10% (a higher value stays) and sets a positive downtime
  fraction to 0, because x/compliance reads any positive burn as a double-sign. The DevNet already
  runs both values from its genesis template, so the upgrade changes nothing there.
- **The evidence window is the unbonding period** (project decision 2026-10-29, DEVELOPMENT.md §4
  item 7a). The `v0.10.0` upgrade sets the `x/consensus` evidence limits to `max_age_duration` = the
  staking unbonding time U and `max_age_num_blocks` = floor(U / 6 s): 302,400 blocks and 1,814,400 s
  for 21 days. CometBFT and `x/evidence` expire evidence only when both limits are passed, so the window
  is exactly U while the average block interval over a U-long span, halts included, is at most 6 s (the
  DevNet measures 5.01 s, which absorbs about 3.5 days of halt in 21 days), and longer only
  otherwise. The DevNet's stock 100,000 blocks / 48 h (about 5.8 days) is raised; a larger stored
  window is capped, because evidence older than U could reach a validator re-created on the key; one
  already at the target is left alone. Evidence `max_bytes` and the other consensus groups are kept
  byte for byte, and the result is validated as `x/consensus` validates a `MsgUpdateParams`. The genesis
  templates now use the same rule (302,400 blocks instead of 362,880, and the same `min-retain-blocks`),
  and so does `./scripts/gov-evidence-window.sh`: it reads the unbonding time from the chain, reports a
  window shorter (GAP) or longer (EXCESS) than unbonding, says "ok, nothing to propose" on a chain at
  the rule, and its governance proposal never sets a limit above the rule. It also measures the window
  in force now (the age of the oldest block the block limit still protects) and reports `EXCESS NOW`
  with exit 1 when a halt has stretched it past unbonding, even with the limits at the rule; when that
  window cannot be measured it says so and exits 3 rather than passing.
- **`x/compliance` parameters gain fields 2-3**, `coordinated_attack_fraction` (default 0.30, bound
  [0.10, 1.00], and not below the chain's `slash_fraction_double_sign`) and `appeal_window_seconds`
  (default 604,800, bound [86,400, 1,209,600]); the module is at **consensus version 2** and its genesis
  gains the offences, the sanctions, the next sanction id and the removed consensus keys. The `v0.10.0`
  upgrade migrates it: the cooldown is kept, the two new values take their defaults, and the fraction
  is then lifted to the double-sign fraction in force if that is higher. The new collections live in
  the existing store.
- **A new compliance event, 6 (`COMPLIANCE_EVENT_TYPE_COORDINATED_ATTACK`)**, written only by the
  sanction. `MsgSubmitEvidence` refuses it (`ErrInvalidEvent`), and the status transition treats it as
  JAILED, like `CRITICAL_VIOLATION`.
- **The double-sign hook stays silent inside a sanction's own slash.** x/staking calls
  `BeforeValidatorSlashed` again during it; the hook is told by a context marker to return, so the
  burn is recorded as event 6 and not as a second double-sign.
- **`github.com/oasisprotocol/curve25519-voi` is a direct dependency** (the same version, already
  linked through CometBFT; BSD 3-Clause), used by `chain/crypto/vrf`. No module is added.
- **Activation is gated.** Activation on bond, `MsgClearFlag`, `MsgUnjail` and the resume after
  an aborted exit now ask the registry. `MsgClearFlag` and `MsgUnjail` fail for a validator
  without an attestation (`ErrActivationHeld`, code 1107). Auto-created and genesis validators
  become ACTIVE only with an attested entity (ADR-0004, superseded in part; ADR-0014 accepted for D1-D3).
- **`x/stakeengine` consensus version 2**, with two new parameters: `entity_cap_fraction`
  (default 0.05) and `entity_cap_floor`; the migration sets them from the validator values.
  New errors: `ErrEntityCapReached` (1114) and `ErrEntityFrozen` (1115).
- **The router honours entity room.** One leg per entity; a frozen validator receives nothing
  (ADR-0005).
- **Gas.** `create-validator` uses 203,389 gas (197,780 on v0.9.1), so the 200,000 default
  fails; a delegation into an entity uses 178,814 gas for a one-member entity (about 10% under
  the 200,000 default) and 202,600 for an 8-member entity, which exceeds it, so clients should
  send `--gas 600000` or `--gas auto --gas-adjustment 1.5`.
- **Lint is clean and blocking.** golangci-lint finds nothing in `chain/` or `indexer/`;
  unchecked `Close` errors, redundant assignments, unused code and simplifiable selectors
  are fixed, and the remaining uses of APIs the SDK marks deprecated but v0.53 still needs
  (x/params, the legacy module interface, the simulation flags) carry a reason. The lint
  workflow covers both modules and fails the check on any finding.

## v0.9.1 — 2026-10-25

A patch on v0.9.0, from an independent audit of the tree before its first public release.
It closes a way to skip the self-stake lock-up, makes the 21M ceiling bind the genesis
allocation too, hardens the container scripts, moves the Go module to its public home and
makes every release carry the licenses and sources of what it ships. The lock-up fix is
**state-breaking**: validators upgrade together, at a halt height.

### Fixed

- **Self-stake lock-up could be skipped by asking for too long a one.** The duration a
  validator commits to was bounded only from below; a value large enough to overflow
  the nanosecond arithmetic put the expiry in the past and let the self-stake unbond at
  once. Registration now refuses anything above ten years
  (`requested self-stake lock-up is longer than the chain maximum`, code 1106). A
  stored or genesis record keeps its lock-up as written up to the longest that can be
  computed (~292 years); one beyond that is refused at startup and, if already stored,
  read as that longest value, so it stays locked.
  **State-breaking:** a v0.9.0 node accepts a registration a v0.9.1 node refuses, so
  every validator moves to v0.9.1 together, at a halt height.
- **Genesis above the 21M ceiling.** A genesis whose initial `nscn` supply exceeds 21M
  SCN no longer starts. Before, it started and the ceiling only stopped later emission.
- **Container scripts no longer print mnemonics.** The single-node and testnet scripts
  create keys with `--no-backup`, so recovery phrases stay out of container logs.
- **The hidden validator of the sentry layout serves nothing.** With
  `NODE_EXPOSURE=validator` RPC listens on loopback inside the container and REST is
  off on every start.

### Changed

- **Go module path** is now `github.com/saicon-network/saicon/chain` (and `/indexer`).
  Code importing the previous module path must change its imports.
- **Third-party licenses ship with the node.** `./scripts/third-party-licenses` collects the
  license files of every module linked into `saicond` for the target platform, the source
  of the MPL-2.0 and LGPL packages among them, and the Go standard library's terms, and
  refuses a module whose terms are missing or not recognised. The release archives carry
  the result in `third_party/`; the image carries it in `/usr/share/licenses/saicon`,
  with the installed Alpine packages and their licenses, and is published with an SBOM.
- **The image ships with the source of its copyleft Alpine packages.** Busybox, bash,
  readline, apk-tools and the other GPL / MPL packages of the base system: each release
  attaches their build recipes, patches and upstream archives, for exactly the versions in
  the image, as `saicon-image-alpine-sources_<version>.tar.gz`. Images are published only
  for release tags, and only after those sources were collected; the image upgrades its
  packages to the Alpine 3.20 branch head when built.
- CI tests the indexer as well, and the image and release workflows publish only a
  commit that passed. The image reports its real version.

## v0.9.0 — 2026-10-12

Delegating no longer means finding room by hand. A delegator names a validator; when
that validator is at its cap the rest of the stake goes to other validators that have
room, and anything past the global cap waits in the queue, all in one transaction. The
rules that keep stake spread out - the caps, the entry floor, the lock-up - now hold
on every path a delegation can take, governance proposals included.

### Added

- **`x/delegationrouter`.** `MsgDelegateRouted { delegator, validator, amount }`: the
  chosen validator is filled up to its cap and the remainder is spread over at most two
  more validators (`max_route_legs`) that are active, not jailed, score at least 0.75
  (`min_route_score`) in the latest closed epoch and have room. Weight is
  `score x room / stake`; ties break by operator address. The part above the global cap
  joins the stake queue in the same message, all or nothing. A plain `MsgDelegate` keeps
  its stock behaviour and, at a cap, is refused with an error that names the router. The
  weight function is a design choice under research (brief S-13 has no run in the
  archive) and is one function body. A routed delegation needs about 1,000,000 gas.
  Queries under `/saicon/chain/delegationrouter/v1/`: `params` and `route` (a preview of
  the plan the message would execute, from the same function).
- **At most three validators per delegator**, on every path: `MsgDelegate`,
  redelegation, cancelling an unbonding, the router and the queue. An operator's own
  self-bond and unbonding entries do not count. `max_validators_per_delegator`
  (default 3, bound 2-5) is a governance parameter.
- **A 100 SCN minimum delegation** on `MsgDelegate`, `MsgDelegateRouted` and
  `MsgEnqueueDelegation` (`min_delegation`, bound 1-10,000 SCN); the minimum queue entry
  rises to 100 SCN to match.
- **The staking backstop.** The S6 caps, the entry floor and the V-3 lock-up are now
  also enforced in `x/staking`'s delegation hooks, so proposals executed by `x/gov` or
  `x/group`, an interchain-accounts host and `authz` can no longer pass around them.
  Slashing and every EndBlock path stay unblockable.
- **A three-validator devnet profile** with a cap floor low enough for overflow to occur,
  REST published on node0 only, and a localnet demo of the router story.
- ADR-0005 (the router), accepted.

### Changed

- **The devnet restarts from a fresh genesis** — with one validator by default, as today; the
  three-validator profile is the alternative (the devnet operations runbook).
- **The roadmap moves to S8.** S7 is shipped; entity caps and the concentration
  dashboard are in progress.

### Known limits

The global cap is measured on the bonded pool, so stake that becomes bonded without a
delegation operation (a validator entering the active set, several same-block validator
creations) is not seen by the hook; the 100 SCN minimum is not applied through
governance, group or ICA; existing over-concentrated delegations are not rebalanced.
The list, and what closes each, is in `docs/DEVELOPMENT.md` section 1.4.

## v0.8.0 — 2026-10-07

The network now limits how much stake it will hold, and says plainly how much it
holds. Until this release nothing on the chain stopped one validator from taking
most of the stake, or a new validator from entering with a token delegation, and
the participation numbers the indexer serves had no page of their own. This release
adds the module that draws those limits, cuts issuance to a seventh, and puts the
validator set on a public page.

### Added

- **`x/stakeengine`.** Three limits and a queue, all enforced on the transaction
  path.

  *The entry floor.* A new validator must bond at least 2,000 SCN of its own
  self-bond; delegations never count toward it. The floor is a code constant, not a
  parameter.

  *The global cap.* Total bonded stake may not pass `min(8% × max supply,
  60% × public circulating)`. Public circulating supply is computed on-chain at each
  epoch close, and bonded and queued stake both count as public — so staking does
  not shrink the base the cap is measured against.

  *The per-validator cap.* `max(2% × a 30-epoch moving average of total bonded,
  validator_cap_floor)`. The moving average moves slowly, so every check inside an
  epoch reads one value; the floor keeps delegation open on a network with few
  validators. The devnet genesis sets the floor to 2,000,000 SCN.

  *The queue.* A direct `MsgDelegate` that would take total bonded past the global cap
  is rejected, and the rejection points at `MsgEnqueueDelegation`: the delegator
  queues the stake explicitly (`MsgCancelQueuedDelegation` takes it back), the
  amount sits in escrow, and `EndBlock` fills the queue every block, ordered by
  validator quality — participation score first, then EP. Redelegation from a bonded
  validator flows freely at the global cap; the destination's own per-validator cap
  still applies.

  *Invariant V-10* checks every block that the queue's escrow balance equals the sum
  of its entries, exactly; the escrow is a blocked address, so nothing can reach it
  by transfer. A violation halts the chain, like the other runtime invariants.

  Six queries under `/saicon/chain/stakeengine/v1/`: `params`, `caps`,
  `validators/{address}/cap`, `queue`, `queue/{id}` and
  `delegators/{address}/queue`.

- **The public scoreboard (on the project website).** The whole validator set on one
  page, sortable by score, with each validator's epoch history behind a click and the
  caps shown beside it. It reads the S5 indexer and the chain's own REST, converts
  amounts at display time only, and shows `—` for anything the indexer has no record
  of rather than a number it made up. English and Chinese.

### Changed

- **The security budget is a seventh of what it was.** The three clip rates in the
  defaults are the Register values divided by seven, so the formula keeps its shape:
  about 29 SCN a day on the devnet, from about 205. A network started from a fresh
  genesis carries them; one already running takes them by governance
  `MsgUpdateParams` on `x/rewards`.
- **The devnet restarts from a fresh genesis.** A new module needs one. The indexer's
  volume goes with it, because it must not keep the old chain's epochs. On a fresh
  genesis the caps are 1,680,000 SCN globally and 2,000,000 SCN per validator
  (the devnet operations runbook).
- **The roadmap moves to S7.** S6 is shipped; the delegation router is in progress.

### Known limits

The caps are checked in the ante handler, so messages that bypass it are not covered
(group, gov and ICA-host proposals); the global cap is measured on the bonded pool
only; a new validator has no way in while the global cap is full. The full list, and
what closes each, is in `docs/DEVELOPMENT.md` §1.4, "Stake caps — what S6 does not
cover yet".

## v0.7.0 — 2026-09-23

The chain answers about participation one address at a time. A view of the whole
set costs hundreds of requests against the node that is also signing blocks, and
there is one question it cannot answer at all: which validator proposed which
block. The header carries it and no query indexes it. This release adds the
service that closes both.

### Added

- **An off-chain indexer (`indexer/`).** A Go service that follows blocks, takes
  the proposer from each header, and at every epoch close reads the numbers the
  chain computed for itself — Participation Score, empirical points, reward
  snapshot, registry standing, compliance. Four JSON endpoints: `/v1/scoreboard`,
  `/v1/validators/{operator}`, `/v1/validators/{operator}/epochs` and
  `/v1/status`. SQLite through `modernc.org/sqlite`, because the monorepo builds
  CGO-free and the usual driver does not.

  It recomputes nothing. Every figure it serves was either read back from the
  chain verbatim or counted by walking headers, and the proposal count is the
  only one it originates.

  That count is also the only one no chain query can audit, so the write path is
  built around it: the counter and the height cursor advance in a single
  transaction, a height at or below the cursor is a replay and is ignored, and a
  count is written only for an epoch the follower has walked end to end — below
  that it stays `NULL`, because "counted none" and "have not counted yet" cannot
  share an integer.

  Absence is `NULL` throughout, never `0` or `""`, and the three absences stay
  distinct: no registry record at all, no ruling from `x/compliance`, no epoch
  walked. Compliance is read per address from `x/compliance` and never from the
  registry's `compliance_flag`, which invariant V-4.1 records as able to diverge
  undetected — on the devnet the mirror says `true` while `x/compliance` has no
  record. Amounts stay strings in base units end to end.

  Epoch numbering follows the chain's own: `x/participation` closes epoch
  `h/epochLen` at `h % epochLen == 0, h > 1`, so epoch 1 is heights 1–100 and
  there is no epoch 0. The length is recorded on first sight and re-checked every
  pass; a governance change to it renumbers the chain's epochs while everything
  already indexed keeps the old bucketing, which nothing in the data would reveal
  afterwards, so the service stops rather than serving the mixture.

  It stops, loudly, on three conditions it cannot fix by retrying: a chain shorter
  than the index (a reset that reused the chain id), a changed epoch length, and a
  different chain id in the same database. `/v1/status` answers 503 for all of
  them and for an unreachable node, and keeps serving what was already learned.

- **`indexer-build` and `indexer-test` in CI**, the same three gates the chain
  carries, with their own cache key — the shared one is keyed on `chain/go.sum`
  and would never have invalidated on an indexer dependency change.
- **A container and a vhost.** `indexer/Dockerfile` builds CGO-free into a 24 MB
  image; the block explorer gains the service, a healthcheck on `/v1/status`, and a
  `{$SCOREBOARD_DOMAIN}` vhost.

### Changed

- **Validator self-stake floor is 2,000 SCN, lowered from 3,000** (project decision,
  2026-09-22). The floor is not enforced in code — registration still performs no
  stake check — so this is a change to the published parameter and to every document
  that states it, ahead of the stake engine that will enforce it.
- **Self-stake lock-up is 60 days, lowered from 180.** `DefaultLockupDurationSeconds`
  changes, and with it what a new registration receives when it names no duration and
  the minimum a registration may request.

  **It does not shorten anyone's existing lock-up.** The window is frozen at bonding
  time (V-3), and `unbonding.go` clamps a stored duration *up* to the default and never
  down — so a validator already carrying 180 days keeps 180. Only a chain started from
  fresh genesis has everyone at 60.

  Study S-19 swept the admission ladder and recommended keeping 3,000. This lowers it
  anyway, by decision rather than by measurement, and ADR-0012 now says so in place of
  the old "provisional" note.

  A new test pins the value itself: every other assertion in the package compares
  against the constant symbolically and would have stayed green through a silent edit,
  while the white paper, the lite paper and the public site all publish the number.

## v0.6.0 — 2026-09-09

Retention and exposure are two different axes, and until this release the node
only had one of them. A node kept either a validator's window or everything, and
whatever it kept, it faced the network the same way. Both halves are now
configurable and independent.

### Added

- **A third node profile.** `NODE_PROFILE=full` keeps every block with state
  pruned to the validator's window — what an explorer or an indexer actually
  needs, at a fraction of an archive's disk. `validator` and `archive` are
  unchanged.
- **The sentry layout (ADR-0015).** `NODE_EXPOSURE` gives a node one of three
  postures: `open` is the SDK default; `validator` turns peer exchange off and
  dials only its own sentries; `sentry` keeps peer exchange on while listing the
  validator in `private_peer_ids`, which is what stops its address being
  gossiped. A sentry that omits that flag defeats the whole layout while looking
  correct, so the three-node stand wires it rather than documenting it.
- **A validator with peer exchange off and no sentries refuses to start.** That
  configuration is partitioned, not protected, and it used to start anyway.

### Changed

- **The active set is 75 seats, raised from 60.** Worst-case Top-10 voting power
  falls from 38.3% to 34.2% and the Nakamoto coefficient rises from about 9 to
  10.2, paid for in mean technical quality — 95.42 down to 94.60. A set of 90
  buys almost nothing further. `max_validators` is 75 in both genesis paths; it
  remains an `x/staking` parameter, so a running chain moves it by governance
  without a restart and without a migration. The 70/30 split carries over as 53
  deterministic seats and 22 by lottery, and the consensus benchmark can still
  move the size before mainnet.
- **The emergency liveness floor is restated against the new size** — at least
  90% of seats filled, 68 of 75, replacing 54 of 60.
- **The product name is SAiCoN in prose**, interfaces and documentation. Chain
  identifiers are untouched and stay lowercase: `saicon-devnet-1`, the bech32
  prefixes, the binary and the module routes are case-sensitive, and a
  "correction" there yields a valid-looking address belonging to nobody.

### Fixed

- **An unknown `NODE_PROFILE` no longer becomes a validator.** A typo such as
  `ful` used to fall through to the default and run a different node than the
  operator asked for.

## v0.5.0 — 2026-08-24

The chain's own state now survives leaving the chain. `x/participation` and
`x/rewards` exported their params and nothing else, so an export-and-restart —
a coordinated upgrade, a state migration, a genesis rebuild — erased every score
snapshot, every accumulated empirical point and the whole payment record. Since
a validator earns only while `ACTIVE`, compliant and `EP > 0`, the entire set
would have come back unable to earn, and nothing would have reported why.

The simulation suite found it, on its first real run. That suite had reported
green for months without executing anything, because `go test` exits 0 when a
test skips.

### ⚠️ Upgrading — read before deploying

The genesis format changed again, in the same shape as v0.3.0: a genesis written
by an older build still starts, with the new fields arriving empty, but a chain
that has been *running* loses the state those fields carry when it is exported.

- **`x/participation` and `x/rewards` genesis each gained repeated fields** —
  eight collections between them that previously had no genesis representation.
- **`last_status_change_height` is normalised on every write.** It was stored
  raw at creation while `InitGenesis` clamped it, so the field diverged across a
  round trip. A validator created during `InitChain` no longer stores height 0,
  which cannot be told apart from "never recorded" and made the unjail cooldown
  pass immediately. Records written by an older build keep their raw values.

### Added

- **Genesis representation for participation and reward state** — score
  snapshots, empirical points, signed and missed block counts, reward snapshots,
  the carried remainder and observed gas fees. Signed and missed counts are two
  separate lists on purpose: the chain writes exactly one side per block, so
  merging them invented the missing side on export and created it on import.
- **ADR-0013 — bounded DAO authority.** What governance may tune, and what it
  may never touch.
- **ADR-0014 — entity registry.** Attestation before a validator's first
  activation, the evidence ladder, and the appeal path.
- **A read and wallet integration specification** for an application building
  against the chain, in English and Russian: what the deployed node serves, which
  endpoints are standard Cosmos and which are specific to SAiCoN.
- **The unbonding period is pinned in both genesis paths** at 1,814,400 s. It had
  been the SDK default that happened to agree, while four scripts described it in
  comments as a decision of ours. The three-node stand also gained the evidence
  window it had never carried: it ran CometBFT's stock 100,000 blocks / 48 h,
  which does not cover a 21-day unbonding period.

### Fixed

- **A skipped simulation counted as a passing one.** `TestAppStateDeterminism`
  was skipping on an empty validator set while `make test-sim` exited 0 — the one
  check that catches consensus-breaking non-determinism, silently absent from a
  release gate. Every target now runs through a guard that fails on a skip. The
  seed was the cause: `SIM_SEED` is 42, which is exactly the SDK's default seed
  value, and the determinism test reads that as an instruction to draw a random
  one. It has its own pinned seed now.
- **`last_status_change_height` diverged across an export/import round trip**
  (see *Upgrading*).

---

## v0.4.0 — 2026-08-13

Two kinds of node run on one chain: a validator that signs, and an archive that
keeps every byte of history and answers for block 1. Each carries its own
retention policy and adopts it by restarting — no state reset, no migration.
Alongside them, two history queries the per-validator dashboard is built on.

### ⚠️ Upgrading — read before deploying

- **`x/mint` is removed from the application entirely** — not zeroed in a
  template, gone. `x/rewards` is now the only module holding minting rights for
  `nscn`, so the 21,000,000 ceiling guards every path that can create supply.
  This is **state-breaking**: a running chain cannot be brought up over existing
  state, and the DevNet restart needs a fresh genesis.
- **The active set is pinned to 60** in both genesis paths — Register v1.0's
  number, and not a settled one: the review round prefers 75, pending a consensus
  benchmark. `max_validators` is a governance-editable `x/staking` param, so
  changing it later needs no restart.
- **Retention profiles.** `NODE_PROFILE` selects `validator` or `archive`; the
  policy is applied on every start rather than only at first init.

### Added

- **Two node profiles, validator and archive**, exercised together on a
  four-node bench: three validators and one archive on one chain, 35 automated
  checks, no failures. The archive joins as an ordinary node with no stake and no
  seat, never signs, and keeps pace block for block.
- **`SnapshotRange`** — participation snapshots over a range of epochs in one
  query.
- **`RewardSummary`** — a validator's emissions summed over an epoch range, with
  coverage reported so a partial answer cannot read as a complete one.
- **An evidence-window checker and its governance fix**, and a node resource
  sampler used to re-derive the published hardware requirements from measurement
  rather than extrapolation.

### Fixed

- **Emission was denominated in `scn` where the chain's base unit is `nscn`** —
  a factor of 10⁸ between what the module intended to pay and what it paid.
- **An eligible epoch that produced no amount was recorded as a denial**, which
  made a validator that earned nothing indistinguishable from one that was
  refused.
- **A malformed or mixed-denomination reward amount produced a silently short
  total.** It now fails closed.
- **Loop-counter overflow at the `MaxUint64` boundary** in participation and
  rewards, and a width-guard overflow in `SnapshotRange` with its fencepost
  pinned.

---

## v0.3.0 — 2026-07-29

The validator lifecycle is complete: a validator's own stake is locked for 180
days, leaving takes another 21, and both are enforced by the chain rather than
promised by a document. Alongside it, the first public Validator Handbook.

This release also fixes three defects found by adversarial review of work that
had already shipped — one of them a security hole, one a permanent brick, one a
silent loss of enforcement on restart. They are listed under *Fixed* with what
they actually allowed, not softened.

### ⚠️ Upgrading — read before deploying

This is a **consensus-breaking** release and the genesis format changed. There
is no upgrade handler; a running chain cannot be migrated in place.

- **A fresh genesis is required.** `x/validatorregistry` and `x/compliance`
  genesis each gained a repeated field. A genesis written by an older build
  still starts (the fields arrive empty), but a chain that has been *running*
  under the old rules cannot continue under these.
- **The genesis must fund less than 21,000,000 SCN.** The shipped default
  previously funded far more than that; a genesis above the ceiling leaves no
  headroom and pays no rewards at all. The templates now fund 10,000,000 SCN.
- **Slashing parameters changed.** Ordinary downtime now carries **no stake
  slash** (it is punished through reward loss and jail instead); double-signing
  doubles to **10%**. Both are set in the genesis templates.
- **Module order changed.** `x/rewards` now runs first in `BeginBlock`. It reads
  the fee collector and changes nothing downstream.
- Runbook for the DevNet.

### Added

- **V-5 — validator exit lifecycle and the 21-day unstake cooldown.** The exit
  path was previously unwired end to end: nothing wrote `unbonding_start`,
  nothing moved a validator to `UNBONDING` or `INACTIVE`, and both staking hooks
  were empty. Leaving the bonded set now records the exit and moves
  `ACTIVE`/`QUARANTINED` → `UNBONDING`; removal by `x/staking` completes it to
  `INACTIVE`. The cooldown is the chain's own 21-day unbonding period — SAiCoN
  defines no second timer, so there is exactly one clock and it cannot be
  shortened. The completion instant is **frozen** when unbonding begins, so a
  later governance change of the staking parameter cannot retroactively
  invalidate an exit in flight. Backed by a fail-loud EndBlock invariant
  (`UnstakeCooldownInvariant`) that halts rather than let a validator record an
  exit early; a record with nothing to verify is skipped rather than halted on.
- **Recovery after a downtime jail.** An ordinary outage is not an exit:
  repairing the node and re-bonding resumes service, with the aborted exit
  discarded. Re-bonding never washes off a sanction — a quarantined validator
  returns quarantined, a jailed one stays jailed until a governed unjail passes.
- **Validator metadata and compliance state now survive genesis export/import.**
- **Public Validator Handbook** on the roadmap site: what the chain actually
  measures, minimum and recommended hardware, the three-key model and the
  permanent tombstone, exit rules, and an honest section on what is specified
  but not yet running.
- **Per-validator dashboard** (on the project website) — status, compliance flag,
  Empirical Points, the lock-up countdown, the exit cooldown and a per-epoch
  table of participation and reward snapshots, read live from the chain's own
  queries with no indexer in between.

### Fixed

- **SECURITY: governance `MsgClearFlag` bypassed the unjail gates.** Both
  `MsgUnjail` and `MsgClearFlag` restore `C=1` and share the same authority
  gate, but only `MsgUnjail` enforced the safety checks. A governance proposal
  calling `ClearFlag` on a `JAILED` validator restored compliance and flipped it
  back to `ACTIVE` with **neither the cooldown nor the tombstone check
  evaluated** — reinstating even a tombstoned double-signer that `MsgUnjail`
  bars permanently, and contradicting the rule that governance may never reset a
  tombstone. `ClearFlag` now refuses a jailed validator
  (`ErrJailedRequiresUnjail`, 1108), leaving `MsgUnjail` the single audited way
  out of jail.
- **An ordinary downtime jail permanently bricked a validator.** A downtime
  slash is far below the double-sign threshold, so the registry kept reporting
  the validator `ACTIVE` while `x/staking` dropped it from the bonded set —
  which moved the registry to `UNBONDING`. Re-bonding did nothing, and the
  record stayed `UNBONDING` forever: no participation score, no Empirical
  Points, no rewards, and the governed unjail could not reach it either. Fixed
  by the recovery path above.
- **A routine genesis export/import silently erased the registry.** Validator
  metadata was not part of genesis, so a standard `export` → edit → restart
  wiped every record. Participation and rewards then saw nothing for any
  validator and **nobody ever earned again** — and, worse, the 180-day
  self-stake lock-up stopped being enforced, because the ante decorator reads
  the same metadata and a missing record reads as "not locked". A restart voided
  every commitment. Compliance state had the same gap: a sanctioned validator
  came back reward-eligible.
- **The shipped genesis funded far more than the maximum supply**, which left no
  headroom and denied every reward. A test now reads the values out of the
  genesis templates so the two cannot drift apart again.
- **Re-creating a validator at an address the registry already knew** either
  produced a permanently-inactive ghost or silently discarded a live
  registration — including a frozen custom lock-up and an active sanction.
  Creation now discards only a completed exit, and a preserved record has its
  lock-up clock restarted.

### Changed

- **The validator status table gained four edges** (12 → 16): `UNBONDING` back
  to `ACTIVE`/`QUARANTINED` for an aborted exit, and `ACTIVE`/`QUARANTINED` to
  `INACTIVE` because removal by `x/staking` is ground truth — a record that
  cannot record an exit becomes a ghost.
- **`MsgRegisterValidator` rejects a lock-up shorter than the chain minimum**
  (`ErrLockupTooShort`). The message is permissionless and the duration was
  signer-supplied with no floor.
- **Downtime no longer burns stake; double-signing burns 10%.** With downtime at
  zero, the compliance hook's inference became exact rather than heuristic: any
  positive burn is a double-sign, because `x/staking` never invokes the hook on
  a zero burn.

### Verified

- Full unit suite green across all modules; `go vet` and `go build` clean.
- The project's end-to-end PoP script (`chain/scripts/demo-pop-cycle.sh`) runs
  green against a dockerized node: register → auto-activate → earn → flag →
  reward denied, with balances asserted at each step.
- Live single-node runs reaching block production with zero panics and the three
  fail-loud invariants active every block.
- Every fix in this release was reverted and re-run to confirm its test fails
  without it.

### Previously unreleased in this line

### Added
- **Validator unjail path — governed `MsgUnjail`.** Closes the "no unjail path" gap: the
  `JAILED → ACTIVE` transition (already legal in the V-1 table) now has a dedicated,
  authority-gated (`x/gov` account) `MsgUnjail` in `x/compliance`. It restores `C=1` and moves the
  validator to `ACTIVE` via the existing `SetCompliance(true, RESTORED)` choke point, gated by: a
  **block-based cooldown** (`now − last_status_change_height ≥ Params.unjail_cooldown_blocks`, new
  gov-tunable param defaulting to 43200 ≈ 1 day) and an **x/slashing tombstone check** — a
  double-signer is tombstoned and can never be unjailed (`ErrValidatorTombstoned`), matching Cosmos
  semantics. So the meaningful case is a *governance*-jailed validator (manual critical-violation,
  no tombstone). Participation-layer only (re-opens the reward gate; consensus is unaffected). EP is
  untouched (lazy-decay). Errors: `ErrNotJailed` / `ErrUnjailCooldown` / `ErrValidatorTombstoned` /
  `ErrValidatorNotFound`. CLI `saicond tx compliance unjail [validator-address] [reason]`; emits a
  `validator_unjailed` event.
- **V-3 — validator self-stake lock-up enforcement.** The 180-day lock-up
  (`bonding_start` + `lockup_duration_seconds`, tracked since M1-A) now actually gates exit.
  A new ante decorator (`x/validatorregistry/ante`) rejects a validator withdrawing its **own**
  self-bonded stake — both `MsgUndelegate` and `MsgBeginRedelegate` where `delegator == validator`
  — while the window is open, returning `ErrLockupActive` (code 1104). It recurses into
  `authz.MsgExec` and **fails closed** past a nesting cap so a wrapped message cannot bypass it.
  Wired via `app/ante.go` after the SDK auth chain (metered, authenticated txs only). Backed by a
  fail-loud **EndBlock invariant V-3**: for every still-locked validator it panics (chain halt) if a
  self-unbonding or self-redelegation **created while locked** is in flight — defense-in-depth for
  any future non-ante dispatch. The invariant is **creation-height-aware** (new
  `ValidatorMetadata.bonding_start_height`): a self-exit initiated *before* the lock began (e.g. a
  validator that self-undelegated while unbonded, then bonded) is exempt, so a legitimate lingering
  exit never halts the chain. Lock-up is purely time-based and status-independent (a jailed
  validator is still locked). Enforced via per-tx ante + EndBlock, mirroring V-1/V-2.
- **M1-D — observability: event catalogue + read-surface completion.** Five typed events
  (string-const + `sdk.NewEvent`, repo convention), emitted after state persistence:
  `participation_score_computed` {validator, epoch_id, blocks_signed, blocks_missed, uptime,
  score, ep_after} — per bonded validator per epoch, in `ComputeEpoch`; `reward_minted`
  {validator, epoch_id, amount} / `reward_denied` {validator, epoch_id, reason} — in
  `DistributeEpochRewards`, reason ∈ `"not active"` / `"non-compliant"` / `"EP=0"`;
  `compliance_flagged` / `compliance_cleared` {validator, event, reason} — from `SetCompliance`,
  the single choke point covering both `MsgSubmitEvidence` and the double-sign auto-jail hook.
- **Two new queries closing the read-surface gaps:** `compliance Compliance(validator)` →
  `ComplianceState` (CLI `saicond q compliance compliance [validator-address]`, REST
  `/saicon/chain/compliance/v1/compliance/{validator_address}`); `rewards
  RewardSnapshot(validator, epoch_id)` → `RewardSnapshot` (CLI `saicond q rewards snapshot
  [validator-address] [epoch-id]`, REST `/saicon/chain/rewards/v1/snapshot/{validator_address}/{epoch_id}`).
- **Entrypoint env knobs (single-node, demo/test only):** `EPOCH_LENGTH_BLOCKS` and
  `GOV_VOTING_PERIOD`, applied as jq genesis patches on first init only; invalid values fail
  loud before genesis is written (a masked failure would otherwise silently boot on default
  params on container restart). Passthrough wired in `docker-compose.yml`.
- **`chain/scripts/demo-pop-cycle.sh`** — asserting end-to-end PoP cycle demo: register → auto-
  activate → earn (score/EP/reward) → governed flag (real gov proposal carrying
  `MsgSubmitEvidence`, voted, passed) → reward = 0 (eligible=false, balance unchanged). Closes
  the "negative-path live demo pending" gap. Verified green across implementer + reviewer runs.

### Notes
- protojson omits `false` boolean fields entirely (no `"eligible":false` key) — the demo script
  treats a missing key the same as an explicit `false`.
- The post-flag denial reason is `"not active"`, not `"non-compliant"`: the compliance flag also
  quarantines the validator, and the rewards gate checks `status == ACTIVE` before `compliant`,
  so quarantine trips the first gate.

## v0.2.4 — 2026-06-12

### Added
- **M1-C — parameter-driven EP decay.** Empirical Points now decay multiplicatively toward a
  floor of 1 over idle epochs (active-with-zero-score and gaps after leaving the bonded set,
  charged lazily on return), governed by the `ep_decay` param. Default `ep_decay = 0` (decay off)
  until calibrated (Q4). `ep_decay` is validated to be in `[0,1)`.

### Changed
- With `ep_decay = 0` (default), an active validator that scores 0 no longer loses a flat 1 EP —
  decay is now fully parameter-driven (the previous hardcoded `-1` ignored the param).

## v0.2.3 — 2026-06-12

The Proof-of-Participation cycle is **live end-to-end**. This is the **v0.2 line in
progress** — hardening continues toward the full "PoP Complete & Hardened" v0.2.

### Added
- **M1-A — automatic activation on bond.** `AfterValidatorBonded` staking hook activates
  validators; genesis validators are auto-registered + activated (ADR-0004).
- **M1-B — Participation Score v1.** Per-block precommit tally from the consensus
  last-commit → epoch-window uptime as an extensible weighted sum; per-epoch counter reset.

### Verified
- 10-epoch localnet run: EP 0 → 9, ~9.1M `scn` emitted strictly through the gate
  (`ACTIVE & C=1 & EP>0`), 0 panics, 0 invariant violations.

### Still ahead in the v0.2 line (not in this tag)
- M1-C EP-decay · M1-D observability + negative-path demo · lock-up enforcement (V-3)
  · governed unjail · full invariant suite V-1…V-9 · reward economics v1 🔒.

## v0.1.0 — 2026-06-01 (baseline)
- Runnable BFT PoS L1: blocks produced, `scn` denom, four PoP modules scaffolded,
  Docker single- + 3-validator testnet, CI, one-command release bundle.
