package cmd

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/basecamp/hey-sdk/go/pkg/generated"
	"github.com/spf13/cobra"

	"github.com/basecamp/hey-cli/internal/output"
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

func TestBoxHelpUsesMixedItemTerminology(t *testing.T) {
	command := newBoxCommand().cmd
	if command.Short != "List email and HEY World items in a box" {
		t.Errorf("short help = %q", command.Short)
	}
	if usage := command.Flags().Lookup("limit").Usage; usage != "Maximum number of items to show" {
		t.Errorf("--limit help = %q", usage)
	}
}

// makePostings creates n test postings with sequential IDs starting at offset+1.
func makePostings(n, offset int) []generated.Posting {
	postings := make([]generated.Posting, n)
	for i := range postings {
		postings[i] = generated.Posting{Id: int64(offset + i + 1), Kind: "topic"}
	}
	return postings
}

// mockFetcher returns a pageFetcher that serves a predefined sequence of pages.
// Each call returns the next page; after all pages are exhausted it returns an error.
func mockFetcher(pages []generated.BoxShowResponse) pageFetcher {
	idx := 0
	return func(_ context.Context, _ string) (*generated.BoxShowResponse, error) {
		if idx >= len(pages) {
			return nil, fmt.Errorf("unexpected fetch beyond %d pages", len(pages))
		}
		page := pages[idx]
		idx++
		return &page, nil
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
			if response.Summary != "0 emails in "+tt.name {
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
	if response.Summary != "1 email in Receipts" {
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
	if response.Summary != "0 emails in Receipts" {
		t.Errorf("summary = %q", response.Summary)
	}
}

func TestBoxCommandRejectsCrossOriginPagination(t *testing.T) {
	var requests atomic.Int32
	_, err := runJSONCommand(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"id":1,"kind":"imbox","name":"Imbox","next_history_url":"https://attacker.example/page-2","postings":[{"id":1}]}`)
	}), "box", "imbox", "--all")
	if err == nil || !strings.Contains(err.Error(), "pagination URL origin") {
		t.Fatalf("error = %v, want cross-origin pagination rejection", err)
	}
	if requests.Load() != 1 {
		t.Errorf("requests = %d, want no request to pagination origin", requests.Load())
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

func TestBoxPostingCountsAndSummary(t *testing.T) {
	postings := makePostings(2944, 0)
	for i := 0; i < 21; i++ {
		postings = append(postings, generated.Posting{Id: int64(3000 + i), Kind: "world/post"})
	}

	counts := countBoxPostings(postings)
	if counts.postings != 2965 || counts.emails != 2944 || counts.worldPosts != 21 {
		t.Fatalf("counts = %+v", counts)
	}
	if got := counts.summary("Imbox"); got != "2,944 emails and 21 HEY World posts in Imbox" {
		t.Errorf("summary = %q", got)
	}
}

func TestBoxTableLabelsMixedItemsByKind(t *testing.T) {
	headers := boxTableHeaders()
	if len(headers) < 2 || headers[0] != "Item" || headers[1] != "Kind" {
		t.Fatalf("headers = %v, want Item and Kind columns", headers)
	}

	row := boxTableRow(generated.Posting{Id: 102, Kind: "world/post", Summary: "Published note"})
	if len(row) < 2 || row[0] != "102" || row[1] != "world/post" {
		t.Fatalf("row = %v, want World item ID and kind", row)
	}
}

func runBox(t *testing.T, server *httptest.Server, args ...string) (output.Response, error) {
	t.Helper()
	t.Setenv("HEY_TOKEN", "test-token")
	t.Setenv("HEY_NO_KEYRING", "1")
	t.Setenv("HEY_BASE_URL", "")
	tmpDir := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", tmpDir)
	t.Setenv("XDG_STATE_HOME", tmpDir)
	t.Setenv("XDG_CACHE_HOME", tmpDir)

	root := newRootCmd()
	var buf bytes.Buffer
	root.SetOut(&buf)
	root.SetErr(&buf)
	root.SetArgs(append([]string{"box", "--json", "--base-url", server.URL}, args...))

	err := root.Execute()
	var resp output.Response
	if buf.Len() > 0 {
		if decodeErr := json.Unmarshal(buf.Bytes(), &resp); decodeErr != nil {
			t.Fatalf("decode response: %v\n%s", decodeErr, buf.String())
		}
	}
	return resp, err
}

func TestBoxMixedPostingKindsJSONContract(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet || r.URL.Path != "/imbox.json" {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{
			"id": 1,
			"kind": "imbox",
			"name": "Imbox",
			"postings": [
				{"id": 101, "kind": "topic", "summary": "Project update"},
				{"id": 102, "kind": "world/post", "summary": "Published note"}
			]
		}`))
	}))
	defer server.Close()

	resp, err := runBox(t, server, "imbox")
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	if resp.Summary != "1 email and 1 HEY World post in Imbox" {
		t.Errorf("summary = %q", resp.Summary)
	}
	if got := resp.Meta["posting_count"]; got != float64(2) {
		t.Errorf("posting_count = %v, want 2", got)
	}
	if got := resp.Meta["email_count"]; got != float64(1) {
		t.Errorf("email_count = %v, want 1", got)
	}
	if got := resp.Meta["world_post_count"]; got != float64(1) {
		t.Errorf("world_post_count = %v, want 1", got)
	}

	data, ok := resp.Data.(map[string]any)
	if !ok {
		t.Fatalf("data type = %T, want map[string]any", resp.Data)
	}
	postings, ok := data["postings"].([]any)
	if !ok || len(postings) != 2 {
		t.Fatalf("postings = %#v, want 2 entries", data["postings"])
	}
	first, _ := postings[0].(map[string]any)
	second, _ := postings[1].(map[string]any)
	if first["kind"] != "topic" || second["kind"] != "world/post" {
		t.Errorf("posting kinds = %q, %q", first["kind"], second["kind"])
	}
}

func TestPaginateBoxPostings_NoFlagsSinglePage(t *testing.T) {
	first := &generated.BoxShowResponse{
		Postings:       makePostings(30, 0),
		NextHistoryUrl: "https://app.hey.com/page2",
	}
	postings, nextURL, err := paginateBoxPostings(context.Background(), first, 0, false, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(postings) != 30 {
		t.Errorf("expected 30 postings, got %d", len(postings))
	}
	if nextURL == "" {
		t.Error("expected non-empty nextURL when next_history_url is present")
	}
}

func TestPaginateBoxPostings_AllFlag(t *testing.T) {
	first := &generated.BoxShowResponse{
		Postings:       makePostings(30, 0),
		NextHistoryUrl: "https://app.hey.com/page2",
	}
	pages := []generated.BoxShowResponse{
		{Postings: makePostings(30, 30), NextHistoryUrl: "https://app.hey.com/page3"},
		{Postings: makePostings(15, 60)},
	}

	postings, nextURL, err := paginateBoxPostings(context.Background(), first, 0, true, mockFetcher(pages))
	if err != nil {
		t.Fatal(err)
	}
	if len(postings) != 75 {
		t.Errorf("expected 75 postings, got %d", len(postings))
	}
	if nextURL != "" {
		t.Errorf("expected empty nextURL when last page has no next URL, got %q", nextURL)
	}
}

func TestPaginateBoxPostings_LimitExceedsFirstPage(t *testing.T) {
	first := &generated.BoxShowResponse{
		Postings:       makePostings(30, 0),
		NextHistoryUrl: "https://app.hey.com/page2",
	}
	pages := []generated.BoxShowResponse{
		{Postings: makePostings(30, 30), NextHistoryUrl: "https://app.hey.com/page3"},
	}

	postings, nextURL, err := paginateBoxPostings(context.Background(), first, 50, false, mockFetcher(pages))
	if err != nil {
		t.Fatal(err)
	}
	if len(postings) != 60 {
		t.Errorf("expected 60 postings, got %d", len(postings))
	}
	if nextURL == "" {
		t.Error("expected non-empty nextURL when stopped by limit with more pages available")
	}
}

func TestPaginateBoxPostings_LimitSatisfiedByFirstPage(t *testing.T) {
	first := &generated.BoxShowResponse{
		Postings:       makePostings(30, 0),
		NextHistoryUrl: "https://app.hey.com/page2",
	}

	postings, nextURL, err := paginateBoxPostings(context.Background(), first, 10, false, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(postings) != 30 {
		t.Errorf("expected 30 postings (full first page), got %d", len(postings))
	}
	if nextURL == "" {
		t.Error("expected non-empty nextURL")
	}
}

func TestPaginateBoxPostings_NoNextURL(t *testing.T) {
	first := &generated.BoxShowResponse{
		Postings: makePostings(10, 0),
	}

	postings, nextURL, err := paginateBoxPostings(context.Background(), first, 0, true, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(postings) != 10 {
		t.Errorf("expected 10 postings, got %d", len(postings))
	}
	if nextURL != "" {
		t.Errorf("expected empty nextURL when no next URL, got %q", nextURL)
	}
}

func TestPaginateBoxPostings_EmptyPageStopsPagination(t *testing.T) {
	first := &generated.BoxShowResponse{
		Postings:       makePostings(30, 0),
		NextHistoryUrl: "https://app.hey.com/page2",
	}
	pages := []generated.BoxShowResponse{
		{Postings: nil},
	}

	postings, nextURL, err := paginateBoxPostings(context.Background(), first, 0, true, mockFetcher(pages))
	if err != nil {
		t.Fatal(err)
	}
	if len(postings) != 30 {
		t.Errorf("expected 30 postings, got %d", len(postings))
	}
	if nextURL != "" {
		t.Errorf("expected empty nextURL after empty page, got %q", nextURL)
	}
}

func TestPaginateBoxPostings_NilFetchReturnsError(t *testing.T) {
	first := &generated.BoxShowResponse{
		Postings:       makePostings(30, 0),
		NextHistoryUrl: "https://app.hey.com/page2",
	}
	_, _, err := paginateBoxPostings(context.Background(), first, 0, true, nil)
	if err == nil {
		t.Fatal("expected error when fetch is nil and pagination is required")
	}
}

func TestValidateSameOrigin(t *testing.T) {
	tests := []struct {
		name    string
		base    string
		target  string
		wantErr bool
	}{
		{"same origin", "https://app.hey.com", "https://app.hey.com/page2", false},
		{"different host", "https://app.hey.com", "https://evil.com/page2", true},
		{"different scheme", "https://app.hey.com", "http://app.hey.com/page2", true},
		{"with port match", "https://app.hey.com:443", "https://app.hey.com:443/page2", false},
		{"port mismatch", "https://app.hey.com:443", "https://app.hey.com:8080/page2", true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := validateSameOrigin(tt.base, tt.target)
			if (err != nil) != tt.wantErr {
				t.Errorf("validateSameOrigin(%q, %q) error = %v, wantErr %v", tt.base, tt.target, err, tt.wantErr)
			}
		})
	}
}

func TestBoxTruncationNotice(t *testing.T) {
	tests := []struct {
		name    string
		shown   int
		fetched int
		hasMore bool
		all     bool
		want    string
	}{
		{"client truncated", 10, 30, false, false, "Showing 10 of 30 results. Use --all to see everything."},
		{"more pages available", 30, 30, true, false, "Showing 30 results. More available; use --all to fetch all."},
		{"all shown no more", 30, 30, false, false, ""},
		{"truncated with more", 10, 30, true, false, "Showing 10 of 30 results. Use --all to see everything."},
		{"all flag pagination capped", 30, 30, true, true, "Showing 30 results. Pagination limit reached; not all results could be fetched."},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := boxTruncationNotice(tt.shown, tt.fetched, tt.hasMore, tt.all)
			if got != tt.want {
				t.Errorf("boxTruncationNotice(%d, %d, %v, %v) = %q, want %q", tt.shown, tt.fetched, tt.hasMore, tt.all, got, tt.want)
			}
		})
	}
}
