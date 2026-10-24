#!/usr/bin/env bash
# Usage: scripts/release.sh VERSION [--dry-run]
#   VERSION: semver, with or without the v prefix (0.2.0, v0.2.0, 0.2.0-rc.1)
#
# Validates, then tags and pushes the tag to trigger the release workflow. Set
# DRY_RUN=1 (or pass --dry-run) to run the checks only.
#
# A stable release ships its version in nix/package.nix and
# .claude-plugin/plugin.json, and main takes changes only through pull
# requests, so a stable release is two runs. The first finds the metadata
# unstamped, stamps it on release/vX.Y.Z, opens a PR and stops. Once that PR is
# merged, the second run from main finds the metadata stamped and pushes only
# the tag. The script never pushes to main.

set -euo pipefail

# --- Colors ---
RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[0;33m'
BOLD='\033[1m'
RESET='\033[0m'

info()  { echo -e "${GREEN}==>${RESET} ${BOLD}$*${RESET}"; }
warn()  { echo -e "${YELLOW}WARNING:${RESET} $*"; }
error() { echo -e "${RED}ERROR:${RESET} $*" >&2; }
die()   { error "$@"; exit 1; }

# --- Args ---
usage() {
  echo "Usage: scripts/release.sh VERSION [--dry-run]"
  echo "       make release VERSION=0.2.0 [DRY_RUN=1]"
}

if [[ $# -eq 0 ]]; then
  usage
  exit 1
fi
if [[ $# -gt 2 ]]; then
  die "Unexpected arguments (expected VERSION [--dry-run])"
fi

VERSION="$1"
# Capture the release switch under its own name, then keep the generic DRY_RUN
# variable out of release-check's environment. Individual checks use DRY_RUN for
# their own interfaces, with values such as "local" and "remote".
release_dry_run_input="${DRY_RUN:-}"
case "${2:-}" in
  "") ;;
  --dry-run) release_dry_run_input=1 ;;
  *) die "Unknown argument: '$2' (expected --dry-run)" ;;
esac
unset DRY_RUN
case "$release_dry_run_input" in
  ""|0|false) RELEASE_DRY_RUN=0 ;;
  1|true) RELEASE_DRY_RUN=1 ;;
  *) die "Invalid release DRY_RUN value: '$release_dry_run_input' (expected 0, 1, false or true)" ;;
esac
readonly RELEASE_DRY_RUN

if [[ "$VERSION" == "dev" ]]; then
  usage
  exit 1
fi

# --- Normalise and validate the version ---
VERSION="${VERSION#v}"
# Leading zeros are refused (semver forbids them, and bash arithmetic would
# read 08 as broken octal in the version comparison below).
if [[ ! "$VERSION" =~ ^(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)(-[a-zA-Z0-9.]+)?$ ]]; then
  die "Invalid version '${VERSION}' (expected X.Y.Z or X.Y.Z-suffix without leading zeros, optionally v-prefixed)"
fi

TAG="v${VERSION}"
PRERELEASE=0
if [[ "$VERSION" == *-* ]]; then
  PRERELEASE=1
fi

if [[ "$RELEASE_DRY_RUN" -eq 1 ]]; then
  info "Dry run — no commits, tags or pushes"
  echo ""
fi

# --- Verify branch ---
DEFAULT_BRANCH=$(git remote show origin 2>/dev/null | sed -n 's/.*HEAD branch: //p')
DEFAULT_BRANCH="${DEFAULT_BRANCH:-main}"
BRANCH=$(git rev-parse --abbrev-ref HEAD)
if [[ "$BRANCH" != "$DEFAULT_BRANCH" ]]; then
  die "Not on $DEFAULT_BRANCH (currently on $BRANCH)"
fi

# --- Verify clean tree ---
if [[ -n "$(git status --porcelain)" ]]; then
  die "Working tree is not clean. Commit or stash changes first."
fi

# From here on the tree is known clean, so on failure the stable metadata
# files can be checked out to undo any half-made edits — a failed Nix version
# or plugin stamp must not leave a dirty tree that blocks the next attempt.
# Checkout from HEAD, not the index: a failed release commit (hook, signing)
# leaves the files staged, and a plain `git checkout --` would restore the
# staged copies and leave the index dirty. Once the release prep commit
# lands, HEAD contains the new metadata and the checkout is a no-op. A failure
# while preparing the release PR also puts the clone back on the default
# branch, where the next attempt starts.
restore_release_metadata() {
  if [[ "${1:-1}" -ne 0 ]]; then
    git checkout --quiet HEAD -- nix/package.nix .claude-plugin/plugin.json || true
    if [[ "$(git rev-parse --abbrev-ref HEAD)" != "$DEFAULT_BRANCH" ]]; then
      git switch --quiet "$DEFAULT_BRANCH" || true
    fi
  fi
}
trap 'restore_release_metadata "$?"' EXIT

# --- Verify synced with remote ---
git fetch origin "$DEFAULT_BRANCH" --quiet
LOCAL=$(git rev-parse HEAD)
REMOTE=$(git rev-parse "origin/$DEFAULT_BRANCH")
if [[ "$LOCAL" != "$REMOTE" ]]; then
  die "Local $DEFAULT_BRANCH (${LOCAL:0:7}) is not synced with origin (${REMOTE:0:7}). Pull or push first."
fi

# --- Verify no replace directives ---
if grep -q '^[[:space:]]*replace[[:space:]]' go.mod; then
  die "go.mod contains replace directives. Remove them before releasing."
fi

# --- Verify required tools ---
if ! command -v jq >/dev/null 2>&1; then
  die "jq is required but not found. Install with your package manager."
fi

# --- Validate the tag before anything mutates ---
# Everything below this point that pushes (the release PR's branch, the tag)
# happens after the checks, so a release that is going to be refused must be
# refused here, before anything reaches origin.
git fetch origin --tags --quiet
if git rev-parse -q --verify "refs/tags/${TAG}^{commit}" >/dev/null; then
  EXISTING_SHA=$(git rev-parse "refs/tags/${TAG}^{commit}")
  if [[ "$EXISTING_SHA" != "$LOCAL" ]]; then
    # Never suggest moving a tag: proxy.golang.org caches tags as it first saw
    # them, so a moved tag is at best ignored and at worst a checksum mismatch
    # for everyone who fetched the original.
    die "Tag $TAG already exists at ${EXISTING_SHA:0:7} (not HEAD). Published tags are cached by the Go module proxy and must not move — choose a new version."
  fi
  # Tag at HEAD: only re-runnable if HEAD already carries the stable metadata.
  # A hand-pushed tag at an unstamped commit must be refused HERE: the release
  # PR would stamp a commit the tag does not point at, and the operator would
  # be stuck with a proxy-cached tag that cannot be reused.
  if [[ "$PRERELEASE" -eq 0 ]]; then
    STAMPED_NIX=$(sed -n 's/.*version = "\([^"]*\)".*/\1/p' nix/package.nix | head -1)
    STAMPED_PLUGIN=$(jq -r .version .claude-plugin/plugin.json)
    if [[ "$STAMPED_NIX" != "$VERSION" || "$STAMPED_PLUGIN" != "$VERSION" ]]; then
      die "Tag $TAG already exists at HEAD, but HEAD's release metadata is not stamped for it (nix ${STAMPED_NIX}, plugin ${STAMPED_PLUGIN}). The tag may already be cached by the Go module proxy — choose a new version."
    fi
  fi
fi

# A stable version below the latest stable tag would roll the Nix flake and
# plugin metadata on main backwards, and sync-skills would mirror the older
# tree. Compare numerically, never lexically — and never with sort -V, which
# macOS's stock sort does not provide (same reasoning as check-lint-lockstep.sh).
# Git's version:refname ordering picks the latest stable tag, and a field
# compare orders it against the new version; both sides are stable X.Y.Z here.
# Force base 10: the requested version is validated against leading zeros
# above, but an already-pushed tag like v1.09.0 is not, and bash would
# otherwise read its fields as octal and error the comparison into a no.
version_lt() {
  local -a a b
  local i
  IFS=. read -r -a a <<< "${1#v}"
  IFS=. read -r -a b <<< "${2#v}"
  for i in 0 1 2; do
    if (( 10#${a[i]:-0} < 10#${b[i]:-0} )); then return 0; fi
    if (( 10#${a[i]:-0} > 10#${b[i]:-0} )); then return 1; fi
  done
  return 1
}

if [[ "$PRERELEASE" -eq 0 ]]; then
  LATEST_STABLE=$(git tag --list 'v[0-9]*.[0-9]*.[0-9]*' --sort=-version:refname | awk '!/-/ { print; exit }')
  if [[ -n "$LATEST_STABLE" && "$LATEST_STABLE" != "$TAG" ]] && version_lt "$TAG" "$LATEST_STABLE"; then
    die "Version $VERSION is older than the latest stable release ${LATEST_STABLE#v}. Stable releases cannot go backwards."
  fi
fi

# --- Run pre-flight checks ---
info "Running release checks"
info "  Branch: $BRANCH"
info "  Commit: ${LOCAL:0:7}"
info "  Tag:    $TAG"
echo ""
# A variable assigned on `make release` also travels through MAKEFLAGS into the
# nested make. The explicit empty command-line override wins over that inherited
# assignment; unsetting the shell variable alone does not.
make DRY_RUN= release-check

# --- Stable release metadata ---
# Prereleases leave the Nix flake and plugin metadata on the latest stable
# version: those channels only ever point at stable. A stable release needs
# both stamped with its version in the commit it tags, and main takes changes
# only through pull requests, so unstamped metadata means the first run: stamp
# it on a release branch, open the PR, and stop until it is merged.
metadata_stamped() {
  local nix plugin
  nix=$(sed -n 's/.*version = "\([^"]*\)".*/\1/p' nix/package.nix | head -1)
  plugin=$(jq -r .version .claude-plugin/plugin.json)
  [[ "$nix" == "$VERSION" && "$plugin" == "$VERSION" ]]
}

next_steps() {
  echo ""
  echo "  1. Merge $1"
  echo "  2. Pull $DEFAULT_BRANCH and run: make release VERSION=$VERSION"
  echo "     It finds the metadata stamped and pushes only the tag."
}

open_release_pr() {
  local branch="release/${TAG}" existing url
  command -v gh >/dev/null 2>&1 || die "gh is required to open the release PR (https://cli.github.com)."

  existing=$(gh pr list --head "$branch" --base "$DEFAULT_BRANCH" --state open --json url --jq '.[0].url // empty')
  if [[ -n "$existing" ]]; then
    info "The release PR for $TAG is already open: $existing"
    next_steps "$existing"
    return
  fi
  # A branch on origin with no open PR is somebody's earlier attempt. Pushing
  # over it could discard their work, so say what is there instead.
  if [[ -n "$(git ls-remote --heads origin "$branch")" ]]; then
    die "origin already has $branch but no open PR from it. Open a PR from it, or delete it (git push origin --delete $branch) and re-run."
  fi

  info "Stamping release metadata on $branch"
  git switch --quiet -C "$branch"
  scripts/stamp-nix-version.sh "$VERSION"
  scripts/stamp-plugin-version.sh "$VERSION"
  git add nix/package.nix .claude-plugin/plugin.json
  git commit --quiet -m "Update nix flake and plugin version for ${TAG}"
  git push --quiet --set-upstream origin "$branch"

  url=$(gh pr create --base "$DEFAULT_BRANCH" --head "$branch" --label release \
    --title "Prepare ${TAG} release" \
    --body "Stamps \`nix/package.nix\` and \`.claude-plugin/plugin.json\` with ${VERSION} for ${TAG}.

Opened by \`scripts/release.sh\`: \`${DEFAULT_BRANCH}\` takes changes only through pull requests, and a stable tag must point at a commit that carries its version. Once this is merged, running \`make release VERSION=${VERSION}\` again from an up-to-date \`${DEFAULT_BRANCH}\` finds the metadata stamped and pushes only the tag.")
  git switch --quiet "$DEFAULT_BRANCH"
  info "Opened the release PR: $url"
  next_steps "$url"
}

if [[ "$PRERELEASE" -eq 1 ]]; then
  info "Skipping stable release metadata for prerelease"
  echo "  nix flake: unchanged"
  echo "  Claude plugin metadata: unchanged"
elif ! metadata_stamped; then
  if [[ "$RELEASE_DRY_RUN" -eq 1 ]]; then
    info "Release metadata is not stamped for $VERSION"
    echo "  A real run stamps it on release/${TAG}, opens the release PR and stops;"
    echo "  the tag is pushed by the run after that PR is merged."
    echo ""
    info "Dry run complete. No branch, PR or tag created."
    exit 0
  fi
  open_release_pr
  exit 0
else
  info "Release metadata is stamped for $VERSION"
fi

if [[ "$RELEASE_DRY_RUN" -eq 1 ]]; then
  echo ""
  info "Dry run complete. No tag created."
  exit 0
fi

# --- Handle tag ---
if git rev-parse -q --verify "refs/tags/${TAG}^{commit}" >/dev/null; then
  info "Tag $TAG already exists at HEAD"
else
  info "Creating tag $TAG"
  git tag -a "$TAG" -m "Release $TAG"
fi

# Only the tag is pushed. The tag check ran before the release checks, so
# another operator can push the same tag in between; on any rejection the
# local tag goes, leaving the clone where a re-run (under a new version, if
# the tag was taken) can start.
info "Pushing $TAG to origin"
if ! push_output=$(git push origin "refs/tags/${TAG}" 2>&1); then
  echo "$push_output" >&2
  git tag -d "$TAG" >/dev/null
  git fetch origin --tags --quiet || true
  if grep -q -E 'GH013|rule violations' <<<"$push_output"; then
    die "The push of $TAG was refused by the repository rules (above). Nothing was pushed and the local tag was removed."
  fi
  die "The push of $TAG was rejected (above). Nothing was pushed and the local tag was removed. If $TAG now exists on origin, choose a new version."
fi
[[ -z "$push_output" ]] || echo "$push_output"

echo ""
info "Release $TAG triggered"
echo ""
echo "  Actions: https://github.com/basecamp/hey-cli/actions"
echo "  Release: https://github.com/basecamp/hey-cli/releases/tag/$TAG"
