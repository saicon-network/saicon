#!/usr/bin/env bash
# gov-evidence-window.sh — check, and fix by governance, whether the evidence
# window is the unbonding period.
#
# THE RULE (project decision 2026-10-29, DEVELOPMENT.md section 4 item 7a)
#   max_age_duration   = unbonding_time
#   max_age_num_blocks = floor(unbonding_time / 6 s)
# CometBFT expires evidence only when BOTH limits are passed (evidence/pool.go
# isExpired uses `&&`, and x/evidence applies the same test), so the window is
# max(max_age_num_blocks x block time, max_age_duration). Under the rule it is
# exactly the unbonding period while the AVERAGE block interval over any
# unbonding-long span, chain halts included, is at most 6 s. At 5.01 s that
# absorbs about 3.5 days of halt in 21 days; a longer halt keeps evidence
# admissible past unbonding until enough blocks follow. The script measures the
# window in force right now (the age of the oldest block the block limit still
# protects), so run it with --check-only after any outage longer than ~3 days.
#
# Both directions are wrong. A window shorter than unbonding leaves a stretch in
# which a double-sign is proven, the stake is still bonded, and nothing can be
# done about it. A window longer than unbonding lets old evidence reach a
# validator re-created on the key after its unbonding completed (x/evidence
# upstream, and the removed-key record of the coordinated-attack sanction). So
# the script never proposes a value above the rule.
#
# WHEN IT IS NEEDED
# The v0.10.0 upgrade handler applies the rule (app/upgrades.go
# applyEvidenceWindow), and the genesis templates start a chain on it, so on the
# DevNet after v0.10.0 this script only confirms it. It stays for a chain made
# some other way (plain `saicond init` writes CometBFT's 100,000 blocks / 48h) or
# one whose governance moved the limits since: x/consensus is wired, so a
# governance MsgUpdateParams changes them on a running chain without a restart.
# A governance change to the staking unbonding_time desynchronises the window as
# well (a shorter unbonding leaves the window above it); the script reads the
# unbonding time from the chain, so it reports that too.
#
# Exit 0 = the limits are the rule and the window in force is not longer than
# unbonding ("ok, nothing to propose"). Exit 1 = the limits are not the rule (the
# proposal is written unless --check-only), the window in force outlasts
# unbonding after a halt (nothing to propose: tell the team), or the chain could
# not be reached. Exit 3 = the limits are the rule but the window in force could
# not be measured (no block readable at the edge, or an unparsable time): not a
# pass, since after an outage that measurement is the point. Safe to run from
# cron as a monitor.
#
# --secs-between LATER EARLIER prints the seconds between two CometBFT
# timestamps and exits (3 if one does not parse); the test uses it.
set -euo pipefail
source "$(dirname "${BASH_SOURCE[0]}")/lib-narrate.sh"

API="http://localhost:1317"
BIN="docker exec saicon-node saicond"
CHAIN_ID="saicon-localnet-1"
FROM="validator"
KEYFLAGS="--keyring-backend test --home /home/saicon/.saicon"
CHECK_ONLY=0
UNBONDING_SECONDS=""        # read from the chain's staking params unless given
MAX_BLOCK_TIME=6            # seconds; the rule's assumption (app/upgrades.go evidenceMaxBlockTime)
BLOCK_TIME=""               # measured from the chain unless given
PROP_OUT="/tmp/evidence-prop.json"
SB_LATER=""                 # --secs-between LATER EARLIER
SB_EARLIER=""

while [ $# -gt 0 ]; do
  case "$1" in
    --api) API="$2"; shift;;
    --bin) BIN="$2"; shift;;
    --chain-id) CHAIN_ID="$2"; shift;;
    --from) FROM="$2"; shift;;
    --unbonding-seconds) UNBONDING_SECONDS="$2"; shift;;
    --block-time) BLOCK_TIME="$2"; shift;;
    --out) PROP_OUT="$2"; shift;;
    --check-only) CHECK_ONLY=1;;
    --secs-between) SB_LATER="$2"; SB_EARLIER="$3"; shift 2;;
    -h|--help) sed -n '2,/^set -euo pipefail$/p' "$0" | sed '$d'; exit 0;;
    *) fail "unknown arg: $1";;
  esac
  shift
done

api() { curl -s --max-time 20 -H 'User-Agent: saicon-ops' "${API}$1"; }

# CometBFT writes RFC 3339 times with 0 to 9 fractional digits (trailing zeros
# trimmed) and a Z; Python 3.9's fromisoformat takes only 3 or 6 digits and no
# Z. The fraction is padded or cut to 6 digits and Z becomes +00:00 before
# parsing. Prints nothing when a time does not parse.
secs_between() { python3 - "$1" "$2" 2>/dev/null <<'PY' || true
import datetime, re, sys
def parse(s):
    m = re.fullmatch(r'(\d{4}-\d\d-\d\dT\d\d:\d\d:\d\d)(?:\.(\d+))?(Z|[+-]\d\d:\d\d)', s.strip())
    if not m:
        raise ValueError(s)
    frac = (m.group(2) or '')[:6].ljust(6, '0')
    tz = '+00:00' if m.group(3) == 'Z' else m.group(3)
    return datetime.datetime.fromisoformat(m.group(1) + '.' + frac + tz)
print((parse(sys.argv[1]) - parse(sys.argv[2])).total_seconds())
PY
}

if [ -n "$SB_LATER" ]; then
  r=$(secs_between "$SB_LATER" "$SB_EARLIER")
  [ -n "$r" ] || exit 3
  echo "$r"
  exit 0
fi

step "reading consensus params from ${API}"
PARAMS=$(api /cosmos/consensus/v1/params) || fail "cannot reach ${API}"
[ -n "$PARAMS" ] || fail "empty response from ${API} — chain unreachable"
CUR_BLOCKS=$(printf '%s' "$PARAMS" | jq -r '.params.evidence.max_age_num_blocks // empty' 2>/dev/null || true)
CUR_DUR=$(printf '%s' "$PARAMS" | jq -r '.params.evidence.max_age_duration // empty' 2>/dev/null | tr -d 's' || true)
[ -n "$CUR_BLOCKS" ] && [ -n "$CUR_DUR" ] || fail "no evidence params in the response from ${API}"

if [ -z "$UNBONDING_SECONDS" ]; then
  UNBONDING_SECONDS=$(api /cosmos/staking/v1beta1/params | jq -r '.params.unbonding_time // empty' 2>/dev/null | tr -d 's' || true)
  [ -n "$UNBONDING_SECONDS" ] || fail "could not read staking unbonding_time from ${API}"
fi
# Durations come back as decimal seconds ("1814400s", "1.5s"). They are handled
# as exact decimals, never floored: the upgrade writes max_age_duration = the
# unbonding time to the nanosecond, so a fractional unbonding time must match as
# it stands, and floor(unbonding / 6 s) is taken on the exact value as Go does.
pydec() { python3 -c "from decimal import Decimal as D, ROUND_FLOOR; $1"; }
UNBONDING_SECONDS=$(pydec "print(format(D('$UNBONDING_SECONDS').normalize(), 'f'))")
CUR_DUR=$(pydec "print(format(D('$CUR_DUR').normalize(), 'f'))")
WANT_BLOCKS=$(pydec "print(int((D('$UNBONDING_SECONDS') / $MAX_BLOCK_TIME).to_integral_value(ROUND_FLOOR)))")
WANT_DUR=$UNBONDING_SECONDS
[ "$WANT_BLOCKS" -gt 0 ] || fail "unbonding_time ${UNBONDING_SECONDS}s is shorter than one ${MAX_BLOCK_TIME}s block: CometBFT refuses max_age_num_blocks 0"

block_time_at() { api "/cosmos/base/tendermint/v1beta1/blocks/$1" | jq -r '.block.header.time // empty' 2>/dev/null || true; }

LATEST=$(api /cosmos/base/tendermint/v1beta1/blocks/latest)
H=$(printf '%s' "$LATEST" | jq -r '.block.header.height // empty' 2>/dev/null || true)
T_NOW=$(printf '%s' "$LATEST" | jq -r '.block.header.time // empty' 2>/dev/null || true)
[ -n "$H" ] && [ -n "$T_NOW" ] || fail "could not read the latest block from ${API}"

# The block time is not part of the rule; it shows what the stored limits mean
# in days, and whether the average interval stays at or below 6 s.
if [ -z "$BLOCK_TIME" ]; then
  if [ "$H" -gt 120 ]; then
    SPAN=$(secs_between "$T_NOW" "$(block_time_at $((H - 100)))")
    [ -n "$SPAN" ] && BLOCK_TIME=$(python3 -c "print(round($SPAN / 100, 2))")
  fi
  if [ -z "$BLOCK_TIME" ]; then
    BLOCK_TIME=$MAX_BLOCK_TIME
    ok "block time not measured (height ${H}) — taking the rule's ${MAX_BLOCK_TIME}s for the day figures"
  else
    ok "block time: ${BLOCK_TIME}s (measured over 100 blocks)"
  fi
fi

# The window in force right now. Evidence at height h is admissible while
# H - h <= max_age_num_blocks OR its age <= max_age_duration, so the block limit
# alone keeps everything from block max(1, H - max_age_num_blocks) on, however
# old. After a halt that block can be older than the unbonding period. A pruned
# block is skipped for the next one, which only makes the figure smaller.
EDGE_H=$(( H > CUR_BLOCKS ? H - CUR_BLOCKS : 1 ))
LIVE_S=""
for h in $(seq "$EDGE_H" $(( EDGE_H + 20 > H ? H : EDGE_H + 20 ))); do
  T_EDGE=$(block_time_at "$h")
  if [ -n "$T_EDGE" ]; then LIVE_S=$(secs_between "$T_NOW" "$T_EDGE"); EDGE_H=$h; break; fi
done

days() { python3 -c "print(round($1 / 86400, 2))"; }
COVER_BLOCKS_D=$(days "$CUR_BLOCKS * $BLOCK_TIME")
COVER_DUR_D=$(days "$CUR_DUR")
UNBOND_D=$(days "$UNBONDING_SECONDS")
# Expiry is AND, so the window is the longer of the two.
WINDOW_S=$(python3 -c "print(max($CUR_BLOCKS * $BLOCK_TIME, $CUR_DUR))")
EFFECTIVE_D=$(days "$WINDOW_S")

ok "max_age_num_blocks = ${CUR_BLOCKS} → ${COVER_BLOCKS_D} days at ${BLOCK_TIME}s"
ok "max_age_duration   = ${CUR_DUR}s → ${COVER_DUR_D} days"
ok "effective window: ${EFFECTIVE_D} days — expiry is AND, so the longer governs"
ok "unbonding period: ${UNBONDING_SECONDS}s = ${UNBOND_D} days"
ok "the rule: max_age_num_blocks ${WANT_BLOCKS} (unbonding / ${MAX_BLOCK_TIME}s), max_age_duration ${WANT_DUR}s"

LIVE_EXCESS=0
if [ -n "$LIVE_S" ]; then
  ok "in force now: the block limit keeps evidence from block ${EDGE_H} on, $(days "$LIVE_S") days (${LIVE_S}s) old"
  LIVE_EXCESS=$(python3 -c "print(1 if $LIVE_S > $UNBONDING_SECONDS and $LIVE_S > $CUR_DUR else 0)")
else
  printf '\033[0;31m✗ could not measure the window in force: no readable block time from block %s to the 20 after it\033[0m\n' "$EDGE_H" >&2
fi
if [ "$LIVE_EXCESS" = "1" ]; then
  printf '\033[0;31m✗ EXCESS NOW: evidence %s days (%ss) old is still admissible, longer than unbonding — a halt or slow blocks made the block limit govern. Tell the team.\033[0m\n' \
    "$(days "$LIVE_S")" "$LIVE_S" >&2
fi

if python3 -c "import sys; sys.exit(0 if $BLOCK_TIME > $MAX_BLOCK_TIME else 1)"; then
  printf '\033[0;33m! blocks are slower than %ss: even under the rule the block limit governs and the window is unbonding x %s/%s. Tell the team.\033[0m\n' \
    "$MAX_BLOCK_TIME" "$BLOCK_TIME" "$MAX_BLOCK_TIME" >&2
fi

AT_RULE=$(pydec "print(1 if $CUR_BLOCKS == $WANT_BLOCKS and D('$CUR_DUR') == D('$WANT_DUR') else 0)")
if [ "$AT_RULE" = "1" ]; then
  if [ -z "$LIVE_S" ]; then
    ok "nothing to propose: the limits are the rule, but the window in force is unknown — not a pass"
    exit 3
  fi
  if [ "$LIVE_EXCESS" = "1" ]; then
    ok "nothing to propose: the limits are the rule; the excess ends once enough blocks follow"
    exit 1
  fi
  ok "ok, nothing to propose: the evidence window is the unbonding period"
  exit 0
fi

DIRECTION=$(python3 -c "
w, u = $WINDOW_S, $UNBONDING_SECONDS
print('GAP' if w < u else ('EXCESS' if w > u else 'EQUAL'))")
case "$DIRECTION" in
  GAP)
    printf '\033[0;31m✗ GAP: %s days in which a proven double-sign cannot be slashed while the stake is still bonded\033[0m\n' \
      "$(python3 -c "print(round(($UNBONDING_SECONDS - $WINDOW_S) / 86400, 2))")" >&2 ;;
  EXCESS)
    printf '\033[0;31m✗ EXCESS: the window outlasts unbonding by %s days; old evidence could reach a validator re-created on its key\033[0m\n' \
      "$(python3 -c "print(round(($WINDOW_S - $UNBONDING_SECONDS) / 86400, 2))")" >&2 ;;
  *)
    printf '\033[0;33m! the window is the unbonding period at %ss blocks, but the limits are not the rule (it holds only at this block time)\033[0m\n' \
      "$BLOCK_TIME" >&2 ;;
esac

if [ "$CHECK_ONLY" -eq 1 ]; then
  exit 1
fi

step "building the proposal"
AUTH=$($BIN query auth module-accounts -o json \
  | jq -r '.accounts[] | select(.value.name=="gov") | .value.address')
[ -n "$AUTH" ] || fail "could not resolve the gov module address"
ok "authority: ${AUTH}"

# MsgUpdateParams replaces the block, evidence and validator groups wholesale, so
# they are echoed back unchanged except the two age limits, which are set to the
# rule and never above it. abci is left out: x/consensus keeps it when absent.
jq -n \
  --arg auth "$AUTH" \
  --arg mb "$(printf '%s' "$PARAMS" | jq -r '.params.block.max_bytes')" \
  --arg mg "$(printf '%s' "$PARAMS" | jq -r '.params.block.max_gas')" \
  --arg eb "$WANT_BLOCKS" \
  --arg ed "${WANT_DUR}s" \
  --arg emb "$(printf '%s' "$PARAMS" | jq -r '.params.evidence.max_bytes')" \
  --argjson pk "$(printf '%s' "$PARAMS" | jq -c '.params.validator.pub_key_types')" '{
  messages: [{
    "@type": "/cosmos.consensus.v1.MsgUpdateParams",
    authority: $auth,
    block: { max_bytes: $mb, max_gas: $mg },
    evidence: { max_age_num_blocks: $eb, max_age_duration: $ed, max_bytes: $emb },
    validator: { pub_key_types: $pk }
  }],
  metadata: "evidence-window-is-unbonding",
  deposit: "10000000nscn",
  title: "The evidence window is the unbonding period",
  summary: ("Set max_age_num_blocks to " + $eb + " and max_age_duration to " + $ed +
            ": a double-sign stays punishable exactly as long as the stake stays bonded, and no longer.")
}' > "$PROP_OUT"
ok "proposal written to ${PROP_OUT}"

step "submit and vote"
cat >&2 <<EOF
Copy the file to wherever the CLI can read it, then:

  ${BIN} tx gov submit-proposal ${PROP_OUT} --from ${FROM} ${KEYFLAGS} \\
      --chain-id ${CHAIN_ID} --fees 5000nscn -y

  ${BIN} query gov proposals -o json | jq -r '.proposals[-1].id'

  ${BIN} tx gov vote <ID> yes --from ${FROM} ${KEYFLAGS} \\
      --chain-id ${CHAIN_ID} --fees 5000nscn -y

Wait out the voting period, then re-run this script with --check-only.
Exit 0 means it took.
EOF
exit 1
