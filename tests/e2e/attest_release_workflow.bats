#!/usr/bin/env bats
# attest_release_workflow.bats - static contracts for the manual attestation
# workflow. actionlint validates syntax; these assertions validate intent.

setup() {
  REPO_ROOT="$(cd "${BATS_TEST_DIRNAME}/../.." && pwd)"
  WORKFLOW="$REPO_ROOT/.github/workflows/attest-release.yml"
}

step_order() {
  grep -n -- "- name: $1" "$WORKFLOW" | head -1 | cut -d: -f1
}

@test "checksums are trusted only when the release run for the tag signed them" {
  run cat "$WORKFLOW"
  [[ "$output" == *'.github/workflows/release.yml@refs/tags/${TAG}'* ]]
  [[ "$output" == *"--certificate-oidc-issuer https://token.actions.githubusercontent.com"* ]]
}

@test "attestation comes after both verifications" {
  signed=$(step_order "Verify checksums.txt was signed by the release run")
  published=$(step_order "Verify every checksum matches the published asset")
  attest=$(step_order "Attest build provenance")
  [ -n "$signed" ] && [ -n "$published" ] && [ -n "$attest" ]
  [ "$signed" -lt "$attest" ]
  [ "$published" -lt "$attest" ]
}

@test "it attests the release's own checksums, as release.yml does" {
  run grep -c "subject-checksums: ./checksums.txt" "$WORKFLOW"
  [ "$output" = "1" ]
  expected=$(grep -o "actions/attest-build-provenance@[0-9a-f]*" "$REPO_ROOT/.github/workflows/release.yml")
  actual=$(grep -o "actions/attest-build-provenance@[0-9a-f]*" "$WORKFLOW")
  [ "$expected" = "$actual" ]
}

@test "it can read the release but cannot write to the repository" {
  run grep -E "contents: write|actions: write" "$WORKFLOW"
  [ "$status" -ne 0 ]
  run grep -c "^      contents: read$" "$WORKFLOW"
  [ "$output" = "1" ]
}
