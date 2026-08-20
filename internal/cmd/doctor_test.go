package cmd

import (
	"context"
	"errors"
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
