#!/usr/bin/env bash
# Files or updates the single open issue tracking a recurring failure.
#
# Usage:
#   scripts/notify-issue.sh --repo OWNER/NAME --label LABEL --title TITLE --body BODY
#
# Dedupes by *label*, not by title. Titles get edited the moment a human triages
# the issue, and a title-based lookup then misses and opens a duplicate. A label
# survives retitling, reassignment and rewording.
#
# Fails closed. The lookup's failure and its empty result must never be the same
# thing: `2>/dev/null || true` around the search turns a rate limit, a
# permissions gap or a GitHub outage into "no match found", and the very next
# line creates a duplicate. If we cannot see the issue list, we file nothing and
# say so — the workflow annotation still makes the underlying failure visible.
#
# Shared by release.yml and aur-publish.yml so a fix to this logic reaches both.
#
# Exit codes:
#   0 — commented on the existing issue, or created a new one
#   1 — usage error, or the comment/create itself failed
#   2 — the lookup failed; nothing was created
#   3 — more than one open issue carries the label; nothing was created

set -euo pipefail

REPO=""
LABEL=""
TITLE=""
BODY=""

while [[ $# -gt 0 ]]; do
  case "$1" in
    --repo)  REPO="${2:-}";  shift 2 ;;
    --label) LABEL="${2:-}"; shift 2 ;;
    --title) TITLE="${2:-}"; shift 2 ;;
    --body)  BODY="${2:-}";  shift 2 ;;
    *)
      echo "notify-issue.sh: unknown argument '$1'" >&2
      exit 1
      ;;
  esac
done

missing=""
[[ -n "$REPO"  ]] || missing="$missing --repo"
[[ -n "$LABEL" ]] || missing="$missing --label"
[[ -n "$TITLE" ]] || missing="$missing --title"
[[ -n "$BODY"  ]] || missing="$missing --body"
if [[ -n "$missing" ]]; then
  echo "notify-issue.sh: missing required argument(s):$missing" >&2
  exit 1
fi

if ! matches=$(gh issue list --repo "$REPO" --state open --label "$LABEL" \
                 --json number --jq '.[].number'); then
  echo "::error::Could not list open '${LABEL}' issues in ${REPO}. Filing nothing rather than risk a duplicate — the failure is still annotated on this run."
  exit 2
fi

issues=()
while IFS= read -r number; do
  if [[ "$number" =~ ^[0-9]+$ ]]; then
    issues+=("$number")
  fi
done <<<"$matches"

case "${#issues[@]}" in
  0)
    echo "No open '${LABEL}' issue; filing one."
    gh issue create --repo "$REPO" --title "$TITLE" --body "$BODY" --label "$LABEL"
    ;;
  1)
    echo "Commenting on existing '${LABEL}' issue #${issues[0]}."
    gh issue comment --repo "$REPO" "${issues[0]}" --body "$BODY"
    ;;
  *)
    # Picking one arbitrarily would scatter the history of a single outage
    # across issues that a human already decided to keep separate.
    echo "::error::${#issues[@]} open issues carry the '${LABEL}' label (${issues[*]}). Refusing to guess which one to update — consolidate them, then re-run."
    exit 3
    ;;
esac
