package cmd

import (
	"context"
	"errors"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/basecamp/hey-cli/internal/auth"
)

// doctorChecks runs every check against a machine hey has never seen, so the
// shell completion check reads a temporary home rather than the developer's.
func doctorChecks(t *testing.T) []map[string]string {
	t.Helper()
	stubCompletionEnv(t, testCompletionEnv(t, "bash"))
	return runDoctorChecks(context.Background(), newRootCmd())
}

func versionCheck(t *testing.T, checks []map[string]string) map[string]string {
	t.Helper()
	for _, c := range checks {
		if c["name"] == "CLI Version" {
			return c
		}
	}
	t.Fatalf("no CLI Version check in %v", checks)
	return nil
}

func TestDoctorReportsAnUnreadableAuthenticationState(t *testing.T) {
	t.Setenv("HEY_TOKEN", "")
	t.Setenv("HEY_NO_KEYRING", "1")
	configDir := t.TempDir()
	if err := os.WriteFile(filepath.Join(configDir, "credentials.json"), []byte("not-json"), 0600); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	previous := authMgr
	authMgr = auth.NewManager("https://app.hey.com", http.DefaultClient, configDir)
	t.Cleanup(func() { authMgr = previous })

	var authentication map[string]string
	for _, check := range doctorChecks(t) {
		if check["name"] == "Authentication" {
			authentication = check
			break
		}
	}
	if authentication == nil {
		t.Fatal("no Authentication check")
	}
	if authentication["status"] != "error" || !strings.Contains(authentication["message"], "Could not read authentication status") {
		t.Errorf("Authentication check = %v, want the storage read failure", authentication)
	}
	if strings.Contains(authentication["message"], "Not authenticated") {
		t.Errorf("Authentication check = %v, unreadable state must not be reported as signed out", authentication)
	}
}

func TestDoctorVersionWarnsWhenUpdateAvailable(t *testing.T) {
	stubVersion(t, "1.0.0")
	stubReleaseFetcher(t, func(ctx context.Context) (releaseInfo, error) {
		if _, ok := ctx.Deadline(); !ok {
			t.Error("doctor's release lookup must carry a deadline")
		}
		return releaseInfo{Version: "1.1.0"}, nil
	})

	check := versionCheck(t, doctorChecks(t))
	if check["status"] != "warning" {
		t.Errorf("status = %q, want warning", check["status"])
	}
	assertContains(t, check["message"], "update available: 1.1.0")
	if check["hint"] != "hey upgrade" {
		t.Errorf("hint = %q, want `hey upgrade`", check["hint"])
	}
}

func TestDoctorVersionOKWhenCurrentOrOffline(t *testing.T) {
	stubVersion(t, "1.1.0")
	stubReleaseFetcher(t, func(context.Context) (releaseInfo, error) {
		return releaseInfo{Version: "1.1.0"}, nil
	})
	check := versionCheck(t, doctorChecks(t))
	if check["status"] != "ok" || check["hint"] != "" {
		t.Errorf("current build should pass: %v", check)
	}

	stubReleaseFetcher(t, func(context.Context) (releaseInfo, error) {
		return releaseInfo{}, errors.New("dial tcp: network is unreachable")
	})
	check = versionCheck(t, doctorChecks(t))
	if check["status"] != "ok" {
		t.Errorf("an offline lookup is best-effort and must not fail the check: %v", check)
	}
}

func TestDoctorVersionSkipsLookupForNonReleaseBuilds(t *testing.T) {
	for _, v := range []string{"dev", "custom-build"} {
		stubVersion(t, v)
		stubReleaseFetcher(t, func(context.Context) (releaseInfo, error) {
			t.Errorf("release lookup must be skipped for %q", v)
			return releaseInfo{}, nil
		})
		check := versionCheck(t, doctorChecks(t))
		if !strings.HasPrefix(check["message"], v+" (") {
			t.Errorf("message = %q", check["message"])
		}
	}
}

// A go-install build can't run hey upgrade — that command is guaranteed to
// refuse with the goInstallChecker branch — so doctor must hint the module
// toolchain command directly instead of routing through a failing operation.
func TestDoctorVersionGoInstallUpdateHint(t *testing.T) {
	stubVersion(t, "1.0.0")
	stubReleaseFetcher(t, func(context.Context) (releaseInfo, error) {
		return releaseInfo{Version: "1.1.0"}, nil
	})
	stubGoInstallChecker(t, true)

	check := versionCheck(t, doctorChecks(t))
	if check["status"] != "warning" {
		t.Errorf("status = %q, want warning", check["status"])
	}
	if check["hint"] != "go install github.com/basecamp/hey-cli/cmd/hey@latest" {
		t.Errorf("hint = %q, want the go install command", check["hint"])
	}
}

func TestDoctorVersionMarksGoInstallBuilds(t *testing.T) {
	stubVersion(t, "1.0.0")
	stubReleaseFetcher(t, func(context.Context) (releaseInfo, error) {
		return releaseInfo{Version: "1.0.0"}, nil
	})

	stubGoInstallChecker(t, false)
	check := versionCheck(t, doctorChecks(t))
	assertNotContains(t, check["message"], "[go install]")

	stubGoInstallChecker(t, true)
	check = versionCheck(t, doctorChecks(t))
	assertContains(t, check["message"], "1.0.0 (none, unknown) [go install]")
}

// A non-semver latest tag (a `nightly` or a blank) is not something
// `hey upgrade` can install, so doctor must not recommend it.
func TestDoctorVersionIgnoresNonReleaseLatestTag(t *testing.T) {
	for _, latest := range []string{"nightly", ""} {
		stubVersion(t, "1.0.0")
		stubReleaseFetcher(t, func(context.Context) (releaseInfo, error) {
			return releaseInfo{Version: latest}, nil
		})
		check := versionCheck(t, doctorChecks(t))
		if check["status"] != "ok" || check["hint"] != "" {
			t.Errorf("latest %q: doctor must not warn about a non-release tag: %v", latest, check)
		}
	}
}

// Presence and health are reported separately: an unmanaged skill gets the
// move-aside remediation (hey skill install would refuse it), a missing one
// gets the install hint, and a marker that is not a regular file confers no
// ownership.
func TestDoctorBaselineSkillDiagnostics(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)

	check := checkBaselineSkill()
	if check["message"] != "Not installed" || check["hint"] != "hey skill install" {
		t.Errorf("missing skill: %v", check)
	}

	dir := filepath.Join(home, ".agents", "skills", "hey")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "SKILL.md"), []byte("# mine"), 0o644); err != nil {
		t.Fatal(err)
	}
	check = checkBaselineSkill()
	if check["status"] != "warning" || !strings.Contains(check["message"], "not a hey-cli-managed install") {
		t.Errorf("unmanaged skill: %v", check)
	}
	if check["hint"] != "Move it aside and run: hey skill install" {
		t.Errorf("unmanaged hint = %q", check["hint"])
	}

	// A directory planted in the marker's name confers no ownership.
	if err := os.MkdirAll(filepath.Join(dir, ".managed-by-hey-cli"), 0o755); err != nil {
		t.Fatal(err)
	}
	check = checkBaselineSkill()
	if !strings.Contains(check["message"], "not a hey-cli-managed install") {
		t.Errorf("non-regular marker treated as ownership: %v", check)
	}

	if err := os.RemoveAll(filepath.Join(dir, ".managed-by-hey-cli")); err != nil {
		t.Fatal(err)
	}
	writeOwnershipMarker(dir)
	check = checkBaselineSkill()
	if check["status"] != "ok" {
		t.Errorf("managed skill: %v", check)
	}
}
