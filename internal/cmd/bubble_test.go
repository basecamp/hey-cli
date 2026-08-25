package cmd

import (
	"bytes"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"

	"github.com/basecamp/hey-cli/internal/apierr"
	"github.com/basecamp/hey-cli/internal/output"
)

type recordedBubble struct {
	method     string
	path       string
	postingIDs []int64
	slot       string
	date       string
	status     int
	requests   int
}

func bubbleServer(t *testing.T) (*httptest.Server, *recordedBubble) {
	t.Helper()
	recorded := &recordedBubble{status: http.StatusNoContent}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		recorded.requests++
		recorded.method = r.Method
		recorded.path = r.URL.Path

		switch {
		case r.URL.Path == "/postings/bulk_bubble_up_now.json":
			var body struct {
				PostingIDs []int64 `json:"posting_ids"`
			}
			_ = json.NewDecoder(r.Body).Decode(&body)
			recorded.postingIDs = body.PostingIDs
			w.WriteHeader(recorded.status)
		case r.URL.Path == "/postings/bubble_up.json" && r.Method == http.MethodPost:
			var body struct {
				PostingIDs []int64 `json:"posting_ids"`
				Slot       string  `json:"slot"`
				Date       string  `json:"date"`
			}
			_ = json.NewDecoder(r.Body).Decode(&body)
			recorded.postingIDs = body.PostingIDs
			recorded.slot = body.Slot
			recorded.date = body.Date
			w.WriteHeader(recorded.status)
		case r.URL.Path == "/postings/bubble_up.json":
			for _, part := range strings.Split(r.URL.Query().Get("posting_ids"), ",") {
				id, err := strconv.ParseInt(part, 10, 64)
				if err != nil {
					t.Errorf("posting_ids carries %q, want integers", part)
					continue
				}
				recorded.postingIDs = append(recorded.postingIDs, id)
			}
			w.WriteHeader(recorded.status)
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(server.Close)
	return server, recorded
}

func runBubble(t *testing.T, server *httptest.Server, args ...string) (output.Response, error) {
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
	root.SetArgs(append([]string{"bubble"}, append(args, "--json", "--base-url", server.URL)...))

	err := root.Execute()
	var resp output.Response
	if buf.Len() > 0 {
		_ = json.Unmarshal(buf.Bytes(), &resp)
	}
	return resp, err
}

func TestBubbleUpAndPop(t *testing.T) {
	tests := []struct {
		name     string
		args     []string
		method   string
		path     string
		wantIDs  []int64
		wantSlot string
		wantDate string
		summary  string
	}{
		{"up one now", []string{"up", "12345", "--now"}, http.MethodPost, "/postings/bulk_bubble_up_now.json", []int64{12345}, "", "", "1 thread bubbled up"},
		{"up multiple now", []string{"up", "12345", "67890", "--now"}, http.MethodPost, "/postings/bulk_bubble_up_now.json", []int64{12345, 67890}, "", "", "2 threads bubbled up"},
		{"up one on a date", []string{"up", "12345", "--on", "2026-09-04"}, http.MethodPost, "/postings/bubble_up.json", []int64{12345}, "custom", "2026-09-04", "1 thread will bubble up on 2026-09-04"},
		{"up multiple on a date", []string{"up", "12345", "67890", "--on", "2026-09-04"}, http.MethodPost, "/postings/bubble_up.json", []int64{12345, 67890}, "custom", "2026-09-04", "2 threads will bubble up on 2026-09-04"},
		{"pop one", []string{"pop", "12345"}, http.MethodDelete, "/postings/bubble_up.json", []int64{12345}, "", "", "1 thread no longer bubbled up"},
		{"pop multiple", []string{"pop", "12345", "67890"}, http.MethodDelete, "/postings/bubble_up.json", []int64{12345, 67890}, "", "", "2 threads no longer bubbled up"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			server, recorded := bubbleServer(t)
			resp, err := runBubble(t, server, tt.args...)
			if err != nil {
				t.Fatalf("bubble %s failed: %v", tt.args[0], err)
			}
			if recorded.method != tt.method || recorded.path != tt.path {
				t.Errorf("request = %s %s, want %s %s", recorded.method, recorded.path, tt.method, tt.path)
			}
			if len(recorded.postingIDs) != len(tt.wantIDs) {
				t.Fatalf("posting_ids = %v, want %v", recorded.postingIDs, tt.wantIDs)
			}
			for i, want := range tt.wantIDs {
				if recorded.postingIDs[i] != want {
					t.Errorf("posting_ids[%d] = %d, want %d", i, recorded.postingIDs[i], want)
				}
			}
			if recorded.slot != tt.wantSlot {
				t.Errorf("slot = %q, want %q", recorded.slot, tt.wantSlot)
			}
			if recorded.date != tt.wantDate {
				t.Errorf("date = %q, want %q", recorded.date, tt.wantDate)
			}
			if resp.Summary != tt.summary {
				t.Errorf("summary = %q, want %q", resp.Summary, tt.summary)
			}
		})
	}
}

func TestBubbleUpRequiresExactlyOneOfNowAndOn(t *testing.T) {
	tests := map[string]struct {
		args    []string
		message string
	}{
		"neither": {[]string{"up", "12345"}, "either --now or --on <date> is required"},
		"both":    {[]string{"up", "12345", "--now", "--on", "2026-09-04"}, "--now and --on are mutually exclusive"},
	}

	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			server, recorded := bubbleServer(t)
			_, err := runBubble(t, server, tt.args...)
			var cliErr *apierr.Error
			if !errors.As(err, &cliErr) || cliErr.Code != "usage" {
				t.Fatalf("bubble up should produce a usage error, got %v", err)
			}
			if cliErr.Message != tt.message {
				t.Errorf("message = %q, want %q", cliErr.Message, tt.message)
			}
			if recorded.requests != 0 {
				t.Errorf("usage error made %d requests", recorded.requests)
			}
		})
	}
}

func TestBubbleUpRejectsInvalidDateBeforeRequest(t *testing.T) {
	server, recorded := bubbleServer(t)
	_, err := runBubble(t, server, "up", "12345", "--on", "next tuesday")
	var cliErr *apierr.Error
	if !errors.As(err, &cliErr) || cliErr.Code != "usage" {
		t.Fatalf("invalid date should produce a usage error, got %v", err)
	}
	if !strings.Contains(cliErr.Message, "invalid on date") {
		t.Errorf("message = %q, want it to name the on date", cliErr.Message)
	}
	if recorded.requests != 0 {
		t.Errorf("invalid date made %d requests", recorded.requests)
	}
}

func TestBubbleUpAndPopRequireIDs(t *testing.T) {
	for _, subcommand := range []string{"up", "pop"} {
		t.Run(subcommand, func(t *testing.T) {
			server, recorded := bubbleServer(t)
			_, err := runBubble(t, server, subcommand)
			if err == nil || !strings.Contains(err.Error(), "Usage:") {
				t.Fatalf("missing ID should produce a usage error, got %v", err)
			}
			if recorded.requests != 0 {
				t.Errorf("missing ID made %d requests", recorded.requests)
			}
		})
	}
}

func TestBubbleUpAndPopRejectInvalidIDsBeforeRequest(t *testing.T) {
	tests := map[string][]string{
		"up":  {"up", "not-an-id", "--now"},
		"pop": {"pop", "not-an-id"},
	}

	for subcommand, args := range tests {
		t.Run(subcommand, func(t *testing.T) {
			server, recorded := bubbleServer(t)
			_, err := runBubble(t, server, args...)
			var cliErr *apierr.Error
			if !errors.As(err, &cliErr) || cliErr.Code != "usage" {
				t.Fatalf("invalid ID should produce a usage error, got %v", err)
			}
			if recorded.requests != 0 {
				t.Errorf("invalid ID made %d requests", recorded.requests)
			}
		})
	}
}

func TestBubbleUpAndPopReportServerFailures(t *testing.T) {
	tests := map[string][]string{
		"up now":       {"up", "12345", "--now"},
		"up on a date": {"up", "12345", "--on", "2026-09-04"},
		"pop":          {"pop", "12345"},
	}

	for subcommand, args := range tests {
		t.Run(subcommand, func(t *testing.T) {
			server, recorded := bubbleServer(t)
			recorded.status = http.StatusUnprocessableEntity
			if _, err := runBubble(t, server, args...); err == nil {
				t.Fatal("server failure should be reported")
			}
		})
	}
}
