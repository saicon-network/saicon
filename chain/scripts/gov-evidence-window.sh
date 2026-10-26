#!/usr/bin/env bash
# gov-evidence-window.sh — check, and fix, whether the evidence window covers
# the unbonding period.
#
# THE PROBLEM
# A validator that double-signs can only be slashed while the evidence is still
# admissible. If that window is shorter than the unbonding period, there is a
# stretch in which the offence is proven, the stake is still bonded, and nothing
# can be done about it.
#
# CometBFT expires evidence only when BOTH limits are passed — evidence/pool.go
# isExpired uses `&&` — so the LONGER of max_age_num_blocks and max_age_duration
# governs, and both have to be raised. Stock defaults are 100,000 blocks and 48h;
# at a 5s block that is 5.8 days against SAiCoN's 21-day unbonding.
#
# WHY THIS NEEDS NO RESTART
# Evidence limits are consensus params, and x/consensus is wired, so a governance
# MsgUpdateParams changes them on a running chain. Verified end to end on a local
# node before this script was written.
#
# Exit 0 = the window covers unbonding. Exit 1 = it does not, or the chain could
# not be reached. Safe to run from cron as a monitor.
set -euo pipefail
source "$(dirname "${BASH_SOURCE[0]}")/lib-narrate.sh"

API="http://localhost:1317"
BIN="docker exec saicon-node saicond"
CHAIN_ID="saicon-localnet-1"
FROM="validator"
KEYFLAGS="--keyring-backend test --home /home/saicon/.saicon"
CHECK_ONLY=0
UNBONDING_SECONDS=1814400   # 21 days — must match staking unbonding_time (V-5)
BLOCK_TIME=""               # measured from the chain unless given
PROP_OUT="/tmp/evidence-prop.json"

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
    -h|--help) sed -n '2,22p' "$0"; exit 0;;
    *) fail "unknown arg: $1";;
  esac
  shift
done

api() { curl -s --max-time 20 -H 'User-Agent: saicon-ops' "${API}$1"; }

step "reading consensus params from ${API}"
PARAMS=$(api /cosmos/consensus/v1/params) || fail "cannot reach ${API}"
[ -n "$PARAMS" ] || fail "empty response from ${API} — chain unreachable"
CUR_BLOCKS=$(printf '%s' "$PARAMS" | jq -r '.params.evidence.max_age_num_blocks // empty')
CUR_DUR=$(printf '%s' "$PARAMS" | jq -r '.params.evidence.max_age_duration // empty' | tr -d 's')
[ -n "$CUR_BLOCKS" ] && [ -n "$CUR_DUR" ] || fail "no evidence params in the response from ${API}"

# Measure the block time rather than assume it: the whole comparison is
# "how many days is max_age_num_blocks worth", which is meaningless without it.
if [ -z "$BLOCK_TIME" ]; then
  LATEST=$(api /cosmos/base/tendermint/v1beta1/blocks/latest)
  H=$(printf '%s' "$LATEST" | jq -r '.block.header.height')
  if [ "$H" -gt 120 ]; then
    T_NOW=$(printf '%s' "$LATEST" | jq -r '.block.header.time')
    T_OLD=$(api "/cosmos/base/tendermint/v1beta1/blocks/$((H - 100))" | jq -r '.block.header.time')
    BLOCK_TIME=$(python3 -c "
import datetime,sys
f=lambda s: datetime.datetime.fromisoformat(s.replace('Z','+00:00'))
print(round((f('$T_NOW')-f('$T_OLD')).total_seconds()/100, 2))")
  else
    BLOCK_TIME=5
    ok "chain too young to measure block time (height ${H}) — assuming ${BLOCK_TIME}s"
  fi
fi

# Derive the block count from a block time 20% FASTER than measured. A count
# sized to exactly today's pace under-covers the moment the chain speeds up:
# 362,155 blocks is 21.0 days at 5.01s but only 20.5 days at 4.9s.
#
# The real guarantee is max_age_duration, not this: expiry is AND, so evidence
# cannot expire before the duration limit no matter how many blocks pass. The
# block count is the second half of the condition and simply must not be the
# thing that runs out first.
SAFETY=0.8
WANT_BLOCKS=$(python3 -c "
import math; print(math.ceil($UNBONDING_SECONDS / ($BLOCK_TIME * $SAFETY)))")
COVER_BLOCKS_D=$(python3 -c "print(round($CUR_BLOCKS * $BLOCK_TIME / 86400, 1))")
COVER_DUR_D=$(python3 -c "print(round($CUR_DUR / 86400, 1))")
UNBOND_D=$(python3 -c "print(round($UNBONDING_SECONDS / 86400, 1))")
# Expiry is AND, so the window actually in force is the longer of the two.
EFFECTIVE_D=$(python3 -c "print(max($COVER_BLOCKS_D, $COVER_DUR_D))")

ok "block time: ${BLOCK_TIME}s (measured)"
ok "max_age_num_blocks = ${CUR_BLOCKS} → ${COVER_BLOCKS_D} days"
ok "max_age_duration   = ${CUR_DUR}s → ${COVER_DUR_D} days"
ok "effective window: ${EFFECTIVE_D} days — expiry is AND, so the longer governs"
ok "unbonding period: ${UNBOND_D} days"
ok "required max_age_num_blocks: ${WANT_BLOCKS} (sized for a block time 20% faster than measured)"
ok "required max_age_duration: ${UNBONDING_SECONDS}s — this is what actually bounds expiry"

# The safety gate is max_age_duration ALONE.
#
# Expiry is `blocksExceeded && durationExceeded`, so evidence stays admissible
# while EITHER limit is unexceeded. A duration of 21 days therefore means nothing
# can expire inside 21 days however many blocks pass — the block count cannot
# open a hole by itself, it is only the other half of the conjunction.
#
# Gating on both (as an earlier version of this script did) flags a correct chain
# as broken: our own genesis carries 362,880 blocks, which is exactly 21 days at
# the current pace and perfectly safe, yet fails a check that demands a margin
# the block half does not need.
DUR_OK=$(python3 -c "print(1 if $CUR_DUR >= $UNBONDING_SECONDS else 0)")
BLOCKS_TIGHT=$(python3 -c "print(1 if $CUR_BLOCKS < $WANT_BLOCKS else 0)")

if [ "$DUR_OK" = "1" ]; then
  ok "max_age_duration covers unbonding — evidence cannot expire early"
  if [ "$BLOCKS_TIGHT" = "1" ]; then
    printf '\033[0;33m! max_age_num_blocks (%s) is below the %s that a 20%%-faster chain would want.\n  Not a hole — duration bounds expiry on its own — but worth raising at the next params change.\033[0m\n' \
      "$CUR_BLOCKS" "$WANT_BLOCKS" >&2
  fi
  exit 0
fi

GAP_D=$(python3 -c "print(round($UNBOND_D - $EFFECTIVE_D, 1))")
printf '\033[0;31m✗ GAP: %s days in which a proven double-sign cannot be slashed while the stake is still bonded\033[0m\n' "$GAP_D" >&2

if [ "$CHECK_ONLY" -eq 1 ]; then
  exit 1
fi

step "building the proposal"
AUTH=$($BIN query auth module-accounts -o json \
  | jq -r '.accounts[] | select(.value.name=="gov") | .value.address')
[ -n "$AUTH" ] || fail "could not resolve the gov module address"
ok "authority: ${AUTH}"

# MsgUpdateParams replaces every section wholesale, so block and validator must
# be echoed back unchanged or they are silently reset.
jq -n \
  --arg auth "$AUTH" \
  --arg mb "$(printf '%s' "$PARAMS" | jq -r '.params.block.max_bytes')" \
  --arg mg "$(printf '%s' "$PARAMS" | jq -r '.params.block.max_gas')" \
  --arg eb "$WANT_BLOCKS" \
  --arg ed "${UNBONDING_SECONDS}s" \
  --arg emb "$(printf '%s' "$PARAMS" | jq -r '.params.evidence.max_bytes')" \
  --argjson pk "$(printf '%s' "$PARAMS" | jq -c '.params.validator.pub_key_types')" '{
  messages: [{
    "@type": "/cosmos.consensus.v1.MsgUpdateParams",
    authority: $auth,
    block: { max_bytes: $mb, max_gas: $mg },
    evidence: { max_age_num_blocks: $eb, max_age_duration: $ed, max_bytes: $emb },
    validator: { pub_key_types: $pk }
  }],
  metadata: "evidence-window-covers-unbonding",
  deposit: "10000000nscn",
  title: "Evidence window must cover the unbonding period",
  summary: ("Raise max_age_num_blocks to " + $eb + " and max_age_duration to " + $ed +
            " so a double-sign stays punishable for as long as the stake stays bonded.")
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
