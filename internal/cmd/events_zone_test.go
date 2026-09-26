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

// An all-day event has no clock time and no zone, so it asks the account nothing, and its
// today is this machine's.
func TestEventsAddAllDayNeedsNoAccountRead(t *testing.T) {
	atInstant(t, "2026-10-14T12:00:00Z")
	handler, requests := zoneServer(t, zoneFixture{})
	_, err := runJSONCommand(t, handler, "event", "add", "Sarah's birthday", "--calendar", "9")
	if err != nil {
		t.Fatalf("execute event add: %v", err)
	}
	form := requests.written(t)
	if got, want := form.Get("calendar_event[starts_at]"), eventNow().Local().Format(dateLayout); got != want {
		t.Errorf("starts_at = %q, want this machine's today %q", got, want)
	}
	if got := form.Get("calendar_event[all_day]"); got != "1" {
		t.Errorf("all_day = %q, want 1", got)
	}
	if got := requests.identity.Load(); got != 0 {
		t.Errorf("identity reads = %d, want none", got)
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
// skip moves an hour on, and a time they repeat is the first of the two — which Go's
// time.Date gets right in New York and wrong in Zagreb.
func TestWallClockOnPlacesAClockTimeAsHEYDoes(t *testing.T) {
	tests := []struct{ name, zone, day, clock, want string }{
		{name: "Zagreb repeats 02:30", zone: "Europe/Zagreb", day: "2026-10-25", clock: "02:30", want: "2026-10-25T00:30:00Z"},
		{name: "New York repeats 01:30", zone: "America/New_York", day: "2026-11-01", clock: "01:30", want: "2026-11-01T05:30:00Z"},
		{name: "New York skips 02:30", zone: "America/New_York", day: "2026-03-08", clock: "02:30", want: "2026-03-08T07:30:00Z"},
		{name: "Lord Howe repeats 01:45", zone: "Australia/Lord_Howe", day: "2026-04-05", clock: "01:45", want: "2026-04-04T14:45:00Z"},
		{name: "Lord Howe skips 02:15", zone: "Australia/Lord_Howe", day: "2026-10-04", clock: "02:15", want: "2026-10-03T16:15:00Z"},
		{name: "an ordinary day", zone: "America/New_York", day: "2026-06-10", clock: "10:00", want: "2026-06-10T14:00:00Z"},
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
				if !errors.As(err, &cliErr) || cliErr.Code != apierr.CodeUsage || !strings.Contains(cliErr.Message, "clocks go back") {
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
	if !errors.As(err, &cliErr) || cliErr.Code != apierr.CodeUsage || !strings.Contains(cliErr.Message, "clocks go back") {
		t.Fatalf("error = %v, want a usage error about the repeated hour", err)
	}
	if writes.Load() != 0 {
		t.Errorf("writes = %d, want none", writes.Load())
	}
}

// --account has the SDK read the identity before the command runs, to check the account is
// one the identity has. The account's zone comes from that same answer: a timed add reads
// the identity once, not twice.
func TestEventsAddWithAnAccountReadsTheIdentityOnce(t *testing.T) {
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

	if _, err := runAccountsCLI(t, server, "--account", "2", "event", "add", "Dentist appointment", "--calendar", "9",
		"--starts-on", "2026-10-14", "--start-time", "10:00"); err != nil {
		t.Fatalf("execute event add: %v", err)
	}
	if got := identityReads.Load(); got != 1 {
		t.Errorf("identity reads = %d, want one", got)
	}
	if got := writes.Load(); got != 1 {
		t.Errorf("writes = %d, want one", got)
	}
}
