#!/usr/bin/env bash
# test-sync-skills.sh — Run sync-skills.sh as two CLIs against one throwaway
# basecamp/skills and prove neither deletes the other's skills.
#
# The target is a local bare repository the script clones from and pushes to
# through SKILLS_REPO_URL, exactly as it would basecamp/skills — so every run
# here is a real clone, commit and push, and the test reads the result back
# from a clone of its own. It starts in the state basecamp/skills#5 left it:
# basecamp-cli's skills and the shared .managed-skills listing them. Then
# hey-cli and basecamp-cli sync in turn, one loses a skill, a pre-fix sibling
# rewrites the legacy manifest, two manifests claim one name, and a sibling
# wins the race to push — after each step both sources' skills must be where
# they belong. No network and no token.
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

# A file:// URL rather than a path: git ignores --depth for a path, and the
# script's clone is shallow, so the retry has to fetch as it would from GitHub.
origin="${work}/origin.git"
origin_url="file://${origin}"
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
assert_output() { assert "output says: $1" grep -q -- "$1" "$out"; }
assert_head() {  # expected sha, description
  assert "$2" test "$(git -C "$origin" rev-parse main)" = "$1"
}

origin_head() { git -C "$origin" rev-parse main; }

# --- Running the script ---
#
# Every run clones origin afresh, as a release would clone basecamp/skills, and
# the target clone is brought to origin's tip afterwards for the assertions.

refresh_target() {
  git -C "$target" fetch -q origin main
  git -C "$target" reset -q --hard FETCH_HEAD
}

sync() {  # source, fixture, [VAR=value...]
  local source="$1" fixture="$2"
  shift 2
  if env SKILLS_REPO_URL="$origin_url" "$@" SYNC_SOURCE="$source" SKILLS_SOURCE="${fixture}/skills" \
       RELEASE_TAG=v9.9.9 SOURCE_SHA=0123abcd "$SYNC_SCRIPT" > "$out" 2>&1; then
    ok "sync as ${source} succeeded"
  else
    not_ok "sync as ${source} succeeded"
    sed 's/^/    /' "$out"
  fi
  refresh_target
}

sync_expecting_failure() {  # source, fixture, [VAR=value...]
  local source="$1" fixture="$2"
  shift 2
  if env SKILLS_REPO_URL="$origin_url" "$@" SYNC_SOURCE="$source" SKILLS_SOURCE="${fixture}/skills" \
       RELEASE_TAG=v9.9.9 SOURCE_SHA=0123abcd "$SYNC_SCRIPT" > "$out" 2>&1; then
    not_ok "sync as ${source} refused"
    sed 's/^/    /' "$out"
  else
    ok "sync as ${source} refused"
  fi
  refresh_target
}

# Commit the target's working tree and push it, as a hand-made or pre-fix commit
publish() {  # message
  git -C "$target" add -A
  git -C "$target" commit -q -m "$1"
  git -C "$target" push -q origin main
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
git init -q --bare -b main "$origin"
git init -q -b main "$target"
git -C "$target" remote add origin "$origin_url"
write_skill "${target}/skills/basecamp" "basecamp v1"
write_skill "${target}/skills/basecamp-doctor" "basecamp-doctor v1"
printf 'basecamp\nbasecamp-doctor\n' > "${target}/.managed-skills"
echo "# skills" > "${target}/README.md"
publish "State after basecamp/skills#5"

# --- Interleaved syncs: A, B, A, B ---

echo "# hey-cli syncs first: restores its skills, touches nothing else"
sync hey-cli "$a"
assert_output "Skills synced to basecamp/skills"
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
assert_output "first run for hey-cli, removing nothing"
assert "origin main is the seed commit then hey-cli's sync" \
  test "$(git -C "$origin" log --format=%s -2 main | tr '\n' '|')" = "Sync skills from hey-cli v9.9.9|State after basecamp/skills#5|"

echo "# basecamp-cli syncs: refreshes its skills, leaves hey-cli's"
sync basecamp-cli "$b"
assert_skill hey
assert_skill hey-doctor
assert_skill basecamp
assert_skill basecamp-doctor
assert_content skills/basecamp/SKILL.md "basecamp v2"
assert_manifest hey-cli hey hey-doctor
assert_manifest basecamp-cli basecamp basecamp-doctor
assert_tombstone
assert_author basecamp-cli

echo "# both sync again with nothing new: no commits, nothing lost"
head_before=$(origin_head)
sync hey-cli "$a"
assert_output "No changes to commit"
sync basecamp-cli "$b"
assert_output "No changes to commit"
assert_head "$head_before" "origin main unchanged by the no-op syncs"
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
sync hey-cli "$a"
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
publish "Sync skills from basecamp-cli v0.0.0 (pre-fix script)"
sync hey-cli "$a"
assert_skill basecamp
assert_skill basecamp-doctor
assert_skill hey
assert_tombstone
assert_manifest basecamp-cli basecamp basecamp-doctor

# --- Two manifests claim one name: removal is refused with a warning ---

echo "# hey-cli's manifest also lists basecamp, which basecamp-cli owns"
printf 'basecamp\nhey\n' > "${target}/.managed-skills.hey-cli"
publish "Collision: hey-cli claims basecamp"
sync hey-cli "$a"
assert_skill basecamp
assert_content skills/basecamp/SKILL.md "basecamp v2"
assert_output "WARNING: skills/basecamp is no longer in hey-cli's skills but basecamp-cli lists it"
assert_manifest hey-cli hey
assert_manifest basecamp-cli basecamp basecamp-doctor

# --- Publishing a name another source owns is refused before anything changes ---

echo "# hey-cli ships a skill named basecamp"
write_skill "${a}/skills/basecamp" "hey-cli's basecamp"
head_before=$(origin_head)
sync_expecting_failure hey-cli "$a"
assert_output "ERROR: skills/basecamp is published by basecamp-cli"
assert_content skills/basecamp/SKILL.md "basecamp v2"
assert_head "$head_before" "origin main unchanged by the refused sync"
rm -rf "${a}/skills/basecamp"

# --- DRY_RUN=remote clones and shows the diff but commits nothing ---

echo "# DRY_RUN=remote against origin"
echo "hey v3" > "${a}/skills/hey/SKILL.md"
head_before=$(origin_head)
sync hey-cli "$a" DRY_RUN=remote
assert_output "DRY_RUN=remote: skipping commit and push"
assert_output "+hey v3"
assert_head "$head_before" "origin main unchanged by DRY_RUN=remote"
assert_content skills/hey/SKILL.md "hey v2"

# --- DRY_RUN=local: no clone at all, lists what would be published ---

echo "# DRY_RUN=local never reaches the repository"
sync hey-cli "$a" DRY_RUN=local SKILLS_REPO_URL="file://${work}/nowhere.git"
assert_output "skills/hey/SKILL.md"
assert_output "skills/hey/reference/commands.md"
assert_output "No network operations performed"
if grep -q "embed.go" "$out"; then not_ok "preview leaves out embed.go"; else ok "preview leaves out embed.go"; fi

# --- Without a token, github.com is refused before anything is cloned ---

echo "# the default target needs SKILLS_TOKEN"
sync_expecting_failure hey-cli "$a" SKILLS_REPO_URL=https://github.com/basecamp/skills.git
assert_output "ERROR: SKILLS_TOKEN is required"
if grep -q "Cloning" "$out"; then not_ok "refused before cloning"; else ok "refused before cloning"; fi

# --- Another publisher pushes first: the sync is applied again from its tip ---
#
# The script clones afresh, so the sibling's push has to land after that clone and
# before the push. A post-commit hook, reached through the GIT_CONFIG_* environment
# the script's private gitconfig cannot hide, pushes the sibling's commit at exactly
# that moment; the script's push is then rejected as "fetch first".

sibling="${work}/sibling"
git clone -q "$origin_url" "$sibling"
hooks="${work}/hooks"
mkdir -p "$hooks"
printf '#!/usr/bin/env bash\ngit -C "%s" push -q origin main\n' "$sibling" > "${hooks}/post-commit"
chmod +x "${hooks}/post-commit"
racing() {  # source, fixture: sync with the sibling pushing between clone and push
  sync "$1" "$2" GIT_CONFIG_COUNT=1 GIT_CONFIG_KEY_0=core.hooksPath "GIT_CONFIG_VALUE_0=${hooks}"
}
racing_expecting_failure() {
  sync_expecting_failure "$1" "$2" GIT_CONFIG_COUNT=1 GIT_CONFIG_KEY_0=core.hooksPath "GIT_CONFIG_VALUE_0=${hooks}"
}

echo "# a concurrent publisher wins the race to origin"
echo "# skills (sibling)" > "${sibling}/README.md"
git -C "$sibling" commit -q -am "Sync skills from basecamp-cli v0.0.1 (concurrent)"
sibling_head=$(git -C "$sibling" rev-parse HEAD)
echo "hey v4" > "${a}/skills/hey/SKILL.md"
racing hey-cli "$a"
assert_output "Push rejected"
assert_output "Skills synced to basecamp/skills"
assert "origin main holds the sibling's commit then the sync" \
  test "$(git -C "$origin" log --format=%s -2 main | tr '\n' '|')" = "Sync skills from hey-cli v9.9.9|Sync skills from basecamp-cli v0.0.1 (concurrent)|"
assert "the sync commit was made on the sibling's tip" test "$(git -C "$origin" rev-parse main^)" = "$sibling_head"
assert_content skills/hey/SKILL.md "hey v4"
assert_content README.md "# skills (sibling)"
assert_author hey-cli

echo "# a concurrent publisher claims a name this source ships: the retry refuses"
git -C "$sibling" pull -q origin main
printf 'basecamp\nbasecamp-doctor\nhey\n' > "${sibling}/.managed-skills.basecamp-cli"
git -C "$sibling" commit -q -am "Collision: basecamp-cli claims hey (concurrent)"
sibling_head=$(git -C "$sibling" rev-parse HEAD)
echo "hey v5" > "${a}/skills/hey/SKILL.md"
racing_expecting_failure hey-cli "$a"
assert_output "Push rejected"
assert_output "ERROR: skills/hey is published by basecamp-cli"
assert_head "$sibling_head" "origin main tip is the sibling's commit"
assert_content skills/hey/SKILL.md "hey v4"

echo "# the sibling drops its claim: the next release publishes"
git -C "$sibling" checkout -q HEAD~1 -- .managed-skills.basecamp-cli
git -C "$sibling" commit -q -am "basecamp-cli drops its claim on hey"
git -C "$sibling" push -q origin main
sync hey-cli "$a"
assert_output "Skills synced to basecamp/skills"
assert_content skills/hey/SKILL.md "hey v5"
assert_manifest basecamp-cli basecamp basecamp-doctor
assert_manifest hey-cli hey

# --- Verdict ---

echo ""
if [[ "$failures" -eq 0 ]]; then
  echo "sync-skills: all assertions passed"
else
  echo "sync-skills: ${failures} assertion(s) failed" >&2
  exit 1
fi
