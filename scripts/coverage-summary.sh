#!/usr/bin/env bash
set -euo pipefail

profile=${1:-coverage.out}
function_summary=${2:-coverage.func.txt}
package_summary=${3:-coverage.packages.txt}

if [[ ! -s "$profile" ]]; then
  echo "coverage profile not found: $profile" >&2
  exit 1
fi

GOWORK=off go tool cover -func="$profile" > "$function_summary"

awk '
  NR == 1 { next }
  {
    block = $1
    file = block
    sub(/:[0-9].*$/, "", file)
    package = file
    sub(/\/[^/]+$/, "", package)
    sub(/^github.com\/basecamp\/hey-cli\//, "", package)

    if (!(block in statements)) {
      statements[block] = $2
      packages[block] = package
      totals[package] += $2
    }
    if ($3 > 0 && !(block in hit)) {
      hit[block] = 1
      covered[package] += statements[block]
    }
  }
  END {
    for (package in totals) {
      percent = totals[package] == 0 ? 0 : 100 * covered[package] / totals[package]
      printf "%s\t%d / %d\t%.1f%%\n", package, covered[package], totals[package], percent
    }
  }
' "$profile" | sort > "$package_summary"

echo "Package coverage"
printf "PACKAGE\tSTATEMENTS\tCOVERAGE\n"
cat "$package_summary"

echo
echo "Lowest-covered functions (up to 15)"
printf "FUNCTION\tCOVERAGE\n"
awk '$1 != "total:" { gsub(/%$/, "", $3); print $1 " " $2 "\t" $3 "%" }' "$function_summary" \
  | sort -t $'\t' -k2,2n \
  | awk 'NR <= 15'

echo
awk '/^total:/ { print "Repository total\t" $NF }' "$function_summary"
