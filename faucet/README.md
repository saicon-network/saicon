# SAiCoN devnet faucet

A small Go service that gives out test SCN for fees and for trying transfers and
delegation. It does not hand out validator stake (the 2,000 SCN entry floor), and faucet tokens alone cannot activate a validator: activation also needs the entity's attestation.

- `POST /request` with `{"address":"saicon1..."}` returns `{"txhash":"...","amount":"10000000000nscn","status":"included"}`
- `GET /status` returns faucet address, balance, per-request amount, remaining daily budget, chain id, and the rules the page shows: `daily_budget` (base units, decimal string) and `window_seconds` (the per-address and per-network cooldown, integer seconds)
- `GET /healthz`
- `GET /` is a one-input page that works on a phone, with no external scripts or fonts

Errors are JSON `{"error":"<code>","message":"..."}`: `invalid_address` (400; the message is a plain instruction, the parser's reason goes to the log only),
`address_limited` / `ip_limited` (429, with `retry_after_seconds`),
`budget_exhausted` (503), `send_failed` (502).

## Design: shell out to `saicond`

The faucet signs by running `saicond tx bank send ... --keyring-backend test`
(approach a). Reasons:

- The module stays tiny: standard library plus `modernc.org/sqlite`. Signing in
  process would pull in the Cosmos SDK, protobuf codegen and gRPC, hundreds of
  transitive modules, for one message type.
- It uses the exact signing, account-number and sequence logic the chain's own
  CLI uses, so it cannot drift from the node's rules.
- The image carries a `saicond` copied out of the running node container before the
  build (`faucet/saicond-bin/`, git-ignored): the binary
  Cosmovisor actually runs (`cosmovisor/current/bin/saicond`) if there is one,
  else the container's own `/usr/bin/saicond`. Taken from the container, not from
  an image tag, so a re-pointed tag cannot change which one. Why the running
  binary: after a governance upgrade it is newer than the image's. A bank send
  needs only codec compatibility, but the CLI also decodes the account it queries
  and builds and signs the tx, so using the chain's current binary avoids a
  mismatch the first time an upgrade touches those types. Nothing is compiled from
  the repository.

Costs, accepted: one process spawn per payout, and sends are serialised (one at a
time) until the transaction is seen in a block, otherwise the CLI would read the
same committed account sequence twice. Throughput is about one payout per block,
far above what a daily limit allows.

Time budget of one request, kept under Cloudflare's 100 s proxy timeout:
queue wait `FAUCET_QUEUE_TIMEOUT` 30 s (then it is refused and keeps its
allowance), then one `saicond` run `FAUCET_RUN_TIMEOUT` 20 s, then the wait for
inclusion `FAUCET_TX_WAIT` 20 s (then `status: "pending"` with the hash), plus 5 s
of slack: at most 75 s. A send that has started is never cut short by the queue
deadline. On SIGTERM the faucet waits 80 s for requests in flight, and compose
sets `stop_grace_period: 90s` to match.

Address validation is a small in-tree bech32 decoder (checksum, `saicon` prefix,
20 or 32 byte payload). `saiconvaloper...` and other prefixes are rejected.

## Limits

- One payout per address and one per client IP in each window (default 24 h).
- A global budget over a rolling 24 h (default 5,000 SCN). When it is spent the
  API answers 503 `budget_exhausted`.
- The faucet can never pay more than `FAUCET_AMOUNT` per request.
- Every payout is written to SQLite (`$FAUCET_DATA_DIR/faucet.db`) BEFORE it is
  sent. A clear failure (the CLI gave up before broadcasting, the chain refused
  the tx, the request waited too long for its turn) deletes the row, so it costs
  the user nothing. Any other failure is ambiguous and keeps the row, answering
  502 `send_unconfirmed`: refusing a retry is cheaper than paying twice. Only
  positive proof of "never broadcast" counts as clear: saicond printed a refusal
  as JSON (CheckTx code != 0), or its error is one that can only occur before
  broadcast (key not found, invalid address, insufficient funds or fee, sequence
  mismatch), or the request never got its turn. A connection error with empty
  output is ambiguous, because it can happen after the node accepted the tx. Limits survive restarts as long as the data volume does.
- `POST /request` must be `Content-Type: application/json` and, when a browser
  sends `Sec-Fetch-Site` or `Origin`, it must come from the faucet page itself
  (`FAUCET_PUBLIC_ORIGIN`, or the request's own host if unset). Otherwise any web
  page could spend its visitors' allowance. The faucet sends no CORS headers.
- Client IP: `X-Forwarded-For`'s first entry is used only when the TCP peer is in
  `FAUCET_TRUSTED_PROXIES`; otherwise the peer address. IPv6 clients are limited
  per /48 (the allocation of one site or tunnel-broker account).
- `GET /status` queries the node at most once per 10 s.

### Client IP behind Cloudflare and Caddy

The faucet trusts Caddy and nothing else. In the reverse proxy's configuration the global
`servers` block lists Cloudflare's edge ranges as `trusted_proxies` and sets
`client_ip_headers CF-Connecting-IP`, so Caddy takes the visitor address from the
header Cloudflare sets, and only for connections that really come from
Cloudflare. The explorer vhost's `/faucet-api/` route then overwrites `X-Forwarded-For` with that single
address (`header_up X-Forwarded-For {client_ip}`). Without the overwrite Caddy
would append to a client-supplied header and the faucet would key on a value the
client typed. A client that reaches the origin directly is not in the trusted
list, so its headers are ignored and its TCP address is used. The explorer's record
can be proxied like the others. The range list was copied from cloudflare.com/ips-v4
and /ips-v6 on 2026-11-20; refresh it when Cloudflare changes it.

## Configuration (environment)

Amounts are in the base denom: 1 SCN = 100,000,000 nscn (display exponent 8).

| Variable | Default | Meaning |
|---|---|---|
| `FAUCET_LISTEN` | `:8080` | listen address |
| `FAUCET_RPC_URL` | `http://saicon-node:26657` | CometBFT RPC (broadcast, tx lookup) |
| `FAUCET_REST_URL` | `http://saicon-node:1317` | REST (balance) |
| `FAUCET_CHAIN_ID` | `saicon-devnet-1` | chain id |
| `FAUCET_DENOM` | `nscn` | base denom |
| `FAUCET_DENOM_EXPONENT` | `8` | display exponent, used for defaults and the page |
| `FAUCET_BECH32_PREFIX` | `saicon` | account prefix |
| `FAUCET_AMOUNT` | `10000000000` (100 SCN) | per request; equals the DevNet minimum delegation, so one request is enough to try delegating |
| `FAUCET_DAILY_BUDGET` | `500000000000` (5,000 SCN) | rolling 24 h total (50 requests) |
| `FAUCET_WINDOW` | `24h` | per-address and per-IP cooldown |
| `FAUCET_FEES` | `0nscn` | tx fee (devnet node accepts zero) |
| `FAUCET_GAS` | `200000` | gas limit |
| `FAUCET_TX_WAIT` | `20s` | how long to wait for inclusion; after that the tx hash is returned with `status: "pending"` |
| `FAUCET_QUEUE_TIMEOUT` | `30s` | how long a request may wait for its turn to send |
| `FAUCET_RUN_TIMEOUT` | `20s` | cap on one `saicond tx` run |
| `FAUCET_DATA_DIR` | `/data` | SQLite file and default keyring location |
| `FAUCET_KEYRING_DIR` | `$FAUCET_DATA_DIR/keyring` | saicond home holding `keyring-test/` |
| `FAUCET_KEY_NAME` | `faucet` | key name in that keyring |
| `FAUCET_MNEMONIC_FILE` | unset | path to a mnemonic FILE, imported once if the key is missing. Never a mnemonic in an env var |
| `FAUCET_SAICOND_BIN` | `saicond` | binary to run |
| `FAUCET_TRUSTED_PROXIES` | empty | comma-separated CIDRs allowed to set `X-Forwarded-For` |
| `FAUCET_PUBLIC_ORIGIN` | empty | exact Origin of the page, e.g. `https://explorer.saicon.world`; empty accepts only an Origin with the request's own host |
| `FAUCET_EXPLORER_TX_URL` | `https://explorer.saicon.world/saicon/tx/` | link prefix on the page |

The keyring uses the `test` backend: the key sits unencrypted on the faucet's
data volume. That is acceptable for a devnet key that only holds test tokens and
must not be reused anywhere else.

## Funding the faucet key

Generate a key on the operator's machine (commands only; keep the output private):

```sh
saicond keys add faucet --keyring-backend test --home /tmp/faucet-home   # prints a mnemonic once
saicond keys show faucet -a --keyring-backend test --home /tmp/faucet-home
```

Write the mnemonic to a file on the host and point `FAUCET_MNEMONIC_FILE` at it. The container
runs as uid 10001 and a bind mount keeps the host file's owner and mode, so the file must belong
to that uid with mode 400. On first start the faucet imports it through stdin into its own keyring.
Then send test SCN to the printed address from a funded account, for example:

```sh
saicond tx bank send <funded-key> <faucet-address> 20000000000000nscn \
  --chain-id saicon-devnet-1 --node https://rpc.saicon.world --fees 0nscn --yes
```

Check it with `curl https://explorer.saicon.world/faucet-api/status`.

## How this deployment exposes it

The faucet has no domain of its own. Its page is the explorer's **Faucet** tab
(`https://explorer.saicon.world/saicon/faucet`), which fills in
the connected wallet's address and reads the rules from `/status`. The explorer's Caddy vhost proxies
`/faucet-api/status` and `/faucet-api/request` to `faucet:8080` with the prefix stripped; every other
`/faucet-api/` path answers 404, so `/` and `/healthz` are not public. Caddy passes `Host` through, so
the Origin check accepts the explorer page with `FAUCET_PUBLIC_ORIGIN` left empty. The page served at
`/` by the faucet itself stays for a standalone run.

## Deploy

Run it as a container next to a node: build the image from the repository root
(`docker build -f faucet/Dockerfile .`, with a `saicond` placed in `faucet/saicond-bin/` as the
Dockerfile describes), mount a data volume at `/data` and the mnemonic file read-only, and set the
`FAUCET_*` variables above (for compose, in the service's `environment:` or an `.env` file). Put a
TLS-terminating reverse proxy in front and list it in `FAUCET_TRUSTED_PROXIES`. A value set in an
`.env` file overrides the built-in default, so remove old `FAUCET_AMOUNT` / `FAUCET_DAILY_BUDGET`
lines when the defaults should apply. Check `/status` after starting.

After a chain reset the faucet's SQLite record and key describe a chain that no longer exists:
wipe its data volume, fund its address on the new chain and start it again.

## Tests

```sh
cd faucet && go build ./... && go vet ./... && go test ./...
```

## Getting help

Problems with the faucet, a node or a validator: open an issue at
https://github.com/saicon-network/saicon/issues/new/choose (pick *Node operator question*).
Never paste a mnemonic or `priv_validator_key.json`.
