package cmd

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/basecamp/hey-cli/internal/output"
)

type recordedScreenerRequest struct {
	Method string
	Path   string
	Query  string
	Body   []byte
}

type recordedScreener struct {
	mu       sync.Mutex
	requests []recordedScreenerRequest
	statuses map[string]int
}

func (r *recordedScreener) snapshot() []recordedScreenerRequest {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]recordedScreenerRequest(nil), r.requests...)
}

func screenerServer(t *testing.T) (*httptest.Server, *recordedScreener) {
	t.Helper()
	recorded := &recordedScreener{statuses: make(map[string]int)}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		body := new(bytes.Buffer)
		_, _ = body.ReadFrom(req.Body)
		recorded.mu.Lock()
		recorded.requests = append(recorded.requests, recordedScreenerRequest{
			Method: req.Method, Path: req.URL.Path, Query: req.URL.RawQuery, Body: body.Bytes(),
		})
		status := recorded.statuses[req.Method+" "+req.URL.Path]
		recorded.mu.Unlock()

		w.Header().Set("Content-Type", "application/json")
		if status != 0 {
			w.WriteHeader(status)
			_, _ = w.Write([]byte(`{"message":"refused"}`))
			return
		}

		switch {
		case req.Method == http.MethodGet && req.URL.Path == "/clearances.json":
			if req.URL.Query().Get("include_clearances") != "true" {
				_, _ = w.Write([]byte(`{"pending_clearances_count":3,"signed_stream_name":"abc"}`))
				return
			}
			if req.URL.Query().Get("page") == "2" {
				_, _ = w.Write([]byte(`{"pending_clearances_count":3,"clearances":[]}`))
				return
			}
			_, _ = w.Write([]byte(`{"pending_clearances_count":3,"clearances":[
				{"id":91,"status":"pending",
				 "petitioner":{"id":51,"name":"Hollis Heimboch","email_address":"hollis@example.com"},
				 "most_recent_entry":{"id":71,"subject":"New numbers!","topic_id":81,"summary":"The latest sales figures"}}]}`))
		case req.Method == http.MethodPatch && req.URL.Path == "/clearances/91.json":
			_, _ = w.Write([]byte(`{"id":91,"status":"approved","petitioner":{"id":51,"name":"Hollis Heimboch","email_address":"hollis@example.com"}}`))
		case req.Method == http.MethodPatch && req.URL.Path == "/clearances/bulk.json":
			_, _ = w.Write([]byte(`{"clearances":[
				{"id":91,"status":"denied","petitioner":{"id":51,"name":"Hollis Heimboch"}},
				{"id":92,"status":"denied","petitioner":{"id":52,"name":"Scott Ellrod"}}]}`))
		case req.Method == http.MethodPost && req.URL.Path == "/clearances/punt.json":
			w.WriteHeader(http.StatusAccepted)
		case req.Method == http.MethodGet && req.URL.Path == "/my/clearances.json":
			if req.URL.Query().Get("page") == "2" {
				_, _ = w.Write([]byte(`{"clearances":[]}`))
				return
			}
			_, _ = w.Write([]byte(`{"clearances":[
				{"id":91,"status":"approved","updated_at":"2026-08-19T10:00:00Z","petitioner":{"id":51,"name":"Glenn","email_address":"glenn@example.com"}},
				{"id":92,"status":"denied","updated_at":"2026-08-18T10:00:00Z","petitioner":{"id":52,"name":"Spammer","email_address":"spam@example.com"}}]}`))
		case req.Method == http.MethodGet && req.URL.Path == "/feedbox.json":
			_, _ = w.Write([]byte(`{"id":7,"name":"The Feed","postings":[]}`))
		default:
			w.WriteHeader(http.StatusNotFound)
			_, _ = w.Write([]byte(`{"message":"unexpected request"}`))
		}
	}))
	t.Cleanup(server.Close)
	return server, recorded
}

func runScreener(t *testing.T, server *httptest.Server, args ...string) (output.Response, error) {
	t.Helper()
	t.Setenv("HEY_TOKEN", "test-token")
	t.Setenv("HEY_NO_KEYRING", "1")
	t.Setenv("HEY_BASE_URL", "")
	tmpDir := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", tmpDir)
	t.Setenv("XDG_STATE_HOME", tmpDir)
	t.Setenv("XDG_CACHE_HOME", tmpDir)

	root := newRootCmd()
	var stdout bytes.Buffer
	root.SetOut(&stdout)
	root.SetErr(&stdout)
	root.SetArgs(append([]string{"screener", "--json", "--base-url", server.URL}, args...))
	err := root.Execute()
	var resp output.Response
	if stdout.Len() > 0 {
		_ = json.Unmarshal(stdout.Bytes(), &resp)
	}
	return resp, err
}

func decodeScreenerData[T any](t *testing.T, data any) T {
	t.Helper()
	encoded, err := json.Marshal(data)
	if err != nil {
		t.Fatal(err)
	}
	var result T
	if err := json.Unmarshal(encoded, &result); err != nil {
		t.Fatal(err)
	}
	return result
}

func TestScreenerListCarriesTheSenderAndWhatTheySent(t *testing.T) {
	server, recorded := screenerServer(t)

	resp, err := runScreener(t, server, "list")
	if err != nil {
		t.Fatalf("list failed: %v; requests=%+v", err, recorded.snapshot())
	}

	pending := decodeScreenerData[[]pendingClearance](t, resp.Data)
	if len(pending) != 1 {
		t.Fatalf("pending = %+v", pending)
	}
	if pending[0].ID != 91 || pending[0].Name != "Hollis Heimboch" || pending[0].Subject != "New numbers!" || pending[0].TopicID != 81 {
		t.Errorf("pending[0] = %+v", pending[0])
	}
	if resp.Summary != "1 sender waiting" {
		t.Errorf("summary = %q", resp.Summary)
	}

	requests := recorded.snapshot()
	if len(requests) != 1 || !strings.Contains(requests[0].Query, "include_clearances=true") {
		t.Errorf("requests = %+v", requests)
	}
}

// The queue costs the server real work, so asking only for the number must not drag it
// along.
func TestScreenerListCountAsksForTheCountAlone(t *testing.T) {
	server, recorded := screenerServer(t)

	resp, err := runScreener(t, server, "list", "--count")
	if err != nil {
		t.Fatalf("count failed: %v", err)
	}

	result := decodeScreenerData[map[string]int](t, resp.Data)
	if result["pending_count"] != 3 {
		t.Errorf("result = %+v", result)
	}
	requests := recorded.snapshot()
	if len(requests) != 1 || strings.Contains(requests[0].Query, "include_clearances") {
		t.Errorf("expected one request without include_clearances, got %+v", requests)
	}
}

func TestScreenerListPaginates(t *testing.T) {
	server, recorded := screenerServer(t)

	if _, err := runScreener(t, server, "list", "--all"); err != nil {
		t.Fatalf("list --all failed: %v", err)
	}

	requests := recorded.snapshot()
	if len(requests) != 2 || !strings.Contains(requests[0].Query, "page=1") || !strings.Contains(requests[1].Query, "page=2") {
		t.Errorf("requests = %+v", requests)
	}
}

func TestScreenerIn(t *testing.T) {
	server, recorded := screenerServer(t)

	resp, err := runScreener(t, server, "in", "91")
	if err != nil {
		t.Fatalf("in failed: %v; requests=%+v", err, recorded.snapshot())
	}

	results := decodeScreenerData[[]screenedResult](t, resp.Data)
	if len(results) != 1 || results[0].Status != "approved" || results[0].Name != "Hollis Heimboch" {
		t.Errorf("results = %+v", results)
	}
	if resp.Summary != "1 sender screened in" {
		t.Errorf("summary = %q", resp.Summary)
	}

	requests := recorded.snapshot()
	if len(requests) != 1 || requests[0].Method != http.MethodPatch || requests[0].Path != "/clearances/91.json" {
		t.Fatalf("requests = %+v", requests)
	}
	var body map[string]any
	_ = json.Unmarshal(requests[0].Body, &body)
	if body["status"] != "approved" {
		t.Errorf("body = %+v", body)
	}
	if _, sent := body["designation_box_id"]; sent {
		t.Errorf("expected no designation box, got %+v", body)
	}
}

func TestScreenerInResolvesTheDeliveryBoxByName(t *testing.T) {
	server, recorded := screenerServer(t)

	if _, err := runScreener(t, server, "in", "91", "--box", "The Feed", "--seen"); err != nil {
		t.Fatalf("in --box failed: %v; requests=%+v", err, recorded.snapshot())
	}

	requests := recorded.snapshot()
	if len(requests) != 2 || requests[0].Path != "/feedbox.json" {
		t.Fatalf("expected the box to be resolved first, got %+v", requests)
	}
	var body map[string]any
	_ = json.Unmarshal(requests[1].Body, &body)
	if body["designation_box_id"] != float64(7) {
		t.Errorf("expected the resolved box id, got %+v", body["designation_box_id"])
	}
	if body["mark_topics_as_seen"] != true {
		t.Errorf("expected mark_topics_as_seen, got %+v", body)
	}
}

// The bulk endpoint takes neither a delivery box nor a seen flag, so combining them with
// several senders would silently drop them.
func TestScreenerInRefusesPerSenderFlagsForManySenders(t *testing.T) {
	server, recorded := screenerServer(t)

	if _, err := runScreener(t, server, "in", "91", "92", "--box", "The Feed"); err == nil {
		t.Fatal("expected --box with several senders to be refused")
	}
	if _, err := runScreener(t, server, "in", "91", "92", "--seen"); err == nil {
		t.Fatal("expected --seen with several senders to be refused")
	}
	if requests := recorded.snapshot(); len(requests) != 0 {
		t.Errorf("expected no request to be sent, got %+v", requests)
	}
}

func TestScreenerOutSeveralSendersGoesThroughBulk(t *testing.T) {
	server, recorded := screenerServer(t)

	resp, err := runScreener(t, server, "out", "91", "92", "--spam")
	if err != nil {
		t.Fatalf("out failed: %v; requests=%+v", err, recorded.snapshot())
	}

	results := decodeScreenerData[[]screenedResult](t, resp.Data)
	if len(results) != 2 {
		t.Fatalf("results = %+v", results)
	}
	if resp.Summary != "2 senders screened out" {
		t.Errorf("summary = %q", resp.Summary)
	}

	requests := recorded.snapshot()
	if len(requests) != 1 || requests[0].Path != "/clearances/bulk.json" {
		t.Fatalf("requests = %+v", requests)
	}
	var body map[string]any
	_ = json.Unmarshal(requests[0].Body, &body)
	if body["ids"] != "91,92" || body["status"] != "denied" || body["spam"] != true {
		t.Errorf("body = %+v", body)
	}
}

func TestScreenerRejectsBadClearanceIDs(t *testing.T) {
	server, recorded := screenerServer(t)

	for _, id := range []string{"abc", "0", "-1"} {
		if _, err := runScreener(t, server, "out", id); err == nil {
			t.Errorf("expected %q to be refused", id)
		}
	}
	if requests := recorded.snapshot(); len(requests) != 0 {
		t.Errorf("expected no request to be sent, got %+v", requests)
	}
}

func TestScreenerClear(t *testing.T) {
	server, recorded := screenerServer(t)

	resp, err := runScreener(t, server, "clear")
	if err != nil {
		t.Fatalf("clear failed: %v", err)
	}
	if resp.Summary != "Screener cleared" {
		t.Errorf("summary = %q", resp.Summary)
	}

	requests := recorded.snapshot()
	if len(requests) != 1 || requests[0].Method != http.MethodPost || requests[0].Path != "/clearances/punt.json" {
		t.Errorf("requests = %+v", requests)
	}
}

func TestScreenerHistoryShowsBothDecisions(t *testing.T) {
	server, recorded := screenerServer(t)

	resp, err := runScreener(t, server, "history")
	if err != nil {
		t.Fatalf("history failed: %v; requests=%+v", err, recorded.snapshot())
	}

	screened := decodeScreenerData[[]screenedClearance](t, resp.Data)
	if len(screened) != 2 {
		t.Fatalf("screened = %+v", screened)
	}
	if screened[0].Status != "approved" || screened[1].Status != "denied" {
		t.Errorf("screened = %+v", screened)
	}
	if screened[0].Name != "Glenn" || screened[0].Decided == "" {
		t.Errorf("screened[0] = %+v", screened[0])
	}
	if resp.Summary != "2 senders screened" {
		t.Errorf("summary = %q", resp.Summary)
	}

	requests := recorded.snapshot()
	if len(requests) != 1 || requests[0].Path != "/my/clearances.json" {
		t.Errorf("requests = %+v", requests)
	}
}

func TestScreenerSurfacesServerRefusals(t *testing.T) {
	server, recorded := screenerServer(t)
	recorded.statuses[http.MethodPatch+" /clearances/91.json"] = http.StatusForbidden

	if _, err := runScreener(t, server, "in", "91"); err == nil {
		t.Fatal("expected a forbidden response to be surfaced")
	}
}
