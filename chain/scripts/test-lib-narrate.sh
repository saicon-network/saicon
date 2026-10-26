#!/usr/bin/env bash
# Guards the shared narration helpers. They look trivial, but three scripts and
# the e2e check depend on them writing to stderr — if any of them starts writing
# to stdout, every `$(...)` capture in those callers silently swallows commentary
# as data.
set -uo pipefail
HERE="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
source "$HERE/lib-narrate.sh"

fails=0
check() {
  if [ "$2" = "$3" ]; then
    echo "  ok: $1"
  else
    echo "  FAIL: $1 — got '$2' want '$3'"
    fails=$((fails + 1))
  fi
}

out=$(ok "hello" 2>&1)
check "ok writes the message" "$(printf '%s' "$out" | grep -c hello)" "1"

out=$(step "phase" 2>&1)
check "step writes the message" "$(printf '%s' "$out" | grep -c phase)" "1"

out=$(runq "some cmd" 2>&1)
check "runq echoes the command" "$(printf '%s' "$out" | grep -c 'some cmd')" "1"

( fail "boom" >/dev/null 2>&1 )
check "fail exits 1" "$?" "1"

# The property the callers actually rely on: nothing lands on stdout.
out=$( { ok "x"; step "y"; runq "z"; } 2>/dev/null )
check "nothing goes to stdout" "$(printf '%s' "$out" | wc -c | tr -d ' ')" "0"

[ "$fails" -eq 0 ] && echo "PASS" || { echo "FAIL ($fails)"; exit 1; }
