#!/bin/sh
# alpine-sources.sh <alpine-packages.txt> <out-dir>
#
# Collects the complete corresponding source of every copyleft-licensed Alpine
# package in the node image (busybox, bash, readline, apk-tools, ... — GPL), so
# each published image ships with its sources instead of a pointer to Alpine's
# mirrors, which do not keep old archives forever.
#
# Runs inside the image's own base (alpine:3.20), as root. The input is the
# `apk list --installed` output the image carries in
# /usr/share/licenses/saicon/alpine-packages.txt. For each package's origin it takes
# the build recipe from aports (branch 3.20-stable), checks that the recipe builds
# exactly the version installed in the image, and fetches the upstream sources the
# recipe names. A version mismatch fails the run: sources for a different build
# would not be the corresponding source.
set -eu

LIST="${1:?usage: alpine-sources.sh <alpine-packages.txt> <out-dir>}"
OUT="${2:?usage: alpine-sources.sh <alpine-packages.txt> <out-dir>}"
BRANCH="${APORTS_BRANCH:-3.20-stable}"

apk add --no-cache -q abuild git >/dev/null

# name-version-rN arch {origin} (license) [installed]
pkgs=$(grep -E '\((.*GPL.*|.*MPL.*|.*EPL.*|.*CDDL.*)\)' "$LIST" || true)
[ -n "$pkgs" ] || { echo "no copyleft packages in $LIST"; exit 0; }

origins=$(echo "$pkgs" | sed -n 's/.*{\([^}]*\)}.*/\1/p' | sort -u)
echo "copyleft origins: $(echo $origins)"

work=$(mktemp -d)
git clone -q --depth 1 --filter=blob:none --sparse -b "$BRANCH" \
  https://gitlab.alpinelinux.org/alpine/aports.git "$work/aports"
paths=""
for o in $origins; do paths="$paths main/$o community/$o"; done
# shellcheck disable=SC2086
git -C "$work/aports" sparse-checkout set $paths

mkdir -p "$OUT/aports" "$OUT/distfiles"
for o in $origins; do
  dir=""
  for repo in main community; do
    [ -f "$work/aports/$repo/$o/APKBUILD" ] && dir="$work/aports/$repo/$o" && break
  done
  [ -n "$dir" ] || { echo "FAIL: no recipe for $o in aports $BRANCH" >&2; exit 1; }

  # APKBUILDs reference variables abuild sets (srcdir, ...): source without -u.
  want=$( (cd "$dir" && set +u && . ./APKBUILD && echo "$pkgver-r$pkgrel") )
  have=$(echo "$pkgs" | grep "{$o}" | head -1 | awk '{print $1}' | sed -E 's/^.*-([^-]+-r[0-9]+)$/\1/')
  [ "$want" = "$have" ] || { echo "FAIL: $o: image has $have, aports $BRANCH builds $want" >&2; exit 1; }

  cp -R "$dir" "$OUT/aports/$o"
  (cd "$dir" && SRCDEST="$OUT/distfiles" abuild -F fetch >/dev/null)
  echo "  $o $want"
done

cat > "$OUT/README.txt" <<TXT
Complete corresponding source of the copyleft-licensed Alpine Linux packages in the
SAiCoN node image. aports/<origin>/ holds each package's build recipe (APKBUILD) and
patches from https://gitlab.alpinelinux.org/alpine/aports (branch $BRANCH);
distfiles/ holds the upstream sources the recipes fetch. Packages covered:

$(echo "$pkgs" | sed 's/ \[installed\]//')
TXT
rm -rf "$work"
