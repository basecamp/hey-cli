package cmd

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/basecamp/hey-cli/internal/apierr"
	"github.com/basecamp/hey-cli/internal/auth"
	"github.com/basecamp/hey-cli/internal/config"
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

	// hey mcp scopes its own clients to the configured account, on its own credential;
	// the pre-run's account scoping would ask HEY with the CLI's.
	if commandUsesAccountScope(command) {
		t.Error("the pre-run must not scope hey mcp's account with the CLI's credential")
	}
}

// hey mcp refuses to start without its own credential, even with the CLI signed in
// and HEY_TOKEN set: both hold full, and serving on either would undo the scoped
// sign-in. Nothing reaches HEY.
func TestMCPCommandRefusesWithoutItsOwnCredential(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Errorf("unexpected HTTP request: %s %s", r.Method, r.URL.Path)
	}))
	t.Cleanup(upstream.Close)

	tmpDir := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", tmpDir)
	t.Setenv("XDG_STATE_HOME", tmpDir)
	t.Setenv("XDG_CACHE_HOME", tmpDir)
	t.Setenv("HEY_NO_KEYRING", "1")
	t.Setenv("HEY_TOKEN", "a-full-token")
	stubInteractive(t, false)
	if err := auth.NewManager(upstream.URL, upstream.Client(), config.ConfigDir()).LoginWithToken("the-clis-full-token"); err != nil {
		t.Fatal(err)
	}

	// If hey mcp started serving instead of refusing, it would wait on this transport.
	_ = stubMCPTransport(t)
	root := newRootCmd()
	var buf bytes.Buffer
	root.SetOut(&buf)
	root.SetErr(&buf)
	root.SetArgs([]string{"mcp", "--base-url", upstream.URL})

	done := make(chan error, 1)
	go func() { done <- root.Execute() }()
	var err error
	select {
	case err = <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("hey mcp started serving without its own credential")
	}
	var refusal *apierr.Error
	if !errors.As(err, &refusal) || refusal.Code != apierr.CodeAuth || !strings.Contains(refusal.Hint, "hey auth login --mcp") {
		t.Fatalf("err = %#v, want an auth error pointing at hey auth login --mcp", err)
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

// mcpTestToken is the access token of the hey mcp credential the tests store.
const mcpTestToken = "mcp-scoped-token"

// storeMCPCredential stores a hey mcp credential granted scope, as `hey auth login
// --mcp` would, signed in a week ago.
func storeMCPCredential(t *testing.T, baseURL, scope string) {
	t.Helper()
	mgr := auth.NewMCPManager(baseURL, http.DefaultClient, config.ConfigDir(), "LocalMcpClient0000000001")
	creds := &auth.Credentials{
		AccessToken: mcpTestToken, RefreshToken: "mcp-refresh", OAuthType: "oauth",
		ExpiresAt: time.Now().Add(time.Hour).Unix(), Scope: scope, ClientID: "LocalMcpClient0000000001",
		GrantKey: "test-grant", GrantedAt: time.Now().Add(-7 * 24 * time.Hour).Unix(),
	}
	if err := mgr.GetStore().Save(mgr.CredentialKey(), creds); err != nil {
		t.Fatal(err)
	}
}

// runMCPCommand runs `hey mcp` against a stub HEY server, signed in on its own
// credential with read write send, and connects a real MCP client to it over the
// transport seam. The command exits when the client session closes.
func runMCPCommand(t *testing.T, upstream *httptest.Server, args ...string) *mcp.ClientSession {
	t.Helper()
	return runMCPCommandWith(t, upstream, auth.MCPScope, nil, args...)
}

// runMCPCommandWith is runMCPCommand with the credential's scope and the client's
// options given.
func runMCPCommandWith(t *testing.T, upstream *httptest.Server, scope string, clientOpts *mcp.ClientOptions, args ...string) *mcp.ClientSession {
	t.Helper()
	// HEY_TOKEN holds whatever a person exported; hey mcp must never send it.
	t.Setenv("HEY_TOKEN", "a-full-token-hey-mcp-must-not-use")
	t.Setenv("HEY_NO_KEYRING", "1")
	tmpDir := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", tmpDir)
	t.Setenv("XDG_STATE_HOME", tmpDir)
	t.Setenv("XDG_CACHE_HOME", tmpDir)
	storeMCPCredential(t, upstream.URL, scope)

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

	client := mcp.NewClient(&mcp.Implementation{Name: "test-client", Version: "0.0.0"}, clientOpts)
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
		// Tool calls ride on hey mcp's own credential, never the CLI's or HEY_TOKEN.
		if got := r.Header.Get("Authorization"); got != "Bearer "+mcpTestToken {
			t.Errorf("Authorization = %q, want hey mcp's own token", got)
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
	if len(names) != 8 || !slices.Contains(names, "hey_send") {
		t.Fatalf("tools = %v, want the 7 domain tools and hey_send", names)
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

// `hey mcp` never delivers: a request to send is refused before HEY is
// contacted, and a plain compose is saved as a draft.
func TestMCPCommandSavesDraftsAndNeverSends(t *testing.T) {
	var requests atomic.Int32
	var status atomic.Value
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		if r.Method != http.MethodPost || r.URL.Path != "/messages.json" {
			t.Errorf("unexpected HTTP request: %s %s", r.Method, r.URL.Path)
			http.NotFound(w, r)
			return
		}
		var body struct {
			Entry struct {
				Status string `json:"status"`
			} `json:"entry"`
		}
		_ = json.NewDecoder(r.Body).Decode(&body)
		status.Store(body.Entry.Status)
		w.Header().Set("Location", "/messages/42")
		w.WriteHeader(http.StatusNoContent)
	}))
	t.Cleanup(upstream.Close)

	session := runMCPCommand(t, upstream)
	call := func(entry map[string]any) *mcp.CallToolResult {
		t.Helper()
		params := map[string]any{"acting_sender_id": 7, "message": map[string]any{"subject": "s", "content": "c"}}
		if entry != nil {
			params["entry"] = entry
		}
		result, err := session.CallTool(context.Background(), &mcp.CallToolParams{
			Name:      "hey_threads",
			Arguments: map[string]any{"action": "create_message", "params": params},
		})
		if err != nil {
			t.Fatal(err)
		}
		return result
	}

	refused := call(map[string]any{"status": "sent"})
	if !refused.IsError {
		t.Fatalf("a send request was accepted: %v", refused.Content)
	}
	if text := refused.Content[0].(*mcp.TextContent).Text; !strings.Contains(text, "hey draft send <id>") {
		t.Errorf("refusal doesn't say how to send: %s", text)
	}
	if got := requests.Load(); got != 0 {
		t.Fatalf("a refused send reached HEY (%d requests)", got)
	}

	saved := call(nil)
	if saved.IsError {
		t.Fatalf("draft save failed: %v", saved.Content)
	}
	if got := status.Load(); got != "drafted" {
		t.Errorf("HEY received entry.status %v, want drafted", got)
	}
	if text := saved.Content[0].(*mcp.TextContent).Text; !strings.Contains(text, `"drafted": true`) || !strings.Contains(text, "hey draft send 42") {
		t.Errorf("result = %s", text)
	}
}

func toolNames(t *testing.T, session *mcp.ClientSession) []string {
	t.Helper()
	var names []string
	for tool, err := range session.Tools(context.Background(), nil) {
		if err != nil {
			t.Fatal(err)
		}
		names = append(names, tool.Name)
	}
	return names
}

// The tools follow the scope HEY granted: a sign-in with send unchecked serves no
// hey_send, and one without write serves nothing that writes.
func TestMCPCommandServesWhatTheGrantAllows(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Errorf("unexpected HTTP request: %s %s", r.Method, r.URL.Path)
	}))
	t.Cleanup(upstream.Close)

	t.Run("send unchecked", func(t *testing.T) {
		names := toolNames(t, runMCPCommandWith(t, upstream, "read write", nil))
		if slices.Contains(names, "hey_send") || len(names) != 7 {
			t.Errorf("tools = %v, want the 7 domain tools and no hey_send", names)
		}
	})
	t.Run("read only", func(t *testing.T) {
		session := runMCPCommandWith(t, upstream, "read", nil)
		for tool, err := range session.Tools(context.Background(), nil) {
			if err != nil {
				t.Fatal(err)
			}
			if tool.Name == "hey_send" || strings.Contains(tool.Description, "- trash_topic") {
				t.Errorf("a read-only grant serves a write: %s", tool.Name)
			}
		}
	})
	t.Run("--read-only", func(t *testing.T) {
		if names := toolNames(t, runMCPCommand(t, upstream, "--read-only")); slices.Contains(names, "hey_send") {
			t.Errorf("--read-only serves hey_send: %v", names)
		}
	})
}

// End to end through the command: send_draft reads the draft, asks the person, and
// sends the approved snapshot once, on hey mcp's own credential, through the client
// that never retries.
func TestMCPCommandSendsAConfirmedDraftOnce(t *testing.T) {
	var puts atomic.Int32
	var putBody atomic.Value
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.Header.Get("Authorization"); got != "Bearer "+mcpTestToken {
			t.Errorf("%s %s Authorization = %q, want hey mcp's own token", r.Method, r.URL.Path, got)
		}
		w.Header().Set("Content-Type", "application/json")
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/messages/5/edit.json":
			_, _ = w.Write([]byte(`{"id":5,"subject":"Re: Q3 plan","content":"<div>Hi Ann</div>","sender":{"id":77,"name":"Jeremy Daer","email_address":"jeremy@hey.example"},"addressed":{"directly":[{"id":1,"name":"Ann Example","email_address":"ann@example.com"}]}}`))
		case r.Method == http.MethodGet && r.URL.Path == "/identity.json":
			_, _ = w.Write([]byte(`{"id":1,"primary_contact":{"id":77,"email_address":"jeremy@hey.example"},"senders":[{"id":77,"email_address":"jeremy@hey.example"}]}`))
		case r.Method == http.MethodGet && r.URL.Path == "/advanced_search.json":
			_, _ = w.Write([]byte(`{"matches":[]}`))
		case r.Method == http.MethodPut && r.URL.Path == "/messages/5.json":
			puts.Add(1)
			var body map[string]any
			_ = json.NewDecoder(r.Body).Decode(&body)
			putBody.Store(body)
			_, _ = w.Write([]byte(`{"id":5,"topic_id":900}`))
		default:
			t.Errorf("unexpected HTTP request: %s %s", r.Method, r.URL.Path)
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(upstream.Close)

	var prompt string
	session := runMCPCommandWith(t, upstream, auth.MCPScope, &mcp.ClientOptions{
		ElicitationHandler: func(_ context.Context, req *mcp.ElicitRequest) (*mcp.ElicitResult, error) {
			prompt = req.Params.Message
			return &mcp.ElicitResult{Action: "accept", Content: map[string]any{"confirm": true}}, nil
		},
	})
	call := func() *mcp.CallToolResult {
		t.Helper()
		result, err := session.CallTool(context.Background(), &mcp.CallToolParams{
			Name:      "hey_send",
			Arguments: map[string]any{"action": "send_draft", "params": map[string]any{"draft_id": 5}},
		})
		if err != nil {
			t.Fatal(err)
		}
		return result
	}

	sent := call()
	if sent.IsError {
		t.Fatalf("send_draft failed: %v", sent.Content)
	}
	if !strings.Contains(prompt, `"Ann Example" <ann@example.com> (new: never emailed before)`) {
		t.Errorf("prompt = %s", prompt)
	}
	body, _ := putBody.Load().(map[string]any)
	entry, _ := body["entry"].(map[string]any)
	if entry["status"] != nil || entry["addressed"] == nil {
		t.Errorf("delivery entry = %v, want recipients and no drafted status", entry)
	}

	again := call()
	if !again.IsError {
		t.Fatalf("a second send_draft of the same draft went through: %v", again.Content)
	}
	if got := puts.Load(); got != 1 {
		t.Errorf("HEY saw %d deliveries, want 1", got)
	}
}
