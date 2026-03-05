#!/usr/bin/env bash
set -euo pipefail

# Copy the hey skill to the local agent skills directory.
# Usage: scripts/sync-skill.sh

SKILL_SRC="$(cd "$(dirname "$0")/.." && pwd)/skills/hey"
SKILL_DST="${HOME}/.agents/skills/hey"

if [ ! -d "$SKILL_SRC" ]; then
  echo "ERROR: skill source not found: $SKILL_SRC" >&2
  exit 1
fi

mkdir -p "$SKILL_DST"
cp "$SKILL_SRC/SKILL.md" "$SKILL_DST/SKILL.md"
echo "Synced skill to $SKILL_DST"
