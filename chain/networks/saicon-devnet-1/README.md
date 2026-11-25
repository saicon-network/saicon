# saicon-devnet-1

The published facts of the SAiCoN DevNet, the network a new operator joins.

| File | What it is |
|---|---|
| `genesis.json` | the genesis the DevNet started from on 22 Oct 2026, byte for byte as the network's own node holds it |
| `genesis.json.sha256` | its SHA-256: `6bb888ed3cc63498a812a967d09d456484e5959d1bae004262cd0a49c634dc61` |
| `chain.json` | the network in the Cosmos chain-registry format: chain-id, bech32 prefix, fees, peer, public RPC and REST, explorer, release binaries with their checksums |
| `assetlist.json` | SCN in the same format: base unit `nscn`, display unit `SCN`, 8 decimals, matching the chain's own denom metadata |
| `images/` | the SCN logo the two files above point to |

Check a downloaded genesis with `sha256sum -c genesis.json.sha256` (macOS:
`shasum -a 256 -c genesis.json.sha256`); it must print `genesis.json: OK`. How to start a node
from it is in [`chain/docs/MULTINODE-JOIN.md`](../../docs/MULTINODE-JOIN.md).

`chain.json` and `assetlist.json` validate against the chain-registry schemas, and their layout is
the registry's own (`testnets/saicondevnet/`), so they can be submitted as they are. The logo
addresses already point at the registry's copy of `images/`, which exists once the entry is
merged there.

The genesis and the node id in `chain.json` change only when the DevNet restarts from a fresh
genesis. When it does, this directory is replaced in the same change.
