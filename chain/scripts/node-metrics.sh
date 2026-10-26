#!/usr/bin/env bash
# node-metrics.sh — sample a running node's resource use into CSV.
#
# WHY
# The published hardware requirements were set before there was a node to
# measure, and the disk figures that replaced them extrapolate from two samples
# on a chain a few dozen blocks old. This produces the observations that replace
# the arithmetic.
#
# Columns: ts, height, data_kb, rss_mb, cpu_pct, node_cpu_pct, net_rx_kb, net_tx_kb
#
# TWO CPU columns, and the difference matters. cpu_pct is what docker reports
# for the whole CONTAINER; node_cpu_pct is PID 1 alone, read from /proc.
# When load is generated with `docker exec ... saicond tx`, the signing CLI runs
# inside the same container, so cpu_pct includes the cost of the measurement
# itself — idle, the container reads 20% while the node process reads 12%.
# Size hardware from node_cpu_pct. Both are percentages of ONE core: 250% is
# 2.5 cores, not a saturated machine.
#
# Unit handling is deliberate: docker prints MiB or GiB for memory and B, kB, MB
# or GB for network, switching as values grow. A parser that assumes one unit
# reads 7.663GiB as 7.663 and reports a node using 8 GB as using 8 MB, so every
# figure goes through the same normaliser.
set -euo pipefail
source "$(dirname "${BASH_SOURCE[0]}")/lib-narrate.sh"

CONTAINER="saicon-node"
DATA_DIR="/home/saicon/.saicon/data"
RPC="http://localhost:26657"
SECONDS_TOTAL=600
INTERVAL=15
OUT="/tmp/node-metrics.csv"

while [ $# -gt 0 ]; do
  case "$1" in
    --container) CONTAINER="$2"; shift;;
    --data-dir) DATA_DIR="$2"; shift;;
    --rpc) RPC="$2"; shift;;
    --seconds) SECONDS_TOTAL="$2"; shift;;
    --interval) INTERVAL="$2"; shift;;
    --out) OUT="$2"; shift;;
    -h|--help) sed -n '2,18p' "$0"; exit 0;;
    *) fail "unknown arg: $1";;
  esac
  shift
done

docker inspect "$CONTAINER" >/dev/null 2>&1 || fail "container '${CONTAINER}' not found"

echo "ts,height,data_kb,rss_mb,cpu_pct,node_cpu_pct,net_rx_kb,net_tx_kb" > "$OUT"
PREV_TICKS=""; PREV_TS=""; RESET_SEEN=0
step "sampling ${CONTAINER} every ${INTERVAL}s for ${SECONDS_TOTAL}s → ${OUT}"

elapsed=0
while [ "$elapsed" -lt "$SECONDS_TOTAL" ]; do
  ts=$(date +%s)
  height=$(curl -s --max-time 5 "${RPC}/status" 2>/dev/null \
    | jq -r '.result.sync_info.latest_block_height // 0' 2>/dev/null || echo 0)
  data_kb=$(docker exec "$CONTAINER" du -sk "$DATA_DIR" 2>/dev/null | cut -f1 || echo 0)
  stats=$(docker stats "$CONTAINER" --no-stream \
    --format '{{.MemUsage}}|{{.CPUPerc}}|{{.NetIO}}' 2>/dev/null || echo "0B / 0B|0%|0B / 0B")
  # PID 1 only: utime + stime from /proc, in USER_HZ ticks (100/s on Linux).
  ticks=$(docker exec "$CONTAINER" sh -c "awk '{print \$14+\$15}' /proc/1/stat" 2>/dev/null || echo 0)
  # A restarted container resets /proc/1/stat, so the delta goes negative. Left
  # unguarded that lands in the CSV as a negative CPU percentage and silently
  # drags down any mean computed from it later. Clamp and say so.
  if [ -n "$PREV_TICKS" ] && [ "$ts" -gt "${PREV_TS:-$ts}" ]; then
    if [ "$ticks" -lt "$PREV_TICKS" ]; then
      node_cpu=0
      printf '\033[0;31m✗ node restarted mid-sample (tick counter went backwards) — this run is not usable\033[0m\n' >&2
      RESET_SEEN=1
    else
      node_cpu=$(python3 -c "print(round(($ticks-$PREV_TICKS)/100/($ts-$PREV_TS)*100, 2))")
    fi
  else
    node_cpu=0
  fi
  PREV_TICKS="$ticks"; PREV_TS="$ts"

  python3 - "$ts" "$height" "$data_kb" "$stats" "$node_cpu" >> "$OUT" <<'PY'
import re, sys
ts, height, data_kb, stats, node_cpu = sys.argv[1:6]

UNITS = {'b': 1, 'kb': 1e3, 'mb': 1e6, 'gb': 1e9, 'tb': 1e12,
         'kib': 1024, 'mib': 1024**2, 'gib': 1024**3, 'tib': 1024**4}

def to_bytes(s):
    m = re.match(r'\s*([\d.]+)\s*([a-zA-Z]+)', s or '')
    if not m:
        return 0.0
    return float(m.group(1)) * UNITS.get(m.group(2).lower(), 1)

mem, cpu, net = (stats.split('|') + ['', '', ''])[:3]
rss_mb = to_bytes(mem.split('/')[0]) / 1024**2
cpu_pct = float(re.sub(r'[^\d.]', '', cpu) or 0)
rx, _, tx = (net.split('/') + ['', ''])[0], None, (net.split('/') + ['', ''])[1]
print(f"{ts},{height},{data_kb or 0},{rss_mb:.1f},{cpu_pct:.2f},{node_cpu},"
      f"{to_bytes(rx)/1024:.1f},{to_bytes(tx)/1024:.1f}")
PY

  sleep "$INTERVAL"
  elapsed=$((elapsed + INTERVAL))
done

ok "wrote $(( $(wc -l < "$OUT") - 1 )) samples"
if [ "$RESET_SEEN" = "1" ]; then
  fail "the node restarted during this sample — height and disk both went backwards, so nothing here can be used for sizing. Re-run against a node nobody is touching."
fi

step "summary"
python3 - "$OUT" <<'PY'
import csv, sys
rows = list(csv.DictReader(open(sys.argv[1])))
if len(rows) < 2:
    print("  too few samples to summarise", file=sys.stderr)
    raise SystemExit(0)

dh = int(rows[-1]['height']) - int(rows[0]['height'])
dd = int(rows[-1]['data_kb']) - int(rows[0]['data_kb'])
dt = int(rows[-1]['ts']) - int(rows[0]['ts'])
out = []
if dh > 0:
    bt = dt / dh
    per_block = dd / dh
    out.append(f"  blocks {dh} over {dt}s → block time {bt:.2f}s")
    out.append(f"  disk   {dd} KB / {dh} blocks = {per_block:.2f} KB/block")
    out.append(f"         = {per_block*86400/bt/1024:.1f} MB/day"
               f" = {per_block*86400*365/bt/1024**2:.1f} GB/year")
else:
    out.append(f"  no new blocks in {dt}s — is the chain halted?")

cpu = [float(r['cpu_pct']) for r in rows]
# Skip the first sample: node_cpu_pct is a delta and has no predecessor.
ncpu = [float(r['node_cpu_pct']) for r in rows[1:]] or [0.0]
rss = [float(r['rss_mb']) for r in rows]
out.append(f"  cpu    container mean {sum(cpu)/len(cpu):.1f}%  peak {max(cpu):.1f}%")
out.append(f"         node only mean {sum(ncpu)/len(ncpu):.1f}%  peak {max(ncpu):.1f}%  ← size from this")
out.append(f"  rss    mean {sum(rss)/len(rss):.0f} MB  peak {max(rss):.0f} MB")
if max(ncpu) < 1.0:
    out.append("  ⚠ the node was effectively idle — these numbers cannot size CPU or RAM")
print("\n".join(out), file=sys.stderr)
PY
