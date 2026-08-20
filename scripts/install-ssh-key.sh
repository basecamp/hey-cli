#!/usr/bin/env bash
# Usage: SSH_KEY="$SECRET" scripts/install-ssh-key.sh DEST
#
# Writes an OpenSSH private key held in a secret to DEST (mode 0600) and proves
# it loads before anything tries to authenticate with it.
#
# A private key is the one secret that has to survive copy-paste byte for byte,
# and a secrets UI is where it gets mangled: CRLF line endings from a Windows
# clipboard, the trailing newline dropped by a form field, the whole key
# flattened onto one line with literal "\n" sequences, blank lines or spaces
# around it. Every one of those makes ssh report the useless
# `Load key "...": invalid format` and then `Permission denied (publickey)`,
# which reads as a credentials problem when it is a transport one — a mistake
# this team has made repeatedly. The mangling is repaired here, so a key that
# was pasted in any of those shapes still works, and a key that is genuinely
# wrong is reported with a shape diagnosis that never prints key material.

set -euo pipefail

dest="${1:?usage: SSH_KEY=... install-ssh-key.sh DEST}"
raw="${SSH_KEY:?SSH_KEY is not set}"

die() {
  echo "::error::$*" >&2
  exit 1
}

# A flattened paste carries the newlines as the two characters '\' 'n'. Expand
# them only when the key actually arrived on one line: a well-formed key has no
# backslashes in it (PEM armor plus base64), so the expansion is safe there, and
# a multi-line key is left untouched rather than risk rewriting anything.
if [[ "$raw" == *'\n'* ]] && [[ "$(printf '%s' "$raw" | tr -d '\r' | wc -l)" -le 1 ]]; then
  raw="${raw//\\n/$'\n'}"
fi

tmp="$(mktemp "${dest}.XXXXXX")"
trap 'rm -f "$tmp"' EXIT
chmod 600 "$tmp"

# Drop carriage returns, surrounding blank lines and trailing whitespace, and
# end the file with exactly one newline — OpenSSH refuses a key without it.
printf '%s\n' "$raw" \
  | tr -d '\r' \
  | sed -e 's/[[:space:]]*$//' \
  | awk 'NF { if (blank && started) printf "%s", blank; blank = ""; started = 1; print; next } started { blank = blank "\n" }' \
  > "$tmp"

if ! err="$(ssh-keygen -y -f "$tmp" 2>&1 >/dev/null)"; then
  lines=$(wc -l < "$tmp")
  first=$(head -n1 "$tmp")
  last=$(tail -n1 "$tmp")
  shape="lines=${lines}"
  case "$first" in
    "-----BEGIN "*" PRIVATE KEY-----") shape="${shape} header=ok" ;;
    "ssh-"*|"ecdsa-"*) shape="${shape} header=PUBLIC-KEY" ;;
    *) shape="${shape} header=missing" ;;
  esac
  case "$last" in
    "-----END "*" PRIVATE KEY-----") shape="${shape} footer=ok" ;;
    *) shape="${shape} footer=missing" ;;
  esac
  [[ "$SSH_KEY" == *$'\r'* ]] && shape="${shape} had-CR"
  [[ "$SSH_KEY" == *'\n'* ]] && shape="${shape} had-literal-backslash-n"
  [[ "$SSH_KEY" == *$'\n' ]] || shape="${shape} no-trailing-newline"
  die "SSH key does not load after normalisation (${shape}): ${err}. Re-save the secret from the key file itself (gh secret set NAME < key), not from a paste."
fi

mv "$tmp" "$dest"
trap - EXIT
chmod 600 "$dest"
