# SAiCoN

An L1 blockchain built on Cosmos SDK v0.53 and CometBFT, with a **Proof of Participation**
mechanism: validators earn rewards for what they do for the network (taking part in consensus,
staying compliant, being reliable), not only for the stake behind them.

- Binary `saicond` · base denom `nscn` (display `SCN`, 1 SCN = 100,000,000 nscn) · bech32 prefix `saicon`
- Supply ceiling 21,000,000 SCN; issuance goes through a single path (`x/rewards`)
- Custom modules: `x/validatorregistry`, `x/compliance`, `x/participation`, `x/rewards`,
  `x/stakeengine`, `x/delegationrouter`
- Companion service: an indexer (the scoreboard API)

## Status

**v0.9.1 is a DevNet release.** The Proof of Participation cycle runs end to end: scores and
Empirical Points are computed and paid every epoch, and six invariants are checked every block and
halt the chain if one is violated. Delegation routes itself: a delegator names a validator and,
when that validator is at its cap, the rest of the stake is spread over other well-participating
validators that have room. Each delegator may hold stake with at most three validators.

What is **not** done yet: consensus voting power is still stock stake-weighted, the entity
registry is ahead, the economic parameters are a DevNet baseline, and rewards are paid in DevNet
coins only. This is not a production network and nothing on it has monetary value. The honest list of
gaps is in [chain/docs/DEVELOPMENT.md](chain/docs/DEVELOPMENT.md), section 1.4, and the release
history is in [chain/CHANGELOG.md](chain/CHANGELOG.md).

## Quick start

You need Docker with the Compose plugin. A single local node, using the public image:

```bash
docker run -d --name saicon -p 26657:26657 -p 1317:1317 \
  --entrypoint /usr/local/bin/entrypoint-singlenode.sh \
  ghcr.io/saicon-network/saicon:v0.9.1
```

The entrypoint creates a fresh single-validator chain on first start; `docker rm -f saicon`
removes it.

or build from source (this is what the compose file does):

```bash
git clone https://github.com/saicon-network/saicon.git
cd saicon
docker compose -f chain/docker-compose.yml up -d
```

Blocks start in 10 to 20 seconds. Host ports: RPC 26657, REST 1317.

```bash
curl -s localhost:26657/health                         # {} = OK
curl -s localhost:26657/status | grep latest_block_height
docker compose -f chain/docker-compose.yml down -v      # stop and wipe
```

A local three-validator network: `docker compose -f chain/docker-compose.testnet.yml up -d --build`.
Building the binary without Docker needs Go 1.25: `cd chain && go build ./cmd/saicond`.
The full guide is [chain/docs/RUN_A_NODE.md](chain/docs/RUN_A_NODE.md); hardware guidance is in
[chain/docs/node-requirements.md](chain/docs/node-requirements.md).

## Join the DevNet

[chain/docs/MULTINODE-JOIN.md](chain/docs/MULTINODE-JOIN.md) walks through joining the running
DevNet from an empty node: fetch the genesis, sync, get tokens, register a validator. Its
rehearsal script, `chain/scripts/rehearse-join.sh`, does the whole journey locally in about two
minutes, so you can see it work before you touch a real network.

## Where to read

| You want to | Read |
|---|---|
| Understand the chain, its formulas and open gaps | [chain/docs/DEVELOPMENT.md](chain/docs/DEVELOPMENT.md) |
| See why it is built the way it is | [Architecture decision records](chain/docs/adr/) |
| Build an application or wallet against it | [chain/docs/integration/READ_AND_WALLET_API.md](chain/docs/integration/READ_AND_WALLET_API.md) |
| Read the papers | [docs/papers/](docs/papers/README.md): whitepaper and litepaper (English and Chinese) |
| Work on the code | [chain/README.md](chain/README.md) and [CONTRIBUTING.md](CONTRIBUTING.md) |
| Run the demos | `chain/scripts/demo-*.sh` (each narrates what it does) |

## Repository layout

```
.
├── chain/            the node (Go): app wiring, custom modules in x/, protobuf, Docker, scripts
│   ├── cmd/saicond/  binary entrypoint
│   ├── app/          Cosmos SDK app wiring
│   ├── x/            custom modules
│   ├── proto/        .proto definitions
│   ├── scripts/      demos and operator tools
│   └── docs/         development guide, operator guides, ADRs, integration API
├── indexer/          off-chain indexer: the scoreboard API (Go)
├── docs/papers/      white paper and lite paper
└── .github/          CI, container image and release workflows
```

## Contributing and security

Read [CONTRIBUTING.md](CONTRIBUTING.md). This repository is a release mirror: changes are
reviewed and land through the main repository, then appear in the next release. To report a
vulnerability, follow [SECURITY.md](SECURITY.md); do not open a public issue for it. Everyone
taking part is expected to follow the [Code of Conduct](CODE_OF_CONDUCT.md).

## License

Apache License 2.0. See [LICENSE](LICENSE) and [NOTICE](NOTICE). The licenses of the
third-party code linked into `saicond` travel with every release archive (`third_party/`) and
with the node image (`/usr/share/licenses/saicon`); `chain/scripts/third-party-licenses`
produces them.
