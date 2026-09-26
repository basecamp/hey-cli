package cmd

import (
	"errors"
	"io/fs"
	"net/http"
	"strings"
	"sync/atomic"
	"testing"
	"testing/fstest"

	"github.com/basecamp/hey-cli/internal/apierr"
)

// HEY stores a zone name it cannot look up and reads the event as UTC, so only a name HEY
// will find gets through — and it is refused before anything is read.
func TestEventsRefuseATimeZoneHEYCannotFind(t *testing.T) {
	for _, zone := range []string{"Not/AZone", "america/new_york", "Local", "localtime", "posixrules", "Factory", "posix/America/New_York", "right/UTC", "America//New_York", "America/./New_York", "/America/New_York", "Eastern Time (US & Canada)"} {
		for _, command := range [][]string{
			{"event", "add", "Dentist appointment", "--start-time", "10:00"},
			{"event", "edit", "4821", "--start-time", "10:00"},
		} {
			t.Run(command[1]+" "+zone, func(t *testing.T) {
				var requests atomic.Int32
				_, err := runJSONCommand(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					requests.Add(1)
					http.Error(w, "unexpected request", http.StatusInternalServerError)
				}), append(command, "--time-zone", zone)...)
				var cliErr *apierr.Error
				if !errors.As(err, &cliErr) || cliErr.Code != apierr.CodeUsage || !strings.Contains(cliErr.Message, "invalid time-zone") {
					t.Fatalf("error = %v, want an invalid time-zone usage error", err)
				}
				if got := requests.Load(); got != 0 {
					t.Errorf("requests = %d, want none", got)
				}
			})
		}
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
