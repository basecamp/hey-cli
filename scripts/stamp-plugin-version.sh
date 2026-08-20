#!/usr/bin/env bash
# Usage: scripts/stamp-plugin-version.sh VERSION
#        scripts/stamp-plugin-version.sh --check VERSION
#
# Stamps the CLI release version into .claude-plugin/plugin.json, which Claude
# Code reads to detect plugin updates. release.sh runs it before committing the
# stable release prep. With --check it only verifies that the file already
# carries VERSION, exiting 1 otherwise: GoReleaser runs that form so a stable
# tag pushed by hand, without the stamp commit, fails before anything is
# published rather than shipping stale plugin metadata.

set -euo pipefail

CHECK=0
if [[ "${1:-}" == "--check" ]]; then
  CHECK=1
  shift
fi

VERSION="${1:?Usage: stamp-plugin-version.sh [--check] VERSION}"
PLUGIN_JSON=".claude-plugin/plugin.json"

if ! command -v jq >/dev/null 2>&1; then
  echo "Error: jq is required but not found. Install with your package manager." >&2
  exit 1
fi

if [[ "$CHECK" -eq 1 ]]; then
  CURRENT=$(jq -r .version "$PLUGIN_JSON")
  if [[ "$CURRENT" != "$VERSION" ]]; then
    echo "Error: ${PLUGIN_JSON} is at version ${CURRENT}, expected ${VERSION}." >&2
    echo "  Stable tags must be cut with scripts/release.sh, which commits the stamp before tagging." >&2
    exit 1
  fi
  echo "${PLUGIN_JSON} is at ${VERSION}"
  exit 0
fi

jq --arg v "$VERSION" '.version = $v' "$PLUGIN_JSON" > "${PLUGIN_JSON}.tmp"
mv "${PLUGIN_JSON}.tmp" "$PLUGIN_JSON"
