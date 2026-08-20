#!/usr/bin/env bats
#
# The nix/package.nix version gate in scripts/release.sh. The flake's version
# is a literal nothing else updates, so a stable tag must not be created while
# it still names the previous release.
#
# The gate sits before the branch check, so a throwaway repo on a side branch
# reaches it; a version that passes the gate then fails on that next check,
# which is how "passed" is observed here.

setup() {
  REPO_ROOT="$(cd "${BATS_TEST_DIRNAME}/../.." && pwd)"
  WORK="$(mktemp -d)"
  mkdir -p "$WORK/repo/nix" "$WORK/repo/scripts"
  cp "$REPO_ROOT/scripts/release.sh" "$WORK/repo/scripts/"
  cat > "$WORK/repo/nix/package.nix" <<'NIXPKG'
{
  version = "0.1.1";
  vendorHash = "sha256-OLDOLDOLDOLDOLDOLDOLDOLDOLDOLDOLDOLDOLDOLDA=";
}
NIXPKG
  git init -q --bare -b main "$WORK/origin.git"
  cd "$WORK/repo"
  git init -q -b main .
  git config user.email t@t && git config user.name t
  git add -A && git commit -qm init
  git remote add origin "$WORK/origin.git"
  git push -q origin main
  git checkout -q -b release-test
}

teardown() {
  rm -rf "$WORK"
}

@test "refuses a stable tag that nix/package.nix does not carry" {
  run scripts/release.sh v0.2.0 --dry-run
  [ "$status" -eq 1 ]
  [[ "$output" == *"nix/package.nix is at 0.1.1, not 0.2.0"* ]]
  [[ "$output" == *"make update-nix-hash VERSION=v0.2.0"* ]]
}

@test "passes a stable tag that matches nix/package.nix" {
  run scripts/release.sh v0.1.1 --dry-run
  [[ "$output" != *"nix/package.nix is at"* ]]
  # Past the gate: the next check is the one that fails here.
  [[ "$output" == *"Not on main"* ]]
}

@test "exempts prereleases" {
  run scripts/release.sh v0.2.0-rc.1 --dry-run
  [[ "$output" != *"nix/package.nix is at"* ]]
  [[ "$output" == *"Not on main"* ]]
}
