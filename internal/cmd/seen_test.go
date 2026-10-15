package cmd

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"sync"
	"testing"

	"github.com/basecamp/hey-cli/internal/apierr"
	"github.com/basecamp/hey-cli/internal/output"
)

type seenRequest struct {
	Method string
	Path   string
	Query  string
	Body   string
}

type recordedSeen struct {
	mu       sync.Mutex
	requests []seenRequest
	handler  http.HandlerFunc
}

func (s *recordedSeen) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	body, _ := io.ReadAll(r.Body)
	s.mu.Lock()
	s.requests = append(s.requests, seenRequest{r.Method, r.URL.Path, r.URL.RawQuery, string(body)})
	s.mu.Unlock()
	w.Header().Set("Content-Type", "application/json")
	if s.handler == nil {
		http.NotFound(w, r)
		return
	}
	s.handler(w, r)
}

func (s *recordedSeen) snapshot() []seenRequest {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]seenRequest(nil), s.requests...)
}

func TestSeenBoxQueuesTheResolvedBox(t *testing.T) {
	recorded := &recordedSeen{handler: func(w http.ResponseWriter, r *http.Request) {
		switch r.Method + " " + r.URL.Path {
		case "GET /paper_trail.json":
			_, _ = io.WriteString(w, `{"id":7,"name":"Paper Trail","postings":[{"id":101},{"id":102}],"next_history_url":"/paper_trail.json?page=next-cursor"}`)
		case "POST /boxes/7/observation.json":
			w.WriteHeader(http.StatusCreated)
		default:
			http.NotFound(w, r)
		}
	}}
	resp, err := runJSONCommand(t, recorded, "seen", "--box", "paper trail")
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	if !resp.OK || resp.Summary != "Queued marking Paper Trail as seen" || resp.Data != nil {
		t.Fatalf("response = %#v, want queued acknowledgment without posting data", resp)
	}
	want := []seenRequest{
		{Method: "GET", Path: "/paper_trail.json"},
		{Method: "POST", Path: "/boxes/7/observation.json"},
	}
	if got := recorded.snapshot(); !reflect.DeepEqual(got, want) {
		t.Fatalf("requests = %#v, want %#v", got, want)
	}
}

func TestSeenIDsSendOnlyOneBulkRequest(t *testing.T) {
	recorded := &recordedSeen{handler: func(w http.ResponseWriter, r *http.Request) {
		if r.Method == "POST" && r.URL.Path == "/postings/seen.json" {
			w.WriteHeader(http.StatusCreated)
			return
		}
		http.NotFound(w, r)
	}}
	resp, err := runJSONCommand(t, recorded, "seen", "12345", "67890", "12345")
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	if !resp.OK || resp.Summary != "2 threads marked as seen" || resp.Data != nil {
		t.Fatalf("response = %#v", resp)
	}
	want := []seenRequest{{Method: "POST", Path: "/postings/seen.json", Body: `{"posting_ids":[12345,67890]}`}}
	if got := recorded.snapshot(); !reflect.DeepEqual(got, want) {
		t.Fatalf("requests = %#v, want %#v", got, want)
	}
}

func TestSeenBoxResolution(t *testing.T) {
	for _, tc := range []struct {
		name   string
		target string
		reads  []seenRequest
	}{
		{"named", "PAPER TRAIL", []seenRequest{{Method: "GET", Path: "/paper_trail.json"}}},
		{"numeric", "7", []seenRequest{{Method: "GET", Path: "/boxes/7.json"}}},
		{"fallback", "Receipts", []seenRequest{{Method: "GET", Path: "/boxes.json"}, {Method: "GET", Path: "/boxes/7.json"}}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			recorded := &recordedSeen{handler: func(w http.ResponseWriter, r *http.Request) {
				switch r.Method + " " + r.URL.Path {
				case "GET /boxes.json":
					_, _ = io.WriteString(w, `[{"id":7,"kind":"trailbox","name":"Receipts"}]`)
				case "GET /paper_trail.json", "GET /boxes/7.json":
					_, _ = io.WriteString(w, `{"id":7,"name":"Paper Trail","postings":[]}`)
				case "POST /boxes/7/observation.json":
					w.WriteHeader(http.StatusCreated)
				default:
					http.NotFound(w, r)
				}
			}}
			resp, err := runJSONCommand(t, recorded, "seen", "--box", tc.target)
			if err != nil || !resp.OK || resp.Summary != "Queued marking Paper Trail as seen" {
				t.Fatalf("response = %#v, error = %v", resp, err)
			}
			want := append(tc.reads, seenRequest{Method: "POST", Path: "/boxes/7/observation.json"})
			if got := recorded.snapshot(); !reflect.DeepEqual(got, want) {
				t.Fatalf("requests = %#v, want %#v", got, want)
			}
		})
	}
}

func TestSeenTargetValidationMakesNoRequests(t *testing.T) {
	for _, tc := range []struct {
		name string
		args []string
		want string
	}{
		{"missing", nil, "Usage: hey seen (<box-item-id>... | --box <name|id>)"},
		{"empty box", []string{"--box", ""}, "--box requires a box name or ID"},
		{"blank box", []string{"--box", " \t"}, "--box requires a box name or ID"},
		{"conflicting", []string{"12345", "--box", "paper trail"}, "--box cannot be combined with box item IDs"},
		{"empty box with IDs", []string{"12345", "--box", ""}, "--box cannot be combined with box item IDs"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			recorded := &recordedSeen{}
			// Account selection would read /identity.json if validation ran too late.
			args := append([]string{"seen", "--account", "2"}, tc.args...)
			resp, err := runJSONCommand(t, recorded, args...)
			if err == nil || err.Error() != tc.want {
				t.Fatalf("error = %v, want %q", err, tc.want)
			}
			if resp.OK || resp.Summary != "" || len(recorded.snapshot()) != 0 {
				t.Fatalf("response = %#v, requests = %#v; want no success or requests", resp, recorded.snapshot())
			}
		})
	}
}

func TestSeenBoxLookupFailureDoesNotMutate(t *testing.T) {
	for _, tc := range []struct {
		name   string
		target string
		path   string
		status int
		body   string
		code   string
	}{
		{"unknown", "missing", "/boxes.json", 200, `[]`, apierr.CodeNotFound},
		{"refused", "paper trail", "/paper_trail.json", 404, `{}`, apierr.CodeNotFound},
		{"nil box", "paper trail", "/paper_trail.json", 204, "", apierr.CodeAPI},
		{"zero ID", "paper trail", "/paper_trail.json", 200, `{"id":0,"name":"Paper Trail"}`, apierr.CodeAPI},
		{"negative ID", "paper trail", "/paper_trail.json", 200, `{"id":-1,"name":"Paper Trail"}`, apierr.CodeAPI},
	} {
		t.Run(tc.name, func(t *testing.T) {
			recorded := &recordedSeen{handler: func(w http.ResponseWriter, r *http.Request) {
				if r.Method != "GET" || r.URL.Path != tc.path {
					http.NotFound(w, r)
					return
				}
				w.WriteHeader(tc.status)
				_, _ = io.WriteString(w, tc.body)
			}}
			resp, err := runJSONCommand(t, recorded, "seen", "--box", tc.target)
			var apiErr *apierr.Error
			if !errors.As(err, &apiErr) || apiErr.Code != tc.code {
				t.Fatalf("error = %v, want %s", err, tc.code)
			}
			if tc.code == apierr.CodeAPI && err.Error() != "HEY returned no valid box ID" {
				t.Fatalf("error = %v, want invalid box ID error", err)
			}
			if resp.OK || resp.Summary != "" {
				t.Fatalf("unexpected success: %#v", resp)
			}
			want := []seenRequest{{Method: "GET", Path: tc.path}}
			if got := recorded.snapshot(); !reflect.DeepEqual(got, want) {
				t.Fatalf("requests = %#v, want %#v", got, want)
			}
		})
	}
}

func TestSeenBoxMutationFailureIsNotRetriedOrReportedAsSuccess(t *testing.T) {
	recorded := &recordedSeen{handler: func(w http.ResponseWriter, r *http.Request) {
		switch r.Method + " " + r.URL.Path {
		case "GET /paper_trail.json":
			_, _ = io.WriteString(w, `{"id":7,"name":"Paper Trail"}`)
		case "POST /boxes/7/observation.json":
			w.WriteHeader(http.StatusServiceUnavailable)
			_, _ = io.WriteString(w, `{"error":"Service unavailable"}`)
		default:
			http.NotFound(w, r)
		}
	}}
	stdout, err := runFormattedCommand(t, recorded, []string{"--json"}, "seen", "--box", "paper trail")
	var apiErr *apierr.Error
	if !errors.As(err, &apiErr) || apiErr.HTTPStatus != http.StatusServiceUnavailable {
		t.Fatalf("error = %v, want API error with status 503", err)
	}
	if stdout != "" {
		t.Fatalf("unexpected success output: %q", stdout)
	}
	want := []seenRequest{
		{Method: "GET", Path: "/paper_trail.json"},
		{Method: "POST", Path: "/boxes/7/observation.json"},
	}
	if got := recorded.snapshot(); !reflect.DeepEqual(got, want) {
		t.Fatalf("requests = %#v, want %#v", got, want)
	}
}

// This checks the account filter on the wire, not which postings HEY changes.
func TestSeenBoxUsesTheSelectedAccountForLookupAndMutation(t *testing.T) {
	recorded := &recordedSeen{handler: func(w http.ResponseWriter, r *http.Request) {
		switch r.Method + " " + r.URL.Path {
		case "GET /paper_trail.json":
			_, _ = io.WriteString(w, `{"id":7,"name":"Paper Trail"}`)
		case "POST /boxes/7/observation.json":
			w.WriteHeader(http.StatusCreated)
		default:
			http.NotFound(w, r)
		}
	}}
	server := linkedAccountServer(t, recorded.ServeHTTP)
	stdout, err := runAccountsCLI(t, server, "seen", "--box", "paper trail", "--account", "2")
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	var resp output.Response
	if err := json.Unmarshal([]byte(stdout), &resp); err != nil || !resp.OK {
		t.Fatalf("response = %q, decode error = %v", stdout, err)
	}
	want := []seenRequest{
		{Method: "GET", Path: "/paper_trail.json", Query: "filtered_account_id=2"},
		{Method: "POST", Path: "/boxes/7/observation.json", Query: "filtered_account_id=2"},
	}
	if got := recorded.snapshot(); !reflect.DeepEqual(got, want) {
		t.Fatalf("requests = %#v, want %#v", got, want)
	}
}

func TestSeenBoxCancellationDuringLookupDoesNotMutate(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	recorded := &recordedSeen{handler: func(w http.ResponseWriter, r *http.Request) {
		if r.Method == "GET" && r.URL.Path == "/paper_trail.json" {
			cancel()
			_, _ = io.WriteString(w, `{"id":7,"name":"Paper Trail"}`)
			return
		}
		http.NotFound(w, r)
	}}
	server := httptest.NewServer(recorded)
	t.Cleanup(server.Close)
	t.Setenv("HEY_TOKEN", "test-token")
	t.Setenv("HEY_NO_KEYRING", "1")
	t.Setenv("HEY_BASE_URL", "")
	t.Setenv("HEY_ACCOUNT_ID", "")
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	root := newRootCmd()
	var stdout bytes.Buffer
	root.SetOut(&stdout)
	root.SetErr(io.Discard)
	root.SetArgs([]string{"seen", "--box", "paper trail", "--json", "--base-url", server.URL})
	if err := root.ExecuteContext(ctx); err == nil || !strings.Contains(err.Error(), context.Canceled.Error()) {
		t.Fatalf("error = %v, want cancellation", err)
	}
	if stdout.Len() != 0 {
		t.Fatalf("unexpected success output: %q", stdout.String())
	}
	want := []seenRequest{{Method: "GET", Path: "/paper_trail.json"}}
	if got := recorded.snapshot(); !reflect.DeepEqual(got, want) {
		t.Fatalf("requests = %#v, want %#v", got, want)
	}
}

func TestSeenBoxStyledAcknowledgmentSanitizesTheName(t *testing.T) {
	recorded := &recordedSeen{handler: func(w http.ResponseWriter, r *http.Request) {
		switch r.Method + " " + r.URL.Path {
		case "GET /paper_trail.json":
			_, _ = io.WriteString(w, `{"id":7,"name":"\u001b[31mPaper\u001b[0m\nTrail\u0007"}`)
		case "POST /boxes/7/observation.json":
			w.WriteHeader(http.StatusCreated)
		default:
			http.NotFound(w, r)
		}
	}}
	stdout, err := runStyledCommand(t, recorded, "seen", "--box", "paper trail")
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	if want := "Queued marking Paper Trail as seen.\n"; stdout != want {
		t.Fatalf("output = %q, want %q", stdout, want)
	}
}

func seenServer(t *testing.T) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == "POST" && (r.URL.Path == "/postings/seen" || r.URL.Path == "/postings/seen.json"):
			body, _ := io.ReadAll(r.Body)
			var req map[string]any
			_ = json.Unmarshal(body, &req)
			if req["posting_ids"] == nil {
				w.WriteHeader(400)
				return
			}
			w.WriteHeader(201)
		case r.Method == "POST" && (r.URL.Path == "/postings/unseen" || r.URL.Path == "/postings/unseen.json"):
			body, _ := io.ReadAll(r.Body)
			var req map[string]any
			_ = json.Unmarshal(body, &req)
			if req["posting_ids"] == nil {
				w.WriteHeader(400)
				return
			}
			w.WriteHeader(201)
		case r.Method == "GET" && r.URL.Path == "/me.json":
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(200)
			_, _ = w.Write([]byte(`{"id": 1}`))
		default:
			w.WriteHeader(404)
		}
	}))
}

func runSeen(t *testing.T, server *httptest.Server, args ...string) (output.Response, error) {
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
	root.SetArgs(append([]string{"seen", "--json", "--base-url", server.URL}, args...))

	err := root.Execute()
	var resp output.Response
	if buf.Len() > 0 {
		_ = json.Unmarshal(buf.Bytes(), &resp)
	}
	return resp, err
}

func runUnseen(t *testing.T, server *httptest.Server, args ...string) (output.Response, error) {
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
	root.SetArgs(append([]string{"unseen", "--json", "--base-url", server.URL}, args...))

	err := root.Execute()
	var resp output.Response
	if buf.Len() > 0 {
		_ = json.Unmarshal(buf.Bytes(), &resp)
	}
	return resp, err
}

func TestSeenSingle(t *testing.T) {
	server := seenServer(t)
	defer server.Close()

	resp, err := runSeen(t, server, "12345")
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	if resp.Summary != "1 thread marked as seen" {
		t.Errorf("summary = %q, want %q", resp.Summary, "1 thread marked as seen")
	}
}

func TestSeenMultiple(t *testing.T) {
	server := seenServer(t)
	defer server.Close()

	resp, err := runSeen(t, server, "12345", "67890")
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	if resp.Summary != "2 threads marked as seen" {
		t.Errorf("summary = %q, want %q", resp.Summary, "2 threads marked as seen")
	}
}

func TestSeenNoArgs(t *testing.T) {
	server := seenServer(t)
	defer server.Close()

	_, err := runSeen(t, server)
	if err == nil {
		t.Fatal("expected error for missing args")
	}
	if got := err.Error(); !strings.Contains(got, "Usage:") {
		t.Errorf("error = %q, want it to contain %q", got, "Usage:")
	}
}

func TestSeenInvalidID(t *testing.T) {
	server := seenServer(t)
	defer server.Close()

	_, err := runSeen(t, server, "abc")
	if err == nil {
		t.Fatal("expected error for non-numeric ID")
	}
	if err.Error() != "invalid ID: abc" {
		t.Errorf("error = %q, want %q", err, "invalid ID: abc")
	}
}

func TestSeenNonPositiveID(t *testing.T) {
	server := seenServer(t)
	defer server.Close()

	for _, arg := range []string{"0", "-12345"} {
		_, err := runSeen(t, server, "--", arg)
		if err == nil {
			t.Fatalf("expected error for ID %q", arg)
		}
		if err.Error() != "invalid ID: "+arg {
			t.Errorf("error = %q, want %q", err, "invalid ID: "+arg)
		}
	}
}

func TestSeenDuplicateIDs(t *testing.T) {
	server := seenServer(t)
	defer server.Close()

	resp, err := runSeen(t, server, "12345", "67890", "12345")
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	if resp.Summary != "2 threads marked as seen" {
		t.Errorf("summary = %q, want %q", resp.Summary, "2 threads marked as seen")
	}
}

func TestParseIntArgsDropsDuplicatesPreservingOrder(t *testing.T) {
	ids, err := parseIntArgs([]string{"67890", "12345", "67890", "12345", "24680"})
	if err != nil {
		t.Fatalf("parseIntArgs: %v", err)
	}
	want := []int64{67890, 12345, 24680}
	if len(ids) != len(want) {
		t.Fatalf("ids = %v, want %v", ids, want)
	}
	for i := range want {
		if ids[i] != want[i] {
			t.Fatalf("ids = %v, want %v", ids, want)
		}
	}
}

func TestUnseenSingle(t *testing.T) {
	server := seenServer(t)
	defer server.Close()

	resp, err := runUnseen(t, server, "12345")
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	if resp.Summary != "1 thread marked as unseen" {
		t.Errorf("summary = %q, want %q", resp.Summary, "1 thread marked as unseen")
	}
}

func TestUnseenMultiple(t *testing.T) {
	server := seenServer(t)
	defer server.Close()

	resp, err := runUnseen(t, server, "12345", "67890")
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	if resp.Summary != "2 threads marked as unseen" {
		t.Errorf("summary = %q, want %q", resp.Summary, "2 threads marked as unseen")
	}
}

func TestUnseenNoArgs(t *testing.T) {
	server := seenServer(t)
	defer server.Close()

	_, err := runUnseen(t, server)
	if err == nil {
		t.Fatal("expected error for missing args")
	}
	if got := err.Error(); !strings.Contains(got, "Usage:") {
		t.Errorf("error = %q, want it to contain %q", got, "Usage:")
	}
}

func TestUnseenInvalidID(t *testing.T) {
	server := seenServer(t)
	defer server.Close()

	_, err := runUnseen(t, server, "abc")
	if err == nil {
		t.Fatal("expected error for non-numeric ID")
	}
}
