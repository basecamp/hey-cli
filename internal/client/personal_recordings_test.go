package client

import (
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/basecamp/hey-cli/internal/auth"
)

func newAuthenticatedTestClient(t *testing.T, handler http.HandlerFunc) *Client {
	t.Helper()

	t.Setenv("HEY_NO_KEYRING", "1")
	t.Setenv("HEY_TOKEN", "test-token")

	server := httptest.NewServer(handler)
	t.Cleanup(server.Close)

	mgr := auth.NewManager(server.URL, server.Client(), t.TempDir())
	client := New(server.URL, mgr)
	client.HTTPClient = server.Client()
	return client
}

func TestListTodosFromPersonalCalendarRecordings(t *testing.T) {
	sawWindowParams := false

	c := newAuthenticatedTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/calendars.json":
			_, _ = io.WriteString(w, `{
				"calendars": [
					{"calendar": {"id": 1, "name": "Work", "kind": "normal", "owned": true, "personal": false}},
					{"calendar": {"id": 110, "name": "", "kind": "normal", "owned": false, "personal": true}}
				]
			}`)
		case "/calendars/110/recordings.json":
			if r.URL.Query().Get("starts_on") != "" && r.URL.Query().Get("ends_on") != "" {
				sawWindowParams = true
			}
			_, _ = io.WriteString(w, `{
				"Calendar::Todo": [
					{
						"id": 7,
						"title": "Ship release",
						"starts_at": "2026-03-04T00:00:00Z",
						"completed_at": "",
						"created_at": "2026-03-01T12:00:00Z",
						"updated_at": "2026-03-02T12:00:00Z"
					}
				]
			}`)
		default:
			http.NotFound(w, r)
		}
	})

	todos, err := c.ListTodos()
	if err != nil {
		t.Fatalf("ListTodos() error = %v", err)
	}

	if len(todos) != 1 {
		t.Fatalf("len(todos) = %d, want 1", len(todos))
	}
	if todos[0].ID != 7 {
		t.Fatalf("todos[0].ID = %d, want 7", todos[0].ID)
	}
	if todos[0].Title != "Ship release" {
		t.Fatalf("todos[0].Title = %q, want %q", todos[0].Title, "Ship release")
	}
	if !sawWindowParams {
		t.Fatalf("expected starts_on and ends_on query params to be sent")
	}
}

func TestListJournalEntriesFromPersonalCalendarRecordings(t *testing.T) {
	c := newAuthenticatedTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/calendars.json":
			_, _ = io.WriteString(w, `{
				"calendars": [{"calendar": {"id": 110, "name": "", "kind": "normal", "owned": false, "personal": true}}]
			}`)
		case "/calendars/110/recordings.json":
			_, _ = io.WriteString(w, `{
				"Calendar::JournalEntry": [
					{
						"id": 99,
						"starts_at": "2026-03-03T00:00:00Z",
						"content": "A concise preview",
						"created_at": "2026-03-03T08:00:00Z",
						"updated_at": "2026-03-03T09:00:00Z"
					}
				]
			}`)
		default:
			http.NotFound(w, r)
		}
	})

	entries, err := c.ListJournalEntries()
	if err != nil {
		t.Fatalf("ListJournalEntries() error = %v", err)
	}

	if len(entries) != 1 {
		t.Fatalf("len(entries) = %d, want 1", len(entries))
	}
	if entries[0].ID != 99 {
		t.Fatalf("entries[0].ID = %d, want 99", entries[0].ID)
	}
	if entries[0].Date != "2026-03-03" {
		t.Fatalf("entries[0].Date = %q, want %q", entries[0].Date, "2026-03-03")
	}
	if entries[0].Body != "A concise preview" {
		t.Fatalf("entries[0].Body = %q, want %q", entries[0].Body, "A concise preview")
	}
}

func TestListTimeTracksFromPersonalCalendarRecordings(t *testing.T) {
	c := newAuthenticatedTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/calendars.json":
			_, _ = io.WriteString(w, `{
				"calendars": [{"calendar": {"id": 110, "name": "", "kind": "normal", "owned": false, "personal": true}}]
			}`)
		case "/calendars/110/recordings.json":
			_, _ = io.WriteString(w, `{
				"Calendar::TimeTrack": [
					{
						"id": 55,
						"title": "Debug API",
						"starts_at": "2026-03-03T12:00:00Z",
						"ends_at": "2026-03-03T13:00:00Z",
						"created_at": "2026-03-03T13:00:00Z",
						"updated_at": "2026-03-03T13:00:00Z"
					}
				]
			}`)
		default:
			http.NotFound(w, r)
		}
	})

	tracks, err := c.ListTimeTracks()
	if err != nil {
		t.Fatalf("ListTimeTracks() error = %v", err)
	}

	if len(tracks) != 1 {
		t.Fatalf("len(tracks) = %d, want 1", len(tracks))
	}
	if tracks[0].ID != 55 {
		t.Fatalf("tracks[0].ID = %d, want 55", tracks[0].ID)
	}
	if tracks[0].Title != "Debug API" {
		t.Fatalf("tracks[0].Title = %q, want %q", tracks[0].Title, "Debug API")
	}
}

func TestGetOngoingTimeTrackReturnsZeroValueFor404(t *testing.T) {
	c := newAuthenticatedTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/calendar/ongoing_time_track":
			http.NotFound(w, r)
		default:
			http.NotFound(w, r)
		}
	})

	track, err := c.GetOngoingTimeTrack()
	if err != nil {
		t.Fatalf("GetOngoingTimeTrack() error = %v", err)
	}
	if track.ID != 0 {
		t.Fatalf("track.ID = %d, want 0", track.ID)
	}
}
