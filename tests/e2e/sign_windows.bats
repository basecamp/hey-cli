#!/usr/bin/env bats
# sign_windows.bats - Tests for scripts/sign-windows.sh's fail-closed contract.
#
# The wrapper gates Authenticode signing in the release pipeline: it must
# skip cleanly when signing is unconfigured (forks, make test-release), but
# hard-fail on partial configuration or missing material so a tagged release
# can never silently ship unsigned.

setup() {
  SIGN_SH="${BATS_TEST_DIRNAME}/../../scripts/sign-windows.sh"

  STUB_DIR="$(mktemp -d)"
  LOG="$STUB_DIR/java-args.log"
  TARGET_FILE="$STUB_DIR/hey.exe"
  CERT_FILE="$STUB_DIR/client-cert.p12"
  JAR_FILE="$STUB_DIR/jsign.jar"
  echo "pe-bytes" > "$TARGET_FILE"
  echo "cert" > "$CERT_FILE"
  echo "jar" > "$JAR_FILE"

  # A `java` stub that records its argv one-per-line and exits JAVA_EXIT.
  {
    echo '#!/usr/bin/env bash'
    echo "printf '%s\\n' \"\$@\" > \"$LOG\""
    echo 'exit "${JAVA_EXIT:-0}"'
  } > "$STUB_DIR/java"
  chmod +x "$STUB_DIR/java"
}

teardown() {
  [[ -n "${STUB_DIR:-}" ]] && rm -rf "$STUB_DIR"
}

# run_sign VAR=VALUE... TARGET — runs the wrapper with exactly the given
# signing env (everything else cleared), against $TARGET_FILE.
run_sign() {
  local env_args=()
  while [[ "$1" == *=* ]]; do
    env_args+=("$1")
    shift
  done
  run env -u SM_API_KEY -u SM_CLIENT_CERT_FILE -u SM_CLIENT_CERT_PASSWORD \
    -u SIGN_ALIAS -u JSIGN_JAR "${env_args[@]}" \
    PATH="$STUB_DIR:$PATH" \
    "$SIGN_SH" "$1" "$TARGET_FILE"
}

full_env() {
  echo "SM_API_KEY=api-key" \
    "SM_CLIENT_CERT_FILE=$CERT_FILE" \
    "SM_CLIENT_CERT_PASSWORD=cert-pass" \
    "SIGN_ALIAS=alias-guid" \
    "JSIGN_JAR=$JAR_FILE"
}

@test "non-windows target is a silent no-op even with full signing env" {
  # shellcheck disable=SC2046
  run_sign $(full_env) darwin_amd64
  [[ "$status" -eq 0 ]]
  [[ -z "$output" ]]
  [[ "$(cat "$TARGET_FILE")" == "pe-bytes" ]]
  [[ ! -f "$LOG" ]]  # java never invoked
}

@test "all SM_* empty skips with a notice" {
  run_sign windows_amd64
  [[ "$status" -eq 0 ]]
  [[ "$output" == *"skipping Authenticode signing"* ]]
  [[ ! -f "$LOG" ]]
}

@test "skip notice applies to the plain 'windows' target too" {
  run_sign windows
  [[ "$status" -eq 0 ]]
  [[ "$output" == *"skipping Authenticode signing"* ]]
}

# Partial configuration is a misconfigured release, not a fork building from
# source — each missing var must hard-fail and be named.
@test "partial config: each empty var fails naming the var" {
  local var
  for var in SM_API_KEY SM_CLIENT_CERT_FILE SM_CLIENT_CERT_PASSWORD SIGN_ALIAS JSIGN_JAR; do
    local env_args=()
    local pair
    for pair in $(full_env); do
      if [[ "$pair" == "$var="* ]]; then
        env_args+=("$var=")
      else
        env_args+=("$pair")
      fi
    done
    run env -u SM_API_KEY -u SM_CLIENT_CERT_FILE -u SM_CLIENT_CERT_PASSWORD \
      -u SIGN_ALIAS -u JSIGN_JAR "${env_args[@]}" \
      PATH="$STUB_DIR:$PATH" \
      "$SIGN_SH" windows_amd64 "$TARGET_FILE"
    [[ "$status" -eq 1 ]] || { echo "expected exit 1 for empty $var, got $status"; return 1; }
    [[ "$output" == *"$var is empty"* ]] || { echo "missing var name for $var in: $output"; return 1; }
  done
}

@test "nonexistent client cert path fails" {
  run_sign SM_API_KEY=api-key "SM_CLIENT_CERT_FILE=$STUB_DIR/missing.p12" \
    SM_CLIENT_CERT_PASSWORD=cert-pass SIGN_ALIAS=alias-guid "JSIGN_JAR=$JAR_FILE" \
    windows_amd64
  [[ "$status" -eq 1 ]]
  [[ "$output" == *"client cert not found"* ]]
}

@test "nonexistent jsign jar path fails" {
  run_sign SM_API_KEY=api-key "SM_CLIENT_CERT_FILE=$CERT_FILE" \
    SM_CLIENT_CERT_PASSWORD=cert-pass SIGN_ALIAS=alias-guid \
    "JSIGN_JAR=$STUB_DIR/missing.jar" windows_amd64
  [[ "$status" -eq 1 ]]
  [[ "$output" == *"jsign jar not found"* ]]
}

@test "happy path invokes jsign with the exact KeyLocker argv" {
  # shellcheck disable=SC2046
  run_sign $(full_env) windows_amd64
  [[ "$status" -eq 0 ]]
  [[ "$output" == *"signing $TARGET_FILE via DigiCert KeyLocker"* ]]

  expected="-jar
$JAR_FILE
--storetype
DIGICERTONE
--alias
alias-guid
--storepass
api-key|$CERT_FILE|cert-pass
--tsaurl
http://timestamp.digicert.com
--tsretries
3
--tsretrywait
10
$TARGET_FILE"
  [[ "$(cat "$LOG")" == "$expected" ]]
}

@test "jsign failure propagates its exit code" {
  # shellcheck disable=SC2046
  run_sign $(full_env) JAVA_EXIT=7 windows_amd64
  [[ "$status" -eq 7 ]]
}
