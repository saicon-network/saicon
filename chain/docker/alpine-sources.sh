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
# the build recipe from aports (branch 3.20-stable) at the commit that builds exactly
# the version installed in the image, and fetches the upstream sources the recipe
# names. The branch head is tried first; when it has moved past the installed version
# (a cached image layer, or a package updated after the build), the branch history is
# searched. No recipe for the installed version fails the run: sources for a different
# build would not be the corresponding source.
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

# Alpine publishes aports from two hosts that carry the same commits; the first one
# that answers is used and named in the README. Either refusing (gitlab.alpinelinux.org
# answers some CI networks with HTTP 418) is not a reason to ship without sources.
URLS="${APORTS_URLS:-https://gitlab.alpinelinux.org/alpine/aports.git https://git.alpinelinux.org/aports}"
work=$(mktemp -d)
src=""
for url in $URLS; do
  rm -rf "$work/aports"
  if git clone -q --depth 1 --filter=blob:none --sparse -b "$BRANCH" "$url" "$work/aports"; then
    src="$url"; break
  fi
  echo "aports: $url refused, trying the next host" >&2
done
[ -n "$src" ] || { echo "FAIL: no aports host answered ($URLS)" >&2; exit 1; }
echo "aports from $src at $(git -C "$work/aports" rev-parse HEAD)"
paths=""
for o in $origins; do paths="$paths main/$o community/$o"; done
# shellcheck disable=SC2086
git -C "$work/aports" sparse-checkout set $paths

# The version an APKBUILD read from stdin builds. APKBUILDs reference variables abuild
# sets (srcdir, ...): sourced without -u, in an empty directory.
recipe_version() {
  t=$(mktemp -d)
  cat > "$t/APKBUILD"
  (cd "$t" && set +u && . ./APKBUILD >/dev/null 2>&1; echo "$pkgver-r$pkgrel")
  rm -r "$t"
}
deepened=""
deepen() {
  [ -n "$deepened" ] && return 0
  # 3.20-stable branched in May 2024: its history from April holds every 3.20 build.
  echo "  searching the $BRANCH history for older recipes" >&2
  git -C "$work/aports" fetch -q --shallow-since="${APORTS_SINCE:-2024-04-01}" origin "$BRANCH"
  deepened=1
}

mkdir -p "$OUT/aports" "$OUT/distfiles"
commits=""
for o in $origins; do
  have=$(echo "$pkgs" | grep "{$o}" | head -1 | awk '{print $1}' | sed -E 's/^.*-([^-]+-r[0-9]+)$/\1/')
  path=""
  for repo in main community; do
    [ -f "$work/aports/$repo/$o/APKBUILD" ] && path="$repo/$o" && break
  done
  [ -n "$path" ] || { echo "FAIL: no recipe for $o in aports $BRANCH" >&2; exit 1; }

  at=$(git -C "$work/aports" rev-parse HEAD)
  if [ "$(git -C "$work/aports" show "$at:$path/APKBUILD" | recipe_version)" != "$have" ]; then
    deepen
    at=""
    for c in $(git -C "$work/aports" log --format=%H FETCH_HEAD -- "$path/APKBUILD"); do
      if [ "$(git -C "$work/aports" show "$c:$path/APKBUILD" | recipe_version)" = "$have" ]; then
        at=$c; break
      fi
    done
    [ -n "$at" ] || { echo "FAIL: $o: no recipe in aports $BRANCH builds $have" >&2; exit 1; }
  fi

  # The recipe directory as of that commit (APKBUILD and its patches). The fetch runs
  # in a copy: abuild leaves a src/ of symlinks next to the recipe.
  mkdir -p "$work/fetch"
  git -C "$work/aports" archive --format=tar --prefix="$o/" "$at:$path" > "$work/$o.tar"
  tar -x -C "$OUT/aports" -f "$work/$o.tar"
  tar -x -C "$work/fetch" -f "$work/$o.tar"
  (cd "$work/fetch/$o" && SRCDEST="$OUT/distfiles" abuild -F fetch >/dev/null)
  commits="$commits
  $o $have  aports $at"
  echo "  $o $have (aports $at)"
done

cat > "$OUT/README.txt" <<TXT
Complete corresponding source of the copyleft-licensed Alpine Linux packages in the
SAiCoN node image. aports/<origin>/ holds each package's build recipe (APKBUILD) and
patches from $src (branch $BRANCH), each at the commit that builds the installed
version; distfiles/ holds the upstream sources the recipes fetch.

Recipes:$commits

Packages covered:

$(echo "$pkgs" | sed 's/ \[installed\]//')
TXT
rm -rf "$work"
