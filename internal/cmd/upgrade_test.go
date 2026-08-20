package cmd

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
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

func stubBrewPrefixResolver(t *testing.T, resolve func(context.Context) (string, error)) {
	t.Helper()
	orig := brewPrefixResolver
	brewPrefixResolver = resolve
	t.Cleanup(func() { brewPrefixResolver = orig })
}

func stubReleaseFetcher(t *testing.T, fetch func(context.Context) (releaseInfo, error)) {
	t.Helper()
	orig := releaseFetcher
	releaseFetcher = fetch
	t.Cleanup(func() { releaseFetcher = orig })
}

type upgradeCheckersStub struct {
	latestVersion   string
	release         *releaseInfo // optional richer release (assets); read at call time so tests can mutate
	isBrew          bool
	isScoop         bool
	isGlobalScoop   bool
	homebrewUpgrade func(context.Context, io.Writer, io.Writer) error
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
		homebrewUpgrader = func(context.Context, io.Writer, io.Writer) error { return nil }
	}
	t.Cleanup(func() { homebrewUpgrader = origHU })

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
	isolateCommandEnv(t)

	root := newRootCmd()
	var stdout, stderr bytes.Buffer
	root.SetOut(&stdout)
	root.SetErr(&stderr)
	root.SetArgs([]string{"upgrade", "--json"})

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
	assertContains(t, run.stderr, "already up to date")
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
	assertContains(t, run.stderr, "update available: 1.3.0")
	assertContains(t, run.stderr, "releases/tag/v1.3.0")
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
	assertContains(t, run.stderr, "already up to date")
	assertNotContains(t, run.stderr, "update available")
}

// Styled output narrates on stdout; machine output keeps stdout for the
// envelope and narrates on stderr. Neither leaks to os.Stdout.
func TestUpgradeNarrationFollowsOutputMode(t *testing.T) {
	stubVersion(t, "1.0.0")
	stubUpgradeCheckers(t, upgradeCheckersStub{latestVersion: "1.0.0"})

	isolateCommandEnv(t)
	root := newRootCmd()
	var stdout, stderr bytes.Buffer
	root.SetOut(&stdout)
	root.SetErr(&stderr)
	root.SetArgs([]string{"upgrade", "--styled"})
	mustNoError(t, root.Execute())
	assertContains(t, stdout.String(), "Current version: 1.0.0")
	assertContains(t, stdout.String(), "already up to date")
	if stderr.Len() != 0 {
		t.Errorf("styled mode wrote to stderr: %q", stderr.String())
	}

	run := executeUpgradeCommand(t)
	mustNoError(t, run.err)
	assertContains(t, run.stderr, "Current version: 1.0.0")
	assertNotContains(t, run.stdout, "Current version")
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
	assertContains(t, run.stderr, "releases/tag/v1.1.0")
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
	stubBrewPrefixResolver(t, func(context.Context) (string, error) { return "/opt/homebrew", nil })
	stubBinaryVersionProber(t, func(_ context.Context, path string) (string, error) {
		if path != "/opt/homebrew/bin/hey" {
			t.Errorf("probed %q, want the brew-managed entrypoint", path)
		}
		return "1.3.0", nil
	})

	run := executeUpgradeCommand(t)
	mustNoError(t, run.err)
	assertContains(t, run.stderr, "Upgrading via Homebrew…")
	data := run.data(t)
	if data["status"] != "upgraded" || data["method"] != "homebrew" || data["to"] != "1.3.0" {
		t.Errorf("unexpected envelope data: %v", data)
	}
}

func TestUpgradeHomebrewStaleProbeIsIncomplete(t *testing.T) {
	stubVersion(t, "1.2.3")
	stubUpgradeCheckers(t, upgradeCheckersStub{latestVersion: "1.3.0", isBrew: true})
	stubBrewPrefixResolver(t, func(context.Context) (string, error) { return "/opt/homebrew", nil })
	stubBinaryVersionProber(t, func(context.Context, string) (string, error) { return "1.2.3", nil })

	run := executeUpgradeCommand(t)
	apiErr := requireUpgradeError(t, run.err, "upgrade_incomplete")
	assertContains(t, apiErr.Message, "still reports 1.2.3")
	assertContains(t, apiErr.Message, "1.3.0")
	assertContains(t, apiErr.Hint, "brew reinstall --cask basecamp/tap/hey")
}

func TestUpgradeHomebrewPrefixFailureIsUnverified(t *testing.T) {
	stubVersion(t, "1.2.3")
	stubUpgradeCheckers(t, upgradeCheckersStub{latestVersion: "1.3.0", isBrew: true})
	stubBrewPrefixResolver(t, func(context.Context) (string, error) { return "", errors.New("brew not on PATH") })

	run := executeUpgradeCommand(t)
	apiErr := requireUpgradeError(t, run.err, "upgrade_unverified")
	assertContains(t, apiErr.Hint, "hey version")
}

func TestUpgradeHomebrewProbeFailureIsUnverified(t *testing.T) {
	stubVersion(t, "1.2.3")
	stubUpgradeCheckers(t, upgradeCheckersStub{latestVersion: "1.3.0", isBrew: true})
	stubBrewPrefixResolver(t, func(context.Context) (string, error) { return "/opt/homebrew", nil })
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
		homebrewUpgrade: func(context.Context, io.Writer, io.Writer) error {
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
	stubBrewPrefixResolver(t, func(context.Context) (string, error) { return "/opt/homebrew", nil })
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
	stubBrewPrefixResolver(t, func(context.Context) (string, error) { return "/opt/homebrew", nil })
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
	assertContains(t, run.stderr, "Upgrading via Scoop…")
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
	stubScoopPrefixResolver(t, func(context.Context, string) (string, bool) {
		return "c:/programdata/scoop/apps/hey/current", true
	})
	stubBinaryVersionProber(t, func(context.Context, string) (string, error) { return "1.3.0", nil })

	run := executeUpgradeCommand(t)
	mustNoError(t, run.err)
	if !gotGlobal {
		t.Error("global scoop install must run `scoop update -g`")
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
