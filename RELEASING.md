# Releasing hey-cli

## Quick release

A stable release is two runs of the same command, with a PR merged in between:

```bash
make release VERSION=0.2.0   # opens "Prepare v0.2.0 release" and stops
# review and merge that PR, then from an up-to-date main:
make release VERSION=0.2.0   # pushes the v0.2.0 tag
```

`main` takes changes only through pull requests (the `main-gate` ruleset), and a
stable tag must point at a commit that carries its version, so the version lands
through a PR first. The script never pushes to `main`.

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
2. Runs `make release-check`, including the same pinned gosec version used by
   the release workflow and `check-size-release`, which builds every release
   target and holds each to the size budget (see [Release size budget](#release-size-budget))
3. For a **stable** version whose metadata is not yet stamped (the first run):
   stamps `nix/package.nix` (`scripts/stamp-nix-version.sh`) and
   `.claude-plugin/plugin.json` (`scripts/stamp-plugin-version.sh`) on
   `release/vX.Y.Z`, pushes that branch, opens a "Prepare vX.Y.Z release" PR
   labelled `release`, returns to `main` and stops. Running it again while that
   PR is open points at the PR rather than opening another, and a
   `release/vX.Y.Z` branch on origin with no open PR is refused rather than
   pushed over
4. Once the metadata is stamped (the run after the PR is merged, or any
   prerelease): creates the annotated tag and pushes only the tag

Before touching anything it fetches tags and refuses a tag that already exists at
another commit, or a stable version older than the latest stable tag, so a
rejected release pushes nothing. A tag push that is rejected — another operator
took the tag, or a repository rule refused it, which the error names — removes
the local tag so the next attempt starts clean. Pushing a stable tag by hand at
an unstamped commit fails the release at GoReleaser's stable metadata check
(`scripts/check-stable-metadata.sh`, covering the plugin stamp and the Nix
package version) rather than publishing stale metadata.

The release PR needs `gh`, and the `release` label keeps it out of the release
notes (see [Release notes](#release-notes)).

The [release workflow](.github/workflows/release.yml) then runs **against the tag
SHA** (which is why the metadata is merged first):

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
  are then attested with GitHub build provenance. The size report that follows
  is informational (`continue-on-error`): by then the release is published, and
  a failure there must not skip the attestation or the jobs below
- After publication: `macos-verify`, `windows-verify`, `nix-verify` (stable
  only), `aur-publish` (stable only, non-blocking), `sync-skills` (stable only,
  non-blocking)

## Release notes

GitHub generates the notes from `.github/release.yml`, which groups each PR by
the labels `.github/labeler.yml` applies from the paths it touches:
Authentication, TUI, CLI, Agents and integrations, Dependencies, Documentation,
then Other Changes. A PR lands under the first group that matches, so one that
touches both the TUI and the commands is listed under TUI. Read the notes once
the release is published and move or reword entries where the paths tell the
wrong story (`gh release edit vX.Y.Z --notes-file notes.md`). The release PR is
left out by its `release` label.

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

`.size-budget` holds ceilings for every platform (`stripped_max_mib`,
`gzip_max_mib`), measured against the binary as shipped. `scripts/check-size-budget.sh`
runs:

- in `make release-check` (`check-size-release`), over a goreleaser snapshot of
  every release target, so the dry run fails before a tag exists — a tag that
  fails later can never be reused
- as a goreleaser build hook on each binary, so a breach stops the release
  before anything is archived or published
- over `dist/` after publication, for the per-platform table in the job
  summary; informational, since the release is already out
- on `./bin/hey` on PRs (`make check-size`)

The hook runs before notarize signs the macOS binaries, and a snapshot signs
nothing, so `SIZE_BUDGET_UNSIGNED` names the platforms still unsigned and the
script charges each the signature it will ship with (marked `~` in the table):
32 bytes per 4 KiB page plus 32 KiB for macOS (326 KB on the 36.7 MiB v1.7.0
binary), 16 KiB for Windows (10,024 bytes at v1.7.0). Without that, v1.7.0's
darwin_amd64 passed the gate at 36.73 MiB and shipped at 37.04, over a 37 MiB
ceiling.

The ceilings are ceil(max × 1.15) over the largest shipped binary:
**43 MiB stripped / 15 MiB gzipped** since v1.7.0 (darwin_amd64, 37.04 / 12.7
MiB). `.size-budget` records what grew at each raise; an increase beyond a
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

Stable releases mirror `skills/` into [basecamp/skills](https://github.com/basecamp/skills),
which several CLIs share. `scripts/sync-skills.sh` owns only this CLI's skills there:
it records the names it published in `.managed-skills.hey-cli` at the target root and
removes a `skills/<name>` only when that manifest lists it, the release no longer ships
it, and no other CLI's `.managed-skills.*` claims it (a collision is warned about and
left alone), and refuses outright to publish a name another CLI's manifest holds. A
target with no `.managed-skills.hey-cli` yet is a first run: nothing is removed. A push
rejected because another CLI published first is retried by applying the whole sync
again from the remote's new tip, not by replaying the stale commit. The legacy shared `.managed-skills` is rewritten as a comment-only tombstone
so a CLI still on the pre-fix script — which deleted everything its own tree lacked —
deletes nothing (basecamp/skills#5). The script always clones the target fresh and pushes
only the commit it made, so there is no checkout to hand it. `scripts/test-sync-skills.sh`
(`make test-sync-skills`, in `make check`) pins the contract by running the script as both
CLIs against a local bare repository — real clones, commits and pushes, no network.

If that job fails, a `skills-sync`-labeled issue is filed; recover with the
`Sync skills` workflow (`workflow_dispatch`, stable tag, optional dry run). It
refuses anything but the latest stable release so it cannot roll the distribution
repo back, and it runs the sync script from the dispatching branch against the
tag's skills tree — so when the failure was a defect in `sync-skills.sh` itself,
merge the fix to main and dispatch; no new release needed. The release-time job makes
the same check before it publishes, so an older release whose run stalls, or whose
failed sync is rerun after a newer release has shipped, skips the sync instead of
rolling it back.

## Local dry runs

```bash
make release VERSION=0.2.0 DRY_RUN=1   # preflight, including govulncheck, gosec and every target's size
make test-release                      # goreleaser snapshot, no publish/sign
```

A dry run opens no PR, creates no branch and pushes nothing; for an unstamped
stable version it says the real run would open the release PR.

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
