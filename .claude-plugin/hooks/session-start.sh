#!/bin/bash
# Emits current hey-cli context for agent sessions
set -euo pipefail

echo "=== HEY CLI Status ==="

if command -v hey &>/dev/null; then
  echo "Binary: $(command -v hey)"
  hey --version 2>/dev/null || echo "Version: unknown"
else
  echo "Binary: not found in PATH"
  exit 0
fi

echo ""
echo "=== Authentication ==="
hey auth status 2>/dev/null || echo "Auth: unavailable"

echo ""
echo "=== Configuration ==="
echo "Config dir: ${XDG_CONFIG_HOME:-$HOME/.config}/hey-cli"
if [ -f "${XDG_CONFIG_HOME:-$HOME/.config}/hey-cli/config.json" ]; then
  cat "${XDG_CONFIG_HOME:-$HOME/.config}/hey-cli/config.json"
else
  echo "No config file found (using defaults)"
fi
