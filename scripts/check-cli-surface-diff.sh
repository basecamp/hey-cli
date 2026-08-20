#!/usr/bin/env bash
# Compare two CLI surface snapshots and fail on removals.
#
# Usage: check-cli-surface-diff.sh BASELINE CURRENT [ALLOWLIST]
#
# A removal listed in ALLOWLIST (one surface line per line, `#` comments and
# blank lines ignored) is an acknowledged breaking change: it is reported but
# does not fail the check. The allowlist is .surface-breaking at the repo root;
# entries are meant to be added in the PR that removes the command or flag and
# pruned after the release that ships it.
set -euo pipefail
if [ $# -lt 2 ] || [ $# -gt 3 ]; then
  echo "Usage: $0 <baseline> <current> [allowlist]"
  exit 1
fi
BASELINE="$1"
CURRENT="$2"
ALLOWLIST="${3:-}"

sorted() { LC_ALL=C sort -u -- "$1"; }

REMOVED=$(LC_ALL=C comm -23 <(sorted "$BASELINE") <(sorted "$CURRENT"))

ALLOWED=""
if [ -n "$ALLOWLIST" ] && [ -f "$ALLOWLIST" ]; then
  ALLOWED=$(grep -vE '^[[:space:]]*(#|$)' "$ALLOWLIST" | LC_ALL=C sort -u || true)
fi

BLOCKED=$(LC_ALL=C comm -23 <(printf '%s\n' "$REMOVED" | sed '/^$/d') <(printf '%s\n' "$ALLOWED" | sed '/^$/d'))
ACKNOWLEDGED=$(LC_ALL=C comm -12 <(printf '%s\n' "$REMOVED" | sed '/^$/d') <(printf '%s\n' "$ALLOWED" | sed '/^$/d'))

if [ -n "$ACKNOWLEDGED" ]; then
  echo "NOTE: acknowledged breaking removals (listed in ${ALLOWLIST}):"
  while IFS= read -r line; do echo "  $line"; done <<<"$ACKNOWLEDGED"
fi

if [ -n "$BLOCKED" ]; then
  echo "FAIL: CLI surface removals detected:"
  while IFS= read -r line; do echo "  $line"; done <<<"$BLOCKED"
  echo ""
  echo "Removing a command or flag is a breaking change. If intentional, add each"
  echo "line above to .surface-breaking in the same PR and call it out in the PR."
  exit 1
fi
echo "PASS: no unacknowledged CLI surface removals"
