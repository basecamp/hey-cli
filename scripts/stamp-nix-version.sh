#!/usr/bin/env bash
# Usage: scripts/stamp-nix-version.sh VERSION
#        scripts/stamp-nix-version.sh --check VERSION
#
# Stamps the CLI release version into nix/package.nix without building the
# flake. Pull-request CI keeps the flake and vendorHash valid as source and
# dependencies change; the release workflow builds the exact stable tag before
# publishing it. With --check this only verifies the committed stamp.

set -euo pipefail

CHECK=0
if [[ "${1:-}" == "--check" ]]; then
  CHECK=1
  shift
fi

VERSION="${1:?Usage: stamp-nix-version.sh [--check] VERSION}"
NIX_PKG="nix/package.nix"

CURRENT=$(sed -n 's/^[[:space:]]*version = "\([^"]*\)";[[:space:]]*$/\1/p' "$NIX_PKG" | head -1)
if [[ -z "$CURRENT" ]]; then
  echo "Error: ${NIX_PKG} has no version literal." >&2
  exit 1
fi

if [[ "$CHECK" -eq 1 ]]; then
  if [[ "$CURRENT" != "$VERSION" ]]; then
    echo "Error: ${NIX_PKG} is at version ${CURRENT}, expected ${VERSION}." >&2
    echo "  Stable tags must be cut with scripts/release.sh, which commits the stamp before tagging." >&2
    exit 1
  fi
  echo "${NIX_PKG} is at ${VERSION}"
  exit 0
fi

if [[ "$CURRENT" == "$VERSION" ]]; then
  exit 0
fi

# Write through a sibling temporary file so a failed rewrite leaves neither a
# partial tracked file nor an untracked file that blocks the next release.
TMP=$(mktemp "${NIX_PKG}.XXXXXX")
trap 'rm -f "$TMP"' EXIT
awk -v version="$VERSION" '
  !updated && /^[[:space:]]*version = "[^"]*";/ {
    sub(/version = "[^"]*"/, "version = \"" version "\"")
    updated = 1
  }
  { print }
  END { if (!updated) exit 1 }
' "$NIX_PKG" > "$TMP"
mv "$TMP" "$NIX_PKG"
