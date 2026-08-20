#!/usr/bin/env bash
# Builds the Nix flake, runs the resulting binary, and turns a stale vendorHash
# into an actionable annotation instead of a raw nix dump.
#
# Usage: scripts/nix-build-check.sh
#
# This exists because the nix-build job is *supposed* to fail on a dependency
# bump: changing go.mod or go.sum invalidates vendorHash, and dependabot will
# not update it, so every weekly `gomod` PR fails here. The failure is correct.
# What was wrong is that it arrived as an unexplained nix log with no remedy.
#
# Lives in a script rather than inline in the workflow so the classification is
# reachable from tests — an inline `run:` block can only be exercised by
# breaking main.
#
# Exit status is the nix build's own. Nothing here may swallow a failure: a
# `|| true` plus a "did it look like it built" heuristic is what let v0.8.0
# ship a flake that could not build at all.

set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"

LOG="$(mktemp)"
trap 'rm -f "$LOG"' EXIT

STATUS=0
STORE_PATH="$(nix build --no-link --print-out-paths 2>"$LOG")" || STATUS=$?

# Always replay what nix said, whichever way this goes. The annotation below is
# a hint layered on top of the diagnostics, never a replacement for them.
cat "$LOG" >&2

if [[ "$STATUS" -ne 0 ]]; then
  # Only claim this is a hash problem when the shared classifier agrees, which
  # requires both the fixed-output diagnostic and an SRI-shaped value. Every
  # other failure — a stale flake.lock whose Go is older than go.mod's
  # directive, say — falls through with nix's own output and no false remedy.
  if HASH="$("$SCRIPT_DIR/extract-nix-vendor-hash.sh" "$LOG")"; then
    echo "::error::Stale Nix vendorHash. Expected ${HASH}. A go.mod or go.sum change invalidates it and dependabot does not update it — run 'make update-nix-hash' and commit nix/package.nix."
  fi
  exit "$STATUS"
fi

if [[ -z "$STORE_PATH" ]]; then
  echo "::error::nix build reported success but printed no store path"
  exit 1
fi

"$STORE_PATH/bin/hey" --version
