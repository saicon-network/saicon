#!/usr/bin/env bash
# Rehearse joining a SAiCoN network, locally, the way three separate operators
# would: node A starts the network; B and C each start an empty node, take the
# network's genesis and a peer, sync, get tokens from someone who has them, and
# register a validator. Everything runs in its own Docker network and volumes,
# publishes no host ports, and is removed at the end (KEEP=1 keeps it).
#
#   bash ./scripts/rehearse-join.sh             # uses the saicond:dev image
#   IMAGE=<registry>/<image>:<tag> bash ./scripts/rehearse-join.sh   # a published image
#
# The steps are the ones docs/MULTINODE-JOIN.md describes; this script is the
# proof that they work on the current build.
set -euo pipefail
source "$(dirname "$0")/lib-narrate.sh"

IMAGE="${IMAGE:-saicond:dev}"
NET=saicon-rehearse
CHAIN_ID=saicon-rehearse-1
H=/home/saicon/.saicon                 # the node home inside the image
KR=(--keyring-backend test --home "$H")  # test keyring: a rehearsal, never real funds
TX=(--chain-id "$CHAIN_ID" --fees 0nscn -y -o json)

cleanup() {
  [ "${KEEP:-0}" = 1 ] && return
  docker rm -f rj-a rj-b rj-c >/dev/null 2>&1 || true
  docker volume rm rj-a rj-b rj-c >/dev/null 2>&1 || true
  docker network rm "$NET" >/dev/null 2>&1 || true
}
trap cleanup EXIT
cleanup

height() { docker exec "$1" saicond status --home "$H" | jq -r .sync_info.latest_block_height; }
wait_height() { # container, target
  for _ in $(seq 1 60); do
    h=$(height "$1" 2>/dev/null || echo 0); [ "${h:-0}" -ge "$2" ] && return 0; sleep 2
  done; fail "$1 did not reach height $2"
}
wait_tx() { sleep 7; }  # two blocks
# A broadcast's "code 0" only means the mempool accepted the tx. Whether it ran is
# known only once it is in a block: ask for it by hash.
tx_ok() { # container txhash
  local r; r=$(docker exec "$1" saicond q tx "$2" --home "$H" -o json 2>/dev/null) || fail "tx $2 is not in a block"
  [ "$(jq -r .code <<<"$r")" = 0 ] || fail "tx $2 failed in the block: $(jq -r .raw_log <<<"$r")"
}

step "Node A starts the network (single-node genesis, as the devnet does)"
docker network create "$NET" >/dev/null
docker run -d --name rj-a --network "$NET" -e CHAIN_ID="$CHAIN_ID" -e MONIKER=node-a \
  -v rj-a:"$H" --entrypoint /usr/local/bin/entrypoint-singlenode.sh "$IMAGE" >/dev/null
wait_height rj-a 3
A_ID=$(docker exec rj-a saicond comet show-node-id --home "$H")
ok "A is producing blocks; its peer address is $A_ID@rj-a:26656"

join() { # container moniker peers
  local c=$1 m=$2 peers=$3
  step "$m: an empty node, the network's genesis, a peer"
  docker run --rm -v "$c":"$H" "$IMAGE" init "$m" --chain-id "$CHAIN_ID" --default-denom nscn --home "$H" >/dev/null 2>&1
  # init writes a genesis of its own. It must be replaced by the network's, or the
  # node starts a different chain ("validator set is empty after InitGenesis").
  docker run --rm --network "$NET" -v "$c":"$H" --entrypoint sh "$IMAGE" -c \
    "wget -qO- http://rj-a:26657/genesis | jq '.result.genesis' > $H/config/genesis.json && saicond genesis validate-genesis --home $H" >/dev/null
  ok "genesis taken from A's RPC and validated"
  docker run -d --name "$c" --network "$NET" -v "$c":"$H" "$IMAGE" \
    start --home "$H" --minimum-gas-prices 0nscn --p2p.persistent_peers "$peers" >/dev/null
  sleep 10
  [ "$(docker exec "$c" saicond status --home "$H" | jq -r .sync_info.catching_up)" = false ] || sleep 10
  ok "$m synced to height $(height "$c")"
}

become_validator() { # container key moniker funder funderKey selfbond_nscn spare_nscn
  local c=$1 key=$2 m=$3 funder=$4 fkey=$5 bond=$6 spare=$7
  step "$m: a key, tokens from someone who has them, a validator"
  local addr; addr=$(docker exec "$c" saicond keys add "$key" "${KR[@]}" --output json 2>/dev/null | jq -r .address)
  local fund=$((bond + spare))
  local hash; hash=$(docker exec "$funder" saicond tx bank send "$fkey" "$addr" "${fund}nscn" "${TX[@]}" "${KR[@]}" | jq -r .txhash)
  wait_tx; tx_ok "$funder" "$hash"
  ok "$m holds $(docker exec "$c" saicond q bank balances "$addr" --home "$H" -o json | jq -r '.balances[0].amount') nscn"
  local pk; pk=$(docker exec "$c" saicond comet show-validator --home "$H")
  docker exec "$c" sh -c "cat > $H/validator.json <<EOF
{\"pubkey\": $pk, \"amount\": \"${bond}nscn\", \"moniker\": \"$m\",
 \"commission-rate\": \"0.05\", \"commission-max-rate\": \"0.20\",
 \"commission-max-change-rate\": \"0.01\", \"min-self-delegation\": \"1\"}
EOF"
  out=$(docker exec "$c" saicond tx staking create-validator "$H/validator.json" --from "$key" "${TX[@]}" "${KR[@]}")
  [ "$(jq -r .code <<<"$out")" = 0 ] || fail "create-validator for $m: $(jq -r .raw_log <<<"$out")"
  wait_tx; tx_ok "$c" "$(jq -r .txhash <<<"$out")"
  local val; val=$(docker exec "$c" saicond keys show "$key" --bech val -a "${KR[@]}")
  st=$(docker exec "$c" saicond q validatorregistry validator "$val" --home "$H" -o json | jq -r .validator.status)
  [ "$st" = STATUS_ACTIVE ] || fail "$m registry status is $st"
  ok "$m is a validator: $val, registry status $st"
}

join rj-b node-b "$A_ID@rj-a:26656"

step "node-b: a self-bond below 2,000 SCN is refused"
addr_probe=$(docker exec rj-b saicond keys add probe "${KR[@]}" --output json 2>/dev/null | jq -r .address)
docker exec rj-a saicond tx bank send validator "$addr_probe" 200000000000nscn "${TX[@]}" "${KR[@]}" >/dev/null; wait_tx
pk=$(docker exec rj-b saicond comet show-validator --home "$H")
docker exec rj-b sh -c "cat > $H/low.json <<EOF
{\"pubkey\": $pk, \"amount\": \"100000000000nscn\", \"moniker\": \"too-small\", \"commission-rate\": \"0.05\", \"commission-max-rate\": \"0.20\", \"commission-max-change-rate\": \"0.01\", \"min-self-delegation\": \"1\"}
EOF"
log=$(docker exec rj-b saicond tx staking create-validator "$H/low.json" --from probe "${TX[@]}" "${KR[@]}" 2>&1 || true)
grep -q "entry floor" <<<"$log" && ok "refused: entry floor (2,000 SCN of its own)" || fail "a 1,000 SCN validator was not refused: $log"

become_validator rj-b operator-b node-b rj-a validator 1000000000000 300000000000   # 10,000 SCN bonded, 3,000 to spare
B_ID=$(docker exec rj-b saicond comet show-node-id --home "$H")
join rj-c node-c "$A_ID@rj-a:26656,$B_ID@rj-b:26656"
become_validator rj-c operator-c node-c rj-b operator-b 200000000000 10000000000    # 2,000 SCN, funded by B

step "Three validators — and what one of them going down does"
docker exec rj-a saicond q comet-validator-set --home "$H" -o json | jq -c '[.validators[]|.voting_power]' >&2
docker stop rj-b >/dev/null; h1=$(height rj-c); sleep 12; h2=$(height rj-c); docker start rj-b >/dev/null
[ "$h2" -gt "$h1" ] && ok "B (1% of the power) down: the chain goes on ($h1 -> $h2)" || fail "chain stopped without B"
docker stop rj-a >/dev/null; h1=$(height rj-c); sleep 12; h2=$(height rj-c); docker start rj-a >/dev/null
[ "$h2" -eq "$h1" ] && ok "A (99% of the power) down: the chain stops at $h1 — power, not node count, decides" \
  || fail "the chain moved without A ($h1 -> $h2)"
wait_height rj-c $((h1 + 2))
ok "A back: the chain resumes at $(height rj-c)"

step "Rehearsal complete"
