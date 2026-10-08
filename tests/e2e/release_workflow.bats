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

@test "the size report after publication cannot skip the jobs that follow it" {
  release=$(job_body release)
  report=$(awk '/- name: Report release size budget/ { inside = 1 } inside && /run:/ { print; exit } inside { print }' <<<"$release")
  [[ "$report" == *"continue-on-error: true"* ]]
}

@test "Packslip runs after release without becoming a publication dependency" {
  signing=$(job_body packslip)
  [[ "$signing" == *"needs: [release]"* ]]
  [[ "$signing" == *"needs.release.result == 'success'"* ]]
  [[ "$signing" == *"!cancelled()"* ]]
  [[ "$signing" == *"continue-on-error: true"* ]]
  [[ "$signing" == *"timeout-minutes: 10"* ]]

  for job in release aur-publish macos-verify windows-verify sync-skills; do
    body=$(job_body "$job")
    [[ "$body" != *"needs: [packslip]"* ]]
    [[ "$body" != *"publish-packslip"* ]]
  done
}

@test "failed signing or staging cannot upload a Packslip bundle" {
  signing=$(job_body packslip)
  [[ "$signing" == *"if: steps.sign.outcome == 'success'"* ]]
  [[ "$signing" == *"bundle-ready: \${{ steps.stage.outcome == 'success' }}"* ]]
  [[ "$signing" == *"if-no-files-found: error"* ]]
  [[ "$signing" == *"upload: false"* ]]

  publishing=$(job_body publish-packslip)
  [[ "$publishing" == *"needs.packslip.result == 'success'"* ]]
  [[ "$publishing" == *"needs.packslip.outputs.bundle-ready == 'true'"* ]]
  [[ "$publishing" == *"!cancelled()"* ]]
  [[ "$publishing" == *"continue-on-error: true"* ]]
  [[ "$publishing" == *"timeout-minutes: 10"* ]]
}

@test "Packslip separates signing from release-write access and links build provenance" {
  signing=$(job_body packslip)
  [[ "$signing" == *"contents: read"* ]]
  [[ "$signing" == *"id-token: write"* ]]
  [[ "$signing" != *"contents: write"* ]]
  [[ "$signing" != *"attestations: write"* ]]
  [[ "$signing" != *"secrets."* ]]
  [[ "$signing" == *"attest: link"* ]]

  publishing=$(job_body publish-packslip)
  [[ "$publishing" == *"contents: write"* ]]
  [[ "$publishing" != *"id-token:"* ]]
  [[ "$publishing" != *"uses: jdx/packslip@"* ]]
}
