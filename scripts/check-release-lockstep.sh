#!/usr/bin/env bash
# Verify the pins and references the release pipeline depends on agree.
#
# Usage: scripts/check-release-lockstep.sh [ROOT]
#
# Wraps check-lint-lockstep.sh (golangci-lint pins across workflows) and adds:
#   1. .mise.toml's goreleaser version == the goreleaser-action `version:` pin
#      in release.yml, so `make test-release` locally exercises the goreleaser
#      that will cut the tag.
#   2. .pre-commit-config.yaml's golangci-lint rev == the CI pin, so a commit
#      hook and CI cannot disagree on a finding.
#   3. Every scripts/*.sh named in docs, workflows, the Makefile, goreleaser
#      config, other scripts or tests exists, and every scripts/*.sh is named
#      somewhere — a renamed or orphaned script fails here rather than at the
#      moment a release step or a README command reaches for it.
#
# Each check prints its own FAIL; the script exits 1 if any failed.
set -euo pipefail

ROOT="${1:-$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)}"
cd "$ROOT"

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
failed=0
fail() { echo "FAIL: $*"; failed=1; }

# --- 0. golangci-lint lockstep across workflows ---
if [ -x "$SCRIPT_DIR/check-lint-lockstep.sh" ]; then
  "$SCRIPT_DIR/check-lint-lockstep.sh" || failed=1
fi

# --- 1. goreleaser: .mise.toml vs release.yml ---
MISE_GR=$(sed -nE 's/^goreleaser[[:space:]]*=[[:space:]]*"([^"]+)".*/\1/p' .mise.toml 2>/dev/null | head -1)
CI_GR=$(awk '
  /uses:.*goreleaser\/goreleaser-action/ { in_step = 1; next }
  in_step && /^[[:space:]]*version:/ {
    match($0, /v?[0-9]+\.[0-9]+\.[0-9]+/); print substr($0, RSTART, RLENGTH); in_step = 0
  }
  in_step && /^[[:space:]]*-[[:space:]]/ { in_step = 0 }
' .github/workflows/release.yml 2>/dev/null | head -1)
if [ -z "$MISE_GR" ]; then
  fail "no goreleaser pin in .mise.toml"
elif [ -z "$CI_GR" ]; then
  fail "no goreleaser-action version pin in .github/workflows/release.yml"
elif [ "v${MISE_GR#v}" != "v${CI_GR#v}" ]; then
  fail "goreleaser pins disagree: .mise.toml has ${MISE_GR}, release.yml has ${CI_GR}"
else
  echo "goreleaser pin in lockstep (${CI_GR})"
fi

# --- 2. golangci-lint: pre-commit vs CI ---
PC_REV=$(awk '
  /repo:.*golangci\/golangci-lint/ { in_repo = 1; next }
  in_repo && /^[[:space:]]*rev:/ { match($0, /v[0-9]+\.[0-9]+\.[0-9]+/); print substr($0, RSTART, RLENGTH); exit }
  in_repo && /^[[:space:]]*-[[:space:]]*repo:/ { in_repo = 0 }
' .pre-commit-config.yaml 2>/dev/null | head -1)
shopt -s nullglob
workflows=(.github/workflows/*.yml .github/workflows/*.yaml)
shopt -u nullglob
CI_LINT=""
if [ "${#workflows[@]}" -gt 0 ]; then
  CI_LINT=$(awk '
    /uses:.*golangci-lint-action/ { in_step = 1; next }
    in_step && /^[[:space:]]*version:/ { match($0, /v[0-9]+\.[0-9]+\.[0-9]+/); print substr($0, RSTART, RLENGTH); in_step = 0 }
    in_step && /^[[:space:]]*-[[:space:]]/ { in_step = 0 }
  ' "${workflows[@]}" | LC_ALL=C sort -u | head -1)
fi
if [ -f .pre-commit-config.yaml ]; then
  if [ -z "$PC_REV" ]; then
    fail "no golangci-lint rev in .pre-commit-config.yaml"
  elif [ -n "$CI_LINT" ] && [ "$PC_REV" != "$CI_LINT" ]; then
    fail "golangci-lint pins disagree: .pre-commit-config.yaml has ${PC_REV}, CI has ${CI_LINT}"
  else
    echo "pre-commit golangci-lint rev in lockstep (${PC_REV})"
  fi
fi

# --- 3. scripts/*.sh referenced <-> existing ---
shopt -s nullglob
existing=()
for f in scripts/*.sh; do existing+=("$(basename "$f")"); done
shopt -u nullglob

# Two directions, two file sets. "Referenced -> must exist" scans docs,
# workflows, the Makefile, goreleaser config and scripts themselves; a name
# there is an invocation or an instruction. Tests are excluded from that
# direction because bats fixtures name scripts that exist only inside the
# fixture. "Exists -> must be referenced" additionally counts tests, so a
# script whose only caller is its own test still counts as used. The
# sensitive-change gate is excluded from both: it lists path *patterns* to
# watch, which may name a script before it lands.
collect_files() {
  { find . -maxdepth 1 -type f \( -name '*.md' -o -name Makefile -o -name '.goreleaser.y*ml' \);
    find "$@" -type f \( -name '*.yml' -o -name '*.yaml' -o -name '*.sh' -o -name '*.bats' -o -name '*.md' \) \
      ! -name 'sensitive-change-gate.yml'; } 2>/dev/null | LC_ALL=C sort -u
}
script_refs() {
  # Tokens of the form scripts/<name>.sh, printed as the bare <name>.sh.
  grep -hoE '[A-Za-z0-9_./-]*[A-Za-z0-9_-]+\.sh\b' "$@" 2>/dev/null \
    | grep -E '(^|/)scripts/[A-Za-z0-9_-]+\.sh$' | sed -E 's#.*scripts/##' | LC_ALL=C sort -u || true
}

ref_files=()
while IFS= read -r f; do ref_files+=("$f"); done < <(collect_files .github scripts)
usage_files=()
while IFS= read -r f; do usage_files+=("$f"); done < <(collect_files .github scripts tests)

referenced=$(script_refs "${ref_files[@]}")
used=$(script_refs "${usage_files[@]}")

for name in $referenced; do
  [ -f "scripts/$name" ] || fail "scripts/$name is referenced but does not exist ($(grep -rlE "scripts/$name" "${ref_files[@]}" | head -3 | tr '\n' ' '))"
done
for name in "${existing[@]}"; do
  # A script may be referenced by other scripts under its bare name via
  # "$SCRIPT_DIR/name.sh" — count those too.
  if ! grep -qx "$name" <<<"$used" && ! grep -rqE "/${name}\b" scripts/ --include='*.sh' 2>/dev/null; then
    fail "scripts/$name is not referenced anywhere (docs, workflows, Makefile, goreleaser, scripts, tests)"
  fi
done
[ "$failed" -eq 0 ] && echo "scripts/*.sh references resolve (${#existing[@]} scripts)"

if [ "$failed" -ne 0 ]; then
  echo "release lockstep check failed"
  exit 1
fi
echo "release lockstep check passed"
