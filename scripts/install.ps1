# The documented install path is `irm ... | iex`, and Invoke-Expression
# evaluates this file in the caller's scope. The invoked script block below
# gives the installer its own scope, so $ErrorActionPreference and the helper
# functions do not leak into (or clobber) the interactive session. The body
# stays unindented: the here-strings' closing "@ must sit at column zero.
& {

$ErrorActionPreference = 'Stop'

try {
  [Net.ServicePointManager]::SecurityProtocol = [Net.ServicePointManager]::SecurityProtocol -bor [Net.SecurityProtocolType]::Tls12
} catch {
  # Ignore when the runtime manages TLS defaults.
}

# Environment options:
#   HEY_VERSION   Specific version to install (default: latest)
#   HEY_BIN_DIR   Where to install the binary
#
# This file must stay pure ASCII: the release pipeline stages and
# Authenticode-signs a CRLF copy of it, and Windows PowerShell 5.1 decodes a
# BOM-less file as Windows-1252 while jsign assumes UTF-8, so any byte above
# 0x7F makes the two digests disagree (HashMismatch).
$Repo = 'basecamp/hey-cli'
$Version = $env:HEY_VERSION
$BinDir = $env:HEY_BIN_DIR

function Step([string]$Message) {
  Write-Host "  -> $Message"
}

function Info([string]$Message) {
  Write-Host "  + $Message" -ForegroundColor Green
}

function Fail([string]$Message) {
  throw $Message
}

function Get-PlatformArch {
  $arch = $env:PROCESSOR_ARCHITECTURE
  if ($env:PROCESSOR_ARCHITEW6432) {
    $arch = $env:PROCESSOR_ARCHITEW6432
  }

  switch -Regex ($arch) {
    '^(AMD64|x86_64)$' { return 'amd64' }
    '^ARM64$' { return 'arm64' }
    default { Fail "Unsupported Windows architecture: $arch" }
  }
}

function Get-LatestVersion {
  Step 'Resolving latest release version...'

  # Follow the releases/latest redirect first to avoid GitHub API rate limits.
  # -MaximumRedirection 0 turns the expected 302 into a terminating error, so
  # we read Location off the caught response. Headers.Location is Uri on
  # PowerShell Core and string on Windows PowerShell 5.1, so coerce to string.
  $location = $null
  try {
    $response = Invoke-WebRequest -MaximumRedirection 0 -UseBasicParsing `
      -Headers @{ 'User-Agent' = 'hey-cli-installer' } `
      -Uri "https://github.com/$Repo/releases/latest" -ErrorAction Stop
    $location = $response.Headers.Location
  } catch {
    if ($_.Exception.Response) {
      $location = $_.Exception.Response.Headers.Location
      if (-not $location) {
        $location = $_.Exception.Response.Headers['Location']
      }
    }
  }

  if ($location) {
    $tag = ([string]$location).TrimEnd('/').Split('/')[-1]
    $candidate = $tag.TrimStart('v')
    if ($candidate -match '^[0-9]+\.[0-9]+\.[0-9]+(-[0-9A-Za-z.-]+)?$') {
      return $candidate
    }
  }

  # Fall back to the GitHub API if the redirect path didn't yield a semver tag.
  $release = Invoke-RestMethod -ErrorAction Stop `
    -Headers @{ 'User-Agent' = 'hey-cli-installer' } `
    -Uri "https://api.github.com/repos/$Repo/releases/latest"
  if (-not $release.tag_name) {
    Fail 'Could not determine latest release version from GitHub.'
  }

  return $release.tag_name.TrimStart('v')
}

function Download-File([string]$Url, [string]$Destination) {
  # -UseBasicParsing avoids initializing IE's MSHTML parser on Windows
  # PowerShell 5.1 -- required on Server Core and locked-down installs.
  # No-op on PowerShell 6+, where basic parsing is the only mode.
  Invoke-WebRequest -UseBasicParsing -ErrorAction Stop `
    -Headers @{ 'User-Agent' = 'hey-cli-installer' } `
    -Uri $Url -OutFile $Destination
}

function Verify-Checksum([string]$ChecksumsPath, [string]$ArchivePath, [string]$ArchiveName) {
  $expected = $null
  foreach ($line in Get-Content $ChecksumsPath) {
    if ($line -match '^(?<hash>[0-9a-fA-F]{64})\s+\*?(?<name>.+)$') {
      if ($Matches.name -eq $ArchiveName) {
        $expected = $Matches.hash.ToLowerInvariant()
        break
      }
    }
  }

  if (-not $expected) {
    Fail "Could not find checksum entry for $ArchiveName"
  }

  $actual = (Get-FileHash -Algorithm SHA256 -Path $ArchivePath).Hash.ToLowerInvariant()
  if ($actual -ne $expected) {
    Fail "Checksum verification failed for $ArchiveName"
  }

  Info 'Checksum verified'
}

# Get-CosignBundleSupport decides how to verify the release's Sigstore bundle,
# which is the new (protobuf, v0.3+json) format:
#   v3+          -> new-format parsing is the default; no extra flag ('')
#   v2.6 - v2.x  -> needs '--new-bundle-format=true' (v2.x defaults it to false)
#   < v2.6 / unparseable -> $null: cannot verify (v2.4 chokes on the bundle's
#                  tlog key type, v2.2 lacks the flag); caller warns and skips
function Get-CosignBundleSupport {
  try { $versionOutput = & cosign version 2>$null } catch { return $null }

  foreach ($line in @($versionOutput)) {
    if ("$line" -match 'GitVersion:\s*v?(\d+)\.(\d+)\.') {
      $major = [int]$Matches[1]
      $minor = [int]$Matches[2]
      if ($major -ge 3) { return '' }
      if ($major -eq 2 -and $minor -ge 6) { return '--new-bundle-format=true' }
      return $null
    }
  }

  return $null
}

function Verify-CosignSignature([string]$Version, [string]$BaseUrl, [string]$TmpDir) {
  if (-not (Get-Command cosign -ErrorAction SilentlyContinue)) {
    return
  }

  $bundleFlag = Get-CosignBundleSupport
  if ($null -eq $bundleFlag) {
    Step "Skipping signature verification: this cosign can't verify the release bundle format (need cosign >= 2.6)"
    return
  }

  Step 'Verifying cosign signature...'

  $bundlePath = Join-Path $TmpDir 'checksums.txt.bundle'
  $checksumsPath = Join-Path $TmpDir 'checksums.txt'
  Download-File -Url "$BaseUrl/checksums.txt.bundle" -Destination $bundlePath

  $cosignArgs = @('verify-blob', '--bundle', $bundlePath)
  if ($bundleFlag) {
    $cosignArgs += $bundleFlag
  }
  $cosignArgs += @(
    '--certificate-identity', "https://github.com/basecamp/hey-cli/.github/workflows/release.yml@refs/tags/v$Version",
    '--certificate-oidc-issuer', 'https://token.actions.githubusercontent.com',
    $checksumsPath
  )

  # Native exits don't trigger ErrorActionPreference=Stop on Windows PowerShell 5.1,
  # so check $LASTEXITCODE explicitly -- otherwise a verify failure would false-green.
  & cosign @cosignArgs
  if ($LASTEXITCODE -ne 0) {
    Fail 'Cosign signature verification failed'
  }

  Info 'Signature verified'
}

# Get-FirstRunFailureMessage diagnoses why running the freshly installed
# hey.exe failed. It best-effort probes the binary's Authenticode status and
# the Smart App Control state (Smart App Control blocks unsigned executables
# at process creation). Every branch leads with the original failure:
# diagnosis augments, never masks, the underlying error. PowerShell
# 5.1-compatible.
function Get-FirstRunFailureMessage([string]$Binary, [string]$Reason) {
  $sigStatus = $null
  try {
    $sigStatus = (Get-AuthenticodeSignature -FilePath $Binary -ErrorAction Stop).Status
  } catch { }

  # Smart App Control state: 0 = off, 1 = on, 2 = evaluation mode. Only 1
  # enforces; evaluation mode observes without blocking, so it must not
  # claim the failure.
  $sacState = $null
  try {
    $policy = Get-ItemProperty -Path 'HKLM:\SYSTEM\CurrentControlSet\Control\CI\Policy' -ErrorAction Stop
    $sacState = $policy.VerifiedAndReputablePolicyState
  } catch { }

  $lead = "Installed hey.exe to $Binary, but running it failed: $Reason"

  if ("$sigStatus" -eq 'NotSigned' -and $sacState -eq 1) {
    return @"
$lead

This build of hey.exe is not code-signed, and Smart App Control is enabled. Smart App Control blocks unsigned executables no matter where they were downloaded from, and it has no per-app exceptions. Two options:

  1. (Preferred) Install the Linux build inside WSL2 - Smart App Control does not apply there and your Windows security setup is untouched:
       wsl --install
     then, inside the WSL terminal:
       curl -fsSL https://raw.githubusercontent.com/basecamp/hey-cli/main/scripts/install.sh | bash

  2. Turn Smart App Control off (Windows Security > App & browser control > Smart App Control settings) and leave it off while using this unsigned build. Because there are no per-app exceptions, turning it back on re-blocks hey.exe on its next run - only re-enable after upgrading to a signed release. Windows 11 with the March/April 2026 updates can re-enable Smart App Control from Windows Security without a reset; on older builds re-enabling requires resetting Windows, so prefer the WSL2 option there.
"@
  }

  if ("$sigStatus" -eq 'NotSigned') {
    return @"
$lead

This build of hey.exe is not code-signed, and Windows Security or SmartScreen may have blocked or quarantined it. Check Windows Security > Protection history for a block or quarantine event, restore or allow hey.exe, then re-run the installer.
"@
  }

  return @"
$lead

If Windows Security or antivirus interfered, check Windows Security > Protection history, restore or allow hey.exe, then re-run the installer.
"@
}

function Get-PathEntries {
  param([string]$PathValue)

  if (-not $PathValue) {
    return @()
  }

  return $PathValue -split ';' | Where-Object { $_ }
}

function Normalize-PathEntry([string]$PathValue) {
  if (-not $PathValue) {
    return ''
  }

  return $PathValue.Trim().TrimEnd('\')
}

function Get-DefaultBinDir {
  $currentPathEntries = Get-PathEntries $env:Path
  $userPathEntries = Get-PathEntries ([Environment]::GetEnvironmentVariable('Path', 'User'))
  $allEntries = @($currentPathEntries + $userPathEntries) | ForEach-Object { Normalize-PathEntry $_ }

  $homeBin = Normalize-PathEntry (Join-Path $HOME 'bin')
  $homeLocalBin = Normalize-PathEntry (Join-Path $HOME '.local\bin')

  if ($allEntries -contains $homeBin) {
    return $homeBin
  }

  if ($allEntries -contains $homeLocalBin) {
    return $homeLocalBin
  }

  return $homeBin
}

function Ensure-UserPath([string]$Dir) {
  $userPath = [Environment]::GetEnvironmentVariable('Path', 'User')
  $segments = Get-PathEntries $userPath

  $normalizedSegments = $segments | ForEach-Object { Normalize-PathEntry $_ }
  $normalizedDir = Normalize-PathEntry $Dir
  if ($normalizedSegments -contains $normalizedDir) {
    return
  }

  $newPath = if ($userPath) { "$Dir;$userPath" } else { $Dir }
  [Environment]::SetEnvironmentVariable('Path', $newPath, 'User')
  $env:Path = "$Dir;$env:Path"
  Info "Added $Dir to your user PATH"
}

function Main {
  $arch = Get-PlatformArch
  if (-not $BinDir) {
    $BinDir = Get-DefaultBinDir
  }

  $resolvedVersion = if ($Version) { $Version } else { Get-LatestVersion }

  if ($resolvedVersion -notmatch '^[0-9]+\.[0-9]+\.[0-9]+(-[0-9A-Za-z.-]+)?$') {
    Fail "Invalid version '$resolvedVersion'. Expected semver format like 1.2.3 or 1.2.3-rc.1."
  }

  $archiveName = "hey_${resolvedVersion}_windows_${arch}.zip"
  $baseUrl = "https://github.com/$Repo/releases/download/v$resolvedVersion"

  Step "Downloading hey v$resolvedVersion for windows_$arch..."
  $tmpDir = Join-Path ([IO.Path]::GetTempPath()) ([IO.Path]::GetRandomFileName())
  New-Item -ItemType Directory -Path $tmpDir | Out-Null

  try {
    $archivePath = Join-Path $tmpDir $archiveName
    $checksumsPath = Join-Path $tmpDir 'checksums.txt'
    $extractDir = Join-Path $tmpDir 'extract'

    Download-File -Url "$baseUrl/$archiveName" -Destination $archivePath

    Step 'Verifying checksums...'
    Download-File -Url "$baseUrl/checksums.txt" -Destination $checksumsPath
    Verify-Checksum -ChecksumsPath $checksumsPath -ArchivePath $archivePath -ArchiveName $archiveName

    Verify-CosignSignature -Version $resolvedVersion -BaseUrl $baseUrl -TmpDir $tmpDir

    Step 'Extracting...'
    Expand-Archive -Path $archivePath -DestinationPath $extractDir -Force

    $binaryPath = Join-Path $extractDir 'hey.exe'
    if (-not (Test-Path $binaryPath)) {
      Fail 'hey.exe not found in archive. If Windows Security or antivirus removed it during extraction, check Windows Security > Protection history, restore it, and re-run the installer.'
    }

    $installedBinary = Join-Path $BinDir 'hey.exe'

    New-Item -ItemType Directory -Force -Path $BinDir | Out-Null
    # Windows holds an exclusive lock on running PE files; -Force doesn't help.
    # Generic catch -- typed catches miss ActionPreferenceStopException wrapping.
    try {
      Copy-Item -Force $binaryPath $installedBinary -ErrorAction Stop
    } catch {
      Fail "Failed to install hey.exe. If it is in use, close any running 'hey' processes and re-run the installer. If Windows Security quarantined it, check Windows Security > Protection history and restore it. (Original error: $($_.Exception.Message))"
    }
    Ensure-UserPath -Dir $BinDir
    Info "Installed hey to $installedBinary"

    # Smart App Control kills CreateProcess for unsigned executables, so the
    # first run is where a block surfaces. Generic catch per the Copy-Item
    # precedent above. A launch that succeeds but exits nonzero does not
    # throw in Windows PowerShell 5.1, so check $LASTEXITCODE as well.
    try {
      $installedVersion = & $installedBinary --version
    } catch {
      Fail (Get-FirstRunFailureMessage -Binary $installedBinary -Reason $_.Exception.Message)
    }
    if ($LASTEXITCODE -ne 0) {
      Fail (Get-FirstRunFailureMessage -Binary $installedBinary -Reason "hey --version exited with code $LASTEXITCODE")
    }
    Info "$installedVersion installed"

    Write-Host ''
    Write-Host '  Next steps:'
    Write-Host '    hey auth login    Authenticate with HEY'
    Write-Host '    hey --help        See what you can do'
    Write-Host ''
    Write-Host '  Installed executable:'
    Write-Host "    $installedBinary"
    Write-Host ''
    Write-Host '  New terminals pick up the PATH change; in this session, use the installed executable path directly.'
    Write-Host ''
  }
  finally {
    if (Test-Path $tmpDir) {
      Remove-Item -Recurse -Force $tmpDir
    }
  }
}

Main

}
