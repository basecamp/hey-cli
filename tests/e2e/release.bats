#!/usr/bin/env bats
#
# scripts/release.sh against a real git repo with a local bare origin. The
# script mutates main (the stable metadata commit) before it creates the tag,
# so what matters is ordering: a release that is going to be refused must be
# refused before main is touched, and the prep commit must reach origin before
# the tag that the release workflow builds from.
#
# make and update-nix-flake.sh are stubbed; stamp-plugin-version.sh is the real
# one. A pre-receive hook on the bare origin records every ref it receives, in
# order, which is the only way to observe the push sequence after the fact.

setup() {
  REPO_ROOT="$(cd "${BATS_TEST_DIRNAME}/../.." && pwd)"
  WORK="$(mktemp -d)"
  STUB_DIR="$WORK/bin"
  export LOG="$WORK/calls.log"
  PUSH_LOG="$WORK/pushes.log"
  mkdir -p "$STUB_DIR"

  git init -q --bare --initial-branch=main "$WORK/origin.git"
  cat > "$WORK/origin.git/hooks/pre-receive" <<HOOK
#!/bin/sh
while read -r _old _new ref; do echo "\$ref" >> "$PUSH_LOG"; done
HOOK
  chmod +x "$WORK/origin.git/hooks/pre-receive"

  git clone -q "$WORK/origin.git" "$WORK/repo"
  cd "$WORK/repo"
  git config user.email release@example.com
  git config user.name "Release Bot"
  git checkout -q -b main

  mkdir -p scripts nix .claude-plugin
  cp "$REPO_ROOT/scripts/release.sh" "$REPO_ROOT/scripts/stamp-plugin-version.sh" scripts/
  cat > scripts/update-nix-flake.sh <<'STUB'
#!/usr/bin/env bash
echo "update-nix-flake $1" >> "$LOG"
if grep -q "version = \"$1\"" nix/package.nix; then exit 2; fi
printf '{\n  version = "%s";\n}\n' "$1" > nix/package.nix
STUB
  chmod +x scripts/update-nix-flake.sh
  printf '{\n  version = "0.1.0";\n}\n' > nix/package.nix
  printf '{\n  "name": "hey",\n  "version": "0.1.0"\n}\n' > .claude-plugin/plugin.json
  printf 'module example.com/hey\n\ngo 1.24\n' > go.mod

  git add -A && git commit -qm "Initial import"
  git tag -a v0.1.0 -m "Release v0.1.0"
  printf 'package main\n' > main.go
  git add -A && git commit -qm "Add main"
  git push -q origin main --tags
  : > "$PUSH_LOG"
  BASE=$(git rev-parse HEAD)

  cat > "$STUB_DIR/make" <<'STUB'
#!/usr/bin/env bash
echo "make $*" >> "$LOG"
STUB
  chmod +x "$STUB_DIR/make"

  # macOS's stock sort has no -V/--version-sort. Shadow sort with a stub that
  # refuses those flags so every test fails if release.sh reaches for them.
  cat > "$STUB_DIR/sort" <<'STUB'
#!/usr/bin/env bash
for arg in "$@"; do
  if [[ "$arg" == "--version-sort" || ( "$arg" == -[a-zA-Z]* && "$arg" == *V* ) ]]; then
    echo "sort: illegal option -- V" >&2
    exit 2
  fi
done
exec /usr/bin/sort "$@"
STUB
  chmod +x "$STUB_DIR/sort"
  PATH="$STUB_DIR:$PATH"
}

teardown() {
  rm -rf "$WORK"
}

origin() { git --git-dir="$WORK/origin.git" "$@"; }
plugin_version() { jq -r .version .claude-plugin/plugin.json; }

@test "stable release commits and pushes the metadata before pushing the tag" {
  run scripts/release.sh 0.2.0
  [ "$status" -eq 0 ]

  [ "$(origin log -1 --format=%s main)" = "Update nix flake and plugin version for v0.2.0" ]
  [ "$(origin rev-parse v0.2.0^{commit})" = "$(origin rev-parse main)" ]
  [ "$(origin show main:.claude-plugin/plugin.json | jq -r .version)" = "0.2.0" ]
  origin show main:nix/package.nix | grep -q 'version = "0.2.0"'
  [ "$(cat "$PUSH_LOG")" = $'refs/heads/main\nrefs/tags/v0.2.0' ]
  grep -q '^make release-check$' "$LOG"
}

@test "prerelease tags HEAD and leaves the stable metadata alone" {
  run scripts/release.sh v0.2.0-rc.1
  [ "$status" -eq 0 ]

  [ "$(origin rev-parse main)" = "$BASE" ]
  [ "$(origin rev-parse v0.2.0-rc.1^{commit})" = "$BASE" ]
  [ "$(plugin_version)" = "0.1.0" ]
  [ "$(cat "$PUSH_LOG")" = "refs/tags/v0.2.0-rc.1" ]
  ! grep -q update-nix-flake "$LOG"
}

@test "dry run checks everything and pushes nothing" {
  run scripts/release.sh 0.2.0 --dry-run
  [ "$status" -eq 0 ]
  [[ "$output" == *"Dry run complete"* ]]

  [ "$(git rev-parse HEAD)" = "$BASE" ]
  [ "$(origin rev-parse main)" = "$BASE" ]
  ! origin rev-parse -q --verify refs/tags/v0.2.0 >/dev/null
  [ "$(plugin_version)" = "0.1.0" ]
  [ ! -s "$PUSH_LOG" ]
  grep -q '^make release-check$' "$LOG"
  ! grep -q update-nix-flake "$LOG"
}

@test "refuses a tag that exists at another commit before touching main" {
  git tag -a v0.2.0 -m "Release v0.2.0" v0.1.0
  git push -q origin v0.2.0
  : > "$PUSH_LOG"

  run scripts/release.sh 0.2.0
  [ "$status" -eq 1 ]
  [[ "$output" == *"Tag v0.2.0 already exists at"*"(not HEAD)"* ]]

  [ "$(origin rev-parse main)" = "$BASE" ]
  [ "$(git rev-parse HEAD)" = "$BASE" ]
  [ "$(plugin_version)" = "0.1.0" ]
  [ ! -s "$PUSH_LOG" ]
  [ ! -f "$LOG" ]
}

@test "sees a tag that only exists on origin" {
  git tag -a v0.2.0 -m "Release v0.2.0" v0.1.0
  git push -q origin v0.2.0
  git tag -d v0.2.0 >/dev/null
  : > "$PUSH_LOG"

  run scripts/release.sh 0.2.0
  [ "$status" -eq 1 ]
  [[ "$output" == *"Tag v0.2.0 already exists at"* ]]
  [ "$(origin rev-parse main)" = "$BASE" ]
  [ ! -s "$PUSH_LOG" ]
}

@test "refuses a stable version older than the latest stable tag" {
  git tag -a v0.3.0 -m "Release v0.3.0"
  git push -q origin v0.3.0
  : > "$PUSH_LOG"

  run scripts/release.sh 0.2.0
  [ "$status" -eq 1 ]
  [[ "$output" == *"older than the latest stable release 0.3.0"* ]]

  [ "$(origin rev-parse main)" = "$BASE" ]
  [ "$(plugin_version)" = "0.1.0" ]
  [ ! -s "$PUSH_LOG" ]
  [ ! -f "$LOG" ]
}

@test "compares versions numerically, not lexically" {
  git tag -a v0.9.0 -m "Release v0.9.0"
  git push -q origin v0.9.0

  run scripts/release.sh 0.10.0
  [ "$status" -eq 0 ]
  [ "$(origin rev-parse v0.10.0^{commit})" = "$(origin rev-parse main)" ]
}

@test "a failed nix update restores the metadata and leaves the tree clean" {
  cat > scripts/update-nix-flake.sh <<'STUB'
#!/usr/bin/env bash
printf '{\n  version = "%s";\n}\n' "$1" > nix/package.nix
echo "ERROR: nix build failed, and not because of the vendorHash"
exit 1
STUB
  git add scripts/update-nix-flake.sh
  git commit -qm "Break the nix update"
  git push -q origin main
  : > "$PUSH_LOG"
  BASE=$(git rev-parse HEAD)

  run scripts/release.sh 0.2.0
  [ "$status" -eq 1 ]
  [[ "$output" == *"update-nix-flake.sh failed (exit 1)"* ]]

  [ -z "$(git status --porcelain)" ]
  grep -q 'version = "0.1.0"' nix/package.nix
  [ "$(git rev-parse HEAD)" = "$BASE" ]
  [ "$(origin rev-parse main)" = "$BASE" ]
  [ ! -s "$PUSH_LOG" ]
}

@test "a failed plugin stamp restores the metadata and leaves the tree clean" {
  cat > scripts/stamp-plugin-version.sh <<'STUB'
#!/usr/bin/env bash
echo '{ "broken":' > .claude-plugin/plugin.json
exit 1
STUB
  git add scripts/stamp-plugin-version.sh
  git commit -qm "Break the plugin stamp"
  git push -q origin main
  : > "$PUSH_LOG"
  BASE=$(git rev-parse HEAD)

  run scripts/release.sh 0.2.0
  [ "$status" -eq 1 ]

  # The nix update succeeded before the stamp failed; both files come back.
  [ -z "$(git status --porcelain)" ]
  grep -q 'version = "0.1.0"' nix/package.nix
  [ "$(plugin_version)" = "0.1.0" ]
  [ "$(git rev-parse HEAD)" = "$BASE" ]
  [ "$(origin rev-parse main)" = "$BASE" ]
  [ ! -s "$PUSH_LOG" ]
}

@test "prerelease below the latest stable is not a downgrade of any channel" {
  git tag -a v0.3.0 -m "Release v0.3.0"
  git push -q origin v0.3.0

  run scripts/release.sh 0.2.0-rc.1
  [ "$status" -eq 0 ]
  [ "$(origin rev-parse main)" = "$BASE" ]
}

@test "re-running a stable release whose tag is already at HEAD pushes nothing new" {
  scripts/release.sh 0.2.0 >/dev/null
  PREPPED=$(git rev-parse HEAD)
  : > "$PUSH_LOG"

  run scripts/release.sh 0.2.0
  [ "$status" -eq 0 ]
  [[ "$output" == *"Tag v0.2.0 already exists at HEAD"* ]]
  [ "$(git rev-parse HEAD)" = "$PREPPED" ]
  [ "$(origin rev-parse main)" = "$PREPPED" ]
  [ "$(origin rev-parse v0.2.0^{commit})" = "$PREPPED" ]
  [ ! -s "$PUSH_LOG" ]
}

@test "refuses to release from a branch other than the default" {
  git checkout -q -b topic
  run scripts/release.sh 0.2.0
  [ "$status" -eq 1 ]
  [[ "$output" == *"Not on main"* ]]
  [ ! -s "$PUSH_LOG" ]
}

@test "rejects malformed versions" {
  run scripts/release.sh 0.2
  [ "$status" -eq 1 ]
  [[ "$output" == *"Invalid version"* ]]
}
