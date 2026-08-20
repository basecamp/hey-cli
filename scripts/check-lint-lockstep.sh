#!/usr/bin/env bash
# Verify every workflow lints with the same golangci-lint version.
#
# A stale pin here does not fail a PR — it fails whichever job is the odd one
# out, at the moment that job runs. release.yml sat on v2.9.0 while test.yml and
# security.yml moved to v2.11.1, and the drift surfaced as a gosec G115 false
# positive that blocked a release tag after every PR check had gone green. The
# "keep in lockstep" comments were already there; comments do not enforce.
#
# Every golangci-lint-action step must carry a version, not just agree with the
# others. An unpinned step resolves to whatever the action defaults to, which
# drifts on its own schedule and would rebuild exactly the release-only mismatch
# this exists to prevent — so a missing pin is a failure, not a skip.
#
# Agreement alone is not enough either: three workflows uniformly pinned back at
# v2.9.0 would pass this check and reproduce the release failure that motivated
# it. Hence a floor as well as a lockstep.
set -euo pipefail

WORKFLOW_DIR=".github/workflows"

# The oldest golangci-lint every workflow may pin. Raising this is a deliberate
# act, not housekeeping: move it in the same commit that moves the workflow
# pins, so the floor and the pins never disagree in a merged tree.
MIN_VERSION="v2.11.1"

# Compare with sort -V, never lexically or arithmetically. As strings v2.9.0
# sorts *above* v2.11.1 — 9 > 1 — so a lexical test would wave through the exact
# version that failed the release tag. `[ -gt ]` cannot parse either one.
version_gte() {
  printf '%s\n%s\n' "$2" "$1" | sort -V | head -1 | grep -qx -- "$2"
}

# Both spellings: every workflow here is .yml today, but GitHub honours .yaml
# just as well, and a .yaml workflow that ran a linter this check never opened
# would be exactly the invisible drift it exists to catch.
shopt -s nullglob
workflows=("$WORKFLOW_DIR"/*.yml "$WORKFLOW_DIR"/*.yaml)
shopt -u nullglob

if [[ "${#workflows[@]}" -eq 0 ]]; then
  echo "FAIL: no workflow files found under $WORKFLOW_DIR"
  echo "An empty scan is a broken check, not a clean tree."
  exit 1
fi

# One line per golangci-lint-action step: "<file>:<version>", or
# "<file>:UNPINNED" when the step declares no version.
mapfile -t pins < <(
  for f in "${workflows[@]}"; do
    awk -v file="$f" '
      function close_step() {
        if (in_step) { print file ":UNPINNED"; in_step = 0 }
      }
      # A new list item ends the previous step, pinned or not.
      /^[[:space:]]*-[[:space:]]/ { close_step() }
      /uses:.*golangci-lint-action/ { in_step = 1; next }
      in_step && /^[[:space:]]*version:[[:space:]]*v[0-9]/ {
        match($0, /v[0-9]+\.[0-9]+\.[0-9]+/)
        print file ":" substr($0, RSTART, RLENGTH)
        in_step = 0
        next
      }
      # Dedenting to a new job or top-level key also ends the step.
      in_step && /^[[:space:]]{0,4}[A-Za-z_-]+:/ { close_step() }
      END { close_step() }
    ' "$f"
  done
)

if [[ "${#pins[@]}" -eq 0 ]]; then
  echo "FAIL: found no golangci-lint-action steps under $WORKFLOW_DIR"
  echo "If the linter moved to a different action, update $0 to match."
  exit 1
fi

unpinned=$(printf '%s\n' "${pins[@]}" | grep ':UNPINNED$' || true)
if [[ -n "$unpinned" ]]; then
  echo "FAIL: golangci-lint-action step with no version pin:"
  printf '%s\n' "$unpinned" | sed 's/:UNPINNED$//; s/^/  /'
  echo ""
  echo "An unpinned step takes whatever the action defaults to and drifts on"
  echo "its own schedule, which is the mismatch this check exists to prevent."
  exit 1
fi

versions=$(printf '%s\n' "${pins[@]}" | sed 's/.*://' | sort -u)
count=$(printf '%s\n' "$versions" | wc -l | tr -d ' ')

if [[ "$count" -ne 1 ]]; then
  echo "FAIL: golangci-lint pins disagree across workflows:"
  printf '  %s\n' "${pins[@]}"
  echo ""
  echo "Every workflow that lints must pin the same version. Otherwise a job"
  echo "that is not run on PRs — the release gate especially — can fail on a"
  echo "finding no PR check would ever have surfaced."
  exit 1
fi

if ! version_gte "$versions" "$MIN_VERSION"; then
  echo "FAIL: golangci-lint pinned at $versions, below the floor $MIN_VERSION:"
  printf '  %s\n' "${pins[@]}"
  echo ""
  echo "Agreeing on a stale version is still stale. v2.9.0 in lockstep would"
  echo "pass the check above and fail the release on the same gosec G115 false"
  echo "positive it was written to prevent. Raise the pins, then raise"
  echo "MIN_VERSION in $0 in the same commit."
  exit 1
fi

echo "golangci-lint lockstep check passed (${#pins[@]} steps across ${#workflows[@]} workflows pinned at $versions, floor $MIN_VERSION)"
