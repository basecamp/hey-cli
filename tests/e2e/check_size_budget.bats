#!/usr/bin/env bats
# check_size_budget.bats - scripts/check-size-budget.sh measures binaries
# against .size-budget, reports per platform, and fails (or only warns, in
# report-only mode) on a breach.

setup() {
  REPO_ROOT="$(cd "${BATS_TEST_DIRNAME}/../.." && pwd)"
  CHECK="$REPO_ROOT/scripts/check-size-budget.sh"
  WORK="$(mktemp -d)"
  BUDGET="$WORK/.size-budget"
  export SIZE_BUDGET_FILE="$BUDGET"
  # 2 MiB of incompressible bytes: ~2.0 MiB stripped, ~2.0 MiB gzipped.
  head -c 2097152 /dev/urandom > "$WORK/big"
  # 2 MiB of zeros: ~2.0 MiB stripped, tiny gzipped.
  head -c 2097152 /dev/zero > "$WORK/zeros"
}

teardown() {
  rm -rf "$WORK"
}

write_budget() {
  printf 'stripped_max_mib = %s\ngzip_max_mib = %s\nenforce = %s\n' "$1" "$2" "$3" > "$BUDGET"
}

@test "passes binaries under budget and prints a table" {
  write_budget 3 3 true
  run "$CHECK" "$WORK/big"
  [ "$status" -eq 0 ]
  [[ "$output" == *"| Platform |"* ]]
  [[ "$output" == *"| 2.0 |"* ]]
  [[ "$output" == *"PASS"* ]]
}

@test "fails on a stripped-size breach when enforcing" {
  write_budget 1 3 true
  run "$CHECK" "$WORK/big"
  [ "$status" -eq 1 ]
  [[ "$output" == *"OVER"* ]]
  [[ "$output" == *"FAIL"* ]]
}

@test "fails on a gzip breach alone" {
  write_budget 3 1 true
  run "$CHECK" "$WORK/big"
  [ "$status" -eq 1 ]
  [[ "$output" == *"OVER"* ]]
}

@test "gzip bound is about the compressed size, not the raw size" {
  # zeros: 2 MiB raw but ~2 KiB gzipped — passes a 1 MiB gzip bound.
  write_budget 3 1 true
  run "$CHECK" "$WORK/zeros"
  [ "$status" -eq 0 ]
}

@test "report-only mode still prints OVER but exits 0" {
  write_budget 1 1 false
  run "$CHECK" "$WORK/big"
  [ "$status" -eq 0 ]
  [[ "$output" == *"OVER"* ]]
  [[ "$output" == *"WARN"* ]]
  [[ "$output" != *"PASS"* ]]
}

@test "labels goreleaser dist/ layout by platform" {
  mkdir -p "$WORK/dist/hey_linux_amd64_v1" "$WORK/dist/hey_darwin_arm64_v8.0" "$WORK/dist/hey_windows_amd64_v1"
  cp "$WORK/big" "$WORK/dist/hey_linux_amd64_v1/hey"
  cp "$WORK/big" "$WORK/dist/hey_darwin_arm64_v8.0/hey"
  cp "$WORK/big" "$WORK/dist/hey_windows_amd64_v1/hey.exe"
  write_budget 3 3 true
  run "$CHECK" "$WORK"/dist/*/hey "$WORK"/dist/*/hey.exe
  [ "$status" -eq 0 ]
  [[ "$output" == *"| linux_amd64 |"* ]]
  [[ "$output" == *"| darwin_arm64 |"* ]]
  [[ "$output" == *"| windows_amd64 |"* ]]
}

@test "appends the table to GITHUB_STEP_SUMMARY when set" {
  write_budget 3 3 true
  GITHUB_STEP_SUMMARY="$WORK/summary.md" run "$CHECK" "$WORK/big"
  [ "$status" -eq 0 ]
  grep -q "Release size budget" "$WORK/summary.md"
  grep -q "| Platform |" "$WORK/summary.md"
}

@test "fails on a malformed budget file" {
  printf 'stripped_max_mib = lots\ngzip_max_mib = 3\n' > "$BUDGET"
  run "$CHECK" "$WORK/big"
  [ "$status" -eq 1 ]
  [[ "$output" == *"numeric"* ]]
}

@test "fails when given nothing to check" {
  write_budget 3 3 true
  run "$CHECK" "$WORK/does-not-exist"
  [ "$status" -eq 1 ]
}

@test "the committed .size-budget parses" {
  SIZE_BUDGET_FILE="$REPO_ROOT/.size-budget" run "$CHECK" "$WORK/zeros"
  [ "$status" -eq 0 ]
  [[ "$output" == *"Size budget: stripped <="* ]]
}
