package cmd

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
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
	attachments, ok := data["attachments"].([]any)
	if !ok || len(attachments) != 0 {
		t.Errorf("attachments = %#v, want an empty array", data["attachments"])
	}
	if len(writes) != 0 {
		t.Errorf("show must not write, wrote %+v", writes)
	}
}

func TestDraftShowIncludesAttachmentMetadataWithoutInternalLocators(t *testing.T) {
	content := `<div>Agenda to follow.</div>
<action-text-attachment sgid="secret-one" url="https://example.com/rails/blobs/report.pdf?signature=secret" filename="quarterly-report.pdf" content-type="application/pdf" filesize="128"></action-text-attachment>
<figure data-trix-attachment='{"sgid":"secret-two","url":"https://example.com/rails/blobs/empty.txt?signature=secret","filename":"empty.txt","contentType":"text/plain","filesize":0}'></figure>
<figure data-trix-attachment='{"url":"https://example.com/rails/blobs/unknown.bin?signature=secret","filename":"unknown.bin","contentType":"application/octet-stream"}'></figure>`
	editJSON := `{"id":12345,"subject":"Quarterly planning","content":` + strconv.Quote(content) + `,
		"sender":{"id":77,"email_address":"projects@example.org"},"addressed":{}}`
	var writes []draftWrite
	response, err := runJSONCommand(t, draftLifecycleServer(t, editJSON, &writes),
		"draft", "show", "12345")
	if err != nil {
		t.Fatalf("draft show: %v", err)
	}

	data, _ := response.Data.(map[string]any)
	attachments, _ := data["attachments"].([]any)
	if len(attachments) != 3 {
		t.Fatalf("attachments = %#v, want three files", data["attachments"])
	}
	first, _ := attachments[0].(map[string]any)
	if first["filename"] != "quarterly-report.pdf" || first["content_type"] != "application/pdf" || first["byte_size"] != float64(128) {
		t.Errorf("first attachment = %#v", first)
	}
	second, _ := attachments[1].(map[string]any)
	if second["filename"] != "empty.txt" || second["byte_size"] != float64(0) {
		t.Errorf("empty attachment = %#v", second)
	}
	third, _ := attachments[2].(map[string]any)
	if third["filename"] != "unknown.bin" || third["content_type"] != "application/octet-stream" {
		t.Errorf("unknown-size attachment = %#v", third)
	}
	if _, exists := third["byte_size"]; exists {
		t.Errorf("unknown-size attachment unexpectedly has byte_size: %#v", third)
	}
	for index, value := range attachments {
		attachment, _ := value.(map[string]any)
		if _, exists := attachment["url"]; exists {
			t.Errorf("attachment %d exposes url: %#v", index, attachment)
		}
		if _, exists := attachment["sgid"]; exists {
			t.Errorf("attachment %d exposes sgid: %#v", index, attachment)
		}
	}
	if len(writes) != 0 {
		t.Errorf("show must not write, wrote %+v", writes)
	}
}

func TestDraftShowStyledListsAttachmentMetadata(t *testing.T) {
	content := `<div>Agenda to follow.</div><action-text-attachment url="/rails/blobs/report.pdf" filename="quarterly-report.pdf" content-type="application/pdf" filesize="128"></action-text-attachment>`
	editJSON := `{"id":12345,"subject":"Quarterly planning","content":` + strconv.Quote(content) + `,
		"sender":{"id":77,"email_address":"projects@example.org"},"addressed":{}}`
	var writes []draftWrite
	server := httptest.NewServer(draftLifecycleServer(t, editJSON, &writes))
	t.Cleanup(server.Close)

	stdout, stderr, err := runCLIRaw(t, server, "--styled", "draft", "show", "12345")
	if err != nil {
		t.Fatalf("draft show --styled: %v", err)
	}
	for _, want := range []string{"Attachments:", "quarterly-report.pdf (application/pdf, 128 B)"} {
		if !strings.Contains(stdout, want) {
			t.Errorf("stdout = %q, want %q", stdout, want)
		}
	}
	if stderr != "" {
		t.Errorf("stderr = %q, want empty", stderr)
	}
	if len(writes) != 0 {
		t.Errorf("show must not write, wrote %+v", writes)
	}
}

func TestDraftShowHTMLAnswersTheCompleteStoredBody(t *testing.T) {
	stdoutTerminal(t, false)

	for _, test := range []struct {
		name    string
		content string
	}{
		{
			name:    "rich body with attachment markup",
			content: `<div><strong>Quarterly planning</strong></div><figure data-trix-attachment="{&quot;filename&quot;:&quot;agenda.pdf&quot;}"></figure>`,
		},
		{name: "stored trailing newline", content: "<div>Agenda.</div>\n"},
		{name: "empty body"},
	} {
		t.Run(test.name, func(t *testing.T) {
			editJSON := `{"id":12345,"subject":"Quarterly planning","content":` + strconv.Quote(test.content) + `,
				"sender":{"id":77,"email_address":"projects@example.org"},"addressed":{}}`
			var writes []draftWrite
			server := httptest.NewServer(draftLifecycleServer(t, editJSON, &writes))
			t.Cleanup(server.Close)

			stdout, stderr, err := runCLIRaw(t, server, "draft", "show", "12345", "--html")
			if err != nil {
				t.Fatalf("draft show --html: %v", err)
			}
			if stdout != test.content {
				t.Errorf("stdout = %q, want byte-exact stored HTML %q", stdout, test.content)
			}
			if stderr != "" {
				t.Errorf("stderr = %q, want empty", stderr)
			}
			if len(writes) != 0 {
				t.Errorf("show must not write, wrote %+v", writes)
			}
		})
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

func TestDraftEditReadsRawHTMLFromFileVerbatim(t *testing.T) {
	path := filepath.Join(t.TempDir(), "revised-message.html")
	want := "<h1>Revised agenda</h1>\n<ol><li>Budget</li><li>Hiring</li></ol>\n"
	if err := os.WriteFile(path, []byte(want), 0o600); err != nil {
		t.Fatal(err)
	}

	var writes []draftWrite
	_, err := runJSONCommand(t, draftLifecycleServer(t, draftEditJSON, &writes),
		"draft", "edit", "12345", "--message-html-file", path)
	if err != nil {
		t.Fatalf("draft edit --message-html-file: %v", err)
	}
	if len(writes) != 1 {
		t.Fatalf("writes = %+v", writes)
	}
	message, _ := writes[0].Body["message"].(map[string]any)
	if message["content"] != want {
		t.Errorf("content = %q, want exact file bytes %q", message["content"], want)
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

// A schedule set from a HEY app on a fractional-offset clock sits between the whole
// UTC hours the API can express. An edit must refuse rather than move the delivery —
// and must refuse before writing anything.
func TestDraftEditRefusesAScheduleItCannotPreserve(t *testing.T) {
	fractional := `{"id":12345,"subject":"Quarterly planning","content":"<div>Agenda.</div>",
		"scheduled_delivery_at":"2026-09-01T03:30:00Z",
		"addressed":{"directly":[{"id":7,"name":"Maria Delgado","email_address":"maria@example.com"}]}}`
	var writes []draftWrite
	_, err := runJSONCommand(t, draftLifecycleServer(t, fractional, &writes),
		"draft", "edit", "12345", "--subject", "Quarterly planning (v2)")
	if err == nil || !strings.Contains(err.Error(), "adjust it in a HEY app") {
		t.Fatalf("err = %v, want a cannot-preserve refusal", err)
	}
	if len(writes) != 0 {
		t.Errorf("nothing should be written, wrote %+v", writes)
	}
}
