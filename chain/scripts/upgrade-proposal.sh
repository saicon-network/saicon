#!/usr/bin/env bash
# Write a software-upgrade proposal whose plan info names the release binaries, so that
# nodes running under Cosmovisor take the upgrade by themselves at the upgrade height.
#
#   bash ./scripts/upgrade-proposal.sh <name> <height> <release-tag> [options] > upgrade.json
#
#   <name>         the upgrade name the new binary registers a handler for (e.g. v0.11.0)
#   <height>       the upgrade height
#   <release-tag>  the GitHub release whose bare linux binaries the plan points at
#
# Options:
#   --info JSON        more plan info merged in (e.g. '{"attestors":["saicon1..."]}');
#                      "binaries" is always the one this script writes
#   --title TEXT       default: <name>
#   --summary TEXT     default: "Upgrade to <name>"
#   --deposit COINS    default: the chain's min_deposit (expedited: expedited_min_deposit)
#   --expedited        an expedited proposal
#   --api URL          REST endpoint (default https://api.saicon.world), for the gov
#                      module address and the deposit
#   --repo OWNER/NAME  default saicon-network/saicon
#
# Every binary is downloaded and checked against the release's SHA256SUMS before the
# proposal is written; the URLs carry the checksum, which Cosmovisor requires
# (DAEMON_DOWNLOAD_MUST_HAVE_CHECKSUM=true in the node image). Submit the file with
#   saicond tx gov submit-proposal upgrade.json --from <key> --gas 600000 ...
# Needs curl, jq and sha256sum or shasum.
set -euo pipefail

usage() { sed -n '2,25p' "$0" >&2; exit 2; }
[ $# -ge 3 ] || usage
NAME=$1 HEIGHT=$2 TAG=$3; shift 3
EXTRA='{}' TITLE="" SUMMARY="" DEPOSIT="" EXPEDITED=false
API=https://api.saicon.world REPO=saicon-network/saicon
while [ $# -gt 0 ]; do
  case "$1" in
    --info) EXTRA=$2; shift 2 ;;
    --title) TITLE=$2; shift 2 ;;
    --summary) SUMMARY=$2; shift 2 ;;
    --deposit) DEPOSIT=$2; shift 2 ;;
    --expedited) EXPEDITED=true; shift ;;
    --api) API=$2; shift 2 ;;
    --repo) REPO=$2; shift 2 ;;
    *) usage ;;
  esac
done
[[ "$NAME" =~ ^[A-Za-z0-9._-]+$ ]] || { echo "bad upgrade name '$NAME'" >&2; exit 1; }
[[ "$HEIGHT" =~ ^[1-9][0-9]*$ ]] || { echo "bad height '$HEIGHT'" >&2; exit 1; }
jq -e 'type == "object"' <<<"$EXTRA" >/dev/null || { echo "--info must be a JSON object" >&2; exit 1; }
sha() { if command -v sha256sum >/dev/null; then sha256sum "$1"; else shasum -a 256 "$1"; fi | cut -d' ' -f1; }

BASE="https://github.com/${REPO}/releases/download/${TAG}"
WORK=$(mktemp -d); trap 'rm -r "$WORK"' EXIT
curl -fsSL "$BASE/SHA256SUMS" -o "$WORK/SHA256SUMS" || { echo "no SHA256SUMS in release $TAG" >&2; exit 1; }

BIN='{}'
for arch in amd64 arm64; do
  file="saicond_${TAG}_linux_${arch}"
  want=$(awk -v f="$file" '$2 == f {print $1}' "$WORK/SHA256SUMS")
  [ -n "$want" ] || { echo "release $TAG has no $file in SHA256SUMS" >&2; exit 1; }
  curl -fsSL "$BASE/$file" -o "$WORK/$file" || { echo "cannot download $BASE/$file" >&2; exit 1; }
  got=$(sha "$WORK/$file")
  [ "$got" = "$want" ] || { echo "$file: sha256 $got, SHA256SUMS says $want" >&2; exit 1; }
  BIN=$(jq -c --arg k "linux/$arch" --arg u "$BASE/$file?checksum=sha256:$want" '. + {($k): $u}' <<<"$BIN")
  echo "checked $file  sha256 $want" >&2
done
INFO=$(jq -c --argjson b "$BIN" '. + {binaries: $b}' <<<"$EXTRA")

GOV=$(curl -fsS "$API/cosmos/auth/v1beta1/module_accounts/gov" | jq -r .account.base_account.address)
[[ "$GOV" == saicon1* ]] || { echo "could not read the gov module address from $API" >&2; exit 1; }
if [ -z "$DEPOSIT" ]; then
  key=min_deposit; [ "$EXPEDITED" = true ] && key=expedited_min_deposit
  DEPOSIT=$(curl -fsS "$API/cosmos/gov/v1/params/deposit" | jq -r --arg k "$key" '.params[$k] | map(.amount + .denom) | join(",")')
fi

jq -n --arg gov "$GOV" --arg name "$NAME" --arg h "$HEIGHT" --arg info "$INFO" --arg dep "$DEPOSIT" \
  --arg title "${TITLE:-$NAME}" --arg summary "${SUMMARY:-Upgrade to $NAME}" --argjson exp "$EXPEDITED" \
  '{messages: [{"@type": "/cosmos.upgrade.v1beta1.MsgSoftwareUpgrade", authority: $gov,
               plan: {name: $name, height: $h, info: $info}}],
    metadata: "", deposit: $dep, title: $title, summary: $summary, expedited: $exp}'
