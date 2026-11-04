#!/usr/bin/env bash
# Rehearse the v0.10.0 in-place upgrade, locally and without Docker: a node on the
# OLD binary is taken through a software-upgrade proposal, halts at the upgrade
# height, and is restarted on the NEW binary on the very same home. This is the only
# proof of the upgrade store-loader path (the Go tests cover the handler, not the
# loader that mounts the new x/entityregistry store).
#
#   bash ./scripts/rehearse-upgrade-v0.10.0.sh                  # old = git tag v0.9.1
#   OLD_REF=v0.9.0 bash ./scripts/rehearse-upgrade-v0.10.0.sh   # old = the version the devnet runs
#   KEEP=1 bash ./scripts/rehearse-upgrade-v0.10.0.sh           # keep the temp dir (home, logs, binaries)
#   SKIP_VRF=1 bash ./scripts/rehearse-upgrade-v0.10.0.sh       # the upgrade only, no VRF key ceremony
#
# After the upgrade x/compliance is checked (its 1 -> 2 migration ran in the same handler: unjail
# cooldown kept, coordinated-attack fraction 0.30, appeal window 604800, no armed case; the
# consensus evidence window is compared before and after: 100,000 blocks / 48h become 302,400 / 21 days,
# the rest of the consensus params unchanged). The randomness store is checked too (params, round 1
# scheduled from the upgrade height). Unless SKIP_VRF=1 the rehearsal then runs the W2 key ceremony of the
# runbook on the new binary and the same home: vrf keygen, offer-vrf-key, the legacy entity
# re-attested with --vrf-keys, the submitter loop (./scripts/vrf-submit-loop.sh), and waits for
# round 1 SKIPPED (by design) and round 2 DRAWN with the legacy entity as the winner. That
# part shortens the old chain's participation epoch to 10 blocks (a rotation every 70) and adds
# about 9 minutes of 2 s blocks. SKIP_VRF=1 keeps the W1 chain as it was (100-block epoch).
#
# The old binary is built from OLD_REF in a temporary git worktree (removed at exit),
# the new one from the current tree. Both run natively on 127.0.0.1 ports 36656/36657/39090.
# Everything is checked; the exit status is non-zero on any failure, with the node log tails.
set -euo pipefail
source "$(dirname "$0")/lib-narrate.sh"

OLD_REF="${OLD_REF:-v0.9.1}"
CHAIN_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
REPO_DIR="$(git -C "$CHAIN_DIR" rev-parse --show-toplevel)"
CHAIN_ID=saicon-rehearse-1
UPGRADE=v0.10.0
RPC_PORT=36657
NODE="tcp://127.0.0.1:${RPC_PORT}"
SCN=100000000

TMP="$(mktemp -d "${TMPDIR:-/tmp}/saicon-rehearse-upgrade.XXXXXX")"
HOME_DIR="$TMP/home"
OLD_BIN="$TMP/old/saicond"
NEW_BIN="$TMP/new/saicond"
WORKTREE="$TMP/old-src"
NODE_PID=""
LOOP_PID=""
SKIP_VRF="${SKIP_VRF:-0}"

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
  NODE_PID=""
}
cleanup() {
  local rc=$?
  stop_loop
  stop_node
  if [ "$rc" != 0 ]; then
    for f in old.log new.log loop.log; do
      [ -f "$TMP/$f" ] && { printf '\n--- tail of %s ---\n' "$f" >&2; tail -n 25 "$TMP/$f" | cut -c1-300 >&2; }
    done
  fi
  git -C "$REPO_DIR" worktree remove --force "$WORKTREE" >/dev/null 2>&1 || true
  git -C "$REPO_DIR" worktree prune >/dev/null 2>&1 || true
  if [ "${KEEP:-0}" = 1 ]; then echo "kept: $TMP" >&2; else rm -rf "$TMP"; fi
}
trap cleanup EXIT

# --- helpers ------------------------------------------------------------------
KR=(--keyring-backend test --home "$HOME_DIR")
TXF=(--chain-id "$CHAIN_ID" --node "$NODE" --fees 0nscn --gas 600000 -y -o json)

height() { curl -s "http://127.0.0.1:${RPC_PORT}/status" | jq -r '.result.sync_info.latest_block_height' 2>/dev/null || echo 0; }
wait_height() { # target [max seconds, default 180]
  local h=0
  for _ in $(seq 1 "${2:-180}"); do
    h=$(height); [ "${h:-0}" -ge "$1" ] 2>/dev/null && return 0; sleep 1
  done
  fail "node did not reach height $1 (last seen: ${h:-none})"
}
tx() { # bin args... ; waits for the tx to be in a block and checks its code
  local bin=$1; shift
  local out hash r
  out=$("$bin" tx "$@" "${KR[@]}" "${TXF[@]}" 2>&1) || fail "tx broadcast failed: $out"
  [ "$(jq -r '.code // 1' <<<"$out")" = 0 ] || fail "tx refused at CheckTx: $(jq -r '.raw_log // .' <<<"$out")"
  hash=$(jq -r .txhash <<<"$out")
  for _ in $(seq 1 30); do
    r=$("$bin" q tx "$hash" --node "$NODE" --home "$HOME_DIR" -o json 2>/dev/null || true)
    [ -n "$r" ] && break; sleep 1
  done
  [ -n "$r" ] || r='{}'
  [ "$(jq -r '.code // 99' <<<"$r")" = 0 ] || fail "tx $hash failed in the block: $(jq -r '.raw_log // "not in a block"' <<<"$r")"
  TX_HEIGHT=$(jq -r .height <<<"$r")
}
start_node() { # bin logfile
  "$1" start --home "$HOME_DIR" --minimum-gas-prices 0nscn --api.enable=false >"$2" 2>&1 &
  NODE_PID=$!
}
eq() { [ "$1" = "$2" ] && ok "$3: $1" || fail "$3: got '$1', want '$2'"; }

# --- 1. the two binaries ------------------------------------------------------
step "Build the old binary ($OLD_REF) and the new one (this tree)"
git -C "$REPO_DIR" rev-parse --verify --quiet "${OLD_REF}^{commit}" >/dev/null || fail "OLD_REF '$OLD_REF' is not a commit/tag of this repository"
git -C "$REPO_DIR" worktree add --detach "$WORKTREE" "$OLD_REF" >/dev/null 2>&1 || fail "git worktree add $OLD_REF failed"
mkdir -p "$TMP/old" "$TMP/new"
(cd "$WORKTREE/chain" && go build -o "$OLD_BIN" ./cmd/saicond) || fail "building $OLD_REF failed"
(cd "$CHAIN_DIR" && go build -o "$NEW_BIN" ./cmd/saicond) || fail "building the current tree failed"
ok "old: $OLD_REF ($(git -C "$REPO_DIR" rev-parse --short "$OLD_REF^{commit}"))   new: current tree ($(git -C "$REPO_DIR" rev-parse --short HEAD))"

# --- 2. a single-node chain on the old binary ---------------------------------
step "A single-node chain on the old binary, as the devnet's entrypoint builds it"
"$OLD_BIN" init rehearse --chain-id "$CHAIN_ID" --default-denom nscn --home "$HOME_DIR" >/dev/null 2>&1
"$OLD_BIN" keys add validator "${KR[@]}" --no-backup >/dev/null 2>&1
"$OLD_BIN" keys add attestor "${KR[@]}" --no-backup >/dev/null 2>&1
"$OLD_BIN" genesis add-genesis-account validator "1000000000000000nscn" "${KR[@]}"
"$OLD_BIN" genesis gentx validator "100000000000000nscn" --chain-id "$CHAIN_ID" "${KR[@]}" >/dev/null 2>&1
"$OLD_BIN" genesis collect-gentxs --home "$HOME_DIR" >/dev/null 2>&1
GEN="$HOME_DIR/config/genesis.json"
# The devnet's values: a short voting period for the rehearsal, the 2,000,000 SCN cap floor.
jq '.app_state.gov.params.voting_period = "20s"
    | .app_state.gov.params.expedited_voting_period = "10s"
    | .app_state.stakeengine.params.validator_cap_floor = "200000000000000"' \
  "$GEN" > "$GEN.tmp" && mv "$GEN.tmp" "$GEN"
if [ "$SKIP_VRF" != 1 ]; then
  # The VRF part needs a rotation every 70 blocks (epoch 10 x 7 epochs), not every 700.
  jq '.app_state.participation.params.epoch_length_blocks = "10"' "$GEN" > "$GEN.tmp" && mv "$GEN.tmp" "$GEN"
fi
"$OLD_BIN" genesis validate-genesis --home "$HOME_DIR" >/dev/null 2>&1 || fail "genesis does not validate on the old binary"
# 2 s blocks and private ports, so the rehearsal is short and collides with nothing.
sed -i.bak \
  -e 's#^timeout_commit = .*#timeout_commit = "2s"#' \
  -e "s#^laddr = \"tcp://127.0.0.1:26657\"#laddr = \"${NODE}\"#" \
  -e 's#^laddr = "tcp://0.0.0.0:26656"#laddr = "tcp://127.0.0.1:36656"#' \
  -e 's#^pprof_laddr = .*#pprof_laddr = "127.0.0.1:36060"#' "$HOME_DIR/config/config.toml"
sed -i.bak -e 's#^address = "localhost:9090"#address = "127.0.0.1:39090"#' "$HOME_DIR/config/app.toml"
VAL=$("$OLD_BIN" keys show validator --bech val -a "${KR[@]}")
ATT=$("$OLD_BIN" keys show attestor -a "${KR[@]}")
start_node "$OLD_BIN" "$TMP/old.log"
wait_height 3
ok "the old node is producing blocks (height $(height)); validator $VAL"
BEFORE=$("$OLD_BIN" q validatorregistry validator "$VAL" --node "$NODE" --home "$HOME_DIR" -o json | jq -r .validator.status)
eq "$BEFORE" STATUS_ACTIVE "the validator's registry status before the upgrade"
"$OLD_BIN" q entityregistry params --node "$NODE" --home "$HOME_DIR" >/dev/null 2>&1 && fail "the old binary already has x/entityregistry: OLD_REF is not a pre-v0.10.0 version"
ok "the old binary has no x/entityregistry (as it must not)"
# `init` on the old binary writes the SDK's 5% double-sign and 1% downtime fractions; the upgrade brings
# them to the policy's 10% and 0.
SLP=$("$OLD_BIN" q slashing params --node "$NODE" --home "$HOME_DIR" -o json)
DS_BEFORE=$(jq -r .params.slash_fraction_double_sign <<<"$SLP")
DT_BEFORE=$(jq -r .params.slash_fraction_downtime <<<"$SLP")
eq "$DS_BEFORE" 0.050000000000000000 "the old chain's double-sign slash fraction"
eq "$DT_BEFORE" 0.010000000000000000 "the old chain's downtime slash fraction"
# `init` writes CometBFT's stock evidence window, 100,000 blocks and 48h, as the live DevNet holds it;
# the upgrade sets it to the 21-day unbonding period. The rest of the consensus params must not move.
CONS_BEFORE=$("$OLD_BIN" q consensus params --node "$NODE" --home "$HOME_DIR" -o json)
eq "$(jq -r '.params.evidence.max_age_num_blocks | tostring' <<<"$CONS_BEFORE")" 100000 "the old chain's evidence max_age_num_blocks"
eq "$(jq -r '.params.evidence.max_age_duration' <<<"$CONS_BEFORE")" 48h0m0s "the old chain's evidence max_age_duration"
eq "$(jq -r '.params.unbonding_time // empty' <<<"$("$OLD_BIN" q staking params --node "$NODE" --home "$HOME_DIR" -o json)")" 504h0m0s \
  "the old chain's unbonding time (21 days)"
# x/compliance on the old binary holds the unjail cooldown only; the upgrade must keep it.
PRE_COOLDOWN=$("$OLD_BIN" q compliance params --node "$NODE" --home "$HOME_DIR" -o json | jq -r .params.unjail_cooldown_blocks)
[ -n "$PRE_COOLDOWN" ] && [ "$PRE_COOLDOWN" != null ] || fail "could not read the old chain's compliance unjail cooldown"
ok "the old chain's compliance unjail_cooldown_blocks: $PRE_COOLDOWN"

# --- 3. the attestor and the proposal -----------------------------------------
step "Fund the attestor, submit MsgSoftwareUpgrade with the attestor in the plan info, vote"
tx "$OLD_BIN" bank send validator "$ATT" "$((1000 * SCN))nscn" --from validator
ok "attestor $ATT funded with 1,000 SCN"
DEPOSIT=$(jq -r '.app_state.gov.params.min_deposit[0] | .amount + .denom' "$GEN")
UPGRADE_H=$(( $(height) + 30 ))     # 60 s of 2 s blocks: more than the 20 s voting period plus two txs
if [ "$SKIP_VRF" != 1 ]; then
  # The smallest height >= that with U % 70 == 1: round 1 is then scheduled at U + 139 and its window
  # opens at U + 39, so a key bound in the first few blocks after the upgrade counts from round 2.
  h=$UPGRADE_H; UPGRADE_H=$(( h + ((71 - h % 70) % 70) ))
fi
INFO=$(jq -cn --arg a "$ATT" '{attestors: [$a]}')
tx "$OLD_BIN" upgrade software-upgrade "$UPGRADE" --upgrade-height "$UPGRADE_H" --upgrade-info "$INFO" \
  --title "v0.10.0" --summary "S8 entity registry" --deposit "$DEPOSIT" --no-validate --from validator
PROP=$("$OLD_BIN" q gov proposals --node "$NODE" --home "$HOME_DIR" -o json | jq -r '.proposals[-1].id')
ok "proposal $PROP submitted: upgrade $UPGRADE at height $UPGRADE_H, info $INFO"
tx "$OLD_BIN" gov vote "$PROP" yes --from validator
STATUS=""
for _ in $(seq 1 60); do
  STATUS=$("$OLD_BIN" q gov proposal "$PROP" --node "$NODE" --home "$HOME_DIR" -o json | jq -r '.proposal.status')
  [ "$STATUS" = PROPOSAL_STATUS_PASSED ] && break; sleep 2
done
eq "$STATUS" PROPOSAL_STATUS_PASSED "proposal $PROP"

# --- 4. the halt ----------------------------------------------------------------
step "The old node must stop at height $UPGRADE_H"
# CometBFT reports "CONSENSUS FAILURE" and stops producing blocks, but the process stays up
# (a supervisor or DevOps stops it) — so the halt is read from the log and from the height.
for _ in $(seq 1 120); do
  grep -q "UPGRADE \"$UPGRADE\" NEEDED at height: $UPGRADE_H" "$TMP/old.log" && break
  kill -0 "$NODE_PID" 2>/dev/null || break
  sleep 1
done
grep -q "UPGRADE \"$UPGRADE\" NEEDED at height: $UPGRADE_H" "$TMP/old.log" \
  || fail "no 'UPGRADE \"$UPGRADE\" NEEDED at height: $UPGRADE_H' in the old node's log"
HALT_H=$(height); sleep 8
eq "$(height)" "$HALT_H" "the old node's height is frozen at the halt"
[ -f "$HOME_DIR/data/upgrade-info.json" ] || fail "the old node left no data/upgrade-info.json for the new binary"
ok "upgrade-info.json written: $(cat "$HOME_DIR/data/upgrade-info.json")"
stop_node
ok "old node stopped (this is DevOps swapping the image)"

# --- 4b. the runbook's record check, on a copy of the halted home --------------------
# Runbook 3.3: the OLD binary exports a copy of the halted state (no keys, no node) and the
# runbook's own jq filter confirms every historical-rewards record the v0.53.8 code reads strictly
# exists. The filter is taken from the runbook text, so the procedure is what is proven.
step "The halted state's distribution records (runbook 3.3), read with the old binary on a copy"
RUNBOOK="$REPO_DIR/docs/runbooks/2026-10-30-s8-devnet-upgrade.md"
CHK="$TMP/check"
mkdir -p "$CHK" && cp -R "$HOME_DIR" "$CHK/home"
rm -f "$CHK/home/config/priv_validator_key.json" "$CHK/home/config/node_key.json"
"$OLD_BIN" export --home "$CHK/home" --output-document "$CHK/home/export.json" >"$TMP/export.out" 2>&1 \
  || fail "the old binary's export of the halted copy failed: $(tail -5 "$TMP/export.out")"
RECORDS=$(jq -c '[.app_state.distribution | (.validator_current_rewards | length), (.validator_historical_rewards | length),
  (.delegator_starting_infos | length)]' "$CHK/home/export.json") || fail "the export is not valid JSON"
[ "$(jq 'all(. > 0)' <<<"$RECORDS")" = true ] || fail "the export holds no distribution records: $RECORDS"
ok "distribution records in the export (current, historical, starting infos): $RECORDS"
FILTER=$(sed -n "/^jq -r '\.app_state\.distribution as \$d/,/<DATA_NAME>\/export\.json\"\$/p" "$RUNBOOK")
[ -n "$FILTER" ] || fail "the record check was not found in $RUNBOOK (3.3)"
FILTER=${FILTER//<DATA_NAME>/home}
VERDICT=$(eval "$FILTER") || fail "the runbook's record check failed to run"
eq "$VERDICT" "OK: no missing record" "the runbook's record check on the halted state"

# --- 5. the new binary on the same home ------------------------------------------
step "Start the NEW binary on the same home (the upgrade store loader mounts x/entityregistry)"
start_node "$NEW_BIN" "$TMP/new.log"
wait_height $((UPGRADE_H + 3))
kill -0 "$NODE_PID" 2>/dev/null || fail "the new node died"
ok "the new node is past the upgrade height: $(height) > $UPGRADE_H"

Q() { "$NEW_BIN" q "$@" --node "$NODE" --home "$HOME_DIR" -o json; }
step "What the upgrade did"
VE=$(Q entityregistry validator-entity "$VAL")
eq "$(jq -r '.attested // false' <<<"$VE")" true "the validator is attested"
ENT=$(jq -r .entity_id <<<"$VE")
case "$ENT" in legacy-*) ok "its entity is a legacy one: $ENT";; *) fail "entity id '$ENT' does not start with legacy-";; esac
eq "$(Q entityregistry params | jq -r --arg a "$ATT" '.params.attestors | index($a) != null')" true \
  "the attestor from the plan info is in entityregistry params"
eq "$(Q validatorregistry validator "$VAL" | jq -r .validator.status)" STATUS_ACTIVE "the validator's registry status after the upgrade"
eq "$(Q entityregistry tickets | jq -r '.tickets | length')" 1 "tickets"
SLP=$(Q slashing params)
eq "$(jq -r .params.slash_fraction_double_sign <<<"$SLP")" 0.100000000000000000 \
  "the double-sign slash fraction after the upgrade (was $DS_BEFORE)"
eq "$(jq -r .params.slash_fraction_downtime <<<"$SLP")" 0.000000000000000000 \
  "the downtime slash fraction after the upgrade (was $DT_BEFORE)"
CAP=$(Q stakeengine caps | jq -r '.cap_state.entity_cap // "0"')
[ "$CAP" != 0 ] && ok "stakeengine caps: entity_cap $CAP nscn" || fail "stakeengine caps: entity_cap is zero"
# A malformed plan info is only logged by the handler, never an error: say so if it happened.
if grep -q "plan info carries no valid attestor list" "$TMP/new.log"; then fail "the handler logged a bad plan info (the attestor list was dropped)"; fi
grep -q "v0.10.0: attested 1 active validators" "$TMP/new.log" && ok "handler log: attested 1 active validators as their own entities" \
  || fail "the handler's summary line is missing from the new node's log"

step "x/compliance after the upgrade (coordinated-attack parameters)"
CP=$(Q compliance params)
eq "$(jq -r '.params.coordinated_attack_fraction' <<<"$CP")" 0.300000000000000000 \
  "compliance params: coordinated_attack_fraction (0.30 >= the double-sign 0.10)"
eq "$(jq -r '.params.appeal_window_seconds' <<<"$CP")" 604800 "compliance params: appeal_window_seconds (7 days)"
eq "$(jq -r '.params.unjail_cooldown_blocks' <<<"$CP")" "$PRE_COOLDOWN" "compliance params: unjail_cooldown_blocks (kept)"
eq "$(Q compliance armed | jq -r '.cases | length')" 0 "armed cases"

step "The consensus evidence window after the upgrade: the 21-day unbonding period"
CONS=$(Q consensus params)
eq "$(jq -r '.params.evidence.max_age_num_blocks | tostring' <<<"$CONS")" 302400 \
  "evidence max_age_num_blocks, floor(21 days / 6 s) (was 100000)"
eq "$(jq -r '.params.evidence.max_age_duration' <<<"$CONS")" 504h0m0s "evidence max_age_duration, 21 days (was 48h0m0s)"
eq "$(jq -c '.params | del(.evidence.max_age_num_blocks, .evidence.max_age_duration)' <<<"$CONS")" \
  "$(jq -c '.params | del(.evidence.max_age_num_blocks, .evidence.max_age_duration)' <<<"$CONS_BEFORE")" \
  "every other consensus param, evidence max_bytes included, as before"
grep -q "v0.10.0: setting the evidence window to the unbonding period" "$TMP/new.log" \
  && ok "handler log: the evidence window was set to the unbonding period" \
  || fail "the handler's evidence-window line is missing from the new node's log"

step "x/randomness after the upgrade (shadow mode): params and round 1, scheduled from the upgrade height"
RP=$(Q randomness params)
eq "$(jq -r '.params.rotation_epochs' <<<"$RP")" 7 "randomness params: rotation_epochs"
eq "$(jq -r '.params.window_blocks' <<<"$RP")" 100 "randomness params: window_blocks"
EPOCH_L=$(Q participation params | jq -r '.params.epoch_length_blocks')
if [ "$SKIP_VRF" = 1 ]; then eq "$EPOCH_L" 100 "participation epoch_length_blocks (the W1 default)"; else eq "$EPOCH_L" 10 "participation epoch_length_blocks (the VRF rehearsal's)"; fi
ROT_P=$(( EPOCH_L * 7 ))
# R_1 = the smallest multiple of P that is >= U + window_blocks + 1; O_1 = R_1 - window_blocks (spec 7.2).
R1_WANT=$(( ((UPGRADE_H + 101 + ROT_P - 1) / ROT_P) * ROT_P ))
R1=$(Q randomness round)
eq "$(jq -r '.round.number' <<<"$R1")" 1 "the current round"
eq "$(jq -r '.round.rotation_height' <<<"$R1")" "$R1_WANT" "round 1 rotation_height (smallest multiple of $ROT_P >= $UPGRADE_H + 101)"
eq "$(jq -r '.round.window_open_height' <<<"$R1")" "$(( R1_WANT - 100 ))" "round 1 window_open_height"
eq "$(jq -r '.round.key_cutoff_height // "0"' <<<"$R1")" 0 "round 1 key_cutoff_height (no key is eligible: round 1 is the warm-up)"
O1=$(( R1_WANT - 100 ))
[ "$O1" -gt "$(height)" ] || fail "round 1's window opens at $O1, not after the current height $(height)"
ok "round 1 is scheduled after the upgrade: window opens at $O1, closes at $R1_WANT"

if [ "$SKIP_VRF" != 1 ]; then
step "The VRF key ceremony of the runbook: keygen, offer, re-attest the legacy entity with the key"
VRF_KEY_FILE="$HOME_DIR/config/vrf_key.json"
KEYGEN=$("$NEW_BIN" vrf keygen --output "$VRF_KEY_FILE" --home "$HOME_DIR") || fail "saicond vrf keygen failed"
VRF_PK=$(sed -n 's/^VRF public key: //p' <<<"$KEYGEN")
[ "${#VRF_PK}" = 64 ] || fail "vrf keygen did not print a 64-hex public key: $KEYGEN"
# The real root command (its PersistentPreRunE path), not a unit-level command tree.
eq "$("$NEW_BIN" vrf show --vrf-key-file "$VRF_KEY_FILE" --home "$HOME_DIR")" "$VRF_PK" "saicond vrf show prints the key vrf keygen made"
MODE=$(stat -c '%a' "$VRF_KEY_FILE" 2>/dev/null || stat -f '%Lp' "$VRF_KEY_FILE")
eq "$MODE" 600 "the key file's mode"
tx "$NEW_BIN" entityregistry offer-vrf-key --vrf-key-file "$VRF_KEY_FILE" --from validator
ok "offer-vrf-key in block $TX_HEIGHT"
EQ=$(Q entityregistry entity "$ENT")
EN_NAME=$(jq -r '.entity.display_name' <<<"$EQ"); EN_HASH=$(jq -r '.entity.attestation_hash' <<<"$EQ")
[ -n "$EN_NAME" ] && [ "$EN_NAME" != null ] && [ -n "$EN_HASH" ] && [ "$EN_HASH" != null ] || fail "could not read the legacy entity's name and hash: $EQ"
# A re-attestation REPLACES the member list: it must be exactly this validator (the runbook's read-back).
[ -n "$ENT" ] && [ "$ENT" != null ] && [ -n "$VAL" ] && [ "$VAL" != null ] || fail "ENT or VAL is empty or null"
eq "$(jq -r '.members[].validator' <<<"$EQ")" "$VAL" "the legacy entity's members before the re-attestation"
tx "$NEW_BIN" entityregistry attest "$ENT" "$EN_NAME" "$EN_HASH" "$VAL" \
  --vrf-keys "$(jq -cn --arg v "$VAL" --arg k "$VRF_PK" '{validator:$v, vrf_public_key:$k}')" --from attestor
ok "the legacy entity re-attested with the key in block $TX_HEIGHT"
VK=$(Q entityregistry vrf-key "$VAL")
eq "$(jq -r '.vrf_public_key' <<<"$VK")" "$VRF_PK" "the registry's bound key for the validator"
VK_H=$(jq -r '.vrf_key_height' <<<"$VK")
[ "$VK_H" -lt "$(( O1 ))" ] || fail "the key was bound at $VK_H, not before round 1's window opens ($O1)"
ok "the key was bound at height $VK_H, before round 1's window opens at $O1 (it counts from round 2)"
eq "$(Q entityregistry validator-entity "$VAL" | jq -r .entity_id)" "$ENT" "the validator's entity is still"
eq "$(Q entityregistry tickets | jq -r '.tickets | length')" 1 "tickets after the re-attestation"

step "Start the submitter loop on the new binary and the same home"
: >"$TMP/loop.log"
SAICOND_BIN="$NEW_BIN" SAICON_HOME="$HOME_DIR" CHAIN_ID="$CHAIN_ID" NODE="$NODE" FEES=0nscn INTERVAL=4 \
  bash "$CHAIN_DIR/scripts/vrf-submit-loop.sh" >>"$TMP/loop.log" 2>&1 &
LOOP_PID=$!
ok "./scripts/vrf-submit-loop.sh running (pid $LOOP_PID), interval 4 s"

step "Round 1 closes SKIPPED (by design); round 2 is DRAWN with the legacy entity's key"
wait_height $(( R1_WANT + 1 )) 400
D1=$(Q randomness draw 1)
eq "$(jq -r .draw.outcome <<<"$D1")" DRAW_OUTCOME_SKIPPED "round 1 outcome"
ok "round 1 skip reason: $(jq -r '.draw.skip_reason' <<<"$D1")"
R2=$(Q randomness round)
eq "$(jq -r '.round.number' <<<"$R2")" 2 "the current round"
R2_WANT=$(jq -r '.round.rotation_height' <<<"$R2")
[ "$(jq -r '.round.key_cutoff_height' <<<"$R2")" -gt "$VK_H" ] || fail "round 2's key cutoff is not above the key's height $VK_H"
ok "round 2: rotation at $R2_WANT, key cutoff $(jq -r '.round.key_cutoff_height' <<<"$R2") (> the key's height $VK_H)"
wait_height $(( R2_WANT + 1 )) 400
D2=$(Q randomness draw 2)
jq . <<<"$D2" >&2
eq "$(jq -r .draw.outcome <<<"$D2")" DRAW_OUTCOME_DRAWN "round 2 outcome"
eq "$(jq -c .draw.winners <<<"$D2")" "[\"$ENT\"]" "round 2 winners (the legacy entity)"
eq "$(jq -r '.draw.submitted // 0' <<<"$D2")" 1 "round 2 submitted"
eq "$(jq -r '.draw.required // 0' <<<"$D2")" 1 "round 2 required (quorum 2/3 of one validator is one)"
eq "$(jq -r '.draw.outputs | length' <<<"$D2")" 1 "round 2 verified outputs"
stop_loop
ok "the loop is stopped; its last lines:"
tail -n 4 "$TMP/loop.log" | cut -c1-200 >&2 || true
fi

step "The chain keeps producing blocks on the new binary"
H1=$(height); wait_height $((H1 + 3)); ok "height $H1 -> $(height)"
step "Rehearsal complete: $OLD_REF -> $UPGRADE in place"
