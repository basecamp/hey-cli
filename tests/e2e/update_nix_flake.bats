#!/usr/bin/env bats
#
# Exit-status classification for scripts/update-nix-flake.sh.
#
# The PR-time flake build proves the *current* flake compiles; it says nothing
# about whether this script correctly tells a passing build from a failing one.
# That distinction is the actual defect that shipped: v0.1.1's flake could not
# build, and the script reported "vendorHash: verified (build succeeded)".
#
# `docker` is stubbed so these run anywhere, in milliseconds, and can reproduce
# failure modes that are otherwise awkward to stage.

setup() {
  REPO_ROOT="$(cd "${BATS_TEST_DIRNAME}/../.." && pwd)"
  WORK="$(mktemp -d)"
  STUB_DIR="$WORK/bin"
  mkdir -p "$STUB_DIR" "$WORK/repo/nix" "$WORK/repo/scripts"

  # The classifier travels with it — update-nix-flake.sh resolves it relative to
  # its own directory, and shares it with CI's nix-build-check.sh.
  cp "$REPO_ROOT/scripts/update-nix-flake.sh" \
     "$REPO_ROOT/scripts/extract-nix-vendor-hash.sh" "$WORK/repo/scripts/"
  cat > "$WORK/repo/nix/package.nix" <<'NIXPKG'
{
  version = "0.1.1";
  vendorHash = "sha256-OLDOLDOLDOLDOLDOLDOLDOLDOLDOLDOLDOLDOLDOLDA=";
}
NIXPKG

  cd "$WORK/repo"
  git init -q .
  git config user.email t@t && git config user.name t
  git add -A && git commit -qm init
  # A stable tag at HEAD keeps go.mod/go.sum unchanged, so DEPS_CHANGED=false.
  git tag v0.1.1

  PATH="$STUB_DIR:$PATH"
}

teardown() {
  # Cleanup must not decide whether the test passed. Under `bats -j` these tests
  # each build a throwaway git repo in $TMPDIR, and on macOS `rm -rf`
  # intermittently fails with ENOTEMPTY on .git/objects. bats treats a failing
  # teardown as a failing test, so a green assertion was reported red — twice in
  # six local runs, landing on a different test name each time.
  #
  # ENOTEMPTY says the directory was not empty when rm reached it; it does not
  # say what refilled it, and that has not been established. Hence a fix at the
  # teardown rather than at a presumed cause: retry briefly, then give up
  # quietly. A leftover directory under $TMPDIR is worth less than a trustworthy
  # signal, and the assertions have already run either way.
  for _ in 1 2 3; do
    rm -rf "$WORK" 2>/dev/null && return 0
    sleep 0.1
  done
  rm -rf "$WORK" 2>/dev/null || true
  return 0
}

# Emits a canned nix log plus the sentinel the script reads for exit status.
stub_docker() {
  cat > "$STUB_DIR/docker" <<STUB
#!/usr/bin/env bash
if [ "\${1:-}" = volume ] && [ "\${2:-}" = create ]; then
  echo test-nix-store
  exit 0
fi
if [ "\${1:-}" = volume ] && [ "\${2:-}" = rm ]; then
  exit 0
fi
cat <<'OUT'
$1
OUT
STUB
  chmod +x "$STUB_DIR/docker"
}

@test "succeeds when the nix build succeeds" {
  stub_docker "building '/nix/store/abc-hey-0.1.1-go-modules.drv'...
NIX_BUILD_EXIT=0"

  run scripts/update-nix-flake.sh 0.1.1
  [ "$status" -eq 2 ]   # 2 = no changes needed
  [[ "$output" == *"verified (build succeeded)"* ]]
}

@test "rewrites the version literal when a new version is requested" {
  # The script's other responsibility. Without this case a regression in the
  # version rewrite passes CI, because every other test asks for the version
  # already stored in package.nix.
  stub_docker "NIX_BUILD_EXIT=0"

  run scripts/update-nix-flake.sh 0.2.0
  [ "$status" -eq 0 ]   # 0 = changes written
  [[ "$output" == *"nix version: 0.1.1 → 0.2.0"* ]]
  [[ "$output" == *"verified (build succeeded)"* ]]
  grep -q 'version = "0.2.0";' nix/package.nix
  ! grep -q 'version = "0.1.1";' nix/package.nix
  # The hash line is untouched by a version-only change.
  grep -q 'sha256-OLDOLDOLDOLDOLDOLDOLDOLDOLDOLDOLDOLDOLDOLDA=' nix/package.nix
}

@test "does not write a version when the build fails" {
  # A failed run must leave no trace: the version literal is rewritten before
  # the build, and the EXIT trap restores it when the build fails. Otherwise a
  # re-run would report "no changes needed" while an unverified bump sat in
  # the worktree waiting to be committed.
  stub_docker "error: builder failed with exit code 1
NIX_BUILD_EXIT=1"

  run scripts/update-nix-flake.sh 0.2.0
  [ "$status" -eq 1 ]
  [[ "$output" == *"not because of the vendorHash"* ]]
  grep -q 'version = "0.1.1";' nix/package.nix
  ! grep -q 'version = "0.2.0";' nix/package.nix
}

@test "does not write the Go source tarball's hash into vendorHash" {
  # nix/go.nix fetches the Go source with its own fixed-output hash. When that
  # one is stale, the remedy is in go.nix; the classifier must not hand its
  # `got:` to this script, which would corrupt package.nix and then fail the
  # verification rebuild.
  stub_docker "error: hash mismatch in fixed-output derivation '/nix/store/abc-go1.26.6.src.tar.gz.drv':
         specified: sha256-AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA=
            got:    sha256-BBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBB=
NIX_BUILD_EXIT=1"

  run scripts/update-nix-flake.sh 0.1.1
  [ "$status" -eq 1 ]
  [[ "$output" == *"not because of the vendorHash"* ]]
  grep -q 'sha256-OLDOLDOLDOLDOLDOLDOLDOLDOLDOLDOLDOLDOLDOLDA=' nix/package.nix
  ! grep -q 'sha256-BBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBB=' nix/package.nix
}

@test "fails closed when the build fails without a hash mismatch" {
  # The exact v0.1.1 shape: nix logs that it is *building* hey, then dies
  # on the Go toolchain. The old heuristic matched that line and reported
  # success.
  stub_docker "building '/nix/store/abc-hey-0.1.1-go-modules.drv'...
       > go: go.mod requires go >= 1.26.5 (running go 1.26.4; GOTOOLCHAIN=local)
error: Build failed due to failed dependency
NIX_BUILD_EXIT=1"

  run scripts/update-nix-flake.sh 0.1.1
  [ "$status" -eq 1 ]
  [[ "$output" == *"not because of the vendorHash"* ]]
  [[ "$output" != *"verified (build succeeded)"* ]]
}

@test "records a corrected hash only after a rebuild proves it" {
  # First call reports the mismatch, second call succeeds. Both must mount the
  # same ephemeral Nix store so the verification build can reuse everything
  # the discovery build already downloaded and compiled.
  cat > "$STUB_DIR/docker" <<'STUB'
#!/usr/bin/env bash
STATE="${NIX_STUB_STATE:?}"
LOG="${NIX_STUB_LOG:?}"

if [ "${1:-}" = volume ] && [ "${2:-}" = create ]; then
  echo test-nix-store
  exit 0
fi
if [ "${1:-}" = volume ] && [ "${2:-}" = rm ]; then
  echo "rm $3" >> "$LOG"
  exit 0
fi

# The staged source is mounted at SRC:/src:ro — find it.
SRC=""
STORE=""
for a in "$@"; do
  case "$a" in
    *:/src:ro) SRC="${a%%:*}";;
    *:/nix) STORE="${a%:/nix}";;
  esac
done
echo "run $STORE" >> "$LOG"

if [ -f "$STATE" ]; then
  if [ "$(cat "$STATE")" != "$STORE" ]; then
    echo "error: verification rebuild used a different Nix store"
    echo "NIX_BUILD_EXIT=1"
    exit 0
  fi
  # The verification rebuild must see the corrected hash: the source is
  # restaged per call, so a stale first-call stage would silently verify the
  # old hash instead.
  if ! grep -q 'sha256-NEWNEWNEWNEWNEWNEWNEWNEWNEWNEWNEWNEWNEWNEWB=' "$SRC/nix/package.nix"; then
    echo "error: rebuild staged the stale vendorHash"
    echo "NIX_BUILD_EXIT=1"
    exit 0
  fi
  echo "NIX_BUILD_EXIT=0"
else
  printf '%s\n' "$STORE" > "$STATE"
  echo "error: hash mismatch in fixed-output derivation '/nix/store/abc-hey-0.1.1-go-modules.drv':"
  echo "         specified: sha256-OLDOLDOLDOLDOLDOLDOLDOLDOLDOLDOLDOLDOLDOLDA="
  echo "            got:    sha256-NEWNEWNEWNEWNEWNEWNEWNEWNEWNEWNEWNEWNEWNEWB="
  echo "NIX_BUILD_EXIT=1"
fi
STUB
  chmod +x "$STUB_DIR/docker"

  NIX_STUB_STATE="$WORK/store" NIX_STUB_LOG="$WORK/docker.log" \
    run scripts/update-nix-flake.sh 0.1.1
  [ "$status" -eq 0 ]   # 0 = changes written
  [[ "$output" == *"vendorHash: updated"* ]]
  [[ "$output" == *"verified (build succeeded)"* ]]
  grep -q 'sha256-NEWNEWNEWNEWNEWNEWNEWNEWNEWNEWNEWNEWNEWNEWB=' nix/package.nix

  STORE="$(cat "$WORK/store")"
  [ "$STORE" = test-nix-store ]
  [ "$(grep -c "^run $STORE$" "$WORK/docker.log")" -eq 2 ]
  [ "$(grep -c "^rm $STORE$" "$WORK/docker.log")" -eq 1 ]
}

@test "uses a fresh temporary Nix store for each invocation and removes it" {
  # The speedup is intentionally intra-run only. It must not leave a cache in
  # the checkout or the user's home, and a later updater invocation must begin
  # with a different named volume.
  cat > "$STUB_DIR/docker" <<'STUB'
#!/usr/bin/env bash
LOG="${NIX_STUB_LOG:?}"
COUNTER="${NIX_STUB_COUNTER:?}"
if [ "${1:-}" = volume ] && [ "${2:-}" = create ]; then
  N=0
  [ ! -f "$COUNTER" ] || N="$(cat "$COUNTER")"
  N=$((N + 1))
  printf '%s\n' "$N" > "$COUNTER"
  echo "test-nix-store-$N"
  exit 0
fi
if [ "${1:-}" = volume ] && [ "${2:-}" = rm ]; then
  echo "rm $3" >> "$LOG"
  exit 0
fi

STORE=""
for a in "$@"; do
  case "$a" in *:/nix) STORE="${a%:/nix}";; esac
done
echo "run $STORE" >> "$LOG"
echo "NIX_BUILD_EXIT=0"
STUB
  chmod +x "$STUB_DIR/docker"

  NIX_STUB_LOG="$WORK/docker.log" NIX_STUB_COUNTER="$WORK/counter" \
    run scripts/update-nix-flake.sh 0.1.1
  [ "$status" -eq 2 ]
  NIX_STUB_LOG="$WORK/docker.log" NIX_STUB_COUNTER="$WORK/counter" \
    run scripts/update-nix-flake.sh 0.1.1
  [ "$status" -eq 2 ]

  FIRST_STORE="$(sed -n '1s/^run //p' "$WORK/docker.log")"
  SECOND_STORE="$(sed -n '3s/^run //p' "$WORK/docker.log")"
  [ "$FIRST_STORE" = test-nix-store-1 ]
  [ "$SECOND_STORE" = test-nix-store-2 ]
  [[ "$FIRST_STORE" != */* ]]
  [[ "$SECOND_STORE" != */* ]]
  [ "$FIRST_STORE" != "$SECOND_STORE" ]
  grep -qx "rm $FIRST_STORE" "$WORK/docker.log"
  grep -qx "rm $SECOND_STORE" "$WORK/docker.log"
}

@test "removes the temporary Nix store when the build fails" {
  cat > "$STUB_DIR/docker" <<'STUB'
#!/usr/bin/env bash
LOG="${NIX_STUB_LOG:?}"
if [ "${1:-}" = volume ] && [ "${2:-}" = create ]; then
  echo test-nix-store
  exit 0
fi
if [ "${1:-}" = volume ] && [ "${2:-}" = rm ]; then
  echo "rm $3" >> "$LOG"
  exit 0
fi

for a in "$@"; do
  case "$a" in *:/nix) echo "run ${a%:/nix}" >> "$LOG";; esac
done
echo "error: builder failed with exit code 1"
echo "NIX_BUILD_EXIT=1"
STUB
  chmod +x "$STUB_DIR/docker"

  NIX_STUB_LOG="$WORK/docker.log" run scripts/update-nix-flake.sh 0.2.0
  [ "$status" -eq 1 ]
  STORE="$(sed -n '1s/^run //p' "$WORK/docker.log")"
  grep -qx "rm $STORE" "$WORK/docker.log"
  grep -q 'version = "0.1.1";' nix/package.nix
}

@test "fails closed and restores edits when temporary store cleanup fails" {
  cat > "$STUB_DIR/docker" <<'STUB'
#!/usr/bin/env bash
if [ "${1:-}" = volume ] && [ "${2:-}" = create ]; then
  echo test-nix-store
  exit 0
fi
if [ "${1:-}" = volume ] && [ "${2:-}" = rm ]; then
  exit 1
fi
echo "NIX_BUILD_EXIT=0"
STUB
  chmod +x "$STUB_DIR/docker"

  run scripts/update-nix-flake.sh 0.2.0
  [ "$status" -eq 1 ]
  [[ "$output" == *"could not remove temporary Nix store volume"* ]]
  grep -q 'version = "0.1.1";' nix/package.nix
  ! grep -q 'version = "0.2.0";' nix/package.nix
}

@test "does not attempt cleanup when temporary store creation fails" {
  cat > "$STUB_DIR/docker" <<'STUB'
#!/usr/bin/env bash
LOG="${NIX_STUB_LOG:?}"
printf '%s\n' "$*" >> "$LOG"
if [ "${1:-}" = volume ] && [ "${2:-}" = create ]; then
  echo "Cannot connect to the Docker daemon" >&2
  exit 1
fi
echo "unexpected Docker command" >&2
exit 1
STUB
  chmod +x "$STUB_DIR/docker"

  NIX_STUB_LOG="$WORK/docker.log" run scripts/update-nix-flake.sh 0.2.0
  [ "$status" -eq 1 ]
  [[ "$output" == *"could not create a temporary Nix store volume"* ]]
  [ "$(wc -l < "$WORK/docker.log")" -eq 1 ]
  grep -q '^volume create ' "$WORK/docker.log"
  grep -q 'version = "0.1.1";' nix/package.nix
  ! grep -q 'version = "0.2.0";' nix/package.nix
}

@test "fails when the corrected hash still does not build" {
  # Every call reports a mismatch — the rebuild never converges, so the script
  # must not claim success just because it wrote a new hash.
  stub_docker "error: hash mismatch in fixed-output derivation '/nix/store/abc-hey-0.1.1-go-modules.drv':
         specified: sha256-OLDOLDOLDOLDOLDOLDOLDOLDOLDOLDOLDOLDOLDOLDA=
            got:    sha256-NEWNEWNEWNEWNEWNEWNEWNEWNEWNEWNEWNEWNEWNEWB=
NIX_BUILD_EXIT=1"

  run scripts/update-nix-flake.sh 0.1.1
  [ "$status" -eq 1 ]
  [[ "$output" == *"still fails after updating the vendorHash"* ]]
  # The unproven hash must not survive the failure.
  grep -q 'sha256-OLDOLDOLDOLDOLDOLDOLDOLDOLDOLDOLDOLDOLDOLDA=' nix/package.nix
  ! grep -q 'sha256-NEWNEWNEWNEWNEWNEWNEWNEWNEWNEWNEWNEWNEWNEWB=' nix/package.nix
}

@test "does not mistake an unrelated 'got:' line for a hash mismatch" {
  # A failing build whose log happens to contain `got:` — a Go test assertion,
  # say — must take the non-hash failure path. Matching a bare `got:` would
  # capture "42" and write it into vendorHash, corrupting a tracked file while
  # reporting the wrong kind of failure.
  stub_docker "--- FAIL: TestSomething
    thing_test.go:42: got: 42
    thing_test.go:43: want: 7
error: builder failed with exit code 1
NIX_BUILD_EXIT=1"

  run scripts/update-nix-flake.sh 0.1.1
  [ "$status" -eq 1 ]
  [[ "$output" == *"not because of the vendorHash"* ]]
  [[ "$output" != *"vendorHash: updated"* ]]
  # The tracked file must be untouched.
  grep -q 'sha256-OLDOLDOLDOLDOLDOLDOLDOLDOLDOLDOLDOLDOLDOLDA=' nix/package.nix
  ! grep -q '"42"' nix/package.nix
}

@test "verifies the build even when dependencies did not change" {
  # DEPS_CHANGED=false must not skip the build: the Go-toolchain drift that
  # broke v0.1.1 touches neither go.mod nor go.sum.
  stub_docker "NIX_BUILD_EXIT=0"

  run scripts/update-nix-flake.sh 0.1.1
  [ "$status" -eq 2 ]
  [[ "$output" == *"dependencies unchanged — verifying the Nix build anyway"* ]]
  [[ "$output" == *"verified (build succeeded)"* ]]
}

@test "a NIX_BUILD_EXIT=0 that is not the final line is not a success" {
  # The sentinel is emitted by the docker wrapper after the build; anything
  # else printing that string earlier (a quoted log line) must not count.
  stub_docker "echo NIX_BUILD_EXIT=0 is what success looks like
error: builder failed with exit code 1
NIX_BUILD_EXIT=1"

  run scripts/update-nix-flake.sh 0.1.1
  [ "$status" -eq 1 ]
  [[ "$output" != *"verified (build succeeded)"* ]]
}

@test "refuses to verify when an untracked Go file would be omitted" {
  # The build source stages tracked files only, so an untracked .go file never
  # reaches the verification build — but once committed it reaches CI's build,
  # and an import it adds shifts `go mod vendor`'s output and the vendorHash
  # with it (demonstrated on this repo: identical go.mod/go.sum vendor to
  # different trees with and without the file). Verifying anyway would stamp
  # "verified" on a hash CI then rejects. The script must refuse instead,
  # before any build runs or any edit lands.
  echo 'package scratch' > scratch.go
  # Any docker invocation is itself a failure: the guard must fire first.
  cat > "$STUB_DIR/docker" <<'STUB'
#!/usr/bin/env bash
echo "error: build ran despite untracked Go source"
echo "NIX_BUILD_EXIT=1"
STUB
  chmod +x "$STUB_DIR/docker"

  run scripts/update-nix-flake.sh 0.2.0
  [ "$status" -eq 1 ]
  [[ "$output" == *"untracked Go source"* ]]
  [[ "$output" == *"scratch.go"* ]]
  [[ "$output" != *"build ran despite"* ]]
  [[ "$output" != *"verified (build succeeded)"* ]]
  # Refused before the version edit — nothing to clean up or accidentally commit.
  grep -q 'version = "0.1.1";' nix/package.nix
}

@test "stages tracked files only, and untracked non-Go files do not block" {
  # Untracked files that cannot move the vendorHash (notes, scratch dirs) are
  # excluded from the build source but must not stop the tool — the guard is
  # scoped to the Go build graph, not to a pristine tree.
  echo 'draft release notes' > NOTES.md

  cat > "$STUB_DIR/docker" <<'STUB'
#!/usr/bin/env bash
if [ "${1:-}" = volume ] && [ "${2:-}" = create ]; then
  echo test-nix-store
  exit 0
fi
if [ "${1:-}" = volume ] && [ "${2:-}" = rm ]; then
  exit 0
fi
SRC=""
for a in "$@"; do
  case "$a" in *:/src:ro) SRC="${a%%:*}";; esac
done
[ -n "$SRC" ] || { echo "error: no /src mount"; echo "NIX_BUILD_EXIT=1"; exit 0; }
# Tracked files arrive at their working-tree content...
grep -q 'version = "0.1.1";' "$SRC/nix/package.nix" || {
  echo "error: tracked file missing from stage"; echo "NIX_BUILD_EXIT=1"; exit 0; }
# ...and untracked files do not arrive at all.
if [ -e "$SRC/NOTES.md" ]; then
  echo "error: untracked file staged into build source"
  echo "NIX_BUILD_EXIT=1"
  exit 0
fi
echo "NIX_BUILD_EXIT=0"
STUB
  chmod +x "$STUB_DIR/docker"

  run scripts/update-nix-flake.sh 0.1.1
  [ "$status" -eq 2 ]
  [[ "$output" == *"verified (build succeeded)"* ]]
}

@test "a gitignored Go file does not block verification" {
  # Ignored files are never committed, so they cannot cause the local/CI
  # divergence the guard exists for — and gitignoring is the documented way
  # to keep a scratch .go file while running the tool.
  echo 'scratch.go' > .gitignore
  git add .gitignore && git commit -qm ignore
  echo 'package scratch' > scratch.go
  stub_docker "NIX_BUILD_EXIT=0"

  run scripts/update-nix-flake.sh 0.1.1
  [ "$status" -eq 2 ]
  [[ "$output" == *"verified (build succeeded)"* ]]
}

@test "fails when staging the tracked source fails" {
  # `git ls-files` lists a tracked file deleted from the worktree with plain
  # `rm`, so the staging tar exits nonzero while still emitting the readable
  # files — a partial tree. Left unchecked (errexit is stripped inside the
  # $(run_nix_build) substitution), the build runs against that partial source
  # and can report "verified" for a tree that is not what CI will build, or
  # compute a vendorHash from a partial import graph. The script must abort
  # before invoking Docker.
  echo 'tracked' > notes.md
  git add notes.md && git commit -qm notes
  rm notes.md
  # Volume lifecycle calls are expected, but the build itself must not run.
  cat > "$STUB_DIR/docker" <<'STUB'
#!/usr/bin/env bash
if [ "${1:-}" = volume ] && [ "${2:-}" = create ]; then
  echo test-nix-store
  exit 0
fi
if [ "${1:-}" = volume ] && [ "${2:-}" = rm ]; then
  exit 0
fi
echo "error: build ran despite a failed staging pipeline"
echo "NIX_BUILD_EXIT=0"
STUB
  chmod +x "$STUB_DIR/docker"

  run scripts/update-nix-flake.sh 0.2.0
  [ "$status" -eq 1 ]
  [[ "$output" == *"staging tracked files for the build failed"* ]]
  [[ "$output" != *"build ran despite"* ]]
  [[ "$output" != *"verified (build succeeded)"* ]]
  # The version edit precedes the build; the failure must roll it back.
  grep -q 'version = "0.1.1";' nix/package.nix
  ! grep -q 'version = "0.2.0";' nix/package.nix
}
