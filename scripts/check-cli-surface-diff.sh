#!/usr/bin/env bash
# Compare CLI surface snapshots and fail on removals.
set -euo pipefail
if [ $# -ne 2 ]; then
  echo "Usage: $0 <baseline.txt> <current.txt>"
  exit 1
fi
BASELINE="$1"
CURRENT="$2"
REMOVED=$(LC_ALL=C comm -23 "$BASELINE" "$CURRENT")
if [ -n "$REMOVED" ]; then
  echo "FAIL: CLI surface removals detected:"
  echo "$REMOVED"
  echo ""
  echo "If intentional, this is a breaking change."
  exit 1
fi
echo "PASS: no CLI surface removals"
