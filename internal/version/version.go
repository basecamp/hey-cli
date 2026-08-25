package version

import (
	"runtime/debug"
	"strings"
)

var (
	Version = "dev"
	Commit  = "none"
	Date    = "unknown"
)

// fromBuildInfo records whether Version was derived from debug.ReadBuildInfo
// rather than stamped via ldflags. Build-info versions come from `go install`
// (stable tags and pseudo-versions alike) and cannot be self-updated in place.
var fromBuildInfo bool

func init() {
	if Version != "dev" {
		return
	}
	info, ok := debug.ReadBuildInfo()
	if !ok || info.Main.Version == "" || info.Main.Version == "(devel)" {
		return
	}
	// A build from a source checkout is still a dev build. Go stamps such a build's
	// main-module version with a pseudo-version derived from the checkout's VCS
	// state, which is exactly the shape a `go install module@version` build reports —
	// the difference is that only the checkout build carries the VCS settings. So a
	// version with vcs metadata behind it stays "dev", and only a true module build
	// (no checkout, no vcs.revision) adopts what the toolchain recorded.
	for _, setting := range info.Settings {
		if setting.Key == "vcs.revision" {
			return
		}
	}
	Version = strings.TrimPrefix(info.Main.Version, "v")
	fromBuildInfo = true
}

// Full returns the full version string for display.
func Full() string {
	if Version == "dev" {
		return "hey version dev (built from source)"
	}
	return "hey version " + Version
}

// UserAgent returns the user agent string for API requests.
func UserAgent() string {
	return "hey-cli/" + Version + " (https://github.com/basecamp/hey-cli)"
}

// IsDev returns true if this is a development build.
func IsDev() bool {
	return Version == "dev"
}

// IsGoInstall reports whether this binary's version came from Go module build
// info (a `go install` build) instead of release ldflags.
func IsGoInstall() bool {
	return fromBuildInfo
}

// Source names where this build came from: "dev" for a source build,
// "go install" for a module-toolchain build, "release" for a stamped release.
func Source() string {
	switch {
	case IsDev():
		return "dev"
	case IsGoInstall():
		return "go install"
	default:
		return "release"
	}
}
