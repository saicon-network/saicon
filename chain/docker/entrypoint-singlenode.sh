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

  # The devnet's one attestor (project decision D-S8-1). It signs MsgAttestEntity
  # for every later validator; 1,000 SCN covers its fees. It is a genesis account
  # so it can sign later, and collect-gentxs validates the genesis it is given.
  ${SAICOND} keys add attestor --keyring-backend "${KEYRING}" --no-backup
  ${SAICOND} genesis add-genesis-account attestor 100000000000nscn --keyring-backend "${KEYRING}"

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

  # The evidence window is the unbonding period: no shorter, or a double-signer
  # who is not reported quickly escapes the slash while their stake is still
  # bonded; no longer, or old evidence can reach a validator re-created on the
  # key after its unbonding completed (owner 2026-10-29, the v0.10.0 upgrade
  # applies the same rule: app/upgrades.go evidenceWindow).
  #
  # CometBFT expires evidence only when BOTH limits are passed (evidence/pool.go
  # isExpired uses &&), so the window is max(blocks x block time, duration). The
  # stock defaults are 100,000 blocks and 48h: 5.8 days at the measured 5.01s
  # block, against a 21-day unbonding period. Here max_age_duration = 21 days and
  # max_age_num_blocks = floor(21 days / 6s) = 302,400, so the window is exactly
  # 21 days while the average block interval over any 21 days, halts included,
  # is at most 6s (measured: 5.01s, which absorbs ~3.5 days of halt). Slower
  # blocks or a longer halt make the block limit govern and stretch it.
  #
  # This also sets the floor for block retention: baseapp.GetBlockRetentionHeight
  # never prunes inside the evidence window, so a validator must keep ~302,400
  # blocks no matter what min-retain-blocks says.
  UNBONDING_SECONDS=1814400   # 21 days, matches staking unbonding_time (V-5)
  EVIDENCE_BLOCKS=$(( UNBONDING_SECONDS / 6 ))   # 302,400: the window stays 21 days at an average block interval up to 6s
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

  # --- entity registry (S8, ADR-0014) ---------------------------------------
  # The activation gate admits a validator to ACTIVE only as a member of an
  # attested entity, and x/entityregistry's genesis runs before the gentx bonds
  # the validator, so the entity must be in genesis or the validator is held at
  # BONDING and nothing earns. This runs after the validator_cap_floor line
  # above so that value is already set when entity_cap_floor copies it.
  ATTESTOR_ADDR="$(${SAICOND} keys show attestor -a --keyring-backend "${KEYRING}")"
  VALOPER="$(${SAICOND} keys show validator --bech val -a --keyring-backend "${KEYRING}")"
  # The hash of a fixed text: there is no off-chain document for the devnet's own operator.
  ENTITY_HASH="$(printf '%s' 'SAiCoN DevNet operator entity: genesis attestation' | sha256sum | cut -d' ' -f1)"
  jq --arg att "${ATTESTOR_ADDR}" --arg val "${VALOPER}" --arg h "${ENTITY_HASH}" \
     '.app_state.entityregistry.params.attestors = [$att]
      | .app_state.entityregistry.entities = [{"id":"saicon-devnet-operator","display_name":"SAiCoN DevNet operator","attestation_hash":$h,"attestor":$att,"status":"ENTITY_STATUS_ATTESTED","attested_height":"1"}]
      | .app_state.entityregistry.members = [{"validator":$val,"entity_id":"saicon-devnet-operator","attested_height":"1"}]
      | .app_state.stakeengine.params.entity_cap_floor = .app_state.stakeengine.params.validator_cap_floor' \
    "${GENESIS_FILE}" > "${GENESIS_FILE}.tmp" && mv "${GENESIS_FILE}.tmp" "${GENESIS_FILE}"
  echo "[entrypoint] entityregistry attestor=${ATTESTOR_ADDR}, entity saicon-devnet-operator = ${VALOPER}"

  # --- VRF key (S8 W2, spec §17) --------------------------------------------
  # The validator's VRF key is bound in genesis; the initial input is drawn
  # after the key exists, so the key could not have been chosen against it.
  VRF_KEY_FILE="${SAICON_HOME}/config/vrf_key.json"
  [ -f "${VRF_KEY_FILE}" ] || ${SAICOND} vrf keygen --output "${VRF_KEY_FILE}" >/dev/null
  VRF_PK="$(${SAICOND} vrf show --vrf-key-file "${VRF_KEY_FILE}")"
  INITIAL_INPUT="$(od -An -tx1 -N32 /dev/urandom | tr -d ' \n')"
  jq --arg pk "${VRF_PK}" --arg val "${VALOPER}" --arg in "${INITIAL_INPUT}" \
     '.app_state.entityregistry.members[0].vrf_public_key = $pk
      | .app_state.entityregistry.members[0].vrf_key_height = "0"
      | .app_state.entityregistry.vrf_key_owners = [{"vrf_public_key":$pk,"validator":$val}]
      | .app_state.randomness.initial_input = $in' \
    "${GENESIS_FILE}" > "${GENESIS_FILE}.tmp" && mv "${GENESIS_FILE}.tmp" "${GENESIS_FILE}"
  echo "[entrypoint] VRF key ${VRF_PK} bound for ${VALOPER} (${VRF_KEY_FILE})"

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
EVIDENCE_BLOCKS=$(( UNBONDING_SECONDS / 6 )) # 302,400, the genesis evidence window above

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

# ── Cosmovisor ────────────────────────────────────────────────────────────
# The node runs under Cosmovisor, which takes a governance upgrade by itself: at the
# upgrade height saicond halts and writes data/upgrade-info.json; Cosmovisor backs up
# the data directory, downloads the binary the plan's info names for this platform
# (its sha256 checksum is required), switches cosmovisor/current to it and restarts
# the node. A binary placed in cosmovisor/upgrades/<name>/bin/ beforehand is used
# without a download. COSMOVISOR=0 runs saicond directly, as before.
#
# The binaries live in the node home, not in the image: once Cosmovisor has switched
# to an upgrade, the image's /usr/bin/saicond is older than the binary that runs, and
# pulling a newer image does not change which binary runs. The image's binary only
# seeds the layout, once, when the home has none:
#   - a home that never took an upgrade gets it as cosmovisor/genesis;
#   - a home that already took one (data/upgrade-info.json names it) gets it as that
#     upgrade's binary, with current pointing at it. Seeding it as genesis would make
#     Cosmovisor read the old upgrade-info.json as an upgrade still to take and halt
#     the node. So the image a node is first started with under Cosmovisor must carry
#     the version the chain runs now.
export DAEMON_NAME="${DAEMON_NAME:-saicond}"
export DAEMON_HOME="${SAICON_HOME}"   # Cosmovisor watches and backs up the node home itself
export DAEMON_ALLOW_DOWNLOAD_BINARIES="${DAEMON_ALLOW_DOWNLOAD_BINARIES:-true}"
export DAEMON_DOWNLOAD_MUST_HAVE_CHECKSUM="${DAEMON_DOWNLOAD_MUST_HAVE_CHECKSUM:-true}"
export DAEMON_RESTART_AFTER_UPGRADE="${DAEMON_RESTART_AFTER_UPGRADE:-true}"
export UNSAFE_SKIP_BACKUP="${UNSAFE_SKIP_BACKUP:-false}"
export COSMOVISOR_COLOR_LOGS="${COSMOVISOR_COLOR_LOGS:-false}"
CV_ROOT="${DAEMON_HOME}/cosmovisor"
UPGRADE_INFO="${SAICON_HOME}/data/upgrade-info.json"
RETRY="${UPGRADE_RETRY_SECONDS:-60}"

upgrade_name() { # file -> the upgrade name it records, checked
  local name
  name=$(jq -r '.name // empty' "$1")
  if ! [[ "${name}" =~ ^[A-Za-z0-9._-]+$ ]]; then
    echo "[entrypoint] ERROR: $1 names no usable upgrade ('${name}')" >&2
    exit 1
  fi
  echo "${name}"
}

# Built in a side directory and renamed into place, so a seeding cut short (a full disk)
# is redone on the next start instead of leaving a layout without a binary.
seed_cosmovisor() {
  local tmp="${CV_ROOT}.seeding" name
  [ -e "${CV_ROOT}" ] && return 0
  rm -rf "${tmp}"
  if [ -s "${UPGRADE_INFO}" ]; then
    name=$(upgrade_name "${UPGRADE_INFO}")
    mkdir -p "${tmp}/upgrades/${name}/bin"
    cp /usr/bin/saicond "${tmp}/upgrades/${name}/bin/saicond"
    cp "${UPGRADE_INFO}" "${tmp}/upgrades/${name}/upgrade-info.json"
    ln -s "upgrades/${name}" "${tmp}/current"
    echo "[entrypoint] cosmovisor: this home already took upgrade ${name}; the image's saicond ($(saicond version 2>&1)) is seeded as its binary"
  else
    mkdir -p "${tmp}/genesis/bin"
    cp /usr/bin/saicond "${tmp}/genesis/bin/saicond"
    echo "[entrypoint] cosmovisor: the image's saicond ($(saicond version 2>&1)) is seeded as the genesis binary"
  fi
  mv "${tmp}" "${CV_ROOT}"
}

# An upgrade the node halted for that Cosmovisor did not finish: the download failed, the
# disk filled during the backup, the container stopped half-way. Cosmovisor does not pick
# it up again: it detects an upgrade through the running node's RPC, and the old binary
# now exits during its startup replay ("UPGRADE ... NEEDED") before the RPC is up, so the
# container would restart for ever. It may also have left a partial or unchecked file as
# the upgrade's binary, which it would later trust. So the entrypoint finishes the switch:
# it keeps a binary that matches the plan's checksum, downloads it otherwise (retrying
# every UPGRADE_RETRY_SECONDS, default 60, without exiting), makes the backup Cosmovisor
# would have made, points current at it, and starts Cosmovisor on the new binary.
backup_taken() { # name height
  local f
  for f in "${DAEMON_HOME}"/data-backup-*/upgrade-info.json; do
    [ -s "${f}" ] || continue
    [ "$(jq -r '"\(.name) \(.height)"' "${f}" 2>/dev/null)" = "$1 $2" ] && return 0
  done
  return 1
}

finish_pending_upgrade() {
  local name cur="" dir bin url="" plain want="" got arch tmp
  [ -s "${UPGRADE_INFO}" ] || return 0
  name=$(upgrade_name "${UPGRADE_INFO}")
  [ -f "${CV_ROOT}/current/upgrade-info.json" ] && cur=$(jq -r '.name // empty' "${CV_ROOT}/current/upgrade-info.json")
  [ "${name,,}" = "${cur,,}" ] && return 0
  echo "[entrypoint] upgrade ${name} (height $(jq -r .height "${UPGRADE_INFO}")) is not finished; finishing it"
  dir="${CV_ROOT}/upgrades/${name}"; bin="${dir}/bin/saicond"
  case "$(uname -m)" in x86_64) arch=amd64 ;; aarch64) arch=arm64 ;; *) arch="$(uname -m)" ;; esac
  url=$(jq -r '.info // empty' "${UPGRADE_INFO}" | jq -r --arg k "linux/${arch}" '(.binaries[$k] // .binaries.any) // empty' 2>/dev/null || true)
  [[ "${url}" =~ [?\&]checksum=sha256:([0-9a-fA-F]{64}) ]] && want="${BASH_REMATCH[1],,}"
  if [ -e "${bin}" ]; then
    got=$(sha256sum "${bin}" | cut -d' ' -f1)
    if [ ! -s "${bin}" ] || { [ -n "${want}" ] && [ "${got}" != "${want}" ]; }; then
      echo "[entrypoint] ${bin} (sha256 ${got}) is not the plan's binary (sha256 ${want:-none}); removing it" >&2
      rm -f "${bin}"
    fi
  fi
  while [ ! -s "${bin}" ]; do
    if [ -z "${want}" ]; then
      echo "[entrypoint] the plan names no linux/${arch} binary with a sha256 checksum: place ${name}'s binary at ${bin}; checking again in ${RETRY}s" >&2
      sleep "${RETRY}"; continue
    fi
    plain=$(sed -E 's/([?&])checksum=sha256:[0-9a-fA-F]{64}&?/\1/; s/[?&]$//' <<<"${url}")
    tmp="${bin}.download"; mkdir -p "${dir}/bin"
    if wget -q -O "${tmp}" "${plain}" && [ "$(sha256sum "${tmp}" | cut -d' ' -f1)" = "${want}" ]; then
      chmod 755 "${tmp}" && mv "${tmp}" "${bin}"
      echo "[entrypoint] downloaded ${name}'s binary from ${plain} (sha256 ${want})"
    else
      rm -f "${tmp}"
      echo "[entrypoint] could not download ${name}'s binary from ${plain} with sha256 ${want}; retrying in ${RETRY}s" >&2
      sleep "${RETRY}"
    fi
  done
  # Cosmovisor backs up data/ after the halt and before it downloads, so its backup holds
  # this upgrade's upgrade-info.json; one is enough.
  if [ "${UNSAFE_SKIP_BACKUP}" != true ] && ! backup_taken "${name}" "$(jq -r .height "${UPGRADE_INFO}")"; then
    until rm -rf "${DAEMON_HOME}/data-backup-${name}.tmp" && cp -a "${SAICON_HOME}/data" "${DAEMON_HOME}/data-backup-${name}.tmp" \
        && mv "${DAEMON_HOME}/data-backup-${name}.tmp" "${DAEMON_HOME}/data-backup-${name}"; do
      rm -rf "${DAEMON_HOME}/data-backup-${name}.tmp"
      echo "[entrypoint] could not back up data/ before upgrade ${name} (disk space?); retrying in ${RETRY}s" >&2
      sleep "${RETRY}"
    done
    echo "[entrypoint] data/ backed up to data-backup-${name}"
  fi
  cp "${UPGRADE_INFO}" "${dir}/upgrade-info.json"
  ln -sfn "upgrades/${name}" "${CV_ROOT}/current"
  echo "[entrypoint] cosmovisor: current -> upgrades/${name}"
}

# min-gas-prices is empty in app/config.go, so the node halts without this flag.
if [ "${COSMOVISOR:-1}" = 0 ]; then
  echo "[entrypoint] starting node without cosmovisor (minimum-gas-prices=0nscn)"
  exec saicond --home "${SAICON_HOME}" start --minimum-gas-prices 0nscn
fi
seed_cosmovisor
finish_pending_upgrade
echo "[entrypoint] starting node under cosmovisor: $(readlink "${CV_ROOT}/current" 2>/dev/null || echo genesis) (minimum-gas-prices=0nscn)"
exec cosmovisor run --home "${SAICON_HOME}" start --minimum-gas-prices 0nscn
