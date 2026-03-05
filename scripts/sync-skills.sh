#!/usr/bin/env bash
set -euo pipefail

# Sync skills from hey-cli to basecamp/skills distribution repo.
#
# Env vars:
#   SKILLS_TOKEN  - GitHub token with push access to basecamp/skills (required unless DRY_RUN=local)
#   RELEASE_TAG   - Release tag, e.g. v1.2.3 (required)
#   SOURCE_SHA    - Source commit SHA (required)
#   DRY_RUN       - Optional: "local" (no network) or "remote" (clone but skip push)

RELEASE_TAG="${RELEASE_TAG:?RELEASE_TAG is required}"
SOURCE_SHA="${SOURCE_SHA:?SOURCE_SHA is required}"
DRY_RUN="${DRY_RUN:-}"

SKILLS_SOURCE="skills"
TARGET_REPO="basecamp/skills"
TARGET_BRANCH="main"
SKILLS_SUBDIR="skills"

# --- Helpers ---

die() { echo "ERROR: $*" >&2; exit 1; }

assert_remote_url() {
  local url
  url=$(git -C "$1" remote get-url origin)
  local stripped
  stripped=$(echo "$url" | sed -E 's/\.git$//')
  case "$stripped" in
    https://github.com/"$TARGET_REPO") ;;
    https://x-access-token:*@github.com/"$TARGET_REPO") ;;
    git@github.com:"$TARGET_REPO") ;;
    *) die "origin remote '$(echo "$url" | sed -E 's#(https://[^:@]+:)[^@]*@#\1***@#')' does not point to github.com/$TARGET_REPO" ;;
  esac
}

assert_branch() {
  local branch
  branch=$(git -C "$1" rev-parse --abbrev-ref HEAD)
  [[ "$branch" == "$TARGET_BRANCH" ]] || die "checked-out branch is '$branch', expected '$TARGET_BRANCH'"
}

# --- Discover skills ---

skill_dirs=()
for skill_md in "$SKILLS_SOURCE"/*/SKILL.md; do
  [[ -f "$skill_md" ]] || continue
  skill_dirs+=("$(dirname "$skill_md")")
done

[[ ${#skill_dirs[@]} -gt 0 ]] || die "no skills found under $SKILLS_SOURCE/*/SKILL.md"
echo "Found ${#skill_dirs[@]} skill(s): ${skill_dirs[*]}"

# --- Copy skills into target, excluding *.go and dotfiles ---

copy_skills() {
  local target_dir="$1"
  for skill_dir in "${skill_dirs[@]}"; do
    local name
    name=$(basename "$skill_dir")
    rm -rf "${target_dir:?}/${name}"
    mkdir -p "$target_dir/$name"
    find "$skill_dir" -mindepth 1 \
      ! -name '*.go' \
      ! -name '.*' \
      ! -path '*/.*' \
      -type f \
      -exec bash -c '
        src="$1"; skill_dir="$2"; target_dir="$3"
        rel="${src#$skill_dir/}"
        mkdir -p "$(dirname "$target_dir/$rel")"
        cp "$src" "$target_dir/$rel"
      ' _ {} "$skill_dir" "$target_dir/$name" \;
  done
}

# --- DRY_RUN=local: copy into tmpdir, diff against empty baseline ---

if [[ "$DRY_RUN" == "local" ]]; then
  tmpdir=$(mktemp -d)
  trap 'rm -rf "$tmpdir"' EXIT
  echo "DRY_RUN=local: copying skills into $tmpdir"
  copy_skills "$tmpdir/$SKILLS_SUBDIR"
  echo ""
  echo "=== Skills copied ==="
  find "$tmpdir" -type f | sort | while read -r f; do
    echo "  ${f#"$tmpdir/"}"
  done
  echo ""
  echo "=== Diff (against empty baseline) ==="
  git -C "$tmpdir" init -q
  git -C "$tmpdir" add -A
  git -C "$tmpdir" diff --cached --stat
  echo ""
  echo "DRY_RUN=local complete. No network operations performed."
  exit 0
fi

# --- Clone target repo ---

[[ -n "${SKILLS_TOKEN:-}" ]] || die "SKILLS_TOKEN is required (set DRY_RUN=local for offline testing)"

tmpdir=$(mktemp -d)
trap 'rm -rf "$tmpdir"' EXIT

echo "Cloning $TARGET_REPO into $tmpdir/skills..."
git clone --depth 1 --branch "$TARGET_BRANCH" \
  "https://x-access-token:${SKILLS_TOKEN}@github.com/${TARGET_REPO}.git" \
  "$tmpdir/skills"

target="$tmpdir/skills"

# --- Safety checks ---

assert_remote_url "$target"
assert_branch "$target"

# --- Copy skills ---

echo "Copying skills into target..."
copy_skills "$target/$SKILLS_SUBDIR"

# --- Remove stale skills using manifest ---

MANIFEST=".managed-skills"
source_skill_names=()
for skill_dir in "${skill_dirs[@]}"; do
  source_skill_names+=("$(basename "$skill_dir")")
done

previously_managed_names=()
if [[ -f "$target/$MANIFEST" ]]; then
  while IFS= read -r entry; do
    [[ -z "$entry" ]] && continue
    if [[ "$entry" == "." || "$entry" == ".." || ! "$entry" =~ ^[a-zA-Z0-9._-]+$ ]]; then
      echo "WARNING: skipping invalid manifest entry: $entry" >&2
      continue
    fi
    previously_managed_names+=("$entry")
  done < "$target/$MANIFEST"
else
  for candidate in "$target/$SKILLS_SUBDIR"/*/SKILL.md; do
    [[ -f "$candidate" ]] || continue
    previously_managed_names+=("$(basename "$(dirname "$candidate")")")
  done
fi

for previously_managed in "${previously_managed_names[@]}"; do
  found=0
  for name in "${source_skill_names[@]}"; do
    [[ "$name" == "$previously_managed" ]] && found=1 && break
  done
  if [[ "$found" -eq 0 && -d "$target/$SKILLS_SUBDIR/$previously_managed" ]]; then
    echo "Removing stale skill: $previously_managed"
    rm -rf "${target:?}/$SKILLS_SUBDIR/$previously_managed"
  fi
done

# Write current manifest
printf '%s\n' "${source_skill_names[@]}" | sort > "$target/$MANIFEST"

# --- Commit ---

git -C "$target" add -A

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

git -C "$target" \
  -c user.name="hey-cli[bot]" \
  -c user.email="hey-cli[bot]@users.noreply.github.com" \
  commit -m "$(cat <<EOF
Sync skills from hey-cli ${RELEASE_TAG}

Source: basecamp/hey-cli@${SOURCE_SHA}
EOF
)"

# --- Push (with one retry on non-fast-forward) ---

push_target() {
  git -C "$target" push origin "$TARGET_BRANCH" 2>&1
}

if ! output=$(push_target); then
  if echo "$output" | grep -qi "non-fast-forward"; then
    echo "Push rejected (non-fast-forward). Pulling with rebase and retrying..."
    git -C "$target" pull --rebase origin "$TARGET_BRANCH"
    if ! retry_output=$(push_target); then
      echo "$retry_output" >&2
      die "Push failed after retry"
    fi
  else
    echo "$output" >&2
    die "Push failed"
  fi
fi

echo ""
echo "Skills synced to $TARGET_REPO ($TARGET_BRANCH) from $RELEASE_TAG"
