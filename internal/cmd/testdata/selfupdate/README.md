# Self-update verification fixtures

Real release artifacts used by `TestVerifyBundle*` in `upgrade_selfupdate_test.go`
to exercise Sigstore bundle verification hermetically (no network):

- `checksums.txt` — the signed artifact
- `checksums.txt.bundle` — the keyless cosign bundle (new protobuf bundle format)
- `trusted_root.json` — the Sigstore public-good trusted root at signing time

They come from **basecamp-cli v0.8.1**, not from a hey-cli release: hey-cli's
first signed release did not exist when the upgrade command landed. The tests
therefore verify under basecamp-cli's workflow identity
(`https://github.com/basecamp/basecamp-cli/.github/workflows/release.yml@refs/tags/v0.8.1`),
which is exactly what proves the identity check bites — the same bundle must
fail under hey-cli's identity for the same version number.

Swap them for a hey-cli release once one exists: download `checksums.txt` and
`checksums.txt.bundle` from the release, refresh `trusted_root.json` with
`cosign trusted-root create` (or copy it from the TUF cache), and update
`fixtureIdentity` in the test.
