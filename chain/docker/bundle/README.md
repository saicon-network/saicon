# SAiCoN Node — Pre-built Release (v0.1.0)

Run a single-node SAiCoN localnet **without compiling anything**. The only requirement is
Docker with the Compose v2 plugin. This bundle ships a pre-built image, so there is no Go
toolchain and no source build involved.

SAiCoN (**S**ustainable **AI** **Co**mpute **N**etwork) is a Cosmos SDK v0.53.6 / CometBFT
L1 with a Proof-of-Participation layer. Binary `saicond`, native denom `scn`, bech32 prefix
`saicon`.

## Contents

| File | Purpose |
|---|---|
| `saicon-node-v0.1.0.image.tar.gz` | the pre-built Docker image (`saicon-node:v0.1.0`) |
| `docker-compose.yml` | single-node localnet definition (uses the image, no build) |
| `README.md` | this file |

## Requirements

- Docker Engine with `docker compose` (Compose v2).
- Free host ports **26657** (RPC) and **1317** (REST).
- ~400 MB free disk for the loaded image.

## Run it (3 steps)

```bash
# 1. Load the pre-built image into Docker
docker load -i saicon-node-v0.1.0.image.tar.gz

# 2. Start the node (detached)
docker compose up -d

# 3. Watch it produce blocks
docker compose logs -f
```

On first start the container initializes genesis (a `validator` key in the `test` keyring,
a gentx, REST enabled, RPC/REST bound to `0.0.0.0`) and starts with
`--minimum-gas-prices 0scn`. Chain ID: `saicon-localnet-1`. Blocks begin within ~10–20 s.

## Verify

```bash
# Liveness — returns {} when healthy
curl -s localhost:26657/health

# Block height — run twice a few seconds apart; it should increase
curl -s localhost:26657/status | grep latest_block_height

# REST sanity (total supply, denominated in scn)
curl -s localhost:1317/cosmos/bank/v1beta1/supply
```

Run CLI commands inside the container:

```bash
docker exec -it saicon-node saicond status
docker exec -it saicon-node saicond query validatorregistry params
docker exec -it saicon-node saicond query participation params
docker exec -it saicon-node saicond query rewards params
```

## Stop / reset

```bash
docker compose down       # stop, keep chain data
docker compose down -v     # stop and wipe the data volume (fresh genesis next start)
```

## Notes & limits (v0.1)

- This is a **single-validator localnet** for evaluation, not a production network.
- PoP is active: `x/participation` computes a participation score + Empirical Points each
  epoch, and `x/rewards` gates emission on `status==ACTIVE AND C==1 AND EP>0`.
- Reward amounts are a **mock**, param-gated by `enabled` — final tokenomics are not set.
- There is no `BONDING → ACTIVE` activation path yet, so a freshly registered validator
  stays in `BONDING`; the positive-reward path is demonstrable once activation lands.
- For a 3-validator network, building from source, and the full operator guide, see
  `chain/docs/RUN_A_NODE.md` in the source repository.
- Architecture & current state: `chain/docs/DEVELOPMENT.md` §1.
