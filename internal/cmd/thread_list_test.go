package cmd

import (
	"encoding/json"
	"io"
	"net/http"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
)

const threadListFixture = `{
  "title": "Served view",
  "topics": [{
    "id": 42,
    "name": "Quarterly planning notes",
    "active_at": "2026-08-16T15:00:00Z",
    "creator": {"id": 7, "name": "Amanda Jones", "email_address": "amanda@example.com"}
  }]
}`

func TestThreadListViews(t *testing.T) {
	tests := []struct {
		view string
		path string
	}{
		{view: "sent", path: "/topics/sent.json"},
		{view: "spam", path: "/topics/spam.json"},
		{view: "trash", path: "/topics/trash.json"},
		{view: "everything", path: "/topics/everything.json"},
	}

	for _, test := range tests {
		t.Run(test.view, func(t *testing.T) {
			var requests atomic.Int32
			response, err := runJSONCommand(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				requests.Add(1)
				if r.Method != http.MethodGet || r.URL.Path != test.path {
					t.Errorf("request = %s %s, want GET %s", r.Method, r.URL.Path, test.path)
					http.NotFound(w, r)
					return
				}
				if got := r.URL.Query().Get("page"); got != "current/cursor" {
					t.Errorf("page = %q, want current/cursor", got)
				}
				w.Header().Set("Content-Type", "application/json")
				w.Header().Set("X-Total-Count", "2")
				w.Header().Set("Link", `<https://example.test`+test.path+`?page=next%2Fcursor>; rel="next"`)
				_, _ = io.WriteString(w, threadListFixture)
			}), "thread", "list", "--in", test.view, "--page", "current/cursor")
			if err != nil {
				t.Fatalf("execute: %v", err)
			}
			if requests.Load() != 1 {
				t.Fatalf("requests = %d, want 1", requests.Load())
			}
			if response.Summary != "1 thread in Served view" {
				t.Errorf("summary = %q", response.Summary)
			}
			if response.Notice != "Showing 1 of 2 results. Use --all to see everything." {
				t.Errorf("notice = %q", response.Notice)
			}
			if got := response.Meta["next_page"]; got != "next/cursor" {
				t.Errorf("next_page = %#v, want next/cursor", got)
			}
			if got := response.Meta["total_count"]; got != float64(2) {
				t.Errorf("total_count = %#v, want 2", got)
			}
			if got := response.Meta["pages_fetched"]; got != float64(1) {
				t.Errorf("pages_fetched = %#v, want 1", got)
			}
			if len(response.Breadcrumbs) != 1 || response.Breadcrumbs[0].Command != "hey thread read <thread-id>" {
				t.Errorf("breadcrumbs = %#v", response.Breadcrumbs)
			}

			items, ok := response.Data.([]any)
			if !ok || len(items) != 1 {
				t.Fatalf("data = %#v, want one row", response.Data)
			}
			row, ok := items[0].(map[string]any)
			if !ok || row["id"] != float64(42) || row["topic_id"] != float64(42) {
				t.Fatalf("row = %#v, want id and topic_id 42", items[0])
			}
		})
	}
}

func TestThreadListAllFollowsOpaqueCursorsAndOverridesLimit(t *testing.T) {
	wantCursors := []string{"", "cursor one", "cursor/two+"}
	var requests atomic.Int32
	response, err := runJSONCommand(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		request := int(requests.Add(1)) - 1
		if request >= len(wantCursors) {
			t.Errorf("unexpected request %d", request+1)
			http.Error(w, "unexpected request", http.StatusInternalServerError)
			return
		}
		if r.URL.Path != "/topics/everything.json" {
			t.Errorf("request %d path = %q, want fixed Everything route", request+1, r.URL.Path)
		}
		if got := r.URL.Query().Get("page"); got != wantCursors[request] {
			t.Errorf("request %d page = %q, want %q", request+1, got, wantCursors[request])
		}
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("X-Total-Count", "3")
		if request+1 < len(wantCursors) {
			next := []string{
				"https://attacker.invalid/not-the-view?page=cursor+one",
				"/topics/everything.json?page=cursor%2Ftwo%2B",
			}[request]
			w.Header().Set("Link", `<`+next+`>; rel="next"`)
		}
		_, _ = io.WriteString(w, `{"title":"Everything","topics":[{"id":`+strconv.Itoa(request+1)+`,"name":"Thread"}]}`)
	}), "thread", "list", "--in", "everything", "--limit", "1", "--all")
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	if requests.Load() != 3 {
		t.Fatalf("requests = %d, want 3", requests.Load())
	}
	items, ok := response.Data.([]any)
	if !ok || len(items) != 3 {
		t.Fatalf("data = %#v, want three rows", response.Data)
	}
	if response.Notice != "" || response.Meta["next_page"] != nil {
		t.Errorf("finished listing notice = %q, next_page = %#v", response.Notice, response.Meta["next_page"])
	}
}

func TestThreadListLimitReadsEnoughPagesAndReportsContinuation(t *testing.T) {
	var requests atomic.Int32
	response, err := runJSONCommand(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		request := requests.Add(1)
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("X-Total-Count", "3")
		if request == 1 {
			w.Header().Set("Link", `</topics/sent.json?page=second>; rel="next"`)
			_, _ = io.WriteString(w, `{"title":"Sent","topics":[{"id":1}]}`)
			return
		}
		if got := r.URL.Query().Get("page"); got != "second" {
			t.Errorf("second page cursor = %q", got)
		}
		w.Header().Set("Link", `</topics/sent.json?page=third>; rel="next"`)
		_, _ = io.WriteString(w, `{"title":"Sent","topics":[{"id":2}]}`)
	}), "thread", "list", "--in", "sent", "--limit", "2")
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	if requests.Load() != 2 {
		t.Fatalf("requests = %d, want 2", requests.Load())
	}
	items, ok := response.Data.([]any)
	if !ok || len(items) != 2 {
		t.Fatalf("data = %#v, want two rows", response.Data)
	}
	if got := response.Meta["next_page"]; got != "third" {
		t.Errorf("next_page = %#v, want third", got)
	}
}

func TestThreadListNoticeDistinguishesAllFromCursor(t *testing.T) {
	got := threadListNotice(3, 8, false, true, true)
	want := "Showing 3 remaining results from this cursor (8 threads in the view)."
	if got != want {
		t.Errorf("notice = %q, want %q", got, want)
	}
}

func TestThreadListRejectsInvalidOptionsBeforeRequest(t *testing.T) {
	tests := []struct {
		name string
		args []string
		want string
	}{
		{name: "missing view", args: []string{"thread", "list"}, want: "--in must be sent, spam, trash, or everything"},
		{name: "unknown view", args: []string{"thread", "list", "--in", "inbox"}, want: "--in must be sent, spam, trash, or everything"},
		{name: "negative limit", args: []string{"thread", "list", "--in", "sent", "--limit", "-1"}, want: "--limit must be at least 0"},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			var requests atomic.Int32
			_, err := runJSONCommand(t, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				requests.Add(1)
				http.Error(w, "unexpected", http.StatusInternalServerError)
			}), test.args...)
			if err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("error = %v, want %q", err, test.want)
			}
			if requests.Load() != 0 {
				t.Errorf("requests = %d, want 0", requests.Load())
			}
		})
	}
}

func TestThreadListStyledOutputSanitizesUntrustedFields(t *testing.T) {
	stdout, err := runStyledCommand(t, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{
  "title": "Sent\u001b[8m\rHidden",
  "topics": [{
    "id": 42,
    "name": "Quarterly planning\u001b[31m\rnotes",
    "active_at": "2026-08-16T15:00:00Z",
    "creator": {"name": "Amanda\u001b[2J\nJones"}
  }]
}`)
	}), "thread", "list", "--in", "sent")
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	for _, want := range []string{"Sent", "42", "Quarterly planning", "Amanda", "2026-08-16"} {
		if !strings.Contains(stdout, want) {
			t.Errorf("output missing %q:\n%s", want, stdout)
		}
	}
	for _, unsafe := range []string{"\x1b[8m", "\rHidden", "\x1b[31m", "\x1b[2J", "\r", "\nJones"} {
		if strings.Contains(stdout, unsafe) {
			t.Errorf("styled output contains unsafe text %q:\n%s", unsafe, stdout)
		}
	}
}

func TestThreadListOutputFormats(t *testing.T) {
	handler := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("X-Total-Count", "2")
		w.Header().Set("Link", `</topics/sent.json?page=next%2Fcursor>; rel="next"`)
		_, _ = io.WriteString(w, threadListFixture)
	})

	tests := []struct {
		name       string
		formatArgs []string
		wantOut    []string
		wantErr    []string
	}{
		{name: "ids", formatArgs: []string{"--ids-only"}, wantOut: []string{"42\n"}, wantErr: []string{"next_page: next/cursor"}},
		{name: "count", formatArgs: []string{"--count"}, wantOut: []string{"1\n"}, wantErr: []string{"next_page: next/cursor"}},
		{name: "quiet", formatArgs: []string{"--quiet"}, wantOut: []string{`"id": 42`, `"topic_id": 42`}, wantErr: []string{"next_page: next/cursor"}},
		{name: "markdown", formatArgs: []string{"--markdown"}, wantOut: []string{"# Served view", "| date | from | subject | topic_id |", "Quarterly planning notes", "**Total threads:** 2", "**Next page:** `next/cursor`"}},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			stdout, stderr, err := runFormattedCommandWithStderr(t, handler, test.formatArgs, "thread", "list", "--in", "sent")
			if err != nil {
				t.Fatalf("execute: %v", err)
			}
			for _, want := range test.wantOut {
				if !strings.Contains(stdout, want) {
					t.Errorf("stdout missing %q:\n%s", want, stdout)
				}
			}
			for _, want := range test.wantErr {
				if !strings.Contains(stderr, want) {
					t.Errorf("stderr missing %q:\n%s", want, stderr)
				}
			}
		})
	}
}

func TestThreadListEmptyJSONUsesArray(t *testing.T) {
	response, err := runJSONCommand(t, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"title":"Spam","topics":null}`)
	}), "thread", "list", "--in", "spam")
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	if response.Summary != "0 threads in Spam" {
		t.Errorf("summary = %q", response.Summary)
	}
	data, err := json.Marshal(response.Data)
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != "[]" {
		t.Fatalf("empty JSON data = %s, want []", data)
	}
}

func TestThreadListHelpNamesThreadIDs(t *testing.T) {
	stdout, err := runFormattedCommand(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Errorf("unexpected HTTP request: %s %s", r.Method, r.URL.Path)
	}), nil, "thread", "list", "--help")
	if err != nil {
		t.Fatalf("help: %v", err)
	}
	for _, want := range []string{"List thread IDs", "--in string", "--limit int", "--page string", "--all"} {
		if !strings.Contains(stdout, want) {
			t.Errorf("help missing %q:\n%s", want, stdout)
		}
	}
}
