package mcpserver

import (
	"log/slog"
	"slices"
	"strings"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/basecamp/mcp/catalog"
	"github.com/basecamp/mcp/gateway"
	"github.com/basecamp/mcp/mcptest"
)

func mustFind(t *testing.T, d gateway.Domain, action string) gateway.Operation {
	t.Helper()
	op, ok := d.Find(action)
	if !ok {
		t.Fatalf("action %q not found in domain %q", action, d.Name())
	}
	return op
}

func textContent(t *testing.T, result *mcp.CallToolResult) string {
	t.Helper()
	if len(result.Content) == 0 {
		t.Fatal("result has no content")
	}
	text, ok := result.Content[0].(*mcp.TextContent)
	if !ok {
		t.Fatalf("first content is %T, want text", result.Content[0])
	}
	return text.Text
}

func domainByKey(t *testing.T, cat *catalog.Catalog, key string) *catalog.Domain {
	t.Helper()
	for _, d := range cat.Domains {
		if d.Key == key {
			return d
		}
	}
	t.Fatalf("domain %q not in catalog", key)
	return nil
}

func connect(t *testing.T, api API, cfg Config) (*Server, *mcp.ClientSession) {
	t.Helper()
	srv, err := New(api, cfg)
	if err != nil {
		t.Fatal(err)
	}
	session := mcptest.Connect(t, srv.BuildMCPServer(slog.New(slog.DiscardHandler)))
	return srv, session
}

func TestServerListsGatewayTools(t *testing.T) {
	_, session := connect(t, &fakeAPI{}, Config{})

	tools := mcptest.ListTools(t, session)
	for _, name := range []string{"hey_boxes", "hey_search", "hey_threads", "hey_contacts", "hey_todos", "hey_calendar", "hey_identity"} {
		if _, ok := tools[name]; !ok {
			t.Errorf("missing tool %q", name)
		}
	}
	if len(tools) != 7 {
		t.Errorf("tools/list returned %d tools, want 7", len(tools))
	}
}

func TestServerDescribeServesOperationSchema(t *testing.T) {
	_, session := connect(t, &fakeAPI{}, Config{})

	text, isError := mcptest.CallText(t, session, "hey_boxes", map[string]any{
		"action": "describe",
		"params": map[string]any{"action": "get_box"},
	})
	if isError {
		t.Fatalf("describe failed: %s", text)
	}
	if !strings.Contains(text, "/boxes/{boxId}") {
		t.Errorf("describe payload missing operation path: %s", text)
	}
}

func TestServerDispatchesToolCallsThroughAPI(t *testing.T) {
	api := &fakeAPI{}
	_, session := connect(t, api, Config{})

	text, isError := mcptest.CallText(t, session, "hey_boxes", map[string]any{
		"action": "list_boxes",
		"params": map[string]any{},
	})
	if isError {
		t.Fatalf("list_boxes failed: %s", text)
	}
	if api.method != "GET" || api.path != "/boxes.json" {
		t.Errorf("dispatched %s %s, want GET /boxes.json", api.method, api.path)
	}
	if text != `{"ok":true}` {
		t.Errorf("result = %q", text)
	}
}

func TestServerReadOnlyDropsWriteActions(t *testing.T) {
	srv, session := connect(t, &fakeAPI{}, Config{ReadOnly: true})

	for _, d := range srv.Domains() {
		if slices.Contains(d.ActionNames(), "create_box_designation") {
			t.Error("read-only server still serves create_box_designation")
		}
	}

	text, isError := mcptest.CallText(t, session, "hey_boxes", map[string]any{
		"action": "create_box_designation",
		"params": map[string]any{"boxId": "1"},
	})
	if !isError {
		t.Fatalf("write action succeeded on read-only server: %s", text)
	}
}

func TestServerNarrowsDomains(t *testing.T) {
	_, session := connect(t, &fakeAPI{}, Config{Domains: []string{"boxes"}})

	tools := mcptest.ListTools(t, session)
	if len(tools) != 1 {
		t.Fatalf("tools = %v, want just hey_boxes", tools)
	}
	if _, ok := tools["hey_boxes"]; !ok {
		t.Fatal("hey_boxes missing")
	}

	if _, err := New(&fakeAPI{}, Config{Domains: []string{"bogus"}}); err == nil {
		t.Error("unknown domain did not fail closed")
	}
}

func TestServerRequiresAPI(t *testing.T) {
	if _, err := New(nil, Config{}); err == nil {
		t.Error("nil API did not error")
	}
}
