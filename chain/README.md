# SAiCoN Chain

> L1 network on Cosmos SDK / CometBFT with the **Proof of Participation** mechanism.
>
> Part of the [SAiCoN project](../README.md) (`saicon/chain/`).

---

## Status

**A running developer network.** Six application modules beside the stock Cosmos SDK ones:
the validator lifecycle with its lock-up and exit cooldown, the compliance ladder, participation
scoring, the security-budget emission under the 21M ceiling, the stake limits with their queue,
and the delegation router. Voting power is still stake-weighted (stock `x/staking` + CometBFT);
participation-based selection is the next layer. Where each part stands, and the known
limitations, are in [docs/DEVELOPMENT.md](docs/DEVELOPMENT.md) §1.

---

## Quick Start

The fastest path is Docker (nothing else to install). For development you need Go 1.25 + Ignite. Full operator guide: **[docs/RUN_A_NODE.md](docs/RUN_A_NODE.md)**.

### Run with Docker

**Single-node localnet** (RPC 26657, REST 1317):

```bash
docker compose -f docker-compose.yml up -d
```

The `saicond:dev` image is built, genesis is initialized on first start (validator key, gentx, REST enabled), and the node starts with `--minimum-gas-prices 0scn`. Blocks start flowing after ~10-20 seconds.

```bash
curl -s localhost:26657/health                       # {} = OK
curl -s localhost:26657/status | grep latest_block_height
docker compose -f docker-compose.yml down -v          # stop + wipe
```

> The commands above are relative to `chain/`. From the repository root, add the path prefix: `docker compose -f chain/docker-compose.yml ...`.

**3-validator testnet** (node0 → RPC 26657, node1 → 26658, node2 → 26659):

```bash
docker compose -f docker-compose.testnet.yml up -d --build
```

`genesis-init` (one-shot) builds the shared 3-validator genesis + `peers.txt`, then `node0/node1/node2` start and peer with each other via docker service-name DNS. Check:

```bash
curl -s localhost:26657/validators | grep total       # "total": "3"
curl -s localhost:26657/net_info   | grep n_peers      # "n_peers": "2"
docker compose -f docker-compose.testnet.yml down -v   # stop + wipe
```

### Prerequisites (from source / dev)

- macOS / Linux
- Go 1.25 (see `go.mod` `go 1.25.10` — authoritative) (`brew install go`)
- Ignite CLI v29.10.0 (`brew install ignite`) — only for the `ignite chain serve` dev loop

### Run localnet via Ignite (dev)

```bash
cd chain
ignite chain serve --reset-once
```

Ignite builds everything needed, brings up a single-validator localnet, and creates test accounts (`alice`, `bob`). Blocks start being produced after ~5-10 seconds.

> **Home directory note:** `ignite chain serve` uses the home `~/.chain/` (legacy from the scaffold name) — add `--home ~/.chain --keyring-backend test` to CLI commands. Docker, by contrast, uses an in-container home (`/home/saicon/.saicon` for single-node, `/data/nodeN` for the testnet) — for Docker nodes do not pass `--home ~/.chain`; queries go over RPC via `curl` or `saicond query ... --node tcp://localhost:26657`.

### Build binary standalone

```bash
go build ./...
go install ./cmd/saicond          # → ~/go/bin/saicond
```

Or build the Docker image directly:

```bash
docker build -t saicond:dev -f Dockerfile .
```

### Smoke test (Ignite serve path, `--home ~/.chain`, keyring `test`)

The commands below assume a running `ignite chain serve` (home `~/.chain`, accounts `alice`/`bob`, chain-id `chain`).

```bash
export PATH="$HOME/go/bin:$PATH"

# Total supply
saicond query bank total-supply --home ~/.chain

# Account addresses
ALICE=$(saicond keys show alice -a --keyring-backend test --home ~/.chain)
BOB=$(saicond keys show bob -a --keyring-backend test --home ~/.chain)

# Balances
saicond query bank balances "$ALICE" --home ~/.chain
saicond query bank balances "$BOB" --home ~/.chain

# Test transfer
saicond tx bank send "$ALICE" "$BOB" 100scn \
  --keyring-backend test \
  --home ~/.chain \
  --chain-id chain \
  --gas auto --gas-adjustment 1.5 --fees 200scn \
  --yes
```

**x/validatorregistry smoke (onboarding flow):**

```bash
# Register alice as a managed validator (V-1: must be a real staking validator first)
saicond tx validatorregistry register-validator \
  --from alice --keyring-backend test --home ~/.chain \
  --chain-id chain --gas auto --gas-adjustment 1.5 --fees 300scn --yes

# Query a single validator by valoper address
saicond query validatorregistry validator <valoper-addr> --home ~/.chain

# List validators filtered by status
saicond query validatorregistry list-validators --status-filter bonding --home ~/.chain
```

---

## Configuration

### Chain identity

| Param | Value | Configurable in |
|---|---|---|
| Native denom | `scn` (display `SCN`) | `config.yml` |
| Address prefix | `saicon` (bech32) | `app/app.go` (`AccountAddressPrefix`) |
| Binary name | `saicond` | `cmd/saicond/` directory + `Name = "saicon"` in `app/app.go` |
| Module path | `github.com/saicon-network/saicon/chain` | `go.mod` |

### Genesis accounts (dev only)

`config.yml` creates:
- `alice` — 200_000_000 scn + 20_000 token, bonded 100_000_000 scn as validator
- `bob` — 100_000_000 scn + 10_000 token (also faucet)
- Synthetic validators `validator1` (200M scn), `validator2` (100M scn)

⚠️ **These are dev keys in the `test` keyring** — NEVER use them in prod.

---

## Architecture

See the full spec:
- 📘 [DEVELOPMENT.md](docs/DEVELOPMENT.md) — the development guide: current state (modules, invariants, known limitations), formulas/parameters (Register v1.0), algorithms, evidence
- 📋 [ADR-0001](docs/adr/ADR-0001-use-cosmos-sdk.md) — why Cosmos SDK
- 📋 [ADR-0002](docs/adr/ADR-0002-cosmos-sdk-tooling-version-pin.md) — version pins (SDK v0.53.6, Ignite v29.10.0, Go 1.25.10)

### Module overview

```
Standard Cosmos modules (code unmodified; slashing params overridden per ADR-0010):
  x/auth · x/bank · x/staking · x/slashing
  x/distribution · x/gov · x/params · x/genutil
  x/epochs · x/crisis · x/upgrade
  (x/mint REMOVED from the app — x/rewards is the only module that may mint)

SAiCoN custom modules (implemented):
  x/validatorregistry  — onboarding, Status model, lock-up, exit lifecycle
                         invariants V-1 (transitions), V-3 (lock-up), V-5 (cooldown)
  x/compliance         — C flag, evidence msgs, auto-quarantine/jail, double-sign hook,
                         governed unjail (cooldown + tombstone gate)
  x/participation      — PS/EP computation per epoch, EP decay, invariant V-7
  x/rewards            — security-budget emission under the 21M cap, gas-fee offset,
                         eligibility gate ACTIVE ∧ C=1 ∧ EP>0 (invariant V-2)
  x/stakeengine        — entry floor, global and per-validator caps, stake queue
  x/delegationrouter   — routed delegation: the remainder past a validator's cap goes
                         to up to two more validators with room
```

---

## Version Pin (ADR-0002)

| Component | Version |
|---|---|
| Cosmos SDK | v0.53.6 (Eden++) |
| CometBFT | shipped with SDK v0.53 |
| Ignite CLI | v29.10.0 |
| Go | 1.25.10 (per `go.mod`, authoritative) |

Upgrade procedure — see [ADR-0002 §Future upgrade](docs/adr/ADR-0002-cosmos-sdk-tooling-version-pin.md).

---

## CI

GitHub Actions workflows live in the repo-root `.github/workflows/`:

| Workflow | Trigger | What it does |
|---|---|---|
| `test.yml` | push / PR | `go build ./...`, `go test ./...`, `go vet ./...` (working-directory `chain`, `go-version-file: chain/go.mod`) |
| `lint.yml` | push / PR | `golangci-lint` v2.12.2 on `chain/` and `indexer/`, blocking |
| `docker.yml` | push to `main` / tags `v*` | build + push the image to `ghcr.io/<repository owner>/saicon` |

`setup-go` takes the version from `chain/go.mod` (`go-version-file`), so the CI toolchain always matches the module (1.25.10).

---

## Testing

Unit tests:
```bash
go test ./...
```

Integration tests (with running localnet):
```bash
# See chain/testutil/ for helpers
```

Invariant tests (Cosmos SDK invariant registry) — implemented together with the custom modules.

---

## Project layout

```
chain/
├── README.md                       ← this file
├── .gitignore                      ← chain-specific (ignite build noise)
├── Makefile                        ← go build / install / proto-gen
├── go.mod, go.sum                  ← Go module github.com/saicon-network/saicon/chain (go 1.25.10)
├── config.yml                      ← Ignite localnet config (SCN denom, accounts, validators)
├── buf.lock, buf.yaml              ← proto tooling config
├── Dockerfile                      ← multi-stage build (golang:1.25-alpine → alpine:3.20)
├── docker-compose.yml              ← single-node localnet
├── docker-compose.testnet.yml      ← 3-validator testnet
├── docker/                         ← container scripts
│   ├── entrypoint-singlenode.sh    ← single-node init + start
│   └── testnet/                    ← gen-genesis.sh, entrypoint-node.sh
├── app/                            ← Cosmos SDK app (app.go, app_config.go, etc.)
├── cmd/saicond/                     ← binary entrypoint
├── x/                              ← custom modules (validatorregistry, compliance, participation)
├── proto/                          ← .proto files
├── testutil/                       ← test helpers
└── docs/                           ← architectural docs
    ├── RUN_A_NODE.md               ← operator guide (Docker + source)
    ├── validators/                 ← Validator System Spec
    ├── architecture/               ← module map, app.go layering
    ├── protocol/                   ← PoP spec (TBD)
    ├── simulations/                ← parametric calibration (TBD)
    ├── adr/                        ← ADR-0001, ADR-0002, ADR-0003
    ├── docs.go                     ← OpenAPI serving (Ignite scaffold)
    ├── static/                     ← OpenAPI static assets
    └── template/                   ← OpenAPI templates
```

---

## License

Apache License 2.0. See [LICENSE](../LICENSE) and [NOTICE](../NOTICE).
