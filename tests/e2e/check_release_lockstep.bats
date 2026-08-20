#!/usr/bin/env bats
# check_release_lockstep.bats - scripts/check-release-lockstep.sh keeps the
# release pipeline's pins and script references in agreement. Each check gets a
# fixture tree that is correct except for the one drift under test.

setup() {
  REPO_ROOT="$(cd "${BATS_TEST_DIRNAME}/../.." && pwd)"
  WORK="$(mktemp -d)"
  mkdir -p "$WORK/scripts" "$WORK/.github/workflows" "$WORK/tests/e2e"
  cp "$REPO_ROOT/scripts/check-release-lockstep.sh" "$REPO_ROOT/scripts/check-lint-lockstep.sh" "$WORK/scripts/"
  cd "$WORK"

  printf '[tools]\ngo = "1.26.6"\ngoreleaser = "2.15.4"\n' > .mise.toml
  cat > .pre-commit-config.yaml <<'YAML'
repos:
  - repo: https://github.com/golangci/golangci-lint
    rev: v2.11.1
    hooks:
      - id: golangci-lint
YAML
  cat > .github/workflows/test.yml <<'YAML'
jobs:
  lint:
    steps:
      - uses: golangci/golangci-lint-action@abc # v9.3.0
        with:
          version: v2.11.1
      - run: scripts/check-release-lockstep.sh
YAML
  cat > .github/workflows/release.yml <<'YAML'
jobs:
  test:
    steps:
      - uses: golangci/golangci-lint-action@abc # v9.3.0
        with:
          version: v2.11.1
          install-only: true
      - run: scripts/publish.sh
  release:
    steps:
      - uses: goreleaser/goreleaser-action@def # v7.2.3
        with:
          distribution: goreleaser
          version: 'v2.15.4'
          install-only: true
YAML
  printf '#!/usr/bin/env bash\n' > scripts/publish.sh
  printf '# Docs\n\nRun `scripts/check-lint-lockstep.sh` to verify pins.\n' > README.md
}

teardown() {
  rm -rf "$WORK"
}

@test "passes a consistent tree" {
  run scripts/check-release-lockstep.sh
  [ "$status" -eq 0 ]
  [[ "$output" == *"goreleaser pin in lockstep (v2.15.4)"* ]]
  [[ "$output" == *"pre-commit golangci-lint rev in lockstep (v2.11.1)"* ]]
  [[ "$output" == *"release lockstep check passed"* ]]
}

@test "fails when .mise.toml and release.yml pin different goreleasers" {
  sed -i.bak 's/2.15.4/2.14.1/' .mise.toml && rm -f .mise.toml.bak
  run scripts/check-release-lockstep.sh
  [ "$status" -eq 1 ]
  [[ "$output" == *"goreleaser pins disagree"* ]]
  [[ "$output" == *"2.14.1"* ]]
  [[ "$output" == *"v2.15.4"* ]]
}

@test "fails when the pre-commit golangci-lint rev drifts from CI" {
  sed -i.bak 's/rev: v2.11.1/rev: v2.1.6/' .pre-commit-config.yaml && rm -f .pre-commit-config.yaml.bak
  run scripts/check-release-lockstep.sh
  [ "$status" -eq 1 ]
  [[ "$output" == *"golangci-lint pins disagree"* ]]
  [[ "$output" == *"v2.1.6"* ]]
}

@test "inherits the golangci-lint workflow lockstep failure" {
  sed -i.bak 's/version: v2.11.1/version: v2.9.0/' .github/workflows/release.yml && rm -f .github/workflows/release.yml.bak
  run scripts/check-release-lockstep.sh
  [ "$status" -eq 1 ]
  [[ "$output" == *"golangci-lint pins disagree across workflows"* ]]
}

@test "fails when a workflow names a script that does not exist" {
  rm scripts/publish.sh
  run scripts/check-release-lockstep.sh
  [ "$status" -eq 1 ]
  [[ "$output" == *"scripts/publish.sh is referenced but does not exist"* ]]
  [[ "$output" == *"release.yml"* ]]
}

@test "fails when docs name a script that does not exist" {
  echo 'See `scripts/missing-helper.sh`.' >> README.md
  run scripts/check-release-lockstep.sh
  [ "$status" -eq 1 ]
  [[ "$output" == *"scripts/missing-helper.sh is referenced but does not exist"* ]]
}

@test "fails when a script exists but nothing references it" {
  printf '#!/usr/bin/env bash\n# Usage: scripts/orphan.sh\n' > scripts/orphan.sh
  run scripts/check-release-lockstep.sh
  [ "$status" -eq 1 ]
  [[ "$output" == *"scripts/orphan.sh is not referenced anywhere"* ]]
}

@test "a script referenced only by its test counts as used" {
  printf '#!/usr/bin/env bash\n' > scripts/tested-only.sh
  printf '@test "x" { run "$REPO_ROOT/scripts/tested-only.sh"; }\n' > tests/e2e/tested_only.bats
  run scripts/check-release-lockstep.sh
  [ "$status" -eq 0 ]
}

@test "a fixture script named only inside a test need not exist" {
  printf '@test "x" { echo scripts/fixture-only.sh; }\n' > tests/e2e/fixture.bats
  run scripts/check-release-lockstep.sh
  [ "$status" -eq 0 ]
}

@test "a script referenced only by another script via SCRIPT_DIR counts" {
  printf '#!/usr/bin/env bash\n' > scripts/helper.sh
  printf '#!/usr/bin/env bash\n"$SCRIPT_DIR/helper.sh"\n' > scripts/publish.sh
  run scripts/check-release-lockstep.sh
  [ "$status" -eq 0 ]
}

@test "a script referenced via SCRIPT_DIR must exist" {
  printf '#!/usr/bin/env bash\n"${SCRIPT_DIR}/vanished.sh"\n' > scripts/publish.sh
  run scripts/check-release-lockstep.sh
  [ "$status" -eq 1 ]
  [[ "$output" == *"scripts/vanished.sh is referenced but does not exist"* ]]
  [[ "$output" == *"scripts/publish.sh"* ]]
}

@test "fails when the lint lockstep helper is missing" {
  rm scripts/check-lint-lockstep.sh
  run scripts/check-release-lockstep.sh
  [ "$status" -eq 1 ]
  [[ "$output" == *"check-lint-lockstep.sh is missing or not executable"* ]]
}

@test "ignores the sensitive-change gate's pattern list" {
  printf 'extra-patterns: |\n  scripts/not-yet-landed.sh\n' > .github/workflows/sensitive-change-gate.yml
  run scripts/check-release-lockstep.sh
  [ "$status" -eq 0 ]
}

@test "the real repository is in lockstep" {
  run "$REPO_ROOT/scripts/check-release-lockstep.sh" "$REPO_ROOT"
  [ "$status" -eq 0 ]
}
