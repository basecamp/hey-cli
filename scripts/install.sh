#!/usr/bin/env bash
set -euo pipefail

REPO="basecamp/hey-cli"
INSTALL_DIR="${HEY_BIN_DIR:-$HOME/.local/bin}"

# Detect OS and architecture
OS=$(uname -s | tr '[:upper:]' '[:lower:]')
ARCH=$(uname -m)

case "$ARCH" in
  x86_64|amd64) ARCH="amd64" ;;
  aarch64|arm64) ARCH="arm64" ;;
  *) echo "Unsupported architecture: $ARCH"; exit 1 ;;
esac

case "$OS" in
  linux|darwin) ;;
  mingw*|msys*|cygwin*) OS="windows" ;;
  *) echo "Unsupported OS: $OS"; exit 1 ;;
esac

# Fetch latest version
echo "Fetching latest version..."
VERSION=$(curl -sI "https://github.com/$REPO/releases/latest" | grep -i '^location:' | sed 's/.*tag\///' | tr -d '\r\n')
if [ -z "$VERSION" ]; then
  echo "Failed to determine latest version"
  exit 1
fi
echo "Latest version: $VERSION"

# Download archive
EXT="tar.gz"
if [ "$OS" = "windows" ]; then
  EXT="zip"
fi

ARCHIVE="hey_${VERSION#v}_${OS}_${ARCH}.${EXT}"
DOWNLOAD_URL="https://github.com/$REPO/releases/download/${VERSION}/${ARCHIVE}"
CHECKSUMS_URL="https://github.com/$REPO/releases/download/${VERSION}/checksums.txt"

TMPDIR=$(mktemp -d)
trap 'rm -rf "$TMPDIR"' EXIT

echo "Downloading $ARCHIVE..."
curl -fsSL "$DOWNLOAD_URL" -o "$TMPDIR/$ARCHIVE"
curl -fsSL "$CHECKSUMS_URL" -o "$TMPDIR/checksums.txt"

# Verify SHA256
echo "Verifying checksum..."
cd "$TMPDIR"
EXPECTED=$(awk -v f="$ARCHIVE" '$2 == f {print $1}' checksums.txt)
if [ -z "$EXPECTED" ]; then
  echo "ERROR: Archive not found in checksums file"
  exit 1
else
  ACTUAL=$(sha256sum "$ARCHIVE" 2>/dev/null || shasum -a 256 "$ARCHIVE" | awk '{print $1}')
  ACTUAL=$(echo "$ACTUAL" | awk '{print $1}')
  if [ "$EXPECTED" != "$ACTUAL" ]; then
    echo "ERROR: Checksum mismatch!"
    echo "  Expected: $EXPECTED"
    echo "  Actual:   $ACTUAL"
    exit 1
  fi
  echo "Checksum verified."
fi

# Verify cosign signature (if cosign available)
if command -v cosign >/dev/null 2>&1 && [ -z "${HEY_INSECURE_SKIP_COSIGN:-}" ]; then
  SIG_URL="https://github.com/$REPO/releases/download/${VERSION}/checksums.txt.bundle"
  if curl -fsSL "$SIG_URL" -o checksums.txt.bundle 2>/dev/null; then
    echo "Verifying cosign signature..."
    if cosign verify-blob \
      --bundle checksums.txt.bundle \
      --certificate-identity-regexp="https://github.com/$REPO/.github/workflows/release.yml@refs/tags/v" \
      --certificate-oidc-issuer="https://token.actions.githubusercontent.com" \
      checksums.txt 2>/dev/null; then
      echo "Signature verified."
    else
      echo "ERROR: Signature verification failed"
      echo "Set HEY_INSECURE_SKIP_COSIGN=1 to bypass"
      exit 1
    fi
  fi
fi

# Extract
echo "Extracting..."
if [ "$EXT" = "zip" ]; then
  unzip -q "$ARCHIVE" -d extract
else
  mkdir -p extract
  tar -xzf "$ARCHIVE" -C extract
fi

# Install
mkdir -p "$INSTALL_DIR"
BINARY="hey"
if [ "$OS" = "windows" ]; then
  BINARY="hey.exe"
fi
cp "extract/${BINARY}" "$INSTALL_DIR/${BINARY}"
chmod +x "$INSTALL_DIR/${BINARY}"

echo ""
echo "hey ${VERSION} installed to $INSTALL_DIR/${BINARY}"

# Check if install dir is in PATH
if ! echo "$PATH" | tr ':' '\n' | grep -qx "$INSTALL_DIR"; then
  echo ""
  echo "Add $INSTALL_DIR to your PATH:"
  SHELL_NAME=$(basename "${SHELL:-bash}")
  case "$SHELL_NAME" in
    zsh)  echo "  echo 'export PATH=\"$INSTALL_DIR:\$PATH\"' >> ~/.zshrc && source ~/.zshrc" ;;
    fish) echo "  fish_add_path $INSTALL_DIR" ;;
    *)    echo "  echo 'export PATH=\"$INSTALL_DIR:\$PATH\"' >> ~/.bashrc && source ~/.bashrc" ;;
  esac
fi

echo ""
echo "Run 'hey auth login' to get started."
