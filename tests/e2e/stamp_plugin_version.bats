#!/usr/bin/env bats
# stamp_plugin_version.bats - scripts/stamp-plugin-version.sh writes the release
# version into .claude-plugin/plugin.json, which Claude Code reads to detect
# plugin updates. release.sh runs it for stable tags; goreleaser runs the
# --check form (via check-stable-metadata.sh) before the build so a hand-pushed
# stable tag without the stamp commit fails instead of shipping stale metadata.

setup() {
  STAMP="${BATS_TEST_DIRNAME}/../../scripts/stamp-plugin-version.sh"
  WORK="$(mktemp -d)"
  mkdir -p "$WORK/.claude-plugin"
  printf '{\n  "name": "hey",\n  "version": "0.1.0",\n  "license": "MIT"\n}\n' > "$WORK/.claude-plugin/plugin.json"
  cd "$WORK"
}

teardown() {
  rm -rf "$WORK"
}

@test "stamps the version and leaves the other fields alone" {
  run "$STAMP" 0.2.0
  [ "$status" -eq 0 ]
  [ "$(jq -r .version .claude-plugin/plugin.json)" = "0.2.0" ]
  [ "$(jq -r .name .claude-plugin/plugin.json)" = "hey" ]
  [ "$(jq -r .license .claude-plugin/plugin.json)" = "MIT" ]
}

@test "is idempotent" {
  "$STAMP" 0.2.0
  before=$(cat .claude-plugin/plugin.json)
  "$STAMP" 0.2.0
  [ "$(cat .claude-plugin/plugin.json)" = "$before" ]
}

@test "requires a version argument" {
  run "$STAMP"
  [ "$status" -ne 0 ]
  [ "$(jq -r .version .claude-plugin/plugin.json)" = "0.1.0" ]
}

@test "--check passes when the file already carries the version" {
  run "$STAMP" --check 0.1.0
  [ "$status" -eq 0 ]
}

@test "--check fails on a stale version and does not rewrite the file" {
  run "$STAMP" --check 0.2.0
  [ "$status" -eq 1 ]
  [[ "$output" == *"is at version 0.1.0, expected 0.2.0"* ]]
  [ "$(jq -r .version .claude-plugin/plugin.json)" = "0.1.0" ]
}

@test "--check requires a version argument" {
  run "$STAMP" --check
  [ "$status" -ne 0 ]
}
