#!/usr/bin/env bash
# Usage: scripts/release.sh VERSION [--dry-run]
#   VERSION: semver, with or without the v prefix (0.2.0, v0.2.0, 0.2.0-rc.1)
#
# Validates, updates stable release metadata, tags, and pushes to trigger the
# release workflow. Set DRY_RUN=1 (or pass --dry-run) to run the checks only.

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
VERSION="${1:-${VERSION:-}}"
DRY_RUN="${DRY_RUN:-0}"
if [[ "$*" == *"--dry-run"* ]]; then
  DRY_RUN=1
fi
case "$DRY_RUN" in
  1|true) DRY_RUN=1 ;;
  *) DRY_RUN=0 ;;
esac

if [[ -z "$VERSION" || "$VERSION" == "dev" ]]; then
  echo "Usage: scripts/release.sh VERSION [--dry-run]"
  echo "       make release VERSION=0.2.0 [DRY_RUN=1]"
  exit 1
fi

# --- Normalise and validate the version ---
VERSION="${VERSION#v}"
if [[ ! "$VERSION" =~ ^[0-9]+\.[0-9]+\.[0-9]+(-[a-zA-Z0-9.]+)?$ ]]; then
  die "Invalid version '${VERSION}' (expected X.Y.Z or X.Y.Z-suffix, optionally v-prefixed)"
fi

TAG="v${VERSION}"
PRERELEASE=0
if [[ "$VERSION" == *-* ]]; then
  PRERELEASE=1
fi

if [[ "$DRY_RUN" -eq 1 ]]; then
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
# Everything below this point that touches main (the stable metadata commit)
# happens before the tag is created, so a release that is going to be refused
# must be refused here, while main is still untouched.
git fetch origin --tags --quiet
if git rev-parse -q --verify "refs/tags/${TAG}^{commit}" >/dev/null; then
  EXISTING_SHA=$(git rev-parse "refs/tags/${TAG}^{commit}")
  if [[ "$EXISTING_SHA" != "$LOCAL" ]]; then
    die "Tag $TAG already exists at ${EXISTING_SHA:0:7} (not HEAD). Delete it first or choose a different version."
  fi
fi

# A stable version below the latest stable tag would roll the Nix flake and
# plugin metadata on main backwards, and sync-skills would mirror the older
# tree. Compare with sort -V (GNU and BSD), never lexically.
if [[ "$PRERELEASE" -eq 0 ]]; then
  LATEST_STABLE=$(git tag --list 'v[0-9]*.[0-9]*.[0-9]*' | awk '!/-/' | sort -V | tail -1)
  if [[ -n "$LATEST_STABLE" && "$LATEST_STABLE" != "$TAG" ]]; then
    NEWEST=$(printf '%s\n%s\n' "$LATEST_STABLE" "$TAG" | sort -V | tail -1)
    if [[ "$NEWEST" != "$TAG" ]]; then
      die "Version $VERSION is older than the latest stable release ${LATEST_STABLE#v}. Stable releases cannot go backwards."
    fi
  fi
fi

# --- Run pre-flight checks ---
info "Running release checks"
info "  Branch: $BRANCH"
info "  Commit: ${LOCAL:0:7}"
info "  Tag:    $TAG"
echo ""
make release-check

# --- Update stable release metadata ---
# Prereleases leave the Nix flake and plugin metadata on the latest stable
# version: those channels only ever point at stable.
if [[ "$PRERELEASE" -eq 1 ]]; then
  info "Skipping stable release metadata for prerelease"
  echo "  nix flake: unchanged"
  echo "  Claude plugin metadata: unchanged"
else
  info "Updating Nix flake"
  if [[ "$DRY_RUN" -eq 1 ]]; then
    echo "  (skipped — dry run)"
  else
    NIX_RC=0
    scripts/update-nix-flake.sh "$VERSION" || NIX_RC=$?
    if [[ "$NIX_RC" -eq 0 ]]; then
      : # nix flake updated
    elif [[ "$NIX_RC" -eq 2 ]]; then
      echo "  nix flake: no changes needed"
    else
      die "scripts/update-nix-flake.sh failed (exit $NIX_RC)"
    fi
  fi

  info "Stamping plugin version"
  if [[ "$DRY_RUN" -eq 1 ]]; then
    echo "  (skipped — dry run)"
  else
    scripts/stamp-plugin-version.sh "$VERSION"
  fi
fi

# --- Commit release prep ---
if [[ "$PRERELEASE" -eq 0 && "$DRY_RUN" -eq 0 ]]; then
  git add nix/package.nix .claude-plugin/plugin.json
  if ! git diff --cached --quiet; then
    STAGED=$(git diff --cached --name-only)
    HAS_NIX=0
    HAS_PLUGIN=0
    grep -q '^nix/package\.nix$' <<<"$STAGED" && HAS_NIX=1
    grep -q '^\.claude-plugin/plugin\.json$' <<<"$STAGED" && HAS_PLUGIN=1
    if [[ "$HAS_NIX" -eq 1 && "$HAS_PLUGIN" -eq 1 ]]; then
      COMMIT_MSG="Update nix flake and plugin version for ${TAG}"
    elif [[ "$HAS_NIX" -eq 1 ]]; then
      COMMIT_MSG="Update nix flake for ${TAG}"
    else
      COMMIT_MSG="Update plugin version for ${TAG}"
    fi
    git commit -m "$COMMIT_MSG"
    git push origin "$DEFAULT_BRANCH" --quiet
    LOCAL=$(git rev-parse HEAD)
    info "Pushed release prep (${LOCAL:0:7})"
  fi
fi

if [[ "$DRY_RUN" -eq 1 ]]; then
  echo ""
  info "Dry run complete. No tag created."
  exit 0
fi

# --- Handle tag ---
# Validated above against the pre-release-prep HEAD; the only way it can still
# disagree is a tag that already existed at an unstamped commit.
if git rev-parse -q --verify "refs/tags/${TAG}^{commit}" >/dev/null; then
  EXISTING_SHA=$(git rev-parse "refs/tags/${TAG}^{commit}")
  if [[ "$EXISTING_SHA" == "$LOCAL" ]]; then
    info "Tag $TAG already exists at HEAD"
  else
    die "Tag $TAG exists at ${EXISTING_SHA:0:7} but the release prep commit moved HEAD to ${LOCAL:0:7}. Delete the tag and re-run."
  fi
else
  info "Creating tag $TAG"
  git tag -a "$TAG" -m "Release $TAG"
fi

info "Pushing $TAG to origin"
git push origin "$TAG"

echo ""
info "Release $TAG triggered"
echo ""
echo "  Actions: https://github.com/basecamp/hey-cli/actions"
echo "  Release: https://github.com/basecamp/hey-cli/releases/tag/$TAG"
