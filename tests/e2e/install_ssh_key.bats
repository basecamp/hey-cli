#!/usr/bin/env bats
#
# scripts/install-ssh-key.sh repairs the ways a private key gets mangled on its
# way through a secrets UI, and refuses — with a shape diagnosis and without
# printing key material — anything that still does not load. The AUR publish
# failed its first live run on exactly this: a key that ssh reported only as
# "invalid format". Each mangling that has bitten in practice gets a case, and
# the refusal cases pin that a bad key never reaches the destination.

setup() {
  REPO_ROOT="$(cd "${BATS_TEST_DIRNAME}/../.." && pwd)"
  INSTALL="$REPO_ROOT/scripts/install-ssh-key.sh"
  WORK="$(mktemp -d)"
  ssh-keygen -q -t ed25519 -N '' -C 'aur@example.com' -f "$WORK/id" >/dev/null
  KEY="$(cat "$WORK/id")"
  PUB="$(cut -d' ' -f1,2 "$WORK/id.pub")"
  DEST="$WORK/installed"
}

teardown() {
  rm -rf "$WORK"
}

installed_pubkey() {
  ssh-keygen -y -f "$DEST" | cut -d' ' -f1,2
}

assert_installed() {
  [[ "$status" -eq 0 ]]
  [[ -f "$DEST" ]]
  [[ "$(stat -c %a "$DEST")" == "600" ]]
  [[ "$(installed_pubkey)" == "$PUB" ]]
}

@test "installs a pristine key" {
  SSH_KEY="$KEY"$'\n' run "$INSTALL" "$DEST"
  assert_installed
}

@test "repairs a key whose trailing newline was dropped" {
  SSH_KEY="$KEY" run "$INSTALL" "$DEST"
  assert_installed
  # Substitution strips a trailing newline, so an empty result is the newline.
  [[ -z "$(tail -c1 "$DEST")" ]]
}

@test "repairs CRLF line endings" {
  SSH_KEY="$(printf '%s\n' "$KEY" | sed 's/$/\r/')" run "$INSTALL" "$DEST"
  assert_installed
  ! grep -q $'\r' "$DEST"
}

@test "repairs a key flattened onto one line with literal backslash-n" {
  flat="$(printf '%s\n' "$KEY" | awk '{ printf "%s\\n", $0 }')"
  [[ "$flat" != *$'\n'* ]]
  SSH_KEY="$flat" run "$INSTALL" "$DEST"
  assert_installed
}

@test "repairs surrounding blank lines and trailing spaces" {
  SSH_KEY=$'\n\n'"$(printf '%s\n' "$KEY" | sed 's/$/  /')"$'\n\n  \n' run "$INSTALL" "$DEST"
  assert_installed
}

@test "refuses a public key with a diagnosis and installs nothing" {
  SSH_KEY="$(cat "$WORK/id.pub")" run "$INSTALL" "$DEST"
  [[ "$status" -ne 0 ]]
  [[ ! -e "$DEST" ]]
  [[ "$output" == *"header=PUBLIC-KEY"* ]]
  [[ "$output" == *"gh secret set"* ]]
}

@test "refuses a truncated key and names the missing footer" {
  SSH_KEY="$(printf '%s\n' "$KEY" | head -n 3)" run "$INSTALL" "$DEST"
  [[ "$status" -ne 0 ]]
  [[ ! -e "$DEST" ]]
  [[ "$output" == *"footer=missing"* ]]
}

@test "the diagnosis never prints key material" {
  body="$(printf '%s\n' "$KEY" | sed -n 2p)"
  SSH_KEY="$(printf '%s\n' "$KEY" | head -n 3)" run "$INSTALL" "$DEST"
  [[ "$status" -ne 0 ]]
  [[ "$output" != *"$body"* ]]
}

@test "leaves no temp file behind on refusal" {
  SSH_KEY="not a key" run "$INSTALL" "$DEST"
  [[ "$status" -ne 0 ]]
  [[ -z "$(ls "$WORK" | grep '^installed')" ]]
}

@test "requires SSH_KEY and a destination" {
  run "$INSTALL"
  [[ "$status" -ne 0 ]]
  SSH_KEY= run "$INSTALL" "$DEST"
  [[ "$status" -ne 0 ]]
}
