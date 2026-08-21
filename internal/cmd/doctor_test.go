package cmd

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

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

func TestDoctorVersionWarnsWhenUpdateAvailable(t *testing.T) {
	stubVersion(t, "1.0.0")
	stubReleaseFetcher(t, func(ctx context.Context) (releaseInfo, error) {
		if _, ok := ctx.Deadline(); !ok {
			t.Error("doctor's release lookup must carry a deadline")
		}
		return releaseInfo{Version: "1.1.0"}, nil
	})

	check := versionCheck(t, runDoctorChecks(context.Background()))
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
	check := versionCheck(t, runDoctorChecks(context.Background()))
	if check["status"] != "ok" || check["hint"] != "" {
		t.Errorf("current build should pass: %v", check)
	}

	stubReleaseFetcher(t, func(context.Context) (releaseInfo, error) {
		return releaseInfo{}, errors.New("dial tcp: network is unreachable")
	})
	check = versionCheck(t, runDoctorChecks(context.Background()))
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
		check := versionCheck(t, runDoctorChecks(context.Background()))
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

	check := versionCheck(t, runDoctorChecks(context.Background()))
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
	check := versionCheck(t, runDoctorChecks(context.Background()))
	assertNotContains(t, check["message"], "[go install]")

	stubGoInstallChecker(t, true)
	check = versionCheck(t, runDoctorChecks(context.Background()))
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
		check := versionCheck(t, runDoctorChecks(context.Background()))
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
