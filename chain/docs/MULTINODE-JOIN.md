# Joining a SAiCoN network

> Operator guide for adding a node — and then a validator — to a running SAiCoN network such as
> the `saicon-devnet-1` DevNet. Every step from §1 to §4 (the join itself) was run end to end on
> the current build by [`./scripts/rehearse-join.sh`](../scripts/rehearse-join.sh), which plays three
> separate operators on one machine; run it yourself before you touch a real network (§9). The
> VRF steps of §5 (v0.10.0) are not part of that script: they were run by the upgrade rehearsal
> (`./scripts/rehearse-upgrade-v0.10.0.sh`) and the single-node demo (`./scripts/demo-randomness.sh`).
>
> Binary `saicond` · denom `nscn` (1 SCN = 100,000,000 nscn) · ports: P2P `26656`, RPC `26657`,
> REST `1317`, gRPC `9090`.

> **Which build a network runs decides which rules apply.** Read it first:
> `curl -s https://api.saicon.world/cosmos/base/tendermint/v1beta1/node_info | jq -r .application_version.version`.
> The DevNet runs **v0.10.0** (S8), upgraded in place from v0.9.1: the 2,000 SCN entry floor, the
> stake caps, the 60-day lock-up (longer on request), at most three validators per delegator and a
> 100 SCN minimum delegation all apply, and a validator activates only once its entity is attested
> (§0, §5). Join with the image built from the same commit the network runs (§2).

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

Three things, all readable from the network's public RPC:

```bash
RPC=https://rpc.saicon.world                       # the DevNet; any node's RPC works

curl -s $RPC/status | jq -r '.result.node_info.network'     # chain-id, e.g. saicon-devnet-1
curl -s $RPC/status | jq -r '.result.node_info.id'          # node id, for the peer address
curl -s $RPC/genesis | jq '.result.genesis' > genesis.json  # the network's genesis
```

The peer address is `<node-id>@p2p.saicon.world:26656`. The node id changes whenever the DevNet
restarts from a fresh genesis, so read it rather than copying one from old notes.

**The genesis must be the network's.** `saicond init` writes a genesis of its own; a node started
with it is a different chain and stops at once with *"validator set is empty after InitGenesis"*.

## 2. Start a node and let it sync

### With Docker (the published image)

The image runs as user `saicon` with its home at `/home/saicon/.saicon`, and has `jq` and `wget`
but **not `curl`** — query the node with `saicond` itself.

```bash
# a release image from ghcr.io — pick the release the network runs (see the note at the top)
TAG=v0.10.0
IMG=ghcr.io/saicon-network/saicon:$TAG
H=/home/saicon/.saicon
CHAIN_ID=saicon-devnet-1
PEER=<node-id>@p2p.saicon.world:26656

# 1. an empty home in a named volume
docker run --rm -v saicon-mynode:$H $IMG init my-node --chain-id $CHAIN_ID --default-denom nscn --home $H

# 2. replace its genesis with the network's
docker run --rm -v saicon-mynode:$H --entrypoint sh $IMG -c \
  "wget -qO- https://rpc.saicon.world/genesis | jq '.result.genesis' > $H/config/genesis.json \
   && saicond genesis validate-genesis --home $H"

# 3. start — the gas-price flag is required (§8)
docker run -d --name saicon-mynode -v saicon-mynode:$H -p 26656:26656 $IMG \
  start --home $H --minimum-gas-prices 0nscn --p2p.persistent_peers "$PEER"
```

### From the binary

```bash
saicond init my-node --chain-id saicon-devnet-1 --default-denom nscn --home ~/.saicon
curl -s https://rpc.saicon.world/genesis | jq '.result.genesis' > ~/.saicon/config/genesis.json
saicond genesis validate-genesis --home ~/.saicon
saicond start --home ~/.saicon --minimum-gas-prices 0nscn \
  --p2p.persistent_peers "<node-id>@p2p.saicon.world:26656"
```

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

- **Inbound 26656** is what other nodes dial. A node that only syncs needs outbound 26656; a
  validator should accept inbound and announce itself:
  `saicond config set config p2p.external_address "<public-ip-or-dns>:26656" --home …`
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
IMAGE=ghcr.io/saicon-network/saicon:v0.10.0 bash chain/scripts/rehearse-join.sh
```

It starts node A as the DevNet's genesis node, then has B and C each join from an empty node:
genesis from A's RPC, a peer, sync, a key, tokens from someone who has them (B is funded by A, C by
B), a refused 1,000 SCN validator, then two real ones that turn `ACTIVE`. Finally it stops B and
then A to show which outage halts the chain. Everything runs in its own Docker network with no
host ports, and is removed at the end (`KEEP=1` keeps it for poking around).
