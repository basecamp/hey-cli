#!/usr/bin/env bash
# Extracts the corrected vendorHash from a nix build log.
#
# Usage: scripts/extract-nix-vendor-hash.sh [LOGFILE]   (reads stdin when omitted)
#
# The single classifier for "did this build fail because of the vendorHash?".
# Both production callers use it — update-nix-flake.sh, which writes the value
# into nix/package.nix, and nix-build-check.sh, which reports it in CI. They
# each carried their own copy of this parse once; two classifiers that can
# drift is one classifier too many, because the one that drifts is the one
# that writes to a tracked file.
#
# Two independent conditions must hold before a hash comes back. Nix must have
# actually reported a fixed-output hash mismatch, and the captured value must
# look like an SRI hash. Matching a bare `got:` is far too loose: any failing
# build whose log happens to contain one — a Go test assertion printing
# `got: 42`, say — would otherwise yield "42" as a vendorHash and misreport an
# unrelated failure as a hash problem.
#
# Exit codes:
#   0 — a fixed-output hash mismatch was reported; the SRI hash is on stdout
#   1 — the log reports no vendorHash mismatch; nothing on stdout

set -euo pipefail

LOG=$(cat -- "${1:--}")

if ! grep -q 'hash mismatch in fixed-output derivation' <<<"$LOG"; then
  exit 1
fi

HASH=$(grep -oE 'got:[[:space:]]+sha256-[A-Za-z0-9+/]+=*' <<<"$LOG" \
         | awk '{print $2}' | head -1 || true)

if [[ -z "$HASH" ]]; then
  exit 1
fi

printf '%s\n' "$HASH"
