#!/usr/bin/env bash
# sync-skills.sh — Publish this CLI's skills to the basecamp/skills distribution repo.
#
# Runs from CI on a release tag. Clones basecamp/skills fresh into a temp directory,
# mirrors each skills/<name>/ tree (SKILL.md and its supporting files; no *.go, no
# dotfiles) into skills/<name>/ at its root — the layout `npx skills add
# basecamp/skills` reads — then commits as <source>[bot], pushes, and removes the
# clone. The script never adopts an existing checkout: the only commit it can push
# is the one it made, against the tip it cloned or fetched.
#
# Several CLIs publish into that one repo, so each owns a manifest of its own at the
# target root, .managed-skills.<source>, listing the skill names it has published,
# one per line. A skills/<name> directory is removed only when all of these hold:
# this source's manifest lists it, this source's current skill set no longer has
# it, and no other source's manifest claims it. A name two sources claim is a
# collision to settle upstream, never one a release resolves by deletion — the
# script warns and leaves the directory, and refuses outright to publish a name
# another source's manifest holds. Nothing else in the target is ever deleted: with
# no manifest yet, the first run publishes and removes nothing.
#
# The shared manifest the pre-fix scripts kept, .managed-skills, is rewritten on
# every run as a comment-only tombstone. The pre-fix script skips any line it cannot
# parse as a skill name, but treats a missing file as licence to own every skills/*
# directory — so the tombstone is what stops an un-upgraded sibling from deleting
# anyone's skills, whichever CLI upgrades first (basecamp/skills#5). One case is
# accepted: a skill a still-pre-fix sibling drops after the tombstone exists stays
# behind in the target (that script has no names left to delete by, and the
# sibling's own first run here removes nothing) — a lingering directory to remove
# by hand, which beats guessing ownership from the legacy file.
#
# Required env vars:
#   RELEASE_TAG      — the release tag (e.g. v1.2.3)
#   SOURCE_SHA       — the source commit SHA
#   SKILLS_TOKEN     — GitHub token with push access to basecamp/skills; not needed
#                      for DRY_RUN=local, nor when SKILLS_REPO_URL is not on github.com
#
# Optional env vars:
#   CLI_NAME         — this CLI's name; the publishing source is <CLI_NAME>-cli
#   SYNC_SOURCE      — the publishing repo's name (default: <CLI_NAME>-cli). Names the
#                      manifest, the bot and the commit; the test sets it to play
#                      another CLI
#   SKILLS_SOURCE    — directory holding the skills tree (default: skills). A manual
#                      recovery workflow can point it at a checkout of the release tag
#                      so the sync logic comes from a newer ref than the content
#   SKILLS_REPO_URL  — where basecamp/skills is cloned from and pushed to (default:
#                      https://github.com/basecamp/skills.git). The test points it at
#                      a local bare repository so a real push lands somewhere it can
#                      read back
#   DRY_RUN          — "local": no network; copy into an empty tmpdir and print what
#                      would be published.
#                      "remote": clone, apply, print the diff, and stop before
#                      committing
#

set -euo pipefail

CLI_NAME="${CLI_NAME:-hey}"
SYNC_SOURCE="${SYNC_SOURCE:-${CLI_NAME}-cli}"
RELEASE_TAG="${RELEASE_TAG:?RELEASE_TAG is required}"
SOURCE_SHA="${SOURCE_SHA:?SOURCE_SHA is required}"
SKILLS_SOURCE="${SKILLS_SOURCE:-skills}"
SKILLS_TOKEN="${SKILLS_TOKEN:-}"
DRY_RUN="${DRY_RUN:-}"

TARGET_REPO="basecamp/skills"
TARGET_BRANCH="main"
SKILLS_REPO_URL="${SKILLS_REPO_URL:-https://github.com/${TARGET_REPO}.git}"
SKILLS_SUBDIR="skills"
LEGACY_MANIFEST=".managed-skills"
MANIFEST="${LEGACY_MANIFEST}.${SYNC_SOURCE}"
# The commit's provenance line; GITHUB_REPOSITORY is exact in CI, the default holds
# for the basecamp org's <source> naming.
SOURCE_REPO="${GITHUB_REPOSITORY:-basecamp/${SYNC_SOURCE}}"

# --- Helpers ---

die() { echo "ERROR: $*" >&2; exit 1; }
warn() { echo "WARNING: $*" >&2; }

# A skill directory name or a source name: nothing a path could smuggle in.
plain_name() {
  [[ "$1" != "." && "$1" != ".." && "$1" =~ ^[a-zA-Z0-9._-]+$ ]]
}

in_list() {
  local needle="$1" item
  shift
  for item in "$@"; do
    [[ "$item" == "$needle" ]] && return 0
  done
  return 1
}

# Print the skill names a manifest lists, one per line. Blank and comment lines
# are skipped silently; anything else that is not a plain name, with a warning.
read_manifest() {
  local file="$1" line
  [[ -f "$file" ]] || return 0
  while IFS= read -r line || [[ -n "$line" ]]; do
    [[ -z "$line" || "$line" == \#* ]] && continue
    if plain_name "$line"; then
      echo "$line"
    else
      warn "skipping invalid entry in ${file##*/}: $line"
    fi
  done < "$file"
}

# Print the other source whose manifest claims a name, if any.
claimed_by_other() {
  local name="$1" file other listed
  for file in "${target}/${LEGACY_MANIFEST}".*; do
    [[ -f "$file" ]] || continue
    other="${file##*/"${LEGACY_MANIFEST}".}"
    [[ "$other" == "$SYNC_SOURCE" ]] && continue
    listed=$(read_manifest "$file")
    if grep -qxF -- "$name" <<< "$listed"; then
      echo "$other"
      return 0
    fi
  done
  return 1
}

# --- Validate the knobs ---

plain_name "$SYNC_SOURCE" || die "SYNC_SOURCE '$SYNC_SOURCE' is not a plain name"
case "$DRY_RUN" in
  ""|local|remote) ;;
  *) die "DRY_RUN must be unset, 'local' or 'remote', not '$DRY_RUN'" ;;
esac

# --- Discover skills ---

skill_names=()
for skill_md in "$SKILLS_SOURCE"/*/SKILL.md; do
  [[ -f "$skill_md" ]] || continue
  name=$(basename "$(dirname "$skill_md")")
  plain_name "$name" || die "skill directory '$name' is not a plain name"
  skill_names+=("$name")
done

[[ ${#skill_names[@]} -gt 0 ]] || die "no skills found under ${SKILLS_SOURCE}/*/SKILL.md"
echo "Found ${#skill_names[@]} skill(s) in ${SKILLS_SOURCE}/: ${skill_names[*]}"

# --- Copy skills, excluding *.go and dotfiles, preserving subdirectories ---

copy_skills() {
  local skills_dir="$1" name dest
  for name in "${skill_names[@]}"; do
    dest="${skills_dir}/${name}"
    rm -rf "${dest:?}"
    mkdir -p "$dest"
    (cd "${SKILLS_SOURCE}/${name}" && find . -type f ! -name '*.go' ! -name '.*' ! -path '*/.*/*' -print0) |
      while IFS= read -r -d '' file; do
        mkdir -p "${dest}/$(dirname "$file")"
        cp "${SKILLS_SOURCE}/${name}/${file}" "${dest}/${file}"
      done
  done
}

tmpdir=$(mktemp -d)
trap 'rm -rf "$tmpdir"' EXIT

# --- DRY_RUN=local: what would be published ---

if [[ "$DRY_RUN" == "local" ]]; then
  preview="${tmpdir}/preview"
  echo "DRY_RUN=local: copying skills into ${preview}"
  copy_skills "${preview}/${SKILLS_SUBDIR}"
  echo ""
  echo "=== Skills copied ==="
  find "$preview" -type f | LC_ALL=C sort | while read -r file; do
    echo "  ${file#"${preview}/"}"
  done
  echo ""
  echo "=== Diff (against empty baseline) ==="
  git -C "$preview" init -q
  git -C "$preview" add -A
  git -C "$preview" diff --cached --stat
  echo ""
  echo "DRY_RUN=local complete. No network operations performed."
  exit 0
fi

# --- Git configuration for the target ---
#
# A private global config for every git call below: the bot is the identity for
# the commit, and for the one a rejected push makes again, and the token goes in as
# a URL rewrite so it never appears in argv or in the remote URL. Only the user's
# global file is replaced (~/.gitconfig: identity, signing, credential helpers, hooks
# path); the system config and any GIT_CONFIG_COUNT/GIT_CONFIG_KEY_* settings in the
# environment still apply — the test's race case injects a hooks path that way.
export GIT_CONFIG_GLOBAL="${tmpdir}/gitconfig"
cat > "$GIT_CONFIG_GLOBAL" <<GITCFG
[user]
	name = ${SYNC_SOURCE}[bot]
	email = ${SYNC_SOURCE}[bot]@users.noreply.github.com
GITCFG
chmod 600 "$GIT_CONFIG_GLOBAL"
if [[ "$SKILLS_REPO_URL" == https://github.com/* ]]; then
  [[ -n "$SKILLS_TOKEN" ]] || die "SKILLS_TOKEN is required to push to ${SKILLS_REPO_URL} (set DRY_RUN=local for offline testing)"
  cat >> "$GIT_CONFIG_GLOBAL" <<GITCFG
[url "https://x-access-token:${SKILLS_TOKEN}@github.com/"]
	insteadOf = https://github.com/
GITCFG
fi

# --- Clone the target ---

target="${tmpdir}/skills"
echo "Cloning ${TARGET_REPO} into ${target}..."
git clone -q --depth 1 --branch "$TARGET_BRANCH" "$SKILLS_REPO_URL" "$target"

# --- Apply the sync to the target's working tree ---
#
# Every decision here is made against the tree as it stands, so a retry after a
# rejected push runs this again from the remote's new tip instead of replaying
# decisions made against a stale one.

apply_sync() {
  local name other
  local previously_published=()

  # Refuse a name another source has published
  for name in "${skill_names[@]}"; do
    if other=$(claimed_by_other "$name"); then
      die "skills/${name} is published by ${other} (listed in ${LEGACY_MANIFEST}.${other}); rename the skill or settle ownership upstream"
    fi
  done

  echo "Copying skills into ${target}/${SKILLS_SUBDIR}/..."
  copy_skills "${target}/${SKILLS_SUBDIR}"

  # Remove what this source published before and no longer has
  while IFS= read -r name; do
    previously_published+=("$name")
  done < <(read_manifest "${target}/${MANIFEST}")

  if [[ ! -f "${target}/${MANIFEST}" ]]; then
    echo "No ${MANIFEST} yet: first run for ${SYNC_SOURCE}, removing nothing"
  fi

  for name in ${previously_published[@]+"${previously_published[@]}"}; do
    in_list "$name" "${skill_names[@]}" && continue
    if other=$(claimed_by_other "$name"); then
      warn "skills/${name} is no longer in ${SYNC_SOURCE}'s skills but ${other} lists it in ${LEGACY_MANIFEST}.${other}; leaving it in place"
      continue
    fi
    if [[ -d "${target}/${SKILLS_SUBDIR}/${name}" ]]; then
      echo "Removing stale skill: ${name}"
      rm -rf "${target:?}/${SKILLS_SUBDIR}/${name}"
    fi
  done

  # This source's manifest, and the legacy tombstone
  printf '%s\n' "${skill_names[@]}" | LC_ALL=C sort > "${target}/${MANIFEST}"
  cat > "${target}/${LEGACY_MANIFEST}" <<'TOMBSTONE'
# Superseded by the per-source manifests (.managed-skills.<cli>), one per publishing CLI.
# Each CLI deletes only the skill directories listed in its own manifest.
# Kept so a CLI still running the pre-fix sync script deletes nothing: that script skips
# every line it cannot parse as a skill name and only deletes names it can.
TOMBSTONE

  git -C "$target" add -A
}

commit_sync() {
  git -C "$target" commit -q -m "$(cat <<EOF
Sync skills from ${SYNC_SOURCE} ${RELEASE_TAG}

Source: ${SOURCE_REPO}@${SOURCE_SHA}
EOF
)"
}

apply_sync

if git -C "$target" diff --cached --quiet; then
  echo "No changes to commit. Skills are already up to date."
  exit 0
fi

echo ""
echo "=== Changes ==="
git -C "$target" diff --cached --stat
echo ""

if [[ "$DRY_RUN" == "remote" ]]; then
  echo "DRY_RUN=remote: skipping commit and push."
  echo ""
  echo "=== Full diff ==="
  git -C "$target" diff --cached
  exit 0
fi

commit_sync

# --- Push; when another publisher got there first, apply again from its tip ---
#
# A fresh clone racing a sibling's push is rejected as "fetch first"; a clone whose
# tracking ref already knows the remote moved, as "non-fast-forward". Either way the
# commit just made was decided against a stale tree, so it is dropped and the sync
# applied again to the remote's tip — collision guard included — then pushed once more.

push_target() {
  git -C "$target" push origin "$TARGET_BRANCH" 2>&1
}

if ! output=$(push_target); then
  if ! echo "$output" | grep -Eqi "fetch first|non-fast-forward"; then
    echo "$output" >&2
    die "Push failed"
  fi
  echo "Push rejected (the remote has moved). Applying the sync again from its new tip..."
  git -C "$target" fetch -q origin "$TARGET_BRANCH"
  git -C "$target" reset -q --hard FETCH_HEAD
  apply_sync
  if git -C "$target" diff --cached --quiet; then
    echo "Nothing left to publish: the remote already holds these skills."
    exit 0
  fi
  commit_sync
  if ! retry_output=$(push_target); then
    echo "$retry_output" >&2
    die "Push failed after retry"
  fi
fi

echo ""
echo "Skills synced to ${TARGET_REPO} (${TARGET_BRANCH}) from ${SYNC_SOURCE} ${RELEASE_TAG}"
