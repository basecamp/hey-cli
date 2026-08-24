#!/usr/bin/env bats
# stamp_nix_version.bats - scripts/stamp-nix-version.sh updates only the Nix
# package version. The exact tagged flake is built by the release workflow.

setup() {
  STAMP="${BATS_TEST_DIRNAME}/../../scripts/stamp-nix-version.sh"
  WORK="$(mktemp -d)"
  mkdir -p "$WORK/nix"
  cat > "$WORK/nix/package.nix" <<'NIX'
{ lib, buildGoModule }:
buildGoModule (finalAttrs: {
  pname = "hey";
  version = "0.1.0";
  vendorHash = "sha256-AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA=";
})
NIX
  cd "$WORK"
}

teardown() {
  rm -rf "$WORK"
}

@test "stamps the version and leaves the vendorHash alone" {
  run "$STAMP" 0.2.0
  [ "$status" -eq 0 ]
  grep -q 'version = "0.2.0";' nix/package.nix
  grep -q 'vendorHash = "sha256-AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA=";' nix/package.nix
}

@test "is idempotent" {
  "$STAMP" 0.2.0
  before=$(cat nix/package.nix)
  "$STAMP" 0.2.0
  [ "$(cat nix/package.nix)" = "$before" ]
}

@test "requires a version argument" {
  run "$STAMP"
  [ "$status" -ne 0 ]
  grep -q 'version = "0.1.0";' nix/package.nix
}

@test "--check passes when the file already carries the version" {
  run "$STAMP" --check 0.1.0
  [ "$status" -eq 0 ]
}

@test "--check fails on a stale version and does not rewrite the file" {
  run "$STAMP" --check 0.2.0
  [ "$status" -eq 1 ]
  [[ "$output" == *"is at version 0.1.0, expected 0.2.0"* ]]
  grep -q 'version = "0.1.0";' nix/package.nix
}

@test "fails without leaving a temporary file when no version literal exists" {
  printf '{ vendorHash = "sha256-AAA="; }\n' > nix/package.nix
  run "$STAMP" 0.2.0
  [ "$status" -eq 1 ]
  [ "$(ls -A nix)" = "package.nix" ]
  [ "$(cat nix/package.nix)" = '{ vendorHash = "sha256-AAA="; }' ]
}
