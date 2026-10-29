package cmd

import (
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"testing"

	"github.com/basecamp/hey-cli/internal/apierr"
	"github.com/basecamp/hey-cli/internal/auth"
)

// fakeLocalMCPOAuth serves the device flow and revocation for the local MCP client.
type fakeLocalMCPOAuth struct {
	t           *testing.T
	scope       string
	mu          sync.Mutex
	revocations []url.Values
}

func (f *fakeLocalMCPOAuth) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	raw, _ := io.ReadAll(r.Body)
	form, _ := url.ParseQuery(string(raw))
	w.Header().Set("Content-Type", "application/json")
	switch {
	case r.Method == http.MethodPost && r.URL.Path == "/oauth/device_authorizations":
		if form.Get("client_id") != "LocalMcpClient0000000001" || form.Get("scope") != "read write send" {
			f.t.Errorf("device request = %v", form)
		}
		fmt.Fprint(w, `{"device_code":"secret-device-code","user_code":"WDJB-MJHT","verification_uri":"https://hey.example/device","expires_in":600,"interval":1}`)
	case r.Method == http.MethodPost && r.URL.Path == "/oauth/tokens":
		fmt.Fprintf(w, `{"access_token":"mcp-access","refresh_token":"mcp-refresh","token_type":"bearer","expires_in":1209600,"scope":%q}`, f.scope)
	case r.Method == http.MethodDelete && r.URL.Path == "/oauth/tokens":
		f.mu.Lock()
		f.revocations = append(f.revocations, form)
		f.mu.Unlock()
		fmt.Fprint(w, `{}`)
	default:
		f.t.Errorf("unexpected HTTP request: %s %s", r.Method, r.URL.Path)
		http.NotFound(w, r)
	}
}

func TestAuthLoginMCPKeepsTheCLIsCredentialAndStatusShowsBoth(t *testing.T) {
	hey := &fakeLocalMCPOAuth{t: t, scope: "read write"}
	server := httptest.NewServer(hey)
	defer server.Close()
	configHome := t.TempDir()
	t.Setenv("HEY_MCP_CLIENT_ID", "LocalMcpClient0000000001")

	if _, _, err := runAuthCommand(t, configHome, server.URL, "", true, "auth", "login", "--token", "cli-full-token"); err != nil {
		t.Fatal(err)
	}
	_, login, err := runAuthCommand(t, configHome, server.URL, "", true, "auth", "login", "--mcp", "--no-browser")
	if err != nil {
		t.Fatal(err)
	}
	data, _ := login.Data.(map[string]any)
	if data["method"] != "device" || data["scope"] != "read write" || data["can_send"] != false {
		t.Errorf("login data = %v", data)
	}

	_, status, err := runAuthCommand(t, configHome, server.URL, "", true, "auth", "status")
	if err != nil {
		t.Fatal(err)
	}
	got, _ := status.Data.(map[string]any)
	mcpStatus, _ := got["mcp"].(map[string]any)
	if got["authenticated"] != true || got["auth_type"] != "token" {
		t.Errorf("the CLI's credential changed: %v", got)
	}
	if mcpStatus["authenticated"] != true || mcpStatus["scope"] != "read write" || mcpStatus["can_send"] != false {
		t.Errorf("mcp status = %v", mcpStatus)
	}
	if text := fmt.Sprint(got); strings.Contains(text, "mcp-access") || strings.Contains(text, "mcp-refresh") || strings.Contains(text, "cli-full-token") {
		t.Error("status printed a token")
	}

	styled, _, err := runAuthCommand(t, configHome, server.URL, "", false, "auth", "status", "--styled")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(styled, "MCP:       Signed in (read write), can't send") {
		t.Errorf("styled status = %s", styled)
	}

	_, logout, err := runAuthCommand(t, configHome, server.URL, "", true, "auth", "logout", "--mcp")
	if err != nil {
		t.Fatal(err)
	}
	if data, _ := logout.Data.(map[string]any); data["revoked"] != true {
		t.Errorf("logout data = %v", logout.Data)
	}
	if len(hey.revocations) != 1 || hey.revocations[0].Get("client_id") != "LocalMcpClient0000000001" || hey.revocations[0].Get("token") != "mcp-refresh" {
		t.Errorf("revocations = %d, want one of the MCP refresh token by the local client", len(hey.revocations))
	}

	_, status, err = runAuthCommand(t, configHome, server.URL, "", true, "auth", "status")
	if err != nil {
		t.Fatal(err)
	}
	got, _ = status.Data.(map[string]any)
	mcpStatus, _ = got["mcp"].(map[string]any)
	if got["authenticated"] != true || mcpStatus["authenticated"] != false {
		t.Errorf("after logout --mcp: cli %v, mcp %v; want the CLI still signed in and hey mcp out", got["authenticated"], mcpStatus["authenticated"])
	}
}

func TestAuthLoginMCPNeedsAClientID(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Errorf("unexpected HTTP request: %s %s", r.Method, r.URL.Path)
	}))
	defer server.Close()
	t.Setenv("HEY_MCP_CLIENT_ID", "")

	_, _, err := runAuthCommand(t, t.TempDir(), server.URL, "", true, "auth", "login", "--mcp")
	var usage *apierr.Error
	if !errors.As(err, &usage) || !strings.Contains(usage.Hint, "HEY_MCP_CLIENT_ID") {
		t.Fatalf("err = %#v, want a usage error naming HEY_MCP_CLIENT_ID", err)
	}
}

func TestAuthLoginMCPTakesNoTokenOrCookie(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Errorf("unexpected HTTP request: %s %s", r.Method, r.URL.Path)
	}))
	defer server.Close()
	if _, _, err := runAuthCommand(t, t.TempDir(), server.URL, "", true, "auth", "login", "--mcp", "--token", "x"); err == nil {
		t.Fatal("--mcp stored a hand-supplied token")
	}
}

// can_send is the same predicate hey mcp serves hey_send on: send alone, without write,
// leaves hey mcp read-only.
func TestMCPCanSendNeedsWriteAndSend(t *testing.T) {
	for scope, want := range map[string]bool{"read write send": true, "write send": true, "read send": false, "read write": false} {
		if got := mcpCanSend(&auth.Credentials{Scope: scope, GrantKey: "g"}); got != want {
			t.Errorf("mcpCanSend(%q) = %v, want %v", scope, got, want)
		}
	}
	if mcpCanSend(&auth.Credentials{Scope: "read write send"}) {
		t.Error("a sign-in without a local key can send")
	}
}

func TestHeyMCPSaysAKeychainRefusalIsntAMissingSignIn(t *testing.T) {
	t.Setenv("CODEX_SANDBOX", "")
	// What the store returns when macOS's security tool exits 36 for a process outside
	// the desktop session, such as an MCP client's background server.
	refused := fmt.Errorf("read credentials from system keyring: %w",
		errors.Join(errors.New("exit status 36"), auth.ErrKeyringRefused))

	_, err := checkMCPCredentials(nil, refused)
	var classified *apierr.Error
	if !errors.As(err, &classified) {
		t.Fatalf("err = %v, want a classified error carrying a hint", err)
	}
	if classified.Code == apierr.CodeAuth {
		t.Errorf("code = %q, a refused keyring is not a missing sign-in", classified.Code)
	}
	if !strings.HasPrefix(classified.Hint, "Signing in again won't help") {
		t.Errorf("hint = %q, want it to rule out another hey auth login --mcp", classified.Hint)
	}
	if !strings.HasPrefix(classified.Message, "could not read hey mcp's credentials: ") {
		t.Errorf("message = %q", classified.Message)
	}

	_, err = checkMCPCredentials(nil, fmt.Errorf("%w for mcp::https://app.hey.com", auth.ErrCredentialsNotFound))
	if !errors.As(err, &classified) || classified.Code != apierr.CodeAuth || classified.Hint != "Run: "+mcpLoginCommand {
		t.Errorf("err = %#v, a missing credential still points at %s", err, mcpLoginCommand)
	}
}
