package cmd

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path"
	"slices"
	"strings"
	"time"

	// Every build carries the zone database, so an account's zone and --time-zone load the
	// same way on Windows, Alpine, distroless images and a machine with old zone files.
	_ "time/tzdata"

	"github.com/basecamp/hey-cli/internal/apierr"
	"github.com/basecamp/hey-cli/internal/terminal"
)

// timeZoneHint is how every refusal about an event's zone says what to do instead.
const timeZoneHint = "pass --time-zone with an IANA zone name, for example --time-zone America/New_York"

// errNotAZone is a name time.LoadLocation answers that HEY cannot look up.
var errNotAZone = errors.New("not an IANA zone name")

// loadEventZone loads a zone the way HEY will look it up: by its exact IANA name. HEY stores
// a name it cannot find rather than refusing it, reads the event as UTC, and fails later in
// its own edit form, so anything HEY would not find is refused here instead.
//
// Go answers a few names HEY has no zone for: Local, the zone files that are not zones, and
// a path the file system tidies up, like America//New_York. And on a case-insensitive disk,
// as macOS has, it loads america/new_york from the file America/New_York, where HEY's lookup
// is case-sensitive; zoneFileSpelledAs catches that.
func loadEventZone(name string) (*time.Location, error) {
	if !fs.ValidPath(name) || name == "Local" || name == "localtime" || name == "posixrules" || name == "Factory" ||
		strings.HasPrefix(name, "posix/") || strings.HasPrefix(name, "right/") {
		return nil, errNotAZone
	}
	loc, err := time.LoadLocation(name)
	if err != nil {
		return nil, err
	}
	if !zoneFileSpelledAs(name) {
		return nil, errNotAZone
	}
	return loc, nil
}

// zoneFiles are the zone databases on disk that time.LoadLocation reads before the one
// compiled in, a seam for tests. The embedded database looks names up case-sensitively, so
// only these can answer a name in the wrong case.
var zoneFiles = func() []fs.FS {
	dirs := []string{"/usr/share/zoneinfo", "/usr/share/lib/zoneinfo", "/usr/lib/locale/TZ", "/etc/zoneinfo", "/var/db/timezone/zoneinfo"}
	if dir := os.Getenv("ZONEINFO"); dir != "" {
		dirs = append([]string{dir}, dirs...)
	}
	files := make([]fs.FS, 0, len(dirs))
	for _, dir := range dirs {
		files = append(files, os.DirFS(dir))
	}
	return files
}

// zoneFileSpelledAs is whether every zone file on disk that answers the name is spelled
// exactly that way. A name no file answers was read from the embedded database, which
// only answers exact names.
func zoneFileSpelledAs(name string) bool {
	for _, files := range zoneFiles() {
		if _, err := fs.Stat(files, name); err != nil {
			continue
		}
		dir := "."
		for part := range strings.SplitSeq(name, "/") {
			entries, err := fs.ReadDir(files, dir)
			if err != nil || !slices.ContainsFunc(entries, func(entry fs.DirEntry) bool { return entry.Name() == part }) {
				return false
			}
			dir = path.Join(dir, part)
		}
	}
	return true
}

// writeZone is the zone a timed write's clock times are read in: --time-zone, or the HEY
// account's. Nothing else is guessed at — not the machine's, which is UTC on a server and
// in most sandboxes, and not UTC, which is what HEY would read a zoneless time as.
func (f *eventFields) writeZone(ctx context.Context) (string, *time.Location, error) {
	if f.timeZone != "" {
		loc, err := loadEventZone(f.timeZone)
		if err != nil {
			return "", nil, errInvalidTimeZone(f.timeZone)
		}
		return f.timeZone, loc, nil
	}

	name, loc, err := f.account.location(ctx)
	if err != nil {
		return "", nil, accountZoneRefusal(err, "no time zone to place the event in", timeZoneHint)
	}
	return name, loc, nil
}

func errInvalidTimeZone(name string) error {
	return apierr.ErrUsageHint(fmt.Sprintf("invalid time-zone: %s", terminal.SanitizeLine(name)),
		"an IANA time zone name, spelled exactly, for example America/New_York")
}
