package cmd

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/basecamp/hey-sdk/go/pkg/generated"

	"github.com/basecamp/hey-cli/internal/output"
)

func topicViewServer(t *testing.T, path, title string, requests *atomic.Int32) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		if r.Method != http.MethodGet || r.URL.Path != path {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		if got := r.URL.Query().Get("page"); got != "2" {
			t.Errorf("page = %q, want 2", got)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{
  "title": "` + title + `",
  "topics": [{
    "id": 42,
    "name": "Quarterly planning notes",
    "active_at": "2026-08-16T15:00:00Z",
    "creator": {"id": 7, "name": "Amanda Jones", "email_address": "amanda@example.com"}
  }]
}`))
	}))
}

func runTopicView(t *testing.T, server *httptest.Server, name string, args ...string) (string, error) {
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
	root.SetArgs(append([]string{name, "--base-url", server.URL}, args...))

	err := root.Execute()
	return buf.String(), err
}

func runTopicViewJSON(t *testing.T, server *httptest.Server, name string, args ...string) (output.Response, string, error) {
	t.Helper()
	stdout, err := runTopicView(t, server, name, append(args, "--json")...)
	var response output.Response
	if stdout != "" {
		if decodeErr := json.Unmarshal([]byte(stdout), &response); decodeErr != nil {
			t.Fatalf("decode response: %v\n%s", decodeErr, stdout)
		}
	}
	return response, stdout, err
}

func TestTopicViews(t *testing.T) {
	tests := []struct {
		name    string
		path    string
		title   string
		summary string
	}{
		{name: "sent", path: "/topics/sent.json", title: "Sent", summary: "1 email in Sent"},
		{name: "spammed", path: "/topics/spam.json", title: "Spam", summary: "1 email in Spam"},
		{name: "trashed", path: "/topics/trash.json", title: "Trash", summary: "1 email in Trash"},
		{name: "everything", path: "/topics/everything.json", title: "Everything", summary: "1 email in Everything"},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			var requests atomic.Int32
			server := topicViewServer(t, test.path, test.title, &requests)
			defer server.Close()

			resp, _, err := runTopicViewJSON(t, server, test.name, "--page", "2")
			if err != nil {
				t.Fatalf("execute: %v", err)
			}
			if !resp.OK || resp.Summary != test.summary {
				t.Fatalf("response = %#v", resp)
			}
			data, err := json.Marshal(resp.Data)
			if err != nil {
				t.Fatal(err)
			}
			var topics []generated.Topic
			if err := json.Unmarshal(data, &topics); err != nil {
				t.Fatal(err)
			}
			if len(topics) != 1 || topics[0].Id != 42 {
				t.Fatalf("topics = %#v", topics)
			}
			if requests.Load() != 1 {
				t.Errorf("requests = %d, want 1", requests.Load())
			}
		})
	}
}

func TestTopicViewStyledOutput(t *testing.T) {
	var requests atomic.Int32
	server := topicViewServer(t, "/topics/sent.json", "Sent", &requests)
	defer server.Close()

	stdout, err := runTopicView(t, server, "sent", "--page", "2", "--styled")
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	for _, want := range []string{"Sent", "42", "Quarterly planning notes", "Amanda Jones", "2026-08-16"} {
		if !strings.Contains(stdout, want) {
			t.Errorf("output missing %q:\n%s", want, stdout)
		}
	}
}

func TestTopicViewStyledOutputSanitizesUntrustedFields(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{
  "title": "Sent",
  "topics": [{
    "id": 42,
    "name": "Quarterly planning\u001b[31m\rnotes",
    "creator": {"id": 7, "name": "Amanda\u001b[2J\nJones"}
  }]
}`))
	}))
	defer server.Close()

	stdout, err := runTopicView(t, server, "sent", "--styled")
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	for _, unsafe := range []string{"\x1b[31m", "\x1b[2J", "\r", "\nJones"} {
		if strings.Contains(stdout, unsafe) {
			t.Errorf("styled output contains unsafe text %q:\n%s", unsafe, stdout)
		}
	}
}

func TestTopicViewsRejectInvalidPageBeforeRequest(t *testing.T) {
	for _, name := range []string{"sent", "spammed", "trashed", "everything"} {
		t.Run(name, func(t *testing.T) {
			var requests atomic.Int32
			server := topicViewServer(t, "/unused", "Unused", &requests)
			defer server.Close()

			_, err := runTopicView(t, server, name, "--page", "0", "--json")
			if err == nil || !strings.Contains(err.Error(), "--page must be at least 1") {
				t.Fatalf("error = %v", err)
			}
			if requests.Load() != 0 {
				t.Errorf("requests = %d, want 0", requests.Load())
			}
		})
	}
}

func TestTopicViewEmptyJSONUsesArray(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"title":"Spam","topics":null}`))
	}))
	defer server.Close()

	resp, stdout, err := runTopicViewJSON(t, server, "spammed")
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	if resp.Summary != "0 emails in Spam" {
		t.Errorf("summary = %q", resp.Summary)
	}
	data, err := json.Marshal(resp.Data)
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != "[]" {
		t.Fatalf("empty JSON data = %s, want []\n%s", data, stdout)
	}
}
