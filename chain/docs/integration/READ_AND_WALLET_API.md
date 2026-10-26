# SAiCoN — reading the chain, and connecting a wallet

**For the web application's frontend developer.** Version 3, 22 October 2026.

> **v0.9.0 restarted the devnet from a fresh genesis on 22 October.** Every earlier address,
> balance, delegation and height is gone, and the validator has a new address. The chain id and
> the endpoints stay the same. New in this version: **stake limits and routed delegation**
> (§5.5) — a plain delegation can now be refused, and there is a message that places it for you.

Everything a page needs to connect a wallet and read the chain, with the endpoint for each.

---

## 1. Network connection — everything in one table

| What | Value |
|---|---|
| Chain ID | `saicon-devnet-1` |
| REST (Cosmos SDK) | `https://api.saicon.world` |
| RPC (CometBFT) | `https://rpc.saicon.world` |
| Address prefix | `saicon` |
| Coin type | **118** — the standard Cosmos derivation path |
| Base denomination | `nscn` |
| Display symbol | `SCN` |
| Decimals | **8** — 1 SCN = 100 000 000 nscn |
| Minimum gas price | 0 |
| SDK / consensus | Cosmos SDK v0.53.6 · CometBFT 0.38.21 |
| CORS | already open — `Access-Control-Allow-Origin: *`, methods `GET, POST, OPTIONS` |

**Coin type 118 is the important line.** It means Keplr, Leap and Ledger reach SAiCoN
accounts on the standard path with no custom derivation. Connecting a wallet is a
configuration task, not an integration project.

**SAiCoN is a standard Cosmos SDK chain.** Everything you already know about connecting to
and reading a Cosmos chain applies here unchanged — the same wallets, the same libraries,
the same endpoint paths, the same public documentation. Only §5.4 is specific to SAiCoN;
everything else in §5 is the Cosmos surface you would find on any chain in that ecosystem.

---

## 2. A working reference you can test against

**`https://explorer.saicon.world`** runs against the same two endpoints this document
describes. It is the fastest way to check your own work: if a number on your page disagrees
with the explorer, one of you is reading a different field.

Two things there are worth opening before you write any code.

**Its wallet dialog.** The explorer already connects Keplr, Leap and Ledger to this chain,
and `/wallet/suggest` is a working `suggestChain` page. The flow in §3 is the same one,
described so you can build it rather than copy it.

**Its data pages**, each backed by the endpoints in §5:

| Page | What it reads |
|---|---|
| `/saicon/block` | the block stream — height, age, transaction count |
| `/saicon/tx` | transactions |
| `/saicon/staking` | the validator set |
| `/saicon/uptime` | signing records |
| `/saicon/supply` | total supply |
| `/saicon/params` | chain parameters |
| `/saicon/consensus` | consensus state |

Open a page, open the browser's network tab, and every call it makes is a call you can make
from your own site — same origin policy already allows it (§1, CORS).

---

## 3. Connecting a wallet

### 3.1 Which wallets

Any Cosmos wallet that supports `suggestChain` works. In practice that means **Keplr** and
**Leap** as browser extensions, plus **Ledger** through either of them. Cosmostation and
other Cosmos wallets use the same interface and should also work, but have not been tested
against this chain — treat them as unverified rather than unsupported.

Because it is a standard Cosmos chain, ready-made connect libraries work without adaptation:
**CosmosKit** and **graz** both wrap the whole flow in §3.4, including the account-switch and
locked states. Writing it by hand is equally fine — the payload in §3.3 is all either of them
would be given.

This document covers the browser-extension path. WalletConnect is a separate discussion.

### 3.2 Registering the chain with `suggestChain`

The site registers the chain with the wallet itself, at connect time, by calling
`suggestChain` with the payload below. The user sees a one-time approval dialog naming the
chain; after that the chain behaves like any other in their wallet.

The call is safe to leave in place permanently — `suggestChain` on an already-known chain is
a no-op, and it stays correct once SAiCoN is listed in the Cosmos Chain Registry and wallets
start resolving it on their own.

### 3.3 The payload

```js
const SAICON_CHAIN = {
  chainId: 'saicon-devnet-1',
  chainName: 'SAiCoN DevNet',
  rpc: 'https://rpc.saicon.world',
  rest: 'https://api.saicon.world',
  bip44: { coinType: 118 },
  bech32Config: {
    bech32PrefixAccAddr:  'saicon',
    bech32PrefixAccPub:   'saiconpub',
    bech32PrefixValAddr:  'saiconvaloper',
    bech32PrefixValPub:   'saiconvaloperpub',
    bech32PrefixConsAddr: 'saiconvalcons',
    bech32PrefixConsPub:  'saiconvalconspub',
  },
  currencies: [{
    coinDenom: 'SCN', coinMinimalDenom: 'nscn', coinDecimals: 8,
  }],
  feeCurrencies: [{
    coinDenom: 'SCN', coinMinimalDenom: 'nscn', coinDecimals: 8,
    gasPriceStep: { low: 0, average: 0, high: 0 },
  }],
  stakeCurrency: { coinDenom: 'SCN', coinMinimalDenom: 'nscn', coinDecimals: 8 },
  features: [],
};
```

### 3.4 The connect flow

```js
async function connect() {
  const wallet = window.keplr ?? window.leap;
  if (!wallet) throw new Error('NO_WALLET');          // show an install prompt

  await wallet.experimentalSuggestChain(SAICON_CHAIN); // one-time, user approves
  await wallet.enable(SAICON_CHAIN.chainId);           // per-session permission

  const signer   = wallet.getOfflineSigner(SAICON_CHAIN.chainId);
  const [account] = await signer.getAccounts();
  return { address: account.address, signer };          // saicon1...
}
```

Four things the flow must handle, because all four happen in normal use:

| Situation | What the user should see |
|---|---|
| No extension installed | An install link, not a stack trace |
| User rejects `suggestChain` or `enable` | "Connection cancelled", and the button returns to its initial state |
| User switches account in the wallet | The site re-reads the account — listen for `keplr_keystorechange` / `leap_keystorechange` |
| Wallet is locked | Prompt to unlock; `enable` throws until it is |

### 3.5 Sending a transaction

Use `SigningStargateClient` from `@cosmjs/stargate`, pointed at the RPC endpoint:

```js
import { SigningStargateClient } from '@cosmjs/stargate';

const client = await SigningStargateClient.connectWithSigner(
  'https://rpc.saicon.world', signer, { gasPrice: GasPrice.fromString('0nscn') },
);
```

Minimum gas price is 0 today, so a zero fee is accepted. **Do not hard-code that.** Read
it from configuration so a later change on the chain does not require a frontend release.

Standard Cosmos messages — send, delegate, undelegate, redelegate, withdraw rewards, vote —
work as they do on any Cosmos chain, **with the stake limits of §5.5 on top**: a delegation can
be refused with a SAiCoN error code, and a delegation flow should offer the routed delegation
message of §5.5 rather than a plain `MsgDelegate`.

---

## 4. Two ways to read the chain, and when to use which

| | REST — `api.saicon.world` | RPC — `rpc.saicon.world` |
|---|---|---|
| What it is | Cosmos SDK query endpoints, JSON | CometBFT node interface, JSON-RPC |
| Use it for | balances, validators, staking, supply, module state | blocks, consensus, node status, raw transaction search |
| Shape | one endpoint per query, typed JSON | `/method?param=value`, results wrapped in `{"result": …}` |
| Pagination | `?pagination.limit=` and `?pagination.key=` | per-method, usually `page` and `per_page` |

Most of a site's needs are REST. Reach for RPC when a block or the node's own status is the
subject.

---

## 5. The endpoints, by what a page needs

### 5.1 Blocks and network status

| Need | Call |
|---|---|
| Current height, sync state, node identity | `RPC /status` |
| One block by height | `RPC /block?height=12345` |
| Latest block | `RPC /block` |
| Block results — events emitted in it | `RPC /block_results?height=12345` |
| Chain and app version | `REST /cosmos/base/tendermint/v1beta1/node_info` |

`/status` returns `result.sync_info.latest_block_height` as a **string**. Every number in
both interfaces is a string — see §6.

### 5.2 Transactions

| Need | Call |
|---|---|
| One transaction by hash | `REST /cosmos/tx/v1beta1/txs/{hash}` |
| Search — all txs in a block | `REST /cosmos/tx/v1beta1/txs?query=tx.height=12345` |
| Search — everything from an address | `REST /cosmos/tx/v1beta1/txs?query=message.sender='saicon1...'` |
| Broadcast a signed transaction | `RPC /broadcast_tx_sync` or the SDK client |

The hash is uppercase hex without a `0x` prefix.

### 5.3 Validators, staking, supply

| Need | Call |
|---|---|
| All validators | `REST /cosmos/staking/v1beta1/validators` |
| One validator | `REST /cosmos/staking/v1beta1/validators/{valoper}` |
| Delegations of one address | `REST /cosmos/staking/v1beta1/delegations/{address}` |
| Unbonding entries | `REST /cosmos/staking/v1beta1/delegators/{address}/unbonding_delegations` |
| Delegator rewards, ready to withdraw | `REST /cosmos/distribution/v1beta1/delegators/{address}/rewards` |
| Account balance | `REST /cosmos/bank/v1beta1/balances/{address}` |
| Total supply | `REST /cosmos/bank/v1beta1/supply` |
| Staking parameters | `REST /cosmos/staking/v1beta1/params` |
| Signing info — uptime, jail state | `REST /cosmos/slashing/v1beta1/signing_infos` |

### 5.4 SAiCoN's own endpoints

Four of the modules the standard Cosmos set does not have (the other two, stake limits and
routing, are §5.5). These are what makes a SAiCoN validator
page different from a generic Cosmos one.

| Need | Call |
|---|---|
| SAiCoN registry status of every validator | `REST /saicon/chain/validatorregistry/v1/validators` |
| …of one validator | `REST /saicon/chain/validatorregistry/v1/validators/{address}` |
| Accumulated participation points | `REST /saicon/chain/participation/v1/ep/{address}` |
| Participation score for one epoch | `REST /saicon/chain/participation/v1/snapshot/{address}/{epoch}` |
| Reward record for one epoch | `REST /saicon/chain/rewards/v1/snapshot/{address}/{epoch}` |
| Compliance record | `REST /saicon/chain/compliance/v1/compliance/{address}` |
| Module parameters | `…/{module}/v1/params` for each of the four |

Response shape (values as on the devnet after the v0.7.0 restart; the address is per-network):

```json
GET /saicon/chain/validatorregistry/v1/validators

{ "validators": [ {
    "validator_address": "saicon1…",
    "status": "STATUS_ACTIVE",
    "bonding_start": "<genesis time>",
    "lockup_duration_seconds": "5184000",
    "unbonding_start": null,
    "compliance_flag": true,
    "bonding_start_height": "1",
    "unbonding_complete_time": "0001-01-01T00:00:00Z",
    "pre_unbonding_status": "STATUS_UNSPECIFIED"
} ], "pagination": null }
```

**The six registry statuses**, which are not the same list the application uses:
`STATUS_BONDING` · `STATUS_ACTIVE` · `STATUS_QUARANTINED` · `STATUS_JAILED` ·
`STATUS_UNBONDING` · `STATUS_INACTIVE`. `STATUS_UNSPECIFIED` is a protobuf default and
should never appear at runtime — if it does, treat it as missing data rather than a state.

### 5.5 Stake limits and routed delegation

Three limits apply to every delegation, and a delegation that would break one is refused:

| Limit | Value on the devnet | Read it from |
|---|---|---|
| Total bonded stake (global cap) | 1,680,000 SCN | `cap_state.global_cap` |
| Stake on one validator (validator cap) | 2,000,000 SCN | `cap_state.validator_cap` |
| Validators one account delegates to | at most 3 — the validator an operator runs itself does not count | `params.max_validators_per_delegator` |
| Smallest delegation | 100 SCN | `params.min_delegation` |

Read the values; do not hard-code them — they are governance parameters.

| Need | Call |
|---|---|
| The caps, what is bonded and the room left under the global cap | `REST /saicon/chain/stakeengine/v1/caps` |
| Limits and minimums | `REST /saicon/chain/stakeengine/v1/params` |
| One validator's stake, its cap and the room left | `REST /saicon/chain/stakeengine/v1/validators/{valoper}/cap` |
| Stake waiting for room under the global cap | `REST /saicon/chain/stakeengine/v1/queue` |
| One account's waiting stake | `REST /saicon/chain/stakeengine/v1/delegators/{address}/queue` |
| **Where a delegation would go, before signing** | `REST /saicon/chain/delegationrouter/v1/route?delegator={address}&validator={valoper}&amount={nscn}` |

```json
GET /saicon/chain/stakeengine/v1/caps

{ "cap_state": { "epoch": "8", "public_circulating": "1000000118830708",
    "global_cap": "168000000000000", "ewma_bonded": "100000000000000.000000000000000000",
    "validator_cap": "200000000000000" },
  "total_bonded": "100000000000000", "headroom": "68000000000000" }
```

**Routed delegation.** `MsgDelegateRouted` takes the same three fields as `MsgDelegate`
(`delegator`, `validator`, `amount`) and does the placing: the chosen validator takes what fits
under its cap; the rest goes to up to two other active, well-participating validators that still
have room; and whatever is above the global cap waits in the queue, which fills on its own as
room opens. It all happens in one transaction — the whole delegation lands, or none of it does.
**Use it for every delegation the site makes.** A plain `MsgDelegate` still works, but it is
refused outright the moment its validator is full.

The route query answers with exactly what the message would do now — show it before the user
signs:

```json
GET /saicon/chain/delegationrouter/v1/route?delegator=saicon1…&validator=saiconvaloper1…&amount=90000000000000

{ "legs":   [ { "validator": "saiconvaloper1…", "amount": "68000000000000" } ],
  "queued": [ { "validator": "saiconvaloper1…", "amount": "22000000000000" } ],
  "fits": "90000000000000", "reason": "" }
```

That is the devnet today: 680,000 SCN fit under the global cap and are delegated at once,
220,000 SCN wait in the queue. `legs` are delegated immediately, `queued` waits, `fits` is how
much the chain can place, and a non-empty `reason` is the refusal the message would return —
for example `"500 nscn; the minimum is 100 SCN: delegation below the minimum"`. A refusal is a
normal answer (HTTP 200), not an error.

**Messages:**

| Message | Type URL | Amino name |
|---|---|---|
| Routed delegation | `/chain.delegationrouter.v1.MsgDelegateRouted` | `chain/x/delegationrouter/MsgDelegateRouted` |
| Wait in the queue explicitly | `/chain.stakeengine.v1.MsgEnqueueDelegation` | `chain/x/stakeengine/MsgEnqueueDelegation` |
| Take queued stake back | `/chain.stakeengine.v1.MsgCancelQueuedDelegation` | `chain/x/stakeengine/MsgCancelQueuedDelegation` |

They are not in the standard CosmJS registry: generate the types from
`chain/proto/chain/delegationrouter/v1/tx.proto` and `chain/proto/chain/stakeengine/v1/tx.proto`
(for example with `ts-proto` or `telescope`) and register them with the signing client; for
Ledger and other amino signers register the amino names too.

**Gas.** A routed delegation touches up to three validators and the queue: it needs about
**1,000,000 gas**, and the common 200,000 default fails with *out of gas*. Simulate and add a
margin (`"auto"` gas with a multiplier of 1.5 in CosmJS), or set at least 1,000,000.

**Refusals.** A plain `MsgDelegate` that breaks a limit is refused by the broadcast itself
(`CheckTx`), before it reaches a block. A routed delegation is checked when it runs, so its
refusal comes back in the transaction result — preview with the route query and the user
rarely meets one. Both carry a codespace and a code:

`codespace: "stakeengine"`:

| Code | Meaning | What to show |
|---|---|---|
| 1101 | the network's global cap is reached | the delegation can wait in the queue — offer the routed message |
| 1102 | the validator is at its cap | offer the routed message, which places the rest elsewhere |
| 1112 | the account already delegates to 3 validators and this would be a fourth | delegate to one of the three, or move everything off one of them first |
| 1113 | below the 100 SCN minimum | the minimum, from `params.min_delegation` |
| 1103 | (validator creation) the operator's own stake is below 2,000 SCN | — |

…and from `codespace: "delegationrouter"`:

| Code | Meaning | What to show |
|---|---|---|
| 1101 | the chosen validator cannot take routed stake — not active, or jailed | pick another validator |
| 1102 | no route places the whole amount — the validators with room are full | the amount that fits (`fits` in the route query) |

---

## 6. Five rules that will otherwise cost you a day each

**1 · The registry returns account-prefixed addresses; staking returns valoper.** The
example above shows `saicon19hlt…` while `/cosmos/staking/…/validators` returns
`saiconvaloper19hlt…` for the same validator. **Same bytes, different prefix.** Convert with
bech32 decode/encode; do not compare the strings directly, and do not assume one endpoint's
address can be pasted into another's path.

**2 · Every number is a string.** Heights, amounts, counts, timestamps — all JSON strings,
because they exceed what a JavaScript number holds safely. Parse amounts with `BigInt` and
never with `parseFloat`. A supply of `1000356912265003` nscn silently loses precision as a
float.

**3 · Amounts are in `nscn`; display is `SCN` at 8 decimals.** Divide by 10^8 **for display
only**, using a decimal library or integer arithmetic. Never carry a float through a
calculation and then round it — round once, at the edge, when you print.

**4 · Absence is not zero.** A validator with no participation record is not a validator
with zero participation. Show a dash and, on hover, why the value is missing. This matters
more here than on most chains: reward eligibility requires accumulated points above zero, so
"0" and "no record" mean different things to a validator reading their own page.

**5 · Pagination is opt-in and the default limit is 100.** Pass
`?pagination.limit=200&pagination.key=<next_key>` and follow `pagination.next_key` until it
is null. A page that reads only the first response will silently show a truncated validator
set once the network grows.

---


## 7. What "done" looks like

**Wallet:**

1. Connect works in Keplr and in Leap, from a clean browser profile with no prior SAiCoN
   approval.
2. Rejecting the approval dialog leaves the page usable and the button in its initial state.
3. Switching account in the wallet updates the page without a reload.
4. A signed transaction reaches the chain and its hash resolves through
   `/cosmos/tx/v1beta1/txs/{hash}`.
5. No private key, mnemonic or signature ever reaches your backend, your logs or your
   analytics.

**Reading:**

6. Validator list, one validator, blocks, one transaction and total supply all render from
   live data.
7. Amounts are computed with integers and rounded once, at display.
8. A missing value renders as a dash with a reason, never as `0`.
9. Pagination is followed to the end of the set.
10. A delegation shows the route preview (§5.5) before signing, uses `MsgDelegateRouted` with
    enough gas, and turns each refusal code into a sentence the user understands.
11. Every displayed number can be traced to the endpoint it came from — put the source in a
    tooltip or a debug view. This is what makes a disagreement between two systems solvable
    in minutes instead of days.

---

## 8. What we need from you

1. The origin the site will call from, so it can be checked against the CORS configuration —
   it is open today, and that may narrow before production.
2. Whether the site needs write operations in this phase, or read only. The wallet section
   above covers both; the answer changes what has to be reviewed before launch.
3. Which wallets you intend to support beyond Keplr and Leap, so they can be tested against
   this chain rather than assumed.

Questions about a specific endpoint are best sent with the exact URL and the response you
got — the chain is the same one for both teams, so a difference is reproducible.
