// Package timezone loads a time zone by the name HEY will look it up by.
//
// HEY stores a zone name it cannot find rather than refusing it, reads the event as UTC,
// and fails later in its own edit form, so every name this program sends as an event's
// zone — `hey event --time-zone`, the account's zone, the zone chosen on the TUI's event
// form — is checked here first, by the same rules.
package timezone

import (
	"errors"
	"io/fs"
	"os"
	"path"
	"slices"
	"strings"
	"time"

	// Every build carries the zone database, so an account's zone and a chosen one load the
	// same way on Windows, Alpine, distroless images and a machine with old zone files.
	_ "time/tzdata"
)

// ErrNotAZone is a name time.LoadLocation answers that HEY cannot look up.
var ErrNotAZone = errors.New("not an IANA zone name")

// Load loads a zone the way HEY will look it up: by its exact IANA name.
//
// Go answers a few names HEY has no zone for: Local, the zone files that are not zones, and
// a path the file system tidies up, like America//New_York. And on a case-insensitive disk,
// as macOS has, it loads america/new_york from the file America/New_York, where HEY's lookup
// is case-sensitive; zoneFileSpelledAs catches that.
func Load(name string) (*time.Location, error) {
	if !fs.ValidPath(name) || name == "Local" || name == "localtime" || name == "posixrules" || name == "Factory" ||
		strings.HasPrefix(name, "posix/") || strings.HasPrefix(name, "right/") {
		return nil, ErrNotAZone
	}
	loc, err := time.LoadLocation(name)
	if err != nil {
		return nil, err
	}
	if !zoneFileSpelledAs(name) {
		return nil, ErrNotAZone
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
