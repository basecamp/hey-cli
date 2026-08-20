#!/usr/bin/env bash
# Compare two CLI surface snapshots and fail on removals.
#
# Usage: check-cli-surface-diff.sh BASELINE CURRENT [ALLOWLIST]
#
# A removal listed in ALLOWLIST (one surface line per line, `#` comments and
# blank lines ignored) is an acknowledged breaking change: it is reported but
# does not fail the check. The allowlist is .surface-breaking at the repo root;
# entries are meant to be added in the PR that removes the command or flag and
# pruned after the release that ships it. An entry that is not a removal against
# BASELINE is stale (left over from an earlier release, or pre-authorising a
# break that has not happened) and fails the check, so every break is
# acknowledged in the PR that introduces it.
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
STALE=$(LC_ALL=C comm -13 <(printf '%s\n' "$REMOVED" | sed '/^$/d') <(printf '%s\n' "$ALLOWED" | sed '/^$/d'))

if [ -n "$ACKNOWLEDGED" ]; then
  echo "NOTE: acknowledged breaking removals (listed in ${ALLOWLIST}):"
  while IFS= read -r line; do echo "  $line"; done <<<"$ACKNOWLEDGED"
fi

status=0
if [ -n "$BLOCKED" ]; then
  echo "FAIL: CLI surface removals detected:"
  while IFS= read -r line; do echo "  $line"; done <<<"$BLOCKED"
  echo ""
  echo "Removing a command or flag is a breaking change. If intentional, add each"
  echo "line above to .surface-breaking in the same PR and call it out in the PR."
  status=1
fi

if [ -n "$STALE" ]; then
  echo "FAIL: stale entries in ${ALLOWLIST} (not removals against the baseline):"
  while IFS= read -r line; do echo "  $line"; done <<<"$STALE"
  echo ""
  echo "Entries are pruned after the release that ships them and may not be added"
  echo "ahead of the removal; drop each line above."
  status=1
fi

[ "$status" -eq 0 ] || exit "$status"
echo "PASS: no unacknowledged CLI surface removals"
