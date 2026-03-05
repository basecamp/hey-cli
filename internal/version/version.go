package version

var (
	Version = "dev"
	Commit  = "none"
	Date    = "unknown"
)

func Full() string {
	if Version == "dev" {
		return "hey version dev (built from source)"
	}
	return "hey version " + Version
}

func UserAgent() string {
	return "hey-cli/" + Version
}
