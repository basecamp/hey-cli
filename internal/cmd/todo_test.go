package cmd

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	"github.com/basecamp/hey-cli/internal/output"
)

func todoServer(t *testing.T) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == "POST" && r.URL.Path == "/calendar/todos.json":
			body, _ := io.ReadAll(r.Body)
			var req map[string]any
			_ = json.Unmarshal(body, &req)
			resp := map[string]any{
				"id":    1,
				"title": req["title"],
			}
			w.Header().Set("Content-Type", "application/json")
			json.NewEncoder(w).Encode(resp)
		default:
			w.WriteHeader(200)
		}
	}))
}

func runTodoAdd(t *testing.T, server *httptest.Server, args ...string) (output.Response, error) {
	t.Helper()
	t.Setenv("HEY_TOKEN", "test-token")
	t.Setenv("HEY_NO_KEYRING", "1")
	t.Setenv("HEY_BASE_URL", "")

	root := newRootCmd()
	var buf bytes.Buffer
	root.SetOut(&buf)
	root.SetErr(&buf)
	root.SetArgs(append([]string{"todo", "add", "--json", "--base-url", server.URL}, args...))

	err := root.Execute()
	var resp output.Response
	if buf.Len() > 0 {
		_ = json.Unmarshal(buf.Bytes(), &resp)
	}
	return resp, err
}

func TestTodoAddPositional(t *testing.T) {
	server := todoServer(t)
	defer server.Close()

	resp, err := runTodoAdd(t, server, "Buy milk")
	if err != nil {
		t.Fatalf("execute: %v", err)
	}

	data, ok := resp.Data.(map[string]any)
	if !ok {
		t.Fatalf("data type = %T, want map[string]any", resp.Data)
	}
	if got := data["title"]; got != "Buy milk" {
		t.Errorf("title = %q, want %q", got, "Buy milk")
	}
}

func TestTodoAddShortFlag(t *testing.T) {
	server := todoServer(t)
	defer server.Close()

	resp, err := runTodoAdd(t, server, "-t", "Buy milk")
	if err != nil {
		t.Fatalf("execute: %v", err)
	}

	data, ok := resp.Data.(map[string]any)
	if !ok {
		t.Fatalf("data type = %T, want map[string]any", resp.Data)
	}
	if got := data["title"]; got != "Buy milk" {
		t.Errorf("title = %q, want %q", got, "Buy milk")
	}
}

func TestTodoAddLongFlag(t *testing.T) {
	server := todoServer(t)
	defer server.Close()

	resp, err := runTodoAdd(t, server, "--title", "Buy milk")
	if err != nil {
		t.Fatalf("execute: %v", err)
	}

	data, ok := resp.Data.(map[string]any)
	if !ok {
		t.Fatalf("data type = %T, want map[string]any", resp.Data)
	}
	if got := data["title"]; got != "Buy milk" {
		t.Errorf("title = %q, want %q", got, "Buy milk")
	}
}

func TestTodoAddConflict(t *testing.T) {
	server := todoServer(t)
	defer server.Close()

	_, err := runTodoAdd(t, server, "--title", "X", "Y")
	if err == nil {
		t.Fatal("expected error for conflicting flag and positional")
	}
	if !strings.Contains(err.Error(), "mutually exclusive") {
		t.Errorf("error = %q, want to contain %q", err.Error(), "mutually exclusive")
	}
}

func TestTodoAddEmpty(t *testing.T) {
	server := todoServer(t)
	defer server.Close()

	_, err := runTodoAdd(t, server)
	if err == nil {
		t.Fatal("expected error for missing title")
	}
	if !strings.Contains(err.Error(), "title is required") {
		t.Errorf("error = %q, want to contain %q", err.Error(), "title is required")
	}
}

func TestTodoAddStdin(t *testing.T) {
	server := todoServer(t)
	defer server.Close()

	// Replace stdin with a pipe
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	oldStdin := os.Stdin
	os.Stdin = r
	defer func() { os.Stdin = oldStdin }()

	go func() {
		w.Write([]byte("Buy milk from stdin\n"))
		w.Close()
	}()

	resp, err := runTodoAdd(t, server)
	if err != nil {
		t.Fatalf("execute: %v", err)
	}

	data, ok := resp.Data.(map[string]any)
	if !ok {
		t.Fatalf("data type = %T, want map[string]any", resp.Data)
	}
	if got := data["title"]; got != "Buy milk from stdin" {
		t.Errorf("title = %q, want %q", got, "Buy milk from stdin")
	}
}
