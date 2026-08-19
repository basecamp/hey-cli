package cmd

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/basecamp/hey-cli/internal/output"
)

func runTopicControl(t *testing.T, server *httptest.Server, args ...string) (output.Response, error) {
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
	root.SetArgs(append([]string{"--json", "--base-url", server.URL}, args...))

	err := root.Execute()
	var resp output.Response
	if buf.Len() > 0 {
		if decodeErr := json.Unmarshal(buf.Bytes(), &resp); decodeErr != nil {
			t.Fatalf("decode response: %v\n%s", decodeErr, buf.String())
		}
	}
	return resp, err
}

func TestTopicControlActions(t *testing.T) {
	tests := []struct {
		name    string
		args    []string
		method  string
		path    string
		summary string
	}{
		{name: "restore", args: []string{"restore", "123"}, method: http.MethodPut, path: "/topics/123/status/active.json", summary: "Topic 123 restored to active mail"},
		{name: "mark spam", args: []string{"mark-spam", "456"}, method: http.MethodPut, path: "/entries/456/status/spam.json", summary: "Entry 456 marked as spam"},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Method != test.method || r.URL.Path != test.path {
					t.Errorf("request = %s %s", r.Method, r.URL.Path)
				}
				w.WriteHeader(http.StatusNoContent)
			}))
			defer server.Close()

			resp, err := runTopicControl(t, server, test.args...)
			if err != nil {
				t.Fatalf("execute: %v", err)
			}
			if resp.Summary != test.summary {
				t.Errorf("summary = %q, want %q", resp.Summary, test.summary)
			}
		})
	}
}

func TestTopicControlHelpWarnsAboutMailboxChanges(t *testing.T) {
	root := newRootCmd()
	for _, name := range []string{"restore", "mark-spam"} {
		command, _, err := root.Find([]string{name})
		if err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(command.Long, "changes mailbox state") || !strings.Contains(command.Long, "confirm the exact") {
			t.Errorf("%s help does not explain the state change: %q", name, command.Long)
		}
	}
}

func TestTopicControlsRejectInvalidInputWithoutRequest(t *testing.T) {
	var requests atomic.Int64
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		requests.Add(1)
		w.WriteHeader(http.StatusNoContent)
	}))
	defer server.Close()

	tests := []struct {
		args    []string
		message string
	}{
		{args: []string{"restore", "0"}, message: "invalid topic ID"},
		{args: []string{"mark-spam", "nope"}, message: "invalid entry ID"},
	}

	for _, test := range tests {
		_, err := runTopicControl(t, server, test.args...)
		if err == nil || !strings.Contains(err.Error(), test.message) {
			t.Fatalf("%v error = %v, want %q", test.args, err, test.message)
		}
	}
	if requests.Load() != 0 {
		t.Fatalf("requests = %d, want 0", requests.Load())
	}
}

func TestTopicControlsRejectListOnlyFormatsBeforeRequest(t *testing.T) {
	var requests atomic.Int64
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		requests.Add(1)
		w.WriteHeader(http.StatusNoContent)
	}))
	defer server.Close()

	for _, args := range [][]string{
		{"restore", "123", "--ids-only"},
		{"mark-spam", "456", "--count"},
	} {
		_, err := runTopicControl(t, server, args...)
		if err == nil || !strings.Contains(err.Error(), "requires list data") {
			t.Fatalf("%v error = %v, want list format rejection", args, err)
		}
	}
	if requests.Load() != 0 {
		t.Fatalf("requests = %d, want 0", requests.Load())
	}
}
