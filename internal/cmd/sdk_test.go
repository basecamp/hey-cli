package cmd

import (
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/basecamp/hey-sdk/go/pkg/generated"
	"github.com/basecamp/hey-sdk/go/pkg/hey"
)

// The SDK asks for this at runtime, when a 401 comes back and it wants to know whether
// these credentials can be renewed before surfacing the failure. A signature that drifts
// out of shape would go unnoticed there until a token expired mid-session.
var _ hey.TokenRefresher = (*cliAuthStrategy)(nil)

func TestFormatTimestampUTC(t *testing.T) {
	ts := time.Date(2024, 1, 15, 0, 0, 0, 0, time.UTC)
	got := formatTimestamp(ts)
	want := "2024-01-15T00:00"
	if got != want {
		t.Errorf("formatTimestamp = %q, want %q", got, want)
	}
}

// A recording carries the zone it was recorded in, and that is the wall-clock time the
// reader recognizes. Converting to UTC first moved every hour it printed: a time track
// started at 09:00 in Berlin read as 07:00.
func TestFormatTimestampKeepsTheRecordingsOwnZone(t *testing.T) {
	berlin := time.FixedZone("CEST", 2*60*60)
	ts := time.Date(2026, 8, 21, 9, 0, 0, 0, berlin)
	got := formatTimestamp(ts)
	want := "2026-08-21T09:00"
	if got != want {
		t.Errorf("formatTimestamp = %q, want %q", got, want)
	}
}

func TestFormatTimestampMidDay(t *testing.T) {
	ts := time.Date(2024, 1, 15, 14, 0, 0, 0, time.UTC)
	got := formatTimestamp(ts)
	want := "2024-01-15T14:00"
	if got != want {
		t.Errorf("formatTimestamp = %q, want %q", got, want)
	}
}

func TestFormatTimestampZero(t *testing.T) {
	var ts time.Time
	got := formatTimestamp(ts)
	if got != "" {
		t.Errorf("formatTimestamp(zero) = %q, want empty", got)
	}
}

func TestFormatDateUTC(t *testing.T) {
	ts := time.Date(2024, 1, 15, 0, 0, 0, 0, time.UTC)
	got := formatDate(ts)
	want := "2024-01-15"
	if got != want {
		t.Errorf("formatDate = %q, want %q", got, want)
	}
}

// A day that starts at midnight east of Greenwich is a day earlier in UTC, and the date
// this prints is fed straight back to `hey journal read`, which would then read an empty
// day.
func TestFormatDateKeepsTheRecordingsOwnDay(t *testing.T) {
	berlin := time.FixedZone("CEST", 2*60*60)
	ts := time.Date(2026, 8, 21, 0, 0, 0, 0, berlin)
	got := formatDate(ts)
	want := "2026-08-21"
	if got != want {
		t.Errorf("formatDate = %q, want %q", got, want)
	}
}

func TestFormatDateZero(t *testing.T) {
	var ts time.Time
	got := formatDate(ts)
	if got != "" {
		t.Errorf("formatDate(zero) = %q, want empty", got)
	}
}

func TestFindPersonalCalendarIDByFlag(t *testing.T) {
	calendars := []generated.Calendar{
		{Id: 1, Name: "Work", Personal: false},
		{Id: 110, Name: "", Personal: true},
	}
	id, err := findPersonalCalendarID(calendars)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if id != 110 {
		t.Errorf("findPersonalCalendarID = %d, want 110", id)
	}
}

func TestFindPersonalCalendarIDByName(t *testing.T) {
	calendars := []generated.Calendar{
		{Id: 1, Name: "Work", Personal: false},
		{Id: 42, Name: "Personal", Personal: false},
	}
	id, err := findPersonalCalendarID(calendars)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if id != 42 {
		t.Errorf("findPersonalCalendarID = %d, want 42", id)
	}
}

func TestFindPersonalCalendarIDNotFound(t *testing.T) {
	calendars := []generated.Calendar{
		{Id: 1, Name: "Work", Personal: false},
	}
	_, err := findPersonalCalendarID(calendars)
	if err == nil {
		t.Fatal("expected error, got nil")
	}
}

// An empty listing has to marshal as `[]`, not `null`, or the documented
// `--jq '.data[]'` recipe cannot iterate it.
func TestUnwrapCalendarsNil(t *testing.T) {
	result := unwrapCalendars(nil)
	if result == nil || len(result) != 0 {
		t.Errorf("unwrapCalendars(nil) = %v, want an empty non-nil slice", result)
	}
}

func TestUnwrapCalendars(t *testing.T) {
	payload := &generated.CalendarListPayload{
		Calendars: []generated.CalendarWithRecordingChangesUrl{
			{Calendar: generated.Calendar{Id: 1, Name: "Work"}},
			{Calendar: generated.Calendar{Id: 2, Name: "Personal"}},
		},
	}
	result := unwrapCalendars(payload)
	if len(result) != 2 {
		t.Fatalf("len = %d, want 2", len(result))
	}
	if result[0].Id != 1 || result[1].Id != 2 {
		t.Errorf("IDs = [%d, %d], want [1, 2]", result[0].Id, result[1].Id)
	}
}

func TestFilterRecordingsByType(t *testing.T) {
	resp := &generated.CalendarRecordingsResponse{
		"Calendar::Todo":         {{Id: 1, Title: "Todo"}},
		"Calendar::JournalEntry": {{Id: 2, Title: "Journal"}},
	}
	todos := filterRecordingsByType(resp, "Calendar::Todo")
	if len(todos) != 1 || todos[0].Id != 1 {
		t.Errorf("unexpected todos: %v", todos)
	}
	missing := filterRecordingsByType(resp, "Calendar::TimeTrack")
	if missing == nil || len(missing) != 0 {
		t.Errorf("expected an empty non-nil slice for a missing type, got %v", missing)
	}
	nilResult := filterRecordingsByType(nil, "Calendar::Todo")
	if nilResult == nil || len(nilResult) != 0 {
		t.Errorf("expected an empty non-nil slice for a nil response, got %v", nilResult)
	}
}

// countingTransport answers every request with the status it is set to, counting them.
type countingTransport struct {
	status int
	sent   map[string]int
}

func (c *countingTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	c.sent[req.Method+" "+req.URL.Path]++
	return &http.Response{StatusCode: c.status, Header: http.Header{"Content-Type": {"application/json"}},
		Body: io.NopCloser(strings.NewReader(`{"id":1,"time_zone":"America/New_York"}`)), Request: req}, nil
}

// The identity is read from HEY once a command, and asked again only when the answer might
// have changed: other credentials, or a write in between. An answer that was not a success
// is not kept.
func TestIdentityOnceAnswersARepeatedIdentityRead(t *testing.T) {
	get := func(t *testing.T, transport http.RoundTripper, method, path, token string) string {
		t.Helper()
		req, err := http.NewRequestWithContext(t.Context(), method, "https://app.hey.com"+path, nil)
		if err != nil {
			t.Fatal(err)
		}
		req.Header.Set("Authorization", "Bearer "+token)
		resp, err := transport.RoundTrip(req)
		if err != nil {
			t.Fatal(err)
		}
		body, _ := io.ReadAll(resp.Body)
		_ = resp.Body.Close()
		return string(body)
	}

	inner := &countingTransport{status: http.StatusOK, sent: map[string]int{}}
	once := &identityOnce{inner: inner}
	first := get(t, once, http.MethodGet, "/identity.json", "one")
	if second := get(t, once, http.MethodGet, "/identity.json", "one"); second != first || first == "" {
		t.Errorf("repeated read = %q, want %q", second, first)
	}
	if got := inner.sent["GET /identity.json"]; got != 1 {
		t.Errorf("identity reads sent = %d after a repeat, want one", got)
	}
	get(t, once, http.MethodGet, "/identity.json", "two")
	if got := inner.sent["GET /identity.json"]; got != 2 {
		t.Errorf("identity reads sent = %d after new credentials, want two", got)
	}
	get(t, once, http.MethodPatch, "/identity/time_format.json", "two")
	get(t, once, http.MethodGet, "/identity.json", "two")
	if got := inner.sent["GET /identity.json"]; got != 3 {
		t.Errorf("identity reads sent = %d after a write, want three", got)
	}

	failing := &countingTransport{status: http.StatusInternalServerError, sent: map[string]int{}}
	once = &identityOnce{inner: failing}
	get(t, once, http.MethodGet, "/identity.json", "one")
	get(t, once, http.MethodGet, "/identity.json", "one")
	if got := failing.sent["GET /identity.json"]; got != 2 {
		t.Errorf("identity reads sent = %d after a failure, want each one sent", got)
	}
}
