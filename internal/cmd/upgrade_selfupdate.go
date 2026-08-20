package cmd

import (
	"archive/tar"
	"archive/zip"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"github.com/gofrs/flock"
	"github.com/sigstore/sigstore-go/pkg/bundle"
	"github.com/sigstore/sigstore-go/pkg/root"
	"github.com/sigstore/sigstore-go/pkg/tuf"
	"github.com/sigstore/sigstore-go/pkg/verify"
	"golang.org/x/mod/semver"

	"github.com/basecamp/hey-cli/internal/config"
	"github.com/basecamp/hey-cli/internal/output"
	"github.com/basecamp/hey-cli/internal/version"
)

// Native self-update: download the platform release asset, verify authenticity
// (Sigstore bundle over checksums.txt) and integrity (sha256), then swap the
// running executable transactionally. Applies only to installer-script/tarball
// installs under the user's home directory — package-manager installs are
// delegated or refused with method-specific instructions.

const (
	// upgradeStagePrefix names every transient file the self-update writes into
	// the install directory (staged binaries, write probes). Leftovers from a
	// crash are reaped by cleanupUpgradeSidecars on later invocations.
	upgradeStagePrefix = ".hey-upgrade-"

	maxArchiveBytes   = 200 << 20 // downloaded release archive
	maxTextAssetBytes = 1 << 20   // checksums.txt and its bundle

	sigstoreOIDCIssuer = "https://token.actions.githubusercontent.com"
)

// maxBinaryBytes caps the uncompressed extracted binary (decompression-bomb
// guard). Variable so tests can exercise the cap without a 300MB fixture.
var maxBinaryBytes int64 = 300 << 20

// Self-update seams, swappable for tests (see upgrade.go for the pattern).
var (
	selfUpdateTargetResolver = resolveSelfUpdateTarget
	binaryVersionProber      = probeBinaryVersion
	bundleVerifier           = verifySigstoreBundle
	goInstallChecker         = version.IsGoInstall
	euidResolver             = os.Geteuid
	homeDirResolver          = os.UserHomeDir
	linkFile                 = os.Link
	renameFile               = os.Rename
	removeFile               = os.Remove
	upgradeLocker            = acquireUpgradeLock
	selfUpdateHTTPClient     = &http.Client{Timeout: 5 * time.Minute}
)

// upgradeLockPath names the lock file that serializes upgrades and sidecar
// cleanup across target's install directory. Directory-wide on purpose: the
// staging namespace (`.hey-upgrade-*`) is shared by every hey binary in the
// directory regardless of its filename, so a per-target lock would let a
// sibling install (say hey-preview next to hey) pass its own lock and reap
// another upgrade's live staging files. The dot before "lock" keeps the lock
// file itself out of the always-reapable `.hey-upgrade-*` glob.
func upgradeLockPath(target string) string {
	return filepath.Join(filepath.Dir(target), ".hey-upgrade.lock")
}

// acquireUpgradeLock takes the exclusive upgrade lock for target's install
// directory without blocking. Held across staging, replacement, verification,
// and rollback so a concurrent upgrade cannot mutate the same directory's
// staging namespace and a concurrent invocation's sidecar cleanup cannot
// reap this upgrade's live staging files. The lock file itself is left
// behind and reaped by cleanupUpgradeSidecars on a later invocation.
func acquireUpgradeLock(target string) (*flock.Flock, error) {
	lock := flock.New(upgradeLockPath(target))
	locked, err := lock.TryLock()
	if err != nil {
		return nil, err
	}
	if !locked {
		return nil, errors.New("another hey upgrade is already running")
	}
	return lock, nil
}

// releaseWorkflowIdentity is the certificate SAN the release pipeline signs
// under (keyless cosign from the release workflow, pinned to the version tag).
func releaseWorkflowIdentity(ver string) string {
	return "https://github.com/basecamp/hey-cli/.github/workflows/release.yml@refs/tags/v" + ver
}

func releaseTagURL(ver string) string {
	return "https://github.com/basecamp/hey-cli/releases/tag/v" + ver
}

// installerHint is the one-line reinstall command for this platform.
func installerHint() string {
	if runtime.GOOS == "windows" {
		return "irm https://raw.githubusercontent.com/basecamp/hey-cli/main/scripts/install.ps1 | iex"
	}
	return "curl -fsSL https://raw.githubusercontent.com/basecamp/hey-cli/main/scripts/install.sh | bash"
}

// resolveSelfUpdateTarget returns the canonical, case-preserving path of the
// running executable. Distinct from resolvedExecutablePath, which lowercases
// for substring matching and must stay match-only.
func resolveSelfUpdateTarget() (string, error) {
	exe, err := os.Executable()
	if err != nil {
		return "", err
	}
	if resolved, err := filepath.EvalSymlinks(exe); err == nil {
		exe = resolved
	}
	return filepath.Abs(exe)
}

// selfUpdateIneligibility applies the fail-closed path policy. It returns a
// non-empty reason (and a reason-specific hint) when the target must not be
// self-updated; empty reason means eligible.
func selfUpdateIneligibility(target string) (reason, hint string) {
	if runtime.GOOS != "windows" && euidResolver() == 0 {
		return "running_as_root",
			"Re-run hey upgrade as the user who installed the CLI, or upgrade via your system package manager"
	}

	home, err := homeDirResolver()
	if err != nil || home == "" || !pathWithin(home, target) {
		return "system_path", systemPathHint(target)
	}

	if err := probeDirWritable(filepath.Dir(target)); err != nil {
		return "not_writable",
			fmt.Sprintf("The install directory %s is not writable. Fix its permissions, or reinstall: %s", filepath.Dir(target), installerHint())
	}

	return "", ""
}

// systemPathHint names the upgrade path for a binary outside the user's home.
// The Nix store is immutable by design, so it gets its own wording; everything
// else is some system package manager's territory.
func systemPathHint(target string) string {
	if strings.HasPrefix(filepath.ToSlash(target), "/nix/store/") {
		return "This binary lives in the Nix store. Upgrade it the Nix way, e.g.: nix profile upgrade hey-cli (or update your flake pin)"
	}
	return "This binary is outside your home directory. Upgrade it with the package manager that installed it " +
		"(yay -S hey-cli on Arch; apt, dnf or apk with the hey-cli package; nix profile upgrade hey-cli), " +
		"or reinstall under your home: " + installerHint()
}

// pathWithin reports whether target is inside root after canonicalizing both
// paths (symlinks resolved, absolute). Containment is decided by filepath.Rel
// — never by string prefixing — so sibling-prefix dirs (/Users/jeremyx vs
// /Users/jeremy) and symlinked escapes cannot pass.
func pathWithin(rootDir, target string) bool {
	canonRoot, err := canonicalPath(rootDir)
	if err != nil {
		return false
	}
	canonTarget, err := canonicalPath(target)
	if err != nil {
		return false
	}
	rel, err := filepath.Rel(canonRoot, canonTarget)
	if err != nil {
		return false
	}
	return rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)) && !filepath.IsAbs(rel)
}

func canonicalPath(p string) (string, error) {
	resolved, err := filepath.EvalSymlinks(p)
	if err != nil {
		return "", err
	}
	return filepath.Abs(resolved)
}

// probeDirWritable verifies dir accepts new files by actually creating one.
func probeDirWritable(dir string) error {
	f, err := os.CreateTemp(dir, upgradeStagePrefix+"probe-")
	if err != nil {
		return err
	}
	name := f.Name()
	_ = f.Close()
	return os.Remove(name)
}

// runNativeSelfUpdate performs the download → verify → swap → confirm flow.
// Returns writeUpgradeOK on confirmed success; every other outcome is a
// *output.Error.
func runNativeSelfUpdate(ctx context.Context, w io.Writer, target, current string, release releaseInfo) error {
	latest := release.Version

	ext, binName := "tar.gz", "hey"
	if runtime.GOOS == "windows" {
		ext, binName = "zip", "hey.exe"
	}
	archiveName := fmt.Sprintf("hey_%s_%s_%s.%s", latest, runtime.GOOS, runtime.GOARCH, ext)

	archiveAsset, found := release.asset(archiveName)
	if !found {
		fmt.Fprintln(w)
		fmt.Fprintf(w, "No prebuilt binary for %s/%s — download the release from:\n  %s\n", runtime.GOOS, runtime.GOARCH, releaseTagURL(latest))
		return &output.Error{
			Code:    "upgrade_required",
			Message: fmt.Sprintf("update available (%s → %s) but release v%s has no prebuilt binary for %s/%s", current, latest, latest, runtime.GOOS, runtime.GOARCH),
			Hint:    "Download manually: " + releaseTagURL(latest),
		}
	}
	checksumsAsset, found := release.asset("checksums.txt")
	if !found {
		return errUpgradeFailed(fmt.Sprintf("release v%s is missing checksums.txt — cannot verify the download", latest))
	}
	bundleAsset, found := release.asset("checksums.txt.bundle")
	if !found {
		return errUpgradeFailed(fmt.Sprintf("release v%s is missing checksums.txt.bundle — cannot verify authenticity", latest))
	}

	tmpDir, err := os.MkdirTemp("", "hey-upgrade-")
	if err != nil {
		return errUpgradeFailed(fmt.Sprintf("could not create a download directory: %v", err))
	}
	defer os.RemoveAll(tmpDir)

	fmt.Fprintf(w, "Downloading %s… ", archiveName)
	archivePath := filepath.Join(tmpDir, archiveName)
	archiveSHA, err := downloadFileSHA256(ctx, archiveAsset.DownloadURL, archivePath, maxArchiveBytes)
	if err != nil {
		fmt.Fprintln(w, "failed")
		return errUpgradeFailed(fmt.Sprintf("download failed: %v", err))
	}
	checksums, err := fetchAssetBytes(ctx, checksumsAsset.DownloadURL, maxTextAssetBytes)
	if err != nil {
		fmt.Fprintln(w, "failed")
		return errUpgradeFailed(fmt.Sprintf("could not download checksums.txt: %v", err))
	}
	bundleBytes, err := fetchAssetBytes(ctx, bundleAsset.DownloadURL, maxTextAssetBytes)
	if err != nil {
		fmt.Fprintln(w, "failed")
		return errUpgradeFailed(fmt.Sprintf("could not download checksums.txt.bundle: %v", err))
	}
	fmt.Fprintln(w, "done")

	fmt.Fprint(w, "Verifying signature… ")
	if verifyErr := bundleVerifier(checksums, bundleBytes, latest); verifyErr != nil {
		fmt.Fprintln(w, "failed")
		return errUpgradeFailed(fmt.Sprintf("signature verification failed for checksums.txt: %v", verifyErr))
	}
	expected, found := parseChecksums(checksums)[archiveName]
	if !found {
		fmt.Fprintln(w, "failed")
		return errUpgradeFailed(fmt.Sprintf("verified checksums.txt has no entry for %s", archiveName))
	}
	if !strings.EqualFold(expected, archiveSHA) {
		fmt.Fprintln(w, "failed")
		return errUpgradeFailed(fmt.Sprintf("checksum mismatch for %s: expected %s, got %s", archiveName, expected, archiveSHA))
	}
	fmt.Fprintln(w, "ok")

	fmt.Fprint(w, "Installing… ")
	staged, err := stageBinary(archivePath, ext, binName, target)
	if err != nil {
		fmt.Fprintln(w, "failed")
		return errUpgradeFailed(fmt.Sprintf("could not stage the new binary: %v", err))
	}
	defer os.Remove(staged) // no-op after a successful rename

	if info, statErr := os.Stat(target); statErr == nil {
		_ = os.Chmod(staged, info.Mode().Perm())
	}

	if reported, probeErr := binaryVersionProber(ctx, staged); probeErr != nil {
		fmt.Fprintln(w, "failed")
		return errUpgradeFailed(fmt.Sprintf("downloaded binary failed its pre-install check: %v", probeErr))
	} else if !sameVersion(reported, latest) {
		fmt.Fprintln(w, "failed")
		return errUpgradeFailed(fmt.Sprintf("downloaded binary reports version %s, expected %s", reported, latest))
	}

	backup, err := replaceExecutable(runtime.GOOS, target, staged)
	if err != nil {
		fmt.Fprintln(w, "failed")
		var cat *swapCatastropheError
		if errors.As(err, &cat) {
			cat.backup = preserveRecoveryArtifact(target, cat.backup)
		}
		return swapFailureError(target, err)
	}

	if reported, probeErr := binaryVersionProber(ctx, target); probeErr != nil || !sameVersion(reported, latest) {
		fmt.Fprintln(w, "failed")
		detail := fmt.Sprintf("reports %s, expected %s", reported, latest)
		if probeErr != nil {
			detail = fmt.Sprintf("could not be probed: %v", probeErr)
		}
		if restoreErr := restoreBackup(runtime.GOOS, target, backup); restoreErr != nil {
			kept := preserveRecoveryArtifact(target, backup)
			return errUpgradeFailedHint(
				fmt.Sprintf("installed binary %s, and restoring the previous version failed: %v", detail, restoreErr),
				fmt.Sprintf("Your previous binary was preserved at %s — move it back to %s, or reinstall: %s", kept, target, installerHint()),
			)
		}
		return errUpgradeFailed(fmt.Sprintf("installed binary %s — the previous version (%s) was restored", detail, current))
	}

	discardBackup(target, backup)

	fmt.Fprintln(w, "done")
	fmt.Fprintf(w, "Upgraded %s → %s\n", current, latest)
	return writeUpgradeOK(
		map[string]string{"status": "upgraded", "from": current, "to": latest, "method": "native"},
		fmt.Sprintf("Upgraded %s → %s", current, latest),
	)
}

// sameVersion compares two version strings semantically ("v" prefix
// tolerated). Anything unparseable never matches.
func sameVersion(a, b string) bool {
	a, b = normalizeSemver(a), normalizeSemver(b)
	return semver.IsValid(a) && semver.IsValid(b) && semver.Compare(a, b) == 0
}

func errUpgradeFailed(msg string) *output.Error {
	return errUpgradeFailedHint(msg, "The existing install was left in place. Re-run hey upgrade, or reinstall: "+installerHint())
}

func errUpgradeFailedHint(msg, hint string) *output.Error {
	return &output.Error{Code: "upgrade_failed", Message: msg, Hint: hint}
}

// downloadFileSHA256 streams url to dest, returning the sha256 of the bytes
// written. limit caps the download size.
func downloadFileSHA256(ctx context.Context, url, dest string, limit int64) (string, error) {
	resp, err := httpGetOK(ctx, url)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	f, err := os.OpenFile(dest, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return "", err
	}
	h := sha256.New()
	n, err := io.Copy(io.MultiWriter(f, h), io.LimitReader(resp.Body, limit+1))
	if closeErr := f.Close(); err == nil {
		err = closeErr
	}
	if err != nil {
		return "", err
	}
	if n > limit {
		return "", fmt.Errorf("download exceeds %d bytes", limit)
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

func fetchAssetBytes(ctx context.Context, url string, limit int64) ([]byte, error) {
	resp, err := httpGetOK(ctx, url)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	data, err := io.ReadAll(io.LimitReader(resp.Body, limit+1))
	if err != nil {
		return nil, err
	}
	if int64(len(data)) > limit {
		return nil, fmt.Errorf("download exceeds %d bytes", limit)
	}
	return data, nil
}

func httpGetOK(ctx context.Context, url string) (*http.Response, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", version.UserAgent())
	resp, err := selfUpdateHTTPClient.Do(req)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode != http.StatusOK {
		_ = resp.Body.Close()
		return nil, fmt.Errorf("GET %s: unexpected status %d", url, resp.StatusCode)
	}
	return resp, nil
}

// verifySigstoreBundle verifies the Sigstore bundle over the checksums.txt
// bytes: certificate identity pinned to the release workflow at the version
// tag, issuer pinned to GitHub OIDC, with SCT + transparency-log + observer
// timestamp thresholds. The trusted root comes from the Sigstore TUF repo
// (cached locally).
func verifySigstoreBundle(checksums, bundleBytes []byte, ver string) error {
	trusted, err := sigstoreTrustedRoot()
	if err != nil {
		return fmt.Errorf("fetch sigstore trusted root: %w", err)
	}
	return verifyBundleWithRoot(trusted, checksums, bundleBytes, releaseWorkflowIdentity(ver))
}

// verifyBundleWithRoot verifies bundleBytes over checksums against trusted,
// requiring the signing certificate's SAN to equal identity exactly.
func verifyBundleWithRoot(trusted root.TrustedMaterial, checksums, bundleBytes []byte, identity string) error {
	var b bundle.Bundle
	if err := b.UnmarshalJSON(bundleBytes); err != nil {
		return fmt.Errorf("parse signature bundle: %w", err)
	}

	verifier, err := verify.NewVerifier(trusted,
		verify.WithSignedCertificateTimestamps(1),
		verify.WithTransparencyLog(1),
		verify.WithObserverTimestamps(1),
	)
	if err != nil {
		return err
	}

	certID, err := verify.NewShortCertificateIdentity(sigstoreOIDCIssuer, "", identity, "")
	if err != nil {
		return err
	}

	_, err = verifier.Verify(&b, verify.NewPolicy(
		verify.WithArtifact(bytes.NewReader(checksums)),
		verify.WithCertificateIdentity(certID),
	))
	return err
}

func sigstoreTrustedRoot() (root.TrustedMaterial, error) {
	opts := tuf.DefaultOptions()
	if cacheDir := config.CacheDir(); cacheDir != "" {
		opts.CachePath = filepath.Join(cacheDir, "sigstore-tuf")
	}
	return root.FetchTrustedRootWithOptions(opts)
}

// parseChecksums parses goreleaser's checksums.txt: one "hash  name" (or
// "hash *name") entry per line, CRLF-tolerant.
func parseChecksums(data []byte) map[string]string {
	sums := make(map[string]string)
	for _, line := range strings.Split(string(data), "\n") {
		fields := strings.Fields(strings.TrimRight(line, "\r"))
		if len(fields) != 2 {
			continue
		}
		name := strings.TrimPrefix(fields[1], "*")
		if name == "" {
			continue
		}
		sums[name] = strings.ToLower(fields[0])
	}
	return sums
}

// stageBinary extracts the release binary from the verified archive into the
// target's own directory (same volume ⇒ the final rename is atomic) and
// returns the staged path.
func stageBinary(archivePath, ext, binName, target string) (string, error) {
	suffix := ""
	if strings.HasSuffix(binName, ".exe") {
		suffix = ".exe" // staged binary must be executable for the pre-install probe
	}
	staged := filepath.Join(filepath.Dir(target), upgradeStagePrefix+upgradeRandSuffix()+suffix)

	var err error
	if ext == "zip" {
		err = extractZipMember(archivePath, binName, staged)
	} else {
		err = extractTarGzMember(archivePath, binName, staged)
	}
	if err != nil {
		_ = os.Remove(staged)
		return "", err
	}
	return staged, nil
}

func upgradeRandSuffix() string {
	var b [4]byte
	if _, err := rand.Read(b[:]); err != nil {
		return fmt.Sprintf("%d", os.Getpid())
	}
	return hex.EncodeToString(b[:])
}

// isArchiveRootMember reports whether an archive entry name denotes the
// expected root member literally. Only the conventional "./" prefix is
// tolerated — dot-segment aliases such as "nested/../hey", which path.Clean
// would collapse to the member, are not root entries and must not match.
func isArchiveRootMember(name, member string) bool {
	return name == member || name == "./"+member
}

// extractTarGzMember writes exactly the archive-root member named `member` to
// dest. Hardened: link and symlink entries anywhere in the archive are
// rejected, duplicate members are rejected, and the uncompressed size is
// capped. Archive entry names are never joined into paths.
func extractTarGzMember(archivePath, member, dest string) error {
	f, err := os.Open(archivePath)
	if err != nil {
		return err
	}
	defer f.Close()

	gz, err := gzip.NewReader(f)
	if err != nil {
		return err
	}
	defer gz.Close()

	tr := tar.NewReader(gz)
	found := false
	for {
		hdr, err := tr.Next()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return err
		}
		if hdr.Typeflag == tar.TypeLink || hdr.Typeflag == tar.TypeSymlink {
			return fmt.Errorf("archive contains link entry %q — refusing to extract", hdr.Name)
		}
		if hdr.Typeflag != tar.TypeReg || !isArchiveRootMember(hdr.Name, member) {
			continue
		}
		if found {
			return fmt.Errorf("archive contains duplicate %q entries", member)
		}
		found = true
		if err := writeExtractedFile(dest, tr); err != nil {
			return err
		}
	}
	if !found {
		return fmt.Errorf("archive does not contain %q at its root", member)
	}
	return nil
}

func extractZipMember(archivePath, member, dest string) error {
	zr, err := zip.OpenReader(archivePath)
	if err != nil {
		return err
	}
	defer zr.Close()

	found := false
	for _, zf := range zr.File {
		if zf.Mode()&os.ModeSymlink != 0 {
			return fmt.Errorf("archive contains symlink entry %q — refusing to extract", zf.Name)
		}
		if zf.FileInfo().IsDir() || !isArchiveRootMember(zf.Name, member) {
			continue
		}
		if found {
			return fmt.Errorf("archive contains duplicate %q entries", member)
		}
		found = true

		rc, err := zf.Open()
		if err != nil {
			return err
		}
		writeErr := writeExtractedFile(dest, rc)
		_ = rc.Close()
		if writeErr != nil {
			return writeErr
		}
	}
	if !found {
		return fmt.Errorf("archive does not contain %q at its root", member)
	}
	return nil
}

// writeExtractedFile copies r to dest, capped at maxBinaryBytes. The reader is
// bounded by io.LimitReader, which is the decompression-bomb guard gosec's
// G110 looks for around io.Copy.
func writeExtractedFile(dest string, r io.Reader) error {
	f, err := os.OpenFile(dest, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o755) //nolint:gosec // G302: it's the executable being installed
	if err != nil {
		return err
	}
	n, err := io.Copy(f, io.LimitReader(r, maxBinaryBytes+1)) //nolint:gosec // G110: bounded by LimitReader and checked below
	if closeErr := f.Close(); err == nil {
		err = closeErr
	}
	if err != nil {
		return err
	}
	if n > maxBinaryBytes {
		return fmt.Errorf("extracted binary exceeds %d bytes — refusing", maxBinaryBytes)
	}
	return nil
}

// replaceExecutable swaps staged into place at target, returning the path of
// the preserved previous binary. The target path is continuously occupied on
// unix: the old inode is preserved via hard link (or a synced copy when the
// filesystem refuses links) and a single rename-over installs the new binary.
// On Windows a loaded exe cannot be renamed over, so the running exe moves to
// `<target>.old` first; a failed second rename rolls that back.
func replaceExecutable(goos, target, staged string) (string, error) {
	if goos == "windows" {
		backup := target + ".old"
		// A leftover .old from an earlier upgrade could be a crash's recovery
		// artifact — preserve it under a unique name rather than deleting it.
		if _, statErr := os.Lstat(backup); statErr == nil {
			_ = renameFile(backup, target+".recovered-"+upgradeRandSuffix())
		}
		if err := renameFile(target, backup); err != nil {
			return "", fmt.Errorf("move current binary aside: %w", err)
		}
		if err := renameFile(staged, target); err != nil {
			if restoreErr := renameFile(backup, target); restoreErr != nil {
				return "", &swapCatastropheError{backup: backup, installErr: err, restoreErr: restoreErr}
			}
			return "", fmt.Errorf("install new binary: %w", err)
		}
		return backup, nil
	}

	backup := target + ".old-" + upgradeRandSuffix()
	if err := linkFile(target, backup); err != nil {
		if copyErr := copyFileSync(target, backup); copyErr != nil {
			return "", fmt.Errorf("preserve current binary: %w", copyErr)
		}
	}
	if err := renameFile(staged, target); err != nil {
		_ = os.Remove(backup)
		return "", fmt.Errorf("install new binary: %w", err)
	}
	return backup, nil
}

// swapCatastropheError reports the worst-case swap failure: the running
// executable was moved aside, installing the new binary failed, AND moving
// the original back also failed — the target path may now have no binary at
// all. Callers must surface the preserved backup path instead of claiming
// the existing install was left in place.
type swapCatastropheError struct {
	backup     string
	installErr error
	restoreErr error
}

func (e *swapCatastropheError) Error() string {
	return fmt.Sprintf("install new binary: %v (restoring the original also failed: %v)", e.installErr, e.restoreErr)
}

// swapFailureError maps a replaceExecutable failure to the user-facing error.
// The ordinary case leaves the previous binary installed; the catastrophic
// case must not claim that — it names the preserved backup and how to recover.
func swapFailureError(target string, err error) *output.Error {
	var cat *swapCatastropheError
	if errors.As(err, &cat) {
		return errUpgradeFailedHint(
			fmt.Sprintf("could not install the new binary and could not restore the previous one: %v — %s may now be missing its executable", err, target),
			fmt.Sprintf("Your previous binary was preserved at %s — move it back to %s, or reinstall: %s", cat.backup, target, installerHint()),
		)
	}
	return errUpgradeFailed(fmt.Sprintf("could not install the new binary: %v", err))
}

// restoreBackup puts the preserved previous binary back at target after a
// failed post-install probe. Unix is a single rename-over (target stays
// occupied); Windows moves the bad binary aside first.
func restoreBackup(goos, target, backup string) error {
	if goos == "windows" {
		aside := filepath.Join(filepath.Dir(target), upgradeStagePrefix+"failed-"+upgradeRandSuffix()+".exe")
		if err := renameFile(target, aside); err != nil {
			return err
		}
		if err := renameFile(backup, target); err != nil {
			return err
		}
		_ = os.Remove(aside)
		return nil
	}
	return renameFile(backup, target)
}

// preserveRecoveryArtifact renames a backup to a unique `.recovered-<rand>`
// name once it has become the user's only good binary (rollback failed).
// The unique suffix means a pre-existing recovery artifact from an earlier
// failure is never clobbered. Returns the path to reference — the original
// backup path when even this rename fails, which is still safe: cleanup
// never reaps `.old*` or `.recovered-*` names (only an upgrade disposes of
// its own verified-garbage backup, via discardBackup).
func preserveRecoveryArtifact(target, backup string) string {
	recovered := target + ".recovered-" + upgradeRandSuffix()
	if err := renameFile(backup, recovered); err != nil {
		return backup
	}
	return recovered
}

// discardBackup disposes of the pre-upgrade backup after the installed
// binary has been probe-verified — only then is the backup provably garbage
// (a positive safe-to-reap decision; cleanup never infers it). Windows keeps
// the running old exe locked (delete fails, rename succeeds), so the
// fallback renames it into the always-reaped staging namespace for a later
// invocation's cleanup.
func discardBackup(target, backup string) {
	if removeFile(backup) == nil {
		return
	}
	_ = renameFile(backup, filepath.Join(filepath.Dir(target), upgradeStagePrefix+"reap-"+upgradeRandSuffix()))
}

// copyFileSync writes a fully-synced copy of src at dst, preserving src's
// mode. Fallback for filesystems without hard-link support.
func copyFileSync(src, dst string) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()

	info, err := in.Stat()
	if err != nil {
		return err
	}

	out, err := os.OpenFile(dst, os.O_WRONLY|os.O_CREATE|os.O_EXCL, info.Mode().Perm())
	if err != nil {
		return err
	}
	_, err = io.Copy(out, in)
	if err == nil {
		err = out.Sync()
	}
	if closeErr := out.Close(); err == nil {
		err = closeErr
	}
	if err != nil {
		_ = os.Remove(dst)
		return err
	}
	return nil
}

// probeBinaryVersion runs `path --version` and returns the reported semantic
// version (the trailing field, "v" stripped).
func probeBinaryVersion(ctx context.Context, path string) (string, error) {
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()

	out, err := exec.CommandContext(ctx, path, "--version").Output() //nolint:gosec // G204: path is the resolved install target or our own staged file
	if err != nil {
		return "", err
	}
	fields := strings.Fields(strings.TrimSpace(string(out)))
	if len(fields) == 0 {
		return "", fmt.Errorf("empty --version output from %s", path)
	}
	return strings.TrimPrefix(fields[len(fields)-1], "v"), nil
}

// cleanupUpgradeSidecars removes leftover self-update staging files (the
// `.hey-upgrade-*` namespace) next to the running executable. Runs on every
// ordinary CLI invocation so a crashed upgrade's staging files — and backups
// an upgrade explicitly marked safe to reap — don't linger.
func cleanupUpgradeSidecars() {
	exe, err := resolveSelfUpdateTarget()
	if err != nil {
		return
	}
	cleanupUpgradeSidecarsFor(exe)
}

func cleanupUpgradeSidecarsFor(exe string) {
	// Never reap while the executable itself is missing: after a catastrophic
	// swap the `.old` sidecar may be the only remaining binary (this guard
	// matters when exe was resolved from somewhere other than the running
	// process, e.g. a sibling install invoking cleanup).
	if _, err := os.Stat(exe); err != nil {
		return
	}

	// Skip entirely while an upgrade holds the lock: the staging pattern
	// matches that upgrade's live files. Reap happens under the lock; the
	// lock file itself is removed last (best-effort — Windows can't delete a
	// file with an open handle, so it waits for a later pass).
	//
	// Only the `.hey-upgrade-*` staging namespace is ever reaped — a positive
	// safe-to-reap contract: upgrades move a verified-garbage backup into this
	// namespace themselves (discardBackup). `.old*` and `.recovered-*` names
	// are NEVER reaped here, because after a failed rollback they can be the
	// user's only good binary.
	lock := flock.New(upgradeLockPath(exe))
	locked, err := lock.TryLock()
	if err != nil || !locked {
		return
	}

	matches, _ := filepath.Glob(filepath.Join(filepath.Dir(exe), upgradeStagePrefix+"*"))
	for _, m := range matches {
		_ = os.Remove(m)
	}

	_ = os.Remove(upgradeLockPath(exe))
	_ = lock.Unlock()
}
