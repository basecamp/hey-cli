#!/usr/bin/env bats
# check_surface_compat.bats - scripts/check-surface-compat.sh compares the
# committed .surface against the previous stable tag's copy and fails on
# removals unless they are acknowledged in .surface-breaking.
#
# Runs against a throwaway git repo so the tag, baseline and allowlist are all
# under the test's control.

setup() {
  REPO_ROOT="$(cd "${BATS_TEST_DIRNAME}/../.." && pwd)"
  WORK="$(mktemp -d)"
  mkdir -p "$WORK/scripts"
  cp "$REPO_ROOT/scripts/check-surface-compat.sh" "$REPO_ROOT/scripts/check-cli-surface-diff.sh" "$WORK/scripts/"
  cd "$WORK"
  git init -q .
  git config user.email maya.okafor@example.com && git config user.name "Maya Okafor"
  printf 'hey\nhey --json\nhey box\nhey box --limit\nhey todo\n' > .surface
  git add -A && git commit -qm "v0.1.0"
  git tag v0.1.0
  # A second commit so HEAD^ resolves and the tag is strictly behind HEAD.
  echo "# notes" > README.md && git add -A && git commit -qm "docs"
}

teardown() {
  rm -rf "$WORK"
}

@test "passes when the surface only grew" {
  printf 'hey\nhey --json\nhey box\nhey box --limit\nhey box --unread\nhey todo\n' > .surface
  run scripts/check-surface-compat.sh
  [ "$status" -eq 0 ]
  [[ "$output" == *"against v0.1.0"* ]]
  [[ "$output" == *"PASS"* ]]
}

@test "fails when a flag was removed" {
  printf 'hey\nhey --json\nhey box\nhey todo\n' > .surface
  run scripts/check-surface-compat.sh
  [ "$status" -eq 1 ]
  [[ "$output" == *"hey box --limit"* ]]
  [[ "$output" == *".surface-breaking"* ]]
}

@test "passes when the removal is acknowledged in .surface-breaking" {
  printf 'hey\nhey --json\nhey box\nhey todo\n' > .surface
  printf '# acknowledged\nhey box --limit\n' > .surface-breaking
  run scripts/check-surface-compat.sh
  [ "$status" -eq 0 ]
  [[ "$output" == *"acknowledged"* ]]
  [[ "$output" == *"hey box --limit"* ]]
}

@test "an allowlist entry does not cover a different removal" {
  printf 'hey\nhey --json\nhey box\n' > .surface
  printf 'hey box --limit\n' > .surface-breaking
  run scripts/check-surface-compat.sh
  [ "$status" -eq 1 ]
  [[ "$output" == *"hey todo"* ]]
}

@test "accepts an explicit baseline tag" {
  git tag v0.0.9 v0.1.0
  printf 'hey\nhey --json\nhey box\nhey box --limit\nhey todo\n' > .surface
  run scripts/check-surface-compat.sh v0.0.9
  [ "$status" -eq 0 ]
  [[ "$output" == *"against v0.0.9"* ]]
}

@test "ignores prerelease tags when resolving the baseline" {
  printf 'hey\nhey --json\nhey box\nhey box --limit\nhey todo\nhey rc-only\n' > .surface
  git add -A && git commit -qm "rc" && git tag v0.2.0-rc.1
  echo "# more" >> README.md && git add -A && git commit -qm "more"
  printf 'hey\nhey --json\nhey box\nhey box --limit\nhey todo\n' > .surface
  # v0.2.0-rc.1 had `hey rc-only`; the stable baseline v0.1.0 did not, so this
  # is not a removal against the stable line.
  run scripts/check-surface-compat.sh
  [ "$status" -eq 0 ]
  [[ "$output" == *"against v0.1.0"* ]]
}

@test "skips with exit 0 when there is no previous stable tag" {
  git tag -d v0.1.0 >/dev/null
  run scripts/check-surface-compat.sh
  [ "$status" -eq 0 ]
  [[ "$output" == *"No previous stable tag"* ]]
}

@test "fails when an explicit baseline tag does not exist" {
  run scripts/check-surface-compat.sh v0.2.O
  [ "$status" -eq 1 ]
  [[ "$output" == *"v0.2.O"* ]]
  [[ "$output" == *"does not resolve"* ]]
}

@test "skips when the baseline tag has no .surface" {
  git rm -q --cached .surface && git commit -qm "no surface" --allow-empty >/dev/null
  git tag -f v0.1.0 HEAD >/dev/null
  echo "# again" >> README.md && git add -A && git commit -qm "again"
  run scripts/check-surface-compat.sh
  [ "$status" -eq 0 ]
  [[ "$output" == *"has no .surface"* ]]
}

@test "fails when the current .surface is missing" {
  rm .surface
  run scripts/check-surface-compat.sh
  [ "$status" -eq 1 ]
  [[ "$output" == *"make update-surface"* ]]
}

@test "fails on a stale .surface-breaking entry that is not a removal" {
  # The surface did not drop `hey box --limit`, so the allowlist line is left
  # over from an earlier release (or pre-authorises a future break).
  printf 'hey\nhey --json\nhey box\nhey box --limit\nhey todo\n' > .surface
  printf 'hey box --limit\n' > .surface-breaking
  run scripts/check-surface-compat.sh
  [ "$status" -eq 1 ]
  [[ "$output" == *"stale"* ]]
  [[ "$output" == *"hey box --limit"* ]]
}

@test "a stale allowlist entry fails even alongside a live acknowledged removal" {
  printf 'hey\nhey --json\nhey box\nhey box --limit\n' > .surface
  printf 'hey todo\nhey box --unread\n' > .surface-breaking
  run scripts/check-surface-compat.sh
  [ "$status" -eq 1 ]
  [[ "$output" == *"acknowledged"*"hey todo"* ]]
  [[ "$output" == *"stale"*"hey box --unread"* ]]
}
