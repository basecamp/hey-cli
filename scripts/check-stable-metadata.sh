#!/usr/bin/env bash
# Usage: scripts/check-stable-metadata.sh VERSION
#
# Verifies that both pieces of stable release metadata already carry VERSION.
# release.sh commits both stamps to main before tagging a stable release;
# GoReleaser runs this check before building, so a stable tag pushed by hand
# without that commit fails before anything is published. release.yml's
# pre-publication nix-verify job also builds the exact tag and asserts the
# binary's version — this check reads the metadata, that one proves the
# artifact.

set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"

VERSION="${1:?Usage: check-stable-metadata.sh VERSION}"

"$SCRIPT_DIR/stamp-plugin-version.sh" --check "$VERSION"
"$SCRIPT_DIR/stamp-nix-version.sh" --check "$VERSION"
