#!/usr/bin/env bash
# Rehearse the v0.11.0 in-place upgrade (S10: the WARP engine, the voting-power preview, the PoP
# share paid through x/distribution), locally and without Docker: a node on the OLD binary is
# taken through a software-upgrade proposal, halts at the upgrade height, and is restarted on the
# NEW binary on the very same home.
#
#   bash ./scripts/rehearse-upgrade-v0.11.0.sh                 # old = commit cfbd786 (v0.10.2)
#   OLD_REF=<commit> bash ./scripts/rehearse-upgrade-v0.11.0.sh
#   KEEP=1 bash ./scripts/rehearse-upgrade-v0.11.0.sh          # keep the temp dir (home, logs, binaries)
#
# Checked: before the upgrade the old binary has no WARP query and no preview, x/participation is
# at consensus version 1, and the PoP share goes to the operator's bank account while a delegator
# of the validator earns nothing. After it: x/participation is at version 2, its params carry the
# four WARP defaults and keep the 10-block epoch, the handler logged the S10 state, the preview
# answers (the validator at warp_initial, not measured); after one epoch the validator has a
# measured WARP row, the preview reads it, the epoch's reward decision is eligible, and the PoP
# share reaches the delegator: its x/distribution rewards grow, a withdrawal pays them into its
# bank balance, and the operator's commission accrues instead of a bank transfer.
#
# The old binary is built from OLD_REF in a temporary git worktree (removed at exit), the new
# one from the current tree. Both run natively on 127.0.0.1 ports 36656/36657/39090.
set -euo pipefail
source "$(dirname "$0")/lib-narrate.sh"

OLD_REF="${OLD_REF:-cfbd786}"
CHAIN_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
REPO_DIR="$(git -C "$CHAIN_DIR" rev-parse --show-toplevel)"
CHAIN_ID=saicon-rehearse-1
UPGRADE=v0.11.0
RPC_PORT=36657
NODE="tcp://127.0.0.1:${RPC_PORT}"
SCN=100000000
EPOCH=10

TMP="$(mktemp -d "${TMPDIR:-/tmp}/saicon-rehearse-s10.XXXXXX")"
HOME_DIR="$TMP/home"
OLD_BIN="$TMP/old/saicond"
NEW_BIN="$TMP/new/saicond"
WORKTREE="$TMP/old-src"
NODE_PID=""

stop_node() {
  [ -n "$NODE_PID" ] || return 0
  kill "$NODE_PID" 2>/dev/null || true
  for _ in $(seq 1 20); do kill -0 "$NODE_PID" 2>/dev/null || break; sleep 1; done
  kill -9 "$NODE_PID" 2>/dev/null || true
  NODE_PID=""
}
cleanup() {
  local rc=$?
  stop_node
  if [ "$rc" != 0 ]; then
    for f in old.log new.log; do
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
  local out hash r=""
  # stderr kept apart: the binary may print warnings there, and stdout must stay JSON.
  out=$("$bin" tx "$@" "${KR[@]}" "${TXF[@]}" 2>"$TMP/tx.err") || fail "tx broadcast failed: $out $(cat "$TMP/tx.err")"
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
# Integer part of the nscn amount in a DecCoins answer (0 when absent). The CLI prints a DecCoin
# as a string ("1541.5696nscn"); an object form ({denom, amount}) is read too.
nscn_of() {
  jq -r '[(.. | strings | select(test("^[0-9.]+nscn$")) | rtrimstr("nscn")),
          (.. | objects | select(.denom? == "nscn") | .amount)] | first // "0" | split(".")[0]'
}
bank_nscn() { "$1" q bank balance "$2" nscn --node "$NODE" --home "$HOME_DIR" -o json 2>/dev/null | jq -r '.balance.amount // "0"'; }
# The next height h >= $1 with h % EPOCH == 0, plus $2.
next_boundary() { echo $(( (($1 + EPOCH - 1) / EPOCH) * EPOCH + ${2:-0} )); }

# --- 1. the two binaries ------------------------------------------------------
step "Build the old binary ($OLD_REF) and the new one (this tree)"
git -C "$REPO_DIR" rev-parse --verify --quiet "${OLD_REF}^{commit}" >/dev/null || fail "OLD_REF '$OLD_REF' is not a commit/tag of this repository"
git -C "$REPO_DIR" worktree add --detach "$WORKTREE" "$OLD_REF" >/dev/null 2>&1 || fail "git worktree add $OLD_REF failed"
mkdir -p "$TMP/old" "$TMP/new"
(cd "$WORKTREE/chain" && go build -o "$OLD_BIN" ./cmd/saicond) || fail "building $OLD_REF failed"
(cd "$CHAIN_DIR" && go build -o "$NEW_BIN" ./cmd/saicond) || fail "building the current tree failed"
ok "old: $OLD_REF ($(git -C "$REPO_DIR" rev-parse --short "$OLD_REF^{commit}"))   new: current tree ($(git -C "$REPO_DIR" rev-parse --short HEAD))"

# --- 2. a single-node chain on the old binary ---------------------------------
step "A single-node chain on the old binary, with a delegator"
"$OLD_BIN" init rehearse --chain-id "$CHAIN_ID" --default-denom nscn --home "$HOME_DIR" >/dev/null 2>&1
"$OLD_BIN" keys add validator "${KR[@]}" --no-backup >/dev/null 2>&1
"$OLD_BIN" keys add delegator "${KR[@]}" --no-backup >/dev/null 2>&1
"$OLD_BIN" keys add attestor "${KR[@]}" --no-backup >/dev/null 2>&1
"$OLD_BIN" genesis add-genesis-account validator "1000000000000000nscn" "${KR[@]}"
"$OLD_BIN" genesis add-genesis-account delegator "$((100000 * SCN))nscn" "${KR[@]}"
"$OLD_BIN" genesis add-genesis-account attestor "$((100 * SCN))nscn" "${KR[@]}"
"$OLD_BIN" genesis gentx validator "100000000000000nscn" --chain-id "$CHAIN_ID" "${KR[@]}" >/dev/null 2>&1
"$OLD_BIN" genesis collect-gentxs --home "$HOME_DIR" >/dev/null 2>&1
GEN="$HOME_DIR/config/genesis.json"
ATT=$("$OLD_BIN" keys show attestor -a "${KR[@]}")
# A short voting period, a 10-block epoch, the devnet's cap floors, the attestor listed (a
# validator earns only once it is attested and so registry-ACTIVE).
jq --arg e "$EPOCH" --arg att "$ATT" '.app_state.gov.params.voting_period = "20s"
    | .app_state.entityregistry.params.attestors = [$att]
    | .app_state.gov.params.expedited_voting_period = "10s"
    | .app_state.stakeengine.params.validator_cap_floor = "200000000000000"
    | if (.app_state.stakeengine.params | has("entity_cap_floor")) then .app_state.stakeengine.params.entity_cap_floor = "200000000000000" else . end
    | .app_state.participation.params.epoch_length_blocks = $e' \
  "$GEN" > "$GEN.tmp" && mv "$GEN.tmp" "$GEN"
VG=$("$OLD_BIN" genesis validate-genesis --home "$HOME_DIR" 2>&1) || fail "genesis does not validate on the old binary: $(tail -3 <<<"$VG")"
sed -i.bak \
  -e 's#^timeout_commit = .*#timeout_commit = "2s"#' \
  -e "s#^laddr = \"tcp://127.0.0.1:26657\"#laddr = \"${NODE}\"#" \
  -e 's#^laddr = "tcp://0.0.0.0:26656"#laddr = "tcp://127.0.0.1:36656"#' \
  -e 's#^pprof_laddr = .*#pprof_laddr = "127.0.0.1:36060"#' "$HOME_DIR/config/config.toml"
sed -i.bak -e 's#^address = "localhost:9090"#address = "127.0.0.1:39090"#' "$HOME_DIR/config/app.toml"
VAL=$("$OLD_BIN" keys show validator --bech val -a "${KR[@]}")
OPER=$("$OLD_BIN" keys show validator -a "${KR[@]}")
DEL=$("$OLD_BIN" keys show delegator -a "${KR[@]}")
start_node "$OLD_BIN" "$TMP/old.log"
wait_height 3
ok "the old node is producing blocks (height $(height)); validator $VAL"

OQ() { "$OLD_BIN" q "$@" --node "$NODE" --home "$HOME_DIR" -o json; }
eq "$(OQ upgrade module_versions participation | jq -r '.module_versions[0].version')" 1 "x/participation consensus version before the upgrade"
OQ participation warps >/dev/null 2>&1 && fail "the old binary already serves the WARP query: OLD_REF is not a pre-S10 version"
OQ rewards voting-power-preview >/dev/null 2>&1 && fail "the old binary already serves the voting-power preview"
ok "the old binary has no WARP query and no voting-power preview (as it must not)"

tx "$OLD_BIN" entityregistry attest genesis-entity "Genesis" "$(printf '0b%.0s' $(seq 1 32))" "$VAL" --from attestor
wait_height $((TX_HEIGHT + 2))
eq "$(OQ validatorregistry validator "$VAL" | jq -r .validator.status)" STATUS_ACTIVE "the validator's registry status once attested"
tx "$OLD_BIN" staking delegate "$VAL" "$((1000 * SCN))nscn" --from delegator
ok "delegator $DEL delegated 1,000 SCN to the validator at height $TX_HEIGHT"

step "Before the upgrade: the PoP share goes to the operator's bank account, the delegator earns nothing"
B=$(next_boundary $(( $(height) + 1 )) 1)
OPER_BANK0=$(bank_nscn "$OLD_BIN" "$OPER")
wait_height "$B"
B2=$(( B - 1 + EPOCH + 1 ))
wait_height "$B2"
OPER_BANK1=$(bank_nscn "$OLD_BIN" "$OPER")
PRE_EPOCH=$(( (B2 - 1) / EPOCH ))
PRE_SNAP=$(OQ rewards snapshot "$VAL" "$PRE_EPOCH") || fail "no reward decision for epoch $PRE_EPOCH before the upgrade"
PRE_AMT=$(jq -r '.snapshot.amount' <<<"$PRE_SNAP" | sed 's/nscn$//')
[ "$(jq -r '.snapshot.eligible // false' <<<"$PRE_SNAP")" = true ] \
  || fail "epoch $PRE_EPOCH's reward decision is not eligible on the old binary: $(jq -c . <<<"$PRE_SNAP")"
ok "epoch $PRE_EPOCH's reward decision is eligible on the old binary"
[ "$OPER_BANK1" -gt "$OPER_BANK0" ] || fail "the operator's bank balance did not grow before the upgrade ($OPER_BANK0 -> $OPER_BANK1)"
ok "the operator's bank balance grew by $(( OPER_BANK1 - OPER_BANK0 )) nscn over the epochs closed (last share: $PRE_AMT nscn)"
DEL_REW0=$(OQ distribution rewards-by-validator "$DEL" "$VAL" | nscn_of)
eq "$DEL_REW0" 0 "the delegator's x/distribution rewards before the upgrade (fees are zero, PoP goes to the operator)"

# --- 3. the proposal ------------------------------------------------------------
step "Submit MsgSoftwareUpgrade $UPGRADE and vote"
DEPOSIT=$(jq -r '.app_state.gov.params.min_deposit[0] | .amount + .denom' "$GEN")
# At least 25 blocks ahead (the 20 s vote and two txs), placed mid-epoch.
UPGRADE_H=$(next_boundary $(( $(height) + 25 )) 5)
tx "$OLD_BIN" upgrade software-upgrade "$UPGRADE" --upgrade-height "$UPGRADE_H" \
  --title "$UPGRADE" --summary "S10: WARP, the voting-power preview, PoP rewards to delegators" \
  --deposit "$DEPOSIT" --no-validate --from validator
PROP=$(OQ gov proposals | jq -r '.proposals[-1].id')
ok "proposal $PROP submitted: upgrade $UPGRADE at height $UPGRADE_H"
tx "$OLD_BIN" gov vote "$PROP" yes --from validator
STATUS=""
for _ in $(seq 1 60); do
  STATUS=$(OQ gov proposal "$PROP" | jq -r '.proposal.status')
  [ "$STATUS" = PROPOSAL_STATUS_PASSED ] && break; sleep 2
done
eq "$STATUS" PROPOSAL_STATUS_PASSED "proposal $PROP"

# --- 4. the halt ----------------------------------------------------------------
step "The old node must stop at height $UPGRADE_H"
for _ in $(seq 1 120); do
  grep -q "UPGRADE \"$UPGRADE\" NEEDED at height: $UPGRADE_H" "$TMP/old.log" && break
  kill -0 "$NODE_PID" 2>/dev/null || break
  sleep 1
done
grep -q "UPGRADE \"$UPGRADE\" NEEDED at height: $UPGRADE_H" "$TMP/old.log" \
  || fail "no 'UPGRADE \"$UPGRADE\" NEEDED at height: $UPGRADE_H' in the old node's log"
HALT_H=$(height); sleep 6
eq "$(height)" "$HALT_H" "the old node's height is frozen at the halt"
[ -f "$HOME_DIR/data/upgrade-info.json" ] || fail "the old node left no data/upgrade-info.json for the new binary"
stop_node
ok "old node stopped (this is DevOps swapping the binary)"

# --- 5. the new binary on the same home ------------------------------------------
step "Start the NEW binary on the same home"
start_node "$NEW_BIN" "$TMP/new.log"
wait_height $((UPGRADE_H + 2))
kill -0 "$NODE_PID" 2>/dev/null || fail "the new node died"
ok "the new node is past the upgrade height: $(height) > $UPGRADE_H"

Q() { "$NEW_BIN" q "$@" --node "$NODE" --home "$HOME_DIR" -o json; }
step "What the upgrade did: x/participation 1 -> 2, the WARP params, the handler's record"
eq "$(Q upgrade module_versions participation | jq -r '.module_versions[0].version')" 2 "x/participation consensus version"
eq "$(Q upgrade module_versions rewards | jq -r '.module_versions[0].version')" 1 "x/rewards consensus version (route change only, no store change)"
PP=$(Q participation params)
eq "$(jq -r '.params.epoch_length_blocks' <<<"$PP")" "$EPOCH" "participation epoch_length_blocks (kept)"
eq "$(jq -r '.params.warp_fall' <<<"$PP" | sed 's/0*$//')" 0.65 "warp_fall"
eq "$(jq -r '.params.warp_rise' <<<"$PP" | sed 's/0*$//')" 0.1 "warp_rise"
eq "$(jq -r '.params.warp_initial' <<<"$PP" | sed 's/0*$//; s/\.$//')" 80 "warp_initial"
eq "$(jq -r '.params.warp_threshold' <<<"$PP" | sed 's/0*$//; s/\.$//')" 65 "warp_threshold"
LINE=$(sed 's/\x1b\[[0-9;]*m//g' "$TMP/new.log" | grep "v0.11.0: S10 WARP params in force" | tail -1 | cut -c1-400)
[ -n "$LINE" ] || fail "the handler's S10 line is missing from the new node's log"
grep -q "params=valid" <<<"$LINE" && grep -q "warp_rows=0" <<<"$LINE" && ok "handler log: $LINE" \
  || fail "the handler's S10 line does not say valid params and no rows: $LINE"

step "The voting-power preview answers right after the upgrade"
PV=$(Q rewards voting-power-preview) || fail "the voting-power preview query failed after the upgrade"
eq "$(jq -r '.validators | length' <<<"$PV")" 1 "validators in the preview"
eq "$(jq -r '.validators[0].operator_address' <<<"$PV")" "$VAL" "the previewed validator"
eq "$(jq -r '.validators[0].warp_measured // false' <<<"$PV")" false "warp_measured before the first epoch after the upgrade"
eq "$(jq -r '.validators[0].warp' <<<"$PV" | sed 's/0*$//; s/\.$//')" 80 "the previewed WARP (warp_initial)"

# --- 6. one epoch on the new binary ----------------------------------------------
step "One epoch on the new binary: WARP measured, the PoP share reaches the delegator"
DEL_REW1=$(Q distribution rewards-by-validator "$DEL" "$VAL" | nscn_of)
COMM1=$(Q distribution commission "$VAL" | nscn_of)
OPER_BANK2=$(bank_nscn "$NEW_BIN" "$OPER")
B=$(next_boundary $(( $(height) + 1 )) 0)
POST_EPOCH=$(( B / EPOCH ))
wait_height $(( B + 1 ))
W=$(Q participation warp "$VAL") || fail "no WARP row for the validator after epoch $POST_EPOCH"
ok "WARP after epoch $POST_EPOCH: $(jq -c .warp <<<"$W")"
eq "$(jq -r '.warp.last_epoch' <<<"$W")" "$POST_EPOCH" "the WARP row's last_epoch"
eq "$(jq -r '.warp.eligible // false' <<<"$W")" true "WARP's own eligibility (value >= 65)"
eq "$(Q participation warps | jq -r '.warps | length')" 1 "WARP rows"
PV=$(Q rewards voting-power-preview)
eq "$(jq -r '.validators[0].warp_measured // false' <<<"$PV")" true "the preview reads the measured WARP"
eq "$(jq -r '.validators[0].eligible // false' <<<"$PV")" true "the validator is eligible in the preview"
[ "$(jq -r '.validators[0].vp' <<<"$PV")" -gt 0 ] && ok "its preview vp: $(jq -r '.validators[0].vp' <<<"$PV") nscn" || fail "the preview vp is zero"
SNAP=$(Q rewards snapshot "$VAL" "$POST_EPOCH") || fail "no reward decision for epoch $POST_EPOCH"
eq "$(jq -r '.snapshot.eligible // false' <<<"$SNAP")" true "epoch $POST_EPOCH's reward decision"
SHARE=$(jq -r '.snapshot.amount' <<<"$SNAP" | sed 's/nscn$//')
[ "$SHARE" -gt 0 ] || fail "epoch $POST_EPOCH's share is zero"
ok "epoch $POST_EPOCH's PoP share: $SHARE nscn"
DEL_REW2=$(Q distribution rewards-by-validator "$DEL" "$VAL" | nscn_of)
COMM2=$(Q distribution commission "$VAL" | nscn_of)
OPER_BANK3=$(bank_nscn "$NEW_BIN" "$OPER")
[ "$DEL_REW2" -gt "$DEL_REW1" ] || fail "the delegator's rewards did not grow over the epoch ($DEL_REW1 -> $DEL_REW2): $(Q distribution rewards-by-validator "$DEL" "$VAL" | jq -c .) commission $(Q distribution commission "$VAL" | jq -c .) outstanding $(Q distribution validator-outstanding-rewards "$VAL" | jq -c .) delegation $(Q staking delegation "$DEL" "$VAL" | jq -c .)"
ok "the delegator's x/distribution rewards: $DEL_REW1 -> $DEL_REW2 nscn"
[ "$COMM2" -gt "$COMM1" ] || fail "the validator's commission did not grow over the epoch ($COMM1 -> $COMM2)"
ok "the validator's accrued commission: $COMM1 -> $COMM2 nscn"
eq "$OPER_BANK3" "$OPER_BANK2" "the operator's bank balance over the epoch (no direct transfer any more)"

step "The delegator claims with the stock MsgWithdrawDelegatorReward"
DEL_BANK0=$(bank_nscn "$NEW_BIN" "$DEL")
tx "$NEW_BIN" distribution withdraw-rewards "$VAL" --from delegator
DEL_BANK1=$(bank_nscn "$NEW_BIN" "$DEL")
[ "$DEL_BANK1" -gt "$DEL_BANK0" ] || fail "the withdrawal paid nothing ($DEL_BANK0 -> $DEL_BANK1)"
ok "the delegator's bank balance: $DEL_BANK0 -> $DEL_BANK1 nscn (+$(( DEL_BANK1 - DEL_BANK0 )))"

step "The chain keeps producing blocks on the new binary"
H1=$(height); wait_height $((H1 + 3)); ok "height $H1 -> $(height)"
step "Rehearsal complete: $OLD_REF -> $UPGRADE in place"
