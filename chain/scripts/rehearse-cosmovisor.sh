#!/usr/bin/env bash
# Rehearse a governance upgrade taken by Cosmovisor, in Docker, with no hand on the node:
# a node of the image (entrypoint-singlenode.sh, Cosmovisor) runs an OLD binary as its
# genesis binary; an upgrade proposal names the new binary in its plan info ("binaries",
# with a sha256 checksum, served over HTTP from this machine); at the upgrade height
# Cosmovisor backs up the data directory, downloads the binary, switches to it and
# restarts the node, which goes on past the height. Then:
#   - a restart keeps the upgraded binary (not the image's);
#   - the VRF submitter loop, sharing the node's network as on the devnet, runs the
#     binary Cosmovisor switched to;
#   - a home that already took the upgrade without Cosmovisor (the devnet's case) is
#     moved to it: the entrypoint seeds the image's binary as that upgrade's, and the
#     node goes on without taking the upgrade a second time;
#   - a fresh home is initialized and seeded with the image's binary as genesis.
#
#   bash ./scripts/rehearse-cosmovisor.sh                      # builds the image from this tree
#   IMAGE=saicon-node:x bash ./scripts/rehearse-cosmovisor.sh  # an image already built from it
#   OLD_REF=v0.9.1 UPGRADE=v0.10.0 (defaults)                # the upgrade rehearsed
#   KEEP=1                                                    # keep containers, volumes, temp dir
#
# Needs docker, go, jq, python3 and git. Nothing is published on the host but a file
# server on 127.0.0.1:${HTTP_PORT:-38080}. The exit status is non-zero on any failure.
set -euo pipefail
source "$(dirname "$0")/lib-narrate.sh"

OLD_REF="${OLD_REF:-v0.9.1}"
UPGRADE="${UPGRADE:-v0.10.0}"
HTTP_PORT="${HTTP_PORT:-38080}"
CHAIN_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
REPO_DIR="$(git -C "$CHAIN_DIR" rev-parse --show-toplevel)"
CHAIN_ID=saicon-rehearse-1
H=/home/saicon/.saicon
C=rc-node; VOL=rc-home; FRESH=rc-fresh; SUBC=rc-sub
NEW_VERSION=rehearsal-upgrade     # the version string of the binary the plan names
SCN=100000000

TMP="$(mktemp -d "${TMPDIR:-/tmp}/saicon-rehearse-cosmovisor.XXXXXX")"
WORKTREE="$TMP/old-src"
HTTP_PID=""

cleanup() {
  [ -n "$HTTP_PID" ] && kill "$HTTP_PID" 2>/dev/null || true
  [ -d "$WORKTREE" ] && git -C "$REPO_DIR" worktree remove --force "$WORKTREE" >/dev/null 2>&1 || true
  if [ "${KEEP:-0}" = 1 ]; then echo "kept: $TMP, containers $C $FRESH, volumes $VOL $FRESH" >&2; return; fi
  docker rm -f "$SUBC" "$C" "$FRESH" >/dev/null 2>&1 || true
  docker volume rm "$VOL" "$FRESH" >/dev/null 2>&1 || true
  chmod -R u+w "$TMP" 2>/dev/null || true
  rm -r "$TMP" 2>/dev/null || true
}
trap cleanup EXIT
docker rm -f "$SUBC" "$C" "$FRESH" >/dev/null 2>&1 || true
docker volume rm "$VOL" "$FRESH" >/dev/null 2>&1 || true

# --- helpers ------------------------------------------------------------------
KR=(--keyring-backend test --home "$H")
TXF=(--chain-id "$CHAIN_ID" --fees 0nscn --gas 600000 -y -o json)
X() { docker exec "$C" "$H/cosmovisor/current/bin/saicond" "$@"; }              # the binary that runs now
in_vol() { docker run --rm --network none -v "$VOL:$H" --entrypoint sh "$IMAGE" -c "$1"; }
OLD() { docker run --rm --network none -v "$VOL:$H" --entrypoint "$H/cosmovisor/genesis/bin/saicond" "$IMAGE" "$@"; }
height() { docker exec "$1" wget -qO- http://localhost:26657/status 2>/dev/null | jq -r '.result.sync_info.latest_block_height' 2>/dev/null || echo 0; }
app_version() { docker exec "$1" wget -qO- http://localhost:26657/abci_info 2>/dev/null | jq -r '.result.response.version'; }
wait_height() { # container target [max seconds]
  local h=0
  for _ in $(seq 1 "${3:-180}"); do
    h=$(height "$1"); [ "${h:-0}" -ge "$2" ] 2>/dev/null && return 0
    docker inspect -f '{{.State.Running}}' "$1" 2>/dev/null | grep -q true || fail "$1 stopped: $(docker logs --tail 20 "$1" 2>&1)"
    sleep 1
  done
  fail "$1 did not reach height $2 (last seen: ${h:-none}): $(docker logs --tail 20 "$1" 2>&1)"
}
tx() { # args... ; waits for the tx to be in a block and checks its code
  local out hash r
  out=$(X tx "$@" "${KR[@]}" "${TXF[@]}" 2>&1) || fail "tx broadcast failed: $out"
  [ "$(jq -r '.code // 1' <<<"$out")" = 0 ] || fail "tx refused at CheckTx: $(jq -r '.raw_log // .' <<<"$out")"
  hash=$(jq -r .txhash <<<"$out")
  r=""
  for _ in $(seq 1 30); do
    r=$(X q tx "$hash" --home "$H" -o json 2>/dev/null || true); [ -n "$r" ] && break; sleep 1
  done
  [ -n "$r" ] || r='{}'
  [ "$(jq -r '.code // 99' <<<"$r")" = 0 ] || fail "tx $hash failed in the block: $(jq -r '.raw_log // "not in a block"' <<<"$r")"
}
start_node() { # container volume
  docker run -d --name "$1" -v "$2:$H" -e CHAIN_ID="$CHAIN_ID" -e UPGRADE_RETRY_SECONDS=3 --restart unless-stopped \
    --add-host host.docker.internal:host-gateway --entrypoint /usr/local/bin/entrypoint-singlenode.sh "$IMAGE" >/dev/null
}
logs_have() { docker logs "$1" 2>&1 | grep -c -- "$2" >/dev/null; }   # grep -q would cut the pipe under pipefail
eq() { [ "$1" = "$2" ] && ok "$3: $1" || fail "$3: got '$1', want '$2'"; }

# --- 1. the image and the two binaries --------------------------------------------
step "The image, the old binary ($OLD_REF) and the binary the upgrade plan names (this tree)"
if [ -z "${IMAGE:-}" ]; then
  IMAGE=saicon-node:cosmovisor-rehearsal
  docker build -q -t "$IMAGE" --build-arg VERSION=rehearsal-image "$CHAIN_DIR" >/dev/null || fail "docker build failed"
fi
docker run --rm --entrypoint sh "$IMAGE" -c 'command -v cosmovisor' >/dev/null || fail "the image $IMAGE has no cosmovisor"
IMAGE_VERSION=$(docker run --rm "$IMAGE" version 2>&1)
case "$(docker run --rm --entrypoint uname "$IMAGE" -m)" in
  x86_64) GOARCH=amd64 ;; aarch64) GOARCH=arm64 ;; *) fail "unknown image architecture" ;;
esac
git -C "$REPO_DIR" rev-parse --verify --quiet "${OLD_REF}^{commit}" >/dev/null || fail "OLD_REF '$OLD_REF' is not a commit/tag"
git -C "$REPO_DIR" worktree add --detach "$WORKTREE" "$OLD_REF" >/dev/null 2>&1 || fail "git worktree add $OLD_REF failed"
mkdir -p "$TMP/old" "$TMP/srv"
LDF="-X github.com/cosmos/cosmos-sdk/version.Name=saicon -X github.com/cosmos/cosmos-sdk/version.AppName=saicond"
(cd "$WORKTREE/chain" && CGO_ENABLED=0 GOOS=linux GOARCH=$GOARCH go build \
  -ldflags "$LDF -X github.com/cosmos/cosmos-sdk/version.Version=$OLD_REF" -o "$TMP/old/saicond" ./cmd/saicond) || fail "building $OLD_REF failed"
(cd "$CHAIN_DIR" && CGO_ENABLED=0 GOOS=linux GOARCH=$GOARCH go build \
  -ldflags "$LDF -X github.com/cosmos/cosmos-sdk/version.Version=$NEW_VERSION" -o "$TMP/srv/saicond" ./cmd/saicond) || fail "building this tree failed"
SUM=$(shasum -a 256 "$TMP/srv/saicond" | cut -d' ' -f1)
ok "image $IMAGE (saicond $IMAGE_VERSION, linux/$GOARCH); old $OLD_REF; upgrade binary $NEW_VERSION sha256 $SUM"

step "Serve the upgrade binary over HTTP, as a release page would"
(cd "$TMP/srv" && exec python3 -m http.server "$HTTP_PORT" --bind 127.0.0.1 >"$TMP/http.log" 2>&1) &
HTTP_PID=$!
URL="http://host.docker.internal:${HTTP_PORT}/saicond?checksum=sha256:${SUM}"
for _ in $(seq 1 20); do
  docker run --rm --add-host host.docker.internal:host-gateway --entrypoint wget "$IMAGE" -q --spider \
    "http://host.docker.internal:${HTTP_PORT}/saicond" 2>/dev/null && break; sleep 1
done
docker run --rm --add-host host.docker.internal:host-gateway --entrypoint wget "$IMAGE" -q --spider \
  "http://host.docker.internal:${HTTP_PORT}/saicond" || fail "a container cannot reach the file server on the host"
ok "a container reaches $URL"

# --- 2. a chain on the old binary, under cosmovisor -------------------------------
step "A single-node chain whose genesis binary is $OLD_REF, started by the image's entrypoint"
docker run --rm -v "$VOL:$H" -v "$TMP/old:/in:ro" --entrypoint sh "$IMAGE" -c \
  "mkdir -p $H/cosmovisor/genesis/bin && cp /in/saicond $H/cosmovisor/genesis/bin/saicond"
OLD init rehearse --chain-id "$CHAIN_ID" --default-denom nscn --home "$H" >/dev/null 2>&1
OLD keys add validator "${KR[@]}" --no-backup >/dev/null 2>&1
OLD keys add attestor "${KR[@]}" --no-backup >/dev/null 2>&1
OLD genesis add-genesis-account validator 1000000000000000nscn "${KR[@]}"
OLD genesis gentx validator 100000000000000nscn --chain-id "$CHAIN_ID" "${KR[@]}" >/dev/null 2>&1
OLD genesis collect-gentxs --home "$H" >/dev/null 2>&1
in_vol "G=$H/config/genesis.json; jq '.app_state.gov.params.voting_period = \"20s\" | .app_state.gov.params.expedited_voting_period = \"10s\"
  | .app_state.stakeengine.params.validator_cap_floor = \"200000000000000\"' \$G > \$G.tmp && mv \$G.tmp \$G && sed -i 's#^timeout_commit = .*#timeout_commit = \"2s\"#' $H/config/config.toml"
OLD genesis validate-genesis --home "$H" >/dev/null 2>&1 || fail "genesis does not validate on the old binary"
start_node "$C" "$VOL"
wait_height "$C" 3
eq "$(app_version "$C")" "$OLD_REF" "the running node's application version"
logs_have "$C" 'starting node under cosmovisor' || fail "the entrypoint did not start cosmovisor"
ok "the node runs under cosmovisor, height $(height "$C")"
ATT=$(X keys show attestor -a "${KR[@]}")

# --- 3. the proposal: the plan info names the binary --------------------------------
step "Upgrade proposal $UPGRADE whose plan info carries the attestor and the binary (URL + sha256)"
tx bank send validator "$ATT" "$((1000 * SCN))nscn" --from validator
DEPOSIT=$(in_vol "jq -r '.app_state.gov.params.min_deposit[0] | .amount + .denom' $H/config/genesis.json")
UPGRADE_H=$(( $(height "$C") + 25 ))
INFO=$(jq -cn --arg a "$ATT" --arg k "linux/$GOARCH" --arg u "$URL" '{attestors: [$a], binaries: {($k): $u}}')
# No --no-validate: the CLI checks the plan info, downloads the binary and verifies the checksum
# before it sends, which is the check an operator gets when submitting.
tx upgrade software-upgrade "$UPGRADE" --upgrade-height "$UPGRADE_H" --upgrade-info "$INFO" \
  --title "$UPGRADE" --summary "cosmovisor rehearsal" --deposit "$DEPOSIT" --from validator
PROP=$(X q gov proposals --home "$H" -o json | jq -r '.proposals[-1].id')
tx gov vote "$PROP" yes --from validator
STATUS=""
for _ in $(seq 1 60); do
  STATUS=$(X q gov proposal "$PROP" --home "$H" -o json | jq -r .proposal.status)
  [ "$STATUS" = PROPOSAL_STATUS_PASSED ] && break; sleep 2
done
eq "$STATUS" PROPOSAL_STATUS_PASSED "proposal $PROP (upgrade at height $UPGRADE_H)"

# --- 4. the upgrade, with no hand on the node -------------------------------------
# The release page answers 404 at the height (a download that fails): Cosmovisor exits, the
# container restarts, and the entrypoint keeps retrying until the binary is there.
mv "$TMP/srv/saicond" "$TMP/srv/saicond.held"
step "Height $UPGRADE_H with the binary missing from the release page: the node must wait, not loop"
for _ in $(seq 1 240); do logs_have "$C" "could not download $UPGRADE's binary" && break; sleep 1; done
logs_have "$C" "could not download $UPGRADE's binary" || fail "the entrypoint is not retrying the download: $(docker logs --tail 20 "$C" 2>&1)"
logs_have "$C" "upgrade $UPGRADE (height $UPGRADE_H) is not finished" || fail "the entrypoint did not see the unfinished upgrade"
ok "cosmovisor's download failed; on restart the entrypoint found the unfinished upgrade and retries"
logs_have "$C" "is not the plan's binary" && ok "the partial file cosmovisor left was removed, not trusted"
sleep 6
eq "$(docker inspect -f '{{.State.Running}}' "$C")" true "the container stays up while it waits (no restart loop)"
mv "$TMP/srv/saicond.held" "$TMP/srv/saicond"
step "The binary appears on the release page: the node takes the upgrade by itself"
wait_height "$C" $((UPGRADE_H + 3)) 300
logs_have "$C" "downloaded $UPGRADE's binary" || fail "the entrypoint did not report the download"
eq "$(app_version "$C")" "$NEW_VERSION" "the running node's application version after the upgrade height"
eq "$(docker exec "$C" readlink "$H/cosmovisor/current")" "upgrades/$UPGRADE" "cosmovisor/current"
eq "$(in_vol "ls -d $H/data-backup-* 2>/dev/null | wc -l" | tr -d ' ')" 1 "data/ backups made for the upgrade (one, not two)"
eq "$(in_vol "jq -r .name $H/data-backup-*/upgrade-info.json")" "$UPGRADE" "the backup is of the halted state (it holds the upgrade's upgrade-info.json)"
grep -q 'GET /saicond' "$TMP/http.log" && ok "the binary was downloaded from the plan's URL" || fail "the file server saw no download"
eq "$(X q entityregistry params --home "$H" -o json | jq -r --arg a "$ATT" '.params.attestors | index($a) != null')" true \
  "the v0.10.0 handler read the attestor from the same plan info"
eq "$(X q entityregistry tickets --home "$H" -o json | jq -r '.tickets | length')" 1 "tickets after the upgrade"

step "A restart keeps the binary cosmovisor switched to"
T0=$(date -u +%Y-%m-%dT%H:%M:%SZ)
docker restart "$C" >/dev/null
H0=$(height "$C"); wait_height "$C" $((H0 + 3))
eq "$(app_version "$C")" "$NEW_VERSION" "the application version after a restart"
docker logs --since "$T0" "$C" 2>&1 | grep -c 'is not finished' >/dev/null && fail "after a restart the entrypoint took the finished upgrade for an unfinished one"
ok "the restart did not reopen the finished upgrade"

step "The VRF submitter, sharing the node's network as on the devnet, runs the current binary"
SUB=$(docker run --rm --network "container:$C" -v "$VOL:$H" -e CHAIN_ID="$CHAIN_ID" -e INTERVAL=1 \
  --entrypoint sh "$IMAGE" -c 'timeout 4 /usr/local/bin/vrf-submit-loop.sh 2>&1 || true')
grep -q "using $H/cosmovisor/current/bin/saicond" <<<"$SUB" && ok "the loop uses $H/cosmovisor/current/bin/saicond" \
  || fail "the loop does not use the current binary: $SUB"
grep -qiE 'connection refused|no such host' <<<"$SUB" && fail "the loop cannot reach the node: $SUB"
ok "the loop reaches the node's RPC over the shared network"

# --- 5. the devnet's case: a home that took the upgrade without cosmovisor --------
step "A home that already took $UPGRADE without cosmovisor (the devnet) is moved to it"
docker stop "$C" >/dev/null && docker rm "$C" >/dev/null
in_vol "rm -r $H/cosmovisor $H/data-backup-*"
in_vol "jq -r .name $H/data/upgrade-info.json" | grep -qx "$UPGRADE" || fail "the home has no data/upgrade-info.json naming $UPGRADE"
ok "the home holds data/upgrade-info.json for $UPGRADE and no cosmovisor directory"
start_node "$C" "$VOL"
# The submitter starts with the node, as compose starts it on the devnet: before the
# entrypoint has seeded cosmovisor/.
docker run -d --name "$SUBC" --network "container:$C" -v "$VOL:$H" -e CHAIN_ID="$CHAIN_ID" -e INTERVAL=1 \
  --entrypoint /usr/local/bin/vrf-submit-loop.sh "$IMAGE" >/dev/null
sleep 5; H0=$(height "$C"); wait_height "$C" $((H0 + 5))
LASTBIN=$(docker logs "$SUBC" 2>&1 | grep 'vrf-submit-loop\] using' | tail -1)
eq "$LASTBIN" "[vrf-submit-loop] using $H/cosmovisor/current/bin/saicond" "the submitter started with the node now uses"
docker rm -f "$SUBC" >/dev/null
logs_have "$C" "already took upgrade $UPGRADE" || fail "the entrypoint did not seed the upgrade's binary"
eq "$(docker exec "$C" readlink "$H/cosmovisor/current")" "upgrades/$UPGRADE" "cosmovisor/current after the move"
eq "$(app_version "$C")" "$IMAGE_VERSION" "the running binary is the image's, seeded as $UPGRADE's"
in_vol "ls -d $H/data-backup-*" >/dev/null 2>&1 && fail "cosmovisor took the upgrade a second time (it made a backup)"
ok "no second upgrade: the node went on from height $H0 to $(height "$C")"

# --- 6. a fresh home --------------------------------------------------------------
step "A fresh home: the entrypoint initializes the chain and seeds the image's binary as genesis"
start_node "$FRESH" "$FRESH"
wait_height "$FRESH" 3
logs_have "$FRESH" 'seeded as the genesis binary' || fail "the fresh home was not seeded as genesis"
eq "$(app_version "$FRESH")" "$IMAGE_VERSION" "the fresh node's application version"
ok "the fresh node produces blocks under cosmovisor (height $(height "$FRESH"))"

step "Rehearsal complete: $OLD_REF -> $UPGRADE taken by cosmovisor, and the move of an upgraded home"
