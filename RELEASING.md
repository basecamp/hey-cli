# Releasing hey-cli

## Quick release

```bash
make release VERSION=0.2.0
```

## Release candidate

```bash
make release VERSION=0.2.0-rc.1
```

## Dry run

```bash
make release VERSION=0.2.0 DRY_RUN=1
```

`VERSION` accepts `0.2.0` or `v0.2.0`; the tag is always `v0.2.0`.

## What happens

`scripts/release.sh`:

1. Validates the version, that you are on the default branch with a clean tree
   synced to origin, and that `go.mod` has no `replace` directives
2. Runs `make release-check`, including the same pinned gosec version used by the release workflow
3. For **stable** versions only: runs `scripts/update-nix-flake.sh` (verifies the
   Nix build via Docker and recomputes `vendorHash` if needed) and
   `scripts/stamp-plugin-version.sh`, commits `nix/package.nix` and
   `.claude-plugin/plugin.json`, and pushes that commit to main
4. Creates the annotated tag and pushes it

Before touching anything it fetches tags and refuses a tag that already exists at
another commit, or a stable version older than the latest stable tag, so a
rejected release leaves main untouched. Pushing a stable tag by hand skips the
metadata commit; GoReleaser then fails the release at its stable metadata check
(`scripts/check-stable-metadata.sh`, covering the plugin stamp and the Nix
package version) rather than publishing stale metadata.

The [release workflow](.github/workflows/release.yml) then runs **against the tag
SHA** (which is why the prep commit is pushed first):

- `test`: lint lockstep, fmt, vet, lint, unit tests, bats suite, tidy, surface
  snapshot, race detector, govulncheck, CLI surface compatibility vs the previous tag
- `security`: gitleaks, Trivy, gosec
- `release`: preflights the macOS and Windows signing secrets, verifies the tag
  is on main, installs Temurin 21 and a sha256-checked jsign, then GoReleaser
  checks that a stable tag carries the plugin stamp and the Nix package
  version, builds
  darwin/linux/windows/freebsd/openbsd × amd64/arm64 and deb/rpm/apk packages,
  signs and notarizes macOS binaries, Authenticode-signs the Windows binaries and
  a staged copy of `install.ps1` (`hey_installer.ps1`), signs `checksums.txt`
  with cosign (keyless, `checksums.txt.bundle`), generates SBOMs, publishes the
  GitHub release, and updates the Homebrew cask and Scoop manifest; the checksums
  are then attested with GitHub build provenance
- After publication: `macos-verify`, `windows-verify`, `nix-verify` (stable
  only), `aur-publish` (stable only, non-blocking), `sync-skills` (stable only,
  non-blocking)

## Stable vs prerelease

| Surface | Stable `0.2.0` | Prerelease `0.2.0-rc.1` |
|---------|----------------|-------------------------|
| GitHub Releases | Normal release, marked Latest | Marked prerelease; Latest stays on stable |
| Release assets | Archives, checksums + bundle, SBOMs, deb/rpm/apk, signed installer | Same |
| Homebrew cask `hey` | Updated in `basecamp/homebrew-tap` | Unchanged |
| Scoop `hey` | Updated in `basecamp/homebrew-tap` | Unchanged |
| AUR `hey-cli` | Updated by `publish-aur.sh` | Unchanged |
| Nix flake | `nix/package.nix` bumped and verified | Unchanged |
| Claude plugin metadata | `.claude-plugin/plugin.json` stamped | Unchanged |
| Skills distribution | Synced to `basecamp/skills` | Unchanged |
| Release notes | Diffed against the previous **stable** tag | GoReleaser default |

## Versioning

Pre-1.0: minor bumps for features, patch bumps for fixes. Use `-rc.N` when testers
need a build before the next stable version.

`v0.1.0` exists as a draft with no assets: its release failed at the cosign step
(cosign v3 removed the flags the config used) and `proxy.golang.org` had already
cached the tag, so it could not be reused. The first working release is `v0.1.1`.

## Secrets and variables

Everything lives on the **`release` environment** (Settings → Environments), not
at repository scope. `manage-release-env.sh` in basecamp-cli's scripts directory audits and
converges the environment across the CLI repos and copies secrets in from
1Password; add new rows there rather than pasting by hand.

| Name | Kind | Purpose |
|------|------|---------|
| `RELEASE_CLIENT_ID` | variable | GitHub App client ID for `cli-release-bot` (tap and skills pushes) |
| `RELEASE_APP_PRIVATE_KEY` | secret | GitHub App private key |
| `MACOS_SIGN_P12` | secret | Base64 Developer ID Application certificate (.p12) |
| `MACOS_SIGN_PASSWORD` | secret | .p12 password |
| `MACOS_NOTARY_KEY` | secret | Base64 App Store Connect API key (.p8) |
| `MACOS_NOTARY_KEY_ID` | secret | App Store Connect key ID |
| `MACOS_NOTARY_ISSUER_ID` | secret | App Store Connect issuer UUID |
| `SM_API_KEY` | secret | DigiCert ONE API key for KeyLocker |
| `SM_CLIENT_CERT_FILE_B64` | secret | Base64 (single line) of the DigiCert ONE mTLS client certificate `.p12` attachment |
| `SM_CLIENT_CERT_PASSWORD` | secret | Client certificate password |
| `AUR_KEY` | secret | ed25519 SSH private key for the AUR (optional; publish skips without it) — set it from the key file, never a paste: `gh secret set AUR_KEY --env release < ~/.ssh/aur_hey_cli` |

The `SM_*` values come from the **DigiCert CodeSigning Cert** item (Development
vault): the two text fields byte-exact with no trailing newline, and the `.p12`
attachment encoded with `base64 -w0`. Malformed certificate material has bitten
before — validate the decoded `.p12` opens with the password (`openssl pkcs12
-info -noout`) before setting the secret.

The macOS and Windows preflights are gated on `github.repository == 'basecamp/hey-cli'`, so the
canonical repo refuses to release unsigned. Forks cannot release at all: the
GitHub App token step needs the `release` environment and GoReleaser publishes to
`basecamp/hey-cli`.

## Windows signing

Windows binaries and the installer copy are Authenticode-signed from Linux CI via
[jsign](https://ebourg.github.io/jsign/) against DigiCert KeyLocker (cloud HSM).
bc3-desktop's `docs/windows-signing.md` is the canonical runbook; this repo and
basecamp-cli are further consumers of the same certificate.

- Certificate: OV code signing, `CN=37signals LLC`, expires **2027-04-30**. The
  DigiCert ONE certificate ID (not the 1Password item's keypair alias) is pinned
  once, as `SIGN_ALIAS` in `release.yml` — keep it in sync with bc3-desktop and
  basecamp-cli when the certificate is renewed. The certificate ID is a valid
  jsign `--alias`: for `DIGICERTONE`, jsign accepts the certificate's ID or its
  alias (hex-and-dash values are looked up as `certificates?id=`) and derives
  the signing keypair from the certificate record — basecamp-cli releases sign
  with this exact value.
- jsign version and jar sha256 are pinned in the "Prepare Windows signing" step.
  jsign ≥ 7.5 is required; 7.1–7.3 are broken against DigiCert ONE's current API.
- Quota: KeyLocker signatures draw from a budget shared with bc3-desktop and
  basecamp-cli. Each tag consumes **3 signatures** (two exes + the installer copy);
  rc(s) + stable is ≥ 6, and re-runs after post-signing failures consume more.
  Confirm headroom with the certificate owner before a release burst.
- `scripts/sign-windows.sh` fails closed: all `SM_*` empty skips (forks,
  `make test-release`), partial configuration or a signing/timestamp error aborts
  the release during the build phase — nothing is published. Re-run the workflow
  on the same tag after the outage clears.
- `windows-verify` asserts a Valid signature, the `37signals LLC` signer and a
  timestamp countersignature on both arches, and verifies `hey_installer.ps1`
  under pwsh and Windows PowerShell 5.1.

## Nix flake maintenance

`nix profile install github:basecamp/hey-cli` builds from `nix/package.nix`.
Stable releases bump its version and, when `go.mod`/`go.sum` moved, its
`vendorHash` (requires Docker). To do it by hand, e.g. after an SDK bump:

```bash
make update-nix-hash
```

The PR-time `nix-build` job fails on every dependency bump by design — dependabot
does not update `vendorHash` — and prints the correct hash with that command.

`nix/go.nix` selects the Go toolchain from the locked nixpkgs snapshot for both
the package and development shell. When `go.mod` moves beyond that snapshot,
run `nix flake update nixpkgs` and commit `flake.lock` with the toolchain bump.

## CLI surface compatibility

`.surface` is the committed snapshot of commands and flags. `make check-surface`
(`TestSurfaceSnapshot`) fails a PR that changes the CLI without regenerating it
(`make update-surface`); `make check-surface-compat` diffs `.surface` against the
previous stable tag's copy (`git show <tag>:.surface`, no second build) and fails
on removals. A deliberate removal is acknowledged by listing the `.surface` line
in `.surface-breaking` in the same PR; prune entries after the release ships.

## Release size budget

`.size-budget` holds per-platform ceilings (`stripped_max_mib`, `gzip_max_mib`);
`scripts/check-size-budget.sh` runs as a goreleaser build hook on each binary
(so a breach stops the release before anything is archived, signed or
published), again over `dist/` afterwards for the per-platform table in the job
summary, and on `./bin/hey` on PRs (`make check-size`).

Baseline (goreleaser snapshot at the `hey upgrade` change): the largest target,
windows_amd64, measured **25.2 MiB stripped / 8.8 MiB gzipped**; pre-Sigstore the
same target was 15.2 / 5.4 MiB. sigstore-go's in-process verification cost
~10 MiB stripped, accepted so releases verify without a cosign dependency. The
ceilings are ceil(max × 1.15) = **29 / 11 MiB**, enforced. An increase beyond a
ceiling needs a review of what grew, not a budget bump.

## Pin and reference lockstep

`make check-release-lockstep` (in `make check`, the PR lint job and the release
gate) verifies that the golangci-lint pins agree across workflows and meet the
floor, that `.mise.toml` and `release.yml` pin the same goreleaser, that the
pre-commit golangci-lint rev matches CI, and that every `scripts/*.sh` named in
docs, workflows, the Makefile or goreleaser config exists (and vice versa).

## Things that look safe to rename but are not

- **`.github/workflows/release.yml`** is part of every shipped binary's trust
  identity: the installers and `hey upgrade` verify
  `https://github.com/basecamp/hey-cli/.github/workflows/release.yml@refs/tags/v<ver>`.
  Renaming the file invalidates verification for every binary already installed.
- **Cask `hey` / Scoop `hey` / AUR `hey-cli`** names are what installed copies
  upgrade through.

## AUR

The AUR package installs the prebuilt release binaries (with shell completions);
`publish-aur.sh` derives the PKGBUILD from the published release assets. If the
release-time publish fails (the AUR is down for maintenance regularly), a
labeled `aur-publish` issue is filed and the release itself is unaffected.
Recover by dispatching the `Publish to AUR` workflow with the released version —
it is idempotent and refuses downgrades.

One-time setup: `ssh-keygen -t ed25519 -f aur_key`, add the public key to the AUR
account, store the private key as `AUR_KEY`.

## Skills sync

Stable releases mirror `skills/` into `basecamp/skills`. If that job fails, a
`skills-sync`-labeled issue is filed; recover with the `Sync skills` workflow
(`workflow_dispatch`, stable tag, optional dry run). It refuses anything but the
latest stable release so it cannot roll the distribution repo back, and it runs
the sync script from the dispatching branch against the tag's skills tree — so
when the failure was a defect in `sync-skills.sh` itself, merge the fix to main
and dispatch; no new release needed.

## Local dry runs

```bash
make release VERSION=0.2.0 DRY_RUN=1   # preflight, including govulncheck and gosec
make test-release                      # goreleaser snapshot, no publish/sign
```

The dry-run preflight runs the same gosec version as `.github/workflows/security.yml`;
`make check-release-lockstep` fails if those pins drift.

`make test-release` needs `syft` on PATH for the SBOM step (`mise use syft`); it
blanks the signing env so notarization is skipped, and the stable metadata
check does not run for snapshots.

## Distribution channels

| Channel | Location | Updated by |
|---------|----------|------------|
| GitHub Releases | `basecamp/hey-cli/releases` | GoReleaser |
| Installers | `scripts/install.sh`, `scripts/install.ps1` (served from main) | Merge to main; daily `installer-smoke` canary |
| Homebrew cask `hey` | `basecamp/homebrew-tap` Casks/hey.rb | GoReleaser (stable) |
| Scoop `hey` | `basecamp/homebrew-tap` hey.json | GoReleaser (stable) |
| AUR `hey-cli` | `aur.archlinux.org/packages/hey-cli` | `publish-aur.sh` (stable) |
| deb/rpm/apk | GitHub release assets | GoReleaser (nfpm) |
| Nix flake | `flake.nix` | Self-serve |
| Go install | `go install github.com/basecamp/hey-cli/cmd/hey@latest` | Go module proxy |
| Claude plugin | `.claude-plugin/` | `stamp-plugin-version.sh` (stable) |
| Skills | `basecamp/skills` | `sync-skills.sh` (stable) |
