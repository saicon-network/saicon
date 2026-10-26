#!/usr/bin/env bash
# Guards node-metrics.sh. Needs a running local node.
#
# The failure this is really guarding against is a sampler that produces a
# plausible-looking CSV full of zeros: docker stats formats change, du can fail
# silently inside a container, and a file with the right header and no signal is
# indistinguishable from a healthy one at a glance. So the checks assert that
# values MOVE, not merely that columns exist.
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

OUT=/tmp/metrics-test.csv
rm -f "$OUT"
bash "$HERE/node-metrics.sh" --seconds 24 --interval 6 --out "$OUT" >/dev/null 2>&1
check "exits 0" "$?" "0"
check "header is exact" "$(head -1 "$OUT")" "ts,height,data_kb,rss_mb,cpu_pct,node_cpu_pct,net_rx_kb,net_tx_kb"
check "took at least 4 samples" \
  "$([ "$(tail -n +2 "$OUT" | wc -l | tr -d ' ')" -ge 4 ] && echo yes || echo no)" "yes"
check "height advances" \
  "$([ "$(tail -1 "$OUT" | cut -d, -f2)" -gt "$(sed -n 2p "$OUT" | cut -d, -f2)" ] && echo yes || echo no)" "yes"
check "disk is non-zero" \
  "$([ "$(tail -1 "$OUT" | cut -d, -f3)" -gt 0 ] && echo yes || echo no)" "yes"
check "rss is non-zero" \
  "$(awk -F, 'NR>1 && $4+0>0 {n++} END{print (n>0)?"yes":"no"}' "$OUT")" "yes"
# node_cpu_pct is a delta, so row 2 is 0 by construction; later rows must move.
check "node-only cpu is measured" \
  "$(awk -F, 'NR>2 && $6+0>0 {n++} END{print (n>0)?"yes":"no"}' "$OUT")" "yes"
check "no row has an empty field" \
  "$(awk -F, 'NR>1 && (NF!=8 || $1==""||$2==""||$3==""||$4==""||$5==""||$6==""||$7==""||$8=="") {n++} END{print (n>0)?"no":"yes"}' "$OUT")" "yes"
# A negative CPU is the signature of a counter reset; it must never reach the CSV.
check "no negative cpu values" \
  "$(awk -F, 'NR>1 && ($5+0<0 || $6+0<0) {n++} END{print (n>0)?"no":"yes"}' "$OUT")" "yes"
check "prints a KB/block summary" \
  "$(bash "$HERE/node-metrics.sh" --seconds 12 --interval 6 --out /tmp/metrics-test2.csv 2>&1 | grep -c 'KB/block')" "1"

[ "$fails" -eq 0 ] && echo "PASS" || { echo "FAIL ($fails)"; exit 1; }
