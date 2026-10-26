#!/usr/bin/env bash
# Verifies both retention profiles on a running 3-validator + 1-archive testnet.
#
#   docker compose -f chain/docker-compose.testnet.yml \
#                  -f chain/docker-compose.testnet.archive.yml up -d
#   bash chain/scripts/test-node-profiles.sh
#
# WHAT THIS PROVES
#   - the three validators reach consensus and the height advances
#   - a node with no gentx and no stake joins, follows the same chain, and stays
#     out of the validator set
#   - each profile's app.toml carries the values the policy specifies
#
# WHAT IT DOES NOT PROVE
#   Pruning behaviour. A validator keeps 100,000 state versions and 362,880
#   blocks, so on a chain a few hundred blocks old neither profile has pruned
#   anything yet and their on-disk contents are identical by construction.
#   Proving the archive diverges from a validator needs a chain past the
#   retention floor, which is a soak test, not a minutes-long one.
set -uo pipefail

VALIDATOR_PORTS=(26657 26658 26659)
ARCHIVE_PORT=26660
EVIDENCE_BLOCKS=362880          # 21 days at a 5s block, matches the entrypoints
fails=0
checks=0

pass() { checks=$((checks+1)); printf '  \033[0;32mPASS\033[0m  %s\n' "$1"; }
fail() { checks=$((checks+1)); fails=$((fails+1)); printf '  \033[0;31mFAIL\033[0m  %s\n' "$1"; }

status_field() {   # $1 port, $2 dotted path under .result
  curl -s --max-time 5 "http://localhost:$1/status" 2>/dev/null \
    | python3 -c "
import sys, json
try:
    d = json.load(sys.stdin)['result']
except Exception:
    sys.exit(1)
for k in '$2'.split('.'):
    d = d[k]
print(d)
" 2>/dev/null
}

toml_value() {     # $1 container, $2 key
  docker exec "$1" grep -E "^$2 = " "/data/${1#saicon-}/config/app.toml" 2>/dev/null \
    | head -1 | sed 's/.*= *//'
}

echo "== nodes are up and the chain is advancing =="
declare -A H0
for p in "${VALIDATOR_PORTS[@]}" "$ARCHIVE_PORT"; do
  h="$(status_field "$p" 'sync_info.latest_block_height')"
  if [ -n "$h" ]; then pass "port $p answers /status (height $h)"; H0[$p]=$h
  else fail "port $p does not answer /status"; H0[$p]=0; fi
done

sleep 12
advanced=0
for p in "${VALIDATOR_PORTS[@]}" "$ARCHIVE_PORT"; do
  h="$(status_field "$p" 'sync_info.latest_block_height')"
  if [ -n "$h" ] && [ "$h" -gt "${H0[$p]}" ]; then advanced=$((advanced+1)); fi
done
if [ "$advanced" -eq 4 ]; then pass "all four nodes advanced within 12s"
else fail "only $advanced of 4 nodes advanced — the chain is stalled or a node is stuck"; fi

echo
echo "== the archive follows the same chain =="
va="$(status_field "${VALIDATOR_PORTS[0]}" 'sync_info.latest_block_height')"
ar="$(status_field "$ARCHIVE_PORT" 'sync_info.latest_block_height')"
if [ -n "$va" ] && [ -n "$ar" ]; then
  lag=$(( va > ar ? va - ar : ar - va ))
  if [ "$lag" -le 3 ]; then pass "archive is within $lag blocks of node0 ($ar vs $va)"
  else fail "archive lags node0 by $lag blocks ($ar vs $va) — it is not keeping up"; fi
else
  fail "could not read both heights"
fi

vnet="$(status_field "${VALIDATOR_PORTS[0]}" 'node_info.network')"
anet="$(status_field "$ARCHIVE_PORT" 'node_info.network')"
if [ -n "$vnet" ] && [ "$vnet" = "$anet" ]; then pass "same chain id ($vnet)"
else fail "chain id differs: node0=$vnet archive=$anet"; fi

echo
echo "== the archive is NOT a validator =="
for p in "${VALIDATOR_PORTS[@]}"; do
  vp="$(status_field "$p" 'validator_info.voting_power')"
  if [ -n "$vp" ] && [ "$vp" != "0" ]; then pass "port $p has voting power $vp"
  else fail "port $p has voting power ${vp:-unknown} — expected non-zero"; fi
done
avp="$(status_field "$ARCHIVE_PORT" 'validator_info.voting_power')"
if [ "$avp" = "0" ]; then pass "archive has voting power 0 — it does not sign"
else fail "archive has voting power ${avp:-unknown} — it should not be in the set"; fi

setsize="$(curl -s --max-time 5 "http://localhost:${VALIDATOR_PORTS[0]}/validators" 2>/dev/null \
  | python3 -c "import sys,json;print(len(json.load(sys.stdin)['result']['validators']))" 2>/dev/null)"
if [ "$setsize" = "3" ]; then pass "validator set has exactly 3 members"
else fail "validator set has ${setsize:-unknown} members — expected 3"; fi

echo
echo "== validator profile: keeps what safety requires, discards the rest =="
for c in saicon-node0 saicon-node1 saicon-node2; do
  for kv in "pruning:\"custom\"" "pruning-keep-recent:\"100000\"" "pruning-interval:\"10\"" \
            "min-retain-blocks:${EVIDENCE_BLOCKS}" "snapshot-interval:1000" "snapshot-keep-recent:2"; do
    key="${kv%%:*}"; want="${kv#*:}"
    got="$(toml_value "$c" "$key")"
    if [ "$got" = "$want" ]; then pass "$c $key = $got"
    else fail "$c $key = ${got:-<missing>} — expected $want"; fi
  done
done

echo
echo "== archive profile: keeps everything =="
for kv in "pruning:\"nothing\"" "min-retain-blocks:0" "snapshot-interval:1000" "snapshot-keep-recent:2"; do
  key="${kv%%:*}"; want="${kv#*:}"
  got="$(toml_value saicon-node3 "$key")"
  if [ "$got" = "$want" ]; then pass "saicon-node3 $key = $got"
  else fail "saicon-node3 $key = ${got:-<missing>} — expected $want"; fi
done

echo
echo "== the archive can still answer for the first block =="
b1="$(curl -s --max-time 5 "http://localhost:${ARCHIVE_PORT}/block?height=1" 2>/dev/null \
  | python3 -c "import sys,json;print(json.load(sys.stdin)['result']['block']['header']['height'])" 2>/dev/null)"
if [ "$b1" = "1" ]; then pass "archive serves block 1"
else fail "archive cannot serve block 1 (got ${b1:-nothing})"; fi

echo
if [ "$fails" -eq 0 ]; then
  printf '\033[0;32m%s checks, 0 failures\033[0m\n' "$checks"
  exit 0
fi
printf '\033[0;31m%s checks, %s failures\033[0m\n' "$checks" "$fails"
exit 1
