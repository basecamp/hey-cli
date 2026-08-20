#!/usr/bin/env bash
# Usage: scripts/check-stable-metadata.sh VERSION
#
# Verifies that both pieces of stable release metadata already carry VERSION:
# .claude-plugin/plugin.json (via stamp-plugin-version.sh --check) and
# nix/package.nix. release.sh commits both stamps to main before tagging a
# stable release; GoReleaser runs this check before building, so a stable tag
# pushed by hand without that commit fails before anything is published.
# Post-publication, release.yml's nix-verify still builds the flake and asserts
# the binary's version — this check reads the metadata, that one proves the
# artifact.

set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"

VERSION="${1:?Usage: check-stable-metadata.sh VERSION}"
NIX_PKG="nix/package.nix"

"$SCRIPT_DIR/stamp-plugin-version.sh" --check "$VERSION"

NIX_VERSION=$(sed -n 's/.*version = "\([^"]*\)".*/\1/p' "$NIX_PKG" | head -1)
if [[ "$NIX_VERSION" != "$VERSION" ]]; then
  echo "Error: ${NIX_PKG} is at version ${NIX_VERSION}, expected ${VERSION}." >&2
  echo "  Stable tags must be cut with scripts/release.sh, which bumps the Nix package before tagging." >&2
  exit 1
fi
echo "${NIX_PKG} is at ${VERSION}"
