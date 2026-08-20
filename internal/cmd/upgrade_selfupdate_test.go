package cmd

import (
	"archive/tar"
	"archive/zip"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/gofrs/flock"
	"github.com/sigstore/sigstore-go/pkg/root"
)

// --- seam stubs ---

func stubGoInstallChecker(t *testing.T, isGoInstall bool) {
	t.Helper()
	orig := goInstallChecker
	goInstallChecker = func() bool { return isGoInstall }
	t.Cleanup(func() { goInstallChecker = orig })
}

func stubSelfUpdateTarget(t *testing.T, path string, err error) {
	t.Helper()
	orig := selfUpdateTargetResolver
	selfUpdateTargetResolver = func() (string, error) { return path, err }
	t.Cleanup(func() { selfUpdateTargetResolver = orig })
}

func stubBinaryVersionProber(t *testing.T, probe func(context.Context, string) (string, error)) {
	t.Helper()
	orig := binaryVersionProber
	binaryVersionProber = probe
	t.Cleanup(func() { binaryVersionProber = orig })
}

func stubBundleVerifier(t *testing.T, verify func(checksums, bundleBytes []byte, ver string) error) {
	t.Helper()
	orig := bundleVerifier
	bundleVerifier = verify
	t.Cleanup(func() { bundleVerifier = orig })
}

func stubEuid(t *testing.T, uid int) {
	t.Helper()
	orig := euidResolver
	euidResolver = func() int { return uid }
	t.Cleanup(func() { euidResolver = orig })
}

func stubHomeDir(t *testing.T, home string) {
	t.Helper()
	orig := homeDirResolver
	homeDirResolver = func() (string, error) { return home, nil }
	t.Cleanup(func() { homeDirResolver = orig })
}

func stubLinkFile(t *testing.T, link func(oldname, newname string) error) {
	t.Helper()
	orig := linkFile
	linkFile = link
	t.Cleanup(func() { linkFile = orig })
}

func stubRenameFile(t *testing.T, rename func(oldpath, newpath string) error) {
	t.Helper()
	orig := renameFile
	renameFile = rename
	t.Cleanup(func() { renameFile = orig })
}

func mustWriteFile(t *testing.T, path string, data []byte, mode os.FileMode) {
	t.Helper()
	mustNoError(t, os.WriteFile(path, data, mode))
}

func mustMkdirAll(t *testing.T, path string) {
	t.Helper()
	mustNoError(t, os.MkdirAll(path, 0o755))
}

func readFile(t *testing.T, path string) []byte {
	t.Helper()
	data, err := os.ReadFile(path)
	mustNoError(t, err)
	return data
}

func assertFileContent(t *testing.T, path string, want []byte) {
	t.Helper()
	if got := readFile(t, path); !bytes.Equal(got, want) {
		t.Errorf("%s content = %q, want %q", path, got, want)
	}
}

func assertExists(t *testing.T, path string, msg string) {
	t.Helper()
	if _, err := os.Stat(path); err != nil {
		t.Errorf("%s: %s (%v)", path, msg, err)
	}
}

func assertNotExists(t *testing.T, path string, msg string) {
	t.Helper()
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Errorf("%s: %s (stat err: %v)", path, msg, err)
	}
}

func assertErrorContains(t *testing.T, err error, substr string) {
	t.Helper()
	if err == nil {
		t.Fatalf("expected an error containing %q, got nil", substr)
	}
	assertContains(t, err.Error(), substr)
}

// --- archive builders ---

type tarEntry struct {
	name     string
	body     []byte
	typeflag byte
	linkname string
}

func buildTarGz(t *testing.T, entries []tarEntry) []byte {
	t.Helper()
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gz)
	for _, e := range entries {
		typeflag := e.typeflag
		if typeflag == 0 {
			typeflag = tar.TypeReg
		}
		hdr := &tar.Header{
			Name:     e.name,
			Mode:     0o755,
			Size:     int64(len(e.body)),
			Typeflag: typeflag,
			Linkname: e.linkname,
		}
		mustNoError(t, tw.WriteHeader(hdr))
		if typeflag == tar.TypeReg {
			_, err := tw.Write(e.body)
			mustNoError(t, err)
		}
	}
	mustNoError(t, tw.Close())
	mustNoError(t, gz.Close())
	return buf.Bytes()
}

type zipEntry struct {
	name    string
	body    []byte
	symlink bool
}

func buildZip(t *testing.T, entries []zipEntry) []byte {
	t.Helper()
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	for _, e := range entries {
		hdr := &zip.FileHeader{Name: e.name, Method: zip.Deflate}
		if e.symlink {
			hdr.SetMode(os.ModeSymlink | 0o777)
		} else {
			hdr.SetMode(0o755)
		}
		w, err := zw.CreateHeader(hdr)
		mustNoError(t, err)
		_, err = w.Write(e.body)
		mustNoError(t, err)
	}
	mustNoError(t, zw.Close())
	return buf.Bytes()
}

func writeArchiveFile(t *testing.T, data []byte) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "archive")
	mustWriteFile(t, path, data, 0o600)
	return path
}

// --- flow fixture ---

// nativeFlowFixture wires every seam for an end-to-end `hey upgrade` exercise
// of the native self-update path against an httptest release server.
type nativeFlowFixture struct {
	target     string // installed fake binary
	oldContent []byte
	newContent []byte
	latest     string
	server     *httptest.Server
	hits       atomic.Int32
	release    releaseInfo
}

func setupNativeFlow(t *testing.T, current, latest string) *nativeFlowFixture {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("native flow fixture builds tar.gz archives (unix asset shape)")
	}

	f := &nativeFlowFixture{
		latest:     latest,
		oldContent: []byte("old-binary-" + current),
		newContent: []byte("new-binary-" + latest),
	}

	stubVersion(t, current)
	stubGoInstallChecker(t, false)
	stubEuid(t, 1000)

	home := t.TempDir()
	binDir := filepath.Join(home, "bin")
	mustMkdirAll(t, binDir)
	f.target = filepath.Join(binDir, "hey")
	mustWriteFile(t, f.target, f.oldContent, 0o755)
	stubHomeDir(t, home)
	stubSelfUpdateTarget(t, f.target, nil)

	archiveName := fmt.Sprintf("hey_%s_%s_%s.tar.gz", latest, runtime.GOOS, runtime.GOARCH)
	archive := buildTarGz(t, []tarEntry{{name: "hey", body: f.newContent}})
	sum := sha256.Sum256(archive)
	checksums := []byte(hex.EncodeToString(sum[:]) + "  " + archiveName + "\n")
	bundleBytes := []byte("stub-bundle")

	mux := http.NewServeMux()
	serve := func(name string, data []byte) {
		mux.HandleFunc("/"+name, func(w http.ResponseWriter, r *http.Request) {
			f.hits.Add(1)
			_, _ = w.Write(data)
		})
	}
	serve(archiveName, archive)
	serve("checksums.txt", checksums)
	serve("checksums.txt.bundle", bundleBytes)
	f.server = httptest.NewServer(mux)
	t.Cleanup(f.server.Close)

	f.release = releaseInfo{
		Version: latest,
		Assets: []releaseAsset{
			{Name: archiveName, DownloadURL: f.server.URL + "/" + archiveName},
			{Name: "checksums.txt", DownloadURL: f.server.URL + "/checksums.txt"},
			{Name: "checksums.txt.bundle", DownloadURL: f.server.URL + "/checksums.txt.bundle"},
		},
	}
	stubUpgradeCheckers(t, upgradeCheckersStub{release: &f.release})

	stubBundleVerifier(t, func(_, _ []byte, _ string) error { return nil })

	// Staged and installed binaries both report the new version by default.
	stubBinaryVersionProber(t, func(_ context.Context, path string) (string, error) {
		return latest, nil
	})

	// Confine the flow's MkdirTemp so leftover temp dirs are detectable.
	t.Setenv("TMPDIR", t.TempDir())

	return f
}

func (f *nativeFlowFixture) upgradeTempLeftovers(t *testing.T) []string {
	t.Helper()
	matches, err := filepath.Glob(filepath.Join(os.TempDir(), "hey-upgrade-*"))
	mustNoError(t, err)
	return matches
}

// sidecarLeftovers globs the `.hey*` namespace next to target, excluding
// only the lock file — a designed leftover an upgrade never removes itself
// (deleting a held flock file is racy); the next invocation's startup
// cleanup reaps it.
func sidecarLeftovers(t *testing.T, target string) []string {
	t.Helper()
	matches, err := filepath.Glob(filepath.Join(filepath.Dir(target), ".hey*"))
	mustNoError(t, err)
	kept := matches[:0]
	for _, m := range matches {
		if m != upgradeLockPath(target) {
			kept = append(kept, m)
		}
	}
	return kept
}

// assertTargetUntouched verifies the installed binary and its directory
// survived a failed upgrade unchanged, and no temp dirs leaked.
func assertTargetUntouched(t *testing.T, f *nativeFlowFixture) {
	t.Helper()
	assertFileContent(t, f.target, f.oldContent)

	if leftovers := sidecarLeftovers(t, f.target); len(leftovers) > 0 {
		t.Errorf("sidecars left next to the binary: %v", leftovers)
	}
	if tmp := f.upgradeTempLeftovers(t); len(tmp) > 0 {
		t.Errorf("temp dirs leaked: %v", tmp)
	}
}

// serveBytes spins up a one-asset server and returns its URL.
func serveBytes(t *testing.T, data []byte) string {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write(data)
	}))
	t.Cleanup(srv.Close)
	return srv.URL
}

// --- native flow tests ---

func TestNativeSelfUpdateSuccess(t *testing.T) {
	f := setupNativeFlow(t, "1.0.0", "1.1.0")

	// Distinctive mode to prove preservation across the swap.
	mustNoError(t, os.Chmod(f.target, 0o700))

	run := executeUpgradeCommand(t)
	mustNoError(t, run.err)

	if run.stderr != "" {
		t.Errorf("machine mode must not narrate on stderr, got %q", run.stderr)
	}
	data := run.data(t)
	if data["status"] != "upgraded" || data["method"] != "native" || data["from"] != "1.0.0" || data["to"] != "1.1.0" {
		t.Errorf("unexpected envelope data: %v", data)
	}

	assertFileContent(t, f.target, f.newContent)

	info, err := os.Stat(f.target)
	mustNoError(t, err)
	if info.Mode().Perm() != 0o700 {
		t.Errorf("mode = %o, want 700 preserved from the old binary", info.Mode().Perm())
	}

	// No staging or backup sidecars remain next to the binary.
	if leftovers := sidecarLeftovers(t, f.target); len(leftovers) > 0 {
		t.Errorf("sidecars left after success: %v", leftovers)
	}
	if tmp := f.upgradeTempLeftovers(t); len(tmp) > 0 {
		t.Errorf("temp dirs leaked: %v", tmp)
	}
}

func TestNativeSelfUpdateChecksumMismatch(t *testing.T) {
	f := setupNativeFlow(t, "1.0.0", "1.1.0")

	// Corrupt the served checksums: right shape, wrong hash.
	archiveName := f.release.Assets[0].Name
	bad := strings.Repeat("ab", 32) + "  " + archiveName + "\n"
	f.release.Assets[1].DownloadURL = serveBytes(t, []byte(bad))

	run := executeUpgradeCommand(t)
	apiErr := requireUpgradeError(t, run.err, "upgrade_failed")
	assertContains(t, apiErr.Message, "checksum mismatch")
	assertTargetUntouched(t, f)
}

func TestNativeSelfUpdateChecksumEntryMissing(t *testing.T) {
	f := setupNativeFlow(t, "1.0.0", "1.1.0")

	f.release.Assets[1].DownloadURL = serveBytes(t, []byte("deadbeef  some_other_file.tar.gz\n"))

	run := executeUpgradeCommand(t)
	apiErr := requireUpgradeError(t, run.err, "upgrade_failed")
	assertContains(t, apiErr.Message, "no entry")
	assertTargetUntouched(t, f)
}

func TestNativeSelfUpdateMissingBundleAsset(t *testing.T) {
	f := setupNativeFlow(t, "1.0.0", "1.1.0")

	f.release.Assets = f.release.Assets[:2] // drop checksums.txt.bundle

	run := executeUpgradeCommand(t)
	apiErr := requireUpgradeError(t, run.err, "upgrade_failed")
	assertContains(t, apiErr.Message, "checksums.txt.bundle")
	assertTargetUntouched(t, f)
}

func TestNativeSelfUpdateBundleVerificationFailure(t *testing.T) {
	f := setupNativeFlow(t, "1.0.0", "1.1.0")

	stubBundleVerifier(t, func(_, _ []byte, _ string) error {
		return errors.New("no matching certificate identity")
	})

	run := executeUpgradeCommand(t)
	apiErr := requireUpgradeError(t, run.err, "upgrade_failed")
	assertContains(t, apiErr.Message, "signature verification failed")
	assertTargetUntouched(t, f)
}

// The verifier is handed the release version, which it pins into the
// certificate identity — a bundle from any other tag must not verify.
func TestNativeSelfUpdatePassesReleaseVersionToVerifier(t *testing.T) {
	f := setupNativeFlow(t, "1.0.0", "1.1.0")

	var gotVersion string
	stubBundleVerifier(t, func(_, _ []byte, ver string) error {
		gotVersion = ver
		return nil
	})

	run := executeUpgradeCommand(t)
	mustNoError(t, run.err)
	if gotVersion != f.latest {
		t.Errorf("verifier got version %q, want %q", gotVersion, f.latest)
	}
}

// The full native flow, pinned to a specific release: the tag lookup feeds
// the same download → verify → swap pipeline.
func TestNativeSelfUpdatePinnedVersionSuccess(t *testing.T) {
	f := setupNativeFlow(t, "1.0.0", "1.1.0")
	stubReleaseFetcher(t, func(context.Context) (releaseInfo, error) {
		t.Error("a pinned upgrade must not consult /releases/latest")
		return releaseInfo{}, nil
	})
	stubReleaseByTagFetcher(t, func(_ context.Context, ver string) (releaseInfo, error) {
		if ver != "1.1.0" {
			t.Errorf("fetched tag for %q, want 1.1.0", ver)
		}
		return f.release, nil
	})

	run := executeUpgradeCommandAs(t, "--json", "1.1.0")
	mustNoError(t, run.err)
	data := run.data(t)
	if data["status"] != "upgraded" || data["to"] != "1.1.0" {
		t.Errorf("unexpected envelope data: %v", data)
	}
	assertFileContent(t, f.target, f.newContent)
}

func TestNativeSelfUpdateNoPlatformAsset(t *testing.T) {
	f := setupNativeFlow(t, "1.0.0", "1.1.0")

	f.release.Assets = f.release.Assets[1:] // drop the platform archive

	run := executeUpgradeCommand(t)
	apiErr := requireUpgradeError(t, run.err, "upgrade_required")
	assertContains(t, apiErr.Message, "no prebuilt binary")
	assertContains(t, apiErr.Hint, "releases/tag/v1.1.0")
	if run.stderr != "" {
		t.Errorf("machine mode must not narrate on stderr, got %q", run.stderr)
	}
	assertTargetUntouched(t, f)
}

func TestNativeSelfUpdateOversizedArchiveIsRefused(t *testing.T) {
	f := setupNativeFlow(t, "1.0.0", "1.1.0")

	f.release.Assets[0].DownloadURL = serveBytes(t, bytes.Repeat([]byte("A"), maxArchiveBytes+1))

	run := executeUpgradeCommand(t)
	apiErr := requireUpgradeError(t, run.err, "upgrade_failed")
	assertContains(t, apiErr.Message, "exceeds")
	assertTargetUntouched(t, f)
}

func TestNativeSelfUpdatePostVerifyMismatchRestoresOldBinary(t *testing.T) {
	f := setupNativeFlow(t, "1.0.0", "1.1.0")

	// The staged pre-install probe passes; the installed binary then reports
	// the old version — the swap must be rolled back.
	stubBinaryVersionProber(t, func(_ context.Context, path string) (string, error) {
		if strings.Contains(filepath.Base(path), upgradeStagePrefix) {
			return f.latest, nil
		}
		return "1.0.0", nil
	})

	run := executeUpgradeCommand(t)
	apiErr := requireUpgradeError(t, run.err, "upgrade_failed")
	assertContains(t, apiErr.Message, "was restored")
	assertFileContent(t, f.target, f.oldContent)
}

func TestNativeSelfUpdatePreProbeFailureLeavesTarget(t *testing.T) {
	f := setupNativeFlow(t, "1.0.0", "1.1.0")

	stubBinaryVersionProber(t, func(_ context.Context, path string) (string, error) {
		if strings.Contains(filepath.Base(path), upgradeStagePrefix) {
			return "", errors.New("exec format error")
		}
		return f.latest, nil
	})

	run := executeUpgradeCommand(t)
	apiErr := requireUpgradeError(t, run.err, "upgrade_failed")
	assertContains(t, apiErr.Message, "pre-install check")
	assertTargetUntouched(t, f)
}

// A staged binary that reports a "v"-prefixed version is the same release;
// one that reports a different release is not.
func TestNativeSelfUpdateComparesProbedVersionSemantically(t *testing.T) {
	f := setupNativeFlow(t, "1.0.0", "1.1.0")
	stubBinaryVersionProber(t, func(context.Context, string) (string, error) { return "v1.1.0", nil })

	run := executeUpgradeCommand(t)
	mustNoError(t, run.err)
	assertFileContent(t, f.target, f.newContent)

	f = setupNativeFlow(t, "1.0.5", "1.2.0")
	stubBinaryVersionProber(t, func(_ context.Context, path string) (string, error) {
		if strings.Contains(filepath.Base(path), upgradeStagePrefix) {
			return "1.2.1", nil
		}
		return f.latest, nil
	})

	run = executeUpgradeCommand(t)
	apiErr := requireUpgradeError(t, run.err, "upgrade_failed")
	assertContains(t, apiErr.Message, "reports version 1.2.1, expected 1.2.0")
	assertTargetUntouched(t, f)
}

func TestUpgradePolicyFailureSkipsDownload(t *testing.T) {
	f := setupNativeFlow(t, "1.0.0", "1.1.0")

	stubEuid(t, 0) // policy fails before any download

	run := executeUpgradeCommand(t)
	apiErr := requireUpgradeError(t, run.err, "upgrade_required")
	assertContains(t, apiErr.Message, "running_as_root")
	if f.hits.Load() != 0 {
		t.Errorf("policy failure must precede downloads, server saw %d hits", f.hits.Load())
	}
}

// A second upgrade must refuse before any asset download or filesystem
// mutation while another upgrade holds the lock.
func TestUpgradeRefusesWhenLockHeld(t *testing.T) {
	f := setupNativeFlow(t, "1.0.0", "1.1.0")

	lock := flock.New(upgradeLockPath(f.target))
	locked, err := lock.TryLock()
	mustNoError(t, err)
	if !locked {
		t.Fatal("test could not take the upgrade lock")
	}
	t.Cleanup(func() { _ = lock.Unlock() })

	run := executeUpgradeCommand(t)
	apiErr := requireUpgradeError(t, run.err, "upgrade_failed")
	assertContains(t, apiErr.Message, "another hey upgrade")

	if f.hits.Load() != 0 {
		t.Error("no downloads may start while the lock is held")
	}
	assertFileContent(t, f.target, f.oldContent)
}

// When rollback fails, the backup is the user's only good binary. It must be
// moved out of the sidecar-reap namespace (`.recovered`), referenced by the
// failure hint, and survive a subsequent startup cleanup.
func TestPostProbeRestoreFailurePreservesRecoveryArtifact(t *testing.T) {
	f := setupNativeFlow(t, "1.0.0", "1.1.0")

	stubBinaryVersionProber(t, func(_ context.Context, path string) (string, error) {
		if strings.Contains(filepath.Base(path), upgradeStagePrefix) {
			return f.latest, nil
		}
		return "1.0.0", nil
	})
	// The rollback rename (backup → target) fails; every other rename is real.
	stubRenameFile(t, func(oldpath, newpath string) error {
		if strings.Contains(oldpath, ".old-") && newpath == f.target {
			return errors.New("permission denied")
		}
		return os.Rename(oldpath, newpath)
	})

	run := executeUpgradeCommand(t)
	apiErr := requireUpgradeError(t, run.err, "upgrade_failed")

	matches, err := filepath.Glob(f.target + ".recovered-*")
	mustNoError(t, err)
	if len(matches) != 1 {
		t.Fatalf("want exactly one recovery artifact, got %v", matches)
	}
	recovered := matches[0]
	assertContains(t, apiErr.Hint, recovered)
	assertFileContent(t, recovered, f.oldContent)

	cleanupUpgradeSidecarsFor(f.target)
	assertExists(t, recovered, "recovery artifact must survive startup sidecar cleanup")
}

// Worst case within the worst case: the rollback rename fails AND the
// preservation rename fails, leaving the backup at its `.old-*` name. The
// hint must reference that surviving path, and cleanup must leave it alone.
func TestPostProbeRestoreAndPreserveFailureKeepsOldBackupSafe(t *testing.T) {
	f := setupNativeFlow(t, "1.0.0", "1.1.0")

	stubBinaryVersionProber(t, func(_ context.Context, path string) (string, error) {
		if strings.Contains(filepath.Base(path), upgradeStagePrefix) {
			return f.latest, nil
		}
		return "1.0.0", nil
	})
	// Every rename OF the backup fails: rollback (backup → target) and
	// preservation (backup → .recovered-*) alike.
	stubRenameFile(t, func(oldpath, newpath string) error {
		if strings.Contains(oldpath, ".old-") {
			return errors.New("permission denied")
		}
		return os.Rename(oldpath, newpath)
	})

	run := executeUpgradeCommand(t)
	apiErr := requireUpgradeError(t, run.err, "upgrade_failed")

	backups, err := filepath.Glob(f.target + ".old-*")
	mustNoError(t, err)
	if len(backups) != 1 {
		t.Fatalf("want exactly one .old-* backup, got %v", backups)
	}
	assertContains(t, apiErr.Hint, backups[0])

	cleanupUpgradeSidecarsFor(f.target)
	assertFileContent(t, backups[0], f.oldContent)
}

// --- real-bundle authenticity (hermetic: vendored fixtures, no network) ---

// The fixtures are basecamp-cli's v0.8.1 release artifacts (see
// testdata/selfupdate/README.md), so they verify under that repo's identity.
const fixtureIdentity = "https://github.com/basecamp/basecamp-cli/.github/workflows/release.yml@refs/tags/v0.8.1"

func loadSelfUpdateFixtures(t *testing.T) (trusted root.TrustedMaterial, checksums, bundleBytes []byte) {
	t.Helper()
	trustedRoot, err := root.NewTrustedRootFromPath(filepath.Join("testdata", "selfupdate", "trusted_root.json"))
	mustNoError(t, err)
	checksums = readFile(t, filepath.Join("testdata", "selfupdate", "checksums.txt"))
	bundleBytes = readFile(t, filepath.Join("testdata", "selfupdate", "checksums.txt.bundle"))
	return trustedRoot, checksums, bundleBytes
}

func TestVerifyBundleRealReleaseFixtures(t *testing.T) {
	trusted, checksums, bundleBytes := loadSelfUpdateFixtures(t)
	mustNoError(t, verifyBundleWithRoot(trusted, checksums, bundleBytes, fixtureIdentity))
}

func TestVerifyBundleRejectsWrongIdentity(t *testing.T) {
	trusted, checksums, bundleBytes := loadSelfUpdateFixtures(t)

	// hey-cli's own identity for the same version number: a bundle signed by
	// another repository's workflow must not verify.
	if err := verifyBundleWithRoot(trusted, checksums, bundleBytes, releaseWorkflowIdentity("0.8.1")); err == nil {
		t.Fatal("bundle from another repository verified under hey-cli's identity")
	}
	// Same repository, different tag.
	otherTag := strings.Replace(fixtureIdentity, "v0.8.1", "v9.9.9", 1)
	if err := verifyBundleWithRoot(trusted, checksums, bundleBytes, otherTag); err == nil {
		t.Fatal("bundle verified under a different release tag")
	}
}

func TestVerifyBundleRejectsTamperedArtifact(t *testing.T) {
	trusted, checksums, bundleBytes := loadSelfUpdateFixtures(t)

	tampered := append([]byte("tampered\n"), checksums...)
	if err := verifyBundleWithRoot(trusted, tampered, bundleBytes, fixtureIdentity); err == nil {
		t.Fatal("tampered artifact verified")
	}
}

func TestVerifyBundleRejectsGarbageBundle(t *testing.T) {
	trusted, checksums, _ := loadSelfUpdateFixtures(t)

	if err := verifyBundleWithRoot(trusted, checksums, []byte("not a bundle"), fixtureIdentity); err == nil {
		t.Fatal("garbage bundle verified")
	}
}

func TestReleaseWorkflowIdentityPinsRepoAndTag(t *testing.T) {
	want := "https://github.com/basecamp/hey-cli/.github/workflows/release.yml@refs/tags/v1.2.3"
	if got := releaseWorkflowIdentity("1.2.3"); got != want {
		t.Errorf("releaseWorkflowIdentity = %q, want %q", got, want)
	}
}

// --- path policy ---

func TestSelfUpdateIneligibleAsRoot(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("root check is unix-only")
	}
	stubEuid(t, 0)
	reason, hint := selfUpdateIneligibility("/home/alice/bin/hey")
	if reason != "running_as_root" || hint == "" {
		t.Errorf("got (%q, %q), want running_as_root with a hint", reason, hint)
	}
}

// The Nix store is outside $HOME like any system path; the hint is what
// differs, because "use your package manager" would mislead there.
func TestSelfUpdateIneligibleNixStoreIsSystemPathWithNixHint(t *testing.T) {
	stubEuid(t, 1000)
	stubHomeDir(t, t.TempDir())
	reason, hint := selfUpdateIneligibility("/nix/store/abc123-hey-cli/bin/hey")
	if reason != "system_path" {
		t.Errorf("reason = %q, want system_path", reason)
	}
	assertContains(t, hint, "nix profile upgrade hey-cli")
}

func TestSelfUpdateIneligibleSystemPathHintNamesPackageManagers(t *testing.T) {
	stubEuid(t, 1000)
	stubHomeDir(t, t.TempDir())
	reason, hint := selfUpdateIneligibility("/usr/bin/hey")
	if reason != "system_path" {
		t.Errorf("reason = %q, want system_path", reason)
	}
	for _, want := range []string{"yay -S hey-cli", "apt", "dnf", "apk", "nix profile upgrade hey-cli"} {
		assertContains(t, hint, want)
	}
}

func TestSelfUpdateIneligibleSiblingPrefixHomeEscape(t *testing.T) {
	stubEuid(t, 1000)
	base := t.TempDir()
	home := filepath.Join(base, "jeremy")
	sibling := filepath.Join(base, "jeremyx", "bin")
	mustMkdirAll(t, home)
	mustMkdirAll(t, sibling)
	target := filepath.Join(sibling, "hey")
	mustWriteFile(t, target, []byte("x"), 0o755)
	stubHomeDir(t, home)

	reason, _ := selfUpdateIneligibility(target)
	if reason != "system_path" {
		t.Errorf("reason = %q, want system_path", reason)
	}
}

func TestSelfUpdateIneligibleSymlinkEscape(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("symlink creation not reliable on windows CI")
	}
	stubEuid(t, 1000)
	base := t.TempDir()
	home := filepath.Join(base, "home")
	outside := filepath.Join(base, "outside")
	mustMkdirAll(t, home)
	mustMkdirAll(t, outside)
	// ~/bin is a symlink pointing outside the home directory.
	mustNoError(t, os.Symlink(outside, filepath.Join(home, "bin")))
	target := filepath.Join(home, "bin", "hey")
	mustWriteFile(t, target, []byte("x"), 0o755)
	stubHomeDir(t, home)

	reason, _ := selfUpdateIneligibility(target)
	if reason != "system_path" {
		t.Errorf("reason = %q, want system_path", reason)
	}
}

func TestSelfUpdateIneligibleUnwritableDir(t *testing.T) {
	if runtime.GOOS == "windows" || os.Geteuid() == 0 {
		t.Skip("permission bits not enforceable here")
	}
	stubEuid(t, 1000)
	home := t.TempDir()
	binDir := filepath.Join(home, "bin")
	mustMkdirAll(t, binDir)
	target := filepath.Join(binDir, "hey")
	mustWriteFile(t, target, []byte("x"), 0o755)
	mustNoError(t, os.Chmod(binDir, 0o555))
	t.Cleanup(func() { _ = os.Chmod(binDir, 0o755) })
	stubHomeDir(t, home)

	reason, _ := selfUpdateIneligibility(target)
	if reason != "not_writable" {
		t.Errorf("reason = %q, want not_writable", reason)
	}
}

func TestSelfUpdateEligibleUnderHome(t *testing.T) {
	stubEuid(t, 1000)
	home := t.TempDir()
	binDir := filepath.Join(home, ".local", "bin")
	mustMkdirAll(t, binDir)
	target := filepath.Join(binDir, "hey")
	mustWriteFile(t, target, []byte("x"), 0o755)
	stubHomeDir(t, home)

	reason, hint := selfUpdateIneligibility(target)
	if reason != "" || hint != "" {
		t.Errorf("got (%q, %q), want eligible", reason, hint)
	}
}

// --- swap contract ---

func TestReplaceExecutableUnixPreservesInodeViaHardLink(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("unix swap semantics")
	}
	dir := t.TempDir()
	target := filepath.Join(dir, "hey")
	staged := filepath.Join(dir, ".hey-upgrade-test")
	mustWriteFile(t, target, []byte("old"), 0o755)
	mustWriteFile(t, staged, []byte("new"), 0o755)

	backup, err := replaceExecutable(runtime.GOOS, target, staged)
	mustNoError(t, err)

	assertFileContent(t, target, []byte("new"))
	assertFileContent(t, backup, []byte("old"))

	// Rollback restores the preserved inode via a single rename-over.
	mustNoError(t, restoreBackup(runtime.GOOS, target, backup))
	assertFileContent(t, target, []byte("old"))
}

func TestReplaceExecutableUnixHardLinkFallbackCopies(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("unix swap semantics")
	}
	stubLinkFile(t, func(_, _ string) error { return errors.New("EPERM: links unsupported") })

	dir := t.TempDir()
	target := filepath.Join(dir, "hey")
	staged := filepath.Join(dir, ".hey-upgrade-test")
	mustWriteFile(t, target, []byte("old"), 0o755)
	mustWriteFile(t, staged, []byte("new"), 0o755)

	backup, err := replaceExecutable(runtime.GOOS, target, staged)
	mustNoError(t, err)

	assertFileContent(t, backup, []byte("old"))
	assertFileContent(t, target, []byte("new"))
}

func TestReplaceExecutableUnixBackupFailureLeavesTarget(t *testing.T) {
	if runtime.GOOS == "windows" || os.Geteuid() == 0 {
		t.Skip("permission bits not enforceable here")
	}
	stubLinkFile(t, func(_, _ string) error { return errors.New("EPERM") })

	dir := t.TempDir()
	target := filepath.Join(dir, "hey")
	staged := filepath.Join(dir, ".hey-upgrade-test")
	mustWriteFile(t, target, []byte("old"), 0o755)
	mustWriteFile(t, staged, []byte("new"), 0o755)
	mustNoError(t, os.Chmod(dir, 0o555)) // backup copy creation fails
	t.Cleanup(func() { _ = os.Chmod(dir, 0o755) })

	_, err := replaceExecutable(runtime.GOOS, target, staged)
	assertErrorContains(t, err, "preserve current binary")
	assertFileContent(t, target, []byte("old"))
}

func TestReplaceExecutableUnixRenameFailureKeepsOldBinary(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("unix swap semantics")
	}
	dir := t.TempDir()
	target := filepath.Join(dir, "hey")
	staged := filepath.Join(dir, ".hey-upgrade-missing") // never created
	mustWriteFile(t, target, []byte("old"), 0o755)

	_, err := replaceExecutable(runtime.GOOS, target, staged)
	assertErrorContains(t, err, "install new binary")
	assertFileContent(t, target, []byte("old"))

	// The failed attempt must not strand its backup sidecar.
	leftovers, _ := filepath.Glob(target + ".old-*")
	if len(leftovers) > 0 {
		t.Errorf("stranded backups: %v", leftovers)
	}
}

func TestReplaceExecutableWindowsShuffle(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "hey.exe")
	staged := filepath.Join(dir, ".hey-upgrade-test.exe")
	mustWriteFile(t, target, []byte("old"), 0o755)
	mustWriteFile(t, staged, []byte("new"), 0o755)

	backup, err := replaceExecutable("windows", target, staged)
	mustNoError(t, err)
	if backup != target+".old" {
		t.Errorf("backup = %q, want %q", backup, target+".old")
	}

	assertFileContent(t, target, []byte("new"))
	assertFileContent(t, backup, []byte("old"))

	// Post-probe restore: new moved aside, .old back in place.
	mustNoError(t, restoreBackup("windows", target, backup))
	assertFileContent(t, target, []byte("old"))
}

// Both the install rename AND the rollback rename fail — the worst case,
// where the target path is left with no executable. The error must be the
// distinct catastrophe carrying the backup path, never the ordinary
// "install failed, original still in place" shape.
func TestReplaceExecutableWindowsDoubleRenameFailureIsCatastrophic(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "hey.exe")
	staged := filepath.Join(dir, ".hey-upgrade-test.exe")
	mustWriteFile(t, target, []byte("old"), 0o755)
	mustWriteFile(t, staged, []byte("new"), 0o755)

	// First rename (running exe → .old) succeeds; every later rename fails.
	calls := 0
	stubRenameFile(t, func(oldpath, newpath string) error {
		calls++
		if calls == 1 {
			return os.Rename(oldpath, newpath)
		}
		return errors.New("access denied")
	})

	_, err := replaceExecutable("windows", target, staged)
	var cat *swapCatastropheError
	if !errors.As(err, &cat) {
		t.Fatalf("want swapCatastropheError, got %v", err)
	}
	if cat.backup != target+".old" {
		t.Errorf("backup = %q, want %q", cat.backup, target+".old")
	}

	// The reported condition is real: the target path has no executable.
	assertNotExists(t, target, "target should be missing after the double failure")

	// And the caller-facing mapping surfaces the backup path and never claims
	// the previous binary is still installed.
	apiErr := swapFailureError(target, err)
	if apiErr.Code != "upgrade_failed" {
		t.Errorf("code = %q, want upgrade_failed", apiErr.Code)
	}
	assertContains(t, apiErr.Message, "may now be missing")
	assertContains(t, apiErr.Hint, cat.backup)
	assertNotContains(t, apiErr.Hint, "left in place")
}

func TestSwapFailureErrorOrdinaryCaseKeepsExistingInstallClaim(t *testing.T) {
	apiErr := swapFailureError("/home/alice/bin/hey", errors.New("install new binary: disk full"))
	if apiErr.Code != "upgrade_failed" {
		t.Errorf("code = %q, want upgrade_failed", apiErr.Code)
	}
	assertContains(t, apiErr.Hint, "left in place")
}

func TestReplaceExecutableWindowsSecondRenameFailureRollsBack(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "hey.exe")
	staged := filepath.Join(dir, ".hey-upgrade-missing.exe") // never created
	mustWriteFile(t, target, []byte("old"), 0o755)

	_, err := replaceExecutable("windows", target, staged)
	if err == nil {
		t.Fatal("expected the install rename to fail")
	}

	// First rename is rolled back: target holds the old binary, no .old left.
	assertFileContent(t, target, []byte("old"))
	assertNotExists(t, target+".old", "rollback must remove the .old sidecar")
}

// --- sidecar cleanup ---

// After a catastrophic swap the `.old` sidecar may be the only binary left —
// cleanup must not reap anything when the executable itself is missing.
func TestCleanupSkipsWhenExecutableMissing(t *testing.T) {
	dir := t.TempDir()
	exe := filepath.Join(dir, "hey")
	backup := exe + ".old"
	mustWriteFile(t, backup, []byte("only-copy"), 0o755)

	cleanupUpgradeSidecarsFor(exe)
	assertExists(t, backup, "only remaining binary must survive cleanup")
}

// Startup sidecar cleanup must skip entirely while an upgrade is in flight —
// the glob patterns match that upgrade's live staging and backup files.
func TestCleanupSkipsWhileUpgradeLockHeld(t *testing.T) {
	dir := t.TempDir()
	exe := filepath.Join(dir, "hey")
	mustWriteFile(t, exe, []byte("bin"), 0o755)
	sidecars := []string{
		filepath.Join(dir, ".hey-upgrade-live"),
		exe + ".old-aaaa",
	}
	for _, s := range sidecars {
		mustWriteFile(t, s, []byte("live"), 0o644)
	}

	lock := flock.New(upgradeLockPath(exe))
	locked, err := lock.TryLock()
	mustNoError(t, err)
	if !locked {
		t.Fatal("test could not take the upgrade lock")
	}

	cleanupUpgradeSidecarsFor(exe)
	for _, s := range sidecars {
		assertExists(t, s, "must survive cleanup while the upgrade lock is held")
	}

	mustNoError(t, lock.Unlock())
	cleanupUpgradeSidecarsFor(exe)
	assertNotExists(t, sidecars[0], "staging file is reaped after the lock is released")
	assertExists(t, sidecars[1], ".old-* backups are never reaped by cleanup")
	assertNotExists(t, upgradeLockPath(exe), "lock file itself is reaped once no upgrade holds it")
}

// The lock is directory-wide because the staging namespace is: every hey
// binary in a directory stages into the same `.hey-upgrade-*` glob whatever
// its own filename, so a sibling install (hey next to hey-preview) running
// cleanup while the other upgrades must be locked out too.
func TestCleanupSkipsWhileSiblingUpgradeLockHeld(t *testing.T) {
	dir := t.TempDir()
	upgrading := filepath.Join(dir, "hey-preview")
	bystander := filepath.Join(dir, "hey")
	mustWriteFile(t, upgrading, []byte("bin"), 0o755)
	mustWriteFile(t, bystander, []byte("bin"), 0o755)
	staged := filepath.Join(dir, ".hey-upgrade-live")
	mustWriteFile(t, staged, []byte("staged"), 0o644)

	lock := flock.New(upgradeLockPath(upgrading))
	locked, err := lock.TryLock()
	mustNoError(t, err)
	if !locked {
		t.Fatal("test could not take the upgrade lock")
	}

	cleanupUpgradeSidecarsFor(bystander)
	assertExists(t, staged, "sibling cleanup must not reap another upgrade's live staging file")

	mustNoError(t, lock.Unlock())
	cleanupUpgradeSidecarsFor(bystander)
	assertNotExists(t, staged, "staging file is reaped once no upgrade holds the directory lock")
}

func TestCleanupUpgradeSidecars(t *testing.T) {
	dir := t.TempDir()
	exe := filepath.Join(dir, "hey")
	mustWriteFile(t, exe, []byte("bin"), 0o755)

	// Only the staging namespace is reaped — a positive safe-to-reap
	// contract (discardBackup moves verified-garbage backups into it).
	reaped := []string{
		filepath.Join(dir, ".hey-upgrade-abc123"),
		filepath.Join(dir, ".hey-upgrade-failed-def.exe"),
		filepath.Join(dir, ".hey-upgrade-probe-42"),
		filepath.Join(dir, ".hey-upgrade-reap-99"),
	}
	// `.old*` and `.recovered-*` can be the only good binary after a failed
	// rollback — never reaped, along with unrelated bystanders.
	kept := []string{
		exe + ".old",
		exe + ".old-1a2b3c4d",
		exe + ".recovered-cafe",
		filepath.Join(dir, "hey.bak"),
	}
	for _, s := range append(append([]string{}, reaped...), kept...) {
		mustWriteFile(t, s, []byte("leftover"), 0o644)
	}

	cleanupUpgradeSidecarsFor(exe)

	for _, s := range reaped {
		assertNotExists(t, s, "expected to be reaped")
	}
	for _, s := range append(kept, exe) {
		assertExists(t, s, "expected to survive")
	}
}

// A verified upgrade discards its backup; when deletion is impossible (the
// Windows-locked old exe), the backup is renamed into the staging namespace
// so cleanup reaps it later — the positive safe-to-reap marker.
func TestDiscardBackupFallsBackToReapNamespace(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "hey")
	backup := target + ".old"
	mustWriteFile(t, target, []byte("new"), 0o755)
	mustWriteFile(t, backup, []byte("old"), 0o755)

	orig := removeFile
	removeFile = func(string) error { return errors.New("locked") }
	t.Cleanup(func() { removeFile = orig })

	discardBackup(target, backup)

	assertNotExists(t, backup, "backup must be moved out of the .old name")
	marked, err := filepath.Glob(filepath.Join(dir, ".hey-upgrade-reap-*"))
	mustNoError(t, err)
	if len(marked) != 1 {
		t.Fatalf("want one reap-marked file, got %v", marked)
	}

	cleanupUpgradeSidecarsFor(target)
	assertNotExists(t, marked[0], "reap-marked backup is cleaned up")
}

// Recovery artifacts get unique names: a pre-existing artifact from an
// earlier failure is never clobbered, and repeated failures all survive.
func TestPreserveRecoveryArtifactIsUniqueAndNonClobbering(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "hey")
	preexisting := target + ".recovered-aaaa"
	mustWriteFile(t, preexisting, []byte("earlier-recovery"), 0o755)

	backup1 := target + ".old-1111"
	backup2 := target + ".old-2222"
	mustWriteFile(t, backup1, []byte("first"), 0o755)
	mustWriteFile(t, backup2, []byte("second"), 0o755)

	kept1 := preserveRecoveryArtifact(target, backup1)
	kept2 := preserveRecoveryArtifact(target, backup2)

	if kept1 == kept2 || kept1 == preexisting || kept2 == preexisting {
		t.Errorf("recovery names collide: %q %q %q", preexisting, kept1, kept2)
	}
	assertFileContent(t, preexisting, []byte("earlier-recovery"))
	assertFileContent(t, kept1, []byte("first"))
	assertFileContent(t, kept2, []byte("second"))
}

// --- parseChecksums / extraction ---

func TestParseChecksums(t *testing.T) {
	data := "abc123  hey_1.0.0_linux_amd64.tar.gz\r\n" +
		"DEF456 *hey_1.0.0_windows_amd64.zip\r\n" +
		"malformed line with too many fields here\n" +
		"loner\n" +
		"\n" +
		"789abc  hey_1.0.0_darwin_arm64.tar.gz"

	sums := parseChecksums([]byte(data))
	want := map[string]string{
		"hey_1.0.0_linux_amd64.tar.gz":  "abc123",
		"hey_1.0.0_windows_amd64.zip":   "def456", // star prefix stripped, hash lowercased
		"hey_1.0.0_darwin_arm64.tar.gz": "789abc", // no trailing newline
	}
	if len(sums) != len(want) {
		t.Errorf("parsed %d entries, want %d: %v", len(sums), len(want), sums)
	}
	for name, hash := range want {
		if sums[name] != hash {
			t.Errorf("sums[%q] = %q, want %q", name, sums[name], hash)
		}
	}
}

func TestExtractTarGzMember(t *testing.T) {
	archive := writeArchiveFile(t, buildTarGz(t, []tarEntry{
		{name: "README.md", body: []byte("docs")},
		{name: "completions", typeflag: tar.TypeDir},
		{name: "hey", body: []byte("the-binary")},
	}))
	dest := filepath.Join(t.TempDir(), "out")

	mustNoError(t, extractTarGzMember(archive, "hey", dest))
	assertFileContent(t, dest, []byte("the-binary"))
}

// Some tar producers prefix root entries with "./" — that is still literally
// a root entry and must extract.
func TestExtractTarGzToleratesDotSlashRootMember(t *testing.T) {
	archive := writeArchiveFile(t, buildTarGz(t, []tarEntry{
		{name: "./hey", body: []byte("the-binary")},
	}))
	dest := filepath.Join(t.TempDir(), "out")

	mustNoError(t, extractTarGzMember(archive, "hey", dest))
	assertFileContent(t, dest, []byte("the-binary"))
}

func TestExtractTarGzRejectsBadArchives(t *testing.T) {
	tests := []struct {
		name    string
		entries []tarEntry
		wantErr string
	}{
		{
			name:    "nested only",
			entries: []tarEntry{{name: "subdir/hey", body: []byte("nested")}},
			wantErr: "does not contain",
		},
		{
			name:    "missing member",
			entries: []tarEntry{{name: "README.md", body: []byte("docs")}},
			wantErr: "does not contain",
		},
		{
			name:    "path traversal spelling",
			entries: []tarEntry{{name: "../hey", body: []byte("escape")}},
			wantErr: "does not contain",
		},
		{
			name:    "dot-segment alias of the root member",
			entries: []tarEntry{{name: "nested/../hey", body: []byte("aliased")}},
			wantErr: "does not contain",
		},
		{
			name:    "duplicate members",
			entries: []tarEntry{{name: "hey", body: []byte("one")}, {name: "hey", body: []byte("two")}},
			wantErr: "duplicate",
		},
		{
			name: "symlink entry",
			entries: []tarEntry{
				{name: "evil", typeflag: tar.TypeSymlink, linkname: "/etc/passwd"},
				{name: "hey", body: []byte("bin")},
			},
			wantErr: "link entry",
		},
		{
			name:    "hard link entry",
			entries: []tarEntry{{name: "hey", typeflag: tar.TypeLink, linkname: "target"}},
			wantErr: "link entry",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			archive := writeArchiveFile(t, buildTarGz(t, tt.entries))
			dest := filepath.Join(t.TempDir(), "out")
			err := extractTarGzMember(archive, "hey", dest)
			assertErrorContains(t, err, tt.wantErr)
		})
	}
}

func TestExtractTarGzRejectsOversizedMember(t *testing.T) {
	origMax := maxBinaryBytes
	maxBinaryBytes = 16
	t.Cleanup(func() { maxBinaryBytes = origMax })

	archive := writeArchiveFile(t, buildTarGz(t, []tarEntry{
		{name: "hey", body: bytes.Repeat([]byte("A"), 64)},
	}))
	err := extractTarGzMember(archive, "hey", filepath.Join(t.TempDir(), "out"))
	assertErrorContains(t, err, "exceeds")
}

func TestExtractZipMember(t *testing.T) {
	archive := writeArchiveFile(t, buildZip(t, []zipEntry{
		{name: "README.md", body: []byte("docs")},
		{name: "hey.exe", body: []byte("the-binary")},
	}))
	dest := filepath.Join(t.TempDir(), "out.exe")

	mustNoError(t, extractZipMember(archive, "hey.exe", dest))
	assertFileContent(t, dest, []byte("the-binary"))
}

func TestExtractZipRejectsBadArchives(t *testing.T) {
	tests := []struct {
		name    string
		entries []zipEntry
		wantErr string
	}{
		{
			name: "symlink entry",
			entries: []zipEntry{
				{name: "evil", body: []byte("/etc/passwd"), symlink: true},
				{name: "hey.exe", body: []byte("bin")},
			},
			wantErr: "symlink entry",
		},
		{
			name:    "duplicate members",
			entries: []zipEntry{{name: "hey.exe", body: []byte("one")}, {name: "hey.exe", body: []byte("two")}},
			wantErr: "duplicate",
		},
		{
			name:    "nested only",
			entries: []zipEntry{{name: "nested/hey.exe", body: []byte("nested")}},
			wantErr: "does not contain",
		},
		{
			name:    "path traversal spelling",
			entries: []zipEntry{{name: "../hey.exe", body: []byte("escape")}},
			wantErr: "does not contain",
		},
		{
			name:    "dot-segment alias of the root member",
			entries: []zipEntry{{name: "nested/../hey.exe", body: []byte("aliased")}},
			wantErr: "does not contain",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			archive := writeArchiveFile(t, buildZip(t, tt.entries))
			dest := filepath.Join(t.TempDir(), "out.exe")
			err := extractZipMember(archive, "hey.exe", dest)
			assertErrorContains(t, err, tt.wantErr)
		})
	}
}

func TestExtractZipRejectsOversizedMember(t *testing.T) {
	origMax := maxBinaryBytes
	maxBinaryBytes = 16
	t.Cleanup(func() { maxBinaryBytes = origMax })

	archive := writeArchiveFile(t, buildZip(t, []zipEntry{
		{name: "hey.exe", body: bytes.Repeat([]byte("A"), 64)},
	}))
	err := extractZipMember(archive, "hey.exe", filepath.Join(t.TempDir(), "out.exe"))
	assertErrorContains(t, err, "exceeds")
}

// A power loss between the swap and the backup's deletion must not leave a
// truncated binary with nothing to recover from: the staged file is synced
// before it is renamed over the target, and the directory entry is synced
// before the backup goes.
func TestNativeSelfUpdateSyncsBeforeDiscardingBackup(t *testing.T) {
	f := setupNativeFlow(t, "1.0.0", "1.1.0")

	var events []string
	origFile, origDir := fileSyncer, dirSyncer
	fileSyncer = func(file *os.File) error {
		events = append(events, "sync-file "+file.Name())
		return origFile(file)
	}
	dirSyncer = func(dir string) error {
		events = append(events, "sync-dir "+dir)
		return origDir(dir)
	}
	t.Cleanup(func() { fileSyncer, dirSyncer = origFile, origDir })
	stubRenameFile(t, func(oldpath, newpath string) error {
		events = append(events, "rename "+oldpath+" -> "+newpath)
		return os.Rename(oldpath, newpath)
	})
	origRemove := removeFile
	removeFile = func(path string) error {
		events = append(events, "remove "+path)
		return origRemove(path)
	}
	t.Cleanup(func() { removeFile = origRemove })

	run := executeUpgradeCommand(t)
	mustNoError(t, run.err)
	assertFileContent(t, f.target, f.newContent)

	index := func(prefix string) int {
		for i, e := range events {
			if strings.HasPrefix(e, prefix) {
				return i
			}
		}
		t.Fatalf("no %q event in %q", prefix, events)
		return -1
	}
	syncFile := index("sync-file " + filepath.Join(filepath.Dir(f.target), upgradeStagePrefix))
	install := index("rename " + filepath.Join(filepath.Dir(f.target), upgradeStagePrefix))
	syncDir := index("sync-dir " + filepath.Dir(f.target))
	remove := index("remove " + f.target + ".old-")
	if syncFile >= install || install >= syncDir || syncDir >= remove {
		t.Errorf("want staged sync < install rename < dir sync < backup removal, got %q", events)
	}
}
