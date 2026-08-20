#!/usr/bin/env bash
# Check release binaries against the size budget in .size-budget.
#
# Usage: scripts/check-size-budget.sh [BINARY...]
#
# With no arguments it checks every goreleaser build output under dist/
# (dist/<build>/hey or hey.exe) when dist/ exists, otherwise ./bin/hey. Prints a
# per-platform table, appends it to $GITHUB_STEP_SUMMARY when set, and exits 1
# on a breach unless `enforce = false` in .size-budget (report-only mode, which
# still prints "OVER" so the breach is visible).
#
# Sizes are of the binary as shipped (goreleaser builds with -s -w; so does
# `make build`), and of the same bytes gzip -9 compressed as a proxy for the
# release archive size.
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
BUDGET_FILE="${SIZE_BUDGET_FILE:-$ROOT/.size-budget}"

[ -f "$BUDGET_FILE" ] || { echo "FAIL: budget file not found: $BUDGET_FILE" >&2; exit 1; }

budget_value() {
  local key="$1"
  sed -nE "s/^[[:space:]]*${key}[[:space:]]*=[[:space:]]*([^[:space:]#]+).*/\1/p" "$BUDGET_FILE" | head -1
}

STRIPPED_MAX=$(budget_value stripped_max_mib)
GZIP_MAX=$(budget_value gzip_max_mib)
ENFORCE=$(budget_value enforce)
ENFORCE="${ENFORCE:-true}"

for v in "$STRIPPED_MAX" "$GZIP_MAX"; do
  [[ "$v" =~ ^[0-9]+(\.[0-9]+)?$ ]] || { echo "FAIL: stripped_max_mib and gzip_max_mib must be numeric in $BUDGET_FILE" >&2; exit 1; }
done

bins=("$@")
if [ "${#bins[@]}" -eq 0 ]; then
  if [ -d "$ROOT/dist" ]; then
    # Globs, not `find -mindepth/-maxdepth`, so dist/ discovery behaves the
    # same under BSD find on a macOS operator's machine.
    shopt -s nullglob
    candidates=("$ROOT"/dist/*/hey "$ROOT"/dist/*/hey.exe)
    shopt -u nullglob
    while IFS= read -r f; do [ -f "$f" ] && bins+=("$f"); done < <(printf '%s\n' "${candidates[@]}" | sed '/^$/d' | LC_ALL=C sort)
  elif [ -f "$ROOT/bin/hey" ]; then
    bins=("$ROOT/bin/hey")
  fi
fi

[ "${#bins[@]}" -gt 0 ] || { echo "FAIL: no binaries to check (build first, or pass paths)" >&2; exit 1; }

file_size() {
  if stat -c %s "$1" >/dev/null 2>&1; then stat -c %s "$1"; else stat -f %z "$1"; fi
}

mib() { awk -v b="$1" 'BEGIN { printf "%.1f", b / 1048576 }'; }
over() { awk -v a="$1" -v max="$2" 'BEGIN { exit !(a / 1048576 > max) }'; }

platform_of() {
  # dist/hey_linux_amd64_v1/hey -> linux_amd64 ; ./bin/hey -> local
  local dir; dir=$(basename "$(dirname "$1")")
  case "$dir" in
    hey_*) echo "$dir" | sed -E 's/^hey_//; s/_v[0-9.]+$//' ;;
    *) echo "local" ;;
  esac
}

breached=0
table=$'| Platform | Stripped (MiB) | Gzipped (MiB) | Status |\n|---|---:|---:|---|'
for bin in "${bins[@]}"; do
  [ -f "$bin" ] || { echo "FAIL: not a file: $bin" >&2; exit 1; }
  raw=$(file_size "$bin")
  gz=$(gzip -9 -c -- "$bin" | wc -c | tr -d ' ')
  status="ok"
  if over "$raw" "$STRIPPED_MAX" || over "$gz" "$GZIP_MAX"; then
    status="OVER"
    breached=1
  fi
  table+=$'\n'"| $(platform_of "$bin") | $(mib "$raw") | $(mib "$gz") | $status |"
done

header="Size budget: stripped <= ${STRIPPED_MAX} MiB, gzipped <= ${GZIP_MAX} MiB (enforce=${ENFORCE})"
echo "$header"
echo "$table"

if [ -n "${GITHUB_STEP_SUMMARY:-}" ]; then
  {
    echo "### Release size budget"
    echo ""
    echo "$header"
    echo ""
    echo "$table"
  } >> "$GITHUB_STEP_SUMMARY"
fi

if [ "$breached" -eq 1 ]; then
  if [ "$ENFORCE" = "false" ]; then
    echo "WARN: size budget exceeded (report-only: enforce = false in $(basename "$BUDGET_FILE"))"
    exit 0
  fi
  echo "FAIL: size budget exceeded. Review what grew before raising $(basename "$BUDGET_FILE")."
  exit 1
fi
echo "PASS: all binaries within budget"
