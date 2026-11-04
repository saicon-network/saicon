# SAiCoN indexer

Serves in one request what the chain answers one validator at a time, and counts
the one thing the chain cannot answer about itself: which validator proposed which
block. Since S8 it also derives one family of numbers no chain query computes — the
concentration measures — from inputs it stores and serves beside them.

## Why it exists

The four SAiCoN modules expose participation, rewards, registry standing and
compliance **per address**. There is no query for the whole set, so a scoreboard
over 75 validators costs roughly 300 requests per refresh — against the node that
is also signing blocks.

`proposer_address` is in every block header and in no query:
`/cosmos/base/tendermint/v1beta1/blocks` without a height returns 501, and
CometBFT's `block_search` indexes emitted events, not header fields. Counting
proposals requires walking blocks.

## Which node to point it at

**Not a validator.** ADR-0015 keeps continuous query load off signing nodes. Point
`INDEXER_RPC_URL` and `INDEXER_REST_URL` at a `full` node. The single-node devnet
is the one exception, and only because it has no other node.

## Retention, and what a pruned node costs you

The follower has to read every block header to count proposals, so it can only
count what the node still has. On a fresh database it starts at the node's
earliest retained height and logs that it did; proposals below that height will
never exist in that database, because they cannot be gathered from that node at
all. Everything else — scores, rewards, registry standing, compliance — is read
from current state and is unaffected.

If the proposal history matters, point it at a node that retains from genesis.

## Configuration

| Variable | Default | Meaning |
|---|---|---|
| `INDEXER_DB` | `/data/indexer.db` | SQLite file. Put it on a volume, or every restart re-indexes from the node's earliest retained block |
| `INDEXER_RPC_URL` | `http://localhost:26657` | CometBFT RPC |
| `INDEXER_REST_URL` | `http://localhost:1317` | Cosmos REST |
| `INDEXER_LISTEN` | `:8080` | HTTP listen address |
| `INDEXER_POLL_SECONDS` | `5` | How often to walk toward the tip |

## Endpoints

| Path | Returns |
|---|---|
| `GET /v1/scoreboard` | every validator, ordered by score |
| `GET /v1/validators/{operator}` | one validator |
| `GET /v1/validators/{operator}/epochs?from=&to=` | that validator's epoch series |
| `GET /v1/concentration?epoch=` | the newest epoch whose power was read (or the given one): top-10, Nakamoto, the validators with their units, the two concentration curves |
| `GET /v1/concentration/epochs?from=&to=` | the per-epoch measurements, oldest first, at most 1000, unavailable epochs included with their codes |
| `GET /v1/entities?epoch=` | the entity view of the newest epoch with both parts read (or the given one): every attested entity, its members, and the charged frozen seats of revoked ones |
| `GET /v1/status` | `chain_id`, `indexed_height`, `tip_height`, `lag_blocks` |

Amounts are strings in base units (`nscn`); convert once, at display. A field the
chain did not answer is `null`, never `0` — a measured zero and "no record" are
different facts.

`/v1/status` answers 503 while it cannot reach the chain, so a monitor can watch the
status code and not only the body. `lag_blocks` is signed: a negative value means the
chain is behind the index, which a chain cannot be, and the service stops following
and says so.

**One process per database.** The write path advances a counter and a cursor in one
transaction and is safe against a crash, not against a second writer — two indexers
sharing one `INDEXER_DB` would each silently drop the blocks the other won. Run one.

`epoch_length_blocks` is read at startup and re-checked each pass. It is a governance
parameter, and a change renumbers the chain's epochs from that height on while
everything already indexed keeps the old bucketing — which nothing in the data would
reveal afterwards. The service stops rather than serving the mixture.

A same-chain-id reset is caught by a second check, run each pass before the follower.
The newest stored epoch row that has a block hash is compared with the hash the node holds
at that height; a different hash means the chain under this chain id is not the one this
database holds (a devnet reset that kept the chain id and grew past the old height, which
the follower's tip-below-cursor check cannot see). The service then logs `STOPPING`, stops
following and measuring, keeps serving what it has, and `/v1/status` answers 503 with the
reason. A height the node has pruned or not reached skips only the check; a failed read
skips the check and holds the concentration pass back for that tick. Start a new database.

## Concentration

Once per closed epoch `e` (epoch length `L`) the service reads the chain at the epoch's
last block `H = e·L` and stores the measures together with the inputs they come from, so
every figure can be re-derived from what is served. The pass trails the follower, so `H`
is a block already walked; it measures oldest first, and a failed epoch is retried on the
next tick, never skipped.

**The pass is bounded by requests.** The follower, the S6 epoch reader and this pass share
one loop, so whatever the pass costs is what the follower waits for each tick. One epoch
costs 7 requests, plus one per listed entity (every entity the registry lists, revoked ones
included, so this term grows with the registry's history and not with the 75 seats), one
per attested entity and one per registry-ACTIVE validator that is not a member of an
attested entity (a validator set over 100 adds a page per 100): about 9 on the devnet, 172
at 75 validators, 75 attested entities and 15 validators outside them. The budget is 200
requests per pass, counted on clients the pass alone uses. An epoch is measured in one
piece, so the pass starts the next epoch only while the requests spent plus the last
epoch's cost still fit, and the first epoch always runs, so an epoch that alone costs more
is still measured, one per pass. The budget is therefore a target, not a hard bound: a
pass costs at most about max(200, one epoch) for new epochs, and as much again for the
re-read below.

**What is read, and where.** From CometBFT at `H`: the validator set with voting powers
(`/validators?height=H`, paged) and the block hash (`/commit?height=H`). From the REST
gateway at `H`: x/staking validators (consensus key to operator), the validator registry
(status), and the entity layer: `stakeengine caps` (entity cap), `entityregistry params`
(`max_active_seats`), the entity list, each entity's members and seat count, each
attested entity's stake (`tokens`, `entity_cap`, `room`, seats, `frozen`, `reason`,
stored verbatim), and, for each registry-ACTIVE validator that is not a member of an
attested entity, its entity record, the only place a frozen seat is visible.

**Reads are pinned, and the header is required.** Every REST read carries
`x-cosmos-block-height: H`, and a 200 without a matching `X-Cosmos-Block-Height` response
header is an error. A backfilled epoch must describe that epoch, and the dozen reads of
one epoch must describe one block; a node or proxy that ignored the request header would
otherwise hand back current state labelled as history. A gRPC NotFound answer carries no
height header on the plain channel and stays "no record".

**Formulas.** Voting power is CometBFT's `voting_power` of the set at `H`, `T` their sum,
`N` the set size, `p_(1) ≥ p_(2) ≥ …` the powers ordered descending, ties by consensus
address ascending.

- Top-10 power: the sum of the `min(10, N)` largest powers; the share is that over `T`.
- Nakamoto, validators: the smallest `k` with `3·Σ_{i≤k} p_(i) ≥ T`. Holding exactly a
  third already counts.
- Unit of a validator: `entity:<id>` if it is a member of attested entity `id` at `H`, or
  holds a frozen seat of a revoked entity `id` while its registry status is
  `STATUS_ACTIVE` (the seat is still charged to it); otherwise `val:<operator>`, or
  `cons:<ADDR>` when x/staking has no validator with that consensus key.
- Nakamoto, entities: the same rule over unit powers `P_u = Σ_{v∈u} p_v`, units ordered by
  `P_u` descending, ties by unit key ascending.
- Concentration curves: `c_k = Σ_{i≤k} p_(i) / T` over validators, and the same over
  units, ranked largest first.
- Shares are 6-decimal strings, truncated, never rounded: `0.349999` is not `0.35`. The
  35% reference line and the 36.2% stress-test mark are constants of the page, not
  measurements, and are not served. Exactly 35% is at the line, not above it.

An entity row is listed for every ATTESTED entity, and for a REVOKED one only while the
chain still counts a seat against it (then with its chain figures `null` — the stake
engine has no record for an entity without members — and the charged seats listed).

**Unavailable is a fact, not a number.** Each epoch has two independent parts, power and
entities. A part that could not be read carries a code; its figures are `null`.

| Code | Part | Meaning |
|---|---|---|
| `pruned` | power, entities | the node no longer holds height `H` (CometBFT: `is not available, lowest height is`; SDK: `failed to load state at height`). If the app state is gone but the validator set is not, power is still read and only entities carry `pruned` |
| `before_entity_layer` | entities | `H` is below the height the entity stores came into existence |
| `no_entity_layer` | entities | the chain runs no x/entityregistry |
| `frozen_count_mismatch: <entity>: chain <a>, found <b>` | entities | the scan for charged frozen seats found a different number than the chain's own seat count for that entity; the epoch keeps its validator-level measures |

The classification is by message, never by status code: the gateway answers a pruned and
a future height alike with HTTP 500, and the SDK's pruned-height error is registered with
gRPC code Unknown, so the "no record" rule (404 and code 5) must never absorb it.

**The entity floor.** The chain's multistore serves an *empty* store for a module added
by a later upgrade. Read below the upgrade height, the entity list would come back empty
and `entity_cap` as `"0"` — numbers that look measured. So the service finds the first
height with the entity stores: `module_versions?module_name=entityregistry` (an HTTP 500
with exactly "QueryModuleVersions module entityregistry not found", or an empty list,
means no entity layer at all) and then `applied_plan/v0.10.0` (height `U`; `0` for a chain
started with the module, whose stores exist from height 1). Epochs with `H < U` are
written `before_entity_layer` without asking the chain.

The floor is resolved lazily. It is read at startup and again at the start of every
concentration pass until the chain reports the layer; from then on it is cached. Any
other failure of that read leaves the layer unknown: it is logged, the follower and the
S6 epoch reader carry on, and only that tick's concentration pass is skipped, so a
transient fault is never taken for an absent module and never stops the service. An
absent layer is an answer, and epochs are written `no_entity_layer` as before.

When the layer appears while the service runs (the chain upgraded under it), every stored
epoch whose last height is at or after `U` and whose entity part says `no_entity_layer` is
read again. New epochs come first in every pass; the re-read spends what is left of the
budget, and at least one epoch per pass when nothing is left, so neither can starve the
other. It walks the epochs in order through a cursor that wraps at the end, resuming where
the last pass stopped, until none is left. An epoch whose re-read fails is logged and
waits ten passes, and the walk moves on to the next one, so an epoch that keeps failing is
retried after the others and never blocks them or new epochs. Three failures in a row end
the re-read for that pass, since a failed read can cost a whole HTTP timeout, and a shutdown
ends it at once. The re-read goes through the normal write path, one transaction
per epoch, so a failed rewrite keeps the old rows. A height the node has pruned since
keeps its power figures and only its entity code changes, to `pruned`. Epochs ending
before `U` keep their rows.

Rebuilding and restarting the indexer keeps its database. The existing database gains the concentration tables, empty, on the first
start, and the first passes measure every walked epoch. Deployed after the chain upgrade,
the floor is known from the first pass and the epochs ending before `U` read
`before_entity_layer`. Deployed before it, those epochs read `no_entity_layer`, which was
true of the chain when they were measured, and the epochs from `U` on are read again
once the layer appears. Either way every epoch from `U` on carries its entity figures.

**Retention.** The per-epoch measurements (`concentration_epochs`) are kept for good. The
detail rows — per-validator powers, entity rows and seats — are kept for the newest 2000
epochs; a row older than that serves its measurements and no validators or curve. On a
fresh database against a long-running chain the backfill can only measure what the node
still holds: app state for the pruning window, validator sets for longer.

## Tests

```bash
go test ./...                                              # unit
INDEXER_TEST_REST=http://localhost:1317 go test ./...      # + contract and agreement
```

The agreement test is the one that matters: it asserts the scoreboard equals a
direct per-validator read from the chain. Running is not the same as agreeing.

The concentration tests need a chain with the entity layer, with `INDEXER_TEST_RPC` as
well as `INDEXER_TEST_REST`. `./scripts/localnet.sh` builds `saicond`, starts a native
single-node chain (10-block epochs, app state pruned to the last 50 heights) on 26657 and
1317 and runs the entity demo on it; no Docker:

```bash
bash indexer/scripts/localnet.sh start     # leave it ~2 minutes: the pruned-height test needs a tip past 120
(cd indexer && INDEXER_TEST_RPC=http://127.0.0.1:26657 INDEXER_TEST_REST=http://127.0.0.1:1317 \
  go test ./... -run 'TestConcentrationAgreesWithChain|TestPrunedHeightIsUnavailable|TestAgreesWithChain|TestRESTContract' -v)
bash indexer/scripts/localnet.sh stop
```

That run lands after the window described below, so it reads a one-validator epoch and the
revoked `demo-third` is not in it. To check the charged-seat case itself, read an early
epoch of the same chain: one closed before the second and third validators are jailed
(height below about 110: epochs 5 to 10) and still held by the node
(app state is kept for 50 heights, so the tip must be between 102 and about 150). Make the
test fail if that epoch does not carry the seat:

```bash
(cd indexer && INDEXER_TEST_EPOCH=10 INDEXER_TEST_EXPECT_CHARGED=1 \
  INDEXER_TEST_RPC=http://127.0.0.1:26657 INDEXER_TEST_REST=http://127.0.0.1:1317 \
  go test ./internal/concentration/ -run TestConcentrationAgreesWithChain -v)
```

`INDEXER_TEST_EPOCH` names a closed epoch to check instead of the newest;
`INDEXER_TEST_EXPECT_CHARGED=1` turns "the revoked entity's charged seat is checked when
present" into "it must be present".

`TestConcentrationAgreesWithChain` reads the newest closed epoch and compares what is
stored with what the node says at that height, asked directly: set size and total power, each member's power, top-10 and Nakamoto, every stored field of each
attested entity against its stake record, and each validator's unit against its entity
record. The localnet's second and third validators run no node, so they are jailed for
liveness around height 110 and the set shrinks from three to one; the revoked
`demo-third` and its charged seat are in the picture only for the epochs closed before
that. The test follows the chain's own seat count either way.
