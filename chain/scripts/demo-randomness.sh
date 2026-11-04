#!/usr/bin/env bash
# demo-randomness.sh — S8 W2: the per-validator VRF draw in shadow mode, on a live node.
#   1. a single-node chain whose genesis binds the validator's VRF key and carries a random
#      initial input (the same jq as docker/entrypoint-singlenode.sh), with a short rotation
#      cadence: epoch 5 blocks, rotation every 2 epochs, a 10-block window
#   2. round 1 (warm-up) is SKIPPED: no key was bound before its window, so none is eligible
#   3. the submitter loop (./scripts/vrf-submit-loop.sh) submits, and round 2 is DRAWN; its seed
#      is recomputed here from the round, the input and the sorted outputs
#   4. the loop is stopped: the next round is SKIPPED and the cohort of round 2 still stands
#   5. the loop is restarted: the round after that is DRAWN again
# Every step asserts; exit != 0 on any mismatch. Shadow mode: a record is all a round produces.
#
# Runs natively, without Docker, on 127.0.0.1 ports 46656/46657/49090, in a temporary home.
#   bash ./scripts/demo-randomness.sh
#   KEEP=1 bash ./scripts/demo-randomness.sh    # keep the temp dir (home, logs, binary)
set -euo pipefail
source "$(dirname "$0")/lib-narrate.sh"

CHAIN_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
CHAIN_ID=saicon-demo-randomness-1
RPC_PORT=46657
NODE="tcp://127.0.0.1:${RPC_PORT}"

TMP="$(mktemp -d "${TMPDIR:-/tmp}/saicon-demo-randomness.XXXXXX")"
HOME_DIR="$TMP/home"
BIN="$TMP/saicond"
NODE_PID=""
LOOP_PID=""

stop_loop() {
  [ -n "$LOOP_PID" ] || return 0
  # Freeze the loop so it cannot start a new child, note the children in flight (a
  # saicond or a sleep), kill the loop, then the children: killed first, they would
  # be replaced; killed after the loop alone, they would be orphaned.
  local kids
  kill -STOP "$LOOP_PID" 2>/dev/null || true
  kids="$(pgrep -P "$LOOP_PID" 2>/dev/null || true)"
  kill -KILL "$LOOP_PID" 2>/dev/null || true
  # shellcheck disable=SC2086
  [ -z "$kids" ] || kill $kids 2>/dev/null || true
  wait "$LOOP_PID" 2>/dev/null || true
  LOOP_PID=""
}
stop_node() {
  [ -n "$NODE_PID" ] || return 0
  kill "$NODE_PID" 2>/dev/null || true
  for _ in $(seq 1 20); do kill -0 "$NODE_PID" 2>/dev/null || break; sleep 1; done
  kill -9 "$NODE_PID" 2>/dev/null || true
  wait "$NODE_PID" 2>/dev/null || true
  NODE_PID=""
}
cleanup() {
  local rc=$?
  stop_loop
  stop_node
  if [ "$rc" != 0 ]; then
    for f in node.log loop.log; do
      [ -f "$TMP/$f" ] && { printf '\n--- tail of %s ---\n' "$f" >&2; tail -n 25 "$TMP/$f" | cut -c1-300 >&2; }
    done
  fi
  if [ "${KEEP:-0}" = 1 ]; then echo "kept: $TMP" >&2; else rm -rf "$TMP"; fi
}
trap cleanup EXIT

KR=(--keyring-backend test --home "$HOME_DIR")
Q() { "$BIN" q "$@" --node "$NODE" --home "$HOME_DIR" -o json; }
eq() { [ "$1" = "$2" ] && ok "$3: $1" || fail "$3: got '$1', want '$2'"; }
sha256_stdin() { if command -v sha256sum >/dev/null 2>&1; then sha256sum | cut -d' ' -f1; else shasum -a 256 | cut -d' ' -f1; fi; }

height() { curl -s "http://127.0.0.1:${RPC_PORT}/status" | jq -r '.result.sync_info.latest_block_height' 2>/dev/null || echo 0; }
wait_height() { # target
  local h=0
  for _ in $(seq 1 240); do
    h=$(height); [ "${h:-0}" -ge "$1" ] 2>/dev/null && return 0; sleep 1
  done
  fail "node did not reach height $1 (last seen: ${h:-none})"
}
# The record of closed round N, once it exists (the query fails until then).
wait_draw() { # round
  local out=""
  for _ in $(seq 1 240); do
    out=$("$BIN" q randomness draw "$1" --node "$NODE" --home "$HOME_DIR" -o json 2>/dev/null) && { printf '%s\n' "$out"; return 0; }
    sleep 1
  done
  fail "no record of round $1 (height now $(height))"
}
start_loop() {
  SAICOND_BIN="$BIN" SAICON_HOME="$HOME_DIR" CHAIN_ID="$CHAIN_ID" NODE="$NODE" FEES=0nscn INTERVAL=2 \
    bash "$CHAIN_DIR/scripts/vrf-submit-loop.sh" >>"$TMP/loop.log" 2>&1 &
  LOOP_PID=$!
}
# The seed, recomputed with nothing but hex, xxd and SHA-256 (spec §9.1):
#   SHA-256("saicon/randomness/seed/v1" ‖ u64(N) ‖ input_N ‖ u32(len) ‖ sorted outputs)
recompute_seed() { # draw-json
  local rec hex
  rec=$(jq -c .draw <<<"$1")
  hex="$(printf '%s' 'saicon/randomness/seed/v1' | xxd -p | tr -d '\n')"
  hex+="$(printf '%016x' "$(jq -r .round <<<"$rec")")"
  hex+="$(jq -r .input <<<"$rec")"
  hex+="$(printf '%08x' "$(jq -r '.outputs | length' <<<"$rec")")"
  hex+="$(jq -r '(.outputs // []) | sort | join("")' <<<"$rec")"
  printf '%s' "$hex" | xxd -r -p | sha256_stdin
}
field() { jq -r ".draw | $1" <<<"$2"; }   # a field of the record inside a draw query's reply

# --- 1. the binary and a single-node genesis ------------------------------------
step "Build saicond from this tree"
(cd "$CHAIN_DIR" && go build -o "$BIN" ./cmd/saicond) || fail "building saicond failed"
ok "$("$BIN" version 2>&1 | head -n 1) ($(git -C "$CHAIN_DIR" rev-parse --short HEAD))"

step "A single-node genesis: entity and VRF key bound, a random initial input, a short cadence"
"$BIN" init demo --chain-id "$CHAIN_ID" --default-denom nscn --home "$HOME_DIR" >/dev/null 2>&1
"$BIN" keys add validator "${KR[@]}" --no-backup >/dev/null 2>&1
"$BIN" keys add attestor "${KR[@]}" --no-backup >/dev/null 2>&1
"$BIN" genesis add-genesis-account validator 1000000000000000nscn "${KR[@]}"
"$BIN" genesis add-genesis-account attestor 100000000000nscn "${KR[@]}"
"$BIN" genesis gentx validator 100000000000000nscn --chain-id "$CHAIN_ID" "${KR[@]}" >/dev/null 2>&1
"$BIN" genesis collect-gentxs --home "$HOME_DIR" >/dev/null 2>&1
GEN="$HOME_DIR/config/genesis.json"
VALOPER="$("$BIN" keys show validator --bech val -a "${KR[@]}")"
ATTESTOR_ADDR="$("$BIN" keys show attestor -a "${KR[@]}")"

# The entrypoint's values: the 2,000,000 SCN cap floor, and the devnet operator's entity.
ENTITY_HASH="$(printf '%s' 'SAiCoN DevNet operator entity: genesis attestation' | sha256_stdin)"
jq --arg att "$ATTESTOR_ADDR" --arg val "$VALOPER" --arg h "$ENTITY_HASH" \
   '.app_state.stakeengine.params.validator_cap_floor = "200000000000000"
    | .app_state.entityregistry.params.attestors = [$att]
    | .app_state.entityregistry.entities = [{"id":"saicon-devnet-operator","display_name":"SAiCoN DevNet operator","attestation_hash":$h,"attestor":$att,"status":"ENTITY_STATUS_ATTESTED","attested_height":"1"}]
    | .app_state.entityregistry.members = [{"validator":$val,"entity_id":"saicon-devnet-operator","attested_height":"1"}]
    | .app_state.stakeengine.params.entity_cap_floor = .app_state.stakeengine.params.validator_cap_floor' \
  "$GEN" > "$GEN.tmp" && mv "$GEN.tmp" "$GEN"

# The entrypoint's VRF block, verbatim in effect (SAICON_HOME -> HOME_DIR, SAICOND -> BIN).
VRF_KEY_FILE="$HOME_DIR/config/vrf_key.json"
[ -f "$VRF_KEY_FILE" ] || "$BIN" vrf keygen --output "$VRF_KEY_FILE" --home "$HOME_DIR" >/dev/null
VRF_PK="$("$BIN" vrf show --vrf-key-file "$VRF_KEY_FILE")"
INITIAL_INPUT="$(od -An -tx1 -N32 /dev/urandom | tr -d ' \n')"
jq --arg pk "$VRF_PK" --arg val "$VALOPER" --arg in "$INITIAL_INPUT" \
   '.app_state.entityregistry.members[0].vrf_public_key = $pk
    | .app_state.entityregistry.members[0].vrf_key_height = "0"
    | .app_state.entityregistry.vrf_key_owners = [{"vrf_public_key":$pk,"validator":$val}]
    | .app_state.randomness.initial_input = $in' \
  "$GEN" > "$GEN.tmp" && mv "$GEN.tmp" "$GEN"

# The demo's cadence: a rotation every 2 epochs of 5 blocks, a 10-block window.
jq '.app_state.participation.params.epoch_length_blocks = "5"
    | .app_state.randomness.params = {"rotation_epochs": 2, "window_blocks": "10"}' \
  "$GEN" > "$GEN.tmp" && mv "$GEN.tmp" "$GEN"
"$BIN" genesis validate-genesis --home "$HOME_DIR" >/dev/null 2>&1 || fail "the genesis does not validate"
ok "validator $VALOPER, VRF key $VRF_PK bound in genesis"
ok "initial input $INITIAL_INPUT (drawn after the key was made)"

# 1 s blocks and private ports, so the demo is short and collides with nothing.
sed -i.bak \
  -e 's#^timeout_commit = .*#timeout_commit = "1s"#' \
  -e "s#^laddr = \"tcp://127.0.0.1:26657\"#laddr = \"${NODE}\"#" \
  -e 's#^laddr = "tcp://0.0.0.0:26656"#laddr = "tcp://127.0.0.1:46656"#' \
  -e 's#^pprof_laddr = .*#pprof_laddr = "127.0.0.1:46060"#' "$HOME_DIR/config/config.toml"
sed -i.bak -e 's#^address = "localhost:9090"#address = "127.0.0.1:49090"#' "$HOME_DIR/config/app.toml"

step "Start the node"
"$BIN" start --home "$HOME_DIR" --minimum-gas-prices 0nscn --api.enable=false >"$TMP/node.log" 2>&1 &
NODE_PID=$!
wait_height 3
ok "the node is producing blocks (height $(height))"

eq "$(Q entityregistry vrf-key "$VALOPER" | jq -r .vrf_public_key)" "$VRF_PK" "the registry's bound key for the validator"
R1="$(Q randomness round)"
eq "$(jq -r .round.number <<<"$R1")" 1 "the current round"
eq "$(jq -r .round.input <<<"$R1")" "$INITIAL_INPUT" "round 1's input is the genesis input"
O1="$(jq -r .round.window_open_height <<<"$R1")"; R1H="$(jq -r .round.rotation_height <<<"$R1")"
ok "round 1: window opens at $O1, rotation at $R1H"

# --- 2. round 1: the warm-up ----------------------------------------------------------
step "Round 1 closes SKIPPED: its key cutoff is 0, so no key is eligible (spec §6.4)"
D1="$(wait_draw 1)"
eq "$(field .outcome "$D1")" DRAW_OUTCOME_SKIPPED "round 1 outcome"
ok "skip reason: $(field .skip_reason "$D1")"
eq "$(field '.submitted // 0' "$D1")" 0 "round 1 submissions"
eq "$(field '.cohort_round // 0' "$D1")" 0 "cohort round after round 1 (nobody drawn yet)"
[ "$(recompute_seed "$D1")" = "$(field .seed "$D1")" ] && ok "round 1's seed recomputes from no outputs" || fail "round 1's seed does not recompute"

# --- 3. the loop, and a drawn round ------------------------------------------------------
step "Start the submitter loop; round 2 (input = seed 1, cutoff = round 1's window) is DRAWN"
: >"$TMP/loop.log"
start_loop
ok "./scripts/vrf-submit-loop.sh running (pid $LOOP_PID), interval 2 s"
D2="$(wait_draw 2)"
stop_loop                       # at once: round 3's window opens at height 50, 10 blocks after the close
jq . <<<"$D2" >&2
eq "$(field .outcome "$D2")" DRAW_OUTCOME_DRAWN "round 2 outcome"
eq "$(field .input "$D2")" "$(field .seed "$D1")" "round 2's input is round 1's seed"
eq "$(field '.submitted // 0' "$D2")" 1 "round 2 submissions"
eq "$(field '.outputs | length' "$D2")" 1 "round 2 verified outputs"
eq "$(field '.winners | length' "$D2")" 1 "round 2 winners (a pool of one entity)"
eq "$(field .cohort_round "$D2")" 2 "cohort round"
SEED2="$(recompute_seed "$D2")"
eq "$SEED2" "$(field .seed "$D2")" "round 2's seed, recomputed here with xxd and SHA-256"

step "The loop never prints the VRF key, and the key file stays private"
KEY_SEED="$(jq -r .seed "$VRF_KEY_FILE")"
[ -n "$KEY_SEED" ] && [ "$KEY_SEED" != null ] || fail "could not read the key seed to check the log against"
if grep -qF "$KEY_SEED" "$TMP/loop.log" "$TMP/node.log"; then fail "the VRF key seed appears in a log"; fi
ok "the key seed appears in neither the loop's log nor the node's"
MODE="$(stat -c '%a' "$VRF_KEY_FILE" 2>/dev/null || stat -f '%Lp' "$VRF_KEY_FILE")"
eq "$MODE" 600 "the key file's mode"
printf '%s\n' "the loop's log so far (a refused simulation before the window opens is retried):" >&2
cut -c1-200 "$TMP/loop.log" >&2

# --- 4. a skipped round with the cohort standing -----------------------------------------------
step "The loop was stopped right after round 2 closed: round 3 is SKIPPED, round 2's cohort still stands"
ok "the loop is stopped"
D3="$(wait_draw 3)"
eq "$(field .outcome "$D3")" DRAW_OUTCOME_SKIPPED "round 3 outcome"
ok "skip reason: $(field .skip_reason "$D3")"
eq "$(field '.submitted // 0' "$D3")" 0 "round 3 submissions"
eq "$(field .cohort_round "$D3")" 2 "cohort round (the winners of round 2 stand)"
eq "$(field .input "$D3")" "$SEED2" "round 3's input is round 2's seed"

# --- 5. drawn again --------------------------------------------------------------------------------
step "Restart the loop: round 4 is DRAWN again"
start_loop
ok "./scripts/vrf-submit-loop.sh running again (pid $LOOP_PID)"
D4="$(wait_draw 4)"
eq "$(field .outcome "$D4")" DRAW_OUTCOME_DRAWN "round 4 outcome"
eq "$(field .cohort_round "$D4")" 4 "cohort round"
eq "$(recompute_seed "$D4")" "$(field .seed "$D4")" "round 4's seed, recomputed"
stop_loop

step "Demo complete: warm-up, a draw, a skip with the cohort standing, a draw again"
