#!/usr/bin/env sh
# Usage: scripts/stage-installer-ps1.sh SOURCE DEST
#
# Stage a copy of the PowerShell installer for Authenticode signing, in the
# only shape where jsign's digest and PowerShell's agree.
#
# jsign computes the script digest as UTF-16LE of the file content with the
# signature block stripped, and applies NO line-ending normalization
# (SignableScript.computeDigest). PowerShell's script SIP normalizes to CRLF
# before hashing. Sign an LF file and the two digests differ, so
# Get-AuthenticodeSignature reports HashMismatch and the installer is refused
# under AllSigned/RemoteSigned. That is what shipped in v0.8.0-rc.2.
#
# Two invariants make the digests agree:
#
#   CRLF        removes the normalization disagreement above.
#
#   pure ASCII  removes an encoding disagreement that CRLF alone would not
#               fix. The file has no BOM; jsign assumes UTF-8, but Windows
#               PowerShell 5.1 assumes ANSI (Windows-1252) for BOM-less files.
#               Any byte above 0x7F therefore decodes to different characters
#               on each side and the digests diverge again. Within ASCII the
#               two encodings are identical byte-for-byte, so the question
#               never arises. Adding a BOM would be the other way out, but it
#               trades a known-good invariant for jsign's
#               isByteOrderMarkSigned() matching Microsoft's undocumented
#               treatment of the BOM in the hash — untestable without a
#               signing round-trip.
#
# The source file is left alone: raw-main install.ps1 stays LF and unsigned,
# which is the `irm | iex` path and needs neither.

set -eu

SRC="${1:?usage: stage-installer-ps1.sh SOURCE DEST}"
DEST="${2:?usage: stage-installer-ps1.sh SOURCE DEST}"

[ -f "$SRC" ] || { echo "stage-installer-ps1: source not found: $SRC" >&2; exit 1; }

# Reject non-ASCII before signing rather than after. Under LC_ALL=C every byte
# above 0x7F is non-printable, so this matches exactly the bytes that would
# make jsign and PowerShell 5.1 disagree.
if LC_ALL=C grep -n '[^[:print:][:space:]]' "$SRC" >/dev/null 2>&1; then
  echo "stage-installer-ps1: $SRC contains non-ASCII bytes, which would produce" >&2
  echo "  a signature PowerShell 5.1 rejects as HashMismatch (it decodes BOM-less" >&2
  echo "  files as Windows-1252 while jsign assumes UTF-8). Offending lines:" >&2
  LC_ALL=C grep -n '[^[:print:][:space:]]' "$SRC" >&2
  exit 1
fi

mkdir -p "$(dirname "$DEST")"

# LF -> CRLF, idempotent: strip any existing CR first so a CRLF source does
# not become CR CR LF.
awk '{ sub(/\r$/, ""); printf "%s\r\n", $0 }' "$SRC" > "$DEST"

echo "stage-installer-ps1: staged $DEST (pure ASCII, CRLF)"
