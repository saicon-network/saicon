#!/usr/bin/env bash
# demo-pop-cycle.sh — M1-D: live end-to-end PoP cycle incl. the governed negative path.
#   register → activate → earn (score/EP/reward) → flag (gov) → reward = 0
# Runs against the dockerized single node with short epochs + short gov voting.
# Every step asserts; exit != 0 on any mismatch.
set -euo pipefail

CHAIN_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
EPOCH_LEN="${EPOCH_LEN:-10}"
VOTING="${VOTING:-20s}"
BIN="docker exec saicon-node saicond"
KEYFLAGS=(--keyring-backend test --home /home/saicon/.saicon)
CHAIN_ID="saicon-localnet-1"

source "$(dirname "${BASH_SOURCE[0]}")/lib-narrate.sh"

wait_height() {
  local target="$1" h=""
  # Bounded (~8 min max) so a dead node fails loudly instead of hanging forever.
  for _ in $(seq 1 240); do
    h=$(curl -s localhost:26657/status | jq -r .result.sync_info.latest_block_height 2>/dev/null || echo 0)
    if [ "${h:-0}" -ge "$target" ] 2>/dev/null; then
      ok "reached height >= ${target} (current: ${h})"
      return 0
    fi
    sleep 2
  done
  fail "timed out waiting for height ${target} (last seen: ${h:-none})"
}

# Run a query, print it, and return the JSON on stdout.
# Deliberate double-print: stdout is for the caller's $(...) capture, stderr is
# the live narration the operator watches (same JSON, two audiences).
q() {
  runq "$*" >&2
  local out
  out=$($BIN "$@" -o json 2>&1) || { printf '%s\n' "$out" >&2; fail "query failed: $*"; }
  printf '%s\n' "$out"
  printf '%s\n' "$out" >&2
}

# Broadcast a tx, print it, assert code==0, print the txhash.
# Like q(): narration goes to stderr, only the txhash goes to stdout for capture.
tx() {
  runq "$*" >&2
  local out code
  out=$($BIN "$@" --output json 2>&1) || { printf '%s\n' "$out" >&2; fail "tx broadcast failed: $*"; }
  printf '%s\n' "$out" >&2
  code=$(printf '%s' "$out" | jq -r '.code // empty')
  if [ -z "$code" ] || [ "$code" != "0" ]; then
    fail "tx returned non-zero code: $(printf '%s' "$out" | jq -r '.raw_log // .rawlog // "unknown"')"
  fi
  local hash
  hash=$(printf '%s' "$out" | jq -r '.txhash')
  # Poll for inclusion and confirm code==0 on-chain (broadcast code==0 only means
  # CheckTx passed, not that it was included / DeliverTx succeeded).
  local tx_out tx_code
  for _ in $(seq 1 15); do
    tx_out=$($BIN q tx "$hash" -o json 2>/dev/null || true)
    tx_code=$(printf '%s' "$tx_out" | jq -r '.code // empty' 2>/dev/null || true)
    if [ -n "$tx_code" ]; then
      break
    fi
    sleep 2
  done
  if [ -z "${tx_code:-}" ]; then
    fail "tx $hash never appeared on chain"
  fi
  if [ "$tx_code" != "0" ]; then
    fail "tx $hash included with non-zero code $tx_code: $(printf '%s' "$tx_out" | jq -r '.raw_log // .rawlog // "unknown"')"
  fi
  ok "tx $hash included with code 0"
  printf '%s\n' "$hash"
}

step "Step 0: fresh localnet"
cd "$CHAIN_DIR"
docker compose down -v
EPOCH_LENGTH_BLOCKS="$EPOCH_LEN" GOV_VOTING_PERIOD="$VOTING" docker compose up -d --build
wait_height 2

VALOPER=$($BIN keys show validator "${KEYFLAGS[@]}" --bech val -a)
VALACC=$($BIN keys show validator "${KEYFLAGS[@]}" -a)
printf 'VALOPER=%s\nVALACC=%s\n' "$VALOPER" "$VALACC"

step "Step 1: activation (auto, via M1-A staking hook)"
OUT=$(q q validatorregistry validator "$VALOPER")
STATUS=$(printf '%s' "$OUT" | jq -r '.validator.status')
[ "$STATUS" = "STATUS_ACTIVE" ] || fail "expected STATUS_ACTIVE, got $STATUS"
ok "validator is STATUS_ACTIVE"

step "Step 2: earn (score / EP / reward eligibility)"
wait_height $((EPOCH_LEN * 2 + 1))

q q participation snapshot "$VALOPER" 2 >/dev/null

EP_OUT=$(q q participation ep "$VALOPER")
EP=$(printf '%s' "$EP_OUT" | jq -r '.ep.accumulated_ep')
[ "$EP" -ge 1 ] 2>/dev/null || fail "expected accumulated_ep >= 1, got $EP"
ok "accumulated_ep=$EP"

RS_OUT=$(q q rewards snapshot "$VALOPER" 2)
# NOTE: proto3 JSON (no emitDefaults) omits bool fields when false, so a
# denied/ineligible snapshot has NO "eligible" key at all (not "eligible":false).
# `// false` treats both "missing" and an explicit false the same way.
ELIGIBLE=$(printf '%s' "$RS_OUT" | jq -r '.snapshot.eligible // false')
[ "$ELIGIBLE" = "true" ] || fail "expected eligible == true at epoch 2, got $ELIGIBLE"
ok "epoch 2 reward snapshot eligible=true"

BAL_INFO_OUT=$(q q bank balances "$VALACC")
BAL_INFO=$(printf '%s' "$BAL_INFO_OUT" | jq -r '.balances[] | select(.denom=="nscn") | .amount')
printf 'informational nscn balance (pre-flag, step 2): %s\n' "$BAL_INFO"

step "Step 3: governed flag (negative path via gov)"
GOV_ADDR_OUT=$(q q auth module-account gov)
GOV_ADDR=$(printf '%s' "$GOV_ADDR_OUT" | jq -r '.account.value.address')
[ -n "$GOV_ADDR" ] && [ "$GOV_ADDR" != "null" ] || fail "could not resolve gov module account address"
ok "gov module account: $GOV_ADDR"

GOV_PARAMS_OUT=$(q q gov params)
DEPOSIT_AMOUNT=$(printf '%s' "$GOV_PARAMS_OUT" | jq -r '.params.min_deposit[0].amount')
DEPOSIT_DENOM=$(printf '%s' "$GOV_PARAMS_OUT" | jq -r '.params.min_deposit[0].denom')
[ -n "$DEPOSIT_AMOUNT" ] && [ "$DEPOSIT_AMOUNT" != "null" ] || fail "could not resolve min_deposit amount"
ok "min_deposit=${DEPOSIT_AMOUNT}${DEPOSIT_DENOM}"

PROPOSAL_JSON=$(cat <<EOF
{
  "messages": [
    {
      "@type": "/chain.compliance.v1.MsgSubmitEvidence",
      "authority": "${GOV_ADDR}",
      "validator_address": "${VALOPER}",
      "event": "COMPLIANCE_EVENT_TYPE_EXTERNAL_FLAG",
      "reason": "M1-D demo: governed compliance flag"
    }
  ],
  "metadata": "",
  "deposit": "${DEPOSIT_AMOUNT}${DEPOSIT_DENOM}",
  "title": "M1-D demo: flag validator as non-compliant",
  "summary": "M1-D demo: flag validator as non-compliant via governance"
}
EOF
)
printf '%s' "$PROPOSAL_JSON" | docker exec -i saicon-node tee /tmp/flag.json >/dev/null
printf 'proposal JSON:\n%s\n' "$PROPOSAL_JSON"

tx tx gov submit-proposal /tmp/flag.json --from validator "${KEYFLAGS[@]}" --chain-id "$CHAIN_ID" --fees 0nscn -y >/dev/null

PROPS_OUT=$(q q gov proposals)
PROP_ID=$(printf '%s' "$PROPS_OUT" | jq -r '.proposals[-1].id')
[ -n "$PROP_ID" ] && [ "$PROP_ID" != "null" ] || fail "could not resolve proposal id"
ok "proposal id: $PROP_ID"

tx tx gov vote "$PROP_ID" yes --from validator "${KEYFLAGS[@]}" --chain-id "$CHAIN_ID" --fees 0nscn -y >/dev/null

# Wait for voting period to elapse, then poll for PASSED (bounded retries).
VOTING_SECS="${VOTING%s}"
sleep "$((VOTING_SECS + 10))"
PASSED=""
for _ in $(seq 1 15); do
  PROP_OUT=$(q q gov proposal "$PROP_ID" 2>/dev/null || true)
  PROP_STATUS=$(printf '%s' "$PROP_OUT" | jq -r '.proposal.status' 2>/dev/null || true)
  if [ "$PROP_STATUS" = "PROPOSAL_STATUS_PASSED" ]; then
    PASSED=1
    break
  fi
  sleep 2
done
[ -n "$PASSED" ] || fail "proposal $PROP_ID did not reach PROPOSAL_STATUS_PASSED (last seen: ${PROP_STATUS:-unknown})"
# The polls above suppress stderr; print the final proposal state once for the record.
runq "q gov proposal $PROP_ID"
printf '%s\n' "$PROP_OUT"
ok "proposal $PROP_ID PROPOSAL_STATUS_PASSED"

COMPLIANCE_OUT=$(q q compliance compliance "$VALOPER")
# Same proto3 bool-omission as above: compliant=false is an absent key, not "false".
COMPLIANT=$(printf '%s' "$COMPLIANCE_OUT" | jq -r '.state.compliant // false')
[ "$COMPLIANT" = "false" ] || fail "expected compliant == false after flag, got $COMPLIANT"
ok "compliance state: compliant=false"

VALREG_OUT=$(q q validatorregistry validator "$VALOPER")
VALREG_STATUS=$(printf '%s' "$VALREG_OUT" | jq -r '.validator.status')
[ "$VALREG_STATUS" = "STATUS_QUARANTINED" ] || fail "expected STATUS_QUARANTINED after flag, got $VALREG_STATUS"
ok "validator status: STATUS_QUARANTINED"

step "Step 4: reward denied post-flag"
BAL_BEFORE_OUT=$(q q bank balances "$VALACC")
BAL_BEFORE_FLAG=$(printf '%s' "$BAL_BEFORE_OUT" | jq -r '.balances[] | select(.denom=="nscn") | .amount')
ok "BAL_BEFORE_FLAG (post-PASSED snapshot) = ${BAL_BEFORE_FLAG}nscn"

CUR_HEIGHT=$(curl -s localhost:26657/status | jq -r .result.sync_info.latest_block_height)
EPOCH_AFTER=$(( CUR_HEIGHT / EPOCH_LEN + 1 ))
TARGET_HEIGHT=$(( EPOCH_AFTER * EPOCH_LEN + 1 ))
wait_height "$TARGET_HEIGHT"

RS_AFTER_OUT=$(q q rewards snapshot "$VALOPER" "$EPOCH_AFTER")
ELIGIBLE_AFTER=$(printf '%s' "$RS_AFTER_OUT" | jq -r '.snapshot.eligible // false')
REASON_AFTER=$(printf '%s' "$RS_AFTER_OUT" | jq -r '.snapshot.reason')
[ "$ELIGIBLE_AFTER" = "false" ] || fail "expected eligible == false at epoch $EPOCH_AFTER, got $ELIGIBLE_AFTER"
# emission.go checks gates in order: status==ACTIVE, then compliant, then EP>0.
# A governed compliance flag on this branch also quarantines the validator
# (status STATUS_ACTIVE -> STATUS_QUARANTINED), so the FIRST gate to fail is
# "not active", not "non-compliant" — verified empirically against the real
# node (x/rewards/keeper/emission.go: eligibleStatus is checked before
# compliant). Accept either denial reason as proof of "reward denied", but
# require it to be one of the two compliance-flag-caused reasons.
case "$REASON_AFTER" in
  "not active"|"non-compliant") : ;;
  *) fail "expected reason == 'not active' or 'non-compliant' (denied due to governed flag), got '$REASON_AFTER'" ;;
esac
ok "epoch $EPOCH_AFTER reward snapshot: eligible=false, reason=$REASON_AFTER"

BAL_AFTER_OUT=$(q q bank balances "$VALACC")
BAL_AFTER=$(printf '%s' "$BAL_AFTER_OUT" | jq -r '.balances[] | select(.denom=="nscn") | .amount')
[ "$BAL_AFTER" = "$BAL_BEFORE_FLAG" ] || fail "expected balance unchanged ($BAL_BEFORE_FLAG), got $BAL_AFTER"
ok "nscn balance unchanged post-flag: ${BAL_AFTER}nscn"

printf '\n\033[1;32m'
printf '#############################################\n'
printf '#  M1-D PoP CYCLE DEMO: ALL ASSERTIONS GREEN #\n'
printf '#############################################\n'
printf '\033[0m\n'
