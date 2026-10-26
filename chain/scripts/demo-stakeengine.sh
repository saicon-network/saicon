#!/usr/bin/env bash
# demo-stakeengine.sh — S6: the three stake limits and the queue, on a live node.
#   A. strict 2% rule (floor 0): a delegation over the per-validator cap is refused,
#      a new validator below 2,000 SCN is refused, a queued entry waits and is cancelled.
#   B. devnet floor (2M SCN): the global cap refuses a delegation, the queue fills
#      what fits, an undelegation frees room, the queue fills again, cancel returns the rest.
# Every step asserts; exit != 0 on any mismatch.
set -euo pipefail

CHAIN_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
BIN="docker exec saicon-node saicond"
KEYFLAGS=(--keyring-backend test --home /home/saicon/.saicon)
CHAIN_ID="saicon-localnet-1"
TXFLAGS=(--chain-id "$CHAIN_ID" --fees 0nscn -y)
SCN=100000000

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

# proto3 JSON omits zero values: a queue entry with id 0 has no "id" key, and an
# empty list has no "entries" key — hence the `// "0"` and `// []` guards below.

# Run a query, print it to stderr, return the JSON on stdout.
q() {
  runq "$*" >&2
  local out
  out=$($BIN "$@" -o json 2>&1) || { printf '%s\n' "$out" >&2; fail "query failed: $*"; }
  printf '%s\n' "$out" >&2
  printf '%s\n' "$out"
}

# Broadcast a tx and wait for it to land with code 0. Sets TX_HEIGHT to the block
# that included it; state queried --height TX_HEIGHT includes that block's EndBlock.
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

start() {
  cd "$CHAIN_DIR"
  docker compose down -v
  STAKEENGINE_CAP_FLOOR="$1" EPOCH_LENGTH_BLOCKS=10 docker compose up -d --build
  wait_height 2
  VALOPER=$($BIN keys show validator "${KEYFLAGS[@]}" --bech val -a)
  $BIN keys add alice "${KEYFLAGS[@]}" >/dev/null 2>&1
  ALICE=$($BIN keys show alice "${KEYFLAGS[@]}" -a)
  tx tx bank send validator "$ALICE" "$(nscn 1000000)nscn" --from validator "${KEYFLAGS[@]}" "${TXFLAGS[@]}"
}

# ---------------------------------------------------------------------------
step "A0: localnet with the strict 2% rule (validator_cap_floor = 0)"
start 0

step "A1: the entry floor is 2,000 SCN, reported by the chain"
eq "$(q q stakeengine params | jq -r .entry_floor)" "$(nscn 2000)" "entry_floor (nscn)"

step "A2: the caps — 1.68M global, 2% of 1M bonded per validator"
CAPS=$(q q stakeengine caps)
eq "$(jq -r .cap_state.global_cap <<<"$CAPS")" "$(nscn 1680000)" "global_cap"
eq "$(jq -r .cap_state.validator_cap <<<"$CAPS")" "$(nscn 20000)" "validator_cap"
eq "$(jq -r .headroom <<<"$CAPS")" "$(nscn 680000)" "headroom"

step "A3: a delegation to a validator over its cap is refused"
refused "validator cap reached" \
  tx staking delegate "$VALOPER" "$(nscn 1000)nscn" --from alice "${KEYFLAGS[@]}" "${TXFLAGS[@]}"

step "A4: a new validator below 2,000 SCN of its own is refused"
# The node's own consensus key: the ante refuses the amount before x/staking
# would ever look at the key.
PUBKEY=$($BIN comet show-validator --home /home/saicon/.saicon)
jq -n --argjson pk "$PUBKEY" --arg amt "$(nscn 1999)nscn" \
  '{pubkey: $pk, amount: $amt, moniker: "newcomer", "commission-rate": "0.1",
    "commission-max-rate": "0.2", "commission-max-change-rate": "0.01", "min-self-delegation": "1"}' \
  | docker exec -i saicon-node sh -c 'cat > /tmp/newval.json'
refused "below the entry floor" \
  tx staking create-validator /tmp/newval.json --from alice "${KEYFLAGS[@]}" "${TXFLAGS[@]}"

step "A5: queued stake for a full validator waits, and cancelling returns all of it"
tx tx stakeengine enqueue "$VALOPER" "$(nscn 1000)nscn" --from alice "${KEYFLAGS[@]}" "${TXFLAGS[@]}"
next_block; next_block
ENTRY=$(q q stakeengine delegator-queue "$ALICE" | jq -c '.entries[0]')
eq "$(jq -r .amount <<<"$ENTRY")" "$(nscn 1000)" "queued amount after two blocks"
BEFORE=$(bal "$ALICE")
tx tx stakeengine cancel "$(jq -r '.id // "0"' <<<"$ENTRY")" --from alice "${KEYFLAGS[@]}" "${TXFLAGS[@]}"
eq "$(( $(bal "$ALICE") - BEFORE ))" "$(nscn 1000)" "alice got back exactly the 1,000 SCN left"
eq "$(q q stakeengine queue | jq -r '(.entries // []) | length')" "0" "queue length"

# ---------------------------------------------------------------------------
step "B0: localnet with the devnet floor (2,000,000 SCN)"
start "$(nscn 2000000)"
eq "$(q q stakeengine caps | jq -r .cap_state.validator_cap)" "$(nscn 2000000)" "validator_cap"

step "B1: 50,000 SCN fits; 700,000 more does not — the refusal names the queue"
tx tx staking delegate "$VALOPER" "$(nscn 50000)nscn" --from alice "${KEYFLAGS[@]}" "${TXFLAGS[@]}"
refused "MsgEnqueueDelegation" \
  tx staking delegate "$VALOPER" "$(nscn 700000)nscn" --from alice "${KEYFLAGS[@]}" "${TXFLAGS[@]}"

step "B2: the queue takes the 700,000 and fills the 630,000 that fit, in the same block"
tx tx stakeengine enqueue "$VALOPER" "$(nscn 700000)nscn" --from alice "${KEYFLAGS[@]}" "${TXFLAGS[@]}"
# Queried at the enqueue's own height: the fill ran in that block's EndBlock.
ENTRY=$(q q stakeengine delegator-queue "$ALICE" --height "$TX_HEIGHT" | jq -c '.entries[0]')
ID=$(jq -r '.id // "0"' <<<"$ENTRY")
eq "$(jq -r .amount <<<"$ENTRY")" "$(nscn 70000)" "left in the queue at height ${TX_HEIGHT}"
eq "$(q q stakeengine caps | jq -r .headroom)" "0" "headroom"

step "B3: bonding did not shrink public circulating supply (the 37.5% trap)"
PC=$(q q stakeengine caps | jq -r .cap_state.public_circulating)
[ "$PC" -ge "$(nscn 9990000)" ] && ok "public_circulating ${PC} nscn, bonded stake still counted" \
  || fail "public_circulating ${PC} nscn dropped with bonding"

step "B4: an undelegation frees 50,000 SCN; the queue fills it at the end of a block"
tx tx staking unbond "$VALOPER" "$(nscn 50000)nscn" --from alice "${KEYFLAGS[@]}" "${TXFLAGS[@]}"
eq "$(q q stakeengine queue-entry "$ID" --height "$TX_HEIGHT" | jq -r .entry.amount)" "$(nscn 20000)" "left in the queue at height ${TX_HEIGHT}"

step "B5: cancelling returns exactly the 20,000 left, and the escrow is empty (V-10)"
ESCROW=$(q q auth module-account stakeengine | jq -r '[.. | objects | .address? // empty][0]')
[ -n "$ESCROW" ] && [ "$ESCROW" != "null" ] || fail "no address in the stakeengine module account"
eq "$(bal "$ESCROW")" "$(nscn 20000)" "escrow holds exactly the 20,000 left, before the cancel"
BEFORE=$(bal "$ALICE")
tx tx stakeengine cancel "$ID" --from alice "${KEYFLAGS[@]}" "${TXFLAGS[@]}"
eq "$(( $(bal "$ALICE") - BEFORE ))" "$(nscn 20000)" "alice got back exactly the 20,000 left"
eq "$(q q stakeengine queue | jq -r '(.entries // []) | length')" "0" "queue length"
eq "$(bal "$ESCROW")" "0" "escrow balance"

step "B6: the chain kept producing blocks throughout — V-10 never fired"
next_block
ok "S6 demo complete"
