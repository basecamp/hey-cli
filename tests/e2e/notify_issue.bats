#!/usr/bin/env bats
#
# Dedup behaviour for scripts/notify-issue.sh, shared by release.yml and
# aur-publish.yml and installer-smoke.yml.
#
# Two defects are pinned here, both inherited from basecamp-cli. The first: an
# inline lookup that matched on *title*, so retitling the canonical issue while
# triaging it — the normal fate of an issue that stays open across an outage —
# made the next failure open a duplicate. The second: a lookup wrapped in
# `2>/dev/null || true`, which makes a rate limit, a permissions gap and a
# GitHub outage indistinguishable from "no open issue", and the very next line
# files one.
#
# `gh` is stubbed via PATH. Its four possible answers each get a branch, because
# the failure mode of guessing wrong is a public issue nobody asked for.

setup() {
  REPO_ROOT="$(cd "${BATS_TEST_DIRNAME}/../.." && pwd)"
  NOTIFY="$REPO_ROOT/scripts/notify-issue.sh"
  WORK="$(mktemp -d)"
  STUB_DIR="$WORK/bin"
  mkdir -p "$STUB_DIR"
  GH_CALLS="$WORK/gh-calls"
  : > "$GH_CALLS"
  PATH="$STUB_DIR:$PATH"
  export GH_CALLS
}

teardown() {
  rm -rf "$WORK"
}

# Stubs `gh`. $1 is what `gh issue list` prints (one issue number per line),
# $2 its exit status. Every invocation is recorded to $GH_CALLS so the tests can
# assert on what was *not* called as well as what was.
stub_gh() {
  local list_output="$1" list_status="${2:-0}"

  printf '%s\n' "$list_output" > "$WORK/list-output"

  cat > "$STUB_DIR/gh" <<STUB
#!/usr/bin/env bash
echo "\$*" >> "\$GH_CALLS"
if [ "\$2" = "list" ]; then
  cat "$WORK/list-output"
  exit $list_status
fi
exit 0
STUB
  chmod +x "$STUB_DIR/gh"
}

run_notify() {
  run "$NOTIFY" \
    --repo basecamp/hey-cli \
    --label aur-publish \
    --title "AUR publish failure" \
    --body "The AUR publish failed."
}

@test "comments on the single labelled issue" {
  stub_gh "602" 0

  run_notify
  [ "$status" -eq 0 ]
  grep -q "issue comment .* 602" "$GH_CALLS"
  ! grep -q "issue create" "$GH_CALLS"
}

@test "files one issue, carrying the label, when none is open" {
  stub_gh "" 0

  run_notify
  [ "$status" -eq 0 ]
  grep -q "issue create" "$GH_CALLS"
  # Without the label the issue it just filed would not be found next time, and
  # the run after that would file a third.
  grep -q -- "--label aur-publish" "$GH_CALLS"
  ! grep -q "issue comment" "$GH_CALLS"
}

@test "fails closed when the lookup errors, filing nothing" {
  # The regression that matters. `|| true` here reads an API failure as "no
  # match" and opens a duplicate on every subsequent failed run.
  stub_gh "" 1

  run_notify
  [ "$status" -eq 2 ]
  ! grep -q "issue create" "$GH_CALLS"
  ! grep -q "issue comment" "$GH_CALLS"
  [[ "$output" == *"Could not list"* ]]
}

@test "fails closed when several issues carry the label" {
  # Two open issues means a human split them on purpose. Commenting on whichever
  # sorts first scatters one outage's history across both.
  stub_gh "602
607" 0

  run_notify
  [ "$status" -eq 3 ]
  ! grep -q "issue create" "$GH_CALLS"
  ! grep -q "issue comment" "$GH_CALLS"
  [[ "$output" == *"602"* ]]
  [[ "$output" == *"607"* ]]
}

@test "looks up by label, never by title" {
  # A title search (`in:title`) misses a retitled issue; a label survives
  # retitling.
  stub_gh "602" 0

  run_notify
  [ "$status" -eq 0 ]
  grep -q -- "issue list .*--label aur-publish" "$GH_CALLS"
  ! grep -q -- "--search" "$GH_CALLS"
  ! grep -q "in:title" "$GH_CALLS"
}

@test "ignores blank lines in the lookup output" {
  # `--jq '.[].number'` prints nothing for an empty list, which arrives here as
  # a single empty line. Counting it would make "none open" look like "one open"
  # and send a comment to issue "".
  stub_gh "
" 0

  run_notify
  [ "$status" -eq 0 ]
  grep -q "issue create" "$GH_CALLS"
  ! grep -q "issue comment" "$GH_CALLS"
}

@test "propagates a failure to comment rather than reporting success" {
  cat > "$STUB_DIR/gh" <<'STUB'
#!/usr/bin/env bash
echo "$*" >> "$GH_CALLS"
if [ "$2" = "list" ]; then
  echo 602
  exit 0
fi
echo "HTTP 403" >&2
exit 1
STUB
  chmod +x "$STUB_DIR/gh"

  run_notify
  [ "$status" -ne 0 ]
}

@test "rejects a missing required argument" {
  stub_gh "602" 0

  run "$NOTIFY" --repo basecamp/hey-cli --label aur-publish --title "t"
  [ "$status" -eq 1 ]
  [[ "$output" == *"--body"* ]]
  [ ! -s "$GH_CALLS" ]
}
