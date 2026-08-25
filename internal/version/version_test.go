package version

import (
	"runtime"
	"runtime/debug"
	"strings"
	"testing"
)

func stubVersion(t *testing.T, v string, goInstall bool) {
	t.Helper()
	origVersion, origFromBuildInfo := Version, fromBuildInfo
	Version, fromBuildInfo = v, goInstall
	t.Cleanup(func() { Version, fromBuildInfo = origVersion, origFromBuildInfo })
}

func TestIsDev(t *testing.T) {
	tests := []struct {
		version string
		want    bool
	}{
		{"dev", true},
		{"1.0.0", false},
		{"0.1.0", false},
		{"v1.2.3", false},
		{"", false},
	}

	for _, tt := range tests {
		t.Run(tt.version, func(t *testing.T) {
			stubVersion(t, tt.version, false)
			if got := IsDev(); got != tt.want {
				t.Errorf("IsDev() with Version=%q = %v, want %v", tt.version, got, tt.want)
			}
		})
	}
}

func TestFull(t *testing.T) {
	stubVersion(t, "dev", false)
	if got := Full(); got != "hey version dev (built from source)" {
		t.Errorf("Full() with dev = %q", got)
	}

	stubVersion(t, "1.2.3", false)
	if got := Full(); got != "hey version 1.2.3" {
		t.Errorf("Full() with 1.2.3 = %q", got)
	}
}

func TestUserAgent(t *testing.T) {
	stubVersion(t, "1.0.0", false)

	got := UserAgent()
	if !strings.HasPrefix(got, "HEY-CLI/1.0.0 (") {
		t.Errorf("UserAgent() = %q, want HEY-CLI/1.0.0 product token", got)
	}
	if !strings.HasSuffix(got, " "+runtime.GOARCH+"; https://github.com/basecamp/hey-cli)") {
		t.Errorf("UserAgent() = %q, want OS, arch and repo URL in the comment", got)
	}
	if runtime.GOOS == "linux" && got != "HEY-CLI/1.0.0 (Linux "+runtime.GOARCH+"; https://github.com/basecamp/hey-cli)" {
		t.Errorf("UserAgent() = %q", got)
	}
}

func TestIsGoInstall(t *testing.T) {
	stubVersion(t, "1.0.0", false)
	if IsGoInstall() {
		t.Error("IsGoInstall() = true for ldflags-stamped build, want false")
	}

	// Build-info provenance applies to stable versions and pseudo-versions
	// alike — the flag, not the version shape, is the signal.
	stubVersion(t, "0.4.1-0.20260313174735-243815fa23b2", true)
	if !IsGoInstall() {
		t.Error("IsGoInstall() = false for build-info-derived version, want true")
	}
}

func TestSource(t *testing.T) {
	tests := []struct {
		name      string
		version   string
		goInstall bool
		want      string
	}{
		{name: "dev build", version: "dev", goInstall: false, want: "dev"},
		{name: "go install", version: "1.2.3", goInstall: true, want: "go install"},
		{name: "release", version: "1.2.3", goInstall: false, want: "release"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			stubVersion(t, tt.version, tt.goInstall)
			if got := Source(); got != tt.want {
				t.Errorf("Source() = %q, want %q", got, tt.want)
			}
		})
	}
}

// classify is the provenance decision itself: what an unstamped binary adopts from
// build info, and what it refuses. The vcs.revision branch is the point — a source
// checkout's build carries a toolchain-stamped pseudo-version in exactly the shape a
// `go install` build reports, and only the VCS settings tell the two apart.
func TestClassify(t *testing.T) {
	moduleBuild := func(version string, settings ...debug.BuildSetting) *debug.BuildInfo {
		info := &debug.BuildInfo{Settings: settings}
		info.Main.Version = version
		return info
	}
	vcs := debug.BuildSetting{Key: "vcs.revision", Value: "c426c98b3ea4"}

	tests := []struct {
		name          string
		stamped       string
		info          *debug.BuildInfo
		ok            bool
		wantVersion   string
		wantBuildInfo bool
	}{
		{name: "release ldflags stand", stamped: "1.2.3", info: moduleBuild("v0.9.9"), ok: true, wantVersion: "1.2.3"},
		{name: "go install adopts the module version", stamped: "dev", info: moduleBuild("v1.0.1"), ok: true, wantVersion: "1.0.1", wantBuildInfo: true},
		{name: "go install pseudo-version adopts too", stamped: "dev", info: moduleBuild("v1.0.1-0.20260825032930-c426c98b3ea4"), ok: true, wantVersion: "1.0.1-0.20260825032930-c426c98b3ea4", wantBuildInfo: true},
		{name: "checkout build stays dev", stamped: "dev", info: moduleBuild("v1.0.1-0.20260825032930-c426c98b3ea4+dirty", vcs), ok: true, wantVersion: "dev"},
		{name: "devel stays dev", stamped: "dev", info: moduleBuild("(devel)"), ok: true, wantVersion: "dev"},
		{name: "no build info stays dev", stamped: "dev", info: nil, ok: false, wantVersion: "dev"},
		{name: "empty module version stays dev", stamped: "dev", info: moduleBuild(""), ok: true, wantVersion: "dev"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			version, fromInfo := classify(tt.stamped, tt.info, tt.ok)
			if version != tt.wantVersion || fromInfo != tt.wantBuildInfo {
				t.Errorf("classify(%q) = %q, %v; want %q, %v", tt.stamped, version, fromInfo, tt.wantVersion, tt.wantBuildInfo)
			}
		})
	}
}
