#!/usr/bin/env bash
# Verifies the sentry layout (ADR-0015) on a running testnet.
#
#   docker compose -f chain/docker-compose.testnet.yml \
#                  -f chain/docker-compose.testnet.sentry.yml up -d
#   bash chain/scripts/test-sentry-layout.sh
#
# WHAT THIS PROVES
#   - node0 runs with pex off and dials only its own sentries
#   - both sentries carry node0 in private_peer_ids
#   - node0's address never reaches node1 or node2, which is the whole point:
#     a private peer is used but never advertised
#   - node0 still signs — the layout must not cost liveness
#
# WHAT IT DOES NOT PROVE
#   Behaviour under a sentry outage. ADR-0015 D3 requires two sentries because
#   one is a single point of failure, and proving that needs a node killed and a
#   chain watched, which is a longer test than this one.
set -uo pipefail

VALIDATOR0=26657
OPEN_VALIDATORS=(26658 26659)
SENTRIES=(26661 26662)
fails=0; checks=0
pass() { checks=$((checks+1)); printf '  \033[0;32mPASS\033[0m  %s\n' "$1"; }
fail() { checks=$((checks+1)); fails=$((fails+1)); printf '  \033[0;31mFAIL\033[0m  %s\n' "$1"; }

# node0 answers RPC on loopback inside its own container only (validator
# exposure), so it is queried through docker exec; every other node from the host.
rpc() {
  if [ "$1" = "$VALIDATOR0" ]; then
    docker exec saicon-node0 wget -qO- -T 5 "http://127.0.0.1:26657/$2" 2>/dev/null
  else
    curl -s --max-time 5 "http://localhost:$1/$2" 2>/dev/null
  fi
}

node_id() { rpc "$1" status | python3 -c "import sys,json;print(json.load(sys.stdin)['result']['node_info']['id'])" 2>/dev/null; }
height()  { rpc "$1" status | python3 -c "import sys,json;print(json.load(sys.stdin)['result']['sync_info']['latest_block_height'])" 2>/dev/null; }
peer_ids() { rpc "$1" net_info | python3 -c "
import sys,json
try: d=json.load(sys.stdin)['result']['peers']
except Exception: sys.exit(1)
print(' '.join(p['node_info']['id'] for p in d))" 2>/dev/null; }

toml_value() { docker exec "$1" grep -E "^$2 = " "/data/${1#saicon-}/config/config.toml" 2>/dev/null | head -1 | sed 's/.*= *//'; }

echo "== every node answers and the chain advances =="
declare -A H0
for p in "$VALIDATOR0" "${OPEN_VALIDATORS[@]}" "${SENTRIES[@]}"; do
  h="$(height "$p")"
  if [ -n "$h" ]; then pass "port $p answers (height $h)"; H0[$p]=$h; else fail "port $p does not answer /status"; H0[$p]=0; fi
done
sleep 12
for p in "$VALIDATOR0" "${OPEN_VALIDATORS[@]}" "${SENTRIES[@]}"; do
  h="$(height "$p")"
  if [ -n "$h" ] && [ "$h" -gt "${H0[$p]}" ]; then pass "port $p advanced ${H0[$p]} -> $h"
  else fail "port $p did not advance (was ${H0[$p]}, now ${h:-none}) — the layout must not cost liveness"; fi
done

echo
echo "== the validator is off the open network (D1) =="
if curl -s --max-time 3 "http://localhost:${VALIDATOR0}/status" >/dev/null 2>&1; then
  fail "node0 RPC answers from the host on ${VALIDATOR0}; the hidden validator must not serve it outside its container"
else
  pass "node0 RPC does not answer from the host"
fi
if curl -s --max-time 3 "http://localhost:1317/cosmos/base/tendermint/v1beta1/node_info" >/dev/null 2>&1; then
  fail "REST answers on host port 1317; the hidden validator must not serve it"
else
  pass "no REST on host port 1317"
fi
pex0="$(toml_value saicon-node0 pex)"
[ "$pex0" = "false" ] && pass "node0 pex = false" || fail "node0 pex = ${pex0:-unset}, so its address is gossiped to every peer that asks"

V0="$(node_id "$VALIDATOR0")"
[ -n "$V0" ] && pass "node0 id resolved ($V0)" || fail "could not read node0's id"

n0peers="$(peer_ids "$VALIDATOR0")"
s4="$(node_id "${SENTRIES[0]}")"; s5="$(node_id "${SENTRIES[1]}")"
only_sentries=1
for id in $n0peers; do
  case "$id" in "$s4"|"$s5") ;; *) only_sentries=0 ;; esac
done
if [ "$only_sentries" = "1" ] && [ -n "$n0peers" ]; then
  pass "node0 is connected only to its own sentries"
else
  fail "node0 has peers that are not its sentries: [$n0peers]"
fi

echo
echo "== the sentries hide it (D2) =="
for c in saicon-node4 saicon-node5; do
  pex="$(toml_value "$c" pex)"
  priv="$(toml_value "$c" private_peer_ids | tr -d '"')"
  [ "$pex" = "true" ] && pass "$c pex = true (it takes the traffic)" || fail "$c pex = ${pex:-unset}"
  if [ "$priv" = "$V0" ]; then pass "$c keeps node0 private"
  else fail "$c private_peer_ids = '${priv}', expected node0's id '${V0}' — a sentry without it advertises the address it exists to hide"; fi
done

echo
echo "== the address never reached the open validators (the whole point) =="
for p in "${OPEN_VALIDATORS[@]}"; do
  ids="$(peer_ids "$p")"
  if echo " $ids " | grep -q " $V0 "; then
    fail "port $p knows node0's address — it was gossiped despite private_peer_ids"
  else
    pass "port $p never learned node0's address"
  fi
done

echo
echo "== the validator still signs =="
sig="$(rpc "$VALIDATOR0" 'block' | python3 -c "
import sys,json
try: d=json.load(sys.stdin)['result']['block']['last_commit']['signatures']
except Exception: sys.exit(1)
print(sum(1 for s in d if s.get('block_id_flag')==2))" 2>/dev/null)"
if [ -n "$sig" ] && [ "$sig" -ge 2 ]; then pass "last commit carries $sig signatures"
else fail "last commit carries ${sig:-no} signatures — quorum is not being reached"; fi

echo
printf '%s of %s checks passed\n' "$((checks-fails))" "$checks"
[ "$fails" -eq 0 ] || exit 1
