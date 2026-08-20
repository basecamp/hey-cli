package cmd

import (
	"bytes"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/basecamp/hey-cli/internal/apierr"
	"github.com/basecamp/hey-cli/internal/output"
)

type recordedRemoval struct {
	method     string
	path       string
	postingIDs []int64
	status     int
	requests   int
}

func removalServer(t *testing.T) (*httptest.Server, *recordedRemoval) {
	t.Helper()
	recorded := &recordedRemoval{status: http.StatusNoContent}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		recorded.requests++
		recorded.method = r.Method
		recorded.path = r.URL.Path
		var body struct {
			PostingIDs []int64 `json:"posting_ids"`
		}
		_ = json.NewDecoder(r.Body).Decode(&body)
		recorded.postingIDs = body.PostingIDs

		switch r.URL.Path {
		case "/postings/trash.json", "/postings/spam.json":
			w.WriteHeader(recorded.status)
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(server.Close)
	return server, recorded
}

func runRemoval(t *testing.T, server *httptest.Server, command string, args ...string) (output.Response, error) {
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

func TestTrashAndSpam(t *testing.T) {
	tests := []struct {
		name    string
		command string
		args    []string
		path    string
		summary string
	}{
		{"trash one", "trash", []string{"12345"}, "/postings/trash.json", "1 thread moved to Trash"},
		{"trash multiple", "trash", []string{"12345", "67890"}, "/postings/trash.json", "2 threads moved to Trash"},
		{"spam one", "spam", []string{"12345"}, "/postings/spam.json", "1 thread marked as spam"},
		{"spam multiple", "spam", []string{"12345", "67890"}, "/postings/spam.json", "2 threads marked as spam"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			server, recorded := removalServer(t)
			resp, err := runRemoval(t, server, tt.command, tt.args...)
			if err != nil {
				t.Fatalf("%s failed: %v", tt.command, err)
			}
			if recorded.method != http.MethodPost || recorded.path != tt.path {
				t.Errorf("request = %s %s, want POST %s", recorded.method, recorded.path, tt.path)
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

func TestTrashAndSpamRequireIDs(t *testing.T) {
	for _, command := range []string{"trash", "spam"} {
		t.Run(command, func(t *testing.T) {
			server, recorded := removalServer(t)
			_, err := runRemoval(t, server, command)
			if err == nil || !strings.Contains(err.Error(), "Usage:") {
				t.Fatalf("missing ID should produce a usage error, got %v", err)
			}
			if recorded.requests != 0 {
				t.Errorf("missing ID made %d requests", recorded.requests)
			}
		})
	}
}

func TestTrashAndSpamRejectInvalidIDsBeforeRequest(t *testing.T) {
	for _, command := range []string{"trash", "spam"} {
		t.Run(command, func(t *testing.T) {
			server, recorded := removalServer(t)
			_, err := runRemoval(t, server, command, "not-an-id")
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

func TestTrashAndSpamReportServerFailures(t *testing.T) {
	for _, command := range []string{"trash", "spam"} {
		t.Run(command, func(t *testing.T) {
			server, recorded := removalServer(t)
			recorded.status = http.StatusUnprocessableEntity
			if _, err := runRemoval(t, server, command, "12345"); err == nil {
				t.Fatal("server failure should be reported")
			}
		})
	}
}

func TestTrashAndSpamRejectWorldPostsBeforeRequest(t *testing.T) {
	for _, command := range []string{"trash", "spam"} {
		t.Run(command, func(t *testing.T) {
			server, recorded := removalServer(t)
			_, err := runRemoval(t, server, command, "12345", "--kind", "world/post")
			if err == nil || !strings.Contains(err.Error(), "cannot act on a HEY World post") {
				t.Fatalf("error = %v, want HEY World rejection", err)
			}
			if recorded.requests != 0 {
				t.Errorf("HEY World rejection made %d requests", recorded.requests)
			}
		})
	}
}
