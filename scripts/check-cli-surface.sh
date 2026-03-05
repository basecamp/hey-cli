#!/usr/bin/env bash
# Generate deterministic CLI surface snapshot from --help --agent output.
set -euo pipefail

BINARY="${1:-./bin/hey}"
OUTPUT="${2:-/dev/stdout}"

if ! command -v jq >/dev/null 2>&1; then
  echo "ERROR: jq is required but not installed." >&2
  exit 1
fi

walk_commands() {
  local cmd_path="$1"
  local json

  local -a args=()
  if [ "$cmd_path" != "hey" ]; then
    # shellcheck disable=SC2206
    args=(${cmd_path#hey })
  fi

  local stderr_file
  stderr_file="$(mktemp)"
  if ! json=$("$BINARY" "${args[@]}" --help --agent 2>"$stderr_file"); then
    echo "ERROR: failed to get help for: $cmd_path" >&2
    if [ -s "$stderr_file" ]; then
      cat "$stderr_file" >&2
    fi
    rm -f "$stderr_file"
    exit 1
  fi
  rm -f "$stderr_file"

  echo "$json" | jq -r --arg path "$cmd_path" '
    "CMD \($path)",
    ((.flags // []) | sort_by(.name) | .[] |
      "FLAG \($path) --\(.name) type=\(.type)"),
    ((.subcommands // []) | sort_by(.name) | .[] |
      "SUB \($path) \(.name)")
  '

  local subs
  subs=$(echo "$json" | jq -r '.subcommands // [] | .[].name')
  for sub in $subs; do
    walk_commands "$cmd_path $sub"
  done
}

walk_commands "hey" | LC_ALL=C sort > "$OUTPUT"
