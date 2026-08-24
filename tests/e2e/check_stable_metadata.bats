#!/usr/bin/env bats
# check_stable_metadata.bats - scripts/check-stable-metadata.sh asserts that a
# stable tag's release metadata (.claude-plugin/plugin.json and
# nix/package.nix) already carries the release version. GoReleaser runs it
# before building, so a hand-pushed stable tag without the release prep commit
# fails before anything is published. The pre-publication nix-verify job then
# proves that the exact tagged flake builds with the same version.

setup() {
  CHECK="${BATS_TEST_DIRNAME}/../../scripts/check-stable-metadata.sh"
  WORK="$(mktemp -d)"
  mkdir -p "$WORK/.claude-plugin" "$WORK/nix"
  printf '{\n  "name": "hey",\n  "version": "0.2.0",\n  "license": "MIT"\n}\n' > "$WORK/.claude-plugin/plugin.json"
  cat > "$WORK/nix/package.nix" <<'NIX'
{ lib, buildGoModule }:
buildGoModule (finalAttrs: {
  pname = "hey";
  version = "0.2.0";
  vendorHash = "sha256-AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA=";
})
NIX
  cd "$WORK"
}

teardown() {
  rm -rf "$WORK"
}

@test "passes when both files carry the version" {
  run "$CHECK" 0.2.0
  [ "$status" -eq 0 ]
  [[ "$output" == *".claude-plugin/plugin.json is at 0.2.0"* ]]
  [[ "$output" == *"nix/package.nix is at 0.2.0"* ]]
}

@test "fails when the plugin stamp is stale" {
  printf '{\n  "version": "0.1.0"\n}\n' > .claude-plugin/plugin.json
  run "$CHECK" 0.2.0
  [ "$status" -eq 1 ]
  [[ "$output" == *"plugin.json is at version 0.1.0, expected 0.2.0"* ]]
}

@test "fails when the nix version is stale even though the plugin stamp is current" {
  sed -i.bak 's/version = "0.2.0"/version = "0.1.0"/' nix/package.nix
  run "$CHECK" 0.2.0
  [ "$status" -eq 1 ]
  [[ "$output" == *"nix/package.nix is at version 0.1.0, expected 0.2.0"* ]]
}

@test "does not rewrite either file" {
  sed -i.bak 's/version = "0.2.0"/version = "0.1.0"/' nix/package.nix
  before_plugin=$(cat .claude-plugin/plugin.json)
  before_nix=$(cat nix/package.nix)
  run "$CHECK" 0.2.0
  [ "$status" -eq 1 ]
  [ "$(cat .claude-plugin/plugin.json)" = "$before_plugin" ]
  [ "$(cat nix/package.nix)" = "$before_nix" ]
}

@test "requires a version argument" {
  run "$CHECK"
  [ "$status" -ne 0 ]
}
