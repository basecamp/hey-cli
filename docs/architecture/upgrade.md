# The upgrade command

`hey upgrade` (`internal/cmd/upgrade.go`, `upgrade_selfupdate.go`, `release.go`) self-updates
installer/tarball installs under `$HOME` and delegates Homebrew and Scoop installs to their
package manager; system packages, Nix and `go install` builds are refused with guidance, as is
any build whose version is not a semantic version (`dev` included). The native path trusts
nothing it downloaded until the release's `checksums.txt.bundle` verifies with sigstore-go
against the Sigstore public-good trusted root (TUF-cached under `config.CacheDir()/sigstore-tuf`),
with the signing identity pinned to
`https://github.com/basecamp/hey-cli/.github/workflows/release.yml@refs/tags/v<version>` and
the GitHub Actions OIDC issuer. Only then is the archive's SHA-256 checked against the verified
`checksums.txt`, the binary extracted with hardening (no links, no nested paths, size caps),
probed with `--version`, and swapped in behind a `flock` with the old binary preserved until
the installed one is confirmed.

The verification tests in `upgrade_selfupdate_test.go` run hermetically against real release
fixtures in `internal/cmd/testdata/selfupdate/` (see its README for provenance — they are from
basecamp-cli until hey-cli has a signed release of its own). Everything else goes through the
seam variables (`releaseFetcher`, `bundleVerifier`, `binaryVersionProber`, …) so no test talks
to GitHub or runs a package manager.
