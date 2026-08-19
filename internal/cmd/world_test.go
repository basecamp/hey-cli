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

func runWorldDelete(t *testing.T, server *httptest.Server, args ...string) (output.Response, error) {
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
	root.SetArgs(append([]string{"world", "delete", "--json", "--base-url", server.URL}, args...))

	err := root.Execute()
	var resp output.Response
	if buf.Len() > 0 {
		_ = json.Unmarshal(buf.Bytes(), &resp)
	}
	return resp, err
}

func TestWorldDeleteRequiresConfirmationBeforeRequest(t *testing.T) {
	var requests atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		requests.Add(1)
		w.WriteHeader(http.StatusNoContent)
	}))
	defer server.Close()

	_, err := runWorldDelete(t, server, "abc123")
	if err == nil || !strings.Contains(err.Error(), "requires --confirm") {
		t.Fatalf("error = %v, want confirmation requirement", err)
	}
	if requests.Load() != 0 {
		t.Fatalf("unconfirmed delete made %d requests", requests.Load())
	}
}

func TestWorldDeleteUsesSeparateConfirmedEndpoint(t *testing.T) {
	var requests atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		if r.Method != http.MethodDelete || r.URL.Path != "/world/posts/abc123" {
			t.Errorf("request = %s %s", r.Method, r.URL.Path)
		}
		if r.URL.Path == "/postings/trash.json" {
			t.Fatal("world deletion used the email Trash endpoint")
		}
		w.WriteHeader(http.StatusNoContent)
	}))
	defer server.Close()

	resp, err := runWorldDelete(t, server, "abc123", "--confirm")
	if err != nil {
		t.Fatalf("delete: %v", err)
	}
	if requests.Load() != 1 {
		t.Fatalf("requests = %d, want 1", requests.Load())
	}
	if resp.Summary != "HEY World post abc123 deleted" {
		t.Errorf("summary = %q", resp.Summary)
	}
}
