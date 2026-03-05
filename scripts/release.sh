#!/usr/bin/env bash
set -euo pipefail

VERSION="${1:-${VERSION:-}}"
DRY_RUN="${DRY_RUN:-0}"

if [ -z "$VERSION" ]; then
  echo "Usage: scripts/release.sh VERSION"
  echo "       VERSION=v1.0.0 make release"
  exit 1
fi

# Validate semver
if ! echo "$VERSION" | grep -qE '^v[0-9]+\.[0-9]+\.[0-9]+(-[a-zA-Z0-9.]+)?$'; then
  echo "ERROR: Invalid version '$VERSION' (expected vX.Y.Z or vX.Y.Z-suffix)"
  exit 1
fi

# Detect default branch
DEFAULT_BRANCH=$(git remote show origin 2>/dev/null | sed -n 's/.*HEAD branch: //p')
DEFAULT_BRANCH="${DEFAULT_BRANCH:-main}"

# Verify on default branch
CURRENT_BRANCH=$(git rev-parse --abbrev-ref HEAD)
if [ "$CURRENT_BRANCH" != "$DEFAULT_BRANCH" ]; then
  echo "ERROR: Not on $DEFAULT_BRANCH (currently on $CURRENT_BRANCH)"
  exit 1
fi

# Clean working tree
if [ -n "$(git status --porcelain)" ]; then
  echo "ERROR: Working tree is not clean"
  git status --short
  exit 1
fi

# Synced with remote
git fetch origin "$DEFAULT_BRANCH" --quiet
LOCAL=$(git rev-parse HEAD)
REMOTE=$(git rev-parse "origin/$DEFAULT_BRANCH")
if [ "$LOCAL" != "$REMOTE" ]; then
  echo "ERROR: Local $DEFAULT_BRANCH is not in sync with origin"
  echo "  Local:  $LOCAL"
  echo "  Remote: $REMOTE"
  exit 1
fi

# No replace directives
if grep -q '^replace' go.mod; then
  echo "ERROR: go.mod contains replace directives"
  grep '^replace' go.mod
  exit 1
fi

echo "Release preflight for $VERSION"
echo "  Branch: $CURRENT_BRANCH"
echo "  Commit: $LOCAL"

if [ "$DRY_RUN" = "1" ]; then
  echo ""
  echo "DRY RUN: Would run 'make release-check', tag $VERSION, and push"
  echo "Running release-check..."
  make release-check
  echo ""
  echo "DRY RUN complete. No tag created."
  exit 0
fi

echo ""
echo "Running release-check..."
make release-check

echo ""
echo "Creating tag $VERSION..."
git tag -a "$VERSION" -m "Release $VERSION"
git push origin "$VERSION"

echo ""
echo "Released $VERSION"
echo "GitHub Actions will build and publish the release."
