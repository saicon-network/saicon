#!/usr/bin/env bash
# load-test.sh — drive a sustained transaction rate and measure what it costs.
#
# WHY THIS EXISTS
# CPU and RAM cannot be sized from an idle node. Block execution, IAVL hashing
# and RPC serving are all absent from an idle measurement, which is exactly how
# the published "4 cores / 16 GB" came to rest on nothing. This produces the
# numbers that let those figures be stated honestly, or shows they are wrong.
#
# It measures idle first and load second, and reports the ratio, because the
# absolute peak means little without knowing what the same node did at rest.
set -euo pipefail
HERE="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
source "$HERE/lib-narrate.sh"

BIN="docker exec saicon-node saicond"
HOME_FLAGS="--keyring-backend test --home /home/saicon/.saicon"
CHAIN_ID="saicon-localnet-1"
FROM="validator"
TX_PER_BLOCK=5
SECONDS_TOTAL=300
IDLE_SECONDS=60
BLOCK_TIME=5

while [ $# -gt 0 ]; do
  case "$1" in
    --tx-per-block) TX_PER_BLOCK="$2"; shift;;
    --seconds) SECONDS_TOTAL="$2"; shift;;
    --idle-seconds) IDLE_SECONDS="$2"; shift;;
    --chain-id) CHAIN_ID="$2"; shift;;
    --from) FROM="$2"; shift;;
    --block-time) BLOCK_TIME="$2"; shift;;
    -h|--help) sed -n '2,14p' "$0"; exit 0;;
    *) fail "unknown arg: $1";;
  esac
  shift
done

step "preparing"
$BIN keys add loadsink $HOME_FLAGS >/dev/null 2>&1 || true
SINK=$($BIN keys show loadsink -a $HOME_FLAGS) || fail "cannot resolve the sink address"
SENDER=$($BIN keys show "$FROM" -a $HOME_FLAGS) || fail "cannot resolve ${FROM}"
ok "sending ${TX_PER_BLOCK} tx/block from ${FROM} to ${SINK}"

step "baseline — ${IDLE_SECONDS}s idle"
bash "$HERE/node-metrics.sh" --seconds "$IDLE_SECONDS" --interval 10 --out /tmp/load-idle.csv

step "load — ${SECONDS_TOTAL}s at ${TX_PER_BLOCK} tx/block"
bash "$HERE/node-metrics.sh" --seconds "$SECONDS_TOTAL" --interval 10 --out /tmp/load-busy.csv &
METRICS_PID=$!

# Sequence numbers are supplied explicitly. Broadcasting several transactions
# inside one block means the account's on-chain sequence has not caught up with
# what has been sent, so letting the CLI look it up rejects everything after the
# first with "account sequence mismatch".
ACC_JSON=$($BIN query auth account "$SENDER" -o json) || fail "cannot read the sender account"
SEQ=$(printf '%s' "$ACC_JSON" | jq -r '.account.value.sequence // .sequence // "0"')
# account_number is absent from the response when it is 0 — proto3 omits zero
# values — so an unguarded lookup yields null and every broadcast is rejected.
ACCNUM=$(printf '%s' "$ACC_JSON" | jq -r '.account.value.account_number // .account_number // "0"')
ok "starting sequence ${SEQ}, account number ${ACCNUM}"

sent=0; rejected=0
end=$(( $(date +%s) + SECONDS_TOTAL ))
while [ "$(date +%s)" -lt "$end" ]; do
  for _ in $(seq 1 "$TX_PER_BLOCK"); do
    if $BIN tx bank send "$SENDER" "$SINK" 1nscn $HOME_FLAGS \
         --chain-id "$CHAIN_ID" --fees 0nscn \
         --sequence "$SEQ" --account-number "$ACCNUM" \
         --broadcast-mode sync -y -o json >/dev/null 2>&1; then
      sent=$((sent + 1))
    else
      rejected=$((rejected + 1))
    fi
    SEQ=$((SEQ + 1))
  done
  sleep "$BLOCK_TIME"
done
wait "$METRICS_PID"
ok "broadcast ${sent} accepted, ${rejected} rejected"

# A run where most transactions bounced measured an idle node with extra steps.
if [ "$sent" -lt "$rejected" ]; then
  fail "more transactions were rejected than accepted — the load never landed, do not use these numbers"
fi

step "idle vs load"
python3 - /tmp/load-idle.csv /tmp/load-busy.csv "$TX_PER_BLOCK" <<'PY'
import csv, sys

def summarise(path):
    rows = list(csv.DictReader(open(path)))
    # node_cpu_pct, not cpu_pct: the load generator runs `saicond tx` inside the
    # measured container, so the container figure includes five signing processes
    # per block. Sizing from it would bake the cost of the measurement into the
    # requirement. Row 0 is skipped — the node column is a delta.
    cpu = [float(r['node_cpu_pct']) for r in rows[1:]] or [0.0]
    rss = [float(r['rss_mb']) for r in rows]
    dh = int(rows[-1]['height']) - int(rows[0]['height'])
    dd = int(rows[-1]['data_kb']) - int(rows[0]['data_kb'])
    return max(cpu), max(rss), (dd / dh if dh else 0.0)

idle = summarise(sys.argv[1])
busy = summarise(sys.argv[2])
rate = float(sys.argv[3])

print(f"  {'':<16}{'idle':>10}{'load':>10}{'ratio':>9}")
for name, a, b in (("node cpu peak %", idle[0], busy[0]),
                   ("rss peak MB", idle[1], busy[1]),
                   ("KB per block", idle[2], busy[2])):
    print(f"  {name:<16}{a:>10.1f}{b:>10.1f}{(f'{b/a:.1f}x' if a else 'n/a'):>9}")

print()
print(f"  Measured at {rate:g} tx/block. Paste into hardware-model.py:")
print(f"    LOAD_PEAK_CPU_FRACTION = {busy[0]/100:.3f}")
print(f"    LOAD_PEAK_RSS_MB       = {busy[1]:.0f}")
print(f"    LOAD_TX_PER_BLOCK      = {rate:g}")
if busy[0] < idle[0] * 1.2:
    print()
    print("  ⚠ CPU barely moved. Either the rate is too low to load this node, or")
    print("    the transactions are not landing — check the accepted/rejected counts")
    print("    above before treating this as a load measurement.")
PY
