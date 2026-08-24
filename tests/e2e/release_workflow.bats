#!/usr/bin/env bats
# release_workflow.bats - static contracts for the tag workflow's publication
# ordering. actionlint validates syntax; these assertions validate intent.

setup() {
  REPO_ROOT="$(cd "${BATS_TEST_DIRNAME}/../.." && pwd)"
  WORKFLOW="$REPO_ROOT/.github/workflows/release.yml"
}

job_body() {
  local job="$1"
  awk -v job="$job" '
    $0 == "  " job ":" { inside = 1 }
    inside && $0 ~ /^  [a-zA-Z0-9_-]+:$/ && $0 != "  " job ":" { exit }
    inside { print }
  ' "$WORKFLOW"
}

@test "release waits for exact-tag Nix verification" {
  release=$(job_body release)
  [[ "$release" == *"needs: [test, security, nix-verify]"* ]]

  nix=$(job_body nix-verify)
  [[ "$nix" != *"needs: [release]"* ]]
  [[ "$nix" == *"nix build --no-link --print-out-paths"* ]]
}

@test "prereleases complete the Nix gate without building stable metadata" {
  nix=$(job_body nix-verify)
  [[ "$nix" == *"name: Skip prerelease"* ]]
  [[ "$nix" == *"if: \${{ contains(github.ref_name, '-') }}"* ]]
  [[ "$nix" == *"if: \${{ !contains(github.ref_name, '-') }}"* ]]
}
