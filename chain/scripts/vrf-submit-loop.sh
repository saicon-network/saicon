#!/usr/bin/env bash
# Submit this validator's VRF output once per round, as early in the window as
# possible (W2 spec K19). Every value comes from the environment, so the same
# script serves the devnet image, a rehearsal and the localnet demo:
#
#   CHAIN_ID    required
#   SAICOND_BIN the binary                       (default: the node home's cosmovisor
#               current binary when there is one, so the loop follows an upgrade
#               Cosmovisor took; otherwise saicond)
#   SAICON_HOME the node home, also the keyring  (default /home/saicon/.saicon)
#   VRF_KEY_FILE                                 (default $SAICON_HOME/config/vrf_key.json)
#   FROM        the validator's key name         (default validator)
#   KEYRING     keyring backend                  (default test)
#   NODE        RPC endpoint                     (default tcp://localhost:26657)
#   INTERVAL    seconds between attempts         (default 30)
#   FEES        e.g. 0nscn                       (default none)
#
# submit-vrf exits 0 without a transaction when there is nothing to do (the window
# is not open, this validator is not designated, its entity already submitted), so
# running it every INTERVAL seconds submits once per round. A failure, such as the
# simulation refused in the block before the window opens, is logged and retried.
# The key file is read by saicond only; this script never prints it.
set -uo pipefail   # not -e: one failed submission must not end the loop
: "${CHAIN_ID:?CHAIN_ID is required}"
HOME_DIR="${SAICON_HOME:-/home/saicon/.saicon}"
CURRENT_BIN="${HOME_DIR}/cosmovisor/current/bin/saicond"
# Chosen again on every round: the submitter may start before the node's entrypoint has
# seeded cosmovisor/, and the binary behind current changes when an upgrade is taken.
pick_bin() {
  if [ -n "${SAICOND_BIN:-}" ]; then BIN="${SAICOND_BIN}"
  elif [ -x "${CURRENT_BIN}" ]; then BIN="${CURRENT_BIN}"
  else BIN=saicond
  fi
  [ "${BIN}" = "${LAST_BIN:-}" ] || echo "[vrf-submit-loop] using ${BIN}" >&2
  LAST_BIN="${BIN}"
}
KEY_FILE="${VRF_KEY_FILE:-${HOME_DIR}/config/vrf_key.json}"
FEE_ARGS=(); [ -n "${FEES:-}" ] && FEE_ARGS=(--fees "${FEES}")
while true; do
  pick_bin
  "${BIN}" tx randomness submit-vrf --vrf-key-file "${KEY_FILE}" --home "${HOME_DIR}" \
    --from "${FROM:-validator}" --keyring-backend "${KEYRING:-test}" --chain-id "${CHAIN_ID}" \
    --node "${NODE:-tcp://localhost:26657}" --gas auto --gas-adjustment 1.5 ${FEE_ARGS[@]+"${FEE_ARGS[@]}"} -y \
    || echo "[vrf-submit-loop] submit-vrf failed; retrying in ${INTERVAL:-30}s" >&2
  sleep "${INTERVAL:-30}"
done
