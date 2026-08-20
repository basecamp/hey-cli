#!/usr/bin/env bats
#
# Fixtures for scripts/extract-nix-vendor-hash.sh — the single classifier that
# decides whether a failing nix build failed *because of the vendorHash*.
#
# Both production callers depend on this answer, and they depend on it for
# different reasons: update-nix-flake.sh writes the returned value into a
# tracked file, and nix-build-check.sh puts it in front of a contributor as a
# remedy. A loose parse corrupts nix/package.nix in one and misdirects a human
# in the other, which is why the guard is tested here rather than twice at the
# call sites.

setup() {
  REPO_ROOT="$(cd "${BATS_TEST_DIRNAME}/../.." && pwd)"
  EXTRACT="$REPO_ROOT/scripts/extract-nix-vendor-hash.sh"
  WORK="$(mktemp -d)"
}

teardown() {
  rm -rf "$WORK"
}

@test "extracts the SRI hash from a fixed-output mismatch" {
  run "$EXTRACT" <<'LOG'
error: hash mismatch in fixed-output derivation '/nix/store/abc-hey-0.1.2-go-modules.drv':
         specified: sha256-OLDOLDOLDOLDOLDOLDOLDOLDOLDOLDOLDOLDOLDOLDA=
            got:    sha256-NEWNEWNEWNEWNEWNEWNEWNEWNEWNEWNEWNEWNEWNEWB=
error: 1 dependencies of derivation '/nix/store/xyz-hey-0.1.2.drv' failed to build
LOG
  [ "$status" -eq 0 ]
  [ "$output" = "sha256-NEWNEWNEWNEWNEWNEWNEWNEWNEWNEWNEWNEWNEWNEWB=" ]
}

@test "reads from a file argument as well as stdin" {
  cat > "$WORK/nix.log" <<'LOG'
error: hash mismatch in fixed-output derivation '/nix/store/abc-go-modules.drv':
         specified: sha256-AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA=
            got:    sha256-BBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBB=
LOG
  run "$EXTRACT" "$WORK/nix.log"
  [ "$status" -eq 0 ]
  [ "$output" = "sha256-BBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBB=" ]
}

@test "rejects a bare 'got:' with no mismatch diagnostic" {
  # A Go test assertion printing `got: 42`. Matching a bare `got:` would return
  # "42" — which update-nix-flake.sh would then write into vendorHash.
  run "$EXTRACT" <<'LOG'
--- FAIL: TestSomething
    thing_test.go:42: got: 42
    thing_test.go:43: want: 7
error: builder for '/nix/store/xyz.drv' failed with exit code 1
LOG
  [ "$status" -ne 0 ]
  [ -z "$output" ]
}

@test "rejects a mismatch diagnostic whose value is not SRI-shaped" {
  # Both guards are load-bearing independently: the diagnostic is present here,
  # so a diagnostic-only check would accept "not-a-hash".
  run "$EXTRACT" <<'LOG'
error: hash mismatch in fixed-output derivation '/nix/store/abc-go-modules.drv':
         specified: sha256-AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA=
            got:    not-a-hash
LOG
  [ "$status" -ne 0 ]
  [ -z "$output" ]
}

@test "rejects an md5-style hash that is not sha256 SRI" {
  run "$EXTRACT" <<'LOG'
error: hash mismatch in fixed-output derivation '/nix/store/abc-go-modules.drv':
            got:    md5-d41d8cd98f00b204e9800998ecf8427e
LOG
  [ "$status" -ne 0 ]
  [ -z "$output" ]
}

@test "rejects the Go toolchain drift that broke v0.1.1" {
  # The failure this whole lineage exists for. It reports no hash mismatch, so
  # it must never be classified as one — the fix is flake.lock, not vendorHash.
  run "$EXTRACT" <<'LOG'
building '/nix/store/abc-hey-0.1.1-go-modules.drv'...
     > go: go.mod requires go >= 1.26.5 (running go 1.26.4; GOTOOLCHAIN=local)
error: Build failed due to failed dependency
LOG
  [ "$status" -ne 0 ]
  [ -z "$output" ]
}

@test "rejects a mismatch in a fixed-output derivation that is not go-modules" {
  # This flake carries a second fixed-output derivation: the Go source tarball
  # nix/go.nix fetches while nixpkgs lags go.mod. A stale hash there is a
  # nix/go.nix problem; writing it into vendorHash corrupts the tracked file and
  # then fails the verification rebuild anyway.
  run "$EXTRACT" <<'LOG'
error: hash mismatch in fixed-output derivation '/nix/store/abc-go1.26.6.src.tar.gz.drv':
         specified: sha256-AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA=
            got:    sha256-BBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBB=
error: 1 dependencies of derivation '/nix/store/xyz-go-1.26.6.drv' failed to build
LOG
  [ "$status" -ne 0 ]
  [ -z "$output" ]
}

@test "takes the go-modules hash even when another mismatch precedes it" {
  # The `got:` must come from the go-modules block itself, not from whichever
  # mismatch nix happened to print first.
  run "$EXTRACT" <<'LOG'
error: hash mismatch in fixed-output derivation '/nix/store/abc-go1.26.6.src.tar.gz.drv':
         specified: sha256-AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA=
            got:    sha256-BBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBB=
error: hash mismatch in fixed-output derivation '/nix/store/def-hey-0.1.2-go-modules.drv':
         specified: sha256-OLDOLDOLDOLDOLDOLDOLDOLDOLDOLDOLDOLDOLDOLDA=
            got:    sha256-NEWNEWNEWNEWNEWNEWNEWNEWNEWNEWNEWNEWNEWNEWB=
LOG
  [ "$status" -eq 0 ]
  [ "$output" = "sha256-NEWNEWNEWNEWNEWNEWNEWNEWNEWNEWNEWNEWNEWNEWB=" ]
}

@test "does not borrow a later derivation's got: for a go-modules mismatch" {
  # A go-modules diagnostic whose own value is unusable must not fall through
  # to the next block's hash.
  run "$EXTRACT" <<'LOG'
error: hash mismatch in fixed-output derivation '/nix/store/def-hey-0.1.2-go-modules.drv':
            got:    not-a-hash
error: hash mismatch in fixed-output derivation '/nix/store/abc-go1.26.6.src.tar.gz.drv':
            got:    sha256-BBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBB=
LOG
  [ "$status" -ne 0 ]
  [ -z "$output" ]
}

@test "rejects an empty log" {
  run "$EXTRACT" </dev/null
  [ "$status" -ne 0 ]
  [ -z "$output" ]
}

@test "returns the first hash when nix reports several mismatches" {
  # Deterministic rather than arbitrary: update-nix-flake.sh rebuilds after
  # writing, so it converges across repeated runs. Picking a different one each
  # time would not.
  run "$EXTRACT" <<'LOG'
error: hash mismatch in fixed-output derivation '/nix/store/one-go-modules.drv':
            got:    sha256-1111111111111111111111111111111111111111111=
error: hash mismatch in fixed-output derivation '/nix/store/two-go-modules.drv':
            got:    sha256-2222222222222222222222222222222222222222222=
LOG
  [ "$status" -eq 0 ]
  [ "$output" = "sha256-1111111111111111111111111111111111111111111=" ]
}
