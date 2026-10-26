#!/usr/bin/env bash
# Initialise node4 and node5 as the two sentries that stand in front of node0,
# and publish the three facts the layout needs (ADR-0015).
#
# A sentry is an ordinary full node: no gentx, no stake, no seat. What makes it a
# sentry is exposure, not identity — it carries the p2p traffic so the validator
# does not, and it never advertises the validator's address. If this script ever
# needs a validator key to make a sentry work, the layout is not what the ADR
# claims it is.
#
# Two of them, because one is a single point of failure with extra steps: if the
# only sentry goes down the validator is partitioned and stops signing, which is
# the outcome the layout exists to prevent (ADR-0015 D3).
set -euo pipefail

CHAIN_ID="${CHAIN_ID:-saicon-testnet-1}"
SRC_GENESIS="/data/node0/config/genesis.json"
PEERS_FILE="/data/peers.txt"
P2P_PORT=26656

for _ in $(seq 1 60); do
  if [ -f "${SRC_GENESIS}" ] && [ -f "${PEERS_FILE}" ]; then break; fi
  echo "[init-sentries] waiting for the validators' genesis + peers.txt ..."
  sleep 2
done

if [ ! -f "${SRC_GENESIS}" ] || [ ! -f "${PEERS_FILE}" ]; then
  echo "[init-sentries] FATAL: no genesis or peers.txt on the shared volume" >&2
  exit 1
fi

for i in 4 5; do
  HOME_DIR="/data/node${i}"
  if [ ! -f "${HOME_DIR}/config/genesis.json" ]; then
    echo "[init-sentries] initialising node${i} as a sentry (no gentx, no stake)"
    rm -rf "${HOME_DIR}"
    saicond init "node${i}" --chain-id "${CHAIN_ID}" --default-denom nscn --home "${HOME_DIR}"
    cp "${SRC_GENESIS}" "${HOME_DIR}/config/genesis.json"
  fi
  if ! cmp -s "${SRC_GENESIS}" "${HOME_DIR}/config/genesis.json"; then
    echo "[init-sentries] FATAL: node${i}'s genesis differs from the validators'" >&2
    exit 1
  fi
done

# What node0 dials. pex is off on it, so this list is the whole of its view of
# the network — if it is wrong the validator is isolated rather than protected.
SENTRY_PEERS=""
for i in 4 5; do
  NID="$(saicond comet show-node-id --home "/data/node${i}")"
  ENTRY="${NID}@node${i}:${P2P_PORT}"
  if [ -z "${SENTRY_PEERS}" ]; then SENTRY_PEERS="${ENTRY}"; else SENTRY_PEERS="${SENTRY_PEERS},${ENTRY}"; fi
done
printf '%s' "${SENTRY_PEERS}" > /data/sentry-peers.txt

# What the sentries must never gossip. private_peer_ids takes bare ids, not
# id@host — an entry with a host is silently ignored and the address leaks.
printf '%s' "$(saicond comet show-node-id --home /data/node0)" > /data/validator0-id.txt

if [ "$(id -u)" = "0" ]; then
  chown -R 1000:1000 /data/node4 /data/node5 /data/sentry-peers.txt /data/validator0-id.txt
fi

echo "[init-sentries] ready — node0 dials ${SENTRY_PEERS}"
echo "[init-sentries] sentries keep private: $(cat /data/validator0-id.txt)"
