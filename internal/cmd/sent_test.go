package cmd

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"
	"testing"
	"time"
)

const sentTopicsJSON = `{
	"title":"Sent Mail",
	"topics":[{
		"id":42,
		"name":"Quarterly planning follow-up",
		"app_url":"https://app.hey.com/topics/42/entries/99",
		"latest_entry":{
			"id":99,
			"subject":"Quarterly planning follow-up",
			"summary":"Here are the decisions and owners from today's planning session.",
			"created_at":"2026-09-18T09:00:00Z",
			"active_at":"2026-09-19T14:30:00Z",
			"addressed":{
				"directly":[{"id":1,"name":"Sarah Chen","email_address":"sarah@example.com"}],
				"copied":[{"id":2,"name":"Jamal Reed","email_address":"jamal@example.org"}],
				"blindcopied":[{"id":3,"name":"Pat Quinn","email_address":"pat@example.com"}]
			}
		}
	}]
}`

type sentTestRecipient struct {
	Name         string `json:"name"`
	EmailAddress string `json:"email_address"`
}

type sentTestRow struct {
	ID      int64               `json:"id"`
	Subject string              `json:"subject"`
	To      []sentTestRecipient `json:"to"`
	CC      []sentTestRecipient `json:"cc"`
	BCC     []sentTestRecipient `json:"bcc"`
	SentAt  *time.Time          `json:"sent_at"`
	AppURL  string              `json:"app_url"`
}

func decodeSentData[T any](t *testing.T, data any) T {
	t.Helper()
	encoded, err := json.Marshal(data)
	if err != nil {
		t.Fatalf("encode response data: %v", err)
	}
	var decoded T
	if err := json.Unmarshal(encoded, &decoded); err != nil {
		t.Fatalf("decode response data: %v", err)
	}
	return decoded
}

func TestSentCommandReturnsStableOutboundRows(t *testing.T) {
	response, err := runJSONCommand(t, sentTopicsHandler(t, sentTopicsJSON), "sent")
	if err != nil {
		t.Fatalf("execute sent: %v", err)
	}

	rows := decodeSentData[[]sentTestRow](t, response.Data)
	if len(rows) != 1 {
		t.Fatalf("rows = %d, want 1", len(rows))
	}
	row := rows[0]
	if row.ID != 42 || row.Subject != "Quarterly planning follow-up" {
		t.Errorf("row identity = %#v", row)
	}
	if len(row.To) != 1 || row.To[0].Name != "Sarah Chen" || row.To[0].EmailAddress != "sarah@example.com" {
		t.Errorf("to = %#v", row.To)
	}
	if len(row.CC) != 1 || row.CC[0].EmailAddress != "jamal@example.org" {
		t.Errorf("cc = %#v", row.CC)
	}
	if len(row.BCC) != 1 || row.BCC[0].EmailAddress != "pat@example.com" {
		t.Errorf("bcc = %#v", row.BCC)
	}
	if row.SentAt == nil {
		t.Fatal("sent_at is nil")
	}
	if got := row.SentAt.Format(time.RFC3339); got != "2026-09-19T14:30:00Z" {
		t.Errorf("sent_at = %q", got)
	}
	if row.AppURL != "https://app.hey.com/topics/42/entries/99" {
		t.Errorf("app_url = %q", row.AppURL)
	}
	if response.Summary != "1 sent message" {
		t.Errorf("summary = %q", response.Summary)
	}
}

func TestSentCommandStyledMatchesHEYRecipientSummary(t *testing.T) {
	stdout, err := runStyledCommand(t, sentTopicsHandler(t, sentTopicsJSON), "sent")
	if err != nil {
		t.Fatalf("execute sent --styled: %v", err)
	}
	wantDate := time.Date(2026, time.September, 19, 14, 30, 0, 0, time.UTC).Local().Format("2006-01-02")
	for _, want := range []string{
		"Quarterly planning follow-up",
		"Me → Sarah Chen + 2",
		"Here are the decisions and owners",
		wantDate,
	} {
		if !strings.Contains(stdout, want) {
			t.Errorf("styled output missing %q:\n%s", want, stdout)
		}
	}
}

func TestSentCommandMarkdownReportsRecipientsURLAndUnavailableDeliveryTime(t *testing.T) {
	body := strings.Replace(sentTopicsJSON, `"active_at":"2026-09-19T14:30:00Z",`, "", 1)
	body = strings.Replace(body, `"created_at":"2026-09-18T09:00:00Z",`, "", 1)
	markdown, err := runFormattedCommand(t, sentTopicsHandler(t, body), []string{"--markdown"}, "sent")
	if err != nil {
		t.Fatalf("execute sent --markdown: %v", err)
	}
	for _, want := range []string{
		"Me → Sarah Chen + 2",
		"https://app.hey.com/topics/42/entries/99",
		"Unavailable",
	} {
		if !strings.Contains(markdown, want) {
			t.Errorf("Markdown output missing %q:\n%s", want, markdown)
		}
	}
}

func TestSentCommandPreservesEmptySummaryFields(t *testing.T) {
	body := strings.Replace(sentTopicsJSON, `"summary":"Here are the decisions and owners from today's planning session."`, `"summary":""`, 1)

	response, err := runJSONCommand(t, sentTopicsHandler(t, body), "sent")
	if err != nil {
		t.Fatalf("execute sent --json: %v", err)
	}
	rows := decodeSentData[[]map[string]any](t, response.Data)
	if summary, ok := rows[0]["summary"]; !ok || summary != "" {
		t.Errorf("summary = %#v, present = %v", summary, ok)
	}

	markdown, err := runFormattedCommand(t, sentTopicsHandler(t, body), []string{"--markdown"}, "sent")
	if err != nil {
		t.Fatalf("execute sent --markdown: %v", err)
	}
	if !strings.Contains(markdown, " summary |") {
		t.Errorf("Markdown output has no summary column:\n%s", markdown)
	}
}

func TestSentCommandStyledShowsContinuationAfterAnEmptyPage(t *testing.T) {
	handler := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("Link", `</topics/sent.json?page=cursor-2>; rel="next"`)
		_, _ = io.WriteString(w, `{"title":"Sent Mail","topics":[]}`)
	})

	stdout, err := runStyledCommand(t, handler, "sent")
	if err != nil {
		t.Fatalf("execute sent --styled: %v", err)
	}
	for _, want := range []string{"No sent messages.", "Use --all to see everything."} {
		if !strings.Contains(stdout, want) {
			t.Errorf("styled output missing %q:\n%s", want, stdout)
		}
	}
}

func TestSentCommandAllContinuesAfterAnEmptyPageWithACursor(t *testing.T) {
	var pages []string
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		page := r.URL.Query().Get("page")
		pages = append(pages, page)
		w.Header().Set("Content-Type", "application/json")

		switch page {
		case "":
			w.Header().Set("Link", `</topics/sent.json?page=cursor-2>; rel="next"`)
			_, _ = io.WriteString(w, sentTopicsJSON)
		case "cursor-2":
			w.Header().Set("Link", `</topics/sent.json?page=cursor-3>; rel="next"`)
			_, _ = io.WriteString(w, `{"title":"Sent Mail","topics":[]}`)
		default:
			t.Errorf("unexpected page %q", page)
			http.Error(w, "unexpected page", http.StatusBadRequest)
		}
	})

	response, err := runJSONCommand(t, handler, "sent", "--all")
	if err != nil {
		t.Fatalf("execute sent --all: %v", err)
	}
	if got := strings.Join(pages, ","); got != ",cursor-2" {
		t.Errorf("pages = %q, want first page then cursor-2", got)
	}
	if response.Notice != "Showing 1 sent message. Continue with --page cursor-3." {
		t.Errorf("notice = %q", response.Notice)
	}
	if got := response.Meta["next_page"]; got != "cursor-3" {
		t.Errorf("next_page = %#v", got)
	}
}

func TestSentCommandAllFollowsNextPageLink(t *testing.T) {
	var mu sync.Mutex
	var pages []string
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/topics/sent.json" {
			http.NotFound(w, r)
			return
		}
		page := r.URL.Query().Get("page")
		mu.Lock()
		pages = append(pages, page)
		mu.Unlock()

		w.Header().Set("Content-Type", "application/json")
		switch page {
		case "":
			w.Header().Set("Link", `</topics/sent.json?page=2>; rel="next"`)
			_, _ = io.WriteString(w, sentTopicsJSON)
		case "2":
			_, _ = io.WriteString(w, strings.ReplaceAll(sentTopicsJSON, `"id":42`, `"id":43`))
		default:
			t.Errorf("unexpected page %q", page)
			http.Error(w, "unexpected page", http.StatusBadRequest)
		}
	})

	response, err := runJSONCommand(t, handler, "sent", "--all")
	if err != nil {
		t.Fatalf("execute sent --all: %v", err)
	}
	rows := decodeSentData[[]sentTestRow](t, response.Data)
	if len(rows) != 2 || rows[0].ID != 42 || rows[1].ID != 43 {
		t.Fatalf("rows = %#v", rows)
	}
	mu.Lock()
	gotPages := strings.Join(pages, ",")
	mu.Unlock()
	if gotPages != ",2" {
		t.Errorf("pages = %q, want first page then 2", gotPages)
	}
}

func TestSentCommandLimitReadsEnoughPagesAndTrims(t *testing.T) {
	var pages []string
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		page := r.URL.Query().Get("page")
		pages = append(pages, page)
		w.Header().Set("Content-Type", "application/json")
		switch page {
		case "":
			w.Header().Set("Link", `</topics/sent.json?page=2>; rel="next"`)
			_, _ = io.WriteString(w, sentTopicsPage(42))
		case "2":
			w.Header().Set("Link", `</topics/sent.json?page=3>; rel="next"`)
			_, _ = io.WriteString(w, sentTopicsPage(43, 44))
		default:
			t.Errorf("unexpected page %q", page)
			http.Error(w, "unexpected page", http.StatusBadRequest)
		}
	})

	response, err := runJSONCommand(t, handler, "sent", "--limit", "2")
	if err != nil {
		t.Fatalf("execute sent --limit 2: %v", err)
	}
	rows := decodeSentData[[]sentTestRow](t, response.Data)
	if len(rows) != 2 || rows[0].ID != 42 || rows[1].ID != 43 {
		t.Fatalf("rows = %#v", rows)
	}
	if got := strings.Join(pages, ","); got != ",2" {
		t.Errorf("pages = %q, want first page then 2", got)
	}
	if response.Notice != "Showing 2 sent messages. Use --all to see everything." {
		t.Errorf("notice = %q", response.Notice)
	}
}

func TestSentCommandIDsAndCountUseThreadIDs(t *testing.T) {
	handler := sentTopicsHandler(t, sentTopicsJSON)

	ids, err := runFormattedCommand(t, handler, []string{"--ids-only"}, "sent")
	if err != nil {
		t.Fatalf("execute sent --ids-only: %v", err)
	}
	if ids != "42\n" {
		t.Errorf("ids = %q, want thread ID", ids)
	}

	count, err := runFormattedCommand(t, handler, []string{"--count"}, "sent")
	if err != nil {
		t.Fatalf("execute sent --count: %v", err)
	}
	if count != "1\n" {
		t.Errorf("count = %q", count)
	}
}

func TestSentCommandPreservesOpaquePageCursor(t *testing.T) {
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if page := r.URL.Query().Get("page"); page != "cursor-2" {
			t.Errorf("page = %q, want cursor-2", page)
		}
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("Link", `</topics/sent.json?page=cursor-3>; rel="next"`)
		_, _ = io.WriteString(w, sentTopicsJSON)
	})

	response, err := runJSONCommand(t, handler, "sent", "--page", "cursor-2")
	if err != nil {
		t.Fatalf("execute sent --page cursor-2: %v", err)
	}
	if got := response.Meta["page"]; got != "cursor-2" {
		t.Errorf("page metadata = %#v", got)
	}
	if got := response.Meta["next_page"]; got != "cursor-3" {
		t.Errorf("next_page metadata = %#v", got)
	}
}

func TestSentCommandFallsBackToCreationTimeWhenDeliveryTimeIsMissing(t *testing.T) {
	body := strings.Replace(sentTopicsJSON, `"active_at":"2026-09-19T14:30:00Z",`, "", 1)
	response, err := runJSONCommand(t, sentTopicsHandler(t, body), "sent")
	if err != nil {
		t.Fatalf("execute sent: %v", err)
	}

	rows := decodeSentData[[]sentTestRow](t, response.Data)
	if rows[0].SentAt == nil {
		t.Fatal("sent_at is nil")
	}
	if got := rows[0].SentAt.Format(time.RFC3339); got != "2026-09-18T09:00:00Z" {
		t.Errorf("sent_at = %q", got)
	}
}

func TestSentListingNoticeSanitizesCursor(t *testing.T) {
	notice := sentListingNotice(100, 100, "cursor-\x1b[31mnext", true, false)
	if strings.ContainsRune(notice, '\x1b') {
		t.Errorf("notice contains escape byte: %q", notice)
	}
	if !strings.Contains(notice, "cursor-next") {
		t.Errorf("notice = %q", notice)
	}
}

func sentTopicsPage(ids ...int64) string {
	rows := make([]string, len(ids))
	for i, id := range ids {
		rows[i] = fmt.Sprintf(`{"id":%d,"name":"Planning follow-up %d","app_url":"https://app.hey.com/topics/%d/entries/99","latest_entry":{"id":99,"summary":"Decisions and owners.","active_at":"2026-09-19T14:30:00Z","addressed":{"directly":[],"copied":[],"blindcopied":[]}}}`, id, id, id)
	}
	return `{"title":"Sent Mail","topics":[` + strings.Join(rows, ",") + `]}`
}

func sentTopicsHandler(t *testing.T, body string) http.Handler {
	t.Helper()
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet || r.URL.Path != "/topics/sent.json" {
			t.Errorf("request = %s %s, want GET /topics/sent.json", r.Method, r.URL.Path)
			http.NotFound(w, r)
			return
		}
		if page := r.URL.Query().Get("page"); page != "" && page != "1" {
			t.Errorf("page = %q, want first page", page)
		}
		w.Header().Set("Content-Type", "application/json")
		if _, err := fmt.Fprint(w, body); err != nil {
			t.Errorf("write response: %v", err)
		}
	})
}
