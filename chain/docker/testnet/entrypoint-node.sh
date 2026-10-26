#!/usr/bin/env bash
# Per-node entrypoint for the 3-validator SAiCoN testnet.
# Requires NODE_INDEX (0/1/2). Waits for the shared peers.txt (written by
# genesis-init) then starts the node with persistent_peers wired by docker DNS.
set -euo pipefail

NODE_INDEX="${NODE_INDEX:?NODE_INDEX must be set (0, 1 or 2)}"
HOME_DIR="/data/node${NODE_INDEX}"
PEERS_FILE="/data/peers.txt"

# genesis-init runs to completion before us (depends_on service_completed_successfully),
# but guard anyway so the node never starts without a genesis or peer list.
for _ in $(seq 1 30); do
  if [ -f "${HOME_DIR}/config/genesis.json" ] && [ -f "${PEERS_FILE}" ]; then
    break
  fi
  echo "[entrypoint-node] waiting for genesis + peers.txt ..."
  sleep 2
done

if [ ! -f "${HOME_DIR}/config/genesis.json" ] || [ ! -f "${PEERS_FILE}" ]; then
  echo "[entrypoint-node] FATAL: missing genesis or peers.txt for node${NODE_INDEX}" >&2
  exit 1
fi

# ── retention policy ────────────────────────────────────────────────────────
# The same policy the single-node entrypoint applies, and for the same reason:
# the stock configuration prunes state after 21 days while keeping every block
# for ever with state-sync snapshots off — the disk cost of an archive with the
# query surface of a pruned node, and no way in for a joining operator except a
# full replay from genesis.
#
# This entrypoint had none of it, so the three-node testnet — the environment
# closest to what an invited operator will meet — ran the incoherent defaults.
#
# Applied on every start because these are app.toml settings, not genesis: a
# node with an existing chain directory adopts the policy by restarting, with no
# state reset. Idempotent, each line being a whole-value substitution.
#
# NODE_PROFILE=archive keeps everything, for the explorer and for anyone auditing
# history. NODE_PROFILE=full keeps every block with state pruned to the validator's
# window — what an explorer needs, at a fraction of an archive's disk.
# NODE_PROFILE=validator keeps what consensus and safety require and discards the
# rest. An unrecognised value is rejected rather than treated as a validator.
#
# Two constraints worth knowing before changing a number here. pruning-keep-recent
# must exceed snapshot-interval x snapshot-keep-recent, or the versions a
# snapshot needs are pruned before it is taken (the SDK does not check this for
# you); 100,000 > 1,000 x 2 holds with room. And min-retain-blocks cannot go
# below the evidence window whatever is set — baseapp.GetBlockRetentionHeight
# takes the minimum of the evidence, snapshot and local constraints, so
# consensus safety wins over disk. It is set to the window explicitly so the
# intent is legible rather than implied.
APP_TOML="${HOME_DIR}/config/app.toml"
UNBONDING_SECONDS=1814400                    # 21 days, matches staking unbonding_time (V-5)
EVIDENCE_BLOCKS=$(( UNBONDING_SECONDS / 5 )) # 362,880 at a 5s block
NODE_PROFILE="${NODE_PROFILE:-validator}"
case "${NODE_PROFILE}" in
  archive)
    sed -i 's#^pruning = .*#pruning = "nothing"#' "${APP_TOML}"
    sed -i 's#^min-retain-blocks = .*#min-retain-blocks = 0#' "${APP_TOML}"
    ;;
  full)
    # The validator's state window with the archive's block retention. No new
    # number: 100,000 is already justified against the snapshot constraint below,
    # and inheriting it is what lets this profile ship before the disk measurement
    # rather than after it.
    sed -i 's#^pruning = .*#pruning = "custom"#' "${APP_TOML}"
    sed -i 's#^pruning-keep-recent = .*#pruning-keep-recent = "100000"#' "${APP_TOML}"
    sed -i 's#^pruning-interval = .*#pruning-interval = "10"#' "${APP_TOML}"
    sed -i 's#^min-retain-blocks = .*#min-retain-blocks = 0#' "${APP_TOML}"
    ;;
  validator)
    sed -i 's#^pruning = .*#pruning = "custom"#' "${APP_TOML}"
    sed -i 's#^pruning-keep-recent = .*#pruning-keep-recent = "100000"#' "${APP_TOML}"
    sed -i 's#^pruning-interval = .*#pruning-interval = "10"#' "${APP_TOML}"
    sed -i "s#^min-retain-blocks = .*#min-retain-blocks = ${EVIDENCE_BLOCKS}#" "${APP_TOML}"
    ;;
  *)
    # Fail rather than fall through. The previous two-value form made every
    # unrecognised value a validator, so NODE_PROFILE=ful ran a different node than
    # the deployment asked for and said nothing.
    echo "[entrypoint-node] unknown NODE_PROFILE '"'"'${NODE_PROFILE}'"'"' - expected validator, full or archive" >&2
    exit 1
    ;;
esac
sed -i 's#^snapshot-interval = .*#snapshot-interval = 1000#' "${APP_TOML}"
sed -i 's#^snapshot-keep-recent = .*#snapshot-keep-recent = 2#' "${APP_TOML}"
echo "[entrypoint-node] retention profile: ${NODE_PROFILE}"

PEERS="$(cat "${PEERS_FILE}")"

# ── exposure profile (ADR-0015) ─────────────────────────────────────────────
# Retention is how much a node keeps. Exposure is what it faces, and it is a
# different axis: a validator's job is to sign, so public query load and a
# gossiped address are risks independent of what that node stores.
#
# NODE_EXPOSURE=open (default) — pex on, nothing private. The SDK default, and
#                                right for a single operator's test network.
# NODE_EXPOSURE=validator      — pex off, and peers only the sentries named in
#                                SENTRY_PEERS_FILE. The node is reachable only
#                                through machines its own operator runs (D1).
# NODE_EXPOSURE=sentry         — pex on, with the validator in private_peer_ids
#                                so its address is used but never advertised (D2).
#                                A sentry that omits this defeats the whole
#                                layout while looking correct.
CONFIG_TOML="${HOME_DIR}/config/config.toml"
NODE_EXPOSURE="${NODE_EXPOSURE:-open}"
case "${NODE_EXPOSURE}" in
  open)
    ;;
  validator)
    : "${SENTRY_PEERS_FILE:?NODE_EXPOSURE=validator needs SENTRY_PEERS_FILE}"
    if [ ! -s "${SENTRY_PEERS_FILE}" ]; then
      echo "[entrypoint-node] FATAL: ${SENTRY_PEERS_FILE} is missing or empty — a validator with pex off and no sentries is partitioned, not protected" >&2
      exit 1
    fi
    PEERS="$(cat "${SENTRY_PEERS_FILE}")"
    sed -i 's#^pex = .*#pex = false#' "${CONFIG_TOML}"
    ;;
  sentry)
    : "${PRIVATE_PEER_IDS_FILE:?NODE_EXPOSURE=sentry needs PRIVATE_PEER_IDS_FILE}"
    if [ ! -s "${PRIVATE_PEER_IDS_FILE}" ]; then
      echo "[entrypoint-node] FATAL: ${PRIVATE_PEER_IDS_FILE} is missing or empty — a sentry without private_peer_ids gossips the address it exists to hide" >&2
      exit 1
    fi
    PRIVATE_IDS="$(cat "${PRIVATE_PEER_IDS_FILE}")"
    sed -i 's#^pex = .*#pex = true#' "${CONFIG_TOML}"
    sed -i "s#^private_peer_ids = .*#private_peer_ids = \"${PRIVATE_IDS}\"#" "${CONFIG_TOML}"
    ;;
  *)
    echo "[entrypoint-node] unknown NODE_EXPOSURE '\''${NODE_EXPOSURE}'\'' - expected open, validator or sentry" >&2
    exit 1
    ;;
esac
echo "[entrypoint-node] exposure profile: ${NODE_EXPOSURE}"

# REST API on node0 only: it is the one node docker-compose.testnet.yml publishes 1317
# for (the explorer and api.saicon.world read it), and the browser explorer needs CORS
# there. Every other node exposes no REST. In the sentry overlay node0 is the hidden
# validator, which exposes nothing (ADR-0015), so there it stays off too.
# app.toml settings, applied on every start.
if [ "${NODE_INDEX}" = "0" ] && [ "${NODE_EXPOSURE:-open}" != "validator" ]; then
  sed -i '/^\[api\]/,/^\[/{s/^enable = false/enable = true/}' "${APP_TOML}"
  sed -i 's#^address = "tcp://localhost:1317"#address = "tcp://0.0.0.0:1317"#' "${APP_TOML}"
  sed -i '/^\[api\]/,/^\[/{s/^enabled-unsafe-cors = false/enabled-unsafe-cors = true/}' "${APP_TOML}"
fi
# Both directions are written on every start: a data volume first run as an open
# node0 carries enable = true, and switching it to the validator profile must turn
# REST back off rather than inherit it.
if [ "${NODE_EXPOSURE:-open}" = "validator" ]; then
  sed -i '/^\[api\]/,/^\[/{s/^enable = true/enable = false/}' "${APP_TOML}"
fi

# RPC: the hidden validator listens on loopback only, so nothing outside its own
# container reaches it — not the host, even though the base compose file publishes
# 26657 for node0 (Docker forwards to the container's address, where nothing
# listens), and not the other containers. Health checks against it use 127.0.0.1,
# not localhost: with IPv6 on loopback, localhost resolves to ::1 first, and a
# 127.0.0.1 listener does not answer there. Every other node binds 0.0.0.0 so the
# host and its peers can query it.
RPC_LADDR="tcp://0.0.0.0:26657"
if [ "${NODE_EXPOSURE:-open}" = "validator" ]; then
  RPC_LADDR="tcp://127.0.0.1:26657"
fi

echo "[entrypoint-node] starting node${NODE_INDEX} with peers: ${PEERS}"

# --address is the ABCI/app address; comet ignores its own id in persistent_peers.
exec saicond start \
  --home "${HOME_DIR}" \
  --minimum-gas-prices 0nscn \
  --p2p.persistent_peers "${PEERS}" \
  --rpc.laddr "${RPC_LADDR}" \
  --address tcp://0.0.0.0:26658
