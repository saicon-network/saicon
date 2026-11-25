# chain-registry submission: SAiCoN DevNet

Prepared for a pull request to [`cosmos/chain-registry`](https://github.com/cosmos/chain-registry).
Nothing here has been submitted; the owner opens the PR from the owner's own fork.

## Files and target paths

Copy these four files from this directory into a fork of the registry. The directory name must
equal `chain_name` (`saicondevnet`).

| Source (this directory) | Target in `cosmos/chain-registry` |
|---|---|
| `chain.json` | `testnets/saicondevnet/chain.json` |
| `assetlist.json` | `testnets/saicondevnet/assetlist.json` |
| `images/scn.png` | `testnets/saicondevnet/images/scn.png` |
| `images/scn.svg` | `testnets/saicondevnet/images/scn.svg` |

Do not copy `genesis.json`, `genesis.json.sha256` or this file: `chain.json` points to the genesis
on GitHub (`codebase.genesis.genesis_url`). The logo URLs in both JSON files already name the
registry's own path (`…/master/testnets/saicondevnet/images/`), so they resolve once the PR merges.

SHA-256 of what is submitted (re-check with `shasum -a 256` right before opening the PR; if
`chain.json` changed, refresh `recommended_version`, the binary checksums and the node id first):

```
39a2da611d25998dbd579f9bc48d2e681aede2757425a5542a9feca4f28609a8  chain.json
9385ae7fe9b09d9bb936619a8df0ab83a57ba1bac496c9ab46f084f458253204  assetlist.json
f56f2f6ed16c3bc73a7b61095c69e7ebb445963133c0c0d9ec39ea29f4058670  images/scn.png
46d618f74c10f07c2c1419f2cf17d24aa979651a0cb208e9a1453990ebe75d7d  images/scn.svg
```

## Commands

```bash
git clone https://github.com/<owner-fork>/chain-registry && cd chain-registry
git checkout -b add-saicondevnet
mkdir -p testnets/saicondevnet/images
SRC=<path-to>/chain/networks/saicon-devnet-1
cp $SRC/chain.json $SRC/assetlist.json testnets/saicondevnet/
cp $SRC/images/scn.png $SRC/images/scn.svg testnets/saicondevnet/images/
git add testnets/saicondevnet
git commit -m "Add SAiCoN DevNet (saicondevnet)"
git push -u origin add-saicondevnet
```

## Validation

The registry's schemas are `chain.schema.json` and `assetlist.schema.json` at its root, and both JSON
files carry `"$schema": "../../chain.schema.json"` / `"../../assetlist.schema.json"`, which is correct
for the `testnets/<name>/` layout. Run, from the registry clone:

```bash
python3 -m json.tool testnets/saicondevnet/chain.json >/dev/null && echo chain.json parses
python3 -m json.tool testnets/saicondevnet/assetlist.json >/dev/null && echo assetlist.json parses
pip install check-jsonschema
check-jsonschema --schemafile chain.schema.json     testnets/saicondevnet/chain.json
check-jsonschema --schemafile assetlist.schema.json testnets/saicondevnet/assetlist.json
```

The registry also runs its own checks in CI (see the scripts in its `package.json`, typically
`npm install && npm run validate`); run them locally if the clone offers them. Check by hand that
`recommended_version` is a release that exists, that every binary URL downloads and matches its
`?checksum=sha256:`, and that the logo files are the ones above.
Also check, before submitting, that `codebase.genesis.genesis_url` and the `genesis.json.sha256` next
to it return HTTP 200 (`curl -sI <url>`). They resolve only once the public repository carries
`chain/networks/saicon-devnet-1/`, which it does from the release that includes this directory:
submit the pull request after that release is published, not before.

## Pull request title

```
Add SAiCoN DevNet (saicondevnet)
```

## Pull request body

```
Adds the SAiCoN DevNet, a live development network, to `testnets/saicondevnet/`.

- Chain: `saicon-devnet-1`, bech32 prefix `saicon`, daemon `saicond`, slip44 118
  (Cosmos SDK 0.53.8, CometBFT 0.38.25).
- Token: SCN, base denom `nscn` (exponent 0), display SCN (exponent 8).
- Fees: zero minimum gas price.
- Endpoints: RPC https://rpc.saicon.world, REST https://api.saicon.world,
  persistent peer p2p.saicon.world:26656.
- Explorer: https://explorer.saicon.world/saicon
- Genesis: https://raw.githubusercontent.com/saicon-network/saicon/main/chain/networks/saicon-devnet-1/genesis.json
  (SHA-256 published next to it as `genesis.json.sha256`).
- Source: https://github.com/saicon-network/saicon; the binaries listed in `chain.json` are
  the release assets, each with its SHA-256.
- Join guide: https://github.com/saicon-network/saicon/blob/main/chain/docs/MULTINODE-JOIN.md

Files: `chain.json`, `assetlist.json`, `images/scn.png`, `images/scn.svg`. Both JSON files
validate against the registry's `chain.schema.json` and `assetlist.schema.json`.
```
