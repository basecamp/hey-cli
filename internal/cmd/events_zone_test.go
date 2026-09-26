package cmd

import (
	"errors"
	"io"
	"io/fs"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"testing/fstest"
	"time"

	"github.com/basecamp/hey-cli/internal/apierr"
)

// zoneFixture is what zoneServer answers with: the identity's time zone, or a status for the
// identity read, and the event an edit reads.
type zoneFixture struct {
	accountZone    string
	identityStatus int
	event          string
}

// zoneRequests counts the identity reads and the writes a command made, and keeps the form
// of the last write.
type zoneRequests struct {
	identity atomic.Int32
	writes   atomic.Int32

	mu   sync.Mutex
	form url.Values
}

func (r *zoneRequests) written(t *testing.T) url.Values {
	t.Helper()
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.form == nil {
		t.Fatal("no event was written")
	}
	return r.form
}

// zoneServer answers what an event write reads and writes on calendar 9: the identity, the
// calendars, the event an edit reads, and the write itself.
func zoneServer(t *testing.T, fixture zoneFixture) (http.Handler, *zoneRequests) {
	t.Helper()
	requests := &zoneRequests{}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/identity.json":
			requests.identity.Add(1)
			if fixture.identityStatus != 0 {
				http.Error(w, `{"error":"identity unavailable"}`, fixture.identityStatus)
				return
			}
			zone := "null"
			if fixture.accountZone != "" {
				zone = `"` + fixture.accountZone + `"`
			}
			_, _ = io.WriteString(w, `{"id":1,"name":"Jason Fried","time_zone":`+zone+`}`)
		case r.Method == http.MethodGet && r.URL.Path == "/calendars.json":
			_, _ = io.WriteString(w, `{"calendars":[{"calendar":{"id":9,"name":"Work","owned":true,"kind":"normal"}}]}`)
		case r.Method == http.MethodGet && r.URL.Path == "/calendars/9/recordings.json":
			_, _ = io.WriteString(w, `{"Calendar::Event":[`+fixture.event+`]}`)
		case r.Method == http.MethodPost && r.URL.Path == "/calendar/events.json",
			r.Method == http.MethodPatch && r.URL.Path == "/calendar/events/4821.json":
			requests.writes.Add(1)
			form := eventForm(t, r)
			requests.mu.Lock()
			requests.form = form
			requests.mu.Unlock()
			w.WriteHeader(http.StatusCreated)
			_, _ = io.WriteString(w, `{"id":4821,"title":"Dentist appointment"}`)
		default:
			t.Errorf("unexpected request = %s %s", r.Method, r.URL)
			http.NotFound(w, r)
		}
	}), requests
}

// atInstant pins the clock an event's default date is read from.
func atInstant(t *testing.T, instant string) {
	t.Helper()
	at, err := time.Parse(time.RFC3339, instant)
	if err != nil {
		t.Fatalf("instant %q: %v", instant, err)
	}
	previous := eventNow
	eventNow = func() time.Time { return at }
	t.Cleanup(func() { eventNow = previous })
}

// wantSchedule checks the schedule an event write sent. An empty zone says the write must be
// zoneless: HEY is told to set no zone, and none is named.
func wantSchedule(t *testing.T, form url.Values, startsAt, startTime, endsAt, endTime, zone string) {
	t.Helper()
	want := map[string]string{
		"calendar_event[all_day]":        "0",
		"calendar_event[starts_at]":      startsAt,
		"calendar_event[starts_at_time]": startTime + ":00",
		"calendar_event[ends_at]":        endsAt,
		"calendar_event[ends_at_time]":   endTime + ":00",
	}
	if zone == "" {
		want["calendar_event[set_time_zone]"] = "0"
		for _, field := range []string{"calendar_event[starts_at_time_zone_name]", "calendar_event[ends_at_time_zone_name]"} {
			if form.Has(field) {
				t.Errorf("%s = %q, want no zone", field, form.Get(field))
			}
		}
	} else {
		want["calendar_event[set_time_zone]"] = "1"
		want["calendar_event[starts_at_time_zone_name]"] = zone
		want["calendar_event[ends_at_time_zone_name]"] = zone
	}
	for field, value := range want {
		if got := form.Get(field); got != value {
			t.Errorf("%s = %q, want %q", field, got, value)
		}
	}
}

// The reported case: a laptop whose zone Go calls "Local" sent no zone, and HEY read 10:00
// as UTC — 06:00 in New York. The time is read in the account's zone and the zone is named,
// whatever this machine's is.
func TestEventsAddReadsTimesInTheAccountZone(t *testing.T) {
	for _, zone := range []string{"America/New_York", "Asia/Kolkata", "UTC"} {
		t.Run(zone, func(t *testing.T) {
			handler, requests := zoneServer(t, zoneFixture{accountZone: zone})
			_, err := runJSONCommand(t, handler, "event", "add", "Dentist appointment", "--calendar", "9",
				"--starts-on", "2026-10-14", "--start-time", "10:00", "--end-time", "10:45")
			if err != nil {
				t.Fatalf("execute event add: %v", err)
			}
			wantSchedule(t, requests.written(t), "2026-10-14", "10:00", "2026-10-14", "10:45", zone)
			if got := requests.identity.Load(); got != 1 {
				t.Errorf("identity reads = %d, want one", got)
			}
		})
	}
}

// --time-zone names the zone outright, and then there is nothing to ask the account.
func TestEventsAddTimeZoneFlagNeedsNoAccountRead(t *testing.T) {
	handler, requests := zoneServer(t, zoneFixture{accountZone: "America/New_York"})
	_, err := runJSONCommand(t, handler, "event", "add", "Coffee with Maria", "--calendar", "9",
		"--starts-on", "2026-10-14", "--start-time", "09:30", "--time-zone", "Europe/Lisbon")
	if err != nil {
		t.Fatalf("execute event add: %v", err)
	}
	wantSchedule(t, requests.written(t), "2026-10-14", "09:30", "2026-10-14", "10:30", "Europe/Lisbon")
	if got := requests.identity.Load(); got != 0 {
		t.Errorf("identity reads = %d, want none", got)
	}
}

// Today is the account's today for an all-day event as well, whatever the machine's zone: at
// 02:00 UTC it is still the 14th in New York. A named date needs no zone and asks the
// account nothing, and --time-zone's today needs no account either.
func TestEventsAddAllDayTodayIsTheAccountsToday(t *testing.T) {
	tests := []struct {
		name     string
		args     []string
		want     string
		identity int32
	}{
		{name: "no date", want: "2026-10-14", identity: 1},
		{name: "named date", args: []string{"--starts-on", "2026-10-20"}, want: "2026-10-20"},
		{name: "time zone", args: []string{"--time-zone", "Asia/Tokyo"}, want: "2026-10-15"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			atInstant(t, "2026-10-15T02:00:00Z")
			handler, requests := zoneServer(t, zoneFixture{accountZone: "America/New_York"})
			_, err := runJSONCommand(t, handler, append([]string{"event", "add", "Sarah's birthday", "--calendar", "9"}, tt.args...)...)
			if err != nil {
				t.Fatalf("execute event add: %v", err)
			}
			form := requests.written(t)
			if got := form.Get("calendar_event[starts_at]"); got != tt.want {
				t.Errorf("starts_at = %q, want %s", got, tt.want)
			}
			if got := form.Get("calendar_event[all_day]"); got != "1" {
				t.Errorf("all_day = %q, want 1", got)
			}
			if form.Has("calendar_event[starts_at_time_zone_name]") {
				t.Errorf("zone = %q, want none on an all-day event", form.Get("calendar_event[starts_at_time_zone_name]"))
			}
			if got := requests.identity.Load(); got != tt.identity {
				t.Errorf("identity reads = %d, want %d", got, tt.identity)
			}
		})
	}
}

// With no zone to read a time in, the write is refused and says how to name one — never
// sent as UTC or in the machine's zone.
func TestEventsAddRefusesWithoutAnAccountZone(t *testing.T) {
	tests := []struct {
		name    string
		fixture zoneFixture
		want    string
	}{
		{name: "account names none", fixture: zoneFixture{}, want: "your HEY account has no time zone set"},
		{name: "zone this build does not know", fixture: zoneFixture{accountZone: "Mars/Olympus_Mons"}, want: "Mars/Olympus_Mons is not one this build of hey knows"},
		{name: "server error", fixture: zoneFixture{identityStatus: http.StatusInternalServerError}, want: "could not be read"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			handler, requests := zoneServer(t, tt.fixture)
			_, err := runJSONCommand(t, handler, "event", "add", "Dentist appointment", "--calendar", "9",
				"--starts-on", "2026-10-14", "--start-time", "10:00")
			wantNoZoneRefusal(t, err, tt.want)
			if got := requests.writes.Load(); got != 0 {
				t.Errorf("writes = %d, want none", got)
			}
		})
	}
}

// A login HEY rejects while the account's zone is read is an auth failure, as it would be
// on the write itself, rather than a zone refusal whose --time-zone hint would not help.
func TestEventsAddReportsAnAuthFailureReadingTheAccountAsAuth(t *testing.T) {
	handler, requests := zoneServer(t, zoneFixture{identityStatus: http.StatusUnauthorized})
	_, err := runJSONCommand(t, handler, "event", "add", "Dentist appointment", "--calendar", "9",
		"--starts-on", "2026-10-14", "--start-time", "10:00")
	var cliErr *apierr.Error
	if !errors.As(err, &cliErr) || cliErr.Code != apierr.CodeAuth {
		t.Fatalf("error = %v, want an auth error", err)
	}
	if strings.Contains(cliErr.Hint, "--time-zone") {
		t.Errorf("hint = %q, want no --time-zone advice for an auth failure", cliErr.Hint)
	}
	if got := requests.writes.Load(); got != 0 {
		t.Errorf("writes = %d, want none", got)
	}
}

func wantNoZoneRefusal(t *testing.T, err error, want string) {
	t.Helper()
	var cliErr *apierr.Error
	if !errors.As(err, &cliErr) || cliErr.Code != apierr.CodeUsage {
		t.Fatalf("error = %v, want a usage error", err)
	}
	if !strings.Contains(cliErr.Message, want) {
		t.Errorf("message = %q, want it to say %q", cliErr.Message, want)
	}
	if !strings.Contains(cliErr.Hint, "--time-zone") {
		t.Errorf("hint = %q, want it to name --time-zone", cliErr.Hint)
	}
}

// A network failure reading the identity is refused the same way, and nothing is written.
func TestEventsAddRefusesWhenTheAccountCannotBeReached(t *testing.T) {
	base, requests := zoneServer(t, zoneFixture{accountZone: "America/New_York"})
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/identity.json" {
			requests.identity.Add(1)
			conn, _, err := w.(http.Hijacker).Hijack()
			if err == nil {
				_ = conn.Close()
			}
			return
		}
		base.ServeHTTP(w, r)
	})
	_, err := runJSONCommand(t, handler, "event", "add", "Dentist appointment", "--calendar", "9",
		"--starts-on", "2026-10-14", "--start-time", "10:00")
	wantNoZoneRefusal(t, err, "could not be read")
	if got := requests.writes.Load(); got != 0 {
		t.Errorf("writes = %d, want none", got)
	}
}

// HEY stores a zone name it cannot look up and reads the event as UTC, so only an IANA name
// HEY will find gets through, and anything else is refused before anything is read. Rails'
// friendly names are among the refused: HEY would find those, but the CLI takes IANA names
// alone.
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

// Links and the zones that are not places are names HEY looks up as they are, so they go
// out spelled the way they came in.
func TestEventsAddSendsAZoneAliasAsGiven(t *testing.T) {
	for _, zone := range []string{"US/Eastern", "Etc/GMT+5", "UTC", "EST5EDT"} {
		t.Run(zone, func(t *testing.T) {
			handler, requests := zoneServer(t, zoneFixture{})
			_, err := runJSONCommand(t, handler, "event", "add", "Dentist appointment", "--calendar", "9",
				"--starts-on", "2026-10-14", "--start-time", "10:00", "--time-zone", zone)
			if err != nil {
				t.Fatalf("execute event add: %v", err)
			}
			wantSchedule(t, requests.written(t), "2026-10-14", "10:00", "2026-10-14", "11:00", zone)
		})
	}
}

// An event after the clocks change is still written in the zone, by name: an offset taken
// today would put a December event an hour out.
func TestEventsAddNamesTheZoneAcrossAClockChange(t *testing.T) {
	atInstant(t, "2026-07-01T12:00:00Z")
	handler, requests := zoneServer(t, zoneFixture{accountZone: "America/New_York"})
	_, err := runJSONCommand(t, handler, "event", "add", "Dentist appointment", "--calendar", "9",
		"--starts-on", "2026-12-15", "--start-time", "10:00")
	if err != nil {
		t.Fatalf("execute event add: %v", err)
	}
	wantSchedule(t, requests.written(t), "2026-12-15", "10:00", "2026-12-15", "11:00", "America/New_York")
}

// Today is the account's today: at 02:00 UTC it is still the evening before in New York,
// and already the morning after in Kolkata at 20:00 UTC.
func TestEventsAddDefaultsToTodayInTheAccountZone(t *testing.T) {
	tests := []struct{ now, zone, want string }{
		{now: "2026-10-15T02:00:00Z", zone: "America/New_York", want: "2026-10-14"},
		{now: "2026-10-14T20:00:00Z", zone: "Asia/Kolkata", want: "2026-10-15"},
	}
	for _, tt := range tests {
		t.Run(tt.zone, func(t *testing.T) {
			atInstant(t, tt.now)
			handler, requests := zoneServer(t, zoneFixture{accountZone: tt.zone})
			_, err := runJSONCommand(t, handler, "event", "add", "Dentist appointment", "--calendar", "9", "--start-time", "10:00")
			if err != nil {
				t.Fatalf("execute event add: %v", err)
			}
			wantSchedule(t, requests.written(t), tt.want, "10:00", tt.want, "11:00", tt.zone)
		})
	}
}

// An edit reads the event it changes: every one of these is on calendar 9, found by the
// day it starts on.
func runZoneEdit(t *testing.T, fixture zoneFixture, args ...string) *zoneRequests {
	t.Helper()
	handler, requests := zoneServer(t, fixture)
	_, err := runJSONCommand(t, handler, append([]string{"event", "edit", "4821", "2026-10-14", "--calendar", "9"}, args...)...)
	if err != nil {
		t.Fatalf("execute event edit: %v", err)
	}
	return requests
}

const (
	zonedEventJSON = `{"id":4821,"title":"Dentist appointment","starts_at":"2026-10-14T08:00:00Z","ends_at":"2026-10-14T09:00:00Z",` +
		`"starts_at_time_zone":"Europe/Zagreb","ends_at_time_zone":"Europe/Zagreb"}`
	// 14:00Z is 10:00 in New York; a zoneless event is only ever an instant.
	zonelessEventJSON = `{"id":4821,"title":"Dentist appointment","starts_at":"2026-10-14T14:00:00Z","ends_at":"2026-10-14T15:00:00Z"}`
	allDayEventJSON   = `{"id":4821,"title":"Dentist appointment","all_day":true,"starts_at":"2026-10-14T00:00:00Z","ends_at":"2026-10-14T00:00:00Z"}`
)

// A start and an end in zones of their own each keep theirs. A flight leaves New York and
// lands in London; and where only one end was given a zone, HEY serves the other as Etc/UTC
// — the zone its JSON reads in — rather than leaving it out, so there is never a zoned end
// beside a missing one to fill in. A title-only edit sends each end back in its own zone.
func TestEventsEditKeepsEachEndsOwnZone(t *testing.T) {
	tests := []struct {
		name                       string
		event                      string
		startTime, endsAt, endTime string
		startZone, endZone         string
	}{
		{
			name: "flight",
			event: `{"id":4821,"title":"Flight to London","starts_at":"2026-10-14T22:00:00Z","ends_at":"2026-10-15T05:00:00Z",` +
				`"starts_at_time_zone":"America/New_York","ends_at_time_zone":"Europe/London"}`,
			startTime: "18:00", endsAt: "2026-10-15", endTime: "06:00",
			startZone: "America/New_York", endZone: "Europe/London",
		},
		{
			name: "one end zoned",
			event: `{"id":4821,"title":"Dentist appointment","starts_at":"2026-10-14T14:00:00Z","ends_at":"2026-10-14T15:00:00Z",` +
				`"starts_at_time_zone":"America/New_York","ends_at_time_zone":"Etc/UTC"}`,
			startTime: "10:00", endsAt: "2026-10-14", endTime: "15:00",
			startZone: "America/New_York", endZone: "Etc/UTC",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			requests := runZoneEdit(t, zoneFixture{accountZone: "Asia/Tokyo", event: tt.event}, "--title", "Renamed")
			form := requests.written(t)
			want := map[string]string{
				"calendar_event[set_time_zone]":            "1",
				"calendar_event[starts_at]":                "2026-10-14",
				"calendar_event[starts_at_time]":           tt.startTime + ":00",
				"calendar_event[starts_at_time_zone_name]": tt.startZone,
				"calendar_event[ends_at]":                  tt.endsAt,
				"calendar_event[ends_at_time]":             tt.endTime + ":00",
				"calendar_event[ends_at_time_zone_name]":   tt.endZone,
			}
			for field, value := range want {
				if got := form.Get(field); got != value {
					t.Errorf("%s = %q, want %q", field, got, value)
				}
			}
			if got := requests.identity.Load(); got != 0 {
				t.Errorf("identity reads = %d, want none", got)
			}
		})
	}
}

// A zoned event keeps its zone, for the time typed and for the one kept, and asks the
// account nothing.
func TestEventsEditZonedEventKeepsItsZone(t *testing.T) {
	requests := runZoneEdit(t, zoneFixture{accountZone: "America/New_York", event: zonedEventJSON}, "--start-time", "09:30")
	wantSchedule(t, requests.written(t), "2026-10-14", "09:30", "2026-10-14", "11:00", "Europe/Zagreb")
	if got := requests.identity.Load(); got != 0 {
		t.Errorf("identity reads = %d, want none", got)
	}
}

// A zoned event in a zone this build cannot load is refused rather than read as UTC.
func TestEventsEditRefusesAStoredZoneItCannotLoad(t *testing.T) {
	handler, requests := zoneServer(t, zoneFixture{accountZone: "America/New_York",
		event: strings.ReplaceAll(zonedEventJSON, "Europe/Zagreb", "Mars/Olympus_Mons")})
	_, err := runJSONCommand(t, handler, "event", "edit", "4821", "2026-10-14", "--calendar", "9", "--title", "Dentist appointment (moved)")
	var cliErr *apierr.Error
	if !errors.As(err, &cliErr) || cliErr.Code != apierr.CodeUsage || !strings.Contains(cliErr.Message, "Mars/Olympus_Mons") {
		t.Fatalf("error = %v, want a usage error naming the zone", err)
	}
	if got := requests.writes.Load(); got != 0 {
		t.Errorf("writes = %d, want none", got)
	}
}

// A title-only edit of a zoneless event sends its instants back as they were, zoneless, and
// needs no zone to do it.
func TestEventsEditZonelessEventTitleOnly(t *testing.T) {
	requests := runZoneEdit(t, zoneFixture{event: zonelessEventJSON}, "--title", "Dentist appointment (moved)")
	wantSchedule(t, requests.written(t), "2026-10-14", "14:00", "2026-10-14", "15:00", "")
	if got := requests.identity.Load(); got != 0 {
		t.Errorf("identity reads = %d, want none", got)
	}
}

// A typed time on a zoneless event is read in the account's zone and lands on that instant,
// still zoneless: 21:30 in New York on the 14th is 01:30 UTC on the 15th.
func TestEventsEditZonelessEventReadsTypedTimesInTheAccountZone(t *testing.T) {
	requests := runZoneEdit(t, zoneFixture{accountZone: "America/New_York", event: zonelessEventJSON},
		"--start-time", "09:00", "--end-time", "21:30")
	wantSchedule(t, requests.written(t), "2026-10-14", "13:00", "2026-10-15", "01:30", "")
	if got := requests.identity.Load(); got != 1 {
		t.Errorf("identity reads = %d, want one", got)
	}
}

// A typed time the clocks skip is placed as HEY places one, on the first time that exists:
// 02:30 on the morning New York springs forward is 03:30 EDT, 07:30 UTC — not 01:30 EST,
// where Go's own parse would put it, an hour earlier than the same edit made in HEY.
func TestEventsEditZonelessEventPlacesASkippedTimeAsHEYDoes(t *testing.T) {
	requests := runZoneEdit(t, zoneFixture{accountZone: "America/New_York", event: zonelessEventJSON},
		"--starts-on", "2026-03-08", "--start-time", "02:30", "--ends-on", "2026-03-08", "--end-time", "04:00")
	wantSchedule(t, requests.written(t), "2026-03-08", "07:30", "2026-03-08", "08:00", "")
}

// A zoneless repeating series given a new end time keeps its start exactly: HEY expands the
// series from that instant, so moving it would move every occurrence. The start here is in
// the hour New York repeats as the clocks go back, which a read and rewrite of the wall
// clock could land on either side of.
func TestEventsEditZonelessSeriesKeepsTheStartItWasNotGiven(t *testing.T) {
	atInstant(t, "2026-07-01T12:00:00Z")
	series := `{"id":4821,"title":"Night shift handover","recurring":true,"starts_at":"2026-11-01T05:30:00Z","ends_at":"2026-11-01T06:30:00Z",` +
		`"recurrence_schedule":{"kind":"every_week","preset":true}}`
	handler, requests := zoneServer(t, zoneFixture{accountZone: "America/New_York", event: series})
	_, err := runJSONCommand(t, handler, "event", "edit", "4821", "2026-11-01", "--calendar", "9", "--end-time", "03:00")
	if err != nil {
		t.Fatalf("execute event edit: %v", err)
	}
	// 03:00 on the 1st in New York is after the clocks went back: 08:00 UTC.
	wantSchedule(t, requests.written(t), "2026-11-01", "05:30", "2026-11-01", "08:00", "")
}

// --time-zone gives a zoneless event that zone, and the times not typed keep their instant.
func TestEventsEditTimeZoneFlagKeepsTheInstantsItWasNotGiven(t *testing.T) {
	tests := []struct {
		name, event, want string
	}{
		{name: "zoneless", event: zonelessEventJSON, want: "10:00"},
		{name: "zoned", event: zonedEventJSON, want: "04:00"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			requests := runZoneEdit(t, zoneFixture{event: tt.event}, "--time-zone", "America/New_York")
			start, _ := time.Parse(clockLayout, tt.want)
			wantSchedule(t, requests.written(t), "2026-10-14", tt.want, "2026-10-14", start.Add(time.Hour).Format(clockLayout), "America/New_York")
			if got := requests.identity.Load(); got != 0 {
				t.Errorf("identity reads = %d, want none", got)
			}
		})
	}
}

// An all-day event given a time becomes a timed one in the account's zone, as a new event
// would, or in the zone --time-zone names.
func TestEventsEditAllDayEventGivenATimeTakesAZone(t *testing.T) {
	requests := runZoneEdit(t, zoneFixture{accountZone: "America/New_York", event: allDayEventJSON}, "--start-time", "10:00")
	wantSchedule(t, requests.written(t), "2026-10-14", "10:00", "2026-10-14", "11:00", "America/New_York")
	if got := requests.identity.Load(); got != 1 {
		t.Errorf("identity reads = %d, want one", got)
	}

	requests = runZoneEdit(t, zoneFixture{accountZone: "America/New_York", event: allDayEventJSON}, "--start-time", "10:00", "--time-zone", "Europe/Lisbon")
	wantSchedule(t, requests.written(t), "2026-10-14", "10:00", "2026-10-14", "11:00", "Europe/Lisbon")
	if got := requests.identity.Load(); got != 0 {
		t.Errorf("identity reads = %d, want none with --time-zone", got)
	}
}

// A zoneless event made all-day falls on the account's date: 03:00 UTC on the 15th is the
// evening of the 14th in New York.
func TestEventsEditZonelessEventMadeAllDayTakesTheAccountsDate(t *testing.T) {
	event := `{"id":4821,"title":"Dentist appointment","starts_at":"2026-10-15T03:00:00Z","ends_at":"2026-10-15T03:30:00Z"}`
	requests := runZoneEdit(t, zoneFixture{accountZone: "America/New_York", event: event}, "--all-day")
	form := requests.written(t)
	if got := form.Get("calendar_event[starts_at]"); got != "2026-10-14" {
		t.Errorf("starts_at = %q, want the account's date", got)
	}
	if got := form.Get("calendar_event[all_day]"); got != "1" {
		t.Errorf("all_day = %q, want 1", got)
	}
}

// A zoneless edit that needs a zone and cannot get one is refused, and nothing is written.
func TestEventsEditRefusesWithoutAnAccountZone(t *testing.T) {
	handler, requests := zoneServer(t, zoneFixture{event: zonelessEventJSON})
	_, err := runJSONCommand(t, handler, "event", "edit", "4821", "2026-10-14", "--calendar", "9", "--start-time", "11:00")
	wantNoZoneRefusal(t, err, "your HEY account has no time zone set")
	if got := requests.writes.Load(); got != 0 {
		t.Errorf("writes = %d, want none", got)
	}
}

// One day of a zoneless series follows the same rules as the whole event: a typed time is
// read in the account's zone and sent as UTC, zoneless, whether the edit reaches that day
// alone or every day from it on.
func TestEventsEditOccurrenceOfAZonelessSeriesReadsTypedTimesInTheAccountZone(t *testing.T) {
	series := strings.ReplaceAll(occurrenceSeriesJSON, `"starts_at_time_zone":"Europe/Zagreb","ends_at_time_zone":"Europe/Zagreb",`, "")
	for _, applyTo := range []string{"current", "future"} {
		t.Run(applyTo, func(t *testing.T) {
			var identityReads atomic.Int32
			base, writes := occurrenceServer(t, "2026-09-15",
				`{"Calendar::Event":[`+series+`]}`,
				`{"Calendar::Countdown":[`+occurrenceCountdownJSON+`]}`,
				func(t *testing.T, form url.Values) {
					wantSchedule(t, form, "2026-09-15", "13:00", "2026-09-15", "14:30", "")
				})
			handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path == "/identity.json" {
					identityReads.Add(1)
					w.Header().Set("Content-Type", "application/json")
					_, _ = io.WriteString(w, `{"id":1,"time_zone":"America/New_York"}`)
					return
				}
				base.ServeHTTP(w, r)
			})
			args := []string{"event", "edit", "4821", "--occurrence", "4821_2026-09-15", "--apply-to", applyTo,
				"--start-time", "09:00", "--end-time", "10:30", "--allow-plain-notes"}
			if applyTo == "future" {
				args = append(args, "--repeat", "every_week")
			}
			if _, err := runJSONCommand(t, handler, args...); err != nil {
				t.Fatalf("execute occurrence edit: %v", err)
			}
			if writes.Load() != 1 {
				t.Errorf("writes = %d, want one", writes.Load())
			}
			// A future edit reads the account for the countdown as well: one read serves both.
			if got := identityReads.Load(); got != 1 {
				t.Errorf("identity reads = %d, want one", got)
			}
		})
	}
}

// One day of a zoned series keeps the series' zone, as the whole event does.
func TestEventsEditOccurrenceOfAZonedSeriesKeepsItsZone(t *testing.T) {
	handler, writes := occurrenceServer(t, "2026-09-15",
		`{"Calendar::Event":[`+occurrenceSeriesJSON+`]}`,
		"",
		func(t *testing.T, form url.Values) {
			wantSchedule(t, form, "2026-09-15", "14:30", "2026-09-15", "15:00", "Europe/Zagreb")
		})
	if _, err := runJSONCommand(t, handler, "event", "edit", "4821", "--occurrence", "4821_2026-09-15", "--apply-to", "current",
		"--start-time", "14:30", "--allow-plain-notes"); err != nil {
		t.Fatalf("execute occurrence edit: %v", err)
	}
	if writes.Load() != 1 {
		t.Errorf("writes = %d, want one", writes.Load())
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

// A clock time is placed where HEY places it. These are what ActiveSupport answers for
// Time.zone.parse(clock).change(zone:), the way HEY reads a typed time: a time the clocks
// skip moves an hour on, and a time they repeat is the daylight-saving one of the two, or
// the later where neither keeps daylight saving — Almaty, Volgograd and Moscow going back an
// hour for good. Go's time.Date picks by rules of its own and gets some of each wrong.
func TestWallClockOnPlacesAClockTimeAsHEYDoes(t *testing.T) {
	tests := []struct{ name, zone, day, clock, want string }{
		{name: "Zagreb repeats 02:30", zone: "Europe/Zagreb", day: "2026-10-25", clock: "02:30", want: "2026-10-25T00:30:00Z"},
		{name: "New York repeats 01:30", zone: "America/New_York", day: "2026-11-01", clock: "01:30", want: "2026-11-01T05:30:00Z"},
		{name: "New York skips 02:30", zone: "America/New_York", day: "2026-03-08", clock: "02:30", want: "2026-03-08T07:30:00Z"},
		{name: "Lord Howe repeats 01:45", zone: "Australia/Lord_Howe", day: "2026-04-05", clock: "01:45", want: "2026-04-04T14:45:00Z"},
		{name: "Lord Howe skips 02:15", zone: "Australia/Lord_Howe", day: "2026-10-04", clock: "02:15", want: "2026-10-03T16:15:00Z"},
		{name: "Samoa skips 30 December 2011, noon", zone: "Pacific/Apia", day: "2011-12-30", clock: "12:00", want: "2011-12-30T10:00:00Z"},
		{name: "Samoa skips 30 December 2011, midnight", zone: "Pacific/Apia", day: "2011-12-30", clock: "00:00", want: "2011-12-30T10:00:00Z"},
		{name: "Samoa skips 30 December 2011, 23:59", zone: "Pacific/Apia", day: "2011-12-30", clock: "23:59", want: "2011-12-30T10:59:00Z"},
		{name: "an ordinary day", zone: "America/New_York", day: "2026-06-10", clock: "10:00", want: "2026-06-10T14:00:00Z"},
		{name: "Almaty repeats 23:30 for good", zone: "Asia/Almaty", day: "2024-02-29", clock: "23:30", want: "2024-02-29T18:30:00Z"},
		{name: "Volgograd repeats 01:30 for good", zone: "Europe/Volgograd", day: "2020-12-27", clock: "01:30", want: "2020-12-26T22:30:00Z"},
		{name: "Moscow repeats 01:30 for good", zone: "Europe/Moscow", day: "2014-10-26", clock: "01:30", want: "2014-10-25T22:30:00Z"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			loc, err := time.LoadLocation(tt.zone)
			if err != nil {
				t.Fatal(err)
			}
			day, _ := time.Parse(dateLayout, tt.day)
			clock, _ := time.Parse(clockLayout, tt.clock)
			if got := wallClockOn(day, clock, loc).UTC().Format(time.RFC3339); got != tt.want {
				t.Errorf("wallClockOn(%s %s) = %s, want %s", tt.day, tt.clock, got, tt.want)
			}
		})
	}
}

// A time typed on a zoneless event in the hour the clocks repeat is the first of the two,
// as HEY would read it: 02:30 on the night Zagreb falls back is 00:30 UTC, not 01:30.
func TestEventsEditZonelessEventPlacesARepeatedTimeAsHEYDoes(t *testing.T) {
	requests := runZoneEdit(t, zoneFixture{accountZone: "Europe/Zagreb", event: zonelessEventJSON},
		"--starts-on", "2026-10-25", "--start-time", "02:30", "--ends-on", "2026-10-25", "--end-time", "04:00")
	wantSchedule(t, requests.written(t), "2026-10-25", "00:30", "2026-10-25", "03:00", "")
}

// HEY is sent a clock time and a zone, and takes the first of the two moments a clock time
// names as the clocks go back. An end kept at the second one cannot be sent back as it is,
// so the edit is refused rather than moving it an hour; one at the first goes through.
func TestEventsEditRefusesToMoveAKeptTimeOutOfTheRepeatedHour(t *testing.T) {
	// 05:30Z is the first 01:30 in New York on 1 November, 06:30Z the second.
	zoneless := func(start, end string) string {
		return `{"id":4821,"title":"Night shift handover","starts_at":"` + start + `","ends_at":"` + end + `"}`
	}
	zoned := func(start, end string) string {
		return `{"id":4821,"title":"Night shift handover","starts_at":"` + start + `","ends_at":"` + end + `",` +
			`"starts_at_time_zone":"America/New_York","ends_at_time_zone":"America/New_York"}`
	}
	tests := []struct {
		name  string
		event string
		args  []string
		want  string // the start sent, or empty for a refusal
	}{
		{name: "zoneless given a zone, first 01:30", event: zoneless("2026-11-01T05:30:00Z", "2026-11-01T07:30:00Z"),
			args: []string{"--time-zone", "America/New_York"}, want: "01:30"},
		{name: "zoneless given a zone, second 01:30", event: zoneless("2026-11-01T06:30:00Z", "2026-11-01T07:30:00Z"),
			args: []string{"--time-zone", "America/New_York"}},
		{name: "zoned, first 01:30", event: zoned("2026-11-01T05:30:00Z", "2026-11-01T07:30:00Z"),
			args: []string{"--title", "Night shift handover (Sam)"}, want: "01:30"},
		{name: "zoned, second 01:30", event: zoned("2026-11-01T06:30:00Z", "2026-11-01T07:30:00Z"),
			args: []string{"--title", "Night shift handover (Sam)"}},
		{name: "zoned, second 01:30 retyped", event: zoned("2026-11-01T06:30:00Z", "2026-11-01T07:30:00Z"),
			args: []string{"--start-time", "01:30"}, want: "01:30"},
		{name: "zoned, end at the second 01:30", event: zoned("2026-11-01T04:30:00Z", "2026-11-01T06:30:00Z"),
			args: []string{"--title", "Night shift handover (Sam)"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			handler, requests := zoneServer(t, zoneFixture{accountZone: "America/New_York", event: tt.event})
			_, err := runJSONCommand(t, handler, append([]string{"event", "edit", "4821", "2026-11-01", "--calendar", "9"}, tt.args...)...)
			if tt.want == "" {
				var cliErr *apierr.Error
				if !errors.As(err, &cliErr) || cliErr.Code != apierr.CodeUsage || !strings.Contains(cliErr.Message, "clocks repeat") {
					t.Fatalf("error = %v, want a usage error about the repeated hour", err)
				}
				if got := requests.writes.Load(); got != 0 {
					t.Errorf("writes = %d, want none", got)
				}
				return
			}
			if err != nil {
				t.Fatalf("execute event edit: %v", err)
			}
			if got := requests.written(t).Get("calendar_event[starts_at_time]"); got != tt.want+":00" {
				t.Errorf("starts_at_time = %q, want %s", got, tt.want)
			}
		})
	}
}

// One day of a series goes through the same check: a day HEY wrote out at the second 02:30
// of the night Zagreb falls back is refused rather than moved an hour earlier.
func TestEventsEditOccurrenceRefusesToMoveADayOutOfTheRepeatedHour(t *testing.T) {
	realized := `{"id":9001,"type":"Calendar::Event","parent_id":4821,"occurrence_id":"4821_2026-10-27",` +
		`"title":"Design review","starts_at":"2026-10-25T01:30:00Z","ends_at":"2026-10-25T02:30:00Z",` +
		`"starts_at_time_zone":"Europe/Zagreb","ends_at_time_zone":"Europe/Zagreb","calendar":{"id":9,"name":"Work"}}`
	handler, writes := occurrenceServer(t, "2026-10-27",
		`{"Calendar::Event":[`+occurrenceSeriesJSON+`,`+realized+`]}`,
		"",
		func(t *testing.T, form url.Values) { t.Error("wrote a day an hour away from where it was") })
	_, err := runJSONCommand(t, handler, "event", "edit", "4821", "--occurrence", "4821_2026-10-27", "--apply-to", "current",
		"--title", "Design review (moved)")
	var cliErr *apierr.Error
	if !errors.As(err, &cliErr) || cliErr.Code != apierr.CodeUsage || !strings.Contains(cliErr.Message, "clocks repeat") {
		t.Fatalf("error = %v, want a usage error about the repeated hour", err)
	}
	if writes.Load() != 0 {
		t.Errorf("writes = %d, want none", writes.Load())
	}
}

// The event command reads the identity once for the account's zone. --account adds a read
// of its own before the command runs, the SDK's check that the account is one the identity
// has, so a timed add reads it twice with --account and once without.
func TestEventsAddReadsTheIdentityOnceItself(t *testing.T) {
	for _, tt := range []struct {
		name string
		args []string
		want int32
	}{
		{name: "without --account", want: 1},
		{name: "with --account", args: []string{"--account", "2"}, want: 2},
	} {
		t.Run(tt.name, func(t *testing.T) {
			var identityReads, writes atomic.Int32
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				switch {
				case r.Method == http.MethodGet && r.URL.Path == "/identity.json":
					identityReads.Add(1)
					_, _ = io.WriteString(w, `{"id":1,"time_zone":"America/New_York",`+
						`"accounts":[{"id":1,"name":"Personal","purpose":"home","status":"active"},{"id":2,"name":"Work","purpose":"work","status":"active"}],`+
						`"all_users":[{"id":22,"account_id":2}],"senders":[{"id":222,"account_id":2,"default":true}]}`)
				case r.Method == http.MethodPost && r.URL.Path == "/calendar/events.json":
					writes.Add(1)
					wantSchedule(t, eventForm(t, r), "2026-10-14", "10:00", "2026-10-14", "11:00", "America/New_York")
					w.WriteHeader(http.StatusCreated)
					_, _ = io.WriteString(w, `{"id":4821,"title":"Dentist appointment"}`)
				default:
					t.Errorf("unexpected request = %s %s", r.Method, r.URL)
					http.NotFound(w, r)
				}
			}))
			t.Cleanup(server.Close)

			args := append(append([]string{}, tt.args...), "event", "add", "Dentist appointment", "--calendar", "9",
				"--starts-on", "2026-10-14", "--start-time", "10:00")
			if _, err := runAccountsCLI(t, server, args...); err != nil {
				t.Fatalf("execute event add: %v", err)
			}
			if got := identityReads.Load(); got != tt.want {
				t.Errorf("identity reads = %d, want %d", got, tt.want)
			}
			if got := writes.Load(); got != 1 {
				t.Errorf("writes = %d, want one", got)
			}
		})
	}
}

// Where neither side of a repeated hour keeps daylight saving, HEY takes the later moment:
// Almaty's 23:30 on 29 February 2024 is 18:30Z. An event kept there goes back as it is, and
// one at the earlier 23:30, 17:30Z, is refused rather than moved an hour later.
func TestEventsEditKeepsATimeHEYTakesTheLaterOf(t *testing.T) {
	for _, tt := range []struct {
		start string
		kept  bool
	}{
		{start: "2024-02-29T18:30:00Z", kept: true},
		{start: "2024-02-29T17:30:00Z"},
	} {
		t.Run(tt.start, func(t *testing.T) {
			event := `{"id":4821,"title":"Late call with Aigerim","starts_at":"` + tt.start + `","ends_at":"2024-02-29T20:00:00Z",` +
				`"starts_at_time_zone":"Asia/Almaty","ends_at_time_zone":"Asia/Almaty"}`
			handler, requests := zoneServer(t, zoneFixture{event: event})
			_, err := runJSONCommand(t, handler, "event", "edit", "4821", "2024-02-29", "--calendar", "9", "--title", "Late call with Aigerim (moved)")
			if !tt.kept {
				var cliErr *apierr.Error
				if !errors.As(err, &cliErr) || cliErr.Code != apierr.CodeUsage || !strings.Contains(cliErr.Message, "clocks repeat") {
					t.Fatalf("error = %v, want a usage error about the repeated hour", err)
				}
				if got := requests.writes.Load(); got != 0 {
					t.Errorf("writes = %d, want none", got)
				}
				return
			}
			if err != nil {
				t.Fatalf("execute event edit: %v", err)
			}
			wantSchedule(t, requests.written(t), "2024-02-29", "23:30", "2024-03-01", "01:00", "Asia/Almaty")
		})
	}
}

// HEY checks that a timed event does not end before it starts on the instants, not the
// dates: a flight leaving Tokyo at 09:00 on the 15th lands in Los Angeles at 18:00 on the
// 14th, an hour later. Renaming it sends both ends back as they are.
func TestEventsEditKeepsAnEventThatEndsOnAnEarlierDateElsewhere(t *testing.T) {
	flight := `{"id":4821,"title":"Flight to Los Angeles","starts_at":"2026-10-15T00:00:00Z","ends_at":"2026-10-15T01:00:00Z",` +
		`"starts_at_time_zone":"Asia/Tokyo","ends_at_time_zone":"America/Los_Angeles"}`
	requests := runZoneEdit(t, zoneFixture{event: flight}, "--title", "Flight to Los Angeles (JL62)")
	form := requests.written(t)
	for field, want := range map[string]string{
		"calendar_event[starts_at]":                "2026-10-15",
		"calendar_event[starts_at_time]":           "09:00:00",
		"calendar_event[starts_at_time_zone_name]": "Asia/Tokyo",
		"calendar_event[ends_at]":                  "2026-10-14",
		"calendar_event[ends_at_time]":             "18:00:00",
		"calendar_event[ends_at_time_zone_name]":   "America/Los_Angeles",
	} {
		if got := form.Get(field); got != want {
			t.Errorf("%s = %q, want %q", field, got, want)
		}
	}
}

// An event that would end before it starts is refused before it is written: a timed one on
// its instants, an all-day one on its dates, and one day of a series the same way.
func TestEventsEditRefusesAnEventThatEndsBeforeItStarts(t *testing.T) {
	tests := []struct {
		name  string
		event string
		args  []string
		want  string
	}{
		{name: "timed", event: zonedEventJSON, args: []string{"--end-time", "09:00"},
			want: "would end at 2026-10-14 09:00 Europe/Zagreb, before it starts at 2026-10-14 10:00 Europe/Zagreb"},
		{name: "zoneless", event: zonelessEventJSON, args: []string{"--end-time", "09:00"},
			want: "would end at 2026-10-14 13:00 UTC, before it starts at 2026-10-14 14:00 UTC"},
		{name: "all-day", event: allDayEventJSON, args: []string{"--ends-on", "2026-10-13"},
			want: "ends-on 2026-10-13 is before starts-on 2026-10-14"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			handler, requests := zoneServer(t, zoneFixture{accountZone: "America/New_York", event: tt.event})
			_, err := runJSONCommand(t, handler, append([]string{"event", "edit", "4821", "2026-10-14", "--calendar", "9"}, tt.args...)...)
			var cliErr *apierr.Error
			if !errors.As(err, &cliErr) || cliErr.Code != apierr.CodeUsage || !strings.Contains(cliErr.Message, tt.want) {
				t.Fatalf("error = %v, want a usage error containing %q", err, tt.want)
			}
			if got := requests.writes.Load(); got != 0 {
				t.Errorf("writes = %d, want none", got)
			}
		})
	}

	t.Run("occurrence", func(t *testing.T) {
		handler, writes := occurrenceServer(t, "2026-09-15",
			`{"Calendar::Event":[`+occurrenceSeriesJSON+`]}`, "",
			func(t *testing.T, form url.Values) { t.Error("wrote a day that ends before it starts") })
		_, err := runJSONCommand(t, handler, "event", "edit", "4821", "--occurrence", "4821_2026-09-15", "--apply-to", "current",
			"--starts-on", "2026-09-15", "--ends-on", "2026-09-14", "--allow-plain-notes")
		var cliErr *apierr.Error
		if !errors.As(err, &cliErr) || cliErr.Code != apierr.CodeUsage || !strings.Contains(cliErr.Message, "before it starts") {
			t.Fatalf("error = %v, want a usage error about the order", err)
		}
		if writes.Load() != 0 {
			t.Errorf("writes = %d, want none", writes.Load())
		}
	})
}

// A new event's ends share a zone, so dates in the wrong order are refused before anything is
// read.
func TestEventsAddRefusesDatesInTheWrongOrderBeforeReading(t *testing.T) {
	var requests atomic.Int32
	_, err := runJSONCommand(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		http.Error(w, "unexpected request", http.StatusInternalServerError)
	}), "event", "add", "Team offsite", "--starts-on", "2026-10-15", "--ends-on", "2026-10-14", "--start-time", "09:00")
	if err == nil || !strings.Contains(err.Error(), "ends-on 2026-10-14 is before starts-on 2026-10-15") {
		t.Fatalf("error = %v, want the dates refused", err)
	}
	if got := requests.Load(); got != 0 {
		t.Errorf("requests = %d, want none", got)
	}
}

// HEY is sent clock times in whole minutes, so an end with seconds — an imported event —
// cannot be sent back as it is. A title-only edit that would move it is refused, whether the
// event has a zone or not.
func TestEventsEditRefusesToDropAKeptTimesSeconds(t *testing.T) {
	for _, tt := range []struct{ name, event, want string }{
		{name: "zoned", event: strings.Replace(zonedEventJSON, `"2026-10-14T08:00:00Z"`, `"2026-10-14T08:00:30Z"`, 1),
			want: "the event's start is at 2026-10-14 10:00:30 Europe/Zagreb, and HEY is only sent whole minutes, so the edit would move it 30 seconds earlier"},
		{name: "zoneless", event: strings.Replace(zonelessEventJSON, `"2026-10-14T15:00:00Z"`, `"2026-10-14T15:00:45Z"`, 1),
			want: "the event's end is at 2026-10-14 15:00:45 UTC, and HEY is only sent whole minutes, so the edit would move it 45 seconds earlier"},
		{name: "milliseconds", event: strings.Replace(zonedEventJSON, `"2026-10-14T08:00:00Z"`, `"2026-10-14T08:00:00.500Z"`, 1),
			want: "the event's start is at 2026-10-14 10:00:00.5 Europe/Zagreb, and HEY is only sent whole minutes, so the edit would move it 500 milliseconds earlier"},
		{name: "seconds and a fraction", event: strings.Replace(zonedEventJSON, `"2026-10-14T08:00:00Z"`, `"2026-10-14T08:00:12.250Z"`, 1),
			want: "the event's start is at 2026-10-14 10:00:12.25 Europe/Zagreb, and HEY is only sent whole minutes, so the edit would move it 12.25 seconds earlier"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			handler, requests := zoneServer(t, zoneFixture{event: tt.event})
			_, err := runJSONCommand(t, handler, "event", "edit", "4821", "2026-10-14", "--calendar", "9", "--title", "Dentist appointment (moved)")
			var cliErr *apierr.Error
			if !errors.As(err, &cliErr) || cliErr.Code != apierr.CodeUsage || cliErr.Message != tt.want {
				t.Fatalf("error = %v, want %q", err, tt.want)
			}
			if got := requests.writes.Load(); got != 0 {
				t.Errorf("writes = %d, want none", got)
			}
		})
	}
}

// The refusal says how far the edit would move a kept time: an hour in New York, where the
// clocks go back an hour, and half an hour on Lord Howe Island, where they go back thirty
// minutes.
func TestEventsEditSaysHowFarARepeatedTimeWouldMove(t *testing.T) {
	for _, tt := range []struct{ name, day, event, want string }{
		{name: "New York", day: "2026-11-01", want: "would move it an hour earlier",
			event: `{"id":4821,"title":"Night shift handover","starts_at":"2026-11-01T06:30:00Z","ends_at":"2026-11-01T07:30:00Z",` +
				`"starts_at_time_zone":"America/New_York","ends_at_time_zone":"America/New_York"}`},
		{name: "Lord Howe", day: "2026-04-05", want: "would move it 30 minutes earlier",
			event: `{"id":4821,"title":"Night shift handover","starts_at":"2026-04-04T15:15:00Z","ends_at":"2026-04-04T16:30:00Z",` +
				`"starts_at_time_zone":"Australia/Lord_Howe","ends_at_time_zone":"Australia/Lord_Howe"}`},
	} {
		t.Run(tt.name, func(t *testing.T) {
			handler, requests := zoneServer(t, zoneFixture{event: tt.event})
			_, err := runJSONCommand(t, handler, "event", "edit", "4821", tt.day, "--calendar", "9", "--title", "Night shift handover (Sam)")
			var cliErr *apierr.Error
			if !errors.As(err, &cliErr) || cliErr.Code != apierr.CodeUsage || !strings.Contains(cliErr.Message, tt.want) {
				t.Fatalf("error = %v, want a usage error saying it %s", err, tt.want)
			}
			if got := requests.writes.Load(); got != 0 {
				t.Errorf("writes = %d, want none", got)
			}
		})
	}
}

// A new event that would end before it starts is refused before anything is written, on
// its instants as HEY checks them; one that ends as it starts is HEY's to take.
func TestEventsAddRefusesAnEventThatEndsBeforeItStarts(t *testing.T) {
	handler, requests := zoneServer(t, zoneFixture{})
	_, err := runJSONCommand(t, handler, "event", "add", "Design review", "--calendar", "9",
		"--starts-on", "2026-10-14", "--start-time", "10:00", "--end-time", "09:00", "--time-zone", "Europe/Zagreb")
	var cliErr *apierr.Error
	want := "would end at 2026-10-14 09:00 Europe/Zagreb, before it starts at 2026-10-14 10:00 Europe/Zagreb"
	if !errors.As(err, &cliErr) || cliErr.Code != apierr.CodeUsage || !strings.Contains(cliErr.Message, want) {
		t.Fatalf("error = %v, want a usage error containing %q", err, want)
	}
	if got := requests.writes.Load(); got != 0 {
		t.Errorf("writes = %d, want none", got)
	}

	handler, requests = zoneServer(t, zoneFixture{})
	if _, err := runJSONCommand(t, handler, "event", "add", "Fire drill", "--calendar", "9",
		"--starts-on", "2026-10-14", "--start-time", "10:00", "--end-time", "10:00", "--time-zone", "Europe/Zagreb"); err != nil {
		t.Fatalf("execute event add ending as it starts: %v", err)
	}
	wantSchedule(t, requests.written(t), "2026-10-14", "10:00", "2026-10-14", "10:00", "Europe/Zagreb")
}

// A start with no end runs an hour of elapsed time from where HEY places the start, and ends
// at a clock time HEY places back at that moment. So 23:30 runs to 00:30 on the 15th and
// 23:00 to midnight; on the night Santiago's clocks skip midnight, 23:30 runs to 01:30; on
// the morning New York springs forward, 01:30 runs to 03:30; and on Lord Howe Island, whose
// clocks move half an hour, 01:30 runs to 02:00 as they go back and to 03:00 as they go
// forward. Every pair is an hour apart in ActiveSupport. --ends-on given with no --end-time is
// taken as it is, and one that puts the end before the start is refused.
func TestEventsAddRunsADefaultHourPastMidnight(t *testing.T) {
	tests := []struct {
		name, zone, startsOn, start string
		args                        []string
		endsOn, end                 string
	}{
		{name: "23:30", zone: "Europe/Zagreb", startsOn: "2026-10-14", start: "23:30", endsOn: "2026-10-15", end: "00:30"},
		{name: "23:00", zone: "Europe/Zagreb", startsOn: "2026-10-14", start: "23:00", endsOn: "2026-10-15", end: "00:00"},
		{name: "a normal time", zone: "Europe/Zagreb", startsOn: "2026-10-14", start: "14:00", endsOn: "2026-10-14", end: "15:00"},
		{name: "Santiago skips midnight", zone: "America/Santiago", startsOn: "2026-09-05", start: "23:30", endsOn: "2026-09-06", end: "01:30"},
		{name: "New York springs forward", zone: "America/New_York", startsOn: "2026-03-08", start: "01:30", endsOn: "2026-03-08", end: "03:30"},
		{name: "Lord Howe goes back", zone: "Australia/Lord_Howe", startsOn: "2026-04-05", start: "01:30", endsOn: "2026-04-05", end: "02:00"},
		{name: "Lord Howe goes forward", zone: "Australia/Lord_Howe", startsOn: "2026-10-04", start: "01:30", endsOn: "2026-10-04", end: "03:00"},
		{name: "ends-on given", zone: "Europe/Zagreb", startsOn: "2026-10-14", start: "23:30",
			args: []string{"--ends-on", "2026-10-16"}, endsOn: "2026-10-16", end: "00:30"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			handler, requests := zoneServer(t, zoneFixture{})
			args := append([]string{"event", "add", "Late shift", "--calendar", "9",
				"--starts-on", tt.startsOn, "--start-time", tt.start, "--time-zone", tt.zone}, tt.args...)
			if _, err := runJSONCommand(t, handler, args...); err != nil {
				t.Fatalf("execute event add: %v", err)
			}
			wantSchedule(t, requests.written(t), tt.startsOn, tt.start, tt.endsOn, tt.end, tt.zone)
		})
	}

	t.Run("ends-on the same day", func(t *testing.T) {
		handler, requests := zoneServer(t, zoneFixture{})
		_, err := runJSONCommand(t, handler, "event", "add", "Late shift", "--calendar", "9",
			"--starts-on", "2026-10-14", "--ends-on", "2026-10-14", "--start-time", "23:30", "--time-zone", "Europe/Zagreb")
		if err == nil || !strings.Contains(err.Error(), "before it starts") {
			t.Fatalf("error = %v, want the end before the start refused", err)
		}
		if got := requests.writes.Load(); got != 0 {
			t.Errorf("writes = %d, want none", got)
		}
	})
}

// An all-day event given a late start in an edit runs its default hour into the next day.
func TestEventsEditRunsADefaultHourPastMidnight(t *testing.T) {
	requests := runZoneEdit(t, zoneFixture{accountZone: "America/New_York", event: allDayEventJSON}, "--start-time", "23:30")
	wantSchedule(t, requests.written(t), "2026-10-14", "23:30", "2026-10-15", "00:30", "America/New_York")
}

// A timed event made all-day with both its dates given needs no zone to read it by, so it
// asks the account nothing and goes through for an account with no zone.
func TestEventsEditMadeAllDayOnTwoDatesNeedsNoZone(t *testing.T) {
	requests := runZoneEdit(t, zoneFixture{event: zonelessEventJSON}, "--all-day", "--starts-on", "2026-10-14", "--ends-on", "2026-10-15")
	form := requests.written(t)
	if got := form.Get("calendar_event[all_day]"); got != "1" {
		t.Errorf("all_day = %q, want 1", got)
	}
	if got, want := form.Get("calendar_event[starts_at]")+" "+form.Get("calendar_event[ends_at]"), "2026-10-14 2026-10-15"; got != want {
		t.Errorf("dates = %q, want %q", got, want)
	}
	if got := requests.identity.Load(); got != 0 {
		t.Errorf("identity reads = %d, want none", got)
	}
}

// An hour after 01:30 on the night New York falls back is 01:30 again, the second one, and
// HEY takes 01:30 as the first: sending 01:30 would make no event, and 02:30 two hours. On
// Lord Howe Island an hour after 01:00 is the second 01:30, which HEY would place thirty
// minutes after the start. Neither default hour can be sent, so the add asks for an end.
func TestEventsAddRefusesADefaultHourHEYCannotBeSent(t *testing.T) {
	for _, tt := range []struct{ name, zone, startsOn, start, want string }{
		{name: "New York", zone: "America/New_York", startsOn: "2026-11-01", start: "01:30", want: "would end at 2026-11-01 01:30 America/New_York"},
		{name: "Lord Howe", zone: "Australia/Lord_Howe", startsOn: "2026-04-05", start: "01:00", want: "would end at 2026-04-05 01:30 Australia/Lord_Howe"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			handler, requests := zoneServer(t, zoneFixture{})
			_, err := runJSONCommand(t, handler, "event", "add", "Night shift handover", "--calendar", "9",
				"--starts-on", tt.startsOn, "--start-time", tt.start, "--time-zone", tt.zone)
			var cliErr *apierr.Error
			if !errors.As(err, &cliErr) || cliErr.Code != apierr.CodeUsage || !strings.Contains(cliErr.Message, tt.want) || !strings.Contains(cliErr.Hint, "--end-time") {
				t.Fatalf("error = %v, want a usage error saying it %s and naming --end-time", err, tt.want)
			}
			if got := requests.writes.Load(); got != 0 {
				t.Errorf("writes = %d, want none", got)
			}
		})
	}
}

// An all-day event made a timed one keeps no clock time of its own: it starts at 09:00 unless
// given a start, and runs an hour from its start unless given an end — not to the last of the
// days it covered, which made a three-day birthday edited to 23:30 a 25-hour event. --ends-on
// names the day it ends. A timed event given a new start keeps its own end, days away or not.
func TestEventsEditDerivesTheEndOfAnAllDayEventMadeTimed(t *testing.T) {
	threeDays := `{"id":4821,"title":"Team offsite","all_day":true,"starts_at":"2026-10-14T00:00:00Z","ends_at":"2026-10-16T00:00:00Z"}`
	twoDaysTimed := `{"id":4821,"title":"Team offsite","starts_at":"2026-10-14T08:00:00Z","ends_at":"2026-10-16T10:00:00Z",` +
		`"starts_at_time_zone":"Europe/Zagreb","ends_at_time_zone":"Europe/Zagreb"}`
	tests := []struct {
		name, event                                string
		args                                       []string
		startsAt, startTime, endsAt, endTime, zone string
	}{
		{name: "late start", event: threeDays, args: []string{"--start-time", "23:30"},
			startsAt: "2026-10-14", startTime: "23:30", endsAt: "2026-10-15", endTime: "00:30", zone: "America/New_York"},
		{name: "start and ends-on", event: threeDays, args: []string{"--start-time", "10:00", "--ends-on", "2026-10-16"},
			startsAt: "2026-10-14", startTime: "10:00", endsAt: "2026-10-16", endTime: "11:00", zone: "America/New_York"},
		{name: "end only", event: allDayEventJSON, args: []string{"--end-time", "17:00"},
			startsAt: "2026-10-14", startTime: "09:00", endsAt: "2026-10-14", endTime: "17:00", zone: "America/New_York"},
		{name: "all-day off", event: allDayEventJSON, args: []string{"--all-day=false"},
			startsAt: "2026-10-14", startTime: "09:00", endsAt: "2026-10-14", endTime: "10:00", zone: "America/New_York"},
		{name: "timed event given a start", event: twoDaysTimed, args: []string{"--start-time", "09:00"},
			startsAt: "2026-10-14", startTime: "09:00", endsAt: "2026-10-16", endTime: "12:00", zone: "Europe/Zagreb"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			requests := runZoneEdit(t, zoneFixture{accountZone: "America/New_York", event: tt.event}, tt.args...)
			wantSchedule(t, requests.written(t), tt.startsAt, tt.startTime, tt.endsAt, tt.endTime, tt.zone)
		})
	}
}
