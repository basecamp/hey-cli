package cmd

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"strings"
	"testing"

	"github.com/basecamp/hey-sdk/go/pkg/generated"

	"github.com/basecamp/hey-cli/internal/apierr"
	"github.com/basecamp/hey-cli/internal/output"
)

type recordedSearch struct {
	queries  []url.Values
	requests int
	status   int
}

func searchServer(t *testing.T) (*httptest.Server, *recordedSearch) {
	t.Helper()
	recorded := &recordedSearch{status: http.StatusOK}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		recorded.requests++
		if r.URL.Path == "/advanced_search_filters.json" {
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{
				"refine_in":[{"title":"Imbox","value":"imbox"}],
				"refine_dates":[{"title":"Within the last 7 days","value":"last_7_days"}],
				"refine_labels":[{"title":"Receipts","value":"Receipts"}],
				"refine_attachments":[{"title":"PDFs","value":"pdfs"}]
			}`))
			return
		}
		if r.URL.Path != "/advanced_search.json" {
			http.NotFound(w, r)
			return
		}
		recorded.queries = append(recorded.queries, r.URL.Query())
		if recorded.status != http.StatusOK {
			w.WriteHeader(recorded.status)
			return
		}

		page, _ := strconv.Atoi(r.URL.Query().Get("page"))
		if page == 0 {
			page = 1
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(searchPageJSON(page)))
	}))
	t.Cleanup(server.Close)
	return server, recorded
}

func searchPageJSON(page int) string {
	if page > 2 {
		return `{"matches":[]}`
	}
	return `{"matches":[{
		"topic":{"id":331,"name":"Kitchen remodel","updated_at":"2026-08-18T12:00:00Z"},
		"posting_id":4471829,
		"entries":[{"id":5512,"summary":"The cabinets arrive on Tuesday","kind":"message","created_at":"2026-08-18T11:00:00Z","creator":{"id":7,"name":"Jane Doe","email_address":"jane@example.com"}}]
	}]}`
}

func runSearch(t *testing.T, server *httptest.Server, args ...string) (output.Response, error) {
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
	root.SetArgs(append([]string{"search", "--json", "--base-url", server.URL}, args...))

	err := root.Execute()
	var resp output.Response
	if buf.Len() > 0 {
		_ = json.Unmarshal(buf.Bytes(), &resp)
	}
	return resp, err
}

func decodeSearchResults(t *testing.T, data any) []searchResult {
	t.Helper()
	encoded, err := json.Marshal(data)
	if err != nil {
		t.Fatal(err)
	}
	var results []searchResult
	if err := json.Unmarshal(encoded, &results); err != nil {
		t.Fatal(err)
	}
	return results
}

func TestSearchSendsQueryAndEveryRefinement(t *testing.T) {
	server, recorded := searchServer(t)
	resp, err := runSearch(t, server,
		"planning",
		"--required", "budget timeline",
		"--any", "kitchen cabinets",
		"--none", "cancelled",
		"--exact", "final proposal",
		"--from", "jane@example.com",
		"--to", "mike@example.org",
		"--subject", "remodel",
		"--date", "last_30_days",
		"--in", "imbox",
		"--label", "Projects",
		"--attachment", "pdfs",
		"--page", "2",
	)
	if err != nil {
		t.Fatal(err)
	}
	if recorded.requests != 1 || len(recorded.queries) != 1 {
		t.Fatalf("requests = %d, queries = %d, want 1", recorded.requests, len(recorded.queries))
	}
	query := recorded.queries[0]
	wants := map[string]string{
		"q":                    "planning",
		"page":                 "2",
		"refine[required]":     "budget timeline",
		"refine[any]":          "kitchen cabinets",
		"refine[none]":         "cancelled",
		"refine[exact_phrase]": "final proposal",
		"refine[from]":         "jane@example.com",
		"refine[to]":           "mike@example.org",
		"refine[subject]":      "remodel",
		"refine[date]":         "last_30_days",
		"refine[in]":           "imbox",
		"refine[label]":        "Projects",
		"refine[attachment]":   "pdfs",
	}
	for key, want := range wants {
		if got := query.Get(key); got != want {
			t.Errorf("%s = %q, want %q", key, got, want)
		}
	}

	results := decodeSearchResults(t, resp.Data)
	if len(results) != 1 {
		t.Fatalf("results = %d, want 1", len(results))
	}
	if results[0].ID != 4471829 || results[0].TopicID != 331 || len(results[0].Messages) != 1 || results[0].Messages[0].Id != 5512 {
		t.Errorf("unexpected result: %+v", results[0])
	}
	if resp.Summary != "1 matching thread" {
		t.Errorf("summary = %q", resp.Summary)
	}
}

func TestSearchAcceptsRefinementWithoutFreeText(t *testing.T) {
	server, recorded := searchServer(t)
	if _, err := runSearch(t, server, "--from", "jane@example.com"); err != nil {
		t.Fatal(err)
	}
	if got := recorded.queries[0].Get("q"); got != "" {
		t.Errorf("q = %q, want empty", got)
	}
	if got := recorded.queries[0].Get("refine[from]"); got != "jane@example.com" {
		t.Errorf("refine[from] = %q", got)
	}
}

func TestSearchRequiresCriteriaBeforeRequest(t *testing.T) {
	server, recorded := searchServer(t)
	_, err := runSearch(t, server)
	var cliErr *apierr.Error
	if !errors.As(err, &cliErr) || cliErr.Code != "usage" {
		t.Fatalf("error = %v, want usage error", err)
	}
	if recorded.requests != 0 {
		t.Errorf("requests = %d, want 0", recorded.requests)
	}
}

func TestSearchValidatesPageDateAndBoxBeforeRequest(t *testing.T) {
	tests := [][]string{
		{"planning", "--page", "0"},
		{"planning", "--date", "yesterday"},
		{"planning", "--in", "set-aside"},
		// The kinds are plural, and the singular used to reach HEY as a 500.
		{"planning", "--attachment", "pdf"},
	}
	for _, args := range tests {
		t.Run(strings.Join(args, " "), func(t *testing.T) {
			server, recorded := searchServer(t)
			_, err := runSearch(t, server, args...)
			var cliErr *apierr.Error
			if !errors.As(err, &cliErr) || cliErr.Code != "usage" {
				t.Fatalf("error = %v, want usage error", err)
			}
			if recorded.requests != 0 {
				t.Errorf("requests = %d, want 0", recorded.requests)
			}
		})
	}
}

func TestSearchNamesTheAttachmentKindsItRefuses(t *testing.T) {
	server, _ := searchServer(t)
	_, err := runSearch(t, server, "planning", "--attachment", "pdf")
	if err == nil || !strings.Contains(err.Error(), "any, images, pdfs, calendar_invites, documents, spreadsheets, presentations, media, zip_files") {
		t.Fatalf("error = %v, want the valid attachment kinds", err)
	}
}

func TestSearchAcceptsEveryAttachmentKindHEYLists(t *testing.T) {
	for _, kind := range searchAttachmentKinds {
		t.Run(kind, func(t *testing.T) {
			server, _ := searchServer(t)
			if _, err := runSearch(t, server, "planning", "--attachment", kind); err != nil {
				t.Fatalf("search --attachment %s: %v", kind, err)
			}
		})
	}
}

func TestSearchAllFetchesUntilEmptyPage(t *testing.T) {
	server, recorded := searchServer(t)
	resp, err := runSearch(t, server, "planning", "--all")
	if err != nil {
		t.Fatal(err)
	}
	results := decodeSearchResults(t, resp.Data)
	if len(results) != 2 {
		t.Fatalf("results = %d, want 2", len(results))
	}
	if recorded.requests != 3 {
		t.Errorf("requests = %d, want 3", recorded.requests)
	}
	for i, want := range []string{"", "2", "3"} {
		if got := recorded.queries[i].Get("page"); got != want {
			t.Errorf("request %d page = %q, want %q", i+1, got, want)
		}
	}
}

func TestSearchAllReportsContinuationAtPageLimit(t *testing.T) {
	calls := 0
	lastPage := 0
	firstPage := 7
	read := func(_ context.Context, cursor string) (pageResult[generated.SearchMatch], error) {
		calls++
		lastPage, _ = strconv.Atoi(cursor)
		return pageResult[generated.SearchMatch]{
			Items:  []generated.SearchMatch{{Topic: generated.Topic{Id: 1}}},
			Cursor: strconv.Itoa(lastPage + 1),
		}, nil
	}

	first, err := read(t.Context(), strconv.Itoa(firstPage))
	if err != nil {
		t.Fatal(err)
	}
	collected, err := collectPages(t.Context(), first, pageRequest{All: true, MaxPages: maxSearchPages}, read)
	if err != nil {
		t.Fatal(err)
	}
	if calls != maxSearchPages || collected.Read != maxSearchPages || len(collected.Items) != maxSearchPages || !collected.Truncated || lastPage != 106 {
		t.Errorf("calls=%d read=%d matches=%d truncated=%v lastPage=%d", calls, collected.Read, len(collected.Items), collected.Truncated, lastPage)
	}
	want := "Search stopped after 100 pages. Continue with --page 107."
	if got := searchTruncationNotice(firstPage, collected.Read, collected.Truncated); got != want {
		t.Errorf("notice = %q, want %q", got, want)
	}
}

func TestSearchTruncationNoticeUsesStderrForDataOnlyFormats(t *testing.T) {
	notice := "Search stopped after 100 pages. Continue with --page 107."
	for _, format := range []output.Format{output.FormatQuiet, output.FormatIDs, output.FormatCount, output.FormatMarkdown} {
		if got := paginationNoticeForStderr(format, notice); got != "notice: "+notice {
			t.Errorf("format %d notice = %q", format, got)
		}
	}
	for _, format := range []output.Format{output.FormatJSON, output.FormatStyled} {
		if got := paginationNoticeForStderr(format, notice); got != "" {
			t.Errorf("format %d notice = %q, want empty", format, got)
		}
	}
	if got := paginationNoticeForStderr(output.FormatQuiet, ""); got != "" {
		t.Errorf("empty notice = %q", got)
	}
}

func TestSearchStopsAfterFirstPageByDefault(t *testing.T) {
	server, recorded := searchServer(t)
	if _, err := runSearch(t, server, "planning"); err != nil {
		t.Fatal(err)
	}
	if recorded.requests != 1 {
		t.Errorf("requests = %d, want 1", recorded.requests)
	}
}

func TestSearchReportsServerFailures(t *testing.T) {
	server, recorded := searchServer(t)
	recorded.status = http.StatusUnprocessableEntity
	if _, err := runSearch(t, server, "planning"); err == nil {
		t.Fatal("expected server failure")
	}
}

func TestSearchFiltersReturnsAvailableValues(t *testing.T) {
	server, recorded := searchServer(t)
	resp, err := runSearch(t, server, "filters")
	if err != nil {
		t.Fatal(err)
	}
	if recorded.requests != 1 {
		t.Fatalf("requests = %d, want 1", recorded.requests)
	}
	encoded, _ := json.Marshal(resp.Data)
	var filters searchFilters
	if err := json.Unmarshal(encoded, &filters); err != nil {
		t.Fatal(err)
	}
	if len(filters.Boxes) != 1 || filters.Boxes[0].Value != "imbox" {
		t.Errorf("boxes = %+v", filters.Boxes)
	}
	if len(filters.Dates) != 1 || len(filters.Labels) != 1 || len(filters.Attachments) != 1 {
		t.Errorf("filters = %+v", filters)
	}
}

func TestMakeSearchFiltersHandlesNilResponse(t *testing.T) {
	if got := makeSearchFilters(nil); len(got.Boxes)+len(got.Dates)+len(got.Labels)+len(got.Attachments) != 0 {
		t.Errorf("filters = %+v, want empty", got)
	}
}

func TestSearchSummaryUsesThreadTerminology(t *testing.T) {
	if got := searchSummary(1); got != "1 matching thread" {
		t.Errorf("searchSummary(1) = %q", got)
	}
	if got := searchSummary(2); got != "2 matching threads" {
		t.Errorf("searchSummary(2) = %q", got)
	}
}
