# Releasing hey-cli

## Quick Release

```bash
# Run preflight checks and tag
make release VERSION=v1.0.0

# Or dry-run first
make release VERSION=v1.0.0 DRY_RUN=1
```

Pushing the tag triggers the GitHub Actions release workflow, which:
1. Runs the full test suite
2. Runs security scans (gitleaks, Trivy, gosec)
3. Collects PGO profile from benchmarks
4. Builds binaries for all platforms (linux/darwin/windows/freebsd/openbsd x amd64/arm64)
5. Signs macOS binaries (Developer ID + notarization)
6. Signs checksums with cosign (keyless, OIDC)
7. Generates SBOMs with Syft
8. Generates AI-powered release summary
9. Publishes Homebrew cask to `basecamp/homebrew-tap`
10. Publishes Scoop manifest to `basecamp/homebrew-tap`
11. Builds .deb and .rpm packages
12. Publishes to AUR (if `AUR_KEY` configured)
13. Checks CLI surface compatibility against previous release
14. Syncs skills to `basecamp/skills`

## Versioning

Follow [semver](https://semver.org/). Use `v` prefix for tags: `v1.0.0`, `v1.1.0-rc.1`.

## CI Secrets

| Secret | Purpose |
|--------|---------|
| `RELEASE_APP_ID` (var) | GitHub App ID for `cli-release-bot` |
| `RELEASE_APP_PRIVATE_KEY` | GitHub App private key for tap + skills push |
| `MACOS_SIGN_P12` | Base64-encoded Developer ID Application .p12 |
| `MACOS_SIGN_PASSWORD` | Password for the .p12 certificate |
| `MACOS_NOTARY_KEY` | Base64-encoded App Store Connect API key (.p8) |
| `MACOS_NOTARY_KEY_ID` | App Store Connect API key ID (10 chars) |
| `MACOS_NOTARY_ISSUER_ID` | App Store Connect issuer UUID |
| `AUR_KEY` | ed25519 SSH private key for AUR (optional) |

## Distribution Channels

| Channel | Location | Updated by |
|---------|----------|------------|
| GitHub Releases | `basecamp/hey-cli/releases` | GoReleaser |
| Homebrew | `basecamp/homebrew-tap` Casks/hey.rb | GoReleaser |
| Scoop | `basecamp/homebrew-tap` hey.json | GoReleaser |
| AUR | `aur.archlinux.org/packages/hey-cli` | `publish-aur.sh` |
| deb/rpm | GitHub release assets | GoReleaser (nfpm) |
| Go install | `go install github.com/basecamp/hey-cli/cmd/hey@latest` | Go module proxy |
| curl installer | `scripts/install.sh` | Manual |

## Dry Run

```bash
# Full preflight without tagging
make release VERSION=v1.0.0 DRY_RUN=1

# GoReleaser snapshot (local build test — generate completions first)
go build -o hey-tmp ./cmd/hey
mkdir -p completions
./hey-tmp completion bash > completions/hey.bash
./hey-tmp completion zsh > completions/hey.zsh
./hey-tmp completion fish > completions/hey.fish
rm hey-tmp
goreleaser release --snapshot --clean
```

## AUR Setup

1. Generate ed25519 SSH keypair: `ssh-keygen -t ed25519 -f aur_key`
2. Add public key to your AUR account profile
3. Add private key as `AUR_KEY` secret on the hey-cli repo

## PGO (Profile-Guided Optimization)

Collect a profile locally:

```bash
make collect-profile
make build-pgo
```

The release workflow collects PGO profiles automatically from benchmarks before building.
