# Self-update verification fixtures

Real release artifacts used by `TestVerifyBundle*` in `upgrade_selfupdate_test.go`
to exercise Sigstore bundle verification hermetically (no network):

- `checksums.txt` — the signed artifact
- `checksums.txt.bundle` — the keyless cosign bundle (new protobuf bundle format)
- `trusted_root.json` — the Sigstore public-good trusted root at signing time

They come from **hey-cli v0.1.1**, the first release signed through this
repository's own release workflow, and the tests verify under its identity
(`https://github.com/basecamp/hey-cli/.github/workflows/release.yml@refs/tags/v0.1.1`).
Before that release existed they were basecamp-cli v0.8.1's artifacts.

To refresh for a newer release: download `checksums.txt` and
`checksums.txt.bundle` from the release, refresh `trusted_root.json` from the
Sigstore TUF cache (or `cosign trusted-root create`), and update
`fixtureIdentity` in the test.
