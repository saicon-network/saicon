#!/usr/bin/env bash
# demo-entityregistry.sh — S8: the entity registry and the activation gate, on a live node.
#   1. the devnet genesis already carries one attested entity for its one validator
#   2. a second validator bonds in consensus but is HELD out of the PoP set (BONDING)
#   3. the attestor attests it -> ACTIVE, in the same block's EndBlock
#   4. a third validator in the same entity is refused a third seat (limit 2)
#   5. the entity's stake against its 5% aggregate cap
#   6. re-attest, move the third validator to its own entity, revoke it -> frozen seat
# Every step asserts; exit != 0 on any mismatch.
#
# Runs against the single-node compose (docker exec saicon-node). DEMO_HOME=<dir> runs it
# against a node started natively from docker/entrypoint-singlenode.sh with that
# SAICON_HOME instead (saicond on PATH; nothing is started or reset by this script).
set -euo pipefail

CHAIN_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
CHAIN_ID="saicon-localnet-1"
SCN=100000000
# create-validator and attestation txs read more state than the 200,000 default allows.
TXFLAGS=(--chain-id "$CHAIN_ID" --fees 0nscn --gas 600000 -y)

if [ -n "${DEMO_HOME:-}" ]; then
  BIN=(saicond)
  KEYFLAGS=(--keyring-backend test --home "$DEMO_HOME")
  SCRATCH="$DEMO_HOME"
  put_file() { cat > "$1"; }                 # stdin -> file on the node's side
else
  BIN=(docker exec saicon-node saicond)
  KEYFLAGS=(--keyring-backend test --home /home/saicon/.saicon)
  SCRATCH=/tmp
  put_file() { docker exec -i saicon-node sh -c "cat > $1"; }
fi

source "$(dirname "${BASH_SOURCE[0]}")/lib-narrate.sh"

nscn() { echo $(( $1 * SCN )); }

height() { curl -s localhost:26657/status | jq -r .result.sync_info.latest_block_height; }

wait_height() {
  local target="$1" h=""
  for _ in $(seq 1 240); do
    h=$(height 2>/dev/null || echo 0)
    if [ "${h:-0}" -ge "$target" ] 2>/dev/null; then
      ok "reached height >= ${target} (current: ${h})"
      return 0
    fi
    sleep 2
  done
  fail "timed out waiting for height ${target} (last seen: ${h:-none})"
}

next_block() { wait_height $(( $(height) + 1 )); }

# proto3 JSON omits zero values (a false bool, a zero count, an empty list):
# the `// false` and `// 0` guards below stand in for them.

# Run a query, print it to stderr, return the JSON on stdout.
q() {
  runq "$*" >&2
  local out
  out=$("${BIN[@]}" "$@" -o json 2>&1) || { printf '%s\n' "$out" >&2; fail "query failed: $*"; }
  printf '%s\n' "$out" >&2
  printf '%s\n' "$out"
}

# Broadcast a tx and wait for it to land with code 0. Sets TX_HEIGHT to the block
# that included it; state queried --height TX_HEIGHT includes that block's EndBlock.
tx() {
  runq "$*" >&2
  local out code hash tx_out tx_code
  out=$("${BIN[@]}" "$@" --output json 2>&1) || { printf '%s\n' "$out" >&2; fail "tx broadcast failed: $*"; }
  code=$(printf '%s' "$out" | jq -r '.code // empty')
  [ "$code" = "0" ] || fail "tx refused at CheckTx: $(printf '%s' "$out" | jq -r '.raw_log // "unknown"')"
  hash=$(printf '%s' "$out" | jq -r '.txhash')
  for _ in $(seq 1 15); do
    tx_out=$("${BIN[@]}" q tx "$hash" -o json 2>/dev/null || true)
    tx_code=$(printf '%s' "$tx_out" | jq -r '.code // empty' 2>/dev/null || true)
    [ -n "$tx_code" ] && break
    sleep 2
  done
  [ "${tx_code:-}" = "0" ] || fail "tx $hash did not land with code 0 (code: ${tx_code:-none}, $(printf '%s' "$tx_out" | jq -r '.raw_log // ""' 2>/dev/null))"
  TX_HEIGHT=$(printf '%s' "$tx_out" | jq -r '.height')
  ok "tx $hash included with code 0 at height ${TX_HEIGHT}"
}

eq() { [ "$1" = "$2" ] && ok "$3: $1" || fail "$3: got $1, want $2"; }

# The registry status, the staking status and the entity link of one validator.
reg_status() { q q validatorregistry validator "$1" | jq -r .validator.status; }
stk_status() { q q staking validator "$1" | jq -r '.validator.status // .status'; }
ve() { q q entityregistry validator-entity "$1"; }

# A validator.json with a fresh consensus key. The key never signs: the demo needs a
# validator in the staking set, not a second node, and 2,000 SCN against 1,000,000 SCN is
# 0.2% of the voting power.
validator_json() { # file moniker amount_nscn
  local pk; pk=$(head -c 32 /dev/urandom | base64)
  jq -n --arg pk "$pk" --arg m "$2" --arg amt "$3" \
    '{pubkey: {"@type": "/cosmos.crypto.ed25519.PubKey", key: $pk}, amount: $amt, moniker: $m,
      "commission-rate": "0.1", "commission-max-rate": "0.2", "commission-max-change-rate": "0.01",
      "min-self-delegation": "1"}' | put_file "$1"
}

# ---------------------------------------------------------------------------
step "0: a fresh single-node localnet (genesis from docker/entrypoint-singlenode.sh)"
if [ -z "${DEMO_HOME:-}" ]; then
  cd "$CHAIN_DIR"
  docker compose down -v
  docker compose up -d --build
fi
wait_height 3
VALOPER=$("${BIN[@]}" keys show validator "${KEYFLAGS[@]}" --bech val -a)
ATTESTOR=$("${BIN[@]}" keys show attestor "${KEYFLAGS[@]}" -a)
ok "validator ${VALOPER}, attestor ${ATTESTOR}"

step "1: the genesis already attested the operator's entity — one entity, one ticket"
TICKETS=$(q q entityregistry tickets)
eq "$(jq -r '.tickets | length' <<<"$TICKETS")" "1" "tickets"
eq "$(jq -r '.tickets[0].entity_id' <<<"$TICKETS")" "saicon-devnet-operator" "ticket entity"
eq "$(jq -r '.tickets[0].active_seats // 0' <<<"$TICKETS")" "1" "active seats"
ENTITY=$(q q entityregistry entity saicon-devnet-operator)
HASH=$(jq -r '.entity.attestation_hash' <<<"$ENTITY")
eq "${#HASH}" "64" "attestation hash length"
eq "$(q q entityregistry params | jq -r --arg a "$ATTESTOR" '.params.attestors | index($a) != null')" "true" "attestor is in params"

step "2: a second validator bonds — and is held out of the PoP set until it is attested"
for k in second third; do
  "${BIN[@]}" keys add "$k" "${KEYFLAGS[@]}" --no-backup >/dev/null 2>&1
done
SECOND=$("${BIN[@]}" keys show second "${KEYFLAGS[@]}" -a)
THIRD=$("${BIN[@]}" keys show third "${KEYFLAGS[@]}" -a)
SECOND_VAL=$("${BIN[@]}" keys show second "${KEYFLAGS[@]}" --bech val -a)
THIRD_VAL=$("${BIN[@]}" keys show third "${KEYFLAGS[@]}" --bech val -a)
tx tx bank send validator "$SECOND" "$(nscn 5000)nscn" --from validator "${KEYFLAGS[@]}" "${TXFLAGS[@]}"
tx tx bank send validator "$THIRD" "$(nscn 5000)nscn" --from validator "${KEYFLAGS[@]}" "${TXFLAGS[@]}"

validator_json "${SCRATCH}/second.json" second "$(nscn 2000)nscn"
tx tx staking create-validator "${SCRATCH}/second.json" --from second "${KEYFLAGS[@]}" "${TXFLAGS[@]}"
next_block; next_block
eq "$(ve "$SECOND_VAL" | jq -r '.pending // false')" "true" "second is pending (held by the gate)"
eq "$(reg_status "$SECOND_VAL")" "STATUS_BONDING" "registry status of second"
eq "$(stk_status "$SECOND_VAL")" "BOND_STATUS_BONDED" "staking status of second"
ok "bonded in consensus, not active in PoP: the selection layer closes this (spec §3)"

step "3: the attestor attests the entity with both validators -> second is ACTIVE"
tx tx entityregistry attest saicon-devnet-operator "SAiCoN DevNet operator" "$HASH" \
  "$VALOPER" "$SECOND_VAL" --from attestor "${KEYFLAGS[@]}" "${TXFLAGS[@]}"
next_block
eq "$(reg_status "$SECOND_VAL")" "STATUS_ACTIVE" "registry status of second"
eq "$(ve "$SECOND_VAL" | jq -r '.pending // false')" "false" "second is no longer pending"

step "4: a third validator in the same entity is refused a third seat (max_active_seats = 2)"
tx tx entityregistry attest saicon-devnet-operator "SAiCoN DevNet operator" "$HASH" \
  "$VALOPER" "$SECOND_VAL" "$THIRD_VAL" --from attestor "${KEYFLAGS[@]}" "${TXFLAGS[@]}"
validator_json "${SCRATCH}/third.json" third "$(nscn 2000)nscn"
tx tx staking create-validator "${SCRATCH}/third.json" --from third "${KEYFLAGS[@]}" "${TXFLAGS[@]}"
CREATE_HEIGHT=$TX_HEIGHT
next_block; next_block; next_block
eq "$(reg_status "$THIRD_VAL")" "STATUS_BONDING" "registry status of third"
eq "$(stk_status "$THIRD_VAL")" "BOND_STATUS_BONDED" "staking status of third"
# No query returns the hold reason. It is in the validator_activation_held event of the
# block whose EndBlock bonded the validator: a block event, not a transaction event, so
# `q txs` will not find it. CometBFT has it.
WANT="entity saicon-devnet-operator already holds 2 active seats; the limit is 2"
REASON=""
for h in $(seq "$CREATE_HEIGHT" $(( CREATE_HEIGHT + 3 ))); do
  r=$(curl -s "localhost:26657/block_results?height=$h" \
    | jq -r '.result.finalize_block_events[]? | select(.type=="validator_activation_held") | .attributes[] | select(.key=="reason") | .value')
  if [ -n "$r" ]; then runq "block_results?height=$h -> validator_activation_held" >&2; printf '%s\n' "$r" >&2; REASON="$r"; fi
done
eq "$REASON" "$WANT" "hold reason from the block's events"
ENTITY=$(q q entityregistry entity saicon-devnet-operator)
eq "$(jq -r '.active_seats // 0' <<<"$ENTITY")" "2" "active seats of the entity"

step "5: the entity's stake against its aggregate cap"
STAKE=$(q q stakeengine entity-stake saicon-devnet-operator)
ok "tokens $(jq -r '.tokens // "0"' <<<"$STAKE") nscn, entity cap $(jq -r '.entity_cap // "0"' <<<"$STAKE") nscn, room $(jq -r '.room // "0"' <<<"$STAKE") nscn"
[ "$(jq -r '.entity_cap // "0"' <<<"$STAKE")" != "0" ] || fail "the entity cap is zero"

step "6: free the third seat, give third its own entity, then revoke that entity"
# The operator's entity is not revoked here: that would freeze the only validator's intake.
tx tx entityregistry attest saicon-devnet-operator "SAiCoN DevNet operator" "$HASH" \
  "$VALOPER" "$SECOND_VAL" --from attestor "${KEYFLAGS[@]}" "${TXFLAGS[@]}"
THIRD_HASH=$(printf '%s' "SAiCoN demo third entity" | (sha256sum 2>/dev/null || shasum -a 256) | cut -d' ' -f1)
tx tx entityregistry attest demo-third "Demo third" "$THIRD_HASH" "$THIRD_VAL" \
  --from attestor "${KEYFLAGS[@]}" "${TXFLAGS[@]}"
next_block
eq "$(reg_status "$THIRD_VAL")" "STATUS_ACTIVE" "registry status of third under its own entity"
eq "$(ve "$THIRD_VAL" | jq -r .entity_id)" "demo-third" "third's entity"
tx tx entityregistry revoke demo-third "demo finished" --from attestor "${KEYFLAGS[@]}" "${TXFLAGS[@]}"
VE=$(ve "$THIRD_VAL")
eq "$(jq -r '.attested // false' <<<"$VE")" "false" "third has no attested entity after the revoke"
if [ "$(jq -r '.frozen != null' <<<"$VE")" = "true" ]; then
  ok "third keeps its seat, frozen: it takes no new stake"
else
  fail "no frozen seat for third after the revoke"
fi

step "7: the chain kept producing blocks throughout — V-12 never fired"
next_block
ok "S8 entity registry demo complete"
