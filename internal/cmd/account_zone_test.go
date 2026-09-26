package cmd

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/basecamp/hey-cli/internal/apierr"
)

// newYorkIdentity is the identity of an account whose time zone is New York.
const newYorkIdentity = `{"id":1,"name":"Jason Fried","time_zone":"America/New_York"}`

// todayFixture is what todayServer answers the identity read with: a zone, a status, or a
// connection that drops.
type todayFixture struct {
	accountZone    string
	identityStatus int
	dropIdentity   bool
}

// todayRequests counts the identity reads and keeps every other request a command made, in
// a form that names the day it was sent for.
type todayRequests struct {
	identity atomic.Int32

	mu   sync.Mutex
	sent []string
}

func (r *todayRequests) record(request string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.sent = append(r.sent, request)
}

func (r *todayRequests) all() string {
	r.mu.Lock()
	defer r.mu.Unlock()
	return strings.Join(r.sent, "\n")
}

// todayServer answers every read and write a command that defaults to today makes: the
// identity, a day, a week, the calendars and their recordings, a journal entry, a habit's
// completion and a new to-do.
func todayServer(t *testing.T, fixture todayFixture) (http.Handler, *todayRequests) {
	t.Helper()
	requests := &todayRequests{}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		path := strings.TrimSuffix(r.URL.Path, ".json")
		switch {
		case path == "/identity":
			requests.identity.Add(1)
			switch {
			case fixture.dropIdentity:
				if conn, _, err := w.(http.Hijacker).Hijack(); err == nil {
					_ = conn.Close()
				}
			case fixture.identityStatus != 0:
				http.Error(w, `{"error":"identity unavailable"}`, fixture.identityStatus)
			default:
				zone := "null"
				if fixture.accountZone != "" {
					zone = `"` + fixture.accountZone + `"`
				}
				_, _ = io.WriteString(w, `{"id":1,"name":"Jason Fried","time_zone":`+zone+`}`)
			}
		case path == "/calendars":
			requests.record("GET /calendars")
			_, _ = io.WriteString(w, `{"calendars":[{"calendar":{"id":9,"name":"Work","owned":true,"kind":"normal"}}]}`)
		case path == "/calendars/9/recordings":
			requests.record("GET /calendars/9/recordings starts_on=" + r.URL.Query().Get("starts_on") + " ends_on=" + r.URL.Query().Get("ends_on"))
			_, _ = io.WriteString(w, `{"Calendar::Event":[]}`)
		case strings.HasPrefix(path, "/calendar/days/") && strings.Count(path, "/") == 3,
			strings.HasPrefix(path, "/calendar/weeks/"):
			requests.record(r.Method + " " + path)
			_, _ = io.WriteString(w, `{"kind":"day","recordings":{}}`)
		case path == "/calendar/todos":
			var body struct {
				Todo struct {
					StartsAt string `json:"starts_at"`
				} `json:"calendar_todo"`
			}
			_ = json.NewDecoder(r.Body).Decode(&body)
			requests.record("POST /calendar/todos starts_at=" + body.Todo.StartsAt)
			w.WriteHeader(http.StatusNoContent)
		default:
			requests.record(r.Method + " " + path)
			w.WriteHeader(http.StatusNoContent)
		}
	}), requests
}

// atInstantOn pins the clock to an instant as a machine in zone reads it.
func atInstantOn(t *testing.T, instant, zone string) {
	t.Helper()
	at, err := time.Parse(time.RFC3339, instant)
	if err != nil {
		t.Fatalf("instant %q: %v", instant, err)
	}
	loc, err := time.LoadLocation(zone)
	if err != nil {
		t.Fatalf("zone %q: %v", zone, err)
	}
	previous := clockNow
	clockNow = func() time.Time { return at.In(loc) }
	t.Cleanup(func() { clockNow = previous })
}

// todayCommand is a command that defaults to today, and the request it names the day in.
type todayCommand struct {
	name  string
	args  []string
	sends func(day string) string
	write bool
}

var todayCommands = []todayCommand{
	{name: "event day", args: []string{"event", "day"}, sends: func(day string) string { return "GET /calendar/days/" + day }},
	{name: "event week", args: []string{"event", "week"}, sends: func(day string) string { return "GET /calendar/weeks/" + day }},
	{name: "event list", args: []string{"event", "list"}, sends: func(day string) string {
		start, _ := time.Parse(dateLayout, day)
		return "GET /calendars/9/recordings starts_on=" + day + " ends_on=" + start.AddDate(0, 0, 30).Format(dateLayout)
	}},
	{name: "habit list", args: []string{"habit", "list"}, sends: func(day string) string { return "GET /calendar/weeks/" + day }},
	{name: "journal read", args: []string{"journal", "read"}, sends: func(day string) string { return "GET /calendar/days/" + day + "/journal_entry" }},
	{name: "habit complete", args: []string{"habit", "complete", "42"}, write: true, sends: func(day string) string { return "POST /calendar/days/" + day + "/habits/42/completions" }},
	{name: "habit uncomplete", args: []string{"habit", "uncomplete", "42"}, write: true, sends: func(day string) string { return "DELETE /calendar/days/" + day + "/habits/42/completions" }},
	{name: "journal write", args: []string{"journal", "write", "Shipped the pagination fix."}, write: true, sends: func(day string) string { return "PATCH /calendar/days/" + day + "/journal_entry" }},
	{name: "todo add", args: []string{"todo", "add", "Buy groceries"}, write: true, sends: func(day string) string { return "POST /calendar/todos starts_at=" + day }},
}

// withDate is the command with the day named, the way each command takes one.
func (c todayCommand) withDate(day string) []string {
	args := append([]string{}, c.args...)
	switch c.name {
	case "event day", "event week", "journal read":
		return append(args, day)
	case "event list":
		return append(args, "--starts-on", day)
	case "journal write":
		return []string{"journal", "write", day, args[2]}
	default:
		return append(args, "--date", day)
	}
}

// The reported case: at 02:00 UTC on the 15th it is still the evening of the 14th in New
// York. HEY answers a JSON request in UTC, and a server's clock is UTC too, so asking HEY for
// "now" or reading the machine's date both named the 15th. Every command that defaults to
// today names the account's day, once, whatever the machine's zone.
func TestTodayIsTheAccountsToday(t *testing.T) {
	for _, command := range todayCommands {
		t.Run(command.name, func(t *testing.T) {
			atInstantOn(t, "2026-10-15T02:00:00Z", "UTC")
			handler, requests := todayServer(t, todayFixture{accountZone: "America/New_York"})
			if _, err := runJSONCommand(t, handler, command.args...); err != nil {
				t.Fatalf("execute %s: %v", command.name, err)
			}
			if sent := requests.all(); !strings.Contains(sent, command.sends("2026-10-14")) {
				t.Errorf("requests =\n%s\nwant %q", sent, command.sends("2026-10-14"))
			}
			if got := requests.identity.Load(); got != 1 {
				t.Errorf("identity reads = %d, want 1", got)
			}
		})
	}
}

// A day that is named is the day sent, and the account is not asked for its zone.
func TestANamedDayReadsNoAccountZone(t *testing.T) {
	for _, command := range todayCommands {
		t.Run(command.name, func(t *testing.T) {
			atInstantOn(t, "2026-10-15T02:00:00Z", "UTC")
			handler, requests := todayServer(t, todayFixture{identityStatus: http.StatusInternalServerError})
			if _, err := runJSONCommand(t, handler, command.withDate("2026-03-15")...); err != nil {
				t.Fatalf("execute %s: %v", command.name, err)
			}
			if sent := requests.all(); !strings.Contains(sent, command.sends("2026-03-15")) {
				t.Errorf("requests =\n%s\nwant %q", sent, command.sends("2026-03-15"))
			}
			if got := requests.identity.Load(); got != 0 {
				t.Errorf("identity reads = %d, want none", got)
			}
		})
	}
}

// An account with no zone to read today in is a lasting state, so a read takes the
// machine's today rather than refusing every time, and says so on stderr. At 12:00 UTC it is
// already the 16th on Kiritimati — neither UTC's day nor any the account could name.
func TestAReadWithoutAnAccountZoneTakesTheMachinesToday(t *testing.T) {
	for _, fixture := range []struct {
		name    string
		fixture todayFixture
		reason  string
	}{
		{name: "account names none", fixture: todayFixture{}, reason: "your HEY account has no time zone set"},
		{name: "zone this build does not know", fixture: todayFixture{accountZone: "Mars/Olympus_Mons"}, reason: "your HEY account's time zone Mars/Olympus_Mons is not one this build of hey knows"},
	} {
		for _, command := range todayCommands {
			if command.write {
				continue
			}
			t.Run(fixture.name+"/"+command.name, func(t *testing.T) {
				atInstantOn(t, "2026-10-15T12:00:00Z", "Pacific/Kiritimati")
				handler, requests := todayServer(t, fixture.fixture)
				stdout, stderr, err := runFormattedCommandWithStderr(t, handler, []string{"--json"}, command.args...)
				if err != nil {
					t.Fatalf("execute %s: %v", command.name, err)
				}
				if sent := requests.all(); !strings.Contains(sent, command.sends("2026-10-16")) {
					t.Errorf("requests =\n%s\nwant %q", sent, command.sends("2026-10-16"))
				}
				want := "Notice: " + fixture.reason + ", so today is 2026-10-16 by this machine's clock"
				if !strings.Contains(stderr, want) {
					t.Errorf("stderr = %q, want %q", stderr, want)
				}
				if strings.Contains(stdout, "Notice:") {
					t.Errorf("stdout = %q, want the notice on stderr alone", stdout)
				}
			})
		}
	}
}

// Writing to a day nobody named is not a guess to make: without the account's zone a write
// is refused, says how to name the day, and writes nothing.
func TestAWriteWithoutAnAccountZoneIsRefused(t *testing.T) {
	for _, fixture := range []struct {
		name    string
		fixture todayFixture
		reason  string
	}{
		{name: "account names none", fixture: todayFixture{}, reason: "your HEY account has no time zone set"},
		{name: "zone this build does not know", fixture: todayFixture{accountZone: "Mars/Olympus_Mons"}, reason: "your HEY account's time zone Mars/Olympus_Mons is not one this build of hey knows"},
	} {
		for _, command := range todayCommands {
			if !command.write {
				continue
			}
			t.Run(fixture.name+"/"+command.name, func(t *testing.T) {
				atInstantOn(t, "2026-10-15T02:00:00Z", "UTC")
				handler, requests := todayServer(t, fixture.fixture)
				_, err := runJSONCommand(t, handler, command.args...)
				cliErr, ok := errors.AsType[*apierr.Error](err)
				if !ok || cliErr.Code != apierr.CodeUsage {
					t.Fatalf("error = %v, want a usage error", err)
				}
				if want := "cannot tell which day today is: " + fixture.reason; cliErr.Message != want {
					t.Errorf("message = %q, want %q", cliErr.Message, want)
				}
				if !strings.Contains(cliErr.Hint, "2026-10-14") {
					t.Errorf("hint = %q, want it to show how to name the day", cliErr.Hint)
				}
				if sent := requests.all(); sent != "" {
					t.Errorf("requests =\n%s\nwant none", sent)
				}
			})
		}
	}
}

// A read of the identity that fails is refused by reads and writes alike — a retry answers
// it, and a guessed day does not — and keeps what it failed with, so a script can tell a
// retry from a mistake. A login HEY no longer accepts is an auth failure and nothing else.
// Nothing else is requested.
func TestAFailedAccountReadKeepsItsCode(t *testing.T) {
	for _, tt := range []struct {
		name    string
		fixture todayFixture
		code    string
		status  int
		// only names the one command a failure is tried on: the SDK retries it after a
		// backoff first, which is seconds a command.
		only string
	}{
		{name: "server error", fixture: todayFixture{identityStatus: http.StatusInternalServerError}, code: apierr.CodeAPI, status: http.StatusInternalServerError},
		{name: "rate limited", fixture: todayFixture{identityStatus: http.StatusTooManyRequests}, code: apierr.CodeRateLimit, status: http.StatusTooManyRequests, only: "event day"},
		{name: "unreachable", fixture: todayFixture{dropIdentity: true}, code: apierr.CodeAPI, only: "todo add"},
		{name: "signed out", fixture: todayFixture{identityStatus: http.StatusUnauthorized}, code: apierr.CodeAuth, status: http.StatusUnauthorized},
	} {
		for _, command := range todayCommands {
			if tt.only != "" && command.name != tt.only {
				continue
			}
			t.Run(tt.name+"/"+command.name, func(t *testing.T) {
				atInstantOn(t, "2026-10-15T02:00:00Z", "UTC")
				handler, requests := todayServer(t, tt.fixture)
				_, err := runJSONCommand(t, handler, command.args...)
				cliErr, ok := errors.AsType[*apierr.Error](err)
				if !ok || cliErr.Code != tt.code {
					t.Fatalf("error = %#v, want code %s", err, tt.code)
				}
				if tt.code == apierr.CodeAuth {
					if strings.Contains(cliErr.Message, "today") || strings.Contains(cliErr.Hint, "2026-10-14") {
						t.Errorf("error = %q (hint %q), want the auth failure as it is", cliErr.Message, cliErr.Hint)
					}
				} else {
					if tt.status != 0 && cliErr.HTTPStatus != tt.status {
						t.Errorf("status = %d, want %d", cliErr.HTTPStatus, tt.status)
					}
					if !strings.HasPrefix(cliErr.Message, "cannot tell which day today is: your HEY account's time zone could not be read: ") {
						t.Errorf("message = %q, want it to say the zone could not be read", cliErr.Message)
					}
					if !strings.Contains(cliErr.Hint, "2026-10-14") {
						t.Errorf("hint = %q, want it to show how to name the day", cliErr.Hint)
					}
				}
				if sent := requests.all(); sent != "" {
					t.Errorf("requests =\n%s\nwant none", sent)
				}
			})
		}
	}
}
