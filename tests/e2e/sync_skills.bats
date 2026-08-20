#!/usr/bin/env bats
# sync_skills.bats - scripts/sync-skills.sh mirrors the skills/ tree into
# basecamp/skills. The manual recovery workflow runs the script from the
# dispatching ref but reads content from a checkout of the release tag via
# SKILLS_SOURCE, so these tests pin the source-discovery contract both ways.
# DRY_RUN=local keeps everything offline.

setup() {
  SYNC="${BATS_TEST_DIRNAME}/../../scripts/sync-skills.sh"
  WORK="$(mktemp -d)"
  cd "$WORK"

  mkdir -p skills/hey/reference
  printf '# hey skill from main\n' > skills/hey/SKILL.md
  printf 'reference from main\n' > skills/hey/reference/api.md
  printf 'package hey\n' > skills/hey/tool.go
  printf 'secret\n' > skills/hey/.hidden

  mkdir -p release/skills/hey
  printf '# hey skill as released\n' > release/skills/hey/SKILL.md

  export RELEASE_TAG=v0.2.0
  export SOURCE_SHA=0123456789abcdef0123456789abcdef01234567
  export DRY_RUN=local
}

teardown() {
  rm -rf "$WORK"
}

@test "defaults to the skills/ tree and excludes *.go and dotfiles" {
  run "$SYNC"
  [ "$status" -eq 0 ]
  [[ "$output" == *"Found 1 skill(s): skills/hey"* ]]
  [[ "$output" == *"skills/hey/SKILL.md"* ]]
  [[ "$output" == *"skills/hey/reference/api.md"* ]]
  [[ "$output" != *"tool.go"* ]]
  [[ "$output" != *".hidden"* ]]
}

@test "SKILLS_SOURCE points the sync at a tagged checkout" {
  SKILLS_SOURCE=release/skills run "$SYNC"
  [ "$status" -eq 0 ]
  [[ "$output" == *"Found 1 skill(s): release/skills/hey"* ]]
  [[ "$output" == *"skills/hey/SKILL.md"* ]]
  [[ "$output" != *"reference/api.md"* ]]
}

@test "dies when the source tree has no skills" {
  SKILLS_SOURCE=release/empty run "$SYNC"
  [ "$status" -ne 0 ]
  [[ "$output" == *"no skills found under release/empty/*/SKILL.md"* ]]
}
