#!/usr/bin/env bash
# localnet.sh — a native single-node chain for the indexer's live tests. No Docker.
#
#   bash indexer/scripts/localnet.sh start   # build saicond, fresh home, start, run the entity demo
#   bash indexer/scripts/localnet.sh stop
#
# RPC 127.0.0.1:26657 and REST 127.0.0.1:1317 — the ports chain/scripts/demo-entityregistry.sh
# expects. 10-block epochs; app state pruned to the last 50 heights, so a pruned height can be
# asked for within minutes. State lives in $LOCALNET_DIR (default ${TMPDIR:-/tmp}/saicon-indexer-localnet).
set -euo pipefail
DIR="${LOCALNET_DIR:-${TMPDIR:-/tmp}/saicon-indexer-localnet}"
BIN_DIR="$DIR/bin"
HOME_DIR="$DIR/home"
MARKER="$DIR/.saicon-indexer-localnet"
CHAIN_ID=saicon-localnet-1
KR=(--keyring-backend test --home "$HOME_DIR")

# True while the pid in $DIR/node.pid is a live saicond of ours (guards against a reused pid).
node_alive() {
  local pid
  [ -f "$DIR/node.pid" ] || return 1
  pid=$(cat "$DIR/node.pid")
  kill -0 "$pid" 2>/dev/null && ps -p "$pid" -o command= 2>/dev/null | grep -q "$BIN_DIR/saicond"
}

# Kill our node, wait until it has exited (SIGKILL after 30 s), and drop the pid file.
stop_node() {
  local pid
  if [ -f "$DIR/node.pid" ]; then
    pid=$(cat "$DIR/node.pid")
    if node_alive; then
      kill "$pid" 2>/dev/null || true
      for _ in $(seq 1 30); do kill -0 "$pid" 2>/dev/null || break; sleep 1; done
      if kill -0 "$pid" 2>/dev/null; then kill -9 "$pid" 2>/dev/null || true; sleep 1; fi
    fi
    rm -f "$DIR/node.pid"
  fi
}

port_taken() { (exec 3<>"/dev/tcp/127.0.0.1/$1") 2>/dev/null; }

# Touch nothing in a directory this script did not make (or one that does not exist yet):
# not its node.pid, and not the directory itself.
require_marker() {
  if [ -e "$DIR" ] && [ ! -f "$MARKER" ]; then
    echo "refusing to touch $DIR: no marker file $(basename "$MARKER") (not made by this script)" >&2
    exit 1
  fi
}

# The EXIT trap's action: stop the node this run started. Between the start and the pid
# file being written the file is missing, so fall back to the pid of the last background
# job, which is the node (nothing else here runs in the background); node_alive still
# checks it is our saicond before anything is killed.
stop_started_node() {
  local pid="${NODE_PID:-${!:-}}"
  if [ ! -f "$DIR/node.pid" ] && [ -n "$pid" ]; then echo "$pid" > "$DIR/node.pid"; fi
  stop_node
}

case "${1:-}" in
  stop)
    require_marker
    stop_node
    exit 0 ;;
  start) ;;
  *) echo "usage: $0 start|stop" >&2; exit 2 ;;
esac

for tool in go jq curl git; do
  command -v "$tool" >/dev/null || { echo "missing tool: $tool" >&2; exit 1; }
done
if command -v sha256sum >/dev/null; then SHA256=(sha256sum); else SHA256=(shasum -a 256); fi
REPO="$(git -C "$(dirname "${BASH_SOURCE[0]}")" rev-parse --show-toplevel)"

require_marker
if node_alive; then echo "already running (pid $(cat "$DIR/node.pid")): stop it first" >&2; exit 1; fi
rm -f "$DIR/node.pid"   # absent, or a stale file of a dead node
for port in 26657 1317; do
  ! port_taken "$port" || { echo "port $port is in use: stop whatever holds it first" >&2; exit 1; }
done
rm -rf "$DIR" && mkdir -p "$BIN_DIR" && : > "$MARKER"

# Run a setup command; on failure print what it said (stdout and stderr) and fail.
must() {
  local out
  out=$("$@" 2>&1) || { echo "failed: $*" >&2; echo "$out" >&2; return 1; }
}

(cd "$REPO/chain" && go build -o "$BIN_DIR/saicond" ./cmd/saicond)
S="$BIN_DIR/saicond"
must "$S" init localnet --chain-id "$CHAIN_ID" --default-denom nscn --home "$HOME_DIR"
must "$S" keys add validator "${KR[@]}" --no-backup
must "$S" keys add attestor "${KR[@]}" --no-backup
"$S" genesis add-genesis-account validator 1000000000000000nscn "${KR[@]}"
"$S" genesis add-genesis-account attestor 100000000000nscn "${KR[@]}"
must "$S" genesis gentx validator 100000000000000nscn --chain-id "$CHAIN_ID" "${KR[@]}"
must "$S" genesis collect-gentxs --home "$HOME_DIR"
VAL=$("$S" keys show validator --bech val -a "${KR[@]}")
ATT=$("$S" keys show attestor -a "${KR[@]}")
GEN="$HOME_DIR/config/genesis.json"
HASH=$(printf '%s' 'SAiCoN localnet operator entity' | "${SHA256[@]}" | cut -d' ' -f1)
# The same entity block as chain/docker/entrypoint-singlenode.sh, with the devnet's cap floors.
jq --arg att "$ATT" --arg val "$VAL" --arg h "$HASH" '
    .app_state.participation.params.epoch_length_blocks = "10"
  | .app_state.stakeengine.params.validator_cap_floor = "200000000000000"
  | .app_state.stakeengine.params.entity_cap_floor = "200000000000000"
  | .app_state.entityregistry.params.attestors = [$att]
  | .app_state.entityregistry.entities = [{"id":"saicon-devnet-operator","display_name":"SAiCoN DevNet operator","attestation_hash":$h,"attestor":$att,"status":"ENTITY_STATUS_ATTESTED","attested_height":"1"}]
  | .app_state.entityregistry.members = [{"validator":$val,"entity_id":"saicon-devnet-operator","attested_height":"1"}]' \
  "$GEN" > "$GEN.tmp" && mv "$GEN.tmp" "$GEN"
# Production parameters the indexer's data can depend on, copied from the same entrypoint:
# slashing policy (ADR-0010) and the evidence window = the 21-day unbonding period.
# Left at SDK defaults on purpose: staking max_validators (100 against 75; three validators
# here), bank denom metadata (the indexer reads nscn amounts, not the SCN display unit), the
# slashing signed-blocks window and min-signed (the node never misses blocks), and the
# remaining stakeengine / participation params (only the epoch length and the cap floors above
# shape what the indexer reads).
UNBONDING_SECONDS=1814400   # 21 days
EVIDENCE_BLOCKS=$(( UNBONDING_SECONDS / 6 ))   # 302,400
jq --arg n "$EVIDENCE_BLOCKS" --arg d "${UNBONDING_SECONDS}000000000" --arg u "${UNBONDING_SECONDS}s" '
    .app_state.slashing.params.slash_fraction_downtime = "0.000000000000000000"
  | .app_state.slashing.params.slash_fraction_double_sign = "0.100000000000000000"
  | .consensus.params.evidence.max_age_num_blocks = $n
  | .consensus.params.evidence.max_age_duration = $d
  | .app_state.staking.params.unbonding_time = $u' \
  "$GEN" > "$GEN.tmp" && mv "$GEN.tmp" "$GEN"
# The VRF block of chain/docker/entrypoint-singlenode.sh (S8 W2, spec §17), verbatim in effect:
# the member's key bound in genesis, the initial input drawn after the key exists.
"$S" --home "$HOME_DIR" vrf keygen --output "$HOME_DIR/config/vrf_key.json" >/dev/null
VRF_PK=$("$S" --home "$HOME_DIR" vrf show --vrf-key-file "$HOME_DIR/config/vrf_key.json")
INITIAL_INPUT=$(od -An -tx1 -N32 /dev/urandom | tr -d ' \n')
jq --arg pk "$VRF_PK" --arg val "$VAL" --arg in "$INITIAL_INPUT" '
    .app_state.entityregistry.members[0].vrf_public_key = $pk
  | .app_state.entityregistry.members[0].vrf_key_height = "0"
  | .app_state.entityregistry.vrf_key_owners = [{"vrf_public_key":$pk,"validator":$val}]
  | .app_state.randomness.initial_input = $in' \
  "$GEN" > "$GEN.tmp" && mv "$GEN.tmp" "$GEN"
"$S" genesis validate-genesis --home "$HOME_DIR" >/dev/null
sed -i.bak -e 's#^timeout_commit = .*#timeout_commit = "1s"#' "$HOME_DIR/config/config.toml"

# From here until the demo has run, any exit of the script (a failed command, a closed
# terminal, Ctrl-C, kill) takes the node it started down with it. A signal exits with
# 128+signo, and the EXIT trap does the stopping once.
NODE_PID=""
trap 'rc=$?; trap - EXIT; stop_started_node; exit "$rc"' EXIT
trap 'exit 129' HUP
trap 'exit 130' INT
trap 'exit 143' TERM
"$S" start --home "$HOME_DIR" --minimum-gas-prices 0nscn --api.enable --api.address tcp://127.0.0.1:1317 --api.enabled-unsafe-cors \
  --pruning custom --pruning-keep-recent 50 --pruning-interval 10 > "$DIR/node.log" 2>&1 &
NODE_PID=$!
echo "$NODE_PID" > "$DIR/node.pid"

h=0
for _ in $(seq 1 60); do
  kill -0 "$NODE_PID" 2>/dev/null || { echo "node exited during start; tail of $DIR/node.log:" >&2; tail -n 30 "$DIR/node.log" >&2; stop_node; exit 1; }
  h=$(curl -s 127.0.0.1:26657/status | jq -r '.result.sync_info.latest_block_height // 0' 2>/dev/null || echo 0)
  [ "${h:-0}" -ge 3 ] 2>/dev/null && break
  sleep 1
done
[ "${h:-0}" -ge 3 ] 2>/dev/null || { echo "node did not start; tail of $DIR/node.log:" >&2; tail -n 30 "$DIR/node.log" >&2; stop_node; exit 1; }

PATH="$BIN_DIR:$PATH" DEMO_HOME="$HOME_DIR" bash "$REPO/chain/scripts/demo-entityregistry.sh"
trap - EXIT HUP INT TERM   # success: the node stays up until `stop`
echo
echo "localnet up: INDEXER_TEST_RPC=http://127.0.0.1:26657 INDEXER_TEST_REST=http://127.0.0.1:1317"
