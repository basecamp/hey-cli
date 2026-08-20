#!/usr/bin/env bats
#
# Wrapper behaviour for scripts/nix-build-check.sh — CI's flake gate.
#
# The classifier itself is covered by extract_nix_vendor_hash.bats. What is
# tested here is everything wrapped around it: that the build's real exit status
# survives, that nix's own diagnostics are always replayed, that the `::error::`
# remedy appears only when the classifier agrees, and that a successful build is
# not trusted until the binary it produced actually runs.
#
# `nix` is stubbed via PATH, so these run anywhere in milliseconds and can stage
# failures that are otherwise awkward to reproduce.

setup() {
  REPO_ROOT="$(cd "${BATS_TEST_DIRNAME}/../.." && pwd)"
  CHECK="$REPO_ROOT/scripts/nix-build-check.sh"
  WORK="$(mktemp -d)"
  STUB_DIR="$WORK/bin"
  mkdir -p "$STUB_DIR"
  PATH="$STUB_DIR:$PATH"
}

teardown() {
  rm -rf "$WORK"
}

# Stubs `nix build`: $1 is the store path echoed on stdout, $2 the log written
# to stderr, $3 the exit status.
stub_nix() {
  cat > "$STUB_DIR/nix" <<STUB
#!/usr/bin/env bash
printf '%s' '$1'
[ -n '$1' ] && echo
cat >&2 <<'NIXLOG'
$2
NIXLOG
exit $3
STUB
  chmod +x "$STUB_DIR/nix"
}

# Stubs the built binary at STORE/bin/hey.
stub_store_binary() {
  local store="$WORK/store"
  mkdir -p "$store/bin"
  cat > "$store/bin/hey" <<STUB
#!/usr/bin/env bash
echo "hey version ${1:-0.1.2}"
exit ${2:-0}
STUB
  chmod +x "$store/bin/hey"
  echo "$store"
}

@test "succeeds and runs the built binary" {
  store="$(stub_store_binary 0.1.2 0)"
  stub_nix "$store" "building '/nix/store/abc-hey.drv'..." 0

  run "$CHECK"
  [ "$status" -eq 0 ]
  [[ "$output" == *"hey version 0.1.2"* ]]
}

@test "fails when the flake builds but the binary does not run" {
  # A store path that exists proves nix succeeded, not that the result works.
  store="$(stub_store_binary 0.1.2 1)"
  stub_nix "$store" "" 0

  run "$CHECK"
  [ "$status" -ne 0 ]
}

@test "propagates the build's exit status rather than masking it" {
  stub_nix "" "error: builder failed" 7

  run "$CHECK"
  [ "$status" -eq 7 ]
}

@test "replays nix diagnostics on failure" {
  stub_nix "" "error: attribute 'hey' missing
       at /nix/store/flake.nix:12:5" 1

  run "$CHECK"
  [ "$status" -eq 1 ]
  [[ "$output" == *"attribute 'hey' missing"* ]]
  [[ "$output" == *"flake.nix:12:5"* ]]
}

@test "replays nix diagnostics on success too" {
  store="$(stub_store_binary)"
  stub_nix "$store" "warning: Git tree '/src' is dirty" 0

  run "$CHECK"
  [ "$status" -eq 0 ]
  [[ "$output" == *"Git tree '/src' is dirty"* ]]
}

@test "annotates a stale vendorHash with the correct hash and the remedy" {
  # The dependabot case: a go.mod bump invalidates vendorHash, so the job fails
  # by design. It must fail with instructions, not a raw nix dump.
  stub_nix "" "error: hash mismatch in fixed-output derivation '/nix/store/abc-go-modules.drv':
         specified: sha256-OLDOLDOLDOLDOLDOLDOLDOLDOLDOLDOLDOLDOLDOLDA=
            got:    sha256-NEWNEWNEWNEWNEWNEWNEWNEWNEWNEWNEWNEWNEWNEWB=
error: 1 dependencies of derivation '/nix/store/xyz.drv' failed to build" 1

  run "$CHECK"
  [ "$status" -eq 1 ]
  [[ "$output" == *"::error::"* ]]
  [[ "$output" == *"sha256-NEWNEWNEWNEWNEWNEWNEWNEWNEWNEWNEWNEWNEWNEWB="* ]]
  [[ "$output" == *"make update-nix-hash"* ]]
  # The remedy is layered on top of the diagnostics, never a replacement.
  [[ "$output" == *"hash mismatch in fixed-output derivation"* ]]
}

@test "emits no hash remedy for an unrelated build failure" {
  # The v0.1.1 shape: the fix is flake.lock, and pointing at
  # `make update-nix-hash` would send a contributor down the wrong path.
  stub_nix "" "building '/nix/store/abc-hey-0.1.1-go-modules.drv'...
     > go: go.mod requires go >= 1.26.5 (running go 1.26.4; GOTOOLCHAIN=local)
error: Build failed due to failed dependency" 1

  run "$CHECK"
  [ "$status" -eq 1 ]
  [[ "$output" != *"make update-nix-hash"* ]]
  [[ "$output" == *"GOTOOLCHAIN=local"* ]]
}

@test "emits no hash remedy for a bare 'got:' in a failing build" {
  stub_nix "" "--- FAIL: TestSomething
    thing_test.go:42: got: 42
error: builder failed with exit code 1" 1

  run "$CHECK"
  [ "$status" -eq 1 ]
  [[ "$output" != *"make update-nix-hash"* ]]
}

@test "fails when nix reports success but prints no store path" {
  stub_nix "" "" 0

  run "$CHECK"
  [ "$status" -ne 0 ]
  [[ "$output" == *"no store path"* ]]
}
