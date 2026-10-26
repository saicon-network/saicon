#!/usr/bin/env bash
# Single-node localnet entrypoint for saicond.
# Idempotent: initializes genesis only on first run, then always starts the node.
set -euo pipefail

SAICON_HOME="${SAICON_HOME:-/home/saicon/.saicon}"
CHAIN_ID="${CHAIN_ID:-saicon-localnet-1}"
MONIKER="${MONIKER:-saicon-node}"
KEYRING="${KEYRING:-test}"
# Genesis validator funding / self-delegation. Base denom is nscn (1 SCN = 1e8 nscn);
# defaults below are 10,000,000 SCN funded / 1,000,000 SCN self-staked.
# The genesis MUST fund less than the 21,000,000 SCN maximum supply
# (2.1e15 nscn): x/rewards clamps every mint to the remaining headroom below that
# ceiling (ADR-0011 D4), so a genesis that already exceeds it leaves zero headroom
# and NOTHING is ever paid — every epoch denies with "max supply reached".
# The previous default of 1e19 nscn was 4,762x the cap and did exactly that.
GENESIS_AMOUNT="${GENESIS_AMOUNT:-1000000000000000nscn}"
STAKE_AMOUNT="${STAKE_AMOUNT:-100000000000000nscn}"

SAICOND="saicond --home ${SAICON_HOME}"
CONFIG_TOML="${SAICON_HOME}/config/config.toml"
APP_TOML="${SAICON_HOME}/config/app.toml"

if [ ! -f "${SAICON_HOME}/config/genesis.json" ]; then
  echo "[entrypoint] no genesis found at ${SAICON_HOME} — initializing single-node chain ${CHAIN_ID}"

  # Validate optional demo/test knob values BEFORE any genesis is written.
  # If genesis were created first and a knob check failed later, the restart
  # policy would re-run the entrypoint, hit the "existing genesis found"
  # branch below, and silently boot with DEFAULT params — the exact
  # misconfiguration these checks exist to prevent. Failing before init
  # keeps every restart loud.
  if [ -n "${EPOCH_LENGTH_BLOCKS:-}" ] && ! [[ "${EPOCH_LENGTH_BLOCKS}" =~ ^[0-9]+$ ]]; then
    echo "[entrypoint] ERROR: EPOCH_LENGTH_BLOCKS must be a positive integer, got '${EPOCH_LENGTH_BLOCKS}'" >&2
    exit 1
  fi
  if [ -n "${GOV_VOTING_PERIOD:-}" ] && ! [[ "${GOV_VOTING_PERIOD}" =~ ^[0-9]+s$ ]]; then
    echo "[entrypoint] ERROR: GOV_VOTING_PERIOD must be whole seconds with an 's' suffix (e.g. \"20s\"), got '${GOV_VOTING_PERIOD}'" >&2
    exit 1
  fi
  if [ -n "${STAKEENGINE_CAP_FLOOR:-}" ]; then
    # Canonical decimal only (no leading zeros, at most 15 digits), so the (( ))
    # below can neither read it as octal nor overflow, and the string that is
    # checked is the string that reaches genesis.
    if ! [[ "${STAKEENGINE_CAP_FLOOR}" =~ ^(0|[1-9][0-9]{0,14})$ ]] || ! (( 10#${STAKEENGINE_CAP_FLOOR} <= 210000000000000 )); then
      echo "[entrypoint] ERROR: STAKEENGINE_CAP_FLOOR must be whole nscn between 0 and 210000000000000 (2,100,000 SCN), got '${STAKEENGINE_CAP_FLOOR}'" >&2
      exit 1
    fi
  fi

  ${SAICOND} init "${MONIKER}" --chain-id "${CHAIN_ID}" --default-denom nscn

  # --no-backup: without it the SDK prints the new mnemonic to stderr, which lands
  # in the container log and in anything that collects it. The key itself stays in
  # the keyring on the data volume.
  ${SAICOND} keys add validator --keyring-backend "${KEYRING}" --no-backup

  ${SAICOND} genesis add-genesis-account validator "${GENESIS_AMOUNT}" --keyring-backend "${KEYRING}"

  ${SAICOND} genesis gentx validator "${STAKE_AMOUNT}" --chain-id "${CHAIN_ID}" --keyring-backend "${KEYRING}"

  ${SAICOND} genesis collect-gentxs

  # Register bank DenomMetadata so the display unit SCN (8 decimals) is defined:
  # base nscn (exponent 0, indivisible) -> SCN (exponent 8). 1 SCN = 100_000_000 nscn.
  GENESIS_FILE="${SAICON_HOME}/config/genesis.json"
  jq '.app_state.bank.denom_metadata += [{
    "description": "The native staking and participation token of SAiCoN.",
    "denom_units": [
      { "denom": "nscn", "exponent": 0, "aliases": ["satoshi-scn"] },
      { "denom": "SCN", "exponent": 8, "aliases": [] }
    ],
    "base": "nscn",
    "display": "SCN",
    "name": "SAiCoN",
    "symbol": "SCN",
    "uri": "",
    "uri_hash": ""
  }]' "${GENESIS_FILE}" > "${GENESIS_FILE}.tmp" && mv "${GENESIS_FILE}.tmp" "${GENESIS_FILE}"


  # --- slashing policy (Register v1.0 via ADR-0010) -----------------------
  # Phase 1: ordinary downtime carries NO stake slash. It is punished through
  # the participation layer instead — reward loss, jail, ineligibility — because
  # burning stake for an outage punishes infrastructure luck and pushes the set
  # toward large operators, while the income is already removed.
  # Double-sign stays a stake penalty and doubles to 10%, plus C=0, jail and a
  # permanent tombstone on the SAiCoN side.
  # NOTE: with downtime at 0, x/staking never invokes BeforeValidatorSlashed for
  # it (it returns early on a zero burn), which is what lets x/compliance treat
  # any positive burn as a double-sign.
  jq '.app_state.slashing.params.slash_fraction_downtime = "0.000000000000000000"
      | .app_state.slashing.params.slash_fraction_double_sign = "0.100000000000000000"' \
    "${GENESIS_FILE}" > "${GENESIS_FILE}.tmp" && mv "${GENESIS_FILE}.tmp" "${GENESIS_FILE}"

  # Active set size. The SDK default is 100, which matches no decision we have
  # taken. Register v1.0 adopted 60; the owner raised it to 75 on 2026-09-03,
  # adopting the Stages 8-11 recommendation.
  #
  # Why 75: it lowers worst-world P95 Top-10 voting power from 38.3% to 34.2%
  # and raises the P05 Nakamoto coefficient from ~9 to ~10.2, at a cost of
  # mean technical quality (95.42 -> 94.60). The entity-aware confirmation run
  # gave 32.3% and 11. A set of 90 buys almost nothing further on concentration
  # and keeps degrading quality.
  #
  # STILL GATED, and nothing here is a MainNet constant: Stage 8 gates any
  # production size on a consensus performance benchmark across 50/60/75/90
  # (latency, throughput, block propagation, consensus stability), which has
  # not been run, and the integrated rerun of all Stages 8-11 parameters
  # together is still outstanding. Decision Register v2 is the artifact that
  # will settle it. B1 can still move this number.
  #
  # The selection layer's split is 70/30 of the set: 53 + 22 at 75, 42 + 18 at
  # 60. Stage 8 records the split as a policy choice, not a measured optimum.
  # The layer does not exist yet — voting power is stock stake-weighted top-N —
  # so today this parameter only decides how many validators the set holds.
  #
  # Changing it later needs no chain restart and no migration: max_validators is
  # an x/staking param, editable on a running chain by a governance
  # MsgUpdateParams. Raising it admits more validators at the next EndBlocker;
  # lowering it drops the lowest-ranked ones into unbonding. The devnet runs a
  # single validator, so any value in this range is behaviourally identical here
  # — this exists so the published parameter states an actual decision.
  jq '.app_state.staking.params.max_validators = 75' \
    "${GENESIS_FILE}" > "${GENESIS_FILE}.tmp" && mv "${GENESIS_FILE}.tmp" "${GENESIS_FILE}"

  # --- per-validator cap floor (S6, x/stakeengine) ---------------------------
  # The per-validator cap is 2% of the smoothed total bonded. This devnet runs
  # one validator holding 1,000,000 SCN, whose 2% would be 20,000 SCN — fifty
  # times below what it already holds — so it would accept no delegation at all,
  # and the application team's first delegation would fail. The floor keeps the
  # rule while the set is small: the cap is never below it.
  #
  # 2,000,000 SCN sits above the 1,680,000 SCN global cap, so on this devnet the
  # per-validator rule is off in practice and the global cap and the queue are
  # what bind. The code default is 0 — the strict rule — so a network that forgets
  # this line gets the strict rule, not a disabled one. The production value and
  # a tighter bound go to an owner review before testnet (spec §11).
  #
  # STAKEENGINE_CAP_FLOOR overrides it (nscn); ./scripts/demo-stakeengine.sh sets 0
  # to show the strict rule.
  STAKEENGINE_CAP_FLOOR="${STAKEENGINE_CAP_FLOOR:-200000000000000}"   # 2,000,000 SCN
  jq --arg f "${STAKEENGINE_CAP_FLOOR}" \
     '.app_state.stakeengine.params.validator_cap_floor = $f' \
    "${GENESIS_FILE}" > "${GENESIS_FILE}.tmp" && mv "${GENESIS_FILE}.tmp" "${GENESIS_FILE}"
  echo "[entrypoint] stakeengine validator_cap_floor=${STAKEENGINE_CAP_FLOOR} nscn"

  # Evidence window must cover the unbonding period, or a double-signer who is
  # not reported quickly escapes the slash while their stake is still bonded.
  #
  # CometBFT expires evidence only when BOTH limits are passed (evidence/pool.go
  # isExpired uses &&), so the LONGER of the two governs. The stock defaults are
  # 100,000 blocks and 48h; at our 5s blocks that is 5.8 days against a 21-day
  # unbonding period — a 15-day window in which the offence is unpunishable and
  # the stake has not yet left. Both limits are raised to 21 days.
  #
  # This also sets the floor for block retention: baseapp.GetBlockRetentionHeight
  # never prunes inside the evidence window, so a validator must keep ~362,880
  # blocks no matter what min-retain-blocks says.
  UNBONDING_SECONDS=1814400   # 21 days, matches staking unbonding_time (V-5)
  EVIDENCE_BLOCKS=$(( UNBONDING_SECONDS / 5 ))   # 362,880 at a 5s block
  jq --arg n "${EVIDENCE_BLOCKS}" --arg d "${UNBONDING_SECONDS}000000000" \
     '.consensus.params.evidence.max_age_num_blocks = $n
      | .consensus.params.evidence.max_age_duration = $d' \
    "${GENESIS_FILE}" > "${GENESIS_FILE}.tmp" && mv "${GENESIS_FILE}.tmp" "${GENESIS_FILE}"

  # 21 days is a Register decision (V-5), not a coincidence. Until now nothing set
  # it: the value came from the SDK v0.53.6 default, which happened to agree while
  # four scripts described it in comments as ours. A genesis built against a
  # different SDK moves it without contradicting a line anyone wrote.
  jq --arg u "${UNBONDING_SECONDS}s" \
     '.app_state.staking.params.unbonding_time = $u' \
    "${GENESIS_FILE}" > "${GENESIS_FILE}.tmp" && mv "${GENESIS_FILE}.tmp" "${GENESIS_FILE}"

  # Optional demo/test knobs (default: leave genesis untouched).
  # EPOCH_LENGTH_BLOCKS — participation epoch length (blocks).
  # GOV_VOTING_PERIOD  — gov voting period (e.g. "20s"); expedited period is set
  #                      to half of it (must stay strictly smaller or genesis
  #                      validation fails).
  if [ -n "${EPOCH_LENGTH_BLOCKS:-}" ]; then
    if ! jq --arg len "${EPOCH_LENGTH_BLOCKS}" \
      '.app_state.participation.params.epoch_length_blocks = $len' \
      "${GENESIS_FILE}" > "${GENESIS_FILE}.tmp"; then
      echo "[entrypoint] ERROR: jq failed to patch epoch_length_blocks" >&2
      rm -f "${GENESIS_FILE}.tmp"
      exit 1
    fi
    mv "${GENESIS_FILE}.tmp" "${GENESIS_FILE}"
    echo "[entrypoint] epoch_length_blocks=${EPOCH_LENGTH_BLOCKS}"
  fi
  if [ -n "${GOV_VOTING_PERIOD:-}" ]; then
    if ! jq --arg vp "${GOV_VOTING_PERIOD}" \
      '.app_state.gov.params.voting_period = $vp
       | .app_state.gov.params.expedited_voting_period = (($vp | rtrimstr("s") | tonumber / 2 | floor | tostring) + "s")' \
      "${GENESIS_FILE}" > "${GENESIS_FILE}.tmp"; then
      echo "[entrypoint] ERROR: jq failed to patch gov voting_period" >&2
      rm -f "${GENESIS_FILE}.tmp"
      exit 1
    fi
    mv "${GENESIS_FILE}.tmp" "${GENESIS_FILE}"
    echo "[entrypoint] gov voting_period=${GOV_VOTING_PERIOD}"
  fi

  ${SAICOND} genesis validate-genesis

  # Bind RPC to all interfaces and enable the REST API so the host can reach them.
  sed -i 's#^laddr = "tcp://127.0.0.1:26657"#laddr = "tcp://0.0.0.0:26657"#' "${CONFIG_TOML}"
  sed -i 's#^\(\[api\]\)#\1#' "${APP_TOML}"
  # Enable API server (rest) and bind to all interfaces.
  sed -i '/^\[api\]/,/^\[/{s/^enable = false/enable = true/}' "${APP_TOML}"
  sed -i 's#^address = "tcp://localhost:1317"#address = "tcp://0.0.0.0:1317"#' "${APP_TOML}"
  # Enable CORS so a browser-side explorer (ping.pub) can read RPC + REST directly.
  sed -i 's#^cors_allowed_origins = \[\]#cors_allowed_origins = ["*"]#' "${CONFIG_TOML}"
  sed -i '/^\[api\]/,/^\[/{s/^enabled-unsafe-cors = false/enabled-unsafe-cors = true/}' "${APP_TOML}"


  echo "[entrypoint] genesis initialized"
else
  echo "[entrypoint] existing genesis found at ${SAICON_HOME} — skipping init"
fi

# ── retention policy ────────────────────────────────────────────────────────
# Applied on EVERY start, not only on first init. These are app.toml settings,
# not genesis: a node that already has a chain directory can adopt the policy by
# restarting, with no state reset and no migration. Keeping this inside the
# init-only branch above meant every node created before the policy existed kept
# the stock configuration forever — which is how the running devnet ended up
# with state pruned at 21 days, blocks kept for ever, and snapshots off: the
# disk cost of an archive with the query surface of a pruned node.
#
# Re-applying is safe and idempotent. Each line is a whole-value substitution,
# and tightening retention on a running node only affects what it prunes from
# now on; nothing already stored is deleted retroactively beyond the windows
# below.
UNBONDING_SECONDS=1814400                    # 21 days, matches staking unbonding_time (V-5)
EVIDENCE_BLOCKS=$(( UNBONDING_SECONDS / 5 )) # 362,880 at a 5s block

# ── Data retention profile ────────────────────────────────────────────────
# NODE_PROFILE=validator (default) — keeps what consensus and safety require
#                                    and discards the rest.
# NODE_PROFILE=full                — every block, state pruned to the same window
#                                    as a validator. What an explorer actually
#                                    needs: blocks and transactions far more than
#                                    historical state, and state is the part that
#                                    grows fastest.
# NODE_PROFILE=archive             — keeps everything, for the explorer,
#                                    forensics and anyone auditing history.
#
# Stock defaults are incoherent: state is pruned after 362,880 versions while
# blocks are kept forever and state-sync snapshots are off. That pays for an
# archive and delivers a pruned node — and leaves a new operator no way in
# except replaying the whole chain.
#
# Constraint the SDK does NOT check for you: pruning-keep-recent must exceed
# snapshot-interval x snapshot-keep-recent, or the versions a snapshot needs
# are pruned before it is taken. 100,000 > 1,000 x 2 holds with room.
#
# min-retain-blocks cannot go below the evidence window whatever is set here:
# baseapp.GetBlockRetentionHeight takes the minimum of the evidence, snapshot
# and local constraints, so consensus safety wins over disk. It is set to the
# evidence window explicitly so the intent is legible rather than implied.
NODE_PROFILE="${NODE_PROFILE:-validator}"
case "${NODE_PROFILE}" in
  archive)
    sed -i 's#^pruning = .*#pruning = "nothing"#' "${APP_TOML}"
    sed -i 's#^min-retain-blocks = .*#min-retain-blocks = 0#' "${APP_TOML}"
    ;;
  full)
    # The validator's state window with the archive's block retention. No new
    # number: 100,000 is already justified against the snapshot constraint below,
    # and inheriting it is what lets this profile ship before the disk measurement
    # rather than after it.
    sed -i 's#^pruning = .*#pruning = "custom"#' "${APP_TOML}"
    sed -i 's#^pruning-keep-recent = .*#pruning-keep-recent = "100000"#' "${APP_TOML}"
    sed -i 's#^pruning-interval = .*#pruning-interval = "10"#' "${APP_TOML}"
    sed -i 's#^min-retain-blocks = .*#min-retain-blocks = 0#' "${APP_TOML}"
    ;;
  validator)
    sed -i 's#^pruning = .*#pruning = "custom"#' "${APP_TOML}"
    sed -i 's#^pruning-keep-recent = .*#pruning-keep-recent = "100000"#' "${APP_TOML}"
    sed -i 's#^pruning-interval = .*#pruning-interval = "10"#' "${APP_TOML}"
    sed -i "s#^min-retain-blocks = .*#min-retain-blocks = ${EVIDENCE_BLOCKS}#" "${APP_TOML}"
    ;;
  *)
    # Fail rather than fall through. The previous two-value form made every
    # unrecognised value a validator, so NODE_PROFILE=ful ran a different node than
    # the deployment asked for and said nothing.
    echo "[entrypoint] unknown NODE_PROFILE '"'"'${NODE_PROFILE}'"'"' - expected validator, full or archive" >&2
    exit 1
    ;;
esac
# Snapshots on for both profiles: without them a joining node has no route in
# but a full replay from genesis, which is what makes an invite meaningless.
sed -i 's#^snapshot-interval = .*#snapshot-interval = 1000#' "${APP_TOML}"
sed -i 's#^snapshot-keep-recent = .*#snapshot-keep-recent = 2#' "${APP_TOML}"
echo "[entrypoint] retention profile: ${NODE_PROFILE}"

# min-gas-prices is empty in app/config.go, so the node halts without this flag.
echo "[entrypoint] starting node (minimum-gas-prices=0nscn)"
exec saicond --home "${SAICON_HOME}" start --minimum-gas-prices 0nscn
