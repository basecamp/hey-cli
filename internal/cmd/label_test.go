package cmd

import (
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"sync/atomic"
	"testing"
)

func TestLabelsCommand(t *testing.T) {
	response, err := runJSONCommand(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet || r.URL.Path != "/my/navigation.json" {
			t.Errorf("request = %s %s, want GET /my/navigation.json", r.Method, r.URL.Path)
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"items":[{"title":"Labels","menu_items":[{"title":"All Labels","app_url":"/folders"},{"title":"Receipts","app_url":"/folders/12"},{"title":"Travel","app_url":"/folders/34"}]}]}`)
	}), "labels", "--limit", "1")
	if err != nil {
		t.Fatalf("execute labels: %v", err)
	}
	if response.Summary != "1 label" {
		t.Errorf("summary = %q, want 1 label", response.Summary)
	}
	if response.Notice != "Showing 1 of 2 results. Use --all to see everything." {
		t.Errorf("notice = %q", response.Notice)
	}
	folders, ok := response.Data.([]any)
	if !ok || len(folders) != 1 {
		t.Fatalf("data = %#v, want one folder", response.Data)
	}
	label, ok := folders[0].(map[string]any)
	if !ok || label["id"] != float64(12) || label["name"] != "Receipts" || label["app_url"] != "/folders/12" {
		t.Errorf("label = %#v", folders[0])
	}
	if len(label) != 3 || label["created_at"] != nil || label["updated_at"] != nil {
		t.Errorf("navigation label exposed fields HEY did not return: %#v", label)
	}
}

func TestLabelsCommandAllOverridesLimit(t *testing.T) {
	response, err := runJSONCommand(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"items":[{"title":"Labels","menu_items":[{"title":"Receipts","app_url":"/folders/12"},{"title":"Travel","app_url":"/folders/34"}]}]}`)
	}), "labels", "--limit", "1", "--all")
	if err != nil {
		t.Fatalf("execute labels: %v", err)
	}
	folders, ok := response.Data.([]any)
	if !ok || len(folders) != 2 {
		t.Fatalf("data = %#v, want two folders", response.Data)
	}
	if response.Summary != "2 labels" || response.Notice != "" {
		t.Errorf("response = %#v, want complete result", response)
	}
}

func TestLabelCommand(t *testing.T) {
	response, err := runJSONCommand(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet || r.URL.Path != "/folders/12.json" {
			t.Errorf("request = %s %s, want GET /folders/12.json", r.Method, r.URL.Path)
			http.NotFound(w, r)
			return
		}
		if got := r.URL.Query().Get("page"); got != "current-cursor" {
			t.Errorf("page = %q, want current-cursor", got)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"id":12,"name":"Receipts","postings":[{"id":101,"kind":"topic","summary":"Hotel receipt","app_url":"https://app.hey.com/topics/501","creator":{"name":"Jane Doe"},"created_at":"2026-08-20T09:30:00Z"},{"id":102,"kind":"topic","summary":"Train receipt"}]}`)
	}), "label", "12", "--page", "current-cursor", "--limit", "1")
	if err != nil {
		t.Fatalf("execute label: %v", err)
	}
	if response.Summary != "1 thread labeled Receipts" {
		t.Errorf("summary = %q", response.Summary)
	}
	if response.Notice != "Showing 1 of 2 results. Use --all to see everything." {
		t.Errorf("notice = %q", response.Notice)
	}
	folder, ok := response.Data.(map[string]any)
	if !ok || folder["id"] != float64(12) || folder["name"] != "Receipts" {
		t.Fatalf("data = %#v, want Receipts folder", response.Data)
	}
	postings, ok := folder["postings"].([]any)
	if !ok || len(postings) != 1 {
		t.Fatalf("postings = %#v, want one posting", folder["postings"])
	}
	posting, ok := postings[0].(map[string]any)
	if !ok || posting["id"] != float64(101) || posting["topic_id"] != float64(501) {
		t.Errorf("posting IDs = %#v, want id 101 and topic_id 501", postings[0])
	}
}

func TestLabelCommandEmptyPreservesCollectionAndCount(t *testing.T) {
	response, err := runJSONCommand(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("X-Total-Count", "0")
		_, _ = io.WriteString(w, `{"id":12,"name":"Receipts","postings":[]}`)
	}), "label", "12")
	if err != nil {
		t.Fatalf("execute empty label: %v", err)
	}
	label, ok := response.Data.(map[string]any)
	if !ok {
		t.Fatalf("data = %#v", response.Data)
	}
	postings, ok := label["postings"].([]any)
	if !ok || len(postings) != 0 || label["total_count"] != float64(0) {
		t.Errorf("empty label contract = %#v", label)
	}
	if _, ok := label["created_at"]; ok {
		t.Errorf("empty label fabricated created_at: %#v", label)
	}
	if _, ok := label["updated_at"]; ok {
		t.Errorf("empty label fabricated updated_at: %#v", label)
	}
}

func TestLabelCommandReturnsContinuation(t *testing.T) {
	var requests atomic.Int32
	response, err := runJSONCommand(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("X-Total-Count", "3")
		w.Header().Set("Link", "<http://"+r.Host+"/folders/12.json?page=next-cursor>; rel=\"next\"")
		_, _ = io.WriteString(w, `{"id":12,"name":"Receipts","postings":[{"id":101,"kind":"topic"},{"id":102,"kind":"topic"}]}`)
	}), "label", "12")
	if err != nil {
		t.Fatalf("execute label: %v", err)
	}
	if requests.Load() != 1 {
		t.Errorf("requests = %d, want first page only", requests.Load())
	}
	folder, ok := response.Data.(map[string]any)
	if !ok || folder["next_page"] != "next-cursor" || folder["total_count"] != float64(3) {
		t.Fatalf("pagination metadata = %#v", response.Data)
	}
	postings, ok := folder["postings"].([]any)
	if !ok || len(postings) != 2 {
		t.Fatalf("postings = %#v", folder["postings"])
	}
	for _, raw := range postings {
		posting := raw.(map[string]any)
		if _, exists := posting["topic_id"]; exists {
			t.Errorf("posting without a topic URL exposed topic_id: %#v", posting)
		}
	}
	if response.Notice != "Showing 2 of 3 results. Use --all to see everything." {
		t.Errorf("notice = %q", response.Notice)
	}
}

func TestLabelCommandFetchesAllPages(t *testing.T) {
	var requests atomic.Int32
	response, err := runJSONCommand(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		request := requests.Add(1)
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("X-Total-Count", "3")
		if request == 1 {
			w.Header().Set("Link", "<http://"+r.Host+"/folders/12.json?page=next-cursor>; rel=\"next\"")
			_, _ = io.WriteString(w, `{"id":12,"name":"Receipts","postings":[{"id":101,"kind":"topic"},{"id":102,"kind":"topic"}]}`)
			return
		}
		if got := r.URL.Query().Get("page"); got != "next-cursor" {
			t.Errorf("page = %q, want next-cursor", got)
		}
		_, _ = io.WriteString(w, `{"id":12,"name":"Receipts","postings":[{"id":103,"kind":"topic"}]}`)
	}), "label", "12", "--all")
	if err != nil {
		t.Fatalf("execute label --all: %v", err)
	}
	if requests.Load() != 2 {
		t.Errorf("requests = %d, want 2", requests.Load())
	}
	folder, ok := response.Data.(map[string]any)
	if !ok {
		t.Fatalf("data = %#v", response.Data)
	}
	postings, ok := folder["postings"].([]any)
	if !ok || len(postings) != 3 {
		t.Fatalf("postings = %#v, want three", folder["postings"])
	}
	if folder["total_count"] != float64(3) || folder["next_page"] != nil {
		t.Errorf("pagination metadata = %#v", folder)
	}
	if response.Summary != "3 threads labeled Receipts" || response.Notice != "" {
		t.Errorf("response = %#v", response)
	}
}

func TestLabelCommandStyledTable(t *testing.T) {
	stdout, err := runStyledCommand(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"id":12,"name":"Receipts","postings":[{"id":101,"kind":"topic","summary":"Hotel receipt","app_url":"https://app.hey.com/topics/501","creator":{"name":"Jane Doe"},"created_at":"2026-08-20T09:30:00Z"}]}`)
	}), "label", "12")
	if err != nil {
		t.Fatalf("execute styled folder: %v", err)
	}
	for _, want := range []string{"Label: Receipts", "ID", "Thread", "From", "Summary", "Date", "101", "501", "Jane Doe", "Hotel receipt", "2026-08-20"} {
		if !strings.Contains(stdout, want) {
			t.Errorf("output %q does not contain %q", stdout, want)
		}
	}
}

func TestLabelStyledOutputSanitizesPostingText(t *testing.T) {
	const unsafeCreator = "Jane\x1b]2;owned\a\nDoe"
	const unsafeSummary = "Receipt\x1b]2;owned\a\nArchive"
	stdout, err := runStyledCommand(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"id": 12, "name": "Receipts",
			"postings": []any{map[string]any{
				"id": 101, "kind": "topic", "creator": map[string]any{"name": unsafeCreator}, "summary": unsafeSummary,
			}},
		})
	}), "label", "12")
	if err != nil {
		t.Fatalf("execute styled label: %v", err)
	}
	if strings.Contains(stdout, "\x1b]2;owned") || strings.Contains(stdout, "\nDoe") || strings.Contains(stdout, "\nArchive") {
		t.Errorf("unsafe posting text reached terminal output: %q", stdout)
	}
	for _, want := range []string{"Jane�]2;owned��Doe", "Receipt�]2;owned��Archive"} {
		if !strings.Contains(stdout, want) {
			t.Errorf("sanitized output %q does not contain %q", stdout, want)
		}
	}
}

func TestLabelStyledOutputSanitizesFolderNames(t *testing.T) {
	const unsafeName = "Receipts\x1b]2;owned\a\nArchive"
	stdout, err := runStyledCommand(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{"id": 12, "name": unsafeName})
	}), "label", "12")
	if err != nil {
		t.Fatalf("execute styled folder: %v", err)
	}
	if strings.Contains(stdout, "\x1b]2;owned") || strings.Contains(stdout, "\nArchive\n") {
		t.Errorf("unsafe label name reached terminal output: %q", stdout)
	}
	if !strings.Contains(stdout, "Receipts�]2;owned��Archive") {
		t.Errorf("sanitized label name missing from %q", stdout)
	}
}

func TestLabelMarkdownOutputSanitizesFolderNames(t *testing.T) {
	const unsafeName = "Receipts\x1b]2;owned\a\nArchive"
	server := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{"items": []any{map[string]any{
			"title":      "Labels",
			"menu_items": []any{map[string]any{"title": unsafeName, "app_url": "/folders/12"}},
		}}})
	})

	stdout, err := runFormattedCommand(t, server, []string{"--markdown"}, "labels")
	if err != nil {
		t.Fatalf("execute markdown folders: %v", err)
	}
	if strings.Contains(stdout, "\x1b") || strings.Contains(stdout, "\a") || strings.Contains(stdout, "\nArchive") {
		t.Errorf("unsafe label name reached markdown output: %q", stdout)
	}
	if !strings.Contains(stdout, "<br>Archive") {
		t.Errorf("sanitized label name missing from %q", stdout)
	}
}

func TestLabelTruncationNotice(t *testing.T) {
	tests := []struct {
		name       string
		shown      int
		total      int
		hasMore    bool
		all        bool
		fromCursor bool
		want       string
	}{
		{name: "limited", shown: 2, total: 5, want: "Showing 2 of 5 results. Use --all to see everything."},
		{name: "all capped", shown: 100, total: 200, hasMore: true, all: true, want: "Showing 100 results. Pagination limit reached; continue with --page using next_page."},
		{name: "all from cursor", shown: 3, total: 8, all: true, fromCursor: true, want: "Showing 3 remaining results from this cursor (8 threads with the label)."},
		{name: "missing cursor", shown: 3, total: 8, all: true, want: "Showing 3 of 8 results; HEY returned no additional page cursor."},
		{name: "complete", shown: 8, total: 8, all: true, want: ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := folderTruncationNotice(tt.shown, tt.total, tt.hasMore, tt.all, tt.fromCursor); got != tt.want {
				t.Errorf("notice = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestLabelMutationCommands(t *testing.T) {
	tests := []struct {
		name        string
		args        []string
		method      string
		path        string
		wantSummary string
		check       func(*testing.T, *http.Request)
	}{
		{
			name: "add", args: []string{"label", "add", "101", "102", "--to", "12"},
			method: http.MethodPost, path: "/postings/filings.json", wantSummary: "Label 12 added to 2 threads",
			check: func(t *testing.T, r *http.Request) {
				var body struct {
					FolderID   int64   `json:"folder_id"`
					PostingIDs []int64 `json:"posting_ids"`
				}
				if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
					t.Fatalf("decode body: %v", err)
				}
				if body.FolderID != 12 || len(body.PostingIDs) != 2 || body.PostingIDs[0] != 101 || body.PostingIDs[1] != 102 {
					t.Errorf("body = %#v", body)
				}
			},
		},
		{
			name: "create", args: []string{"label", "create", "Travel receipts", "101"},
			method: http.MethodPost, path: "/postings/folders.json", wantSummary: `Label "Travel receipts" created and added to 1 thread`,
			check: func(t *testing.T, r *http.Request) {
				var body struct {
					Folder struct {
						Name string `json:"name"`
					} `json:"folder"`
					PostingIDs []int64 `json:"posting_ids"`
				}
				if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
					t.Fatalf("decode body: %v", err)
				}
				if body.Folder.Name != "Travel receipts" || len(body.PostingIDs) != 1 || body.PostingIDs[0] != 101 {
					t.Errorf("body = %#v", body)
				}
			},
		},
		{
			name: "remove one", args: []string{"label", "remove", "101", "--from", "12"},
			method: http.MethodDelete, path: "/postings/filings.json", wantSummary: "Label 12 removed from 1 thread",
			check: func(t *testing.T, r *http.Request) {
				if got := r.URL.Query().Get("folder_id"); got != "12" {
					t.Errorf("folder_id = %q", got)
				}
				if got := r.URL.Query().Get("posting_ids"); got != "101" {
					t.Errorf("posting_ids = %q", got)
				}
			},
		},
		{
			name: "remove all", args: []string{"label", "remove", "101", "102", "--from", "all"},
			method: http.MethodDelete, path: "/postings/filings.json", wantSummary: "All labels removed from 2 threads",
			check: func(t *testing.T, r *http.Request) {
				if r.URL.Query().Has("folder_id") {
					t.Errorf("unexpected folder_id in %s", r.URL.RawQuery)
				}
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			response, err := runJSONCommand(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Method != tt.method || r.URL.Path != tt.path {
					t.Errorf("request = %s %s, want %s %s", r.Method, r.URL.Path, tt.method, tt.path)
					http.NotFound(w, r)
					return
				}
				if tt.check != nil {
					tt.check(t, r)
				}
				w.WriteHeader(http.StatusNoContent)
			}), tt.args...)
			if err != nil {
				t.Fatalf("execute mutation: %v", err)
			}
			if response.Summary != tt.wantSummary {
				t.Errorf("summary = %q, want %q", response.Summary, tt.wantSummary)
			}
		})
	}
}

func TestLabelValidationMakesNoRequest(t *testing.T) {
	tests := []struct {
		name string
		args []string
		want string
	}{
		{name: "show label ID", args: []string{"label", "invalid"}, want: "invalid label ID"},
		{name: "show positive label ID", args: []string{"label", "0"}, want: "invalid label ID"},
		{name: "add label required", args: []string{"label", "add", "101"}, want: "label is required"},
		{name: "add label ID", args: []string{"label", "add", "101", "--to", "invalid"}, want: "invalid label ID"},
		{name: "add thread", args: []string{"label", "add", "invalid", "--to", "12"}, want: "invalid thread ID"},
		{name: "add duplicate", args: []string{"label", "add", "101", "101", "--to", "12"}, want: "duplicate thread ID"},
		{name: "create name", args: []string{"label", "create", "   ", "101"}, want: "label name is required"},
		{name: "create posting", args: []string{"label", "create", "Receipts", "0"}, want: "invalid thread ID"},
		{name: "remove label required", args: []string{"label", "remove", "101"}, want: "label is required"},
		{name: "remove label ID", args: []string{"label", "remove", "101", "--from", "invalid"}, want: "invalid label ID"},
		{name: "remove thread", args: []string{"label", "remove", "invalid", "--from", "all"}, want: "invalid thread ID"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var requests atomic.Int32
			_, err := runJSONCommand(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				requests.Add(1)
				http.Error(w, "unexpected request", http.StatusInternalServerError)
			}), tt.args...)
			if err == nil || !strings.Contains(err.Error(), tt.want) {
				t.Fatalf("error = %v, want substring %q", err, tt.want)
			}
			if requests.Load() != 0 {
				t.Errorf("requests = %d, want 0", requests.Load())
			}
		})
	}
}

func TestLabelCommandAPIError(t *testing.T) {
	_, err := runJSONCommand(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "folder unavailable", http.StatusBadRequest)
	}), "label", "12")
	if err == nil || !strings.Contains(err.Error(), "400 Bad Request") {
		t.Fatalf("error = %v, want HTTP failure", err)
	}
}
