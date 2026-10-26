#!/usr/bin/env bash
# Guards gov-evidence-window.sh. Needs a running local node whose genesis
# already carries the 21-day window (chain/docker/entrypoint-singlenode.sh sets
# it), so --check-only must pass there.
set -uo pipefail
HERE="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"

fails=0
check() {
  if [ "$2" = "$3" ]; then
    echo "  ok: $1"
  else
    echo "  FAIL: $1 — got '$2' want '$3'"
    fails=$((fails + 1))
  fi
}

bash "$HERE/gov-evidence-window.sh" --check-only >/dev/null 2>&1
check "check-only exits 0 on a covered chain" "$?" "0"

# grep -c counts matching LINES and both strings appear on more than one line
# (current value and required value), so assert presence, not an exact count.
out=$(bash "$HERE/gov-evidence-window.sh" --check-only 2>&1)
check "reports the required block count" \
  "$([ "$(printf '%s' "$out" | grep -c '362880')" -ge 1 ] && echo yes || echo no)" "yes"
check "states the unbonding period it compares against" \
  "$([ "$(printf '%s' "$out" | grep -ci 'unbonding')" -ge 1 ] && echo yes || echo no)" "yes"

# The whole point is that a gap is loud. Simulate an uncovered chain by telling
# the script the unbonding period is longer than the window can cover.
bash "$HERE/gov-evidence-window.sh" --check-only --unbonding-seconds 999999999 >/dev/null 2>&1
check "exits 1 when the window does not cover unbonding" "$?" "1"

out=$(bash "$HERE/gov-evidence-window.sh" --check-only --unbonding-seconds 999999999 2>&1)
check "names the gap in days" "$(printf '%s' "$out" | grep -c 'GAP')" "1"

# An unreachable API must fail loudly rather than report a false all-clear.
bash "$HERE/gov-evidence-window.sh" --check-only --api http://127.0.0.1:9 >/dev/null 2>&1
check "unreachable API is not a pass" "$?" "1"

[ "$fails" -eq 0 ] && echo "PASS" || { echo "FAIL ($fails)"; exit 1; }
