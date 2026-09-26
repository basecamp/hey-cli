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

// eventNow is the clock an event's default date is read from, a seam for tests.
var eventNow = time.Now

// timeZoneHint is how every refusal about a zone says what to do instead.
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

// accountZone is the HEY account's time zone as the identity serves it, read at most once a
// command and only when a clock time needs it.
type accountZone struct {
	read bool
	name string
	err  error
}

// accountTimeZone is the zone HEY's web app reads typed times in: the one the identity serves,
// which HEY keeps in step with the browser the account last signed in from.
func (f *eventFields) accountTimeZone(ctx context.Context) (string, error) {
	if !f.account.read {
		identity, err := rootSDK.Identity().GetIdentity(ctx)
		f.account = accountZone{read: true, err: err}
		if err == nil && identity != nil {
			f.account.name = identity.TimeZone
		}
	}
	return f.account.name, f.account.err
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

	name, err := f.accountTimeZone(ctx)
	if err != nil {
		// A failed read keeps what it failed with — network, rate limit, a server error — so
		// a script can tell a retry from a mistake, and says --time-zone would do without it.
		// A login HEY no longer accepts is an auth failure and nothing else: --time-zone would
		// only move the refusal to the write.
		readErr := *apierr.AsError(apierr.FromSDK(err))
		if readErr.Code == apierr.CodeAuth {
			return "", nil, &readErr
		}
		readErr.Message = "no time zone to place the event in: your HEY account's time zone could not be read: " + readErr.Message
		readErr.Hint = timeZoneHint
		if readErr.Cause == nil {
			readErr.Cause = err
		}
		return "", nil, &readErr
	}
	if name == "" {
		return "", nil, errNoAccountZone("your HEY account has no time zone set", nil)
	}
	loc, err := loadEventZone(name)
	if err != nil {
		return "", nil, errNoAccountZone(fmt.Sprintf("your HEY account's time zone %s is not one this build of hey knows", terminal.SanitizeLine(name)), err)
	}
	return name, loc, nil
}

func errInvalidTimeZone(name string) error {
	return apierr.ErrUsageHint(fmt.Sprintf("invalid time-zone: %s", terminal.SanitizeLine(name)),
		"an IANA time zone name, spelled exactly, for example America/New_York")
}

func errNoAccountZone(reason string, cause error) error {
	return &apierr.Error{
		Code:    apierr.CodeUsage,
		Message: "no time zone to place the event in: " + reason,
		Hint:    timeZoneHint,
		Cause:   cause,
	}
}
