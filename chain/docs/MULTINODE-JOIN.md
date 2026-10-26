# Joining a SAiCoN network

> Operator guide for adding a node — and then a validator — to a running SAiCoN network such as
> the `saicon-devnet-1` DevNet. Every step below was run end to end on the current build by
> [`./scripts/rehearse-join.sh`](../scripts/rehearse-join.sh), which plays three separate operators on
> one machine; run it yourself before you touch a real network (§9).
>
> Binary `saicond` · denom `nscn` (1 SCN = 100,000,000 nscn) · ports: P2P `26656`, RPC `26657`,
> REST `1317`, gRPC `9090`.

> **Which build a network runs decides which rules apply.** Read it first:
> `curl -s https://api.saicon.world/cosmos/base/tendermint/v1beta1/node_info | jq -r .application_version.version`.
> The DevNet runs **v0.9.0** (S7) since 22 October: the 2,000 SCN entry floor, the stake caps,
> the 60-day lock-up, at most three validators per delegator and a 100 SCN minimum delegation
> all apply. Join with the release image of the same version the network
> runs (§2).

---

## 0. Who does what

Joining is a hand-off between two people:

| The joining operator | Someone who already holds SCN (on the DevNet: the SAiCoN team) |
|---|---|
| starts an empty node, gives it the network's genesis and a peer, waits for it to sync | — |
| creates a key and sends its **address** | sends the operator at least **2,000 SCN** plus a little to spare |
| registers the validator (`create-validator`) and checks it is active | — |

Nothing else needs anyone's permission: there is no allow-list. A validator that brings at least
2,000 SCN of its own, within the network's caps (§5), becomes active when it bonds.

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
TAG=v0.9.1
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
  --chain-id saicon-devnet-1 --fees 0nscn
saicond q tx <txhash>                                       # code 0
saicond q validatorregistry validator <your-saiconvaloper…>  # status STATUS_ACTIVE
```

Your validator address is `saicond keys show operator --bech val -a`. On bond the validator moves
to `STATUS_ACTIVE` by itself, with `compliance_flag: true`. It starts earning once it is in the bonded set and has
Empirical Points, which accrue over closed epochs — the reward gate is `ACTIVE ∧ C=1 ∧ EP>0`
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
| Fees | `--fees 0nscn` on the DevNet | — |
| Validators per delegator *(v0.9.0)* | an account delegates to at most **3** validators; your own validator does not count | `delegator already holds the maximum number of validators` (code 1112) |
| Minimum delegation *(v0.9.0)* | 100 SCN per delegation | `delegation below the minimum` (code 1113) |

The current caps and headroom:

```bash
curl -s https://api.saicon.world/saicon/chain/stakeengine/v1/caps | jq
```

The 60-day lock-up matters for testing too: stake you bond into a validator stays there for 60
days. Bond the minimum unless you mean it.

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
- **Sync the clock (NTP).** Consensus is time-sensitive.
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
| validator active but earns nothing | rewards go only to validators in the bonded set (the top `max_validators` by stake), `ACTIVE`, compliant and with Empirical Points. EP accrue over closed epochs: `saicond q participation ep <saiconvaloper…>`. Compliance is `saicond q compliance compliance <saiconvaloper…>` — no record counts as compliant |

## 9. Rehearse it locally

```bash
docker compose -f chain/docker-compose.yml build                        # builds saicond:dev
bash chain/scripts/rehearse-join.sh                                     # ~2 minutes
IMAGE=ghcr.io/saicon-network/saicon:v0.9.1 bash chain/scripts/rehearse-join.sh
```

It starts node A as the DevNet's genesis node, then has B and C each join from an empty node:
genesis from A's RPC, a peer, sync, a key, tokens from someone who has them (B is funded by A, C by
B), a refused 1,000 SCN validator, then two real ones that turn `ACTIVE`. Finally it stops B and
then A to show which outage halts the chain. Everything runs in its own Docker network with no
host ports, and is removed at the end (`KEEP=1` keeps it for poking around).
