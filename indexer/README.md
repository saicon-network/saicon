# SAiCoN indexer

Serves in one request what the chain answers one validator at a time, and counts
the one thing the chain cannot answer about itself: which validator proposed
which block.

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

## Tests

```bash
go test ./...                                              # unit
INDEXER_TEST_REST=http://localhost:1317 go test ./...      # + contract and agreement
```

The agreement test is the one that matters: it asserts the scoreboard equals a
direct per-validator read from the chain. Running is not the same as agreeing.
