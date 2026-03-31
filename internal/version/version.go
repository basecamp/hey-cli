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

func init() {
	if Version == "dev" {
		if info, ok := debug.ReadBuildInfo(); ok && info.Main.Version != "" && info.Main.Version != "(devel)" {
			Version = strings.TrimPrefix(info.Main.Version, "v")
		}
	}
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
