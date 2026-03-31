#!/usr/bin/env bash
# Usage: scripts/release.sh VERSION [--dry-run]
#   VERSION: semver with v prefix (e.g. v1.0.0)
#
# Validates, tags, and pushes to trigger the release workflow.

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

if [ -z "$VERSION" ]; then
  echo "Usage: scripts/release.sh VERSION [--dry-run]"
  echo "       VERSION=v1.0.0 make release"
  exit 1
fi

# Validate semver
if ! echo "$VERSION" | grep -qE '^v[0-9]+\.[0-9]+\.[0-9]+(-[a-zA-Z0-9.]+)?$'; then
  die "Invalid version '$VERSION' (expected vX.Y.Z or vX.Y.Z-suffix)"
fi

if [[ "$DRY_RUN" == "1" || "$DRY_RUN" == "true" ]]; then
  info "Dry run — no tags will be created or pushed"
  echo ""
fi

# Detect default branch
DEFAULT_BRANCH=$(git remote show origin 2>/dev/null | sed -n 's/.*HEAD branch: //p')
DEFAULT_BRANCH="${DEFAULT_BRANCH:-main}"

# Verify on default branch
CURRENT_BRANCH=$(git rev-parse --abbrev-ref HEAD)
if [ "$CURRENT_BRANCH" != "$DEFAULT_BRANCH" ]; then
  die "Not on $DEFAULT_BRANCH (currently on $CURRENT_BRANCH)"
fi

# Clean working tree
if [ -n "$(git status --porcelain)" ]; then
  die "Working tree is not clean"
fi

# Synced with remote
git fetch origin "$DEFAULT_BRANCH" --quiet
LOCAL=$(git rev-parse HEAD)
REMOTE=$(git rev-parse "origin/$DEFAULT_BRANCH")
if [ "$LOCAL" != "$REMOTE" ]; then
  die "Local $DEFAULT_BRANCH (${LOCAL:0:7}) is not synced with origin (${REMOTE:0:7}). Pull or push first."
fi

# No replace directives
if grep -q '^replace' go.mod; then
  die "go.mod contains replace directives. Remove them before releasing."
fi

# Verify required tools
if ! command -v jq >/dev/null 2>&1; then
  die "jq is required but not found. Install with your package manager."
fi

# --- Run pre-flight checks ---
info "Running release checks"
info "  Branch: $CURRENT_BRANCH"
info "  Commit: ${LOCAL:0:7}"

if [[ "$DRY_RUN" == "1" || "$DRY_RUN" == "true" ]]; then
  echo ""
  info "Running release-check..."
  make release-check
  echo ""
  info "Dry run complete. No tag created."
  exit 0
fi

echo ""
info "Running release-check..."
make release-check

# --- Fetch tags to ensure we see remote state ---
git fetch origin --tags --quiet

# --- Handle tag ---
if git rev-parse "$VERSION" >/dev/null 2>&1; then
  EXISTING_SHA=$(git rev-parse "${VERSION}^{commit}")
  if [[ "$EXISTING_SHA" == "$LOCAL" ]]; then
    info "Tag $VERSION already exists at HEAD"
  else
    die "Tag $VERSION already exists at ${EXISTING_SHA:0:7} (not HEAD). Delete it first or choose a different version."
  fi
else
  echo ""
  info "Creating tag $VERSION..."
  git tag -a "$VERSION" -m "Release $VERSION"
fi

info "Pushing $VERSION to origin..."
git push origin "$VERSION"

echo ""
info "Released $VERSION"
echo ""
echo "  Actions: https://github.com/basecamp/hey-cli/actions"
echo "  Release: https://github.com/basecamp/hey-cli/releases/tag/$VERSION"
