#!/usr/bin/env bash
# test-sync-skills.sh — Run sync-skills.sh as two CLIs against one throwaway
# basecamp/skills checkout and prove neither deletes the other's skills.
#
# The target starts in the state basecamp/skills#5 left it: basecamp-cli's skills
# and the shared .managed-skills listing them. Then hey-cli and basecamp-cli sync
# in turn, one loses a skill, a pre-fix sibling rewrites the legacy manifest, and
# two manifests claim one name — after each step both sources' skills must be
# where they belong. Everything runs with DRY_RUN=local and SKILLS_TARGET, so no
# network and no token.
#
# Usage: scripts/test-sync-skills.sh            (tests scripts/sync-skills.sh)
#        SYNC_SCRIPT=path/to/sync-skills.sh scripts/test-sync-skills.sh

set -euo pipefail

here=$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)
SYNC_SCRIPT="${SYNC_SCRIPT:-${here}/sync-skills.sh}"
[[ -x "$SYNC_SCRIPT" ]] || { echo "ERROR: ${SYNC_SCRIPT} is not an executable script" >&2; exit 1; }

work=$(mktemp -d)
trap 'rm -rf "$work"' EXIT

# The test's own git calls see only this config; the script brings its own.
export GIT_CONFIG_GLOBAL="${work}/gitconfig" GIT_CONFIG_NOSYSTEM=1
printf '[user]\n\tname = test\n\temail = test@example.com\n' > "$GIT_CONFIG_GLOBAL"

target="${work}/target"
out="${work}/out"
failures=0

# --- Assertions ---

ok() { echo "ok - $*"; }
not_ok() { echo "not ok - $*"; failures=$((failures + 1)); }

assert() {  # description, command...
  local desc="$1"
  shift
  if "$@"; then ok "$desc"; else not_ok "$desc"; fi
}

assert_skill() { assert "skills/$1 present" test -f "${target}/skills/$1/SKILL.md"; }
assert_no_skill() { assert "skills/$1 absent" test ! -e "${target}/skills/$1"; }
assert_no_path() { assert "$1 not published" test ! -e "${target}/skills/$1"; }
assert_content() {  # path, expected content
  assert "$1 holds '$2'" test "$(cat "${target}/$1")" = "$2"
}
assert_manifest() {  # source, names...
  local source="$1" want
  shift
  want=$(printf '%s\n' "$@")
  assert ".managed-skills.${source} lists exactly: $*" test "$(cat "${target}/.managed-skills.${source}")" = "$want"
}
assert_no_manifest() { assert ".managed-skills.$1 absent" test ! -e "${target}/.managed-skills.$1"; }
assert_tombstone() {
  if [[ -f "${target}/.managed-skills" ]] && ! grep -qv '^#' "${target}/.managed-skills" && grep -q 'Superseded' "${target}/.managed-skills"; then
    ok ".managed-skills is the comment-only tombstone"
  else
    not_ok ".managed-skills is the comment-only tombstone"
  fi
}
assert_author() { assert "last commit authored by $1[bot]" test "$(git -C "$target" log -1 --format=%an)" = "$1[bot]"; }
assert_clean() { assert "target working tree committed clean" test -z "$(git -C "$target" status --porcelain)"; }
assert_output() { assert "output says: $1" grep -q -- "$1" "$out"; }
assert_head() {  # expected sha, description
  assert "$2" test "$(git -C "$target" rev-parse HEAD)" = "$1"
}

# --- Running the script ---

sync() {  # source, fixture, [VAR=value...]
  local source="$1" fixture="$2"
  shift 2
  if env "$@" SYNC_SOURCE="$source" SKILLS_SOURCE="${fixture}/skills" \
       RELEASE_TAG=v9.9.9 SOURCE_SHA=0123abcd "$SYNC_SCRIPT" > "$out" 2>&1; then
    ok "sync as ${source} succeeded"
  else
    not_ok "sync as ${source} succeeded"
    sed 's/^/    /' "$out"
  fi
}

sync_local() { sync "$1" "$2" DRY_RUN=local SKILLS_TARGET="$target"; }

sync_expecting_failure() {  # source, fixture, [VAR=value...]
  local source="$1" fixture="$2"
  shift 2
  if env "$@" SYNC_SOURCE="$source" SKILLS_SOURCE="${fixture}/skills" \
       RELEASE_TAG=v9.9.9 SOURCE_SHA=0123abcd "$SYNC_SCRIPT" > "$out" 2>&1; then
    not_ok "sync as ${source} refused"
    sed 's/^/    /' "$out"
  else
    ok "sync as ${source} refused"
  fi
}

# --- Fixtures ---

write_skill() {  # dir, content
  mkdir -p "$1"
  echo "$2" > "$1/SKILL.md"
}

a="${work}/hey-cli"
b="${work}/basecamp-cli"

write_skill "${a}/skills/hey" "hey v2"
mkdir -p "${a}/skills/hey/reference" "${a}/skills/hey/.cache"
echo "nested" > "${a}/skills/hey/reference/commands.md"
echo "package hey" > "${a}/skills/hey/embed.go"
echo "secret" > "${a}/skills/hey/.env"
echo "cached" > "${a}/skills/hey/.cache/index"
write_skill "${a}/skills/hey-doctor" "hey-doctor v2"

write_skill "${b}/skills/basecamp" "basecamp v2"
write_skill "${b}/skills/basecamp-doctor" "basecamp-doctor v2"

# The target as basecamp/skills#5 left it: only basecamp-cli's skills survive,
# and the shared manifest names them.
git init -q -b main "$target"
git -C "$target" remote add origin https://github.com/basecamp/skills.git
write_skill "${target}/skills/basecamp" "basecamp v1"
write_skill "${target}/skills/basecamp-doctor" "basecamp-doctor v1"
printf 'basecamp\nbasecamp-doctor\n' > "${target}/.managed-skills"
echo "# skills" > "${target}/README.md"
git -C "$target" add -A
git -C "$target" commit -q -m "State after basecamp/skills#5"

# --- Interleaved syncs: A, B, A, B ---

echo "# hey-cli syncs first: restores its skills, touches nothing else"
sync_local hey-cli "$a"
assert_skill hey
assert_skill hey-doctor
assert_skill basecamp
assert_skill basecamp-doctor
assert_content skills/basecamp/SKILL.md "basecamp v1"
assert_content skills/hey/reference/commands.md "nested"
assert_no_path hey/embed.go
assert_no_path hey/.env
assert_no_path hey/.cache
assert_manifest hey-cli hey hey-doctor
assert_no_manifest basecamp-cli
assert_tombstone
assert_author hey-cli
assert_clean
assert_output "first run for hey-cli, removing nothing"

echo "# basecamp-cli syncs: refreshes its skills, leaves hey-cli's"
sync_local basecamp-cli "$b"
assert_skill hey
assert_skill hey-doctor
assert_skill basecamp
assert_skill basecamp-doctor
assert_content skills/basecamp/SKILL.md "basecamp v2"
assert_manifest hey-cli hey hey-doctor
assert_manifest basecamp-cli basecamp basecamp-doctor
assert_tombstone
assert_author basecamp-cli
assert_clean

echo "# both sync again with nothing new: no commits, nothing lost"
head_before=$(git -C "$target" rev-parse HEAD)
sync_local hey-cli "$a"
assert_output "No changes to commit"
sync_local basecamp-cli "$b"
assert_output "No changes to commit"
assert_head "$head_before" "HEAD unchanged by the no-op syncs"
assert_skill hey
assert_skill hey-doctor
assert_skill basecamp
assert_skill basecamp-doctor
assert_manifest hey-cli hey hey-doctor
assert_manifest basecamp-cli basecamp basecamp-doctor
assert_tombstone

# --- hey-cli drops a skill: only that directory goes ---

echo "# hey-cli drops hey-doctor"
rm -rf "${a}/skills/hey-doctor"
sync_local hey-cli "$a"
assert_output "Removing stale skill: hey-doctor"
assert_no_skill hey-doctor
assert_skill hey
assert_skill basecamp
assert_skill basecamp-doctor
assert_manifest hey-cli hey
assert_manifest basecamp-cli basecamp basecamp-doctor
assert_author hey-cli

# --- A pre-fix sibling rewrote the legacy manifest: still nothing of B's goes ---

echo "# a pre-fix basecamp-cli rewrites .managed-skills with its own names"
printf 'basecamp\nbasecamp-doctor\n' > "${target}/.managed-skills"
git -C "$target" commit -q -am "Sync skills from basecamp-cli v0.0.0 (pre-fix script)"
sync_local hey-cli "$a"
assert_skill basecamp
assert_skill basecamp-doctor
assert_skill hey
assert_tombstone
assert_manifest basecamp-cli basecamp basecamp-doctor

# --- Two manifests claim one name: removal is refused with a warning ---

echo "# hey-cli's manifest also lists basecamp, which basecamp-cli owns"
printf 'basecamp\nhey\n' > "${target}/.managed-skills.hey-cli"
git -C "$target" commit -q -am "Collision: hey-cli claims basecamp"
sync_local hey-cli "$a"
assert_skill basecamp
assert_content skills/basecamp/SKILL.md "basecamp v2"
assert_output "WARNING: skills/basecamp is no longer in hey-cli's skills but basecamp-cli lists it"
assert_manifest hey-cli hey
assert_manifest basecamp-cli basecamp basecamp-doctor

# --- Publishing a name another source owns is refused before anything changes ---

echo "# hey-cli ships a skill named basecamp"
write_skill "${a}/skills/basecamp" "hey-cli's basecamp"
head_before=$(git -C "$target" rev-parse HEAD)
sync_expecting_failure hey-cli "$a" DRY_RUN=local SKILLS_TARGET="$target"
assert_output "ERROR: skills/basecamp is published by basecamp-cli"
assert_content skills/basecamp/SKILL.md "basecamp v2"
assert_head "$head_before" "HEAD unchanged by the refused sync"
assert_clean
rm -rf "${a}/skills/basecamp"

# --- DRY_RUN=remote applies and shows the diff but commits nothing ---

echo "# DRY_RUN=remote against the checkout"
echo "hey v3" > "${a}/skills/hey/SKILL.md"
head_before=$(git -C "$target" rev-parse HEAD)
sync hey-cli "$a" DRY_RUN=remote SKILLS_TARGET="$target"
assert_output "DRY_RUN=remote: skipping commit and push"
assert_output "+hey v3"
assert_head "$head_before" "HEAD unchanged by DRY_RUN=remote"
git -C "$target" reset -q --hard

# --- DRY_RUN=local with no target: no network, lists what would be published ---

echo "# DRY_RUN=local without SKILLS_TARGET"
sync hey-cli "$a" DRY_RUN=local
assert_output "skills/hey/SKILL.md"
assert_output "skills/hey/reference/commands.md"
assert_output "No network operations performed"
if grep -q "embed.go" "$out"; then not_ok "preview leaves out embed.go"; else ok "preview leaves out embed.go"; fi

# --- Another publisher pushes first: the push is retried after a rebase ---

echo "# a concurrent publisher wins the race to origin"
origin="${work}/origin.git"
git init -q --bare -b main "$origin"
git -C "$target" push -q "$origin" main
sibling="${work}/sibling"
git clone -q "$origin" "$sibling"
echo "# skills (sibling)" > "${sibling}/README.md"
git -C "$sibling" commit -q -am "Sync skills from basecamp-cli v0.0.1 (concurrent)"
git -C "$sibling" push -q origin main
echo "hey v4" > "${a}/skills/hey/SKILL.md"
# A real push, with github.com/basecamp/skills routed to the local bare repo through
# the environment — the script's private gitconfig cannot hide that.
sync hey-cli "$a" SKILLS_TARGET="$target" \
  GIT_CONFIG_COUNT=1 "GIT_CONFIG_KEY_0=url.${origin}.insteadOf" GIT_CONFIG_VALUE_0=https://github.com/basecamp/skills.git
assert_output "Push rejected"
assert_output "Skills synced to basecamp/skills"
assert "origin main holds the sibling's commit then the sync" \
  test "$(git -C "$origin" log --format=%s -2 main | tr '\n' '|')" = "Sync skills from hey-cli v9.9.9|Sync skills from basecamp-cli v0.0.1 (concurrent)|"
assert_content skills/hey/SKILL.md "hey v4"
assert_content README.md "# skills (sibling)"
assert_clean

echo "# a concurrent publisher claims a name this source ships: the retry refuses"
git -C "$sibling" pull -q origin main
printf 'basecamp\nbasecamp-doctor\nhey\n' > "${sibling}/.managed-skills.basecamp-cli"
git -C "$sibling" commit -q -am "Collision: basecamp-cli claims hey (concurrent)"
git -C "$sibling" push -q origin main
echo "hey v5" > "${a}/skills/hey/SKILL.md"
sync_expecting_failure hey-cli "$a" SKILLS_TARGET="$target" \
  GIT_CONFIG_COUNT=1 "GIT_CONFIG_KEY_0=url.${origin}.insteadOf" GIT_CONFIG_VALUE_0=https://github.com/basecamp/skills.git
assert_output "Push rejected"
assert_output "ERROR: skills/hey is published by basecamp-cli"
assert "origin main tip is the sibling's commit" \
  test "$(git -C "$origin" log -1 --format=%s main)" = "Collision: basecamp-cli claims hey (concurrent)"
assert_content skills/hey/SKILL.md "hey v4"
assert_clean
git -C "$sibling" checkout -q HEAD~1 -- .managed-skills.basecamp-cli
git -C "$sibling" commit -q -am "basecamp-cli drops its claim on hey"
git -C "$sibling" push -q origin main
git -C "$target" fetch -q "$origin" main
git -C "$target" reset -q --hard FETCH_HEAD

# --- Safety asserts on the checkout ---

echo "# a checkout with uncommitted changes is refused"
echo "stray" > "${target}/stray.txt"
sync_expecting_failure hey-cli "$a" DRY_RUN=local SKILLS_TARGET="$target"
assert_output "has uncommitted changes"
rm "${target}/stray.txt"

echo "# a checkout that is not basecamp/skills on main is refused"
wrong="${work}/wrong-remote"
git init -q -b main "$wrong"
git -C "$wrong" remote add origin https://github.com/basecamp/other.git
git -C "$wrong" commit -q --allow-empty -m "init"
sync_expecting_failure hey-cli "$a" DRY_RUN=local SKILLS_TARGET="$wrong"
assert_output "does not point to github.com/basecamp/skills"

git -C "$target" remote set-url --push origin https://github.com/someone/skills.git
sync_expecting_failure hey-cli "$a" DRY_RUN=local SKILLS_TARGET="$target"
assert_output "origin pushurl 'https://github.com/someone/skills.git' does not point to github.com/basecamp/skills"
git -C "$target" config --unset remote.origin.pushurl

git -C "$target" checkout -q -b not-main
sync_expecting_failure hey-cli "$a" DRY_RUN=local SKILLS_TARGET="$target"
assert_output "checked-out branch is 'not-main', expected 'main'"
git -C "$target" checkout -q main

# --- Verdict ---

echo ""
if [[ "$failures" -eq 0 ]]; then
  echo "sync-skills: all assertions passed"
else
  echo "sync-skills: ${failures} assertion(s) failed" >&2
  exit 1
fi
