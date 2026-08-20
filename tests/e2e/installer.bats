#!/usr/bin/env bats
# installer.bats - Contracts for scripts/install.sh and scripts/install.ps1.
#
# The installers are the one piece of the release pipeline that runs on a
# machine nobody controls, under whatever bash (3.2 on stock macOS), curl and
# cosign happen to be there. Each test here pins a behaviour that went wrong
# once in basecamp-cli's installer and must not come back in hey's: sourcing
# vs executing, per-arch darwin archives, the cosign version gate, a failing
# first run diagnosed rather than swallowed.

setup() {
  INSTALL_SH="${BATS_TEST_DIRNAME}/../../scripts/install.sh"
  INSTALL_PS1="${BATS_TEST_DIRNAME}/../../scripts/install.ps1"

  STUB_DIR="$(mktemp -d)"
  LOG="$STUB_DIR/calls.log"
}

teardown() {
  [[ -n "${STUB_DIR:-}" ]] && rm -rf "$STUB_DIR"
}

# The if-form guard must let sourcing define functions without running main.
@test "install.sh can be sourced without running the installer" {
  run bash -c "set -euo pipefail; source '$INSTALL_SH'; echo sourced-ok"
  [[ "$status" -eq 0 ]]
  [[ "$output" == *"sourced-ok"* ]]
  [[ "$output" != *"HEY CLI"* ]]  # banner would print if main ran
}

@test "install.sh runs main when piped to bash" {
  # PATH points at a binary-free tmpdir so main stops at its curl
  # prerequisite without downloading anything or touching the host.
  run bash -c "cat '$INSTALL_SH' | PATH='$BATS_TEST_TMPDIR' '$BASH'"
  [[ "$status" -ne 0 ]]
  [[ "$output" == *"curl is required but not installed"* ]]
  [[ "$output" != *"BASH_SOURCE[0]: unbound variable"* ]]
}

# No interactive setup wizard exists: the installer must end with printed next
# steps, never by exec'ing the freshly installed binary into a prompt.
@test "install.sh ends with next steps, not an interactive setup" {
  run grep -nE '"\$BIN_DIR/\$binary_name" setup|HEY_SKIP_SETUP|post_install_setup' "$INSTALL_SH"
  [[ "$status" -ne 0 ]]
  grep -q 'hey auth login' "$INSTALL_SH"
}

# The release builds darwin_amd64 and darwin_arm64, never a universal
# darwin_all. The root install.sh this replaced requested darwin_all and 404'd
# on every Mac.
@test "detect_platform reports per-arch darwin, never darwin_all" {
  cat > "$STUB_DIR/uname" <<'EOF'
#!/usr/bin/env bash
case "$1" in
  -s) echo Darwin ;;
  -m) echo "${FAKE_ARCH:-arm64}" ;;
esac
EOF
  chmod +x "$STUB_DIR/uname"

  run bash -c "export PATH=\"$STUB_DIR:\$PATH\"; source '$INSTALL_SH'; detect_platform"
  [[ "$status" -eq 0 ]]
  [[ "$output" == "darwin_arm64" ]]

  run bash -c "export PATH=\"$STUB_DIR:\$PATH\" FAKE_ARCH=x86_64; source '$INSTALL_SH'; detect_platform"
  [[ "$status" -eq 0 ]]
  [[ "$output" == "darwin_amd64" ]]

  # Comments may mention it; code may not.
  run grep -nE '^[^#]*darwin_all' "$INSTALL_SH" "$INSTALL_PS1"
  [[ "$status" -ne 0 ]]
}

@test "download_binary requests the per-platform archive name the release publishes" {
  run bash -c "
    set -euo pipefail
    source '$INSTALL_SH'
    curl_run() { echo \"\$@\" >> '$LOG'; return 1; }
    # error() exits, so run in a subshell and read the log afterwards.
    ( download_binary 9.9.9 darwin_arm64 '$STUB_DIR' ) || true
    cat '$LOG'
  "
  [[ "$status" -eq 0 ]]
  [[ "$output" == *"/releases/download/v9.9.9/hey_9.9.9_darwin_arm64.tar.gz"* ]]
}

# Smart App Control blocks unsigned executables at process creation, so the
# install scripts must surface WHY the first run failed instead of a bare
# "not working".

@test "verify_install surfaces stderr and adds the Windows SAC hint" {
  cat > "$STUB_DIR/hey.exe" <<'EOF'
#!/usr/bin/env bash
echo "simulated block: cannot execute" >&2
exit 126
EOF
  chmod +x "$STUB_DIR/hey.exe"

  run bash -c "
    set -euo pipefail
    source '$INSTALL_SH'
    BIN_DIR='$STUB_DIR'
    verify_install windows_amd64
  "
  [[ "$status" -ne 0 ]]
  [[ "$output" == *"simulated block: cannot execute"* ]]
  [[ "$output" == *"Smart App Control"* ]]
  [[ "$output" == *"#windows-smart-app-control-and-smartscreen"* ]]
}

@test "verify_install on linux surfaces stderr without the Windows hint" {
  cat > "$STUB_DIR/hey" <<'EOF'
#!/usr/bin/env bash
echo "simulated failure" >&2
exit 1
EOF
  chmod +x "$STUB_DIR/hey"

  run bash -c "
    set -euo pipefail
    source '$INSTALL_SH'
    BIN_DIR='$STUB_DIR'
    verify_install linux_amd64
  "
  [[ "$status" -ne 0 ]]
  [[ "$output" == *"simulated failure"* ]]
  [[ "$output" != *"Smart App Control"* ]]
}

@test "install.ps1 carries the Smart App Control first-run diagnosis" {
  grep -q 'function Get-FirstRunFailureMessage' "$INSTALL_PS1"
  grep -q 'Smart App Control' "$INSTALL_PS1"
  grep -q 'Protection history' "$INSTALL_PS1"
  # The first-run failure path routes through the diagnosis helper, for a
  # launch that throws and for one that exits nonzero (PowerShell 5.1 does
  # not throw on a native nonzero exit).
  grep -qF 'Fail (Get-FirstRunFailureMessage' "$INSTALL_PS1"
  grep -qF -- '-Reason "hey --version exited with code $LASTEXITCODE"' "$INSTALL_PS1"
}

# All three diagnosis branches, driven by shadowing the probes. Only the
# function under test is extracted from install.ps1's AST and evaluated; Main
# never runs, and the installer needs no test hooks.
@test "install.ps1 Get-FirstRunFailureMessage diagnoses SAC, quarantine, and generic failures" {
  if ! command -v pwsh >/dev/null 2>&1; then
    if [[ -n "${CI:-}" ]]; then
      echo "pwsh is required in CI for install.ps1 diagnosis coverage" >&2
      return 1
    fi
    skip "pwsh not installed"
  fi

  cat > "$STUB_DIR/sac-driver.ps1" <<'EOF'
$ErrorActionPreference = 'Stop'
$tokens = $null; $parseErrors = $null
$ast = [System.Management.Automation.Language.Parser]::ParseFile($env:INSTALL_PS1_PATH, [ref]$tokens, [ref]$parseErrors)
if ($parseErrors.Count -gt 0) { throw "install.ps1 parse errors: $($parseErrors -join '; ')" }
$fn = $ast.Find({ param($n) $n -is [System.Management.Automation.Language.FunctionDefinitionAst] -and $n.Name -eq 'Get-FirstRunFailureMessage' }, $true)
if (-not $fn) { throw 'Get-FirstRunFailureMessage not found in install.ps1' }
. ([scriptblock]::Create($fn.Extent.Text))

# Shadow the probes the helper relies on. Advanced functions so the helper's
# -ErrorAction Stop common parameter binds.
$script:SigStatus = 'NotSigned'
$script:SacState = 1
function Get-AuthenticodeSignature { [CmdletBinding()] param([string]$FilePath) [pscustomobject]@{ Status = $script:SigStatus } }
function Get-ItemProperty { [CmdletBinding()] param([string]$Path) [pscustomobject]@{ VerifiedAndReputablePolicyState = $script:SacState } }

# Branch 1: unsigned binary, SAC on.
$msg = Get-FirstRunFailureMessage -Binary 'C:\bin\hey.exe' -Reason 'boom-sac'
if ($msg -notmatch '^Installed hey\.exe .+ running it failed: boom-sac') { throw "branch1 does not lead with the original failure: $msg" }
if ($msg -notlike '*Smart App Control*') { throw 'branch1 missing SAC explanation' }
if ($msg -notlike '*wsl --install*') { throw 'branch1 missing WSL option' }
if ($msg -notlike '*no per-app exceptions*') { throw 'branch1 missing no-exceptions caveat' }
if ($msg -notlike '*leave it off while using this unsigned build*') { throw 'branch1 missing stay-off caveat' }
'branch1-ok'

# Branch 1b: SAC in evaluation mode (2) observes without blocking, so it must
# not be blamed -- falls through to the quarantine advice.
$script:SacState = 2
$msg = Get-FirstRunFailureMessage -Binary 'C:\bin\hey.exe' -Reason 'boom-eval'
if ($msg -notlike '*boom-eval*') { throw 'branch1b missing original failure' }
if ($msg -like '*wsl --install*') { throw 'branch1b must not blame SAC evaluation mode' }
if ($msg -notlike '*Protection history*') { throw 'branch1b missing quarantine advice' }
'branch1b-ok'

# Branch 2: unsigned binary, SAC off -- quarantine advice, no WSL pitch.
$script:SacState = 0
$msg = Get-FirstRunFailureMessage -Binary 'C:\bin\hey.exe' -Reason 'boom-quarantine'
if ($msg -notlike '*boom-quarantine*') { throw 'branch2 missing original failure' }
if ($msg -notlike '*Protection history*') { throw 'branch2 missing Protection history advice' }
if ($msg -like '*wsl --install*') { throw 'branch2 should not pitch WSL' }
'branch2-ok'

# Branch 3: signed binary -- generic hint, no unsigned claim.
$script:SigStatus = 'Valid'
$msg = Get-FirstRunFailureMessage -Binary 'C:\bin\hey.exe' -Reason 'boom-generic'
if ($msg -notlike '*boom-generic*') { throw 'branch3 missing original failure' }
if ($msg -notlike '*Protection history*') { throw 'branch3 missing generic hint' }
if ($msg -like '*not code-signed*') { throw 'branch3 must not claim the binary is unsigned' }
'branch3-ok'
EOF

  run bash -c "
    set -euo pipefail
    export INSTALL_PS1_PATH='$INSTALL_PS1'
    pwsh -NoProfile -File '$STUB_DIR/sac-driver.ps1'
  "
  [[ "$status" -eq 0 ]]
  [[ "$output" == *"branch1-ok"* ]]
  [[ "$output" == *"branch1b-ok"* ]]
  [[ "$output" == *"branch2-ok"* ]]
  [[ "$output" == *"branch3-ok"* ]]
}

# FreeBSD/OpenBSD base systems ship sha256(1), not sha256sum or shasum, and
# detect_platform accepts both -- so the checksum step must too.
@test "find_sha256_cmd falls back to the BSD sha256 -q" {
  printf '#!/bin/sh\necho deadbeef\n' > "$STUB_DIR/sha256"
  chmod +x "$STUB_DIR/sha256"
  run bash -c "PATH='$STUB_DIR'; source '$INSTALL_SH'; find_sha256_cmd"
  [[ "$status" -eq 0 ]]
  [[ "$output" == "sha256 -q" ]]

  # Invoked the way verify_checksums does: the command word splits into
  # sha256 plus its -q flag, and the digest lands in field 1.
  run bash -c "PATH='$STUB_DIR'; source '$INSTALL_SH'; \$(find_sha256_cmd) somefile"
  [[ "$status" -eq 0 ]]
  [[ "${output%% *}" == "deadbeef" ]]
}

@test "find_sha256_cmd errors when no SHA256 tool exists" {
  run bash -c "PATH='$STUB_DIR'; source '$INSTALL_SH'; find_sha256_cmd"
  [[ "$status" -ne 0 ]]
  [[ "$output" == *"No SHA256 tool found"* ]]
}

# fish sources neither ~/.profile nor Bourne `export`, so the PATH line for
# a fish login shell has to land in config.fish in fish syntax.
@test "setup_path writes fish_add_path to config.fish for fish shells" {
  local home="$STUB_DIR/home"
  mkdir -p "$home"
  run bash -c "
    export HOME='$home' SHELL=/usr/local/bin/fish PATH=/usr/bin:/bin
    unset XDG_CONFIG_HOME
    source '$INSTALL_SH'
    BIN_DIR='$home/.local/bin'
    setup_path
  "
  [[ "$status" -eq 0 ]]
  [[ -f "$home/.config/fish/config.fish" ]]
  grep -qF "fish_add_path \"$home/.local/bin\"" "$home/.config/fish/config.fish"
  [[ ! -f "$home/.profile" ]]
}

@test "setup_path appends export PATH to .profile for other shells" {
  local home="$STUB_DIR/home"
  mkdir -p "$home"
  run bash -c "
    export HOME='$home' SHELL=/bin/ksh PATH=/usr/bin:/bin
    source '$INSTALL_SH'
    BIN_DIR='$home/.local/bin'
    setup_path
  "
  [[ "$status" -eq 0 ]]
  grep -qF "export PATH=\"$home/.local/bin:\$PATH\"" "$home/.profile"
}

# Releases publish the new (protobuf) Sigstore bundle format. cosign v3 parses
# it by default, v2.6–v2.x needs --new-bundle-format=true, and older cosign
# cannot verify it at all (v2.4 chokes on the bundle's tlog key type, v2.2
# lacks the flag) — those must warn and skip, preserving cosign's optional
# posture, never fail the install or false-green the verification.

@test "cosign_bundle_support: v3 verifies with no extra flag" {
  run bash -c "source '$INSTALL_SH'; cosign_bundle_support v3.0.2"
  [[ "$status" -eq 0 ]]
  [[ -z "$output" ]]
}

@test "cosign_bundle_support: v2.6 verifies with --new-bundle-format=true" {
  run bash -c "source '$INSTALL_SH'; cosign_bundle_support v2.6.0"
  [[ "$status" -eq 0 ]]
  [[ "$output" == "--new-bundle-format=true" ]]
}

@test "cosign_bundle_support: v2.4 is unsupported" {
  run bash -c "source '$INSTALL_SH'; cosign_bundle_support v2.4.0"
  [[ "$status" -ne 0 ]]
}

@test "cosign_bundle_support: garbage version is unsupported" {
  run bash -c "source '$INSTALL_SH'; cosign_bundle_support 'devel-deadbeef'"
  [[ "$status" -ne 0 ]]
  run bash -c "source '$INSTALL_SH'; cosign_bundle_support ''"
  [[ "$status" -ne 0 ]]
}

# write_cosign_stub emits a cosign stub whose `cosign version` reports the
# given GitVersion and which logs every other invocation's argv.
write_cosign_stub() {
  local ver="$1"
  {
    echo '#!/usr/bin/env bash'
    echo 'if [[ "$1" == "version" ]]; then'
    echo "  echo 'GitVersion:    ${ver}'"
    echo '  exit 0'
    echo 'fi'
    echo "echo \"\$@\" >> \"$LOG\""
    echo 'exit 0'
  } > "$STUB_DIR/cosign"
  chmod +x "$STUB_DIR/cosign"
}

# run_verify_checksums drives the real verify_checksums with a stubbed cosign
# and a no-network curl_run, against a locally-built archive + checksums.txt.
run_verify_checksums() {
  run bash -c "
    set -euo pipefail
    export PATH=\"$STUB_DIR:\$PATH\"
    source '$INSTALL_SH'
    tmp_dir='$STUB_DIR/work'
    mkdir -p \"\$tmp_dir\"
    printf 'archive-bytes' > \"\$tmp_dir/hey_9.9.9_linux_amd64.tar.gz\"
    sha=\$(cd \"\$tmp_dir\" && \$(find_sha256_cmd) hey_9.9.9_linux_amd64.tar.gz | awk '{print \$1}')
    verify_checksums_stub_sums() { printf '%s  hey_9.9.9_linux_amd64.tar.gz\n' \"\$sha\" > \"\$tmp_dir/checksums.txt\"; }
    # No-network curl_run: serve the locally-computed checksums.txt and a
    # placeholder bundle (cosign is stubbed, so its content is irrelevant).
    curl_run() {
      local arg dest=''
      local prev=''
      for arg in \"\$@\"; do
        [[ \"\$prev\" == '-o' ]] && dest=\"\$arg\"
        prev=\"\$arg\"
      done
      if [[ \"\$dest\" == *checksums.txt ]]; then
        verify_checksums_stub_sums
      elif [[ -n \"\$dest\" ]]; then
        printf 'stub-bundle' > \"\$dest\"
      fi
      return 0
    }
    verify_checksums 9.9.9 \"\$tmp_dir\" hey_9.9.9_linux_amd64.tar.gz
    cat '$LOG' 2>/dev/null || true
  "
}

@test "verify_checksums with cosign v3 runs verify-blob without the compat flag" {
  write_cosign_stub v3.0.2
  run_verify_checksums
  [[ "$status" -eq 0 ]]
  [[ "$output" == *"Signature verified"* ]]
  [[ "$output" == *"verify-blob"* ]]
  [[ "$output" != *"--new-bundle-format"* ]]
}

@test "verify_checksums pins the release workflow identity" {
  write_cosign_stub v3.0.2
  run_verify_checksums
  [[ "$status" -eq 0 ]]
  [[ "$output" == *"--certificate-identity https://github.com/basecamp/hey-cli/.github/workflows/release.yml@refs/tags/v9.9.9"* ]]
  [[ "$output" == *"--certificate-oidc-issuer https://token.actions.githubusercontent.com"* ]]
}

@test "verify_checksums with cosign v2.6 adds --new-bundle-format=true" {
  write_cosign_stub v2.6.0
  run_verify_checksums
  [[ "$status" -eq 0 ]]
  [[ "$output" == *"Signature verified"* ]]
  [[ "$output" == *"--new-bundle-format=true"* ]]
}

@test "verify_checksums with cosign v2.4 warns, skips verification, still succeeds" {
  write_cosign_stub v2.4.0
  run_verify_checksums
  [[ "$status" -eq 0 ]]
  [[ "$output" == *"Checksum verified"* ]]
  [[ "$output" == *"Skipping signature verification"* ]]
  [[ "$output" != *"verify-blob"* ]]
}

@test "verify_checksums with unparseable cosign version warns and skips" {
  write_cosign_stub 'devel'
  run_verify_checksums
  [[ "$status" -eq 0 ]]
  [[ "$output" == *"Skipping signature verification"* ]]
  [[ "$output" != *"verify-blob"* ]]
}

# A cosign whose `version` subcommand itself FAILS must not abort the
# installer: under `set -euo pipefail` the version probe runs inside a command
# substitution, and without the guard in cosign_version the nonzero exit
# propagates and kills the install. It must degrade to the warn-and-skip path
# with checksum verification intact.
@test "verify_checksums with broken cosign (version exits nonzero) warns, skips, still succeeds" {
  {
    echo '#!/usr/bin/env bash'
    echo 'if [[ "$1" == "version" ]]; then'
    echo '  echo "cosign: catastrophic startup failure" >&2'
    echo '  exit 42'
    echo 'fi'
    echo "echo \"\$@\" >> \"$LOG\""
    echo 'exit 0'
  } > "$STUB_DIR/cosign"
  chmod +x "$STUB_DIR/cosign"

  run_verify_checksums
  [[ "$status" -eq 0 ]]
  [[ "$output" == *"Checksum verified"* ]]
  [[ "$output" == *"Skipping signature verification"* ]]
  [[ "$output" != *"verify-blob"* ]]
}

# A wrong checksum must fail the install outright; there is no skip path for
# the SHA-256 check, cosign or not.
@test "verify_checksums fails on a checksum mismatch" {
  run bash -c "
    set -euo pipefail
    source '$INSTALL_SH'
    tmp_dir='$STUB_DIR/work'
    mkdir -p \"\$tmp_dir\"
    printf 'archive-bytes' > \"\$tmp_dir/hey_9.9.9_linux_amd64.tar.gz\"
    curl_run() {
      printf '%s  hey_9.9.9_linux_amd64.tar.gz\n' 0000000000000000000000000000000000000000000000000000000000000000 > \"\$tmp_dir/checksums.txt\"
      return 0
    }
    verify_checksums 9.9.9 \"\$tmp_dir\" hey_9.9.9_linux_amd64.tar.gz
  "
  [[ "$status" -ne 0 ]]
  [[ "$output" == *"Checksum verification failed"* ]]
}

# install.ps1 equivalent, executed (not review-only): extract the two cosign
# functions from the AST — Main never runs — shadow cosign/Download-File, and
# drive all three version tiers.
@test "install.ps1 cosign version gate selects bare, flagged, and skip paths" {
  if ! command -v pwsh >/dev/null 2>&1; then
    if [[ -n "${CI:-}" ]]; then
      echo "pwsh is required in CI for install.ps1 cosign gate coverage" >&2
      return 1
    fi
    skip "pwsh not installed"
  fi

  cat > "$STUB_DIR/cosign-driver.ps1" <<'EOF'
$ErrorActionPreference = 'Stop'
$tokens = $null; $parseErrors = $null
$ast = [System.Management.Automation.Language.Parser]::ParseFile($env:INSTALL_PS1_PATH, [ref]$tokens, [ref]$parseErrors)
if ($parseErrors.Count -gt 0) { throw "install.ps1 parse errors: $($parseErrors -join '; ')" }
foreach ($name in @('Get-CosignBundleSupport', 'Verify-CosignSignature')) {
  $fn = $ast.Find({ param($n) $n -is [System.Management.Automation.Language.FunctionDefinitionAst] -and $n.Name -eq $name }, $true)
  if (-not $fn) { throw "$name not found in install.ps1" }
  . ([scriptblock]::Create($fn.Extent.Text))
}

function Step([string]$Message) { Write-Host "step: $Message" }
function Info([string]$Message) { Write-Host "info: $Message" }
function Fail([string]$Message) { throw $Message }
function Download-File([string]$Url, [string]$Destination) { Set-Content -Path $Destination -Value 'stub-bundle' }

$script:CosignVersion = ''
$script:CosignArgs = $null
function cosign {
  if ($args[0] -eq 'version') {
    "GitVersion:    $script:CosignVersion"
  } else {
    $script:CosignArgs = $args -join ' '
  }
  $global:LASTEXITCODE = 0
}

$tmp = Join-Path ([IO.Path]::GetTempPath()) ([IO.Path]::GetRandomFileName())
New-Item -ItemType Directory -Path $tmp | Out-Null
Set-Content -Path (Join-Path $tmp 'checksums.txt') -Value 'stub'

# v3: bare verify-blob, no compat flag, hey-cli identity.
$script:CosignVersion = 'v3.0.2'; $script:CosignArgs = $null
Verify-CosignSignature -Version '9.9.9' -BaseUrl 'https://example.invalid' -TmpDir $tmp
if (-not $script:CosignArgs) { throw 'v3: cosign verify-blob was not invoked' }
if ($script:CosignArgs -notmatch 'verify-blob') { throw "v3: unexpected args: $script:CosignArgs" }
if ($script:CosignArgs -match 'new-bundle-format') { throw "v3: compat flag must not be passed: $script:CosignArgs" }
if ($script:CosignArgs -notmatch 'github.com/basecamp/hey-cli/.github/workflows/release.yml@refs/tags/v9.9.9') { throw "v3: identity not pinned: $script:CosignArgs" }
'v3-ok'

# v2.6: compat flag required.
$script:CosignVersion = 'v2.6.0'; $script:CosignArgs = $null
Verify-CosignSignature -Version '9.9.9' -BaseUrl 'https://example.invalid' -TmpDir $tmp
if ($script:CosignArgs -notmatch '--new-bundle-format=true') { throw "v2.6: compat flag missing: $script:CosignArgs" }
'v26-ok'

# v2.4: warn + skip, verify-blob never runs.
$script:CosignVersion = 'v2.4.0'; $script:CosignArgs = $null
Verify-CosignSignature -Version '9.9.9' -BaseUrl 'https://example.invalid' -TmpDir $tmp
if ($null -ne $script:CosignArgs) { throw "v2.4: verify-blob must not run: $script:CosignArgs" }
'v24-ok'

# Garbage version: warn + skip.
$script:CosignVersion = 'devel'; $script:CosignArgs = $null
Verify-CosignSignature -Version '9.9.9' -BaseUrl 'https://example.invalid' -TmpDir $tmp
if ($null -ne $script:CosignArgs) { throw "garbage: verify-blob must not run: $script:CosignArgs" }
'garbage-ok'

Remove-Item -Recurse -Force $tmp
EOF

  run bash -c "
    set -euo pipefail
    export INSTALL_PS1_PATH='$INSTALL_PS1'
    pwsh -NoProfile -File '$STUB_DIR/cosign-driver.ps1'
  "
  [[ "$status" -eq 0 ]]
  [[ "$output" == *"v3-ok"* ]]
  [[ "$output" == *"v26-ok"* ]]
  [[ "$output" == *"v24-ok"* ]]
  [[ "$output" == *"garbage-ok"* ]]
}
