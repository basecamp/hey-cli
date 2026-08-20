#!/usr/bin/env bats
# installer_signing.bats - Tests for the shape the PowerShell installer must
# have before Authenticode signing.
#
# basecamp-cli v0.8.0-rc.2 shipped a hey_installer.ps1 that PowerShell rejected as
# HashMismatch, so the installer would not run under AllSigned/RemoteSigned.
# Cause: jsign digests the script as UTF-16LE of the content with NO
# line-ending normalization, while PowerShell's script SIP normalizes to CRLF
# first. An LF file therefore yields two different digests.
#
# A second, latent cause would have survived a CRLF-only fix: the file carries
# no BOM, jsign assumes UTF-8, and Windows PowerShell 5.1 assumes ANSI
# (Windows-1252). Any byte above 0x7F decodes differently on each side. Within
# ASCII the encodings are identical, so staging rejects non-ASCII outright.

setup() {
  STAGE="${BATS_TEST_DIRNAME}/../../scripts/stage-installer-ps1.sh"
  INSTALL_PS1="${BATS_TEST_DIRNAME}/../../scripts/install.ps1"
  WORK="$(mktemp -d)"
}

teardown() {
  [[ -n "${WORK:-}" ]] && rm -rf "$WORK"
}

@test "install.ps1 is pure ASCII so staging can succeed at release time" {
  # Guards the source, not just the staged copy: a stray em-dash here fails
  # the release, and this is where it is cheap to catch.
  run env LC_ALL=C grep -n '[^[:print:][:space:]]' "$INSTALL_PS1"
  [ "$status" -ne 0 ]
}

@test "staging converts LF to CRLF" {
  run "$STAGE" "$INSTALL_PS1" "$WORK/out.ps1"
  [ "$status" -eq 0 ]
  # Every line ends CRLF; none is bare LF.
  total=$(wc -l < "$WORK/out.ps1")
  crlf=$(grep -c $'\r$' "$WORK/out.ps1")
  [ "$total" -eq "$crlf" ]
}

@test "staging is idempotent (no CR CR LF on a second pass)" {
  "$STAGE" "$INSTALL_PS1" "$WORK/once.ps1"
  "$STAGE" "$WORK/once.ps1" "$WORK/twice.ps1"
  run cmp -s "$WORK/once.ps1" "$WORK/twice.ps1"
  [ "$status" -eq 0 ]
  run grep -c $'\r\r' "$WORK/twice.ps1"
  [ "$status" -ne 0 ]
}

@test "staging rejects non-ASCII rather than signing an unverifiable file" {
  printf '# dash \xe2\x80\x94 here\nWrite-Host x\n' > "$WORK/bad.ps1"
  run "$STAGE" "$WORK/bad.ps1" "$WORK/bad-out.ps1"
  [ "$status" -ne 0 ]
  [[ "$output" == *"non-ASCII"* ]]
  [ ! -f "$WORK/bad-out.ps1" ]
}

@test "staged file makes jsign's digest and PowerShell's agree" {
  # The actual property under test. jsign hashes UTF-16LE of the content as-is;
  # PowerShell hashes UTF-16LE after normalizing to CRLF. On a correctly staged
  # file those are the same bytes, so the digests match and the signature
  # verifies. This reproduces both sides rather than trusting the shape.
  "$STAGE" "$INSTALL_PS1" "$WORK/out.ps1"
  run python3 -c '
import hashlib, sys
raw = open(sys.argv[1], "rb").read()
txt = raw.decode("utf-8")
jsign = hashlib.sha256(txt.encode("utf-16-le")).hexdigest()
ps = hashlib.sha256(txt.replace("\r\n", "\n").replace("\n", "\r\n").encode("utf-16-le")).hexdigest()
print("MATCH" if jsign == ps else f"DIFFER jsign={jsign} powershell={ps}")
' "$WORK/out.ps1"
  [ "$status" -eq 0 ]
  [ "$output" = "MATCH" ]
}

@test "an unstaged LF copy would NOT verify (the rc.2 regression)" {
  # Negative control: without staging, the two digests diverge. If this ever
  # passes, the digest model above has stopped reflecting reality and the
  # positive test is no longer evidence of anything.
  run python3 -c '
import hashlib, sys
raw = open(sys.argv[1], "rb").read()
txt = raw.decode("utf-8")
jsign = hashlib.sha256(txt.encode("utf-16-le")).hexdigest()
ps = hashlib.sha256(txt.replace("\r\n", "\n").replace("\n", "\r\n").encode("utf-16-le")).hexdigest()
print("MATCH" if jsign == ps else "DIFFER")
' "$INSTALL_PS1"
  [ "$status" -eq 0 ]
  [ "$output" = "DIFFER" ]
}
