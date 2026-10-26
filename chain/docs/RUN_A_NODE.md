# Run an SAiCoN node

Operator guide for the **SAiCoN Chain** (binary `saicond`, denom `nscn` — 1 SCN = 100,000,000 nscn, bech32 prefix `saicon`, Cosmos SDK v0.53.6 / CometBFT).

Three supported ways to run:
- **Pre-built release archive** (easiest — `docker load` + `docker compose up`, no source build).
- **Docker from source** (nothing but Docker required): single node or a local 3-validator testnet.
- **From source** (Go + Ignite): for development.

**Joining an existing network** (the DevNet) as a node or a validator: [MULTINODE-JOIN.md](MULTINODE-JOIN.md).

See also: [chain/README.md](../README.md) (build, modules, smoke tests) · [DEVELOPMENT.md](DEVELOPMENT.md) (state, model, evidence) · [ADR-0002 version pin](adr/ADR-0002-cosmos-sdk-tooling-version-pin.md).

> **Status:** Runnable testnet with **Proof-of-Participation active**. Blocks are produced by
> stock CometBFT + Cosmos `x/staking`; on top, `x/participation` computes a participation
> score + Empirical Points each epoch and `x/rewards` gates emission on
> `status==ACTIVE AND C==1 AND EP>0`. Activation on bond is automatic (staking hook). PS is
> single-signal and reward economics are a mock pending the Register build-out — current
> state and plan: [DEVELOPMENT.md](DEVELOPMENT.md) §1–§2.

---

## Prerequisites

**Docker path (recommended):**
- Docker Engine with the `docker compose` plugin (Compose v2).
- Open host ports: `26657` (RPC), `1317` (REST) for single node; `26657`/`26658`/`26659` (RPC) for the 3-validator testnet.

**Source path (dev):**
- Go **1.25** — `go.mod` declares `go 1.25.10` and that is authoritative (`brew install go`).
- Ignite CLI **v29.10.0** — only needed for the `ignite chain serve` dev loop (`brew install ignite`).

All paths below assume you are in the repo root (`saicon/`). Where a command is run from inside `chain/`, that is called out.

---

## Run from the pre-built release archive (easiest)

If you have the `saicon-node-v0.1.0.tar.gz` release bundle, you need **only Docker** — no Go,
no source, no image build:

```bash
tar -xzf saicon-node-v0.1.0.tar.gz
cd saicon-node-v0.1.0
docker load -i saicon-node-v0.1.0.image.tar.gz   # load the pre-built image
docker compose up -d                              # start the node
docker compose logs -f                            # watch blocks
```

The bundle contains the image tarball, a `docker-compose.yml`, and its own `README.md`. Verify
and stop it exactly as in the single-node section below (same ports, same chain ID
`saicon-localnet-1`). To build this bundle yourself from source, see
[Build the release archive](#build-the-release-archive).

---

## Run a single node (Docker)

```bash
docker compose -f chain/docker-compose.yml up -d
```

This builds the `saicond:dev` image, initializes genesis on first run (creates a `validator` key in the `test` keyring, a gentx, enables the REST API, binds RPC/REST to `0.0.0.0`), and starts the node with `--minimum-gas-prices 0nscn`. Home inside the container is `/home/saicon/.saicon` (persisted in the `saicon-data` volume). Chain ID: `saicon-localnet-1`.

Host ports: **RPC `26657`**, **REST `1317`**.

### Verify it is producing blocks

```bash
# Liveness — returns {} when healthy
curl -s localhost:26657/health

# Height should be increasing; run twice with a few seconds gap
curl -s localhost:26657/status | grep latest_block_height

# REST API sanity (bank module)
curl -s localhost:1317/cosmos/bank/v1beta1/supply
```

You can also follow logs:

```bash
docker compose -f chain/docker-compose.yml logs -f
```

### Stop / wipe

```bash
docker compose -f chain/docker-compose.yml down -v   # -v removes the data volume (fresh genesis next time)
```

---

## Run a 3-validator testnet (Docker)

```bash
docker compose -f chain/docker-compose.testnet.yml up -d --build
```

How it works:
- `genesis-init` is a one-shot service (runs as root) that builds a **shared** 3-validator genesis, a common `test` keyring, and `peers.txt` on the `testnet-data` volume, then exits.
- `node0`, `node1`, `node2` wait for `genesis-init` to complete (`service_completed_successfully`), then start and peer up via docker service-name DNS using the persistent-peers list.
- Chain ID: `saicon-testnet-1`. Each node runs with `--minimum-gas-prices 0nscn`.

Host RPC ports: **node0 → `26657`**, **node1 → `26658`**, **node2 → `26659`** (all map to container `26657`).

### Verify the network

```bash
# 3 validators in the active set
curl -s localhost:26657/validators | grep total       # expect "total": "3"

# Each node is connected to the other two
curl -s localhost:26657/net_info   | grep n_peers      # expect "n_peers": "2"

# Same chain on a different node
curl -s localhost:26658/status | grep latest_block_height
curl -s localhost:26659/status | grep latest_block_height
```

### Stop / wipe

```bash
docker compose -f chain/docker-compose.testnet.yml down -v
```

> Re-running `up` without `down -v` reuses the existing genesis + peers (the generator is idempotent). Use `down -v` to start a fresh network.

---

## Build from source

### Install the binary

```bash
cd chain
go build ./...
go install ./cmd/saicond            # → ~/go/bin/saicond
export PATH="$HOME/go/bin:$PATH"
```

### Build the Docker image directly

```bash
docker build -t saicond:dev -f chain/Dockerfile chain/
```

### Build the release archive

To regenerate the distributable `saicon-node-v0.1.0.tar.gz` bundle (pre-built image + compose
+ README) for users who only have Docker:

```bash
# from repo root (saicon/)
docker build --build-arg VERSION=v0.1.0 -t saicon-node:v0.1.0 chain/

mkdir -p dist/saicon-node-v0.1.0
cp chain/docker/bundle/docker-compose.yml dist/saicon-node-v0.1.0/
cp chain/docker/bundle/README.md          dist/saicon-node-v0.1.0/
docker save saicon-node:v0.1.0 | gzip > dist/saicon-node-v0.1.0/saicon-node-v0.1.0.image.tar.gz

tar -C dist -czf dist/saicon-node-v0.1.0.tar.gz saicon-node-v0.1.0/
```

The `dist/` directory is git-ignored (build artifact). The bundle template — its
`docker-compose.yml` and `README.md` — lives under version control at
[`chain/docker/bundle/`](../docker/bundle/), so the archive can always be rebuilt.

### Dev loop with Ignite

```bash
cd chain
ignite chain serve --reset-once
```

Ignite compiles, starts a single-validator localnet, and creates the test accounts `alice` and `bob`. Blocks start in ~5–10 seconds. Chain ID for this path is `chain`.

> **Home directory:** `ignite chain serve` uses `~/.chain/` (legacy from the scaffold name). When you run CLI commands against this localnet, pass `--home ~/.chain --keyring-backend test`. Example:
>
> ```bash
> saicond query bank total-supply --home ~/.chain
> ```

For the full smoke-test command set (bank transfer, `x/validatorregistry` onboarding), see [chain/README.md → Smoke test](../README.md#smoke-test-ignite-serve-path---home-chain-keyring-test).

---

## Common issues

- **Node halts immediately on start / "minimum-gas-prices" error.**
  `minimum-gas-prices` is empty by default in the app config, so the node must be started with the flag. All entrypoints already pass `--minimum-gas-prices 0nscn`; if you start `saicond start` by hand, add it:
  ```bash
  saicond start --home <home> --minimum-gas-prices 0nscn
  ```

- **`--home` confusion between Ignite and Docker.**
  - `ignite chain serve` → home is `~/.chain` (on your host). CLI queries: `--home ~/.chain`.
  - Docker single-node → home is `/home/saicon/.saicon` *inside the container*. Do **not** pass `--home ~/.chain` from your host. Query over RPC instead: `curl localhost:26657/...` or `saicond query ... --node tcp://localhost:26657`.
  - Docker testnet → each node's home is `/data/nodeN` inside its container; query via the published RPC ports (`26657`/`26658`/`26659`).

- **Ports already in use (26657 / 1317 / 26658 / 26659).**
  Another node (or a previous run) is bound. Stop it (`docker compose ... down`) or remap ports in the compose file. The single-node and testnet composes both publish `26657` — don't run both at once without changing one.

- **First `up` is slow.**
  The image is built from source (`golang:1.25-alpine`) on first run; subsequent `up` reuses the cached image. Add `--build` only when you changed chain code.

- **Stale state after a code change.**
  Run `down -v` to drop the data volume so a fresh genesis is initialized on the next `up`.
