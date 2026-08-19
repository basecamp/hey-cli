package cmd

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/basecamp/hey-cli/internal/apierr"
	"github.com/basecamp/hey-cli/internal/output"
)

type recordedIgnoring struct {
	method     string
	path       string
	postingIDs []int64
	status     int
	requests   int
}

func ignoringServer(t *testing.T) (*httptest.Server, *recordedIgnoring) {
	t.Helper()
	recorded := &recordedIgnoring{status: http.StatusNoContent}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		recorded.requests++
		recorded.method = r.Method
		recorded.path = r.URL.Path

		if r.Method == http.MethodDelete {
			for _, value := range strings.Split(r.URL.Query().Get("posting_ids"), ",") {
				if value == "" {
					continue
				}
				var id int64
				_, _ = fmt.Sscan(value, &id)
				recorded.postingIDs = append(recorded.postingIDs, id)
			}
		} else {
			var body struct {
				PostingIDs []int64 `json:"posting_ids"`
			}
			_ = json.NewDecoder(r.Body).Decode(&body)
			recorded.postingIDs = body.PostingIDs
		}

		if r.URL.Path != "/postings/mutings.json" {
			http.NotFound(w, r)
			return
		}
		w.WriteHeader(recorded.status)
	}))
	t.Cleanup(server.Close)
	return server, recorded
}

func runIgnoring(t *testing.T, server *httptest.Server, command string, args ...string) (output.Response, error) {
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
	root.SetArgs(append([]string{command, "--json", "--base-url", server.URL}, args...))

	err := root.Execute()
	var resp output.Response
	if buf.Len() > 0 {
		_ = json.Unmarshal(buf.Bytes(), &resp)
	}
	return resp, err
}

func TestIgnoreAndStopIgnoring(t *testing.T) {
	tests := []struct {
		name    string
		command string
		method  string
		args    []string
		summary string
	}{
		{"ignore one", "ignore", http.MethodPost, []string{"12345"}, "1 thread ignored"},
		{"ignore multiple", "ignore", http.MethodPost, []string{"12345", "67890"}, "2 threads ignored"},
		{"stop ignoring one", "stop-ignoring", http.MethodDelete, []string{"12345"}, "Stopped ignoring 1 thread"},
		{"stop ignoring multiple", "stop-ignoring", http.MethodDelete, []string{"12345", "67890"}, "Stopped ignoring 2 threads"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			server, recorded := ignoringServer(t)
			resp, err := runIgnoring(t, server, tt.command, tt.args...)
			if err != nil {
				t.Fatalf("%s failed: %v", tt.command, err)
			}
			if recorded.method != tt.method || recorded.path != "/postings/mutings.json" {
				t.Errorf("request = %s %s, want %s /postings/mutings.json", recorded.method, recorded.path, tt.method)
			}
			if len(recorded.postingIDs) != len(tt.args) {
				t.Fatalf("posting_ids = %v, want %d IDs", recorded.postingIDs, len(tt.args))
			}
			for i, want := range []int64{12345, 67890}[:len(tt.args)] {
				if recorded.postingIDs[i] != want {
					t.Errorf("posting_ids[%d] = %d, want %d", i, recorded.postingIDs[i], want)
				}
			}
			if resp.Summary != tt.summary {
				t.Errorf("summary = %q, want %q", resp.Summary, tt.summary)
			}
		})
	}
}

func TestIgnoreAndStopIgnoringRequireIDs(t *testing.T) {
	for _, command := range []string{"ignore", "stop-ignoring"} {
		t.Run(command, func(t *testing.T) {
			server, recorded := ignoringServer(t)
			_, err := runIgnoring(t, server, command)
			if err == nil || !strings.Contains(err.Error(), "Usage:") {
				t.Fatalf("missing ID should produce a usage error, got %v", err)
			}
			if recorded.requests != 0 {
				t.Errorf("missing ID made %d requests", recorded.requests)
			}
		})
	}
}

func TestIgnoreAndStopIgnoringRejectInvalidIDsBeforeRequest(t *testing.T) {
	for _, command := range []string{"ignore", "stop-ignoring"} {
		t.Run(command, func(t *testing.T) {
			server, recorded := ignoringServer(t)
			_, err := runIgnoring(t, server, command, "not-an-id")
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

func TestIgnoreAndStopIgnoringReportServerFailures(t *testing.T) {
	for _, command := range []string{"ignore", "stop-ignoring"} {
		t.Run(command, func(t *testing.T) {
			server, recorded := ignoringServer(t)
			recorded.status = http.StatusUnprocessableEntity
			if _, err := runIgnoring(t, server, command, "12345"); err == nil {
				t.Fatal("server failure should be reported")
			}
		})
	}
}
