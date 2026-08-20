package version

import "testing"

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
	if got := UserAgent(); got != "hey-cli/1.0.0 (https://github.com/basecamp/hey-cli)" {
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
