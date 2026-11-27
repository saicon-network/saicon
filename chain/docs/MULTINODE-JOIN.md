# Joining a SAiCoN network

> Operator guide for adding a node — and then a validator — to a running SAiCoN network such as
> the `saicon-devnet-1` DevNet. Every step from §1 to §4 (the join itself) was run end to end on
> the current build by [`./scripts/rehearse-join.sh`](../scripts/rehearse-join.sh), which plays three
> separate operators on one machine; run it yourself before you touch a real network (§9). The
> state-sync start of §2 was run against the DevNet itself. The
> VRF steps of §5 (v0.10.0) are not part of that script: they were run by the upgrade rehearsal
> (`./scripts/rehearse-upgrade-v0.10.0.sh`) and the single-node demo (`./scripts/demo-randomness.sh`).
>
> Binary `saicond` · denom `nscn` (1 SCN = 100,000,000 nscn) · ports: P2P `26656`, RPC `26657`,
> REST `1317`, gRPC `9090`.

> **Which build a network runs decides which rules apply.** Read it first:
> `curl -s https://api.saicon.world/cosmos/base/tendermint/v1beta1/node_info | jq -r .application_version.version`.
> The DevNet runs **v0.10.1** under Cosmovisor (the v0.10.0 rules of S8, taken by an in-place
> upgrade from v0.9.1, with v0.10.1's packaging): the 2,000 SCN entry floor, the
> stake caps, the 60-day lock-up (longer on request), at most three validators per delegator and a
> 100 SCN minimum delegation all apply, and a validator activates only once its entity is attested
> (§0, §5). Join with the image built from the same commit the network runs (§2).
> On the DevNet `saicond version` and the node's `application_version.version` print a build id
> such as `dev-5460e7d3`, the build of the v0.10.1 code it runs; §2 names the image to pull.

---

## 0. Who does what

Joining is a hand-off between two people:

| The joining operator | Someone who already holds SCN (on the DevNet: the SAiCoN team) |
|---|---|
| starts an empty node, gives it the network's genesis and a peer, waits for it to sync | — |
| creates a key and sends its **address** | sends the operator at least **2,000 SCN** plus a little to spare |
| registers the validator (`create-validator`) and checks its status | *(from v0.10.0)* an **attestor** attests the validator's entity, which turns it from `STATUS_BONDING` to `STATUS_ACTIVE` |

Joining and bonding need no one's permission: there is no allow-list for running a node or for
registering a validator. A validator that brings at least 2,000 SCN of its own, within the network's
caps (§5), becomes active when it bonds on a v0.9.x network. **From v0.10.0 (S8)** the one approval
left is the attestation: a validator becomes active when it bonds *and* an attestor has attested the
entity it belongs to (§5). Until then it is bonded and signs blocks, but reads `STATUS_BONDING` and
earns nothing. Ask the network's attestor to attest your validator's address; it can do so before
you create the validator.

## 1. What you need from the network

Three things: the chain-id, a peer, and the genesis. The DevNet publishes all three in
[`chain/networks/saicon-devnet-1/`](../networks/saicon-devnet-1/) — `genesis.json`, its SHA-256 in
`genesis.json.sha256`, and `chain.json` (the network's entry in the Cosmos chain-registry format,
which wallets and explorers read: chain-id, peer, public endpoints, release binaries).

```bash
NET=https://raw.githubusercontent.com/saicon-network/saicon/main/chain/networks/saicon-devnet-1
RPC=https://rpc.saicon.world                       # the DevNet; any node's RPC works

curl -s $RPC/status | jq -r '.result.node_info.network'     # chain-id: saicon-devnet-1
curl -s $RPC/status | jq -r '.result.node_info.id'          # node id, for the peer address
curl -sO $NET/genesis.json && curl -sO $NET/genesis.json.sha256
sha256sum -c genesis.json.sha256                            # macOS: shasum -a 256 -c
```

`sha256sum -c` must print `genesis.json: OK`. The DevNet's genesis is
`6bb888ed3cc63498a812a967d09d456484e5959d1bae004262cd0a49c634dc61`; every operator who checks it
holds byte for byte the file the network started from. A file of any other checksum is a
different chain.

The peer address is `<node-id>@p2p.saicon.world:26656`. The node id and the genesis change only
when the DevNet restarts from a fresh genesis; the published files move with it, so read them
rather than copying values from old notes.

**The genesis must be the network's.** `saicond init` writes a genesis of its own; a node started
with it is a different chain and stops at once with *"validator set is empty after InitGenesis"*.

## 2. Start a node and let it sync

**Join from a snapshot, not from block 1.** The DevNet upgraded in place from v0.9.1 to v0.10.0
at height 262,701, and the current build cannot replay the blocks before it. A new node therefore
starts from a recent state-sync snapshot, which every DevNet node offers each 1,000 blocks, and
follows the chain from there. CometBFT checks the snapshot against a block hash you take from
the network (the *trust height* and *trust hash* below), so a peer cannot hand you a forged state.
For the same reason `saicond genesis validate-genesis` reports the published genesis as missing
the sections added in v0.10.0: the checksum is the check that matters, and the node starts from it.

```bash
RPC=https://rpc.saicon.world
LATEST=$(curl -s $RPC/block | jq -r .result.block.header.height)
TRUST_HEIGHT=$((LATEST - 2000))
TRUST_HASH=$(curl -s "$RPC/block?height=$TRUST_HEIGHT" | jq -r .result.block_id.hash)
# a command that turns state sync on in config.toml; run inside the node's config directory
STATESYNC="sed -i -e '/^\[statesync\]/,/^\[/{s#^enable = .*#enable = true#;s#^rpc_servers = .*#rpc_servers = \"$RPC:443,$RPC:443\"#;s#^trust_height = .*#trust_height = $TRUST_HEIGHT#;s#^trust_hash = .*#trust_hash = \"$TRUST_HASH\"#;s#^trust_period = .*#trust_period = \"168h0m0s\"#}' config.toml"
```

### With Docker (the published image)

The image runs as user `saicon` with its home at `/home/saicon/.saicon`, and has `jq` and `wget`
but **not `curl`** — query the node with `saicond` itself.

```bash
# a release image from ghcr.io — pick the release the network runs (see the note at the top)
TAG=v0.11.0
IMG=ghcr.io/saicon-network/saicon:$TAG
H=/home/saicon/.saicon
CHAIN_ID=saicon-devnet-1
PEER=<node-id>@p2p.saicon.world:26656
NET=https://raw.githubusercontent.com/saicon-network/saicon/main/chain/networks/saicon-devnet-1

# 1. an empty home in a named volume
docker run --rm -v saicon-mynode:$H $IMG init my-node --chain-id $CHAIN_ID --default-denom nscn --home $H

# 2. the network's genesis, checked against the published checksum, and state sync on
docker run --rm -v saicon-mynode:$H --entrypoint sh $IMG -c \
  "cd $H/config && wget -qO genesis.json $NET/genesis.json \
   && wget -qO- $NET/genesis.json.sha256 | sha256sum -c && $STATESYNC"

# 3. start — the gas-price flag is required (§8); add a profile's flags (below) here
docker run -d --name saicon-mynode -v saicon-mynode:$H -p 26656:26656 $IMG \
  start --home $H --minimum-gas-prices 0nscn --p2p.persistent_peers "$PEER"

# 4. once the log shows "Snapshot restored" and then "executed block", restart once (see below)
docker logs -f saicon-mynode 2>&1 | grep -m1 "executed block"
docker restart saicon-mynode
```

### From the binary

On Linux. On macOS the `sed -i` of `$STATESYNC` needs `sed -i ''` and `sha256sum -c` is
`shasum -a 256 -c`.

```bash
saicond init my-node --chain-id saicon-devnet-1 --default-denom nscn --home ~/.saicon
NET=https://raw.githubusercontent.com/saicon-network/saicon/main/chain/networks/saicon-devnet-1
(cd ~/.saicon/config && curl -sO $NET/genesis.json && curl -s $NET/genesis.json.sha256 | sha256sum -c \
  && sh -c "$STATESYNC")
saicond start --home ~/.saicon --minimum-gas-prices 0nscn \
  --p2p.persistent_peers "<node-id>@p2p.saicon.world:26656"
# after "Snapshot restored" and the first "executed block": stop it (Ctrl-C) and start it again
```

**Why the one restart.** Right after the snapshot is restored, the node asks its peers how far
the chain has got. With a single peer the answer can arrive late, and the node then decides it is
already at the top and follows new blocks one at a time instead of downloading the gap. Restarted
once, it downloads the gap at full speed: on the DevNet a node 1,000 blocks behind caught up in
under a minute. Restart only after the first `executed block`: a node stopped between the restore
and its first block refuses to start again (*"state and store height mismatch"*), and has to be
reset (`saicond comet unsafe-reset-all --home …`) and synced again.

### Choose what the node keeps

Three profiles, set by flags on `start` (or the same keys in `config/app.toml`). They differ only
in how much history the node stores; every one follows the chain and can serve as a peer.

| Profile | Keeps | For | Flags to add to `start` |
|---|---|---|---|
| **validator** | the last 100,000 states and the blocks of the evidence window (302,400) | signing blocks | `--pruning custom --pruning-keep-recent 100000 --pruning-interval 10 --min-retain-blocks 302400` |
| **full** | every block, the last 100,000 states | RPC and queries for recent state, an explorer's block and transaction pages | `--pruning custom --pruning-keep-recent 100000 --pruning-interval 10 --min-retain-blocks 0` |
| **archive** | everything | historical queries at any height, audits | `--pruning nothing --min-retain-blocks 0` |

Add `--state-sync.snapshot-interval 1000 --state-sync.snapshot-keep-recent 2` to any of them so
the node offers state-sync snapshots: a later operator can then join from a recent snapshot
instead of replaying the chain. The validator profile cannot drop blocks inside the evidence
window whatever `--min-retain-blocks` says: the node keeps what consensus safety needs first.
The Docker setups in this repository take the same choice as `NODE_PROFILE=validator|full|archive`.

### Check it has synced

```bash
docker exec saicon-mynode saicond status --home /home/saicon/.saicon \
  | jq '.sync_info | {height: .latest_block_height, catching_up}'
```

Done when `catching_up` is `false` and the height keeps climbing. A node that is not a validator
stops here: it follows the chain, serves queries, and adds a peer to the network.

## 3. A key, and tokens for it

```bash
docker exec -it saicon-mynode saicond keys add operator --home /home/saicon/.saicon
```

Inside the container the key is encrypted with a passphrase you choose here, and every
transaction asks for it again — so run `keys` and `tx` commands with `docker exec -it`. From here
on, the commands are written for the binary; in Docker prefix them with
`docker exec -it saicon-mynode` and add `--home /home/saicon/.saicon`.

Write the **mnemonic** down offline — it is the only way back to the key. Send the printed
`saicon1…` **address** (never the mnemonic) to whoever funds you. On the DevNet that is the SAiCoN
team; ask for 2,000 SCN plus about 100 SCN to spare.

The funder sends it with an ordinary transfer:

```bash
saicond tx bank send <funder-key> <your-address> 210000000000nscn \
  --chain-id saicon-devnet-1 --fees 0nscn
```

Check that it arrived — and check it by **the transaction**, not by the broadcast:

```bash
saicond q tx <txhash>                     # code 0 here means it ran
saicond q bank balances <your-address>
```

A broadcast that returns `code: 0` only means the node accepted the transaction into its mempool.
A transfer that then fails in the block — for example because the sender's spendable balance is
smaller than it looks, with most of it bonded — still printed `code: 0` at broadcast.

## 4. Become a validator

Write the validator description. The `pubkey` is your node's consensus key:

```bash
docker exec saicon-mynode saicond comet show-validator --home /home/saicon/.saicon
```

```json
{
  "pubkey": <the output above>,
  "amount": "200000000000nscn",
  "moniker": "my-node",
  "commission-rate": "0.05",
  "commission-max-rate": "0.20",
  "commission-max-change-rate": "0.01",
  "min-self-delegation": "1"
}
```

`amount` is your own stake — at least `200000000000nscn` (2,000 SCN). Then:

```bash
saicond tx staking create-validator validator.json --from operator \
  --chain-id saicon-devnet-1 --fees 0nscn --gas 600000
saicond q tx <txhash>                                       # code 0
saicond q validatorregistry validator <your-saiconvaloper…>  # v0.9.x: STATUS_ACTIVE; v0.10.0: STATUS_BONDING until attested, then STATUS_ACTIVE
```

`create-validator` with the 2,000 SCN self-bond uses **203,389 gas** on v0.10.0 (197,780 on v0.9.1;
both measured in the app test harness), so the 200,000 default runs out (`out of gas in location:
ReadFlat`). Send `--gas 600000`, as above and as the rehearsal scripts do, or `--gas auto
--gas-adjustment 1.5`. Fees are 0 on the DevNet, so the limit costs nothing.

Your validator address is `saicond keys show operator --bech val -a`. On v0.9.x the validator moves
to `STATUS_ACTIVE` on bond by itself, with `compliance_flag: true`; from v0.10.0 it does so once its
entity is attested (§5), and you can read where it stands with
`saicond q entityregistry validator-entity <saiconvaloper…>` (`attested`, `entity_id`, `pending`).
It starts earning once it is in the bonded set, `ACTIVE`, and has Empirical Points, which accrue over closed epochs — the reward gate is `ACTIVE ∧ C=1 ∧ EP>0`
([DEVELOPMENT.md](DEVELOPMENT.md) §2.5).

## 5. The rules your validator meets

| Rule | Value | What you see if you break it |
|---|---|---|
| Own stake to enter | ≥ 2,000 SCN, from your own account — delegations never count | `self-bond below the entry floor` (code 1103) |
| Room under the global cap | your stake ≤ the network's headroom | `global bonded cap reached` |
| Room under the per-validator cap | your stake ≤ the validator cap | `validator cap reached` |
| Lock-up | your own stake cannot be withdrawn for **60 days** after you bond | `validator self-stake is locked` |
| Longer lock-up *(v0.9.1)* | `register-validator` may commit to more than 60 days, up to **10 years** (315,360,000 s) | `requested self-stake lock-up is longer than the chain maximum` (code 1106) |
| Leaving | withdraw **all** of your own stake, or keep at least 2,000 SCN — nothing in between | `remaining self-bond would fall below the entry floor` |
| Attested entity *(v0.10.0)* | your validator is a member of an entity that an attestor has attested; no more than **2** of an entity's validators hold `ACTIVE` seats | the validator stays `STATUS_BONDING` (bonded in consensus, no score, no rewards) until attested or until a seat frees |
| Entity stake cap *(v0.10.0)* | the entity's validators together take at most 5% of the base the validator cap uses (floored by `entity_cap_floor`) | `entity aggregate stake cap reached` (code 1114); `validator takes no new stake while its entity standing is frozen` (code 1115) |
| Gas *(v0.10.0)* | `create-validator` uses 203,389 and a delegation into an 8-member entity 202,600, both over the 200,000 default — send `--gas 600000` or `--gas auto --gas-adjustment 1.5` | `out of gas in location: ReadFlat` |
| Fees | `--fees 0nscn` on the DevNet | — |
| Validators per delegator *(v0.9.0)* | an account delegates to at most **3** validators; your own validator does not count | `delegator already holds the maximum number of validators` (code 1112) |
| Minimum delegation *(v0.9.0)* | 100 SCN per delegation | `delegation below the minimum` (code 1113) |

The current caps and headroom:

```bash
curl -s https://api.saicon.world/saicon/chain/stakeengine/v1/caps | jq
```

The 60-day lock-up matters for testing too: stake you bond into a validator stays there for 60
days. Bond the minimum unless you mean it.

### The VRF key and the shadow draw *(v0.10.0)*

From v0.10.0 the chain runs per-validator VRF randomness in **shadow mode** (`x/randomness`): each
round, the entities in the pool submit a VRF output, the chain verifies it, chains a seed and
publishes a draw of 22 entities (or the whole pool, if smaller). **The draw awards no seats and
nothing reads it.** Not taking part costs nothing: no stake, status, Empirical Points, reward or
ticket depends on it, and a round without enough submissions is recorded as skipped. Taking part
is how an entity shows up in the evidence the network is collecting about the randomness, so the
steps are here. Your validator must already be a member of an attested entity (above).

1. **Make a key.** It is a separate VRF key, neither the consensus key nor the operator key.
   ```bash
   saicond vrf keygen --output ~/.saicon/config/vrf_key.json      # mode 0600, refuses to overwrite
   saicond vrf show --vrf-key-file ~/.saicon/config/vrf_key.json   # the 64-hex public key
   ```
   Back the file up like `priv_validator_key.json` and keep it out of git and chats. It is needed
   online once per rotation. **Never delete or regenerate it on a live network**: the chain keeps your
   old public key bound to your validator for good, so a new key means a new offer.
2. **Offer it.** The operator account signs; the proof of possession is made locally over the chain id
   and your validator address:
   ```bash
   saicond tx entityregistry offer-vrf-key --vrf-key-file ~/.saicon/config/vrf_key.json \
     --from operator --chain-id saicon-devnet-1 --fees 0nscn --gas auto
   ```
3. **Have it bound.** The attestor lists the key in the attestation of your entity (the same id, name,
   hash and validators, plus `--vrf-keys '{"validator":"saiconvaloper1…","vrf_public_key":"<64 hex>"}'`).
   The attestor can only bind a key you have offered. Check:
   `saicond q entityregistry vrf-key <saiconvaloper…>` shows the bound key and the height it was bound at.
4. **Run the submitter.** `./scripts/vrf-submit-loop.sh` (in the image as `/usr/local/bin/vrf-submit-loop.sh`)
   runs `saicond tx randomness submit-vrf` every 30 seconds and sends one transaction per round. Set
   `CHAIN_ID`; `SAICON_HOME`, `FROM`, `KEYRING`, `NODE`, `INTERVAL` and `FEES` have defaults. It must run
   durably, so that a container restart does not stop it.

What to expect:

- **A round is a rotation: `rotation_epochs` (7) participation epochs.** On the DevNet's 100-block
  epoch that is 700 blocks, with a 100-block window before each; "weekly" holds only at the production
  epoch. `saicond q randomness round` shows the current round, its window heights, its pool and the
  submissions so far; `saicond q randomness latest-draw` shows the last closed round.
- **A new key counts one to two rounds after it is bound** (it counts in round N only if it was bound
  before round N-1's window opened), and round 1 after the upgrade counts no key at all. Until then
  `submit-vrf` prints `not designated this round` and exits 0.
- **One validator per entity submits.** The entity's designated submitter is its ACTIVE member with the
  lowest operator address among those holding a counted key. The others print `not designated`.
- **Clients submit from the second block of the window.** The pool is snapshotted at the end of the
  block just before the window, and submissions are accepted from the window's first block, O. A
  transaction is simulated against the last committed block, so with `--gas auto` at latest height
  O-1 the simulation is refused; the first attempt that works is made once block O is the latest,
  and the transaction lands in O+1. The loop logs `submit-vrf failed` (the refusal says "the window
  opens at height O") and retries, so nothing needs doing. A proof that reaches a block at or after the rotation height is refused
  as late, and the round does not wait for it.
- **Gas** (measured): `offer-vrf-key` 60,671, `submit-vrf` 53,172, an attestation with one key 71,477
  and with eight keys 165,833; all under the 200,000 default.

What the draw does and does not tell you: it leaves a bounded residual and is not a guarantee. An entity that
submits last can see the others' outputs and choose between submitting and withholding; cooperating
entities multiply that choice; and the proposer of a window block can leave submissions out. The
VRF removes grinding; it does not claim that the draw cannot be influenced. On a network with one
validator the quorum of 2/3 of one is one, and the single output is all the entropy there is, so such a
network exercises the machinery and shows nothing about manipulation.
The residual is described in [ADR-0008](adr/ADR-0008-voting-power-and-selection.md) and
DEVELOPMENT.md §1.4. The key ceremony is rehearsed by `./scripts/demo-randomness.sh` (a native
single node) and, after an in-place upgrade, by `./scripts/rehearse-upgrade-v0.10.0.sh`; the
join rehearsal of §9 does not exercise it.

## 6. Why one more validator rarely makes the network safer

Consensus needs **more than two thirds of the voting power** online, and voting power is stake.
Counting nodes says nothing; counting stake does.

The rehearsal shows it plainly: node A holds 1,000,000 SCN, B 10,000, C 2,000. Stop B and blocks
keep coming. Stop A and the chain stops — B and C together hold about 1% of the power.

| Validators, equal stake | Can be offline without a halt |
|---|---|
| 1 | none |
| 2 | none — each holds 1/2, the other alone is below 2/3 |
| 3 | none — two of three hold exactly 2/3, which is not *more* than 2/3 |
| **4** | **one** |
| 7 | two |

The chain itself runs with any number of validators, a single one included — nothing in it
requires three, or four. The table is only about outages. So a network that has to survive one
of its own validators going down needs **at least four
validators of roughly equal stake** — not three. Joining validators with the 2,000 SCN minimum add
participation, not fault tolerance, until their stake is comparable.

## 7. Networking and keys

- **Inbound 26656** is what other nodes dial. A node that only syncs needs outbound 26656. A
  full node or sentry that wants to be dialled announces itself: set
  `external_address = "<public-ip-or-dns>:26656"` under `[p2p]` in `config/config.toml` (edit the
  file, or `saicond config set config p2p.external_address "<ip>:26656" -s`; without `-s` the
  command refuses CometBFT keys with *cometbft config is not supported*). **This does not apply to
  a validator behind sentries**: it leaves `external_address` empty and is never announced (see
  the sentry guide below). A validator that runs alone, without sentries, accepts inbound 26656
  and may announce itself.
- **Keep RPC 26657, REST 1317 and gRPC 9090 closed to the public** on a validator. Bind them to
  localhost and reach them over SSH.
- **Keep the host clock in sync with NTP** (`timedatectl status`: `System clock synchronized: yes`;
  chrony or systemd-timesyncd). From v0.10.0 the node's CometBFT (v0.38.25) rejects any block whose
  time is **60 s or more ahead** of the node's own clock (`block_time_tolerance` in `config.toml`), so
  a node whose clock runs behind the network refuses blocks the others accept. Containers share the
  host clock; never step it while the node runs.
- **Back up two files**: the mnemonic (§3) and `config/priv_validator_key.json`, the consensus
  key. Keep neither in git or a chat.
- **Never run the same `priv_validator_key.json` on two machines at once.** Two copies sign
  conflicting blocks: that is a double-sign — 10% of the validator's stake slashed on the DevNet
  (`saicond q slashing params`), jail, and a permanent tombstone on the key.
- The examples use the default keyring, which encrypts the key with your passphrase.
  `--keyring-backend test` (used by the rehearsal) keeps keys unencrypted on disk — fine for a
  rehearsal, never for real funds.

### Run the validator behind sentries

A validator's job is to sign. Anyone who learns its address can flood it, so the expected layout
([ADR-0015](adr/ADR-0015-sentry-layout-and-node-exposure.md), sizing in
[node-requirements.md](node-requirements.md)) puts **two sentries** between it and the network: ordinary
`full` nodes that take the peer traffic and carry the validator as a *private peer* — dialled, never
advertised. Two, because with one the validator is cut off whenever that sentry is down, and a
validator without peers stops signing. Three machines for one seat; put the sentries at different
providers if you can. It is expected rather than required on the DevNet.

```
 network ◄── p2p 26656 ──► sentry A ─┐
                                      ├── p2p 26656, private ──► validator (signs; no public ports)
 network ◄── p2p 26656 ──► sentry B ─┘
```

**The rule that makes the layout work: the validator never connects to a node that gossips, and
never announces itself.** A node that dials another node tells it its own `external_address` (its
listen address if that is empty); a node with `pex` on stores that address and passes it on. So the
validator dials **only its own two sentries** (which hold it as a private peer), keeps
`external_address` **empty**, and never goes through the §2 start with the public DevNet peer. Its
state sync downloads over HTTP RPC (light client) and from its sentries; HTTP does not carry the
p2p address.

**An existing validator** that already joined through §2 to §5 with `pex` on has announced its
address to the DevNet's nodes; moving it behind sentries stops further gossip but cannot recall that.
If hiding matters, give the validator a new IP address (the consensus key `priv_validator_key.json`
stays; only the address changes).

**1. Start the two sentries first.** Each is a node from §2 on the `full` profile with
state-sync snapshots on (`--state-sync.snapshot-interval 1000 --state-sync.snapshot-keep-recent 2`)
— the DevNet join steps above apply to each one unchanged: genesis checked against its checksum,
state sync, the one restart. A sentry holds **no validator or operator key**: `init` gives it a
throw-away `priv_validator_key.json`, which is never bonded. Open inbound `26656` to the world; keep
`26657`, `1317` and `9090` closed to the public, with RPC on `127.0.0.1` (the §2 start already
leaves it there). **A sentry never serves public RPC**: `/net_info` and `/dump_consensus_state` are
ordinary routes, not unsafe ones, and they list every peer's id and address, the validator's included.
Public RPC and REST go on a separate full node that peers with the sentries and never with the
validator.

**2. Read the node ids** (a node id identifies the machine's `node_key.json`; it is public and is not
a signing key). Do this on the validator before the sentries are configured, and on each sentry:

```bash
# Docker: from the node's volume
docker run --rm -v saicon-mynode:$H $IMG comet show-node-id --home $H
# binary
saicond comet show-node-id --home ~/.saicon
```

Call them `SENTRY_A_ID`, `SENTRY_B_ID` and `VALIDATOR_ID`; the machines' addresses `SENTRY_A_IP`,
`SENTRY_B_IP` (public) and `VALIDATOR_PRIVATE_IP`: the private address at which the sentries reach
the validator. The three machines therefore share a private network (the provider's private network
or VPC, or a WireGuard tunnel); the validator has no public address for p2p.

**3. Configure each sentry**, then restart it. Four keys in `[p2p]` of `config/config.toml`: `pex`
on, the validator private (kept out of what the sentry gossips) and unconditional (always accepted),
and `addr_book_strict` off so a private validator address is accepted. `private_peer_ids` and
`unconditional_peer_ids` take **bare ids**: an entry written `id@host` is silently ignored, and a
sentry ignoring `private_peer_ids` advertises exactly the address it exists to hide. The keys are
edited with `sed` because that changes nothing else in the file; this helper works for both setups
(on macOS use `sed -i ''`):

```bash
setkey() { sed -i "s#^$1 = .*#$2#" "$CFG"; }     # setkey <key> '<key> = <value>'

# binary
CFG=~/.saicon/config/config.toml
setkey pex                    'pex = true'
setkey private_peer_ids       'private_peer_ids = "VALIDATOR_ID"'
setkey unconditional_peer_ids 'unconditional_peer_ids = "VALIDATOR_ID"'
setkey addr_book_strict       'addr_book_strict = false'

# Docker: the same four edits on the volume, before `docker run … start`
docker run --rm -v saicon-mynode:$H --entrypoint sh $IMG -c "cd $H/config && sed -i \
  -e 's#^pex = .*#pex = true#' \
  -e 's#^private_peer_ids = .*#private_peer_ids = \"VALIDATOR_ID\"#' \
  -e 's#^unconditional_peer_ids = .*#unconditional_peer_ids = \"VALIDATOR_ID\"#' \
  -e 's#^addr_book_strict = .*#addr_book_strict = false#' config.toml"
```

Start the sentry with the validator next to the DevNet peer as persistent peers, and the full
profile's flags (§2) plus snapshots (binary shown; for Docker add the same after the image name, and
publish `-p 26656:26656`):

```bash
saicond start --home ~/.saicon --minimum-gas-prices 0nscn \
  --pruning custom --pruning-keep-recent 100000 --pruning-interval 10 --min-retain-blocks 0 \
  --state-sync.snapshot-interval 1000 --state-sync.snapshot-keep-recent 2 \
  --p2p.persistent_peers "<node-id>@p2p.saicon.world:26656,VALIDATOR_ID@VALIDATOR_PRIVATE_IP:26656"
```

**4. Start the validator, once both sentries are synced.** It is a node from §2 on the `validator`
profile, with three differences. `pex = false`; `external_address` stays empty (leave the key as
`init` wrote it); `persistent_peers` is exactly its two sentries, never the DevNet peer; and RPC
listens on loopback only. State sync uses the public RPC over HTTPS for the trust height and hash
(`$STATESYNC` of §2, unchanged) — or a sentry's RPC, if you bind that to a private interface the
validator reaches, by setting `RPC` to it before computing `$STATESYNC`. Either is HTTP; neither
announces the validator's p2p address. The snapshot itself comes from the sentries, which offer one
every 1,000 blocks once synced.

```bash
# binary: genesis and state sync as in §2, then
CFG=~/.saicon/config/config.toml
setkey pex               'pex = false'
setkey addr_book_strict  'addr_book_strict = false'
setkey external_address  'external_address = ""'
saicond start --home ~/.saicon --minimum-gas-prices 0nscn \
  --pruning custom --pruning-keep-recent 100000 --pruning-interval 10 --min-retain-blocks 302400 \
  --rpc.laddr tcp://127.0.0.1:26657 \
  --p2p.persistent_peers "SENTRY_A_ID@SENTRY_A_IP:26656,SENTRY_B_ID@SENTRY_B_IP:26656"

# Docker: genesis and state sync as in §2 (steps 1–2), then the same three edits, then start with the
# same flags after the image name. Publish 26656 on the private address only.
docker run --rm -v saicon-mynode:$H --entrypoint sh $IMG -c "cd $H/config && sed -i \
  -e 's#^pex = .*#pex = false#' \
  -e 's#^addr_book_strict = .*#addr_book_strict = false#' \
  -e 's#^external_address = .*#external_address = \"\"#' config.toml"
docker run -d --name saicon-mynode -v saicon-mynode:$H -p VALIDATOR_PRIVATE_IP:26656:26656 $IMG \
  start --home $H --minimum-gas-prices 0nscn \
  --pruning custom --pruning-keep-recent 100000 --pruning-interval 10 --min-retain-blocks 302400 \
  --rpc.laddr tcp://127.0.0.1:26657 \
  --p2p.persistent_peers "SENTRY_A_ID@SENTRY_A_IP:26656,SENTRY_B_ID@SENTRY_B_IP:26656"
```

The validator has no public RPC or REST (leave `[api] enable = false` in `app.toml`). Its firewall
admits `26656` **only from the two sentries' addresses**. Its own steps (§3 to §5: key, tokens,
`create-validator`) are the ones above, run on the validator. `addr_book_strict = false` is for
machines that see each other on private (RFC 1918) addresses, as CometBFT's sentry guidance notes;
the local rehearsal below runs on the default `true`.

**Docker and the host firewall.** Docker publishes a port through its own iptables rules, which are
evaluated before the rules `ufw` manages, so a `ufw` rule does not restrict a published port. Restrict
it at the provider's firewall (security group), publish on the private address as above, or add the
rule in the `DOCKER-USER` chain, which Docker consults first.

**5. Check it worked.** The commands below run as written for Docker (`docker exec` into the node: the
image has `wget` and `jq` but no `curl`) and, for the binary, with `curl` on the machine itself.

```bash
# --- on a sentry: the validator is among its peers
docker exec saicon-mynode sh -c 'wget -qO- http://127.0.0.1:26657/net_info | jq -r ".result.peers[].node_info.id"'
curl -s localhost:26657/net_info | jq -r '.result.peers[].node_info.id'                   # binary
#   -> lists VALIDATOR_ID (and the peers the sentry found)

# --- on the validator: only its two sentries (RPC is loopback, so on the machine itself)
docker exec saicon-mynode sh -c 'wget -qO- http://127.0.0.1:26657/net_info | jq -r ".result.n_peers, (.result.peers[].node_info.id)"'
curl -s 127.0.0.1:26657/net_info | jq -r '.result.n_peers, (.result.peers[].node_info.id)'  # binary
#   -> 2, SENTRY_A_ID, SENTRY_B_ID and nothing else

# --- on each sentry: its address book must not contain the validator
docker exec saicon-mynode sh -c "jq '.addrs | length' $H/config/addrbook.json; grep -c VALIDATOR_ID $H/config/addrbook.json"
jq '.addrs | length' ~/.saicon/config/addrbook.json; grep -c VALIDATOR_ID ~/.saicon/config/addrbook.json   # binary
#   -> the first number is how many addresses the book holds, the second must be 0
#      (grep exits 1 when its count is 0; that is the expected answer here)
```

The address book is what a node gossips from: CometBFT refuses to store a private peer in it, so a
`0` there means the validator is not being passed on. The file is written every two minutes and on
shutdown, and a sentry that has just started holds few entries, so read the first number as well: a
second number of `0` means little when the book is empty; check again after the sentry has been up
for an hour. Then confirm the validator still signs (`saicond q slashing signing-info …`, §5)
before you stop watching it.

The repository ships the same layout as a runnable rehearsal on one machine, which checks every
point above except the address book (its nodes find each other through persistent peers, so their
books stay empty):

```bash
docker compose -f chain/docker-compose.testnet.yml -f chain/docker-compose.testnet.sentry.yml up -d
bash chain/scripts/test-sentry-layout.sh          # 22 checks: pex, private ids, no gossip, still signing
docker compose -f chain/docker-compose.testnet.yml -f chain/docker-compose.testnet.sentry.yml down -v
```

## 8. Troubleshooting

| Symptom | Cause, and the fix |
|---|---|
| `validator set is empty after InitGenesis` at start | the genesis is the one `init` wrote, not the network's. Replace it (§1), then reset the data from the failed start (next row) |
| the same error **after** replacing the genesis | the failed first start left state in `data/`. `saicond comet unsafe-reset-all --home …` clears it (keeps your keys), then start again |
| `set min gas price in app.toml or flag or env variable` | start with `--minimum-gas-prices 0nscn` |
| `sh: curl: not found` inside the container | the image has no `curl`; use `saicond status`, or `wget -qO-` |
| `n_peers = 0`, never syncs | wrong peer address — re-read the node id (§1); inbound 26656 closed on the peer; clock skew |
| `catching_up` stays `true` | not actually peered, or the peer itself is stuck |
| a transfer "succeeded" but nothing arrived | the broadcast's `code: 0` is not the result — `saicond q tx <hash>` shows what happened (§3) |
| `account … not found` when querying a new address | it has never received anything yet — the funding has not landed |
| validator stays `STATUS_BONDING` after it bonded *(v0.10.0)* | its entity is not attested yet, or the entity already holds two active seats. `saicond q entityregistry validator-entity <saiconvaloper…>` shows `pending`; the attestor attests it, and it becomes active at the end of that block |
| `out of gas in location: ReadFlat` on `create-validator` *(v0.10.0)* | the default 200,000 gas is not enough (it uses 203,389) — `--gas 600000` or `--gas auto --gas-adjustment 1.5` |
| `window closed: round N is not accepting submissions` from `submit-vrf` *(v0.10.0)* | the window is not open, or has closed. Normal between rounds. The refusal in the block before a window opens is logged by the loop as `submit-vrf failed` and retried (§5, VRF key) |
| `not designated this round` from `submit-vrf` *(v0.10.0)* | no key of yours counts in this round, or another member of your entity is the designated submitter. A new key counts one to two rounds after it is bound; `saicond q randomness round` lists the pool and who is designated |
| `the key file is not the key bound for this round` *(v0.10.0)* | `vrf_key.json` holds a different key from the one bound for your validator. Use the file made for the bound key; a new key needs a new offer and a re-attestation |
| `has not offered this key` on an attestation *(v0.10.0)* | the operator has not sent `offer-vrf-key` with this exact key; the attestor cannot bind it first |
| validator active but earns nothing | rewards go only to validators in the bonded set (the top `max_validators` by stake), `ACTIVE`, compliant and with Empirical Points. EP accrue over closed epochs: `saicond q participation ep <saiconvaloper…>`. Compliance is `saicond q compliance compliance <saiconvaloper…>` — no record counts as compliant |

## 9. Rehearse it locally

```bash
docker compose -f chain/docker-compose.yml build                        # builds saicond:dev
bash chain/scripts/rehearse-join.sh                                     # ~2 minutes
IMAGE=ghcr.io/saicon-network/saicon:v0.11.0 bash chain/scripts/rehearse-join.sh
```

It starts node A as the DevNet's genesis node, then has B and C each join from an empty node:
genesis from A's RPC, a peer, sync, a key, tokens from someone who has them (B is funded by A, C by
B), a refused 1,000 SCN validator, then two real ones that turn `ACTIVE`. Finally it stops B and
then A to show which outage halts the chain. Everything runs in its own Docker network with no
host ports, and is removed at the end (`KEEP=1` keeps it for poking around).

## 10. Getting help

Open an issue at <https://github.com/saicon-network/saicon/issues/new/choose> and pick **Node operator question** (running a node, syncing, peers, staking, sentries) or
**Bug report**. The form asks for the `saicond version`, your profile, whether it runs in Docker or as a binary, the chain id, what you
expected, what happened and the relevant log lines. **Never paste a mnemonic, `priv_validator_key.json`
or `node_key.json`** — node ids, addresses and transaction hashes are fine.
