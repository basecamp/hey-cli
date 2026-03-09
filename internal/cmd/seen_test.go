package cmd

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/basecamp/hey-cli/internal/output"
)

func seenServer(t *testing.T) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == "POST" && r.URL.Path == "/postings/seen":
			body, _ := io.ReadAll(r.Body)
			var req map[string]any
			_ = json.Unmarshal(body, &req)
			if req["posting_ids"] == nil {
				w.WriteHeader(400)
				return
			}
			w.WriteHeader(201)
		case r.Method == "POST" && r.URL.Path == "/postings/unseen":
			body, _ := io.ReadAll(r.Body)
			var req map[string]any
			_ = json.Unmarshal(body, &req)
			if req["posting_ids"] == nil {
				w.WriteHeader(400)
				return
			}
			w.WriteHeader(201)
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
	if resp.Summary != "1 posting(s) marked as seen" {
		t.Errorf("summary = %q, want %q", resp.Summary, "1 posting(s) marked as seen")
	}
}

func TestSeenMultiple(t *testing.T) {
	server := seenServer(t)
	defer server.Close()

	resp, err := runSeen(t, server, "12345", "67890")
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	if resp.Summary != "2 posting(s) marked as seen" {
		t.Errorf("summary = %q, want %q", resp.Summary, "2 posting(s) marked as seen")
	}
}

func TestSeenNoArgs(t *testing.T) {
	server := seenServer(t)
	defer server.Close()

	_, err := runSeen(t, server)
	if err == nil {
		t.Fatal("expected error for missing args")
	}
}

func TestSeenInvalidID(t *testing.T) {
	server := seenServer(t)
	defer server.Close()

	_, err := runSeen(t, server, "abc")
	if err == nil {
		t.Fatal("expected error for non-numeric ID")
	}
}

func TestUnseenSingle(t *testing.T) {
	server := seenServer(t)
	defer server.Close()

	resp, err := runUnseen(t, server, "12345")
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	if resp.Summary != "1 posting(s) marked as unseen" {
		t.Errorf("summary = %q, want %q", resp.Summary, "1 posting(s) marked as unseen")
	}
}

func TestUnseenMultiple(t *testing.T) {
	server := seenServer(t)
	defer server.Close()

	resp, err := runUnseen(t, server, "12345", "67890")
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	if resp.Summary != "2 posting(s) marked as unseen" {
		t.Errorf("summary = %q, want %q", resp.Summary, "2 posting(s) marked as unseen")
	}
}

func TestUnseenNoArgs(t *testing.T) {
	server := seenServer(t)
	defer server.Close()

	_, err := runUnseen(t, server)
	if err == nil {
		t.Fatal("expected error for missing args")
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
