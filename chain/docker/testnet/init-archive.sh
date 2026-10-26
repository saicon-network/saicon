#!/usr/bin/env bash
# Initialise node3 as a NON-validator archive node against the genesis the
# 3-validator testnet already produced.
#
# The point of the exercise is that an archive node is an ordinary full node:
# it gets no gentx, no stake and no seat in the validator set. It dials the same
# peers, replays the same blocks and reaches the same heights — it simply keeps
# everything instead of pruning. If this script ever needs a validator key or a
# genesis edit to make node3 work, the archive profile is not what the docs claim
# it is.
set -euo pipefail

CHAIN_ID="${CHAIN_ID:-saicon-testnet-1}"
HOME_DIR="/data/node3"
SRC_GENESIS="/data/node0/config/genesis.json"
PEERS_FILE="/data/peers.txt"

# genesis-init has run to completion before us, but a node that starts on a
# half-written genesis fails in ways that look like a consensus bug, so guard.
for _ in $(seq 1 60); do
  if [ -f "${SRC_GENESIS}" ] && [ -f "${PEERS_FILE}" ]; then
    break
  fi
  echo "[init-archive] waiting for the validators' genesis + peers.txt ..."
  sleep 2
done

if [ ! -f "${SRC_GENESIS}" ] || [ ! -f "${PEERS_FILE}" ]; then
  echo "[init-archive] FATAL: no genesis or peers.txt on the shared volume" >&2
  exit 1
fi

# Idempotent: a restart reuses the existing home rather than resyncing.
if [ ! -f "${HOME_DIR}/config/genesis.json" ]; then
  echo "[init-archive] initialising node3 as a full node (no gentx, no stake)"
  rm -rf "${HOME_DIR}"
  saicond init node3 --chain-id "${CHAIN_ID}" --default-denom nscn --home "${HOME_DIR}"
  cp "${SRC_GENESIS}" "${HOME_DIR}/config/genesis.json"
fi

# A byte difference here means a different chain: the node would fail to reach
# consensus with a mismatched app hash rather than say what is wrong.
if ! cmp -s "${SRC_GENESIS}" "${HOME_DIR}/config/genesis.json"; then
  echo "[init-archive] FATAL: node3's genesis differs from the validators'" >&2
  exit 1
fi

# The named volume mounts root-owned; the node services run as uid 1000.
if [ "$(id -u)" = "0" ]; then
  chown -R 1000:1000 "${HOME_DIR}"
fi

echo "[init-archive] node3 ready — genesis identical to node0, no validator key in genesis"
