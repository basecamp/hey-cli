package cmd

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/basecamp/hey-cli/internal/models"
	"github.com/basecamp/hey-cli/internal/output"
)

func searchServer(t *testing.T, body string, checkRequest func(*http.Request)) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet || r.URL.Path != "/advanced_search" {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		if checkRequest != nil {
			checkRequest(r)
		}
		if got := r.Header.Get("Accept"); got != "text/html" {
			t.Errorf("Accept = %q, want text/html", got)
		}
		w.Header().Set("Content-Type", "text/html")
		_, _ = w.Write([]byte(body))
	}))
}

const searchResultsHTML = `<section class="search__results-group">
<article class="search-result posting">
  <time class="search-result__timestamp" datetime="2026-08-16T15:00:00Z">Today</time>
  <a href="/topics/42">
    <span class="search-topic__title">Quarterly planning notes</span>
    <div class="search-result__summary">Amanda shared the latest plan.</div>
  </a>
</article>
<a class="pagination-link" href="/advanced_search?q=quarterly+planning&amp;page=3">More</a>
</section>`

func runSearch(t *testing.T, server *httptest.Server, args ...string) (output.Response, string, error) {
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
	root.SetArgs(append([]string{"search", "--base-url", server.URL}, args...))

	err := root.Execute()
	var resp output.Response
	if strings.Contains(strings.Join(args, " "), "--json") && buf.Len() > 0 {
		_ = json.Unmarshal(buf.Bytes(), &resp)
	}
	return resp, buf.String(), err
}

func TestSearchReturnsTopicsAndForwardsQuery(t *testing.T) {
	server := searchServer(t, searchResultsHTML, func(r *http.Request) {
		if got := r.URL.Query().Get("q"); got != "quarterly planning" {
			t.Errorf("q = %q, want %q", got, "quarterly planning")
		}
		if got := r.URL.Query().Get("page"); got != "2" {
			t.Errorf("page = %q, want %q", got, "2")
		}
	})
	defer server.Close()

	resp, _, err := runSearch(t, server, "quarterly", "planning", "--page", "2", "--json")
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	if !resp.OK {
		t.Fatal("expected ok response")
	}
	if resp.Summary != `1 search result for "quarterly planning"` {
		t.Errorf("summary = %q", resp.Summary)
	}

	topicsJSON, err := json.Marshal(resp.Data)
	if err != nil {
		t.Fatal(err)
	}
	var topics []models.SearchResult
	if err := json.Unmarshal(topicsJSON, &topics); err != nil {
		t.Fatal(err)
	}
	if len(topics) != 1 || topics[0].ID != 42 {
		t.Fatalf("topics = %#v, want one topic with ID 42", topics)
	}
	if resp.Notice != "More results available. Use --page 3." {
		t.Errorf("notice = %q", resp.Notice)
	}
}

func TestSearchStyledOutput(t *testing.T) {
	server := searchServer(t, searchResultsHTML, nil)
	defer server.Close()

	_, stdout, err := runSearch(t, server, "quarterly", "planning", "--styled")
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	for _, want := range []string{"42", "Quarterly planning notes", "Amanda shared the latest plan.", "2026-08-16", "Use --page 3"} {
		if !strings.Contains(stdout, want) {
			t.Errorf("output missing %q:\n%s", want, stdout)
		}
	}
}

func TestSearchEmptyResults(t *testing.T) {
	server := searchServer(t, `<section class="search__results-group"></section>`, nil)
	defer server.Close()

	resp, _, err := runSearch(t, server, "missing phrase", "--json")
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	if resp.Summary != `0 search results for "missing phrase"` {
		t.Errorf("summary = %q", resp.Summary)
	}
}

func TestSearchRequiresQuery(t *testing.T) {
	server := searchServer(t, "", nil)
	defer server.Close()

	_, _, err := runSearch(t, server, "--json")
	if err == nil {
		t.Fatal("expected missing query to fail")
	}
	if !strings.Contains(err.Error(), "Usage:") {
		t.Errorf("error = %q, want usage error", err)
	}
}

func TestSearchRejectsInvalidPage(t *testing.T) {
	server := searchServer(t, "", nil)
	defer server.Close()

	_, _, err := runSearch(t, server, "quarterly planning", "--page", "0", "--json")
	if err == nil {
		t.Fatal("expected invalid page to fail")
	}
	if !strings.Contains(err.Error(), "--page must be at least 1") {
		t.Errorf("error = %q", err)
	}
}
