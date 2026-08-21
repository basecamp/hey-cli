package cmd

import (
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/spf13/cobra"
)

func TestValidateBoxArgs(t *testing.T) {
	command := &cobra.Command{Use: "box"}
	command.SetArgs([]string{})

	tests := []struct {
		name        string
		args        []string
		wantErr     bool
		errContains string
	}{
		{
			name:        "missing arg",
			args:        nil,
			wantErr:     true,
			errContains: "Usage:",
		},
		{
			name:    "one arg",
			args:    []string{"imbox"},
			wantErr: false,
		},
		{
			name:        "too many args",
			args:        []string{"imbox", "extra"},
			wantErr:     true,
			errContains: "expected 1 mailbox argument",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := validateBoxArgs(command, tt.args)
			if tt.wantErr {
				if err == nil {
					t.Fatalf("expected error, got nil")
				}
				if tt.errContains != "" && !strings.Contains(err.Error(), tt.errContains) {
					t.Fatalf("error = %q, want substring %q", err.Error(), tt.errContains)
				}
				return
			}

			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
		})
	}
}

func TestBoxCommandNamedRoutes(t *testing.T) {
	tests := []struct {
		name string
		box  string
		path string
	}{
		{name: "Imbox", box: "imbox", path: "/imbox.json"},
		{name: "Feed", box: "the feed", path: "/feedbox.json"},
		{name: "Paper Trail", box: "paper trail", path: "/paper_trail.json"},
		{name: "Set Aside", box: "set aside", path: "/set_aside.json"},
		{name: "Reply Later", box: "reply later", path: "/reply_later.json"},
		{name: "Bubbled Up", box: "bubbled up", path: "/bubble_up.json"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var requests atomic.Int32
			response, err := runJSONCommand(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				requests.Add(1)
				if r.Method != http.MethodGet || r.URL.Path != tt.path {
					t.Errorf("request = %s %s, want GET %s", r.Method, r.URL.Path, tt.path)
					http.NotFound(w, r)
					return
				}
				w.Header().Set("Content-Type", "application/json")
				_, _ = fmt.Fprintf(w, `{"id":1,"kind":%q,"name":%q,"postings":[]}`, tt.box, tt.name)
			}), "box", tt.box)
			if err != nil {
				t.Fatalf("execute box: %v", err)
			}
			if requests.Load() != 1 {
				t.Errorf("requests = %d, want one named lookup", requests.Load())
			}
			if response.Summary != "0 threads in "+tt.name {
				t.Errorf("summary = %q", response.Summary)
			}
		})
	}
}

func TestBoxCommandNumericIDAndLimit(t *testing.T) {
	response, err := runJSONCommand(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet || r.URL.Path != "/boxes/17.json" {
			t.Errorf("request = %s %s, want GET /boxes/17.json", r.Method, r.URL.Path)
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"id":17,"kind":"custom","name":"Receipts","next_history_url":"https://example.invalid/page-2","postings":[{"id":1,"summary":"First"},{"id":2,"summary":"Second"}]}`)
	}), "box", "17", "--limit", "1")
	if err != nil {
		t.Fatalf("execute box: %v", err)
	}
	if response.Summary != "1 thread in Receipts" {
		t.Errorf("summary = %q", response.Summary)
	}
	if response.Notice != "Showing 1 of 2 results. Use --all to see everything." {
		t.Errorf("notice = %q", response.Notice)
	}
	data, ok := response.Data.(map[string]any)
	if !ok {
		t.Fatalf("data = %T", response.Data)
	}
	if postings, ok := data["postings"].([]any); !ok || len(postings) != 1 {
		t.Errorf("postings = %#v, want one", data["postings"])
	}
	if next, _ := data["next_history_url"].(string); next != "" {
		t.Errorf("next_history_url = %q, want cleared after client truncation", next)
	}
}

func TestBoxCommandUnknownNameFallsBackToList(t *testing.T) {
	var requests []string
	response, err := runJSONCommand(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests = append(requests, r.Method+" "+r.URL.Path)
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/boxes.json":
			_, _ = io.WriteString(w, `[{"id":17,"kind":"receipts","name":"Receipts"}]`)
		case "/boxes/17.json":
			_, _ = io.WriteString(w, `{"id":17,"kind":"receipts","name":"Receipts","postings":[]}`)
		default:
			http.NotFound(w, r)
		}
	}), "box", "receipts")
	if err != nil {
		t.Fatalf("execute box: %v", err)
	}
	if got, want := fmt.Sprint(requests), "[GET /boxes.json GET /boxes/17.json]"; got != want {
		t.Errorf("requests = %s, want %s", got, want)
	}
	if response.Summary != "0 threads in Receipts" {
		t.Errorf("summary = %q", response.Summary)
	}
}

// A next_history_url is never fetched, so a foreign one cannot take the credentials with
// it. Only the page cursor inside the URL is read, and this one carries none.
func TestBoxCommandNeverFetchesAForeignPaginationURL(t *testing.T) {
	var requests atomic.Int32
	_, err := runJSONCommand(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"id":1,"kind":"imbox","name":"Imbox","next_history_url":"https://attacker.example/page-2","postings":[{"id":1}]}`)
	}), "box", "imbox", "--all")
	if err == nil || !strings.Contains(err.Error(), "carries no page cursor") {
		t.Fatalf("error = %v, want an unusable pagination cursor", err)
	}
	if requests.Load() != 1 {
		t.Errorf("requests = %d, want no request to pagination origin", requests.Load())
	}
}

// Pagination stays on the route the box is served by: the Feed, the Paper Trail and
// Bubbled Up order and page differently from /boxes/{id}, so a cursor from one route is
// meaningless to the other.
func TestBoxCommandFollowsPagesOnTheNamedRoute(t *testing.T) {
	var requests []string
	response, err := runJSONCommand(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests = append(requests, r.URL.Path+"?"+r.URL.RawQuery)
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Query().Get("page") {
		case "":
			_, _ = io.WriteString(w, `{"id":3,"kind":"feedbox","name":"The Feed","next_history_url":"https://app.hey.com/feedbox.json?page=cursor-2","postings":[{"id":1}]}`)
		case "cursor-2":
			_, _ = io.WriteString(w, `{"id":3,"kind":"feedbox","name":"The Feed","postings":[{"id":2}]}`)
		default:
			http.NotFound(w, r)
		}
	}), "box", "the feed", "--all")
	if err != nil {
		t.Fatalf("execute box: %v", err)
	}
	want := "[/feedbox.json? /feedbox.json?page=cursor-2]"
	if got := fmt.Sprint(requests); got != want {
		t.Errorf("requests = %s, want %s", got, want)
	}
	if response.Summary != "2 threads in The Feed" {
		t.Errorf("summary = %q", response.Summary)
	}
}

// A numeric ID reaches the same named route, because the box says what kind it is.
func TestBoxCommandFollowsPagesForANumericImbox(t *testing.T) {
	var requests []string
	if _, err := runJSONCommand(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests = append(requests, r.URL.Path+"?"+r.URL.RawQuery)
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Query().Get("page") {
		case "":
			_, _ = io.WriteString(w, `{"id":9,"kind":"imbox","name":"Imbox","next_history_url":"/imbox.json?page=cursor-2","postings":[{"id":1}]}`)
		default:
			_, _ = io.WriteString(w, `{"id":9,"kind":"imbox","name":"Imbox","postings":[]}`)
		}
	}), "box", "9", "--all"); err != nil {
		t.Fatalf("execute box: %v", err)
	}
	want := "[/boxes/9.json? /imbox.json?page=cursor-2]"
	if got := fmt.Sprint(requests); got != want {
		t.Errorf("requests = %s, want %s", got, want)
	}
}

// A box of an unfamiliar kind falls back to the generic route.
func TestBoxCommandFollowsPagesForACustomBox(t *testing.T) {
	var requests []string
	response, err := runJSONCommand(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests = append(requests, r.URL.Path+"?"+r.URL.RawQuery)
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Query().Get("page") {
		case "":
			_, _ = io.WriteString(w, `{"id":17,"kind":"receipts","name":"Receipts","next_history_url":"/boxes/17.json?page=cursor-2","postings":[{"id":1}]}`)
		default:
			_, _ = io.WriteString(w, `{"id":17,"kind":"receipts","name":"Receipts","next_history_url":"/boxes/17.json?page=cursor-3","postings":[{"id":2}]}`)
		}
	}), "box", "17", "--limit", "2")
	if err != nil {
		t.Fatalf("execute box: %v", err)
	}
	want := "[/boxes/17.json? /boxes/17.json?page=cursor-2]"
	if got := fmt.Sprint(requests); got != want {
		t.Errorf("requests = %s, want %s", got, want)
	}
	if response.Summary != "2 threads in Receipts" {
		t.Errorf("summary = %q", response.Summary)
	}
	data, _ := response.Data.(map[string]any)
	if next, _ := data["next_history_url"].(string); next != "/boxes/17.json?page=cursor-3" {
		t.Errorf("next_history_url = %q, want the last page read", next)
	}
}

// Nothing beyond the first page is read unless --all or a --limit asks for it.
func TestBoxCommandReadsOnePageByDefault(t *testing.T) {
	var requests atomic.Int32
	response, err := runJSONCommand(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"id":1,"kind":"imbox","name":"Imbox","next_history_url":"/imbox.json?page=cursor-2","postings":[{"id":1}]}`)
	}), "box", "imbox")
	if err != nil {
		t.Fatalf("execute box: %v", err)
	}
	if requests.Load() != 1 {
		t.Errorf("requests = %d, want one", requests.Load())
	}
	if response.Notice != "Showing 1 results. More available; use --all to fetch all." {
		t.Errorf("notice = %q", response.Notice)
	}
}

// An empty page ends the list, whatever cursor came with it.
func TestBoxCommandStopsAtAnEmptyPage(t *testing.T) {
	var requests atomic.Int32
	response, err := runJSONCommand(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Query().Get("page") {
		case "":
			_, _ = io.WriteString(w, `{"id":1,"kind":"imbox","name":"Imbox","next_history_url":"/imbox.json?page=cursor-2","postings":[{"id":1}]}`)
		default:
			_, _ = io.WriteString(w, `{"id":1,"kind":"imbox","name":"Imbox","postings":[]}`)
		}
	}), "box", "imbox", "--all")
	if err != nil {
		t.Fatalf("execute box: %v", err)
	}
	if requests.Load() != 2 {
		t.Errorf("requests = %d, want two", requests.Load())
	}
	if response.Summary != "1 thread in Imbox" || response.Notice != "" {
		t.Errorf("summary = %q notice = %q", response.Summary, response.Notice)
	}
}

func TestBoxCommandUnknownNameReturnsNotFound(t *testing.T) {
	_, err := runJSONCommand(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `[{"id":17,"kind":"receipts","name":"Receipts"}]`)
	}), "box", "newsletters")
	if err == nil || !strings.Contains(err.Error(), `box "newsletters" not found`) {
		t.Fatalf("error = %v, want box not found", err)
	}
}

func TestBoxSummaryUsesThreadTerminology(t *testing.T) {
	tests := []struct {
		name  string
		count int
		want  string
	}{
		{"one thread", 1, "1 thread in Imbox"},
		{"multiple threads", 2, "2 threads in Imbox"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := boxSummary(tt.count, "Imbox"); got != tt.want {
				t.Errorf("boxSummary(%d) = %q, want %q", tt.count, got, tt.want)
			}
		})
	}
}

// The thread ID is the point of a listing: whatever `hey box --json` calls topic_id is
// what `hey threads` reads, and the box item ID is not.
func TestBoxCommandCarriesAThreadIDThatThreadsReads(t *testing.T) {
	var paths []string
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		paths = append(paths, r.URL.Path)
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/imbox.json":
			_, _ = io.WriteString(w, `{"id":1,"kind":"imbox","name":"Imbox","postings":[{"id":1220478425,"app_url":"https://app.hey.com/topics/2080632163","summary":"Studio invoice"}]}`)
		case "/topics/2080632163/entries.json":
			_, _ = io.WriteString(w, `[{"id":55,"summary":"Studio invoice"}]`)
		case "/messages/55.json":
			_, _ = io.WriteString(w, `{"id":55,"content":"<div>Attached</div>"}`)
		default:
			http.NotFound(w, r)
		}
	})

	response, err := runJSONCommand(t, handler, "box", "imbox")
	if err != nil {
		t.Fatalf("execute box: %v", err)
	}
	data, ok := response.Data.(map[string]any)
	if !ok {
		t.Fatalf("data = %T", response.Data)
	}
	postings, ok := data["postings"].([]any)
	if !ok || len(postings) != 1 {
		t.Fatalf("postings = %#v, want one", data["postings"])
	}
	posting := postings[0].(map[string]any)
	if id, _ := posting["id"].(float64); int64(id) != 1220478425 {
		t.Errorf("id = %v, want the box item ID", posting["id"])
	}
	topicID, _ := posting["topic_id"].(float64)
	if int64(topicID) != 2080632163 {
		t.Fatalf("topic_id = %v, want the thread ID", posting["topic_id"])
	}
	threadID := fmt.Sprintf("%d", int64(topicID))

	if _, err := runJSONCommand(t, handler, "threads", threadID); err != nil {
		t.Fatalf("execute threads %s: %v", threadID, err)
	}
	if got, want := paths[1], "/topics/2080632163/entries.json"; got != want {
		t.Errorf("threads read %s, want %s", got, want)
	}
}

// Adding the thread ID takes nothing away: the box payload a consumer already reads is
// still the box HEY answered with.
func TestBoxCommandKeepsHEYsBoxPayload(t *testing.T) {
	response, err := runJSONCommand(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"id":1,"kind":"imbox","name":"Imbox","app_url":"https://app.hey.com/imbox","url":"https://app.hey.com/imbox.json","signed_stream_name":"stream-token","next_incremental_sync_url":"/imbox.json?since=1","posting_changes_url":"/imbox/postings/changes.json?since=1","next_history_url":"/imbox.json?page=cursor-2","postings":[{"id":101}]}`)
	}), "box", "imbox")
	if err != nil {
		t.Fatalf("execute box: %v", err)
	}
	data := response.Data.(map[string]any)
	want := map[string]string{
		"id":                        "1",
		"kind":                      "imbox",
		"name":                      "Imbox",
		"app_url":                   "https://app.hey.com/imbox",
		"url":                       "https://app.hey.com/imbox.json",
		"signed_stream_name":        "stream-token",
		"next_incremental_sync_url": "/imbox.json?since=1",
		"posting_changes_url":       "/imbox/postings/changes.json?since=1",
		"next_history_url":          "/imbox.json?page=cursor-2",
		"next_page":                 "cursor-2",
	}
	for field, value := range want {
		if got := fmt.Sprint(data[field]); got != value {
			t.Errorf("%s = %v, want %q", field, data[field], value)
		}
	}
}

func TestBoxCommandContinuesFromAPageCursor(t *testing.T) {
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.URL.Query().Get("page"); got != "cursor-2" {
			t.Errorf("page = %q, want cursor-2", got)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"id":1,"kind":"imbox","name":"Imbox","postings":[{"id":102}]}`)
	})

	// The cursor is accepted on its own, and inside the next_history_url it arrived in.
	for _, page := range []string{"cursor-2", "https://app.hey.com/imbox.json?page=cursor-2"} {
		response, err := runJSONCommand(t, handler, "box", "imbox", "--page", page)
		if err != nil {
			t.Fatalf("execute box --page %s: %v", page, err)
		}
		if response.Summary != "1 thread in Imbox" {
			t.Errorf("summary = %q", response.Summary)
		}
	}
}

func TestBoxCommandOutputFormats(t *testing.T) {
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"id":1,"kind":"imbox","name":"Imbox","postings":[
			{"id":101,"summary":"Studio invoice","app_url":"https://app.hey.com/topics/501","creator":{"name":"Jane Doe"}},
			{"id":102,"summary":"Tile samples","app_url":"https://app.hey.com/topics/502"}
		]}`)
	})

	ids, err := runFormattedCommand(t, handler, []string{"--ids-only"}, "box", "imbox")
	if err != nil || ids != "101\n102\n" {
		t.Errorf("ids output = %q, err = %v", ids, err)
	}
	count, err := runFormattedCommand(t, handler, []string{"--count"}, "box", "imbox")
	if err != nil || count != "2\n" {
		t.Errorf("count output = %q, err = %v", count, err)
	}
	markdown, err := runFormattedCommand(t, handler, []string{"--markdown"}, "box", "imbox")
	if err != nil {
		t.Fatalf("markdown box: %v", err)
	}
	for _, want := range []string{"# Imbox", "| id |", "topic_id", "Studio invoice", "**Total threads:** 2"} {
		if !strings.Contains(markdown, want) {
			t.Errorf("markdown %q does not contain %q", markdown, want)
		}
	}
	styled, err := runStyledCommand(t, handler, "box", "imbox")
	if err != nil {
		t.Fatalf("styled box: %v", err)
	}
	for _, want := range []string{"Box: Imbox (imbox)", "Thread", "Jane Doe", "Studio invoice", "101", "501"} {
		if !strings.Contains(styled, want) {
			t.Errorf("styled output %q does not contain %q", styled, want)
		}
	}
}

func TestBoxDataOnlyFormatsReportPaginationOnStderr(t *testing.T) {
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"id":1,"kind":"imbox","name":"Imbox","next_history_url":"/imbox.json?page=cursor-2","postings":[{"id":101}]}`)
	})

	for _, format := range []string{"--ids-only", "--count"} {
		t.Run(format, func(t *testing.T) {
			_, stderr, err := runFormattedCommandWithStderr(t, handler, []string{format}, "box", "imbox")
			if err != nil {
				t.Fatalf("box %s: %v", format, err)
			}
			for _, want := range []string{"notice: Showing 1 results. More available; use --all to fetch all.", "next_page: cursor-2"} {
				if !strings.Contains(stderr, want) {
					t.Errorf("stderr %q does not contain %q", stderr, want)
				}
			}
		})
	}
}
