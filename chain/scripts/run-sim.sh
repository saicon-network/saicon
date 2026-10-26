#!/usr/bin/env bash
# Run one SDK simulation and fail if it did not actually execute.
#
# `go test` exits 0 when a test calls t.Skip, so a simulation that never ran
# reports exactly the same green as one that passed. That is how this suite sat
# green for months having executed nothing, and it is how `make test-sim` reported
# success on 2026-08-24 while TestAppStateDeterminism skipped on "empty validator
# set" — the one check that catches consensus-breaking non-determinism, silently
# absent from a release gate.
#
# Here a skip is a failure. The only reason to run a simulation before a release is
# to learn something, and a skip teaches nothing.
#
# Usage: ./scripts/run-sim.sh <TestName> <seed> [go test flags...]
# Run from chain/ — the -Params path and ./app/ are relative to it.

set -uo pipefail

test_name=${1:?test name required}
seed=${2:?seed required}
shift 2

log=$(mktemp -t saicon-sim)
trap 'rm -f "$log"' EXIT

# Redirect, then read $? directly. Reading it through a pipe would report tee's
# or grep's status instead of the test's.
go test -mod=readonly ./app/ -run "$test_name" "$@" -Seed="$seed" >"$log" 2>&1
rc=$?

cat "$log"

if grep -q -- '--- SKIP' "$log"; then
	echo
	echo "FAIL: ${test_name} SKIPPED — a skip is not a pass, the simulation did not run."
	exit 1
fi

exit "$rc"
