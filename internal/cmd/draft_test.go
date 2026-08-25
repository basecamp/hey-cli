package cmd

import (
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"
)

// draftLifecycleServer serves the identity the SDK's sending operations resolve, a
// draft's edit state, and records every write so a test can say what actually went out.
type draftWrite struct {
	Method string
	Path   string
	Body   map[string]any
}

func draftLifecycleServer(t *testing.T, editJSON string, writes *[]draftWrite) http.Handler {
	t.Helper()
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.Contains(r.URL.Path, "identity"):
			w.Header().Set("Content-Type", "application/json")
			_, _ = io.WriteString(w, `{"email_address":"user@hey.com","id":1,"time_zone_name":"America/New_York","senders":[{"id":42,"default":true}],"primary_contact":{"id":42}}`)
		case r.Method == http.MethodGet && strings.HasSuffix(r.URL.Path, "/edit.json"):
			w.Header().Set("Content-Type", "application/json")
			_, _ = io.WriteString(w, editJSON)
		case r.Method == http.MethodPost && r.URL.Path == "/messages.json",
			r.Method == http.MethodPut && strings.HasPrefix(r.URL.Path, "/messages/"):
			var body map[string]any
			data, _ := io.ReadAll(r.Body)
			_ = json.Unmarshal(data, &body)
			*writes = append(*writes, draftWrite{Method: r.Method, Path: r.URL.Path, Body: body})
			entry, _ := body["entry"].(map[string]any)
			if entry != nil && entry["status"] == "drafted" {
				w.Header().Set("Location", "https://app.hey.com/messages/12345")
				w.WriteHeader(http.StatusNoContent)
				return
			}
			w.Header().Set("Content-Type", "application/json")
			_, _ = io.WriteString(w, `{"id":12345}`)
		case r.Method == http.MethodDelete && strings.HasPrefix(r.URL.Path, "/entries/drafts/"):
			*writes = append(*writes, draftWrite{Method: r.Method, Path: r.URL.Path})
			w.WriteHeader(http.StatusNoContent)
		default:
			t.Errorf("unexpected request: %s %s", r.Method, r.URL.Path)
			http.NotFound(w, r)
		}
	})
}

const draftEditJSON = `{"id":12345,"subject":"Quarterly planning","content":"<div>Agenda to follow.</div>",
	"updated_at":"2026-08-24T10:00:00Z",
	"addressed":{"directly":[{"id":7,"name":"Maria Delgado","email_address":"maria@example.com"}],
	             "copied":[{"id":8,"name":"Priya Natarajan","email_address":"priya@example.com"}]}}`

func TestComposeDraftSavesInsteadOfSending(t *testing.T) {
	var writes []draftWrite
	response, err := runJSONCommand(t, draftLifecycleServer(t, draftEditJSON, &writes),
		"compose", "--subject", "Board update", "-m", "Numbers to follow.", "--draft")
	if err != nil {
		t.Fatalf("compose --draft: %v", err)
	}

	if len(writes) != 1 || writes[0].Method != "POST" || writes[0].Path != "/messages.json" {
		t.Fatalf("writes = %+v", writes)
	}
	entry, _ := writes[0].Body["entry"].(map[string]any)
	if entry["status"] != "drafted" {
		t.Errorf("entry.status = %v, want drafted", entry["status"])
	}
	if response.Summary != "Draft saved" {
		t.Errorf("summary = %q", response.Summary)
	}
	data, _ := response.Data.(map[string]any)
	if data["id"] != float64(12345) {
		t.Errorf("data = %#v, want the Location's draft id", response.Data)
	}
}

// A draft needs nobody on it yet — only a send does.
func TestComposeDraftNeedsNoRecipients(t *testing.T) {
	var writes []draftWrite
	_, err := runJSONCommand(t, draftLifecycleServer(t, draftEditJSON, &writes),
		"compose", "--subject", "Board update", "-m", "Numbers.", "--draft")
	if err != nil {
		t.Fatalf("compose --draft without recipients: %v", err)
	}
	_, err = runJSONCommand(t, draftLifecycleServer(t, draftEditJSON, &writes),
		"compose", "--subject", "Board update", "-m", "Numbers.")
	if err == nil || !strings.Contains(err.Error(), "recipient") {
		t.Fatalf("a send without recipients must still be refused, got %v", err)
	}
}

func TestComposeThreadIDDraftSavesAReplyDraft(t *testing.T) {
	server, sent := threadReplyServer(t, messageAddressedToJane, 11, 12)

	if err := runCLI(t, server, "--account", "8", "compose", "--thread-id", "7", "-m", "reply draft body", "--draft"); err != nil {
		t.Fatalf("compose --thread-id --draft: %v", err)
	}
	if sent.Status != "drafted" {
		t.Errorf("entry.status = %q, want drafted", sent.Status)
	}
	if !strings.Contains(sent.Path, "/entries/12/replies") {
		t.Errorf("path = %q, want the latest entry's replies", sent.Path)
	}
	if len(sent.To) == 0 {
		t.Errorf("a reply draft carries the thread's recipients, got none")
	}
}

func TestDraftShowAnswersTheEditableState(t *testing.T) {
	var writes []draftWrite
	response, err := runJSONCommand(t, draftLifecycleServer(t, draftEditJSON, &writes),
		"draft", "show", "12345")
	if err != nil {
		t.Fatalf("draft show: %v", err)
	}
	data, _ := response.Data.(map[string]any)
	if data["id"] != float64(12345) || data["subject"] != "Quarterly planning" {
		t.Errorf("data = %#v", data)
	}
	if body, _ := data["body"].(string); !strings.Contains(body, "Agenda to follow") {
		t.Errorf("body = %q, want Markdown of the draft's content", data["body"])
	}
	to, _ := data["to"].([]any)
	if len(to) != 1 || to[0] != "maria@example.com" {
		t.Errorf("to = %v", data["to"])
	}
	if len(writes) != 0 {
		t.Errorf("show must not write, wrote %+v", writes)
	}
}

// An edit is a revision of the whole draft, so what is not flagged is read first and
// sent back unchanged.
func TestDraftEditReplacesOnlyTheFlaggedFields(t *testing.T) {
	var writes []draftWrite
	_, err := runJSONCommand(t, draftLifecycleServer(t, draftEditJSON, &writes),
		"draft", "edit", "12345", "--to", "sam@example.org")
	if err != nil {
		t.Fatalf("draft edit: %v", err)
	}

	if len(writes) != 1 || writes[0].Method != "PUT" || writes[0].Path != "/messages/12345.json" {
		t.Fatalf("writes = %+v", writes)
	}
	body := writes[0].Body
	message, _ := body["message"].(map[string]any)
	if message["subject"] != "Quarterly planning" {
		t.Errorf("subject = %v, want the existing subject resent", message["subject"])
	}
	if content, _ := message["content"].(string); !strings.Contains(content, "Agenda to follow") {
		t.Errorf("content = %q, want the existing body resent", message["content"])
	}
	entry, _ := body["entry"].(map[string]any)
	if entry["status"] != "drafted" {
		t.Errorf("entry.status = %v, want drafted", entry["status"])
	}
	addressed, _ := entry["addressed"].(map[string]any)
	directly, _ := addressed["directly"].([]any)
	if len(directly) != 1 || directly[0] != "sam@example.org" {
		t.Errorf("directly = %v, want the flag's replacement", addressed["directly"])
	}
	copied, _ := addressed["copied"].([]any)
	if len(copied) != 1 || copied[0] != "priya@example.com" {
		t.Errorf("copied = %v, want the existing CC kept", addressed["copied"])
	}
}

func TestDraftEditClearsARecipientKindWithAnEmptyValue(t *testing.T) {
	var writes []draftWrite
	_, err := runJSONCommand(t, draftLifecycleServer(t, draftEditJSON, &writes),
		"draft", "edit", "12345", "--cc", "")
	if err != nil {
		t.Fatalf("draft edit --cc '': %v", err)
	}
	entry, _ := writes[0].Body["entry"].(map[string]any)
	addressed, _ := entry["addressed"].(map[string]any)
	if _, present := addressed["copied"]; present {
		t.Errorf("copied = %v, want cleared", addressed["copied"])
	}
	directly, _ := addressed["directly"].([]any)
	if len(directly) != 1 {
		t.Errorf("directly = %v, want the To kind kept", addressed["directly"])
	}
}

func TestDraftSendDeliversWithTheDraftsOwnState(t *testing.T) {
	var writes []draftWrite
	response, err := runJSONCommand(t, draftLifecycleServer(t, draftEditJSON, &writes),
		"draft", "send", "12345")
	if err != nil {
		t.Fatalf("draft send: %v", err)
	}

	if len(writes) != 1 || writes[0].Method != "PUT" {
		t.Fatalf("writes = %+v", writes)
	}
	entry, _ := writes[0].Body["entry"].(map[string]any)
	if _, present := entry["status"]; present {
		t.Errorf("sending must omit entry.status, got %v", entry["status"])
	}
	addressed, _ := entry["addressed"].(map[string]any)
	directly, _ := addressed["directly"].([]any)
	if len(directly) != 1 || directly[0] != "maria@example.com" {
		t.Errorf("directly = %v, want the draft's own recipients", addressed["directly"])
	}
	if response.Summary != "Draft sent" {
		t.Errorf("summary = %q", response.Summary)
	}
}

func TestDraftSendRefusesADraftWithNoRecipients(t *testing.T) {
	var writes []draftWrite
	bare := `{"id":12345,"subject":"Quarterly planning","content":"<div>Agenda.</div>","addressed":{}}`
	_, err := runJSONCommand(t, draftLifecycleServer(t, bare, &writes),
		"draft", "send", "12345")
	if err == nil || !strings.Contains(err.Error(), "no recipients") {
		t.Fatalf("err = %v, want a no-recipients refusal", err)
	}
	if len(writes) != 0 {
		t.Errorf("nothing should be written, wrote %+v", writes)
	}
}

func TestDraftSendSchedulesWithOnAndHour(t *testing.T) {
	var writes []draftWrite
	// The fixture identity keeps America/New_York, so 9 on its clock in December
	// (EST, UTC-5) is 14:00Z — the UTC values HEY's API actually reads.
	response, err := runJSONCommand(t, draftLifecycleServer(t, draftEditJSON, &writes),
		"draft", "send", "12345", "--on", "2026-12-01", "--hour", "9")
	if err != nil {
		t.Fatalf("draft send --on: %v", err)
	}

	entry, _ := writes[0].Body["entry"].(map[string]any)
	if entry["status"] != "drafted" {
		t.Errorf("a scheduled send stays drafted, got status %v", entry["status"])
	}
	if entry["scheduled_delivery"] != "true" || entry["scheduled_delivery_at_date"] != "2026-12-01" || entry["scheduled_delivery_at_hour"] != "14" {
		t.Errorf("schedule = %v", entry)
	}
	if response.Summary != "Draft scheduled for delivery" {
		t.Errorf("summary = %q", response.Summary)
	}
}

func TestDraftDeleteTrashesEachDraft(t *testing.T) {
	var writes []draftWrite
	response, err := runJSONCommand(t, draftLifecycleServer(t, draftEditJSON, &writes),
		"draft", "delete", "12345", "67890")
	if err != nil {
		t.Fatalf("draft delete: %v", err)
	}
	if len(writes) != 2 || writes[0].Path != "/entries/drafts/12345.json" || writes[1].Path != "/entries/drafts/67890.json" {
		t.Errorf("writes = %+v", writes)
	}
	if response.Summary != "2 drafts deleted" {
		t.Errorf("summary = %q", response.Summary)
	}
}

func TestDraftCommandsRejectBadIDs(t *testing.T) {
	var writes []draftWrite
	for _, args := range [][]string{
		{"draft", "show", "abc"},
		{"draft", "edit", "0"},
		{"draft", "send", "999999999999999999999"},
		{"draft", "delete", "abc"},
	} {
		if _, err := runJSONCommand(t, draftLifecycleServer(t, draftEditJSON, &writes), args...); err == nil || !strings.Contains(err.Error(), "invalid draft ID") {
			t.Errorf("%v: err = %v, want invalid draft ID", args, err)
		}
	}
	if len(writes) != 0 {
		t.Errorf("nothing should be written, wrote %+v", writes)
	}
}

// HEY's API reads a schedule's date and hour in UTC, so preserving one through an edit
// says the served UTC moment back as its own UTC date and hour — the exact round-trip.
// Converting into any other zone here is the live bug this pins: a 09:00Z delivery
// became 04:00Z on the first edit from a US Eastern identity.
func TestDraftEditPreservesAScheduleExactly(t *testing.T) {
	scheduled := `{"id":12345,"subject":"Quarterly planning","content":"<div>Agenda.</div>",
		"scheduled_delivery_at":"2026-09-01T13:00:00Z",
		"addressed":{"directly":[{"id":7,"name":"Maria Delgado","email_address":"maria@example.com"}]}}`
	var writes []draftWrite
	_, err := runJSONCommand(t, draftLifecycleServer(t, scheduled, &writes),
		"draft", "edit", "12345", "--subject", "Quarterly planning (v2)")
	if err != nil {
		t.Fatalf("draft edit: %v", err)
	}

	entry, _ := writes[0].Body["entry"].(map[string]any)
	if entry["scheduled_delivery"] != "true" {
		t.Fatalf("schedule was not preserved: %v", entry)
	}
	if entry["scheduled_delivery_at_date"] != "2026-09-01" || entry["scheduled_delivery_at_hour"] != "13" {
		t.Errorf("schedule = %v/%v, want the served moment's own UTC 2026-09-01 hour 13",
			entry["scheduled_delivery_at_date"], entry["scheduled_delivery_at_hour"])
	}
}

// --on is validated against what it advertises before anything is fetched or written.
func TestDraftSendRefusesAMalformedOnDate(t *testing.T) {
	var writes []draftWrite
	_, err := runJSONCommand(t, draftLifecycleServer(t, draftEditJSON, &writes),
		"draft", "send", "12345", "--on", "next-week", "--hour", "9")
	if err == nil || !strings.Contains(err.Error(), "invalid --on date") {
		t.Fatalf("err = %v, want an invalid --on usage error", err)
	}
	if len(writes) != 0 {
		t.Errorf("nothing should be written, wrote %+v", writes)
	}
}

// draftScheduleFor is the identity-clock-to-UTC conversion itself.
func TestDraftScheduleFor(t *testing.T) {
	newYork, err := time.LoadLocation("America/New_York")
	if err != nil {
		t.Skipf("time zone database unavailable: %v", err)
	}
	adelaide, err := time.LoadLocation("Australia/Adelaide")
	if err != nil {
		t.Skipf("time zone database unavailable: %v", err)
	}
	// A fixed moment: 2026-12-01 20:00 in New York (2026-12-02 01:00Z).
	now := time.Date(2026, 12, 1, 20, 0, 0, 0, newYork)

	tests := []struct {
		name     string
		on       string
		hour     int
		wantDate string
		wantHour int
	}{
		{name: "explicit winter date", on: "2026-12-10", hour: 9, wantDate: "2026-12-10", wantHour: 14},
		{name: "today is the identity's today", on: "today", hour: 23, wantDate: "2026-12-02", wantHour: 4},
		{name: "tomorrow rolls the identity's clock", on: "tomorrow", hour: 9, wantDate: "2026-12-02", wantHour: 14},
		{name: "a late hour crosses into the next UTC day", on: "2026-12-10", hour: 22, wantDate: "2026-12-11", wantHour: 3},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			schedule, err := draftScheduleFor(newYork, tt.on, tt.hour, now)
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if schedule.Date != tt.wantDate || schedule.Hour != tt.wantHour {
				t.Errorf("schedule = %s/%d, want %s/%d", schedule.Date, schedule.Hour, tt.wantDate, tt.wantHour)
			}
		})
	}

	if _, err := draftScheduleFor(adelaide, "2026-12-10", 9, now); err == nil {
		t.Error("a half-hour zone offset cannot land on a whole UTC hour and must be refused")
	}
	if _, err := draftScheduleFor(newYork, "soon", 9, now); err == nil {
		t.Error("an unrecognized --on date must be refused")
	}
}
