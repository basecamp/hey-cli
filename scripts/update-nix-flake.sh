#!/usr/bin/env bash
# Updates nix/package.nix version and recomputes vendorHash when go.mod changes.
# Usage: scripts/update-nix-flake.sh VERSION
#
# Exit codes:
#   0 — changes made
#   2 — no changes needed

set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"

VERSION="${1:-}"
if [[ -z "$VERSION" ]]; then
  echo "Usage: scripts/update-nix-flake.sh VERSION"
  exit 1
fi

NIX_PKG="nix/package.nix"
CHANGED=false
NIX_STORE_VOLUME=""
NIX_BUILD_LOG=""

# The build source below is staged from tracked files only, so an untracked
# .go file never reaches the verification build — but once committed it does
# reach CI's build, and an import it adds shifts `go mod vendor`'s output and
# with it the vendorHash. Verifying without it would stamp "verified" on a
# hash CI then rejects. Refuse to verify a tree that diverges from what will
# be committed, rather than silently verifying the wrong one. Scoped to files
# that feed the Go build graph: untracked notes or scratch dirs cannot move
# the vendorHash and must not block the tool.
UNTRACKED_GO=$(git ls-files --others --exclude-standard -- \
  '*.go' 'go.mod' 'go.sum' 'go.work' 'go.work.sum')
if [[ -n "$UNTRACKED_GO" ]]; then
  echo "ERROR: untracked Go source would be missing from the verified build:"
  while IFS= read -r f; do echo "  $f"; done <<<"$UNTRACKED_GO"
  echo "The build stages tracked files only, so the vendorHash verified here"
  echo "would not match what CI computes once these files are committed."
  echo "\`git add\` them (or gitignore them) and re-run."
  exit 1
fi

# Leave no partial edits behind: restore nix/package.nix on any failure so a
# failed run is indistinguishable from no run. Otherwise a leftover version
# bump makes the next invocation report "no changes needed" (exit 2) while an
# unverified edit sits in the worktree waiting to be committed.
ORIG_PKG="$(mktemp)"
cp "$NIX_PKG" "$ORIG_PKG"
# shellcheck disable=SC2329  # invoked via the EXIT trap below
cleanup() {
  local status=$?

  # Each invocation gets its own Docker volume, but the two builds needed for
  # a stale vendorHash share it. Remove it on every exit so this optimization
  # cannot become a persistent repo- or user-level cache. A cleanup failure is
  # a failed run: otherwise the script would silently leave a large Nix store
  # behind while claiming success.
  if [[ -n "$NIX_STORE_VOLUME" ]] && \
     ! docker volume rm "$NIX_STORE_VOLUME" >/dev/null 2>&1; then
    echo "ERROR: could not remove temporary Nix store volume: $NIX_STORE_VOLUME" >&2
    status=1
  fi

  if [[ -n "$NIX_BUILD_LOG" ]] && ! rm -f "$NIX_BUILD_LOG"; then
    echo "ERROR: could not remove temporary Nix build log: $NIX_BUILD_LOG" >&2
    status=1
  fi

  if [[ $status -ne 0 && $status -ne 2 ]]; then
    cp "$ORIG_PKG" "$NIX_PKG"
  fi
  rm -f "$ORIG_PKG"

  # Preserve the triggering status across cleanup commands, including the
  # script's documented exit 2 for an already-current package.
  trap - EXIT
  exit "$status"
}
trap cleanup EXIT

# --- Update version ---
CURRENT_VERSION=$(sed -n 's/.*version = "\([^"]*\)".*/\1/p' "$NIX_PKG" | head -1)
if [[ "$CURRENT_VERSION" != "$VERSION" ]]; then
  sed -i.bak "s/version = \"${CURRENT_VERSION}\"/version = \"${VERSION}\"/" "$NIX_PKG"
  rm -f "${NIX_PKG}.bak"
  CHANGED=true
  echo "  nix version: ${CURRENT_VERSION} → ${VERSION}"
fi

# --- Report whether dependencies moved ---
# Prerelease tags skip Nix metadata updates, so compare dependency changes
# against the latest stable tag instead of an intervening RC tag.
#
# This no longer decides *whether* to build — only what to say about it. The
# build is unconditional: v0.8.0's flake was broken by a Go toolchain bump
# outpacing flake.lock, which changes neither go.mod nor go.sum, so a
# deps-changed gate would have skipped the very check that catches it.
PREV_TAG=$(git tag --merged HEAD --sort=-version:refname --list 'v[0-9]*.[0-9]*.[0-9]*' | awk '!/-/ { print; exit }')
DEPS_CHANGED=false
if [[ -z "$PREV_TAG" ]]; then
  DEPS_CHANGED=true
elif ! git diff --quiet "$PREV_TAG"..HEAD -- go.mod go.sum 2>/dev/null; then
  DEPS_CHANGED=true
fi

if ! command -v docker &>/dev/null; then
  echo "ERROR: Docker unavailable — cannot verify the Nix build"
  echo "Install Docker and re-run; a release must not ship an unverified flake."
  exit 1
fi

if [[ "$DEPS_CHANGED" == "true" ]]; then
  echo "  dependencies changed — verifying the Nix build via Docker..."
else
  echo "  dependencies unchanged — verifying the Nix build anyway..."
fi

# Pin image digest for supply-chain integrity. Update periodically:
#   docker pull nixos/nix && docker inspect nixos/nix:latest --format '{{index .RepoDigests 0}}'
NIX_IMAGE="${NIX_IMAGE:-nixos/nix@sha256:b9c9611c8530fa8049a1215b20638536e1e71dcaf85212e47845112caf3adeea}"

# A Docker volume is much faster than a macOS bind mount for a Nix store.
# Docker chooses a collision-free name, so separate updater runs remain cold
# and isolated; cleanup removes it whether the run succeeds or fails. Create
# it explicitly so a daemon failure cannot be mistaken for a volume that then
# also fails cleanup.
if ! NIX_STORE_VOLUME=$(docker volume create \
  --label com.37signals.hey-cli.temporary=update-nix-flake); then
  echo "ERROR: could not create a temporary Nix store volume"
  exit 1
fi
if [[ -z "$NIX_STORE_VOLUME" ]]; then
  echo "ERROR: Docker returned no name for the temporary Nix store volume"
  exit 1
fi
NIX_BUILD_LOG=$(mktemp)

# Runs `nix build`, streams its combined output, and records the same bytes in
# the file passed as $1. The trailing NIX_BUILD_EXIT= line keeps the Nix status
# available for fail-closed validation after the live stream finishes. Nothing
# here may swallow a failure: a `|| true` plus a "did it print 'building hey'"
# heuristic is what let
# v0.8.0 ship a flake that could not build at all — nix logs
# `building '...hey-0.8.0-go-modules.drv'` when it *starts* the build it
# then fails, so the heuristic reported success on a hard failure.
# The build source is staged from tracked files only (at their working-tree
# content, so the version edit above is included). Mounting the checkout
# directly would sweep untracked files into the temporary flake source, where
# a scratch .go file adding an import could shift the computed vendorHash away
# from what a clean checkout produces — and CI would then reject the
# supposedly verified update. Staged fresh per call so the verification
# rebuild sees the vendorHash written between calls.
run_nix_build() {
  local output_file="$1" stage rc=0
  local -a pipeline_status

  if ! : > "$output_file"; then
    echo "ERROR: preparing the Nix build log failed" >&2
    return 1
  fi

  stage=$(mktemp -d)
  # The staging pipeline must be checked by hand.
  # tar still emits a partial archive — alongside a nonzero status — when a
  # tracked file is missing or unreadable (a plain `rm` without `git rm`, a
  # sparse checkout), and building that partial tree would verify source CI
  # never sees.
  if ! git ls-files -z | tar -cf - --null -T - | tar -xf - -C "$stage"; then
    rm -rf "$stage"
    echo "ERROR: staging tracked files for the build failed" >&2
    return 1
  fi
  # The pipeline is an if-condition so errexit cannot abort before PIPESTATUS
  # is captured. Docker's status remains authoritative; a tee failure also
  # fails the run when Docker itself succeeded because the complete log is a
  # correctness input for the sentinel and vendorHash classifier.
  if docker run --rm \
      -v "$stage:/src:ro" \
      -v "$NIX_STORE_VOLUME:/nix" \
      "$NIX_IMAGE" bash -c '
      cp -a /src /build && cd /build
      git config --global --add safe.directory /build
      git init -q && git add -A && \
        GIT_COMMITTER_NAME=ci GIT_COMMITTER_EMAIL=ci@ci \
        GIT_AUTHOR_NAME=ci GIT_AUTHOR_EMAIL=ci@ci \
        git commit -q -m init
      nix --extra-experimental-features "nix-command flakes" build \
        --no-link --print-build-logs 2>&1
      echo "NIX_BUILD_EXIT=$?"
    ' 2>&1 | tee "$output_file"; then
    pipeline_status=("${PIPESTATUS[@]}")
  else
    pipeline_status=("${PIPESTATUS[@]}")
  fi

  if [[ ${pipeline_status[0]} -ne 0 ]]; then
    rc=${pipeline_status[0]}
  elif [[ ${pipeline_status[1]} -ne 0 ]]; then
    echo "ERROR: recording the Nix build output failed" >&2
    rc=${pipeline_status[1]}
  fi
  rm -rf "$stage"
  return "$rc"
}

# The sentinel must be the LAST line: a `NIX_BUILD_EXIT=0` echoed anywhere
# earlier (a log line quoting this script, say) must not read as success.
nix_build_failed() {
  [[ "$(tail -n1 <<<"$1")" != "NIX_BUILD_EXIT=0" ]]
}

# The explicit failure branch surfaces the captured output when Docker itself
# fails (daemon down, image pull) while leaving the pipeline's Docker status
# authoritative.
run_nix_build "$NIX_BUILD_LOG" || {
  BUILD_OUTPUT=$(<"$NIX_BUILD_LOG")
  echo "ERROR: could not run the Nix build"
  tail -25 <<<"$BUILD_OUTPUT"
  exit 1
}
BUILD_OUTPUT=$(<"$NIX_BUILD_LOG")

if nix_build_failed "$BUILD_OUTPUT"; then
  # Classification lives in extract-nix-vendor-hash.sh, shared with CI's
  # nix-build-check.sh. It requires both a fixed-output mismatch diagnostic and
  # an SRI-shaped value before yielding anything — deliberately, because this
  # caller writes the result into a tracked file.
  NEW_HASH=$("$SCRIPT_DIR/extract-nix-vendor-hash.sh" <<<"$BUILD_OUTPUT" || true)

  if [[ -z "$NEW_HASH" ]]; then
    # The build broke for some other reason — a stale flake.lock whose Go is
    # older than go.mod's directive is the one that has actually bitten us.
    # Never continue from here, and never write to nix/package.nix.
    echo "ERROR: nix build failed, and not because of the vendorHash"
    tail -25 <<<"$BUILD_OUTPUT"
    exit 1
  fi

  CURRENT_HASH=$(sed -n 's/.*vendorHash = "\([^"]*\)".*/\1/p' "$NIX_PKG" | head -1)
  sed -i.bak "s|vendorHash = \"${CURRENT_HASH}\"|vendorHash = \"${NEW_HASH}\"|" "$NIX_PKG"
  rm -f "${NIX_PKG}.bak"
  CHANGED=true
  echo "  vendorHash: updated"

  # Prove the corrected hash actually builds. The old code trusted the hash it
  # had just written without ever rebuilding.
  echo "  verifying the updated vendorHash builds..."
  run_nix_build "$NIX_BUILD_LOG" || {
    BUILD_OUTPUT=$(<"$NIX_BUILD_LOG")
    echo "ERROR: could not run the Nix build"
    tail -25 <<<"$BUILD_OUTPUT"
    exit 1
  }
  BUILD_OUTPUT=$(<"$NIX_BUILD_LOG")
  if nix_build_failed "$BUILD_OUTPUT"; then
    echo "ERROR: nix build still fails after updating the vendorHash"
    tail -25 <<<"$BUILD_OUTPUT"
    exit 1
  fi
fi

echo "  vendorHash: verified (build succeeded)"

if [[ "$CHANGED" == "true" ]]; then
  exit 0
else
  exit 2
fi
