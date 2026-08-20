#!/usr/bin/env bash
#
# sign-windows.sh — Authenticode-sign a Windows artifact in place via
# DigiCert KeyLocker (cloud HSM) using jsign.
#
# Usage: sign-windows.sh TARGET PATH
#   TARGET  goreleaser build target (windows_amd64, windows_arm64) or the
#           literal "windows" for non-goreleaser callers; anything else no-ops
#
# Contract:
#   - non-windows target                   → exit 0, silent no-op
#   - all SM_* signing env vars empty      → exit 0 with a skip notice
#                                            (forks, make test-release, local builds)
#   - partial signing env                  → exit 1 (misconfiguration, not a skip)
#   - signing or timestamping failure      → nonzero (goreleaser aborts before
#                                            publish — a release never silently
#                                            ships unsigned)
#
# Required env when signing: SM_API_KEY, SM_CLIENT_CERT_FILE,
# SM_CLIENT_CERT_PASSWORD, SIGN_ALIAS, JSIGN_JAR. See RELEASING.md and
# bc3-desktop docs/windows-signing.md for the KeyLocker runbook.
set -euo pipefail

target="${1:?usage: sign-windows.sh TARGET PATH}"
path="${2:?usage: sign-windows.sh TARGET PATH}"

case "$target" in
  windows|windows_*) ;;
  *) exit 0 ;;
esac

if [ -z "${SM_API_KEY:-}" ] && [ -z "${SM_CLIENT_CERT_FILE:-}" ] && [ -z "${SM_CLIENT_CERT_PASSWORD:-}" ]; then
  echo "sign-windows: SM_* signing env not set — skipping Authenticode signing for $path" >&2
  exit 0
fi

for var in SM_API_KEY SM_CLIENT_CERT_FILE SM_CLIENT_CERT_PASSWORD SIGN_ALIAS JSIGN_JAR; do
  if [ -z "${!var:-}" ]; then
    echo "sign-windows: $var is empty while other signing env is set — refusing to continue" >&2
    exit 1
  fi
done

[ -f "$SM_CLIENT_CERT_FILE" ] || { echo "sign-windows: client cert not found: $SM_CLIENT_CERT_FILE" >&2; exit 1; }
[ -f "$JSIGN_JAR" ] || { echo "sign-windows: jsign jar not found: $JSIGN_JAR" >&2; exit 1; }

echo "sign-windows: signing $path via DigiCert KeyLocker"
java -jar "$JSIGN_JAR" \
  --storetype DIGICERTONE \
  --alias "$SIGN_ALIAS" \
  --storepass "${SM_API_KEY}|${SM_CLIENT_CERT_FILE}|${SM_CLIENT_CERT_PASSWORD}" \
  --tsaurl http://timestamp.digicert.com \
  --tsretries 3 \
  --tsretrywait 10 \
  "$path"
