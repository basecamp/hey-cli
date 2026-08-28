package cmd

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

func TestMCPCommandRegistration(t *testing.T) {
	root := newRootCmd()
	command, _, err := root.Find([]string{"mcp"})
	if err != nil || command.Name() != "mcp" {
		t.Fatalf("mcp command not registered: %v", err)
	}

	readOnly := command.Flags().Lookup("read-only")
	if readOnly == nil || readOnly.DefValue != "false" {
		t.Errorf("read-only flag = %#v", readOnly)
	}
	domains := command.Flags().Lookup("domains")
	if domains == nil {
		t.Error("domains flag missing")
	}

	if !commandUsesAccountScope(command) {
		t.Error("mcp must use the configured account scope")
	}
}

func TestMCPCommandRequiresAuth(t *testing.T) {
	tmpDir := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", tmpDir)
	t.Setenv("XDG_STATE_HOME", tmpDir)
	t.Setenv("XDG_CACHE_HOME", tmpDir)
	t.Setenv("HEY_TOKEN", "")
	t.Setenv("HEY_NO_KEYRING", "1")
	stubInteractive(t, false)

	root := newRootCmd()
	var buf bytes.Buffer
	root.SetOut(&buf)
	root.SetErr(&buf)
	root.SetArgs([]string{"mcp"})

	if err := root.Execute(); err == nil || !strings.Contains(err.Error(), "Not logged in") {
		t.Fatalf("err = %v, want auth error", err)
	}
}

// stubMCPTransport swaps the stdio transport for the server side of an
// in-memory pipe and returns the client side.
func stubMCPTransport(t *testing.T) mcp.Transport {
	t.Helper()
	clientTransport, serverTransport := mcp.NewInMemoryTransports()
	orig := mcpTransport
	mcpTransport = func() mcp.Transport { return serverTransport }
	t.Cleanup(func() { mcpTransport = orig })
	return clientTransport
}

// runMCPCommand runs `hey mcp` against a stub HEY server and connects a real
// MCP client to it over the transport seam. The command exits when the
// client session closes.
func runMCPCommand(t *testing.T, upstream *httptest.Server, args ...string) *mcp.ClientSession {
	t.Helper()
	t.Setenv("HEY_TOKEN", "test-token")
	t.Setenv("HEY_NO_KEYRING", "1")
	tmpDir := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", tmpDir)
	t.Setenv("XDG_STATE_HOME", tmpDir)
	t.Setenv("XDG_CACHE_HOME", tmpDir)

	clientTransport := stubMCPTransport(t)

	root := newRootCmd()
	var buf bytes.Buffer
	root.SetOut(&buf)
	root.SetErr(&buf)
	root.SetArgs(append([]string{"mcp", "--base-url", upstream.URL}, args...))

	done := make(chan error, 1)
	go func() { done <- root.Execute() }()
	t.Cleanup(func() {
		if err := <-done; err != nil {
			t.Errorf("hey mcp exited with error: %v\n%s", err, buf.String())
		}
	})

	client := mcp.NewClient(&mcp.Implementation{Name: "test-client", Version: "0.0.0"}, nil)
	session, err := client.Connect(context.Background(), clientTransport, nil)
	if err != nil {
		t.Fatalf("MCP initialize failed: %v", err)
	}
	t.Cleanup(func() { _ = session.Close() })
	return session
}

func TestMCPCommandServesMCP(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet || r.URL.Path != "/boxes.json" {
			t.Errorf("unexpected HTTP request: %s %s", r.Method, r.URL.Path)
			http.NotFound(w, r)
			return
		}
		// Tool calls must ride on the CLI's own credentials.
		if got := r.Header.Get("Authorization"); got != "Bearer test-token" {
			t.Errorf("Authorization = %q, want the CLI's token", got)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`[{"id":1,"name":"Imbox"}]`))
	}))
	t.Cleanup(upstream.Close)

	session := runMCPCommand(t, upstream)

	if got := session.InitializeResult().ServerInfo.Name; got != "hey-cli" {
		t.Errorf("server name = %q, want hey-cli", got)
	}

	var names []string
	for tool, err := range session.Tools(context.Background(), nil) {
		if err != nil {
			t.Fatal(err)
		}
		names = append(names, tool.Name)
	}
	if len(names) != 7 {
		t.Fatalf("tools = %v, want 7 hey_* tools", names)
	}

	result, err := session.CallTool(context.Background(), &mcp.CallToolParams{
		Name:      "hey_boxes",
		Arguments: map[string]any{"action": "list_boxes", "params": map[string]any{}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.IsError {
		t.Fatalf("list_boxes failed: %v", result.Content)
	}
	text, ok := result.Content[0].(*mcp.TextContent)
	if !ok {
		t.Fatalf("content = %T", result.Content[0])
	}
	var boxes []struct {
		Name string `json:"name"`
	}
	if err := json.Unmarshal([]byte(text.Text), &boxes); err != nil || len(boxes) == 0 || boxes[0].Name != "Imbox" {
		t.Fatalf("list_boxes result = %q (%v)", text.Text, err)
	}
}

func TestMCPCommandFlagPassthrough(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Errorf("unexpected HTTP request: %s %s", r.Method, r.URL.Path)
		http.NotFound(w, r)
	}))
	t.Cleanup(upstream.Close)

	session := runMCPCommand(t, upstream, "--read-only", "--domains", "boxes")

	var tools []*mcp.Tool
	for tool, err := range session.Tools(context.Background(), nil) {
		if err != nil {
			t.Fatal(err)
		}
		tools = append(tools, tool)
	}
	if len(tools) != 1 || tools[0].Name != "hey_boxes" {
		t.Fatalf("tools = %v, want just hey_boxes", tools)
	}
	if !strings.Contains(tools[0].Description, "list_boxes") {
		t.Error("read-only hey_boxes lost its read actions")
	}
	if strings.Contains(tools[0].Description, "create_box_designation") {
		t.Error("read-only hey_boxes still lists a write action")
	}
}

func TestMCPCommandDoesNotRetryMutations(t *testing.T) {
	var deliveries atomic.Int32
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPut || !strings.HasPrefix(r.URL.Path, "/messages/1") {
			t.Errorf("unexpected HTTP request: %s %s", r.Method, r.URL.Path)
			http.NotFound(w, r)
			return
		}
		deliveries.Add(1)
		// An ambiguous failure on a delivery: a retry could send the mail
		// twice, so the client must surface it instead.
		w.Header().Set("Retry-After", "0")
		w.WriteHeader(http.StatusTooManyRequests)
	}))
	t.Cleanup(upstream.Close)

	session := runMCPCommand(t, upstream)

	result, err := session.CallTool(context.Background(), &mcp.CallToolParams{
		Name: "hey_threads",
		Arguments: map[string]any{"action": "update_message", "params": map[string]any{
			"messageId":        "1",
			"acting_sender_id": 7,
			"message":          map[string]any{"subject": "s", "content": "c"},
		}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if !result.IsError {
		t.Fatalf("update_message against a 429 upstream did not surface an error: %v", result.Content)
	}
	if got := deliveries.Load(); got != 1 {
		t.Errorf("upstream saw %d delivery attempts, want exactly 1 — a delivery must never be retried", got)
	}
}
