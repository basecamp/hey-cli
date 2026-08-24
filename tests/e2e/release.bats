#!/usr/bin/env bats
#
# scripts/release.sh against a real git repo with a local bare origin. The
# script mutates main (the stable metadata commit) before it creates the tag,
# so what matters is ordering: a release that is going to be refused must be
# refused before main is touched, and the prep commit must reach origin before
# the tag that the release workflow builds from.
#
# make is stubbed; both metadata stampers are the real ones. A deliberately
# failing update-nix-flake.sh stub proves that the release path never invokes
# the Docker-backed hash updater. A pre-receive hook on the bare origin records
# every ref it receives, in order, which is the only way to observe the push
# sequence after the fact.

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
  cp "$REPO_ROOT/scripts/release.sh" \
     "$REPO_ROOT/scripts/stamp-nix-version.sh" \
     "$REPO_ROOT/scripts/stamp-plugin-version.sh" scripts/
  cat > scripts/update-nix-flake.sh <<'STUB'
#!/usr/bin/env bash
echo "update-nix-flake $1" >> "$LOG"
echo "release invoked the Docker-backed Nix updater" >&2
exit 99
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
  # Git authorship tooling may finish writing notes just after a fixture
  # command returns. Retry rather than turning that unrelated write into a
  # release-test failure on macOS.
  for _ in 1 2 3; do
    rm -rf "$WORK" 2>/dev/null && return 0
    sleep 0.1
  done
  rm -rf "$WORK" 2>/dev/null || true
  return 0
}

origin() { git --git-dir="$WORK/origin.git" "$@"; }
plugin_version() { jq -r .version .claude-plugin/plugin.json; }
# The fixture records every pushed ref, but authorship tooling may add its own
# notes refs. Only main and release tags belong to the protocol under test.
release_pushes() { grep -E '^refs/(heads/main|tags/v)' "$PUSH_LOG" || true; }

@test "stable release commits and pushes the metadata before pushing the tag" {
  run scripts/release.sh 0.2.0
  [ "$status" -eq 0 ]

  [ "$(origin log -1 --format=%s main)" = "Update nix flake and plugin version for v0.2.0" ]
  [ "$(origin rev-parse v0.2.0^{commit})" = "$(origin rev-parse main)" ]
  [ "$(origin show main:.claude-plugin/plugin.json | jq -r .version)" = "0.2.0" ]
  origin show main:nix/package.nix | grep -q 'version = "0.2.0"'
  [ "$(release_pushes)" = $'refs/heads/main\nrefs/tags/v0.2.0' ]
  grep -q '^make release-check$' "$LOG"
  ! grep -q update-nix-flake "$LOG"
}

@test "prerelease tags HEAD and leaves the stable metadata alone" {
  run scripts/release.sh v0.2.0-rc.1
  [ "$status" -eq 0 ]

  [ "$(origin rev-parse main)" = "$BASE" ]
  [ "$(origin rev-parse v0.2.0-rc.1^{commit})" = "$BASE" ]
  [ "$(plugin_version)" = "0.1.0" ]
  [ "$(release_pushes)" = "refs/tags/v0.2.0-rc.1" ]
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
  [ -z "$(release_pushes)" ]
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
  [ -z "$(release_pushes)" ]
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
  [ -z "$(release_pushes)" ]
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
  [ -z "$(release_pushes)" ]
  [ ! -f "$LOG" ]
}

@test "compares versions numerically, not lexically" {
  git tag -a v0.9.0 -m "Release v0.9.0"
  git push -q origin v0.9.0

  run scripts/release.sh 0.10.0
  [ "$status" -eq 0 ]
  [ "$(origin rev-parse v0.10.0^{commit})" = "$(origin rev-parse main)" ]
}

@test "a failed nix version stamp restores the metadata and leaves the tree clean" {
  cat > scripts/stamp-nix-version.sh <<'STUB'
#!/usr/bin/env bash
printf '{\n  version = "%s";\n}\n' "$1" > nix/package.nix
echo "ERROR: nix version stamp failed"
exit 1
STUB
  git add scripts/stamp-nix-version.sh
  git commit -qm "Break the nix version stamp"
  git push -q origin main
  : > "$PUSH_LOG"
  BASE=$(git rev-parse HEAD)

  run scripts/release.sh 0.2.0
  [ "$status" -eq 1 ]
  [[ "$output" == *"nix version stamp failed"* ]]

  [ -z "$(git status --porcelain)" ]
  grep -q 'version = "0.1.0"' nix/package.nix
  [ "$(git rev-parse HEAD)" = "$BASE" ]
  [ "$(origin rev-parse main)" = "$BASE" ]
  [ -z "$(release_pushes)" ]
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

  # The Nix version stamp succeeded before the plugin stamp failed; both files
  # come back.
  [ -z "$(git status --porcelain)" ]
  grep -q 'version = "0.1.0"' nix/package.nix
  [ "$(plugin_version)" = "0.1.0" ]
  [ "$(git rev-parse HEAD)" = "$BASE" ]
  [ "$(origin rev-parse main)" = "$BASE" ]
  [ -z "$(release_pushes)" ]
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
  [ -z "$(release_pushes)" ]
}

@test "refuses a hand-pushed stable tag at an unstamped HEAD before touching main" {
  # The recovery-run trap: an operator hand-pushes the tag, GoReleaser's stamp
  # check rejects it, and they re-run the script. The tag sits at HEAD, so the
  # existing-elsewhere check passes — but HEAD's metadata was never stamped.
  # Pushing the metadata commit would move main past the tag and leave a
  # proxy-cached tag that can never be reused; the script must refuse first.
  git tag -a v0.2.0 -m "Release v0.2.0"
  git push -q origin v0.2.0
  : > "$PUSH_LOG"

  run scripts/release.sh 0.2.0
  [ "$status" -eq 1 ]
  [[ "$output" == *"release metadata is not stamped"* ]]
  [[ "$output" == *"choose a new version"* ]]
  grep -q 'version = "0.1.0"' nix/package.nix
  [ "$(git rev-parse HEAD)" = "$BASE" ]
  [ "$(origin rev-parse main)" = "$BASE" ]
  [ -z "$(release_pushes)" ]
}

@test "an existing tag elsewhere is refused without suggesting a tag move" {
  git commit -q --allow-empty -m "later work"
  ELSEWHERE=$(git rev-parse HEAD~0)
  git tag -a v0.2.0 -m "Release v0.2.0" HEAD
  git push -q origin v0.2.0 main
  git commit -q --allow-empty -m "even later" && git push -q origin main
  : > "$PUSH_LOG"

  run scripts/release.sh 0.2.0
  [ "$status" -eq 1 ]
  [[ "$output" == *"must not move"* ]]
  [[ "$output" == *"choose a new version"* ]]
  [[ "$output" != *"Delete it first"* ]]
}

@test "refuses to release from a branch other than the default" {
  git checkout -q -b topic
  run scripts/release.sh 0.2.0
  [ "$status" -eq 1 ]
  [[ "$output" == *"Not on main"* ]]
  [ -z "$(release_pushes)" ]
}

@test "rejects malformed versions" {
  run scripts/release.sh 0.2
  [ "$status" -eq 1 ]
  [[ "$output" == *"Invalid version"* ]]
}

@test "a failed release commit restores the metadata, index included, and the next attempt is not blocked" {
  cat > .git/hooks/pre-commit <<'HOOK'
#!/bin/sh
echo "pre-commit: gpg signing failed" >&2
exit 1
HOOK
  chmod +x .git/hooks/pre-commit

  run scripts/release.sh 0.2.0
  [ "$status" -eq 1 ]

  # git add ran before the commit failed; the rollback must unstage too.
  [ -z "$(git status --porcelain)" ]
  grep -q 'version = "0.1.0"' nix/package.nix
  [ "$(plugin_version)" = "0.1.0" ]
  [ "$(git rev-parse HEAD)" = "$BASE" ]
  [ "$(origin rev-parse main)" = "$BASE" ]
  [ -z "$(release_pushes)" ]

  rm .git/hooks/pre-commit
  run scripts/release.sh 0.2.0
  [ "$status" -eq 0 ]
  [ "$(origin rev-parse v0.2.0^{commit})" = "$(origin rev-parse main)" ]
}

@test "rejects leading-zero version fields" {
  git tag -a v0.9.0 -m "Release v0.9.0"
  git push -q origin v0.9.0
  : > "$PUSH_LOG"

  # 0.08.0 would read as older than 0.9.0 to a human, but bash arithmetic
  # rejects 08 as octal, so a naive field compare lets it through.
  run scripts/release.sh 0.08.0
  [ "$status" -eq 1 ]
  [[ "$output" == *"Invalid version"* ]]

  [ "$(origin rev-parse main)" = "$BASE" ]
  [ "$(plugin_version)" = "0.1.0" ]
  [ -z "$(release_pushes)" ]
  [ ! -f "$LOG" ]
}

@test "orders an existing leading-zero tag numerically when refusing a downgrade" {
  # The script can no longer create leading-zero tags, but a hand-pushed one
  # can still be the latest per git's version sort. Its fields must compare
  # as decimal, not as broken octal that would wave the downgrade through.
  git tag -a v1.09.0 -m "Release v1.09.0"
  git push -q origin v1.09.0
  : > "$PUSH_LOG"

  run scripts/release.sh 1.8.0
  [ "$status" -eq 1 ]
  [[ "$output" == *"older than the latest stable release 1.09.0"* ]]

  [ "$(origin rev-parse main)" = "$BASE" ]
  [ "$(plugin_version)" = "0.1.0" ]
  [ -z "$(release_pushes)" ]
  [ ! -f "$LOG" ]
}

@test "a tag pushed by someone else during the checks reaches neither main nor the tag" {
  # The tag check runs before make release-check; the pushes run after it.
  # Another operator landing the same tag in that window must not leave main
  # carrying stable metadata for a tag that points somewhere else — the tag
  # cannot be moved, so that main would be stuck. Nothing may reach origin.
  git clone -q "$WORK/origin.git" "$WORK/other"
  git -C "$WORK/other" config user.email other@example.com
  git -C "$WORK/other" config user.name "Other Operator"
  cat > "$STUB_DIR/make" <<STUB
#!/usr/bin/env bash
echo "make \$*" >> "$LOG"
git -C "$WORK/other" tag -a v0.2.0 -m "Release v0.2.0" v0.1.0
git -C "$WORK/other" push -q origin v0.2.0
STUB
  OTHER=$(git -C "$WORK/other" rev-parse v0.1.0^{commit})

  run scripts/release.sh 0.2.0
  [ "$status" -ne 0 ]
  [[ "$output" == *"v0.2.0"* ]]

  [ "$(origin rev-parse main)" = "$BASE" ]
  [ "$(origin rev-parse v0.2.0^{commit})" = "$OTHER" ]
  [ "$(release_pushes)" = "refs/tags/v0.2.0" ]

  # The local clone is left where a re-run (with a new version) can start:
  # no unpushed prep commit, no stray local tag, clean tree.
  [ -z "$(git status --porcelain)" ]
  [ "$(git rev-parse HEAD)" = "$BASE" ]
  ! git rev-parse -q --verify refs/tags/v0.2.0 >/dev/null || [ "$(git rev-parse v0.2.0^{commit})" = "$OTHER" ]
  [ "$(plugin_version)" = "0.1.0" ]
}

@test "main moved by someone else during the checks reaches neither main nor the tag" {
  git clone -q "$WORK/origin.git" "$WORK/other"
  git -C "$WORK/other" config user.email other@example.com
  git -C "$WORK/other" config user.name "Other Operator"
  cat > "$STUB_DIR/make" <<STUB
#!/usr/bin/env bash
echo "make \$*" >> "$LOG"
git -C "$WORK/other" commit -q --allow-empty -m "Concurrent work"
git -C "$WORK/other" push -q origin main
STUB
  : > "$PUSH_LOG"

  run scripts/release.sh 0.2.0
  [ "$status" -ne 0 ]

  [ "$(origin rev-parse main)" = "$(git -C "$WORK/other" rev-parse HEAD)" ]
  ! origin rev-parse -q --verify refs/tags/v0.2.0 >/dev/null
  [ "$(release_pushes)" = "refs/heads/main" ]
  [ -z "$(git status --porcelain)" ]
  [ "$(git rev-parse HEAD)" = "$BASE" ]
  ! git rev-parse -q --verify refs/tags/v0.2.0 >/dev/null
}
