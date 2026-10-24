#!/usr/bin/env bash
# session-start.sh - HEY plugin liveness check
set -euo pipefail

if ! command -v hey &>/dev/null; then
  cat << 'EOF'
<hook-output>
HEY plugin active — CLI not found on PATH.
Install: https://github.com/basecamp/hey-cli#install
</hook-output>
EOF
  exit 0
fi

auth_json=$(hey auth status --json 2>/dev/null || echo '{}')

if ! command -v jq &>/dev/null; then
  cat << 'EOF'
<hook-output>
HEY plugin active.
</hook-output>
EOF
  exit 0
fi

is_auth=false
if parsed_auth=$(echo "$auth_json" | jq -er '.data.authenticated' 2>/dev/null); then
  is_auth="$parsed_auth"
fi

if [[ "$is_auth" == "true" ]]; then
  cat << 'EOF'
<hook-output>
HEY plugin active.
</hook-output>
EOF
else
  cat << 'EOF'
<hook-output>
HEY plugin active — not authenticated.
Run: hey auth login
</hook-output>
EOF
fi
