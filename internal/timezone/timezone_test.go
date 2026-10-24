package timezone

import (
	"errors"
	"io/fs"
	"strings"
	"testing"
	"testing/fstest"
)

// Load takes the names HEY looks up — canonical names and links alike — and refuses the ones
// Go answers that HEY has no zone for, before anything is sent.
func TestLoad(t *testing.T) {
	for _, name := range []string{"America/New_York", "America/Indiana/Indianapolis", "US/Eastern", "Etc/GMT+5", "UTC", "Europe/Kyiv"} {
		if _, err := Load(name); err != nil {
			t.Errorf("Load(%q) = %v, want the zone", name, err)
		}
	}
	for _, name := range []string{"", "Local", "localtime", "posixrules", "Factory", "posix/Europe/Zagreb", "right/Europe/Zagreb",
		"America//New_York", "/America/New_York", "Eastern Time (US & Canada)", "Mars/Olympus_Mons"} {
		if _, err := Load(name); err == nil {
			t.Errorf("Load(%q) loaded a zone HEY cannot look up", name)
		}
	}
	if _, err := Load("Local"); !errors.Is(err, ErrNotAZone) {
		t.Errorf("Load(Local) = %v, want ErrNotAZone", err)
	}
}

// foldedFS answers a name in any case, as the zone files on a case-insensitive disk do.
type foldedFS struct{ fstest.MapFS }

func (f foldedFS) Open(name string) (fs.File, error) {
	return f.MapFS.Open(f.stored(name))
}

func (f foldedFS) Stat(name string) (fs.FileInfo, error) {
	return f.MapFS.Stat(f.stored(name))
}

func (f foldedFS) stored(name string) string {
	for stored := range f.MapFS {
		if strings.EqualFold(stored, name) {
			return stored
		}
	}
	return name
}

// On macOS Go loads america/new_york from the file America/New_York, and HEY would store a
// name it cannot find. A name is only taken as the files spell it; one no file answers came
// from the embedded database, which answers exact names alone.
func TestZoneFileSpelledAs(t *testing.T) {
	previous := zoneFiles
	zoneFiles = func() []fs.FS {
		return []fs.FS{foldedFS{fstest.MapFS{"America/New_York": {Data: []byte("TZif")}, "UTC": {Data: []byte("TZif")}}}}
	}
	t.Cleanup(func() { zoneFiles = previous })

	for name, want := range map[string]bool{
		"America/New_York": true,
		"america/new_york": false,
		"America/NEW_YORK": false,
		"UTC":              true,
		"utc":              false,
		"Europe/Lisbon":    true,
	} {
		if got := zoneFileSpelledAs(name); got != want {
			t.Errorf("zoneFileSpelledAs(%q) = %v, want %v", name, got, want)
		}
	}
}
