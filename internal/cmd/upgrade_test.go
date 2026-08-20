package cmd

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"path/filepath"
	"strings"
	"testing"

	"github.com/basecamp/hey-cli/internal/output"
	"github.com/basecamp/hey-cli/internal/version"
)

// --- assertion helpers (stdlib only) ---

func mustNoError(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func assertContains(t *testing.T, s, substr string) {
	t.Helper()
	if !strings.Contains(s, substr) {
		t.Errorf("expected %q to contain %q", s, substr)
	}
}

func assertNotContains(t *testing.T, s, substr string) {
	t.Helper()
	if strings.Contains(s, substr) {
		t.Errorf("expected %q not to contain %q", s, substr)
	}
}

// requireUpgradeError asserts the command failed the success/exit contract
// way: structured error with the expected code and a nonzero exit code.
func requireUpgradeError(t *testing.T, err error, code string) *output.Error {
	t.Helper()
	if err == nil {
		t.Fatalf("expected an %s error, got nil", code)
	}
	apiErr := output.AsError(err)
	if apiErr.Code != code {
		t.Fatalf("error code = %q, want %q (message: %s)", apiErr.Code, code, apiErr.Message)
	}
	if output.ExitCodeFor(err) == 0 {
		t.Fatalf("exit code for %s must be nonzero", code)
	}
	return apiErr
}

// --- seam stubs ---

func stubVersion(t *testing.T, v string) {
	t.Helper()
	orig := version.Version
	version.Version = v
	t.Cleanup(func() { version.Version = orig })
}

func stubExecutablePathResolver(t *testing.T, path string, ok bool) {
	t.Helper()
	orig := executablePathResolver
	executablePathResolver = func() (string, bool) { return path, ok }
	t.Cleanup(func() { executablePathResolver = orig })
}

func stubScoopPrefixResolver(t *testing.T, resolve func(context.Context, string) (string, bool)) {
	t.Helper()
	orig := scoopPrefixResolver
	scoopPrefixResolver = resolve
	t.Cleanup(func() { scoopPrefixResolver = orig })
}

func stubRawExecutablePathResolver(t *testing.T, path string, ok bool) {
	t.Helper()
	orig := rawExecutablePathResolver
	rawExecutablePathResolver = func() (string, bool) { return path, ok }
	t.Cleanup(func() { rawExecutablePathResolver = orig })
}

func stubReleaseFetcher(t *testing.T, fetch func(context.Context) (releaseInfo, error)) {
	t.Helper()
	orig := releaseFetcher
	releaseFetcher = fetch
	t.Cleanup(func() { releaseFetcher = orig })
}

func stubReleaseByTagFetcher(t *testing.T, fetch func(context.Context, string) (releaseInfo, error)) {
	t.Helper()
	orig := releaseByTagFetcher
	releaseByTagFetcher = fetch
	t.Cleanup(func() { releaseByTagFetcher = orig })
}

type upgradeCheckersStub struct {
	latestVersion   string
	release         *releaseInfo // optional richer release (assets); read at call time so tests can mutate
	isBrew          bool
	isScoop         bool
	isGlobalScoop   bool
	homebrewUpgrade func(context.Context, string, io.Writer, io.Writer) error
	scoopUpgrade    func(context.Context, bool, io.Writer, io.Writer) error
}

// stubUpgradeCheckers overrides the release lookup and package manager
// helpers for tests.
func stubUpgradeCheckers(t *testing.T, stub upgradeCheckersStub) {
	t.Helper()

	stubReleaseFetcher(t, func(context.Context) (releaseInfo, error) {
		if stub.release != nil {
			return *stub.release, nil
		}
		return releaseInfo{Version: stub.latestVersion}, nil
	})

	origHC := homebrewChecker
	homebrewChecker = func(context.Context) bool { return stub.isBrew }
	t.Cleanup(func() { homebrewChecker = origHC })

	origHU := homebrewUpgrader
	homebrewUpgrader = stub.homebrewUpgrade
	if homebrewUpgrader == nil {
		homebrewUpgrader = func(context.Context, string, io.Writer, io.Writer) error { return nil }
	}
	t.Cleanup(func() { homebrewUpgrader = origHU })

	// The upgrade binds to the installation that owns the running binary, so
	// give each stubbed install method a plausible executable path. Tests
	// that exercise the binding itself stub their own path afterwards.
	if stub.isBrew {
		stubRawExecutablePathResolver(t, "/opt/homebrew/Caskroom/hey/1.2.3/hey", true)
	}
	if stub.isGlobalScoop {
		stubExecutablePathResolver(t, "c:/programdata/scoop/apps/hey/current/hey.exe", true)
	}

	origSC := scoopChecker
	scoopChecker = func(context.Context) bool { return stub.isScoop }
	t.Cleanup(func() { scoopChecker = origSC })

	origGlobalScoop := scoopGlobalScopeChecker
	scoopGlobalScopeChecker = func(context.Context) bool { return stub.isGlobalScoop }
	t.Cleanup(func() { scoopGlobalScopeChecker = origGlobalScoop })

	origSU := scoopUpgrader
	scoopUpgrader = stub.scoopUpgrade
	if scoopUpgrader == nil {
		scoopUpgrader = func(context.Context, bool, io.Writer, io.Writer) error { return nil }
	}
	t.Cleanup(func() { scoopUpgrader = origSU })
}

// upgradeRun captures one `hey upgrade` invocation through the real root
// command: the JSON envelope lands on stdout, progress narration on stderr.
type upgradeRun struct {
	stdout string
	stderr string
	err    error
}

func (r upgradeRun) data(t *testing.T) map[string]string {
	t.Helper()
	var resp struct {
		OK   bool              `json:"ok"`
		Data map[string]string `json:"data"`
	}
	if err := json.Unmarshal([]byte(r.stdout), &resp); err != nil {
		t.Fatalf("stdout is not a JSON envelope: %v\n%s", err, r.stdout)
	}
	if !resp.OK {
		t.Fatalf("envelope reports ok=false:\n%s", r.stdout)
	}
	return resp.Data
}

func executeUpgradeCommand(t *testing.T) upgradeRun {
	t.Helper()
	return executeUpgradeCommandAs(t, "--json")
}

// executeStyledUpgradeCommand runs `hey upgrade --styled`: narration goes to
// stdout and success must end there, with no JSON envelope.
func executeStyledUpgradeCommand(t *testing.T) upgradeRun {
	t.Helper()
	return executeUpgradeCommandAs(t, "--styled")
}

func executeUpgradeCommandAs(t *testing.T, args ...string) upgradeRun {
	t.Helper()
	isolateCommandEnv(t)

	root := newRootCmd()
	var stdout, stderr bytes.Buffer
	root.SetOut(&stdout)
	root.SetErr(&stderr)
	root.SetArgs(append([]string{"upgrade"}, args...))

	err := root.Execute()
	return upgradeRun{stdout: stdout.String(), stderr: stderr.String(), err: err}
}

// isolateCommandEnv keeps a root-command execution away from the developer's
// real config, credentials and keyring.
func isolateCommandEnv(t *testing.T) {
	t.Helper()
	t.Setenv("HEY_NO_KEYRING", "1")
	t.Setenv("HEY_BASE_URL", "")
	t.Setenv("HEY_TOKEN", "")
	tmpDir := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", tmpDir)
	t.Setenv("XDG_STATE_HOME", tmpDir)
	t.Setenv("XDG_CACHE_HOME", tmpDir)
}

// --- command tests ---

// --count and --ids-only can never render upgrade's map data; output.Writer
// rejects them only at write time — after the binary has already been
// replaced. That failure order reports a structured error for a completed
// mutation, so the formats must be refused before the upgrade starts.
func TestUpgradeRejectsListOnlyFormatsBeforeUpgrading(t *testing.T) {
	for _, flag := range []string{"--count", "--ids-only"} {
		t.Run(flag, func(t *testing.T) {
			stubVersion(t, "1.0.0")
			fetched := false
			stubReleaseFetcher(t, func(context.Context) (releaseInfo, error) {
				fetched = true
				return releaseInfo{Version: "1.1.0"}, nil
			})

			run := executeUpgradeCommandAs(t, flag)
			requireUpgradeError(t, run.err, "usage")
			assertContains(t, output.AsError(run.err).Message, flag)
			if fetched {
				t.Error("the format must be rejected before the upgrade starts")
			}
		})
	}
}

// A pinned version must be a semantic version, rejected before any lookup.
func TestUpgradeInvalidPinnedVersionIsUsageError(t *testing.T) {
	stubVersion(t, "1.0.0")
	stubReleaseFetcher(t, func(context.Context) (releaseInfo, error) {
		t.Error("an invalid version argument must not reach the release lookup")
		return releaseInfo{}, nil
	})
	stubReleaseByTagFetcher(t, func(context.Context, string) (releaseInfo, error) {
		t.Error("an invalid version argument must not reach the release lookup")
		return releaseInfo{}, nil
	})

	run := executeUpgradeCommandAs(t, "--json", "not-a-version")
	requireUpgradeError(t, run.err, "usage")
}

// A release fetch that yields a non-semver tag (a manually published
// release, or a response missing tag_name) must fail before any upgrade
// starts: isUpdateAvailable's inequality fallback would otherwise treat the
// bogus value as an update and mutate a package-manager install before its
// confirmation step could refuse it.
func TestUpgradeNonSemverReleaseMetadataFailsBeforeUpgrade(t *testing.T) {
	for name, latest := range map[string]string{
		"non-semver tag":   "nightly",
		"missing tag_name": "",
	} {
		t.Run(name, func(t *testing.T) {
			stubVersion(t, "1.2.3")
			mutated := false
			stubUpgradeCheckers(t, upgradeCheckersStub{
				latestVersion: latest,
				isBrew:        true,
				homebrewUpgrade: func(context.Context, string, io.Writer, io.Writer) error {
					mutated = true
					return nil
				},
			})

			run := executeUpgradeCommand(t)
			apiErr := requireUpgradeError(t, run.err, "upgrade_failed")
			assertContains(t, apiErr.Message, "not a semantic version")
			if mutated {
				t.Error("invalid release metadata must fail before the package manager runs")
			}
		})
	}
}

// A pinned version targets that release's tag, not /releases/latest —
// prereleases are published without make_latest and are invisible there.
func TestUpgradePinnedVersionUsesTagLookup(t *testing.T) {
	stubVersion(t, "1.0.0")
	stubUpgradeCheckers(t, upgradeCheckersStub{latestVersion: "9.9.9"})
	stubReleaseFetcher(t, func(context.Context) (releaseInfo, error) {
		t.Error("a pinned upgrade must not consult /releases/latest")
		return releaseInfo{}, nil
	})
	stubReleaseByTagFetcher(t, func(_ context.Context, ver string) (releaseInfo, error) {
		if ver != "1.1.0-rc.1" {
			t.Errorf("fetched tag for %q, want 1.1.0-rc.1", ver)
		}
		return releaseInfo{Version: "1.1.0-rc.1"}, nil
	})
	stubGoInstallChecker(t, false)
	stubSelfUpdateTarget(t, "", errors.New("no exe"))

	run := executeUpgradeCommandAs(t, "--json", "v1.1.0-rc.1")
	apiErr := requireUpgradeError(t, run.err, "upgrade_required")
	assertContains(t, apiErr.Message, "1.1.0-rc.1")
}

// Pinning never downgrades: a version at or below the installed one is the
// same no-op as an up-to-date check.
func TestUpgradePinnedOlderVersionIsUpToDate(t *testing.T) {
	stubVersion(t, "1.2.0")
	stubUpgradeCheckers(t, upgradeCheckersStub{latestVersion: "1.2.0"})
	stubReleaseByTagFetcher(t, func(context.Context, string) (releaseInfo, error) {
		return releaseInfo{Version: "1.1.0"}, nil
	})

	run := executeUpgradeCommandAs(t, "--json", "1.1.0")
	mustNoError(t, run.err)
	if got := run.data(t)["status"]; got != "up_to_date" {
		t.Errorf("status = %q, want up_to_date", got)
	}
}

// Package managers install their manifest's version; a pin they can't honor
// must be refused, not silently replaced with whatever they would install.
func TestUpgradePinnedManagedInstallRefused(t *testing.T) {
	stubVersion(t, "1.0.0")
	stubReleaseByTagFetcher(t, func(context.Context, string) (releaseInfo, error) {
		return releaseInfo{Version: "1.5.0"}, nil
	})

	t.Run("homebrew", func(t *testing.T) {
		stubUpgradeCheckers(t, upgradeCheckersStub{
			isBrew: true,
			homebrewUpgrade: func(context.Context, string, io.Writer, io.Writer) error {
				t.Error("a refused pin must not run brew")
				return nil
			},
		})
		run := executeUpgradeCommandAs(t, "--json", "1.5.0")
		apiErr := requireUpgradeError(t, run.err, "upgrade_required")
		assertContains(t, apiErr.Message, "managed by Homebrew")
		assertContains(t, apiErr.Hint, "brew upgrade --cask")
	})

	t.Run("scoop", func(t *testing.T) {
		stubUpgradeCheckers(t, upgradeCheckersStub{
			isScoop: true,
			scoopUpgrade: func(context.Context, bool, io.Writer, io.Writer) error {
				t.Error("a refused pin must not run scoop")
				return nil
			},
		})
		run := executeUpgradeCommandAs(t, "--json", "1.5.0")
		apiErr := requireUpgradeError(t, run.err, "upgrade_required")
		assertContains(t, apiErr.Message, "managed by Scoop")
		assertContains(t, apiErr.Hint, "scoop update")
	})
}

// A dev build, or anything else that is not a semantic version, has no
// release lineage and must refuse before touching the network.
func TestUpgradeRefusesNonReleaseBuilds(t *testing.T) {
	for _, current := range []string{"dev", "custom-build", "abc1234", ""} {
		t.Run(current, func(t *testing.T) {
			stubVersion(t, current)
			fetched := false
			stubReleaseFetcher(t, func(context.Context) (releaseInfo, error) {
				fetched = true
				return releaseInfo{Version: "1.0.0"}, nil
			})

			run := executeUpgradeCommand(t)
			apiErr := requireUpgradeError(t, run.err, "upgrade_required")
			assertContains(t, apiErr.Message, "not a release build")
			if fetched {
				t.Error("non-release builds must not query GitHub")
			}
		})
	}
}

func TestUpgradeAlreadyCurrent(t *testing.T) {
	stubVersion(t, "1.2.3")
	stubUpgradeCheckers(t, upgradeCheckersStub{latestVersion: "1.2.3"})

	run := executeUpgradeCommand(t)
	mustNoError(t, run.err)
	if got := run.data(t)["status"]; got != "up_to_date" {
		t.Errorf("status = %q, want up_to_date", got)
	}
}

// An available update the CLI can't apply is a failure, not a success with a
// link — "ok": true plus a release URL reads as a completed upgrade.
func TestUpgradeAvailableButUnappliableFails(t *testing.T) {
	stubVersion(t, "1.2.3")
	stubUpgradeCheckers(t, upgradeCheckersStub{latestVersion: "1.3.0"})
	stubGoInstallChecker(t, false)
	stubSelfUpdateTarget(t, "", errors.New("no exe"))

	run := executeUpgradeCommand(t)
	apiErr := requireUpgradeError(t, run.err, "upgrade_required")
	assertContains(t, apiErr.Hint, "releases/tag/v1.3.0")
	if run.stdout != "" {
		t.Errorf("a failed upgrade must not write an envelope to stdout, got %q", run.stdout)
	}
}

func TestUpgradeSuppressesOlderLatestRelease(t *testing.T) {
	stubVersion(t, "0.4.1-0.20260313174735-243815fa23b2")
	stubUpgradeCheckers(t, upgradeCheckersStub{latestVersion: "0.4.0"})

	run := executeUpgradeCommand(t)
	mustNoError(t, run.err)
	if got := run.data(t)["status"]; got != "up_to_date" {
		t.Errorf("status = %q, want up_to_date", got)
	}
}

// Styled output narrates on stdout. Machine output suppresses narration
// entirely: stdout carries only the envelope, and stderr stays reserved for
// the JSON error envelope Execute writes on failure — human text mixed into
// either stream breaks automation parsing the documented formats.
func TestUpgradeNarrationFollowsOutputMode(t *testing.T) {
	stubVersion(t, "1.0.0")
	stubUpgradeCheckers(t, upgradeCheckersStub{latestVersion: "1.0.0"})

	styled := executeStyledUpgradeCommand(t)
	mustNoError(t, styled.err)
	assertContains(t, styled.stdout, "Current version: 1.0.0")
	assertContains(t, styled.stdout, "already up to date")
	assertNotContains(t, styled.stdout, `"ok"`)
	if styled.stderr != "" {
		t.Errorf("styled mode wrote to stderr: %q", styled.stderr)
	}

	run := executeUpgradeCommand(t)
	mustNoError(t, run.err)
	assertNotContains(t, run.stdout, "Current version")
	if run.stderr != "" {
		t.Errorf("machine mode must not narrate on stderr, got %q", run.stderr)
	}
}

// A styled success ends with its narration: output.Writer.OK has no styled
// case, so an unconditional writeOK would append a JSON envelope after the
// human-readable progress.
func TestUpgradeStyledSuccessEmitsNoEnvelope(t *testing.T) {
	stubVersion(t, "1.2.3")
	stubUpgradeCheckers(t, upgradeCheckersStub{latestVersion: "1.3.0", isBrew: true})
	stubBinaryVersionProber(t, func(context.Context, string) (string, error) { return "1.3.0", nil })

	run := executeStyledUpgradeCommand(t)
	mustNoError(t, run.err)
	assertContains(t, run.stdout, "Upgraded 1.2.3 → 1.3.0")
	assertNotContains(t, run.stdout, `"ok"`)
	if run.stderr != "" {
		t.Errorf("styled mode wrote to stderr: %q", run.stderr)
	}
}

// A failed update check must honor the structured upgrade contract — a plain
// network error would read as a generic API failure, not an upgrade outcome.
func TestUpgradeCheckFailureIsStructured(t *testing.T) {
	stubVersion(t, "1.0.0")
	stubReleaseFetcher(t, func(context.Context) (releaseInfo, error) {
		return releaseInfo{}, errors.New("unexpected status: 403")
	})

	run := executeUpgradeCommand(t)
	apiErr := requireUpgradeError(t, run.err, "upgrade_failed")
	assertContains(t, apiErr.Message, "could not check for updates")
	assertContains(t, apiErr.Hint, "GITHUB_TOKEN")
}

// A pinned go-install upgrade hints the module toolchain command at that
// version rather than @latest.
func TestUpgradeGoInstallPinnedHint(t *testing.T) {
	stubVersion(t, "1.0.0")
	stubUpgradeCheckers(t, upgradeCheckersStub{})
	stubReleaseByTagFetcher(t, func(context.Context, string) (releaseInfo, error) {
		return releaseInfo{Version: "1.5.0"}, nil
	})
	stubGoInstallChecker(t, true)

	run := executeUpgradeCommandAs(t, "--json", "1.5.0")
	apiErr := requireUpgradeError(t, run.err, "upgrade_required")
	assertContains(t, apiErr.Hint, "go install github.com/basecamp/hey-cli/cmd/hey@v1.5.0")
}

func TestUpgradeGoInstallProvenance(t *testing.T) {
	for _, current := range []string{"1.0.0", "0.4.1-0.20260313174735-243815fa23b2"} {
		t.Run(current, func(t *testing.T) {
			stubVersion(t, current)
			stubUpgradeCheckers(t, upgradeCheckersStub{latestVersion: "1.1.0"})
			stubGoInstallChecker(t, true)

			run := executeUpgradeCommand(t)
			apiErr := requireUpgradeError(t, run.err, "upgrade_required")
			assertContains(t, apiErr.Message, "go install")
			assertContains(t, apiErr.Hint, "go install github.com/basecamp/hey-cli/cmd/hey@latest")
		})
	}
}

func TestUpgradeUnresolvableExecutable(t *testing.T) {
	stubVersion(t, "1.0.0")
	stubUpgradeCheckers(t, upgradeCheckersStub{latestVersion: "1.1.0"})
	stubGoInstallChecker(t, false)
	stubSelfUpdateTarget(t, "", errors.New("no exe"))

	run := executeUpgradeCommand(t)
	apiErr := requireUpgradeError(t, run.err, "upgrade_required")
	assertContains(t, apiErr.Message, "could not be resolved")
	assertContains(t, apiErr.Hint, "releases/tag/v1.1.0")
	if run.stderr != "" {
		t.Errorf("machine mode must not narrate on stderr, got %q", run.stderr)
	}
}

// upgrade and version never need a mail account: they must not trigger the
// linked-account selection that every data command goes through.
func TestUpgradeAndVersionSkipAccountScope(t *testing.T) {
	root := newRootCmd()
	for _, name := range []string{"upgrade", "version"} {
		command, _, err := root.Find([]string{name})
		if err != nil {
			t.Fatal(err)
		}
		if commandUsesAccountScope(command) {
			t.Errorf("%s must not use account scope", name)
		}
	}
	boxes, _, err := root.Find([]string{"boxes"})
	if err != nil {
		t.Fatal(err)
	}
	if !commandUsesAccountScope(boxes) {
		t.Error("boxes should still use account scope")
	}
}

// --- homebrew ---

func TestUpgradeHomebrewConfirmedSuccess(t *testing.T) {
	stubVersion(t, "1.2.3")
	stubUpgradeCheckers(t, upgradeCheckersStub{latestVersion: "1.3.0", isBrew: true})
	stubBinaryVersionProber(t, func(_ context.Context, path string) (string, error) {
		if path != "/opt/homebrew/bin/hey" {
			t.Errorf("probed %q, want the entrypoint of the prefix owning the running binary", path)
		}
		return "1.3.0", nil
	})

	run := executeUpgradeCommand(t)
	mustNoError(t, run.err)
	data := run.data(t)
	if data["status"] != "upgraded" || data["method"] != "homebrew" || data["to"] != "1.3.0" {
		t.Errorf("unexpected envelope data: %v", data)
	}
}

func TestUpgradeHomebrewStaleProbeIsIncomplete(t *testing.T) {
	stubVersion(t, "1.2.3")
	stubUpgradeCheckers(t, upgradeCheckersStub{latestVersion: "1.3.0", isBrew: true})
	stubBinaryVersionProber(t, func(context.Context, string) (string, error) { return "1.2.3", nil })

	run := executeUpgradeCommand(t)
	apiErr := requireUpgradeError(t, run.err, "upgrade_incomplete")
	assertContains(t, apiErr.Message, "still reports 1.2.3")
	assertContains(t, apiErr.Message, "1.3.0")
	assertContains(t, apiErr.Hint, "brew reinstall --cask basecamp/tap/hey")
}

// The owning prefix comes from the running executable; if that can't be
// resolved there is nothing to bind the upgrade to, and failing before the
// mutation beats upgrading whichever installation `brew` on PATH happens to
// manage.
func TestUpgradeHomebrewUnresolvableExecutableFailsBeforeMutation(t *testing.T) {
	stubVersion(t, "1.2.3")
	mutated := false
	stubUpgradeCheckers(t, upgradeCheckersStub{
		latestVersion: "1.3.0",
		isBrew:        true,
		homebrewUpgrade: func(context.Context, string, io.Writer, io.Writer) error {
			mutated = true
			return nil
		},
	})
	stubRawExecutablePathResolver(t, "", false)

	run := executeUpgradeCommand(t)
	apiErr := requireUpgradeError(t, run.err, "upgrade_failed")
	assertContains(t, apiErr.Hint, "brew upgrade --cask basecamp/tap/hey")
	if mutated {
		t.Error("brew must not run when the owning prefix is unknown")
	}
}

// On a machine with two Homebrew installations (Intel and Apple Silicon),
// `brew` from PATH can belong to the other prefix — upgrading through it
// would leave the running installation untouched while the probe confirms
// the other one's success. Both the brew invocation and the probe must bind
// to the prefix owning the running executable.
func TestUpgradeHomebrewBindsToOwningPrefix(t *testing.T) {
	stubVersion(t, "1.2.3")
	var brewUsed string
	stubUpgradeCheckers(t, upgradeCheckersStub{
		latestVersion: "1.3.0",
		isBrew:        true,
		homebrewUpgrade: func(_ context.Context, brew string, _ io.Writer, _ io.Writer) error {
			brewUsed = brew
			return nil
		},
	})
	stubRawExecutablePathResolver(t, "/usr/local/Caskroom/hey/1.2.3/hey", true)
	var probed string
	stubBinaryVersionProber(t, func(_ context.Context, path string) (string, error) {
		probed = path
		return "1.3.0", nil
	})

	run := executeUpgradeCommand(t)
	mustNoError(t, run.err)
	if brewUsed != "/usr/local/bin/brew" {
		t.Errorf("ran %q, want the owning prefix's /usr/local/bin/brew", brewUsed)
	}
	if probed != "/usr/local/bin/hey" {
		t.Errorf("probed %q, want the owning prefix's /usr/local/bin/hey", probed)
	}
}

func TestUpgradeHomebrewProbeFailureIsUnverified(t *testing.T) {
	stubVersion(t, "1.2.3")
	stubUpgradeCheckers(t, upgradeCheckersStub{latestVersion: "1.3.0", isBrew: true})
	stubBinaryVersionProber(t, func(context.Context, string) (string, error) {
		return "", errors.New("exec failed")
	})

	run := executeUpgradeCommand(t)
	requireUpgradeError(t, run.err, "upgrade_unverified")
}

// Manager execution failures are upgrade outcomes and must honor the
// structured contract, not surface as a generic error.
func TestUpgradeHomebrewExecFailureIsStructured(t *testing.T) {
	stubVersion(t, "1.2.3")
	stubUpgradeCheckers(t, upgradeCheckersStub{
		latestVersion: "1.3.0",
		isBrew:        true,
		homebrewUpgrade: func(context.Context, string, io.Writer, io.Writer) error {
			return errors.New("exit status 1")
		},
	})

	run := executeUpgradeCommand(t)
	apiErr := requireUpgradeError(t, run.err, "upgrade_failed")
	assertContains(t, apiErr.Message, "brew upgrade failed")
	assertContains(t, apiErr.Hint, "brew upgrade --cask basecamp/tap/hey")
}

// A release published while the manager runs can install something newer
// than the snapshot fetched at the start — reported >= latest is success.
func TestUpgradeHomebrewNewerReportedVersionIsSuccess(t *testing.T) {
	stubVersion(t, "1.2.3")
	stubUpgradeCheckers(t, upgradeCheckersStub{latestVersion: "1.3.0", isBrew: true})
	stubBinaryVersionProber(t, func(context.Context, string) (string, error) { return "1.3.1", nil })

	run := executeUpgradeCommand(t)
	mustNoError(t, run.err)
	if got := run.data(t)["to"]; got != "1.3.1" {
		t.Errorf("to = %q, want 1.3.1", got)
	}
}

// An uninterpretable probe result must fail safely as unconfirmed — never
// claim success or completion either way.
func TestUpgradeHomebrewGarbageReportedVersionIsUnverified(t *testing.T) {
	stubVersion(t, "1.2.3")
	stubUpgradeCheckers(t, upgradeCheckersStub{latestVersion: "1.3.0", isBrew: true})
	stubBinaryVersionProber(t, func(context.Context, string) (string, error) { return "source)", nil })

	run := executeUpgradeCommand(t)
	apiErr := requireUpgradeError(t, run.err, "upgrade_unverified")
	assertContains(t, apiErr.Message, "could not be interpreted")
}

// --- scoop ---

func TestUpgradeScoopConfirmedSuccess(t *testing.T) {
	stubVersion(t, "1.2.3")
	stubUpgradeCheckers(t, upgradeCheckersStub{latestVersion: "1.3.0", isScoop: true})
	stubScoopPrefixResolver(t, func(context.Context, string) (string, bool) {
		return "c:/users/alice/scoop/apps/hey/current", true
	})
	stubBinaryVersionProber(t, func(context.Context, string) (string, error) { return "1.3.0", nil })

	run := executeUpgradeCommand(t)
	mustNoError(t, run.err)
	if got := run.data(t)["method"]; got != "scoop" {
		t.Errorf("method = %q, want scoop", got)
	}
}

func TestUpgradeScoopStaleProbeIsIncomplete(t *testing.T) {
	stubVersion(t, "1.2.3")
	stubUpgradeCheckers(t, upgradeCheckersStub{latestVersion: "1.3.0", isScoop: true})
	stubScoopPrefixResolver(t, func(context.Context, string) (string, bool) {
		return "c:/users/alice/scoop/apps/hey/current", true
	})
	stubBinaryVersionProber(t, func(context.Context, string) (string, error) { return "1.2.3", nil })

	run := executeUpgradeCommand(t)
	requireUpgradeError(t, run.err, "upgrade_incomplete")
}

func TestUpgradeScoopPrefixFailureIsUnverified(t *testing.T) {
	stubVersion(t, "1.2.3")
	stubUpgradeCheckers(t, upgradeCheckersStub{latestVersion: "1.3.0", isScoop: true})
	stubScoopPrefixResolver(t, func(context.Context, string) (string, bool) { return "", false })

	run := executeUpgradeCommand(t)
	requireUpgradeError(t, run.err, "upgrade_unverified")
}

func TestUpgradeGlobalScoopUsesGlobalUpdate(t *testing.T) {
	stubVersion(t, "1.2.3")
	var gotGlobal bool
	stubUpgradeCheckers(t, upgradeCheckersStub{
		latestVersion: "1.3.0",
		isScoop:       true,
		isGlobalScoop: true,
		scoopUpgrade: func(_ context.Context, global bool, _ io.Writer, _ io.Writer) error {
			gotGlobal = global
			return nil
		},
	})
	stubBinaryVersionProber(t, func(context.Context, string) (string, error) { return "1.3.0", nil })

	run := executeUpgradeCommand(t)
	mustNoError(t, run.err)
	if !gotGlobal {
		t.Error("global scoop install must run `scoop update -g`")
	}
}

// `scoop prefix` resolves the local install first and has no scope flag, so
// when the same app exists in both scopes a global upgrade probed through it
// would inspect the user-scope binary — confirming against a copy the
// mutation never touched. The global probe must derive from the running
// executable instead, whether it is the app payload or the shim.
func TestUpgradeGlobalScoopProbesGlobalInstallNotLocalShadow(t *testing.T) {
	for name, exe := range map[string]string{
		"payload": "c:/programdata/scoop/apps/hey/1.2.3/hey.exe",
		"shim":    "c:/programdata/scoop/shims/hey.exe",
	} {
		t.Run(name, func(t *testing.T) {
			stubVersion(t, "1.2.3")
			stubUpgradeCheckers(t, upgradeCheckersStub{latestVersion: "1.3.0", isScoop: true, isGlobalScoop: true})
			stubExecutablePathResolver(t, exe, true)
			stubScoopPrefixResolver(t, func(context.Context, string) (string, bool) {
				t.Error("a global probe must not consult `scoop prefix`, which resolves the local scope first")
				return "c:/users/alice/scoop/apps/hey/current", true
			})
			var probed string
			stubBinaryVersionProber(t, func(_ context.Context, path string) (string, error) {
				probed = path
				return "1.3.0", nil
			})

			run := executeUpgradeCommand(t)
			mustNoError(t, run.err)
			want := filepath.Join(filepath.FromSlash("c:/programdata"), "scoop", "apps", "hey", "current", "hey.exe")
			if probed != want {
				t.Errorf("probed %q, want the global install's %q", probed, want)
			}
		})
	}
}

func TestUpgradeScoopExecFailureIsStructured(t *testing.T) {
	stubVersion(t, "1.2.3")
	stubUpgradeCheckers(t, upgradeCheckersStub{
		latestVersion: "1.3.0",
		isScoop:       true,
		isGlobalScoop: true,
		scoopUpgrade: func(context.Context, bool, io.Writer, io.Writer) error {
			return errors.New("exit status 1")
		},
	})

	run := executeUpgradeCommand(t)
	apiErr := requireUpgradeError(t, run.err, "upgrade_failed")
	assertContains(t, apiErr.Message, "scoop update failed")
	assertContains(t, apiErr.Hint, "scoop update -g hey")
}

// --- install-source detection ---

func TestIsHomebrewAnchorsOnCaskPayload(t *testing.T) {
	tests := []struct {
		path string
		want bool
	}{
		{"/opt/homebrew/caskroom/hey/1.2.3/hey", true},
		{"/usr/local/caskroom/hey/0.9.0/hey", true},
		{"/opt/homebrew/caskroom/hey/1.2.3/docs/hey", false}, // too deep
		{"/opt/homebrew/caskroom/hey/1.2.3/other", false},    // not the binary
		{"/opt/homebrew/caskroom/heyday/1.2.3/hey", false},   // sibling cask
		{"/home/alice/.local/bin/hey", false},
	}
	for _, tt := range tests {
		t.Run(tt.path, func(t *testing.T) {
			stubExecutablePathResolver(t, tt.path, true)
			if got := isHomebrew(context.Background()); got != tt.want {
				t.Errorf("isHomebrew(%q) = %v, want %v", tt.path, got, tt.want)
			}
		})
	}

	stubExecutablePathResolver(t, "", false)
	if isHomebrew(context.Background()) {
		t.Error("isHomebrew must be false when the executable path is unavailable")
	}
}

func TestIsScoopUsesExecutablePathProvenance(t *testing.T) {
	stubExecutablePathResolver(t, "/users/alice/scoop/apps/hey/current/hey.exe", true)
	if !isScoop(context.Background()) {
		t.Error("app payload path should be detected as scoop")
	}

	stubExecutablePathResolver(t, "/users/alice/bin/hey", true)
	if isScoop(context.Background()) {
		t.Error("plain home install is not scoop")
	}
}

func TestIsScoopDetectsShimViaPrefix(t *testing.T) {
	stubExecutablePathResolver(t, "/users/alice/scoop/shims/hey.exe", true)
	stubScoopPrefixResolver(t, func(_ context.Context, app string) (string, bool) {
		if app == scoopApp {
			return "/users/alice/scoop/apps/hey/current", true
		}
		return "", false
	})
	if !isScoop(context.Background()) {
		t.Error("user shim with a user-scope prefix should be detected")
	}

	stubExecutablePathResolver(t, "c:/programdata/scoop/shims/hey.exe", true)
	stubScoopPrefixResolver(t, func(_ context.Context, app string) (string, bool) {
		return "/programdata/scoop/apps/hey/current", true
	})
	if !isScoop(context.Background()) {
		t.Error("global shim with a global-scope prefix should be detected")
	}
}

func TestIsScoopShimIgnoresOppositeScopePrefix(t *testing.T) {
	stubExecutablePathResolver(t, "c:/programdata/scoop/shims/hey.exe", true)
	stubScoopPrefixResolver(t, func(context.Context, string) (string, bool) {
		return "/users/alice/scoop/apps/hey/current", true
	})
	if isScoop(context.Background()) {
		t.Error("a global shim must not claim a user-scope install")
	}
}

func TestIsGlobalScoopInstallUsesExecutablePathProvenance(t *testing.T) {
	tests := []struct {
		path string
		want bool
	}{
		{"c:/programdata/scoop/apps/hey/current/hey.exe", true},
		{"/users/alice/programdata/scoop/apps/hey/current/hey.exe", false},
		{"/users/alice/scoop/apps/hey/current/hey.exe", false},
	}
	for _, tt := range tests {
		stubExecutablePathResolver(t, tt.path, true)
		if got := isGlobalScoopInstall(context.Background()); got != tt.want {
			t.Errorf("isGlobalScoopInstall(%q) = %v, want %v", tt.path, got, tt.want)
		}
	}
}
