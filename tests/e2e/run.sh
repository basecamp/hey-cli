#!/usr/bin/env bash
# Runs the bats end-to-end suite: installer and release-script contracts that
# Go tests cannot reach. Requires bats-core (pacman -S bats, brew install
# bats-core). Some install.ps1 tests need pwsh and skip without it locally;
# in CI (CI=1) they fail instead, so a runner image dropping pwsh cannot turn
# into silent coverage loss.

set -euo pipefail

# Headless: never touch the OS keyring from a test.
export HEY_NO_KEYRING=1

cd "$(dirname "$0")"

if ! command -v bats &>/dev/null; then
  echo "Error: bats not found. Install with your package manager (e.g., pacman -S bats, brew install bats-core)" >&2
  exit 1
fi

jobs=$(nproc 2>/dev/null || sysctl -n hw.ncpu 2>/dev/null || echo 1)

# bats parallelises with GNU parallel (Linux) or rush (macOS) when present.
if command -v rush &>/dev/null; then
  exec bats --parallel-binary-name rush -j "$jobs" "$@" ./*.bats
elif command -v parallel &>/dev/null && parallel --version 2>&1 | grep -q "GNU"; then
  exec bats -j "$jobs" "$@" ./*.bats
else
  exec bats "$@" ./*.bats
fi
