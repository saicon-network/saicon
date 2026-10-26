#!/usr/bin/env bash
# Shared narration helpers for the operator-facing scripts in this directory.
#
# Everything writes to stderr on purpose. These scripts have two audiences at
# once: a person watching the run, and a caller capturing the result with
# $(...) or a pipe. Keeping commentary on stderr means a caller can capture a
# script's real output — a CSV path, a summary, a proposal id — without the
# narration arriving as data.
#
# demo-pop-cycle.sh printed these to stdout before the extraction and redirected
# per call site; nothing captured them, so moving the redirect into the helpers
# changes what a `2>/dev/null` run shows and nothing else.

step()  { printf '\n\033[1;36m== %s ==\033[0m\n' "$*" >&2; }
runq()  { printf '\033[0;33m$ %s\033[0m\n' "$*" >&2; }
ok()    { printf '\033[0;32m\xe2\x9c\x93 %s\033[0m\n' "$*" >&2; }
fail()  { printf '\033[0;31m\xe2\x9c\x97 %s\033[0m\n' "$*" >&2; exit 1; }
