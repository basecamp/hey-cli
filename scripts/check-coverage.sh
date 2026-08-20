#!/usr/bin/env bash
set -euo pipefail

profile=${1:-coverage.out}
floor=${2:-70.8}

if [[ ! -s "$profile" ]]; then
  echo "coverage profile not found: $profile" >&2
  exit 1
fi

read -r covered total < <(
  awk '
    NR == 1 { next }
    {
      block = $1
      if (!(block in statements)) {
        statements[block] = $2
      }
      if ($3 > 0) {
        hit[block] = 1
      }
    }
    END {
      for (block in statements) {
        total += statements[block]
        if (block in hit) {
          covered += statements[block]
        }
      }
      print covered, total
    }
  ' "$profile"
)

if [[ -z "$covered" || -z "$total" || "$total" -eq 0 ]]; then
  echo "could not read statement coverage from $profile" >&2
  exit 1
fi

actual=$(awk -v covered="$covered" -v total="$total" 'BEGIN { printf "%.6f", 100 * covered / total }')
if ! awk -v actual="$actual" -v floor="$floor" 'BEGIN { exit !(actual + 0 >= floor + 0) }'; then
  printf 'coverage %.3f%% (%d / %d statements) is below the %.3f%% floor\n' "$actual" "$covered" "$total" "$floor" >&2
  exit 1
fi

printf 'coverage %.3f%% (%d / %d statements) meets the %.3f%% floor\n' "$actual" "$covered" "$total" "$floor"
