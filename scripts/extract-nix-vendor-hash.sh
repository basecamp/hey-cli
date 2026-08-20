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
# Three conditions must hold before a hash comes back. Nix must have reported
# a fixed-output hash mismatch, that mismatch must belong to buildGoModule's
# vendor derivation (`*-go-modules.drv`), and the `got:` value taken from that
# same diagnostic must look like an SRI hash. Each guard is load-bearing:
#
# - Matching a bare `got:` is far too loose: any failing build whose log happens
#   to contain one — a Go test assertion printing `got: 42`, say — would yield
#   "42" as a vendorHash and misreport an unrelated failure as a hash problem.
# - Accepting any fixed-output mismatch is also too loose. This flake has a
#   second fixed-output derivation — the Go source tarball nix/go.nix fetches
#   while nixpkgs lags go.mod — and a stale hash there must not be written
#   into vendorHash, nor reported to a contributor as the vendorHash remedy.
#   The rebuild would then fail with nix/package.nix already corrupted.
#
# Exit codes:
#   0 — a go-modules fixed-output hash mismatch was reported; the SRI hash is
#       on stdout
#   1 — the log reports no vendorHash mismatch; nothing on stdout

set -euo pipefail

LOG=$(cat -- "${1:--}")

# The mismatch diagnostic names the derivation on its own line; `specified:`
# and `got:` follow on the next lines. Take the first `got:` after a
# go-modules mismatch and stop there, so a later mismatch for some other
# derivation cannot supply the value.
HASH=$(awk '
  /hash mismatch in fixed-output derivation .*-go-modules\.drv/ { in_block = 1; next }
  in_block && /got:/ {
    if (match($0, /sha256-[A-Za-z0-9+\/]+=*/)) { print substr($0, RSTART, RLENGTH) }
    exit
  }
' <<<"$LOG")

if [[ -z "$HASH" ]]; then
  exit 1
fi

printf '%s\n' "$HASH"
