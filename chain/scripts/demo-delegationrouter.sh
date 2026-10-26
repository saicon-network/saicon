#!/usr/bin/env bash
# demo-delegationrouter.sh — S7: the delegation router on the 3-validator testnet.
#   1. preview a 250,000 SCN delegation with `q delegationrouter route`
#   2. a plain MsgDelegate of the same amount is refused, with a hint naming MsgDelegateRouted
#   3. `tx delegationrouter delegate` spreads it over three validators
#   4. a fourth validator for the same delegator is refused
#   5. a gov proposal delegating past a cap ends PROPOSAL_STATUS_FAILED; the chain keeps going
# Every step asserts; exit != 0 on any mismatch.
#
# Needs a FRESH 3-validator compose (the run is not repeatable on one chain), started with a short epoch and gov period:
#   EPOCH_LENGTH_BLOCKS=10 GOV_VOTING_PERIOD=20s \
#     docker compose -f docker-compose.testnet.yml up -d --build
# (the router only sends overflow to validators scored in a closed epoch, and the gov
# step waits for the vote to end).
#
# Environment (defaults match docker-compose.testnet.yml):
#   RPC_URL=http://localhost:26657   REST_URL=http://localhost:1317
#   NODE_CONTAINER=saicon-node0      the container whose saicond signs and queries
set -euo pipefail

RPC_URL="${RPC_URL:-http://localhost:26657}"
REST_URL="${REST_URL:-http://localhost:1317}"
NODE_CONTAINER="${NODE_CONTAINER:-saicon-node0}"
BIN="docker exec ${NODE_CONTAINER} saicond"
KEYFLAGS=(--keyring-backend test --keyring-dir /data/keyring --home /data/node0)
CHAIN_ID="${CHAIN_ID:-saicon-testnet-1}"
TXFLAGS=(--chain-id "$CHAIN_ID" --fees 0nscn -y)
SCN=100000000

source "$(dirname "${BASH_SOURCE[0]}")/lib-narrate.sh"

nscn() { echo $(( $1 * SCN )); }

height() { curl -s "${RPC_URL}/status" | jq -r .result.sync_info.latest_block_height; }

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

# Run a query, print it to stderr, return the output on stdout.
q() {
  runq "$*" >&2
  local out
  out=$($BIN "$@" -o json 2>&1) || { printf '%s\n' "$out" >&2; fail "query failed: $*"; }
  printf '%s\n' "$out" >&2
  printf '%s\n' "$out"
}

# Broadcast a tx and wait for it to land with code 0. Sets TX_HEIGHT.
tx() {
  runq "$*" >&2
  local out code hash tx_out tx_code
  out=$($BIN "$@" --output json 2>&1) || { printf '%s\n' "$out" >&2; fail "tx broadcast failed: $*"; }
  code=$(printf '%s' "$out" | jq -r '.code // empty')
  [ "$code" = "0" ] || fail "tx refused at CheckTx: $(printf '%s' "$out" | jq -r '.raw_log // "unknown"')"
  hash=$(printf '%s' "$out" | jq -r '.txhash')
  for _ in $(seq 1 15); do
    tx_out=$($BIN q tx "$hash" -o json 2>/dev/null || true)
    tx_code=$(printf '%s' "$tx_out" | jq -r '.code // empty' 2>/dev/null || true)
    [ -n "$tx_code" ] && break
    sleep 2
  done
  [ "${tx_code:-}" = "0" ] || fail "tx $hash did not land with code 0 (code: ${tx_code:-none})"
  TX_HEIGHT=$(printf '%s' "$tx_out" | jq -r '.height')
  ok "tx $hash included with code 0 at height ${TX_HEIGHT}"
}

# Broadcast a tx that the ante handler must refuse, and check the reason.
refused() {
  local want="$1"; shift
  runq "$* (expect: ${want})" >&2
  local out
  out=$($BIN "$@" --output json 2>&1 || true)
  printf '%s\n' "$out" >&2
  if printf '%s' "$out" | grep -q "$want"; then
    ok "refused: ${want}"
  else
    fail "expected a refusal containing '${want}'"
  fi
}

bal() { q q bank balance "$1" nscn | jq -r .balance.amount; }

eq() { [ "$1" = "$2" ] && ok "$3: $1" || fail "$3: got $1, want $2"; }

# ---------------------------------------------------------------------------
step "0: the testnet — three validators, caps from genesis"
wait_height 3
VAL0=$($BIN keys show val0 "${KEYFLAGS[@]}" --bech val -a)
VAL0ACC=$($BIN keys show val0 "${KEYFLAGS[@]}" -a)
$BIN keys add alice "${KEYFLAGS[@]}" >/dev/null 2>&1 || true
ALICE=$($BIN keys show alice "${KEYFLAGS[@]}" -a)
eq "$(curl -s "${REST_URL}/cosmos/staking/v1beta1/validators" | jq '.validators | length')" "3" "validators"
CAPS=$(q q stakeengine caps)
eq "$(jq -r .cap_state.validator_cap <<<"$CAPS")" "$(nscn 400000)" "validator_cap"
eq "$(jq -r .total_bonded <<<"$CAPS")" "$(nscn 900000)" "total_bonded"
# Fund the delegator, and show how far in the chain is.
tx tx bank send val0 "$ALICE" "$(nscn 400000)nscn" --from val0 "${KEYFLAGS[@]}" "${TXFLAGS[@]}"

step "1: the route preview — 250,000 SCN to node0's validator"
# The router sends overflow only to validators scored in a closed epoch, so wait
# for the first one to close. The preview comes back with `legs` once it has.
for _ in $(seq 1 90); do
  PLAN=$($BIN q delegationrouter route "$ALICE" "$VAL0" "$(nscn 250000)" -o json 2>/dev/null || true)
  [ -n "$PLAN" ] && [ "$(jq -r '(.legs // []) | length' <<<"$PLAN")" -gt 0 ] && break
  sleep 2
done
PLAN=$(q q delegationrouter route "$ALICE" "$VAL0" "$(nscn 250000)")
eq "$(jq -r '.legs | length' <<<"$PLAN")" "3" "legs in the plan"
eq "$(jq -r '[.legs[].amount | tonumber] | add' <<<"$PLAN")" "$(nscn 250000)" "legs sum to 250,000 SCN"

step "2: the same amount as a plain MsgDelegate is refused — with the hint"
refused "MsgDelegateRouted" \
  tx staking delegate "$VAL0" "$(nscn 250000)nscn" --from alice "${KEYFLAGS[@]}" "${TXFLAGS[@]}"

step "3: the routed delegation lands as three delegations"
# Three delegations in one tx: the default 200000 gas is not enough.
tx tx delegationrouter delegate "$VAL0" "$(nscn 250000)nscn" --from alice --gas 1000000 "${KEYFLAGS[@]}" "${TXFLAGS[@]}"
DELS=$(q q staking delegations "$ALICE")
eq "$(jq -r '.delegation_responses | length' <<<"$DELS")" "3" "alice's validators"
eq "$(jq -r '[.delegation_responses[].balance.amount | tonumber] | add' <<<"$DELS")" "$(nscn 250000)" "alice's total delegated"

step "4: a fourth validator for alice is refused"
# A fresh validator: its own key, funded by alice, with the 2,000 SCN entry floor.
# Its consensus key is a throwaway one; the node behind it never runs, which a
# 2,000 SCN validator among 900,000 does not matter to consensus for.
$BIN keys add val3 "${KEYFLAGS[@]}" >/dev/null 2>&1 || true
VAL3ACC=$($BIN keys show val3 "${KEYFLAGS[@]}" -a)
VAL3=$($BIN keys show val3 "${KEYFLAGS[@]}" --bech val -a)
tx tx bank send alice "$VAL3ACC" "$(nscn 5000)nscn" --from alice "${KEYFLAGS[@]}" "${TXFLAGS[@]}"
docker exec "$NODE_CONTAINER" sh -c "rm -rf /tmp/val3home && saicond init val3 --home /tmp/val3home --chain-id $CHAIN_ID >/dev/null 2>&1"
PUBKEY=$($BIN comet show-validator --home /tmp/val3home)
jq -n --argjson pk "$PUBKEY" --arg amt "$(nscn 2000)nscn" \
  '{pubkey: $pk, amount: $amt, moniker: "val3", "commission-rate": "0.1",
    "commission-max-rate": "0.2", "commission-max-change-rate": "0.01", "min-self-delegation": "1"}' \
  | docker exec -i "$NODE_CONTAINER" sh -c 'cat > /tmp/val3.json'
tx tx staking create-validator /tmp/val3.json --from val3 "${KEYFLAGS[@]}" "${TXFLAGS[@]}"
refused "maximum number of validators" \
  tx staking delegate "$VAL3" "$(nscn 1000)nscn" --from alice "${KEYFLAGS[@]}" "${TXFLAGS[@]}"
eq "$(q q staking delegations "$ALICE" | jq -r '.delegation_responses | length')" "3" "alice still holds three"

step "5: a governance proposal that delegates past a cap fails, and the chain keeps going"
GOV=$(q q auth module-account gov | jq -r '[.. | objects | .address? // empty][0]')
[ -n "$GOV" ] && [ "$GOV" != "null" ] || fail "no address in the gov module account"
tx tx bank send alice "$GOV" "$(nscn 20000)nscn" --from alice "${KEYFLAGS[@]}" "${TXFLAGS[@]}"
# val0 is already at its cap after step 3.
jq -n --arg gov "$GOV" --arg val "$VAL0" --arg amt "$(nscn 10000)" '{
  messages: [{"@type": "/cosmos.staking.v1beta1.MsgDelegate",
              delegator_address: $gov, validator_address: $val,
              amount: {denom: "nscn", amount: $amt}}],
  metadata: "", deposit: "100000000nscn",
  title: "Delegate past the cap", summary: "Must fail: the validator is full."}' \
  | docker exec -i "$NODE_CONTAINER" sh -c 'cat > /tmp/prop.json'
tx tx gov submit-proposal /tmp/prop.json --from val0 "${KEYFLAGS[@]}" "${TXFLAGS[@]}"
PID=$(q q gov proposals | jq -r '.proposals[-1].id')
for v in val0 val1 val2; do
  tx tx gov vote "$PID" yes --from "$v" "${KEYFLAGS[@]}" "${TXFLAGS[@]}"
done
STATUS=""
for _ in $(seq 1 60); do
  STATUS=$($BIN q gov proposal "$PID" -o json | jq -r .proposal.status)
  [ "$STATUS" = "PROPOSAL_STATUS_VOTING_PERIOD" ] || break
  sleep 2
done
eq "$STATUS" "PROPOSAL_STATUS_FAILED" "proposal $PID status"
REASON=$(q q gov proposal "$PID" | jq -r .proposal.failed_reason)
case "$REASON" in *"validator cap reached"*) ok "failed because: ${REASON}";; *) fail "failed for another reason: ${REASON}";; esac
H=$(height); next_block; next_block
[ "$(height)" -gt "$H" ] && ok "height still rising: ${H} -> $(height)" || fail "chain stopped at ${H}"
eq "$(q q staking delegations "$GOV" | jq -r '(.delegation_responses // []) | length')" "0" "the gov account delegated nothing"

step "S7 router demo complete"
