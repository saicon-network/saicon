#!/usr/bin/env bash
# Deterministic, idempotent 3-validator genesis generator for the SAiCoN testnet.
#
# Produces, under the shared /data volume:
#   /data/node0, /data/node1, /data/node2   — per-node homes (identical genesis.json)
#   /data/keyring                            — shared test keyring (all 3 val keys)
#   /data/peers.txt                          — id0@node0:26656,id1@node1:26656,id2@node2:26656
#
# If both /data/node0/config/genesis.json and /data/peers.txt already exist the
# script exits 0 without regenerating, so `up` after a restart reuses the network.
set -euo pipefail

CHAIN_ID="${CHAIN_ID:-saicon-testnet-1}"
KEYRING="test"
# Per-validator genesis balance and self-delegation stake. Base denom is nscn
# (1 SCN = 1e8 nscn).
#   GENESIS_AMOUNT 3,000,000 SCN per validator account (9,000,000 SCN in all).
#   STAKE_AMOUNT     300,000 SCN self-bonded per validator: 900,000 SCN bonded.
# What that gives under the S6/S7 caps: the global cap leaves about 780,000 SCN of
# headroom over the 900,000 bonded; the validator cap floor of 400,000 SCN leaves
# 100,000 SCN of room per validator above its own 300,000 self-bond. Overflow
# happens at this size, so the delegation router has somewhere to send it.
# The genesis MUST fund less than the 21,000,000 SCN maximum supply
# (2.1e15 nscn): x/rewards clamps every mint to the remaining headroom below that
# ceiling (ADR-0011 D4), so a genesis that already exceeds it leaves zero headroom
# and NOTHING is ever paid.
GENESIS_AMOUNT="${GENESIS_AMOUNT:-300000000000000nscn}"
STAKE_AMOUNT="${STAKE_AMOUNT:-30000000000000nscn}"

# Shared keyring so every node's keys are visible from every home.
KEYRING_DIR="/data/keyring"
# Docker compose service names == p2p hostnames.
SERVICES=(node0 node1 node2)
P2P_PORT=26656

# --- ownership: the named volume mounts root-owned; node services run as uid 1000.
# genesis-init runs as root, so make /data writable by the runtime user.
if [ "$(id -u)" = "0" ]; then
  mkdir -p /data
  chown -R 1000:1000 /data
fi

# --- idempotency guard
if [ -f /data/node0/config/genesis.json ] && [ -f /data/peers.txt ]; then
  echo "[gen-genesis] genesis + peers already present — reusing existing network"
  exit 0
fi

echo "[gen-genesis] generating fresh 3-validator genesis for chain ${CHAIN_ID}"

mkdir -p "${KEYRING_DIR}"

# 1. init each node home + create its validator key in the SHARED keyring.
for i in 0 1 2; do
  HOME_I="/data/node${i}"
  rm -rf "${HOME_I}"
  saicond init "node${i}" --chain-id "${CHAIN_ID}" --default-denom nscn --home "${HOME_I}"
  # --no-backup keeps each mnemonic out of the container log (the SDK prints it to
  # stderr by default); the keys stay in the keyring.
  saicond keys add "val${i}" --no-backup \
    --keyring-backend "${KEYRING}" \
    --keyring-dir "${KEYRING_DIR}" \
    --home "${HOME_I}"
done

# 2. build the canonical genesis in node0: add all 3 validator accounts.
for i in 0 1 2; do
  ADDR="$(saicond keys show "val${i}" -a \
    --keyring-backend "${KEYRING}" \
    --keyring-dir "${KEYRING_DIR}" \
    --home /data/node0)"
  saicond genesis add-genesis-account "${ADDR}" "${GENESIS_AMOUNT}" --home /data/node0
done

# 3. each node creates its gentx against the canonical (3-account) genesis.
#    copy node0's genesis into each home first so the gentx validates.
for i in 0 1 2; do
  HOME_I="/data/node${i}"
  if [ "${i}" != "0" ]; then
    cp /data/node0/config/genesis.json "${HOME_I}/config/genesis.json"
  fi
  saicond genesis gentx "val${i}" "${STAKE_AMOUNT}" \
    --chain-id "${CHAIN_ID}" \
    --keyring-backend "${KEYRING}" \
    --keyring-dir "${KEYRING_DIR}" \
    --home "${HOME_I}"
done

# 4. collect all 3 gentxs into node0 and finalize.
mkdir -p /data/node0/config/gentx
for i in 1 2; do
  cp /data/node${i}/config/gentx/*.json /data/node0/config/gentx/
done
saicond genesis collect-gentxs --home /data/node0

# Register bank DenomMetadata: base nscn (exp 0) -> display SCN (exp 8). 1 SCN = 1e8 nscn.
GENESIS_FILE="/data/node0/config/genesis.json"
jq '.app_state.bank.denom_metadata += [{
  "description": "The native staking and participation token of SAiCoN.",
  "denom_units": [
    { "denom": "nscn", "exponent": 0, "aliases": ["satoshi-scn"] },
    { "denom": "SCN", "exponent": 8, "aliases": [] }
  ],
  "base": "nscn", "display": "SCN", "name": "SAiCoN", "symbol": "SCN", "uri": "", "uri_hash": ""
}]' "${GENESIS_FILE}" > "${GENESIS_FILE}.tmp" && mv "${GENESIS_FILE}.tmp" "${GENESIS_FILE}"

# --- slashing policy (Register v1.0 via ADR-0010) --------------------------
# Phase 1: ordinary downtime carries NO stake slash — it is punished through the
# participation layer (reward loss, jail) instead. Double-sign stays a stake
# penalty and doubles to 10%.
# With downtime at 0, x/staking never invokes BeforeValidatorSlashed for it,
# which is what lets x/compliance treat any positive burn as a double-sign.
jq '.app_state.slashing.params.slash_fraction_downtime = "0.000000000000000000"
    | .app_state.slashing.params.slash_fraction_double_sign = "0.100000000000000000"' \
  "${GENESIS_FILE}" > "${GENESIS_FILE}.tmp" && mv "${GENESIS_FILE}.tmp" "${GENESIS_FILE}"

# --- active set size (Register v1.0) ---------------------------------------
# 75, not the SDK's default 100. Register v1.0 adopted 60; the owner raised it
# to 75 on 2026-09-03 on the Stages 8-11 recommendation. Still a TestNet value,
# gated on a consensus performance benchmark that has not been run. Editable
# later by governance MsgUpdateParams, no restart. The reasoning is written out
# once, in chain/docker/entrypoint-singlenode.sh — read it there before
# changing this.
jq '.app_state.staking.params.max_validators = 75' \
  "${GENESIS_FILE}" > "${GENESIS_FILE}.tmp" && mv "${GENESIS_FILE}.tmp" "${GENESIS_FILE}"

# --- per-validator cap floor (S6, x/stakeengine; S7 value) -------------------
# 400,000 SCN: with 900,000 bonded across three validators the strict 2% rule
# would cap each at 18,000 SCN, below the 300,000 each already holds. The floor
# gives each validator 100,000 SCN of room above its self-bond, so the global cap
# (~780,000 SCN headroom) is what binds first and a large delegation overflows
# onto three validators. The reasoning is written out once, in
# chain/docker/entrypoint-singlenode.sh — read it there before changing this.
jq '.app_state.stakeengine.params.validator_cap_floor = "40000000000000"' \
  "${GENESIS_FILE}" > "${GENESIS_FILE}.tmp" && mv "${GENESIS_FILE}.tmp" "${GENESIS_FILE}"

# --- optional demo shortcuts -------------------------------------------------
# Unset on the public devnet. ./scripts/demo-delegationrouter.sh sets them so the
# router has a closed epoch to score validators on and a gov proposal can finish
# in seconds. Same keys the single-node entrypoint patches.
if [ -n "${EPOCH_LENGTH_BLOCKS:-}" ]; then
  jq --arg len "${EPOCH_LENGTH_BLOCKS}" '.app_state.participation.params.epoch_length_blocks = $len' \
    "${GENESIS_FILE}" > "${GENESIS_FILE}.tmp" && mv "${GENESIS_FILE}.tmp" "${GENESIS_FILE}"
fi
if [ -n "${GOV_VOTING_PERIOD:-}" ]; then
  jq --arg vp "${GOV_VOTING_PERIOD}" \
    '.app_state.gov.params.voting_period = $vp
     | .app_state.gov.params.expedited_voting_period = (($vp | rtrimstr("s") | tonumber / 2 | floor | tostring) + "s")' \
    "${GENESIS_FILE}" > "${GENESIS_FILE}.tmp" && mv "${GENESIS_FILE}.tmp" "${GENESIS_FILE}"
fi

# --- unbonding period and the evidence window (Register v1.0, V-5) -----------
# Both here, because they are one decision: CometBFT expires evidence only when
# BOTH limits are passed (evidence/pool.go isExpired uses &&), so the longer
# governs. Stock is 100,000 blocks and 48h — at a 5s block that is 5.8 days
# against a 21-day unbonding period, fifteen days in which a proven double-signer
# is unpunishable while the stake is still bonded. The single-node path has
# carried this since 12 Aug; this path never did.
UNBONDING_SECONDS=1814400                     # 21 days (V-5)
EVIDENCE_BLOCKS=$(( UNBONDING_SECONDS / 5 ))  # 362,880 at a 5s block

jq --arg u "${UNBONDING_SECONDS}s" \
   --arg n "${EVIDENCE_BLOCKS}" \
   --arg d "${UNBONDING_SECONDS}000000000" \
   '.app_state.staking.params.unbonding_time = $u
    | .consensus.params.evidence.max_age_num_blocks = $n
    | .consensus.params.evidence.max_age_duration = $d' \
  "${GENESIS_FILE}" > "${GENESIS_FILE}.tmp" && mv "${GENESIS_FILE}.tmp" "${GENESIS_FILE}"


saicond genesis validate-genesis --home /data/node0

# 5. distribute the final genesis to node1 and node2 (must be IDENTICAL).
cp /data/node0/config/genesis.json /data/node1/config/genesis.json
cp /data/node0/config/genesis.json /data/node2/config/genesis.json

# 6. capture node ids and build the persistent_peers string.
PEERS=""
for i in 0 1 2; do
  NID="$(saicond comet show-node-id --home /data/node${i})"
  ENTRY="${NID}@${SERVICES[$i]}:${P2P_PORT}"
  if [ -z "${PEERS}" ]; then
    PEERS="${ENTRY}"
  else
    PEERS="${PEERS},${ENTRY}"
  fi
done
printf '%s' "${PEERS}" > /data/peers.txt

# keep ownership sane for the node services (uid 1000).
if [ "$(id -u)" = "0" ]; then
  chown -R 1000:1000 /data
fi

echo "[gen-genesis] done. peers: ${PEERS}"
