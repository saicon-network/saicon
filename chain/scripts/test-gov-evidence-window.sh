#!/usr/bin/env bash
# Guards gov-evidence-window.sh. Needs a running node whose genesis carries the
# rule (chain/docker/entrypoint-singlenode.sh and gen-genesis.sh set it: 302,400
# blocks and 1,814,400 s for the 21-day unbonding), so --check-only must pass
# there. API and BIN default to the single-node container; point them at any
# node, e.g. a native one:
#   API=http://127.0.0.1:1317 BIN="saicond --node tcp://127.0.0.1:26657" bash test-gov-evidence-window.sh
set -uo pipefail
HERE="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
API="${API:-http://localhost:1317}"
BIN="${BIN:-docker exec saicon-node saicond}"
OUT="$(mktemp "${TMPDIR:-/tmp}/evidence-prop.XXXXXX")"
trap 'rm -f "$OUT"' EXIT
script() { bash "$HERE/gov-evidence-window.sh" --api "$API" "$@"; }

fails=0
check() {
  if [ "$2" = "$3" ]; then
    echo "  ok: $1"
  else
    echo "  FAIL: $1 — got '$2' want '$3'"
    fails=$((fails + 1))
  fi
}
has() { [ "$(printf '%s' "$1" | grep -c -- "$2")" -ge 1 ] && echo yes || echo no; }

# A chain at the rule: nothing to propose.
out=$(script --check-only 2>&1); rc=$?
check "check-only exits 0 on a chain at the rule" "$rc" "0"
check "says there is nothing to propose" "$(has "$out" 'ok, nothing to propose')" "yes"
check "reports the rule's block count" "$(has "$out" 'max_age_num_blocks 302400')" "yes"
check "reads the unbonding period from the chain" "$(has "$out" 'unbonding period: 1814400s')" "yes"
check "measures the window in force now" "$(has "$out" 'in force now: the block limit keeps evidence from block')" "yes"

# The whole point is that a gap is loud. Simulate an uncovered chain by telling
# the script the unbonding period is longer than the window covers.
out=$(script --check-only --unbonding-seconds 999999999 2>&1); rc=$?
check "exits 1 when the window does not cover unbonding" "$rc" "1"
check "names the gap in days" "$(printf '%s' "$out" | grep -c 'GAP')" "1"

# A window longer than unbonding is just as wrong.
out=$(script --check-only --unbonding-seconds 864000 2>&1); rc=$?
check "exits 1 when the window outlasts unbonding" "$rc" "1"
check "names the excess" "$(printf '%s' "$out" | grep -c 'EXCESS')" "1"

# The proposal sets the rule's values and never more, in both directions; a
# fractional unbonding time is kept exact in the duration and floored only in
# the block count, as the upgrade does.
for case in "864000 144000" "999999999 166666666" "1814400.5 302400"; do
  set -- $case
  script --unbonding-seconds "$1" --bin "$BIN" --out "$OUT" >/dev/null 2>&1
  check "proposal mode exits 1 (a proposal to submit) for unbonding $1" "$?" "1"
  check "proposes max_age_num_blocks floor($1 / 6)" \
    "$(jq -r '.messages[0].evidence.max_age_num_blocks' "$OUT" 2>/dev/null)" "$2"
  check "proposes max_age_duration $1s" "$(jq -r '.messages[0].evidence.max_age_duration' "$OUT" 2>/dev/null)" "${1}s"
  : > "$OUT"
done

# CometBFT trims trailing zeros from the fraction, so a time can carry 0 to 9
# digits; Python 3.9's fromisoformat takes only 3 or 6 without the parser's
# normalisation.
for case in "2026-10-29T10:00:01.5Z|2026-10-29T10:00:00Z|1.5" \
            "2026-10-29T10:00:00.12Z|2026-10-29T10:00:00.1Z|0.02" \
            "2026-10-29T10:00:00.1234Z|2026-10-29T10:00:00.12345Z|-5e-05" \
            "2026-10-29T10:00:00.123456789Z|2026-10-29T10:00:00Z|0.123456" \
            "2026-10-29T12:00:00+02:00|2026-10-29T10:00:00.000001Z|-1e-06"; do
  IFS='|' read -r later earlier want <<<"$case"
  check "seconds between $later and $earlier" \
    "$(bash "$HERE/gov-evidence-window.sh" --secs-between "$later" "$earlier" 2>/dev/null)" "$want"
done
bash "$HERE/gov-evidence-window.sh" --secs-between "not-a-time" "2026-10-29T10:00:00Z" >/dev/null 2>&1
check "an unparsable time exits 3" "$?" "3"

# Fail closed: a chain at the rule whose edge block cannot be read is not a pass
# (exit 3). A static server stands in for the API: the params and the latest
# block answer, every other block height is a 404.
MOCK="$(mktemp -d "${TMPDIR:-/tmp}/evidence-mock.XXXXXX")"
mkdir -p "$MOCK/cosmos/consensus/v1" "$MOCK/cosmos/staking/v1beta1" "$MOCK/cosmos/base/tendermint/v1beta1/blocks"
echo '{"params":{"evidence":{"max_age_num_blocks":"302400","max_age_duration":"1814400s","max_bytes":"1048576"}}}' \
  > "$MOCK/cosmos/consensus/v1/params"
echo '{"params":{"unbonding_time":"1814400s"}}' > "$MOCK/cosmos/staking/v1beta1/params"
echo '{"block":{"header":{"height":"500000","time":"2026-10-29T10:00:00.5Z"}}}' \
  > "$MOCK/cosmos/base/tendermint/v1beta1/blocks/latest"
MOCK_PORT=18917
python3 -m http.server "$MOCK_PORT" --bind 127.0.0.1 --directory "$MOCK" >/dev/null 2>&1 &
MOCK_PID=$!
for _ in $(seq 1 20); do curl -s --max-time 1 "http://127.0.0.1:$MOCK_PORT/cosmos/staking/v1beta1/params" >/dev/null && break; sleep 0.2; done
out=$(bash "$HERE/gov-evidence-window.sh" --check-only --api "http://127.0.0.1:$MOCK_PORT" 2>&1); rc=$?
kill "$MOCK_PID" 2>/dev/null; wait "$MOCK_PID" 2>/dev/null; rm -rf "$MOCK"
check "an unmeasurable window in force exits 3, not 0" "$rc" "3"
check "says it could not measure the window in force" "$(has "$out" 'could not measure the window in force')" "yes"
check "does not claim ok" "$(has "$out" 'ok, nothing to propose')" "no"

# An unreachable API must fail loudly rather than report a false all-clear.
bash "$HERE/gov-evidence-window.sh" --check-only --api http://127.0.0.1:9 >/dev/null 2>&1
check "unreachable API is not a pass" "$?" "1"

[ "$fails" -eq 0 ] && echo "PASS" || { echo "FAIL ($fails)"; exit 1; }
