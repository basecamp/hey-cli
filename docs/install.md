# Installing and upgrading

The [README](../README.md) covers the one-line installers. This page has every other way
to install hey, how a release is verified, what `hey upgrade` does for each install method,
how to set up shell completions, and what to do when Windows blocks the binary.

## Installer scripts

**macOS / Linux / WSL2**

```bash
curl -fsSL https://hey.com/install-cli | bash
```

**Windows (PowerShell)**

```powershell
irm https://hey.com/install-cli.ps1 | iex
```

On Windows 11 with Smart App Control, see [Windows: Smart App Control and SmartScreen](#windows-smart-app-control-and-smartscreen) below if the install is blocked.

Both scripts download the release for your platform, verify its SHA-256 checksum, and — when `cosign` is installed — verify the release's keyless Sigstore signature (cosign v3 as-is, v2.6+ with `--new-bundle-format=true`; older versions skip signature verification with a warning). Set `HEY_VERSION` to pin a release and `HEY_BIN_DIR` to choose the install directory.

## Other installation methods

**mise** (and Omarchy, where `omarchy-mise-install` also adds the desktop pieces):
```bash
mise use -g github:basecamp/hey-cli
omarchy-mise-install github:basecamp/hey-cli hey   # on Omarchy
```

**Homebrew (macOS / Linux):**
```bash
brew install --cask basecamp/tap/hey
```

**Linux (deb/rpm/apk):**
```bash
# Download from https://github.com/basecamp/hey-cli/releases/latest
sudo apt install ./hey-cli_*_linux_amd64.deb                 # Debian/Ubuntu
sudo dnf install ./hey-cli_*_linux_amd64.rpm                 # Fedora/RHEL
sudo apk add --allow-untrusted ./hey-cli_*_linux_amd64.apk   # Alpine
```
Arm64: substitute `arm64` for `amd64` in the filename. Verify the SHA-256 checksum from `checksums.txt` before installing unsigned Alpine packages.

**Scoop (Windows):**
```powershell
scoop bucket add basecamp https://github.com/basecamp/homebrew-tap
scoop install hey
```

**Nix:**
```bash
nix profile install github:basecamp/hey-cli
```

**Go install:**
```bash
go install github.com/basecamp/hey-cli/cmd/hey@latest
```

**From source** (requires Go 1.26+; [mise](https://mise.jdx.dev) installs the right version):
```bash
mise install       # install Go 1.26
make install       # build and install into /usr/local/bin/hey
```

**GitHub Release:** download from [Releases](https://github.com/basecamp/hey-cli/releases). Every release ships `checksums.txt` and a keyless Sigstore signature `checksums.txt.bundle`, verifiable with:
```bash
cosign verify-blob --bundle checksums.txt.bundle \
  --certificate-identity "https://github.com/basecamp/hey-cli/.github/workflows/release.yml@refs/tags/v<VERSION>" \
  --certificate-oidc-issuer https://token.actions.githubusercontent.com checksums.txt
```
That command is for cosign v3. With cosign v2.6–v2.x add `--new-bundle-format=true`; older versions cannot verify the bundle.


## Upgrading

```bash
hey upgrade
hey upgrade 0.2.0-rc.1   # target a specific release, e.g. a prerelease
```

Upgrading only ever moves forward: a requested version at or below the installed one is a
no-op. Mise and native installs can select a specific version; Homebrew and Scoop always
follow their manager's own version and refuse a pinned upgrade.

What happens depends on how hey was installed:

- **Installer script / tarball** (a binary under your home directory, e.g. `~/.local/bin` or `~/bin`): upgrades in place. hey downloads the release for your platform, verifies its Sigstore signature (the keyless `checksums.txt.bundle` published by the release pipeline, identity-pinned to the release workflow and tag) and SHA-256 checksum, swaps the executable transactionally, and confirms the installed binary reports the new version. On failure the previous binary is restored; in the worst case — restoration itself fails mid-swap — the error names the preserved backup file next to the binary so you can put it back by hand.
- **mise**: delegates to mise, updates the configuration that selected the running binary, then verifies normal mise resolution selects the new version.
- **Homebrew / Scoop**: delegates to `brew upgrade --cask basecamp/tap/hey` / `scoop update hey`, then verifies the manager-installed binary actually reports the new version.
- **System packages** (apt/dnf/apk, AUR, Nix) and **`go install` builds**: never touched. `hey upgrade` exits nonzero with upgrade guidance for that install method (the exact command where it can be known, e.g. `go install` or `yay -S hey-cli`; otherwise which package manager to use).

`hey upgrade` exits 0 only when there is no update, or the update was applied *and confirmed*. Every other outcome is a structured failure (`"ok": false` in JSON) with one of these codes:

| Code | Meaning |
|---|---|
| `upgrade_required` | An update exists but hey won't apply it for this install method (or this is not a release build) — the hint carries the right next step |
| `upgrade_incomplete` | The package manager exited 0 but the binary still reports the old version |
| `upgrade_unverified` | The upgrade may have worked, but the installed version could not be confirmed |
| `upgrade_failed` | The update check, download, signature/checksum verification, or executable swap failed — the previous binary remains installed (or the error names the preserved backup if restoration also failed) |

`hey version` prints the installed version; `hey version --json` adds the commit, build date, Go version and build source (`release`, `go install` or `dev`). `hey doctor` warns when a newer release is available.


## Shell completions

Installs from a package manager (AUR, deb, rpm, Homebrew, Scoop) already carry
completions system-wide. Every other install — mise, the installer script, a
downloaded tarball — registers them nowhere, so:

```bash
hey shell-completion install         # takes the shell from $SHELL
hey shell-completion install fish    # or name it
```

The file lands where your shell already looks: `~/.local/share/bash-completion/completions/hey`,
`~/.config/fish/completions/hey.fish`, and for zsh a directory on your `fpath` —
falling back to `~/.local/share/zsh/site-functions/_hey` and telling you the one
line that puts it there. `hey setup` installs them too, so a first run usually
settles this without being asked. `hey doctor` reports whether they are
installed, current, and reachable.

Re-running is a no-op once the file matches. Like the agent skills, hey only
writes completion files it wrote: each carries a `managed by hey-cli` line on
its second line, and a file without it is never overwritten — move it aside, or
pass `--force`.

Installed through **mise** (which is how [Omarchy](https://omarchy.org) installs
hey), the binary is reached through a shim that re-resolves the tool on every
call. The installed script asks the resolved binary directly, so pressing Tab
does not pay for that.

## Windows: Smart App Control and SmartScreen

To check whether your installed binary is signed:

```powershell
Get-AuthenticodeSignature (Get-Command hey).Source
```

**Smart App Control** (Windows 11) blocks unsigned executables no matter where
they were downloaded from, and it has no per-app exceptions — this applies to
the PowerShell installer, Scoop installs, and manual downloads alike. If it
blocks an unsigned `hey.exe`, two options:

1. **Use WSL2 (preferred).** Install the Linux build inside WSL2 — Smart App
   Control doesn't apply there and your Windows security setup is untouched:
   `wsl --install`, then inside the WSL terminal:
   `curl -fsSL https://hey.com/install-cli | bash`
2. **Turn Smart App Control off** (Windows Security → App & browser control →
   Smart App Control settings) **and leave it off while using the unsigned
   build.** Because there are no per-app exceptions, turning it back on
   re-blocks `hey.exe` on its next run — only re-enable after upgrading to a
   signed build. Windows 11 with the March/April 2026 updates can re-enable
   Smart App Control from Windows Security without a reset; on older builds
   re-enabling requires resetting Windows, so prefer WSL2 there.

**SmartScreen** (without Smart App Control) may warn on first run of an
unrecognized executable — choose "More info" → "Run anyway" if you downloaded
the release from this repository.
