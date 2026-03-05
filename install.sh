#!/bin/sh
# Install script for hey CLI
# Usage: curl -fsSL https://raw.githubusercontent.com/basecamp/hey-cli/main/install.sh | sh
set -eu

REPO="basecamp/hey-cli"
BINARY="hey"
INSTALL_DIR="${INSTALL_DIR:-/usr/local/bin}"

# Detect platform
OS="$(uname -s | tr '[:upper:]' '[:lower:]')"
ARCH="$(uname -m)"

case "$ARCH" in
  x86_64|amd64) ARCH="amd64" ;;
  aarch64|arm64) ARCH="arm64" ;;
  *) echo "Unsupported architecture: $ARCH" >&2; exit 1 ;;
esac

case "$OS" in
  darwin) OS="darwin" ;;
  linux) OS="linux" ;;
  *) echo "Unsupported OS: $OS" >&2; exit 1 ;;
esac

# macOS uses universal binary
if [ "$OS" = "darwin" ]; then
  ARCH="all"
fi

# Get latest release
LATEST=$(curl -fsSL "https://api.github.com/repos/$REPO/releases/latest" | grep '"tag_name"' | sed -E 's/.*"v?([^"]+)".*/\1/')
if [ -z "$LATEST" ]; then
  echo "Could not determine latest release" >&2
  exit 1
fi

ARCHIVE="${BINARY}_${LATEST}_${OS}_${ARCH}.tar.gz"
URL="https://github.com/$REPO/releases/download/v${LATEST}/${ARCHIVE}"
CHECKSUM_URL="https://github.com/$REPO/releases/download/v${LATEST}/checksums.txt"

TMPDIR="$(mktemp -d)"
trap 'rm -rf "$TMPDIR"' EXIT

echo "Downloading $BINARY v$LATEST for $OS/$ARCH..."
curl -fsSL "$URL" -o "$TMPDIR/$ARCHIVE"

# Verify checksum
echo "Verifying checksum..."
curl -fsSL "$CHECKSUM_URL" -o "$TMPDIR/checksums.txt"
EXPECTED=$(grep -F -- "$ARCHIVE" "$TMPDIR/checksums.txt" | awk '{print $1}')
if [ -z "$EXPECTED" ]; then
  echo "Checksum for $ARCHIVE not found in checksums.txt" >&2
  exit 1
fi
if [ -n "$EXPECTED" ]; then
  if command -v sha256sum >/dev/null 2>&1; then
    ACTUAL=$(sha256sum "$TMPDIR/$ARCHIVE" | awk '{print $1}')
  elif command -v shasum >/dev/null 2>&1; then
    ACTUAL=$(shasum -a 256 "$TMPDIR/$ARCHIVE" | awk '{print $1}')
  else
    echo "Warning: no sha256sum or shasum available, skipping checksum verification" >&2
    ACTUAL="$EXPECTED"
  fi

  if [ "$ACTUAL" != "$EXPECTED" ]; then
    echo "Checksum mismatch!" >&2
    echo "Expected: $EXPECTED" >&2
    echo "Got:      $ACTUAL" >&2
    exit 1
  fi
fi

# Extract and install
tar -xzf "$TMPDIR/$ARCHIVE" -C "$TMPDIR"
if [ -w "$INSTALL_DIR" ]; then
  install "$TMPDIR/$BINARY" "$INSTALL_DIR/$BINARY"
else
  echo "Installing to $INSTALL_DIR (requires sudo)..."
  sudo install "$TMPDIR/$BINARY" "$INSTALL_DIR/$BINARY"
fi

echo "$BINARY v$LATEST installed to $INSTALL_DIR/$BINARY"
