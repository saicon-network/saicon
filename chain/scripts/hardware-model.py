#!/usr/bin/env python3
"""
hardware-model.py — what a SAiCoN validator node actually needs, as a function of
the things that drive it: validator count, block time, transaction load, and
whether smart contracts are live.

WHY THIS EXISTS
The published requirements (4 cores / 16 GB / 1 TB / 100 Mbps) were set before
there was a running node to measure. This model replaces the guess with terms you
can argue with one at a time. Every constant below is tagged:

    [M] measured on our own node   — reproducible, see MEASUREMENTS
    [C] configuration             — read from the shipped config, not invented
    [D] derived                   — arithmetic from a protocol fact
    [R] reference                 — published by Cosmos Hub, used as a sanity check
    [A] assumed                   — a judgement call; the ones to challenge first

Run:
    python3 hardware-model.py                 # table for the planned scenarios
    python3 hardware-model.py --chart out.svg # growth curves
    python3 hardware-model.py --json          # machine-readable

Nothing here validates production code. It sizes hardware from declared
parameters and one set of measurements at N=1 and N=3.
"""

import argparse
import json
import math

# ─────────────────────────────────────────────────────────────────────────────
# MEASUREMENTS — taken 2026-07-30 on the shipped image, single node, empty chain
# ─────────────────────────────────────────────────────────────────────────────
# Recalibrated 2026-08-04 from a 30-minute run: 60 samples, 375 blocks, one
# validator, no transactions, no restart (node-metrics.sh guards for that now).
#
# The estimate is stable across sample size — 14.08 KB/block at 25 blocks,
# 13.54 at 57, 13.72 at 121, 13.89 at 248, 13.99 at 375 — so the LevelDB
# compaction bias I warned about is real but small: the original 20-block
# constant of 14.4 was 3% high, not the large overstatement I assumed.
DISK_PER_BLOCK_EMPTY_N1_KB = 13.99         # [M] 375 blocks, 1 validator, 0 tx
DISK_PER_BLOCK_EMPTY_N3_KB = 22.7          # [M] 3 validators, 0 tx, 25 blocks
                                           #     ⚠ still the short sample
IDLE_RSS_MB = 151.0                        # [M] node RSS mean; peak 163
# Node process only, from /proc/1/stat — NOT the container figure, which
# includes anything else running inside it.
IDLE_CPU_FRACTION = 0.147                  # [M] mean of one core; peak 0.174

# The slope between those two points. This is the single most consequential
# number in the file, and it is 13.7x larger than the arithmetic of "one extra
# CommitSig plus one validator-set entry" predicts (310 B). CometBFT persists
# ABCI responses and validator-set records per height, and LevelDB charges per
# write rather than per byte, so a validator costs far more on disk than its
# signature does on the wire.
#
# ⚠ TWO POINTS ARE A LINE, NOT A CURVE. Extrapolating N=1,3 out to N=75 is the
# largest single risk in this model. It is also measured on a chain a few dozen
# blocks old, before LevelDB has compacted, which biases it HIGH — safe for
# sizing, possibly wasteful. Re-measure on the restarted devnet before any of
# this is published as a requirement.
DISK_PER_VALIDATOR_PER_BLOCK_B = (
    (DISK_PER_BLOCK_EMPTY_N3_KB - DISK_PER_BLOCK_EMPTY_N1_KB) * 1024 / 2)  # [M] ≈ 4260

# From load-test.sh, 2026-08-04: 5 tx/block sustained for 150s, 130 accepted /
# 0 rejected, measured on the node process rather than the container.
#
# The headline is that memory does not respond. RSS was 155.2 MB idle and
# 155.6 MB under load, inside the 151-163 noise band of the idle run — at this
# rate memory is simply not the sensitive resource. CPU and disk both move.
LOAD_PEAK_CPU_FRACTION = 0.190             # [M] of one core, node process
LOAD_PEAK_RSS_MB = 156                     # [M]
LOAD_TX_PER_BLOCK = 5                      # [M] the rate the peaks were taken at
LOAD_DISK_KB_PER_BLOCK = 20.6              # [M] against 13.1 idle in the same run

# ─────────────────────────────────────────────────────────────────────────────
# CONFIGURATION — read from the running node, not chosen here
# ─────────────────────────────────────────────────────────────────────────────
BLOCK_MAX_BYTES = 22_020_096               # [C] consensus param, 21 MiB
TIMEOUT_COMMIT_S = 5.0                     # [C] config.toml
MAX_INBOUND_PEERS = 40                     # [C]
MAX_OUTBOUND_PEERS = 10                    # [C]
PEER_RATE_LIMIT_BPS = 5_120_000            # [C] send_rate = recv_rate, per peer
MEMPOOL_MAX_BYTES = 1_073_741_824          # [C] max_txs_bytes — 1 GiB (!)
IAVL_CACHE_NODES = 781_250                 # [C] app.toml
TX_INDEXER_ON = True                       # [C] indexer = "kv"
# Retention is now a profile, set by NODE_PROFILE in entrypoint-singlenode.sh.
# The validator default keeps blocks only to the evidence window; archive keeps
# everything. Before 2026-08-04 the shipped config had min-retain-blocks = 0 with
# state pruned after 21 days — archive cost, pruned-node query range.
MIN_RETAIN_BLOCKS_VALIDATOR = 362_880      # [C] = the evidence window
MIN_RETAIN_BLOCKS_ARCHIVE = 0              # [C] 0 = never pruned
PRUNING_KEEP_RECENT = 100_000              # [C] validator profile, state versions
SNAPSHOT_INTERVAL = 1000                   # [C] state-sync snapshots, both profiles

# ─────────────────────────────────────────────────────────────────────────────
# REFERENCE — Cosmos Hub's published figures, docs.cosmos.network/hub/v25
# ─────────────────────────────────────────────────────────────────────────────
# Used as a sanity check on the far end of the curve, not as our requirement.
# The Hub carries years of state on a chain with far more traffic; our node's
# 151 MB is what a young chain with almost no state looks like. The gap between
# the two is the trajectory, and it is why RAM must be stated as "today, and
# rising" rather than as a fixed number.
#
# Notably the Hub publishes NO CPU figure at all — for any node type. Neither
# does CometBFT. Nobody with far more production experience than us is willing
# to name a core count, which is worth remembering before we name one.
HUB_VALIDATOR_RAM_GB = 32                  # [R] Cosmos Hub, validator
HUB_FULL_RAM_GB = 16                       # [R] Cosmos Hub, full node
HUB_VALIDATOR_DISK_GB_LOW = 500            # [R] "500GB-2TB, depends on pruning"
HUB_VALIDATOR_DISK_GB_HIGH = 2000          # [R]
HUB_ARCHIVE_STATE_TB = 1.4                 # [R] cosmoshub-4 archive state

# ─────────────────────────────────────────────────────────────────────────────
# DERIVED — arithmetic from protocol facts
# ─────────────────────────────────────────────────────────────────────────────
# A CommitSig on the wire: 64 B signature + 20 B validator address + timestamp
# + block-ID flag, plus protobuf framing. Stored twice: once in the block's
# commit (blockstore.db) and once in the per-height state record (state.db).
VOTE_BYTES = 110                           # [D]
VOTE_STORED_COPIES = 2                     # [D] blockstore + state
# A validator set entry is persisted per height by CometBFT state storage.
VALSET_ENTRY_BYTES = 90                    # [D]
# ed25519 verification cost, one signature, one modern core.
ED25519_VERIFY_US = 50                     # [D]
# CPU DOES NOT SCALE MEANINGFULLY WITH VALIDATOR COUNT, and this is the sum that
# says so. A block carries N precommits; verifying them costs N x 50us. At 75
# validators that is 3.75 ms per 5-second block — 0.075% of one core. At 150 it
# is 0.15%. The thing that moves CPU is transaction throughput, not set size:
# our own measurement went 14.7% idle to 19.0% at 5 tx/block, which is 57x the
# entire signature cost of a 75-validator set.
#
# This matters for what we publish. A core count quoted "for a 75-validator
# network" implies the number tracks the set, and it does not.

# ─────────────────────────────────────────────────────────────────────────────
# ASSUMED — challenge these first
# ─────────────────────────────────────────────────────────────────────────────
# A plain bank/staking transaction, signed, protobuf-encoded.
AVG_TX_BYTES = 320                         # [A]
# The transaction index stores the tx plus its events, keyed several ways.
TX_INDEX_AMPLIFICATION = 1.4               # [A]
# IAVL writes per transaction: touched keys, each with tree-node overhead.
IAVL_BYTES_PER_TX = 900                    # [A]
# Per epoch, per validator: one participation snapshot + one reward snapshot.
IAVL_BYTES_PER_VALIDATOR_EPOCH = 320       # [A]
# Gossip amplification: CometBFT only sends parts a peer lacks, so a node does
# not re-send the whole block to every peer. 3x is a working figure, not a
# measurement — it is the single largest source of error in the network model.
GOSSIP_AMPLIFICATION = 3.0                 # [A]
# A WASM contract call writes far more state than a bank transfer.
CONTRACT_IAVL_MULTIPLIER = 6.0             # [A]
CONTRACT_TX_BYTES_MULTIPLIER = 2.5         # [A]
# Free-space headroom the operator must keep on top of computed usage.
FREE_SPACE_FRACTION = 0.25                 # [A]
# Sizing horizon: a validator should not have to resize disk within this window.
DEFAULT_HORIZON_YEARS = 2.0                # [A]


class Scenario:
    def __init__(self, name, validators, block_time_s=TIMEOUT_COMMIT_S,
                 tx_per_block=0.0, contracts=False, peers=None,
                 epoch_blocks=None, horizon_years=DEFAULT_HORIZON_YEARS,
                 prune_blocks=False):
        self.name = name
        self.n = validators
        self.bt = block_time_s
        self.tx = tx_per_block
        self.contracts = contracts
        # A node peers with as much of the set as it can, bounded by config.
        self.peers = peers if peers is not None else min(
            validators - 1, MAX_INBOUND_PEERS + MAX_OUTBOUND_PEERS)
        self.peers = max(self.peers, 1)
        # One epoch is ~24h of blocks unless told otherwise.
        self.epoch_blocks = epoch_blocks or max(1, round(86400 / block_time_s))
        self.horizon = horizon_years
        self.prune_blocks = prune_blocks

    # ── disk ────────────────────────────────────────────────────────────────
    def bytes_per_block(self):
        tx_bytes = self.tx * AVG_TX_BYTES * (
            CONTRACT_TX_BYTES_MULTIPLIER if self.contracts else 1.0)
        iavl_tx = self.tx * IAVL_BYTES_PER_TX * (
            CONTRACT_IAVL_MULTIPLIER if self.contracts else 1.0)
        index = tx_bytes * TX_INDEX_AMPLIFICATION if TX_INDEXER_ON else 0.0
        # Measured slope, not the arithmetic of a signature — see the note on
        # DISK_PER_VALIDATOR_PER_BLOCK_B. The N=1 measurement is the intercept,
        # so only validators beyond the first are charged the slope.
        per_validator = (self.n - 1) * DISK_PER_VALIDATOR_PER_BLOCK_B
        base = DISK_PER_BLOCK_EMPTY_N1_KB * 1024
        epoch_amortized = (self.n * IAVL_BYTES_PER_VALIDATOR_EPOCH) / self.epoch_blocks
        return {
            'base': base,
            'consensus_per_validator': per_validator,
            'transactions': tx_bytes + iavl_tx + index,
            'epoch_records': epoch_amortized,
        }

    def blocks_per_year(self):
        return 365 * 86400 / self.bt

    def disk_gb(self, years=None):
        years = self.horizon if years is None else years
        parts = self.bytes_per_block()
        per_block = sum(parts.values())
        if self.prune_blocks:
            # Pruning removes historical blocks but not current state; the state
            # component keeps growing.
            per_block = parts['transactions'] * 0.35 + parts['epoch_records']
        raw = per_block * self.blocks_per_year() * years
        return raw / 1024 ** 3

    def disk_recommended_gb(self, years=None):
        return self.disk_gb(years) / (1 - FREE_SPACE_FRACTION)

    # ── network ─────────────────────────────────────────────────────────────
    def network_mbps(self):
        parts = self.bytes_per_block()
        # Only what actually crosses the wire: block payload + votes.
        on_wire = (parts['base'] * 0.25 + parts['transactions']
                   + self.n * VOTE_BYTES)
        per_block_total = on_wire * GOSSIP_AMPLIFICATION * self.peers
        return per_block_total * 8 / self.bt / 1e6

    def network_peak_mbps(self):
        # Worst case the consensus params permit: a full block, every block.
        return BLOCK_MAX_BYTES * 8 * GOSSIP_AMPLIFICATION / self.bt / 1e6

    def traffic_tb_month(self):
        return self.network_mbps() * 1e6 / 8 * 86400 * 30 / 1024 ** 4

    # ── cpu ─────────────────────────────────────────────────────────────────
    def cpu_cores(self):
        # Per block: verify N precommits, verify tx signatures, run the app.
        sig_us = self.n * ED25519_VERIFY_US
        tx_us = self.tx * ED25519_VERIFY_US * 1.2
        # Our own EndBlock invariants walk the registry every block: V-1, V-3,
        # V-5 are O(N) state reads each.
        invariant_us = self.n * 3 * 12
        # Epoch boundary is the spike: participation + rewards for every
        # validator, amortized here but sized for below.
        epoch_us = self.n * 900
        steady = (sig_us + tx_us + invariant_us) / (self.bt * 1e6)
        spike = (sig_us + tx_us + invariant_us + epoch_us) / (self.bt * 1e6)
        idle = IDLE_CPU_FRACTION
        # Prefer the measured load peak over the analytic estimate. The analytic
        # terms below are a lower bound by construction — they count signature
        # verification and invariant walks and nothing else, while a real block
        # also commits IAVL, serialises gossip and answers RPC.
        if LOAD_PEAK_CPU_FRACTION and LOAD_TX_PER_BLOCK:
            # Scale the measured peak by this scenario's rate relative to the
            # rate it was taken at. Linear is a guess: it was measured at one
            # rate on one validator, so treat anything far from 5 tx/block as
            # extrapolation, not measurement.
            scaled = LOAD_PEAK_CPU_FRACTION * max(1.0, self.tx / LOAD_TX_PER_BLOCK)
            # 4x headroom: a validator that saturates a core misses blocks.
            return {
                'steady_cores': idle + steady,
                'epoch_spike_cores': scaled,
                'recommended': max(2, math.ceil(scaled * 4)),
                'source': 'measured',
            }
        return {
            'steady_cores': idle + steady,
            'epoch_spike_cores': idle + spike,
            'recommended': max(2, math.ceil((idle + spike) * 4)),
            'source': 'idle-only',
        }

    # ── memory ──────────────────────────────────────────────────────────────
    def ram_gb(self):
        iavl = IAVL_CACHE_NODES * 220 / 1024 ** 3
        peers = self.peers * 4 / 1024
        # The mempool cap is 1 GiB of transactions; a node under load can hold
        # it. This is config, not physics — lowering it lowers the requirement.
        mempool = min(MEMPOOL_MAX_BYTES, self.tx * AVG_TX_BYTES * 5000) / 1024 ** 3
        base = IDLE_RSS_MB / 1024
        total = base + iavl + peers + mempool
        return {'model_gb': total, 'recommended_gb': max(4, 2 ** math.ceil(math.log2(max(total * 2, 4))))}


SCENARIOS = [
    Scenario('devnet today',        validators=1,  tx_per_block=0),
    Scenario('launch, 6 nodes',     validators=6,  tx_per_block=0.5),
    Scenario('active set 60',       validators=60, tx_per_block=5),
    Scenario('active set 75',       validators=75, tx_per_block=10),
    Scenario('75 + contracts',      validators=75, tx_per_block=25, contracts=True),
    Scenario('75 + contracts, 2s',  validators=75, tx_per_block=25, contracts=True,
             block_time_s=2.0),
]


def cpu_ram_recommendation():
    """CPU and RAM, separating what was measured from what was extrapolated.

    The measurement is one validator on one machine at one rate. Everything
    beyond that rate is arithmetic on a single slope, and the further out it
    goes the less it is worth. Marked accordingly.
    """
    # Cost per transaction, from the only two points we have.
    per_tx = (LOAD_PEAK_CPU_FRACTION - IDLE_CPU_FRACTION) / LOAD_TX_PER_BLOCK
    rows = []
    for label, tx, measured in (
            ("DevNet today (0 tx/block)", 0.0, True),
            ("Launch, light use (5)", 5.0, True),
            ("Busy (25)", 25.0, False),
            ("Heavy / contracts (100)", 100.0, False)):
        frac = IDLE_CPU_FRACTION + per_tx * tx
        rows.append((label, frac, max(2, math.ceil(frac * 4)), measured))
    return per_tx, rows


def print_cpu_ram():
    per_tx, rows = cpu_ram_recommendation()
    print("CPU — from measurement, not from the validator count\n")
    print(f"  {'workload':<28}{'core use':>10}{'x4 headroom':>13}  basis")
    print('  ' + '-' * 66)
    for label, frac, cores, measured in rows:
        basis = "measured" if measured else "extrapolated"
        print(f"  {label:<28}{frac*100:>9.1f}%{cores:>12}   {basis}")
    print()
    print(f"  Each transaction costs about {per_tx*100:.2f}% of a core, from 14.7% idle")
    print(f"  to 19.0% at 5 tx/block. Only the first two rows sit inside the")
    print(f"  measured range; the rest is one slope carried a long way.")
    print()
    print("  The validator count is NOT what drives this. Verifying 75 precommits")
    print(f"  costs {75*ED25519_VERIFY_US/1000:.2f} ms per block — "
          f"{75*ED25519_VERIFY_US/1e6/TIMEOUT_COMMIT_S*100:.3f}% of a core. Transaction")
    print("  throughput moves CPU; set size does not.")
    print()
    print("RAM — the number we should not publish yet\n")
    print(f"  Measured:        {IDLE_RSS_MB:.0f} MB idle, {LOAD_PEAK_RSS_MB} MB at "
          f"{LOAD_TX_PER_BLOCK:g} tx/block — no response to load")
    print(f"  Cosmos Hub says: {HUB_VALIDATOR_RAM_GB} GB validator, "
          f"{HUB_FULL_RAM_GB} GB full node")
    print()
    print(f"  A {HUB_VALIDATOR_RAM_GB/0.151:.0f}x gap, and it is not a contradiction —")
    print("  it is the trajectory. RAM is dominated by the IAVL working set, and our")
    print("  chain has almost no state yet. The Hub's figure is what years of it")
    print("  looks like. Publishing 151 MB would be true today and wrong within a")
    print("  year; publishing 32 GB would price out the small operators the lottery")
    print("  seats exist for. State it as a floor that rises, with the measurement")
    print("  and the reference both shown.")
    print()
    print("  Neither Cosmos Hub nor CometBFT publishes a CPU figure for any node")
    print("  type. That is worth weighing before we publish one.")
    print()


def fmt_row(s):
    cpu, ram = s.cpu_cores(), s.ram_gb()
    return (f"{s.name:<22} {s.n:>4} {s.bt:>5.1f} {s.tx:>6.1f} "
            f"{s.disk_recommended_gb():>9.0f} {s.network_mbps():>8.1f} "
            f"{s.traffic_tb_month():>8.2f} {cpu['recommended']:>5} "
            f"{ram['recommended_gb']:>5.0f}")


def print_table():
    print(f"\nSAiCoN node sizing — {DEFAULT_HORIZON_YEARS:.0f}-year horizon, "
          f"{FREE_SPACE_FRACTION:.0%} free space kept\n")
    print(f"{'scenario':<22} {'val':>4} {'blk s':>5} {'tx/blk':>6} "
          f"{'disk GB':>9} {'net Mbps':>8} {'TB/mo':>8} {'cores':>5} {'RAM GB':>5}")
    print('-' * 84)
    for s in SCENARIOS:
        print(fmt_row(s))
    print()
    if any(s.cpu_cores().get('source') == 'idle-only' for s in SCENARIOS):
        print("⚠ Cores and RAM are LOWER BOUNDS — no load test has run, so block")
        print("  execution, IAVL hashing and RPC serving are absent from them.")
        print("  Do not publish a CPU or RAM minimum from this model.")
    else:
        print(f"Cores come from a measured load peak ({LOAD_PEAK_CPU_FRACTION:.0%} of one core at "
              f"{LOAD_TX_PER_BLOCK:g} tx/block, node process only) x4 headroom.")
        print(f"RAM did not respond to that load — {LOAD_PEAK_RSS_MB} MB against "
              f"{IDLE_RSS_MB:.0f} MB idle — so the RAM column is a floor with headroom,")
        print("  not a measured requirement. One validator, one machine, one rate.")
    print()
    ref = SCENARIOS[0]
    print(f"Empty chain, one validator: {ref.disk_gb(1):.1f} GB/year with zero "
          f"traffic — the floor nobody can go below.")
    print(f"Consensus params permit a {BLOCK_MAX_BYTES/1024**2:.0f} MiB block; "
          f"sustained, that is {SCENARIOS[0].network_peak_mbps():.0f} Mbps and "
          f"{BLOCK_MAX_BYTES*17280/1024**3:.0f} GB/day. The cap, not the "
          f"expectation — but it is what an attacker may attempt.")
    pruned = Scenario('pruned', validators=75, tx_per_block=10, prune_blocks=True)
    full = Scenario('full', validators=75, tx_per_block=10)
    print()
    print(f"The disk column above assumes the ARCHIVE profile — nothing pruned. "
          f"The shipped default is the validator profile, which keeps blocks only "
          f"to the {MIN_RETAIN_BLOCKS_VALIDATOR:,}-block evidence window:")
    print(f"  75 validators, archive:   {full.disk_recommended_gb():>6.0f} GB over "
          f"{DEFAULT_HORIZON_YEARS:.0f} years")
    print(f"  75 validators, validator: {pruned.disk_recommended_gb():>6.0f} GB — "
          f"{full.disk_recommended_gb()/pruned.disk_recommended_gb():.0f}x less")
    print("A published disk minimum is meaningless without saying which profile it is for.")


def emit_chart(path):
    W, H, PAD_L, PAD_R, PAD_T, PAD_B = 880, 700, 74, 24, 42, 60
    panels = [
        ('Disk over 2 years, GB', lambda n, tx, c: Scenario('x', n, tx_per_block=tx,
                                                            contracts=c).disk_recommended_gb()),
        ('Sustained network, Mbps', lambda n, tx, c: Scenario('x', n, tx_per_block=tx,
                                                              contracts=c).network_mbps()),
    ]
    xs = list(range(1, 121, 2))
    series = [('no traffic', 0.0, False, '#63A9F2'),
              ('5 tx/block', 5.0, False, '#3DDC97'),
              ('25 tx/block + contracts', 25.0, True, '#E8C064')]
    out = [f'<svg viewBox="0 0 {W} {H}" xmlns="http://www.w3.org/2000/svg" '
           f'font-family="ui-monospace,Menlo,monospace">',
           f'<rect width="{W}" height="{H}" fill="#081020"/>']
    GAP = 78
    ph = (H - PAD_T - PAD_B - GAP) / 2
    for pi, (title, fn) in enumerate(panels):
        top = PAD_T + pi * (ph + GAP)
        bot = top + ph
        vals = {lab: [fn(n, tx, c) for n in xs] for lab, tx, c, _ in series}
        vmax = max(max(v) for v in vals.values()) * 1.08
        out.append(f'<text x="{PAD_L}" y="{top-12}" fill="#EFF5FB" font-size="13">{title}</text>')
        for g in range(5):
            y = bot - g * ph / 4
            out.append(f'<line x1="{PAD_L}" y1="{y:.0f}" x2="{W-PAD_R}" y2="{y:.0f}" '
                       f'stroke="#1D3050"/>')
            out.append(f'<text x="{PAD_L-8}" y="{y+4:.0f}" fill="#566982" font-size="10" '
                       f'text-anchor="end">{vmax*g/4:,.0f}</text>')
        for lab, tx, c, col in series:
            pts = ' '.join(
                f'{PAD_L+(n-1)/119*(W-PAD_L-PAD_R):.1f},{bot-v/vmax*ph:.1f}'
                for n, v in zip(xs, vals[lab]))
            out.append(f'<polyline fill="none" stroke="{col}" stroke-width="2.2" points="{pts}"/>')
        for mark, lbl in ((6, '6'), (60, '60'), (75, '75')):
            x = PAD_L + (mark - 1) / 119 * (W - PAD_L - PAD_R)
            out.append(f'<line x1="{x:.0f}" y1="{top}" x2="{x:.0f}" y2="{bot}" '
                       f'stroke="#8FA3BC" stroke-dasharray="3 4" opacity=".5"/>')
            out.append(f'<text x="{x:.0f}" y="{bot+14}" fill="#8FA3BC" font-size="10" '
                       f'text-anchor="middle">{lbl}</text>')
        out.append(f'<text x="{(PAD_L+W-PAD_R)/2:.0f}" y="{bot+32}" fill="#566982" '
                   f'font-size="10" text-anchor="middle">validators in the active set</text>')
    lx = PAD_L
    for lab, _, _, col in series:
        out.append(f'<rect x="{lx}" y="{H-20}" width="10" height="10" fill="{col}"/>')
        out.append(f'<text x="{lx+15}" y="{H-11}" fill="#8FA3BC" font-size="10.5">{lab}</text>')
        lx += 22 + len(lab) * 6.6
    out.append('</svg>')
    open(path, 'w').write('\n'.join(out))
    return path


if __name__ == '__main__':
    ap = argparse.ArgumentParser()
    ap.add_argument('--chart', metavar='PATH')
    ap.add_argument('--json', action='store_true')
    a = ap.parse_args()
    if a.json:
        print(json.dumps([{
            'scenario': s.name, 'validators': s.n, 'block_time_s': s.bt,
            'tx_per_block': s.tx, 'contracts': s.contracts,
            'disk_gb_2y': round(s.disk_recommended_gb(), 1),
            'disk_gb_1y_raw': round(s.disk_gb(1), 1),
            'network_mbps': round(s.network_mbps(), 2),
            'traffic_tb_month': round(s.traffic_tb_month(), 3),
            'cpu': {k: round(v, 3) for k, v in s.cpu_cores().items()},
            'ram_gb': round(s.ram_gb()['recommended_gb'], 1),
        } for s in SCENARIOS], indent=2))
    else:
        print_table()
    if not a.json:
        print_cpu_ram()
    if a.chart:
        print(f"chart written to {emit_chart(a.chart)}")
