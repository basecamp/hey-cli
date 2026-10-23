package auth

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"testing"
	"time"
)

const testMCPClientID = "LocalMcpClient0000000001"

// fakeHEYOAuth is HEY's OAuth endpoints as the local MCP client meets them.
type fakeHEYOAuth struct {
	t *testing.T

	mu sync.Mutex
	// deviceStatus and deviceError answer the device authorization request; zero
	// serves a device code.
	deviceStatus int
	deviceError  string
	// pending is how many polls answer authorization_pending before the token.
	pending int
	// pollErrors answer polls in order before pending counts down.
	pollErrors []string
	// grantedScope is the token response's scope; "-" leaves it out.
	grantedScope string

	deviceRequests url.Values
	polls          []url.Values
	exchanges      []url.Values
	refreshes      []url.Values
	revocations    []url.Values
}

func (f *fakeHEYOAuth) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	defer f.mu.Unlock()
	// net/http parses a form body only for POST, PUT and PATCH; Rack parses one on any
	// method, DELETE /oauth/tokens included, so read it directly.
	raw, _ := io.ReadAll(r.Body)
	form, err := url.ParseQuery(string(raw))
	if err != nil {
		f.t.Errorf("parse form: %v", err)
	}
	w.Header().Set("Content-Type", "application/json")

	switch {
	case r.Method == http.MethodPost && r.URL.Path == "/oauth/device_authorizations":
		f.deviceRequests = form
		if f.deviceStatus != 0 {
			w.WriteHeader(f.deviceStatus)
			fmt.Fprintf(w, `{"error":%q}`, f.deviceError)
			return
		}
		fmt.Fprint(w, `{"device_code":"secret-device-code","user_code":"WDJB-MJHT","verification_uri":"https://hey.example/device","verification_uri_complete":"https://hey.example/device?user_code=WDJB-MJHT","expires_in":600,"interval":5}`)
	case r.Method == http.MethodPost && r.URL.Path == "/oauth/tokens" && form.Get("grant_type") == deviceGrantType:
		f.polls = append(f.polls, form)
		if len(f.pollErrors) > 0 {
			code := f.pollErrors[0]
			f.pollErrors = f.pollErrors[1:]
			w.WriteHeader(http.StatusBadRequest)
			fmt.Fprintf(w, `{"error":%q}`, code)
			return
		}
		if f.pending > 0 {
			f.pending--
			w.WriteHeader(http.StatusBadRequest)
			fmt.Fprint(w, `{"error":"authorization_pending"}`)
			return
		}
		f.writeToken(w, "device")
	case r.Method == http.MethodPost && r.URL.Path == "/oauth/tokens" && form.Get("grant_type") == "authorization_code":
		f.exchanges = append(f.exchanges, form)
		f.writeToken(w, "loopback")
	case r.Method == http.MethodPost && r.URL.Path == "/oauth/tokens" && form.Get("grant_type") == "refresh_token":
		f.refreshes = append(f.refreshes, form)
		f.writeToken(w, "refreshed")
	case r.Method == http.MethodDelete && r.URL.Path == "/oauth/tokens":
		f.revocations = append(f.revocations, form)
		fmt.Fprint(w, `{}`)
	default:
		f.t.Errorf("unexpected request %s %s", r.Method, r.URL.Path)
		http.NotFound(w, r)
	}
}

func (f *fakeHEYOAuth) writeToken(w http.ResponseWriter, kind string) {
	body := map[string]any{"access_token": kind + "-access", "refresh_token": kind + "-refresh", "token_type": "bearer", "expires_in": 1209600}
	switch f.grantedScope {
	case "-":
	case "":
		body["scope"] = MCPScope
	default:
		body["scope"] = f.grantedScope
	}
	_ = json.NewEncoder(w).Encode(body)
}

// mcpRig is a CLI manager and an MCP manager sharing one keychain, against one HEY.
type mcpRig struct {
	hey     *fakeHEYOAuth
	server  *httptest.Server
	keyring *fakeKeyring
	cli     *Manager
	mcp     *Manager
	waits   []time.Duration
}

func newMCPRig(t *testing.T) *mcpRig {
	t.Helper()
	t.Setenv("HEY_TOKEN", "")
	t.Setenv("HEY_NO_KEYRING", "")
	rig := &mcpRig{hey: &fakeHEYOAuth{t: t}, keyring: newFakeKeyring()}
	rig.server = httptest.NewServer(rig.hey)
	t.Cleanup(rig.server.Close)

	configDir := t.TempDir()
	rig.cli = NewManager(rig.server.URL, rig.server.Client(), configDir)
	rig.mcp = NewMCPManager(rig.server.URL, rig.server.Client(), configDir, testMCPClientID)
	for _, m := range []*Manager{rig.cli, rig.mcp} {
		m.store.keyring = credentialKeyring{set: rig.keyring.Set, get: rig.keyring.Get, delete: rig.keyring.Delete}
	}
	rig.mcp.wait = func(_ context.Context, d time.Duration) error {
		rig.waits = append(rig.waits, d)
		return nil
	}
	// No test opens a browser: the device link and the loopback URL are only logged.
	rig.mcp.callbackWait = func(_ context.Context, state, authURL string, listener net.Listener, _ LoginOptions) (string, error) {
		u, err := url.Parse(authURL)
		if err != nil {
			return "", err
		}
		rig.hey.mu.Lock()
		rig.hey.exchanges = append(rig.hey.exchanges, url.Values{"authorize": {u.RawQuery}})
		rig.hey.mu.Unlock()
		if u.Query().Get("state") != state {
			return "", errors.New("state mismatch")
		}
		return "loopback-code", nil
	}
	return rig
}

var quiet = LoginOptions{NoBrowser: true, Logger: func(string) {}}

func TestMCPLoginStoresACredentialApartFromTheCLIs(t *testing.T) {
	rig := newMCPRig(t)
	if err := rig.cli.LoginWithToken("cli-full-token"); err != nil {
		t.Fatal(err)
	}
	rig.hey.pending = 2

	method, err := rig.mcp.LoginMCP(context.Background(), quiet)
	if err != nil {
		t.Fatal(err)
	}
	if method != "device" {
		t.Errorf("method = %q, want device", method)
	}

	// The device request names the local client and the scope, and no resource.
	got := rig.hey.deviceRequests
	if got.Get("client_id") != testMCPClientID || got.Get("scope") != MCPScope || got.Has("resource") {
		t.Errorf("device request = %v", got)
	}
	for _, poll := range rig.hey.polls {
		if poll.Get("client_id") != testMCPClientID || poll.Get("device_code") != "secret-device-code" || !isInstallID(poll.Get("install_id")) {
			t.Errorf("poll = %v", poll)
		}
	}
	if len(rig.hey.polls) != 3 {
		t.Errorf("polled %d times, want 3 (two pending, then the token)", len(rig.hey.polls))
	}

	// Two keychain items: the CLI's, untouched, and hey mcp's.
	cli, err := rig.cli.StoredCredentials()
	if err != nil || cli.AccessToken != "cli-full-token" {
		t.Fatalf("the CLI's credential = %+v, %v", cli, err)
	}
	mcp, err := rig.mcp.StoredCredentials()
	if err != nil {
		t.Fatal(err)
	}
	if mcp.AccessToken != "device-access" || mcp.Scope != MCPScope || mcp.ClientID != testMCPClientID || mcp.GrantKey == "" || mcp.GrantedAt == 0 {
		t.Errorf("hey mcp's credential = %+v", mcp)
	}
	cliItem, mcpItem := serviceName+"/"+key(rig.cli.CredentialKey()), serviceName+"/"+key(rig.mcp.CredentialKey())
	if cliItem == mcpItem {
		t.Fatalf("both credentials live in keychain item %s", cliItem)
	}
	if _, ok := rig.keyring.values[cliItem]; !ok {
		t.Errorf("no CLI keychain item %s", cliItem)
	}
	if _, ok := rig.keyring.values[mcpItem]; !ok {
		t.Errorf("no hey mcp keychain item %s", mcpItem)
	}
}

func TestMCPLoginStoresApartInThePlaintextFallbackToo(t *testing.T) {
	t.Setenv("HEY_TOKEN", "")
	t.Setenv("HEY_NO_KEYRING", "1")
	hey := &fakeHEYOAuth{t: t}
	server := httptest.NewServer(hey)
	t.Cleanup(server.Close)
	dir := t.TempDir()

	cli := NewManager(server.URL, server.Client(), dir)
	if err := cli.LoginWithToken("cli-full-token"); err != nil {
		t.Fatal(err)
	}
	mcp := NewMCPManager(server.URL, server.Client(), dir, testMCPClientID)
	mcp.wait = func(context.Context, time.Duration) error { return nil }
	if _, err := mcp.LoginMCP(context.Background(), quiet); err != nil {
		t.Fatal(err)
	}

	all, err := cli.store.loadAllFromFile()
	if err != nil {
		t.Fatal(err)
	}
	if len(all) != 2 || all[cli.CredentialKey()].AccessToken != "cli-full-token" || all[mcp.CredentialKey()].AccessToken != "device-access" {
		t.Errorf("credentials.json entries = %v", keysOf(all))
	}
}

func keysOf(all map[string]*Credentials) []string {
	keys := make([]string, 0, len(all))
	for k := range all {
		keys = append(keys, k)
	}
	return keys
}

func TestMCPLoginFallsBackToLoopbackWithTheSameScope(t *testing.T) {
	for name, refusal := range map[string]struct {
		status int
		code   string
	}{
		"rollout switch off (haystack#9085)":     {http.StatusBadRequest, "invalid_request"},
		"row refused by the device flow (#9041)": {http.StatusBadRequest, "unauthorized_client"},
		"no device endpoint":                     {http.StatusNotFound, ""},
	} {
		t.Run(name, func(t *testing.T) {
			rig := newMCPRig(t)
			rig.hey.deviceStatus, rig.hey.deviceError = refusal.status, refusal.code

			method, err := rig.mcp.LoginMCP(context.Background(), quiet)
			if err != nil {
				t.Fatal(err)
			}
			if method != "oauth" {
				t.Errorf("method = %q, want the loopback fallback", method)
			}
			authorize, _ := url.ParseQuery(rig.hey.exchanges[0].Get("authorize"))
			if authorize.Get("client_id") != testMCPClientID || authorize.Get("scope") != MCPScope || authorize.Get("code_challenge_method") != "S256" {
				t.Errorf("authorization request = %v", authorize)
			}
			exchange := rig.hey.exchanges[1]
			if exchange.Get("client_id") != testMCPClientID {
				t.Errorf("code exchange named client %q", exchange.Get("client_id"))
			}
			creds, err := rig.mcp.StoredCredentials()
			if err != nil || creds.AccessToken != "loopback-access" || creds.Scope != MCPScope {
				t.Errorf("stored %+v, %v", creds, err)
			}
		})
	}
}

func TestMCPLoginDoesntFallBackOnOtherRefusals(t *testing.T) {
	rig := newMCPRig(t)
	rig.hey.deviceStatus, rig.hey.deviceError = http.StatusUnauthorized, "invalid_client"
	if _, err := rig.mcp.LoginMCP(context.Background(), quiet); err == nil {
		t.Fatal("an unknown client id signed in")
	}
	if len(rig.hey.exchanges) != 0 {
		t.Error("fell back to loopback for a refusal that isn't about the device flow")
	}
}

func TestMCPLoginHonorsTheScopeHEYGranted(t *testing.T) {
	for name, tc := range map[string]struct {
		granted, stored string
		canSend         bool
	}{
		"all of it":                     {"read write send", "read write send", true},
		"send unchecked":                {"read write", "read write", false},
		"left out means what was asked": {"-", "read write send", true},
	} {
		t.Run(name, func(t *testing.T) {
			rig := newMCPRig(t)
			rig.hey.grantedScope = tc.granted
			if _, err := rig.mcp.LoginMCP(context.Background(), quiet); err != nil {
				t.Fatal(err)
			}
			creds, _ := rig.mcp.StoredCredentials()
			if creds.Scope != tc.stored {
				t.Errorf("stored scope %q, want %q", creds.Scope, tc.stored)
			}
			if got := HasScope(creds.Scope, "read", "send"); got != tc.canSend {
				t.Errorf("can send = %v, want %v", got, tc.canSend)
			}
		})
	}
}

func TestMCPLoginRefusesAScopeItDidntAskFor(t *testing.T) {
	rig := newMCPRig(t)
	rig.hey.grantedScope = "full"
	if _, err := rig.mcp.LoginMCP(context.Background(), quiet); err == nil || !strings.Contains(err.Error(), "full") {
		t.Fatalf("err = %v, want a refusal naming full", err)
	}
	if _, err := rig.mcp.StoredCredentials(); !errors.Is(err, ErrCredentialsNotFound) {
		t.Errorf("a full credential was stored for hey mcp: %v", err)
	}
}

func TestMCPLoginNeedsTheLocalClientID(t *testing.T) {
	rig := newMCPRig(t)
	rig.mcp.clientID = ""
	if _, err := rig.mcp.LoginMCP(context.Background(), quiet); !errors.Is(err, ErrMCPClientUnconfigured) {
		t.Errorf("err = %v, want ErrMCPClientUnconfigured", err)
	}
	rig.mcp.clientID = oauthClientID
	if _, err := rig.mcp.LoginMCP(context.Background(), quiet); err == nil {
		t.Error("hey mcp signed in as the CLI's own client")
	}
	if rig.hey.deviceRequests != nil {
		t.Error("HEY was asked anyway")
	}
}

func TestMCPDeviceLoginStopsOnDenialAndBacksOffOnSlowDown(t *testing.T) {
	rig := newMCPRig(t)
	rig.hey.pollErrors = []string{"slow_down", "access_denied"}
	_, err := rig.mcp.LoginMCP(context.Background(), quiet)
	if err == nil || !strings.Contains(err.Error(), "denied") {
		t.Fatalf("err = %v, want denial", err)
	}
	if len(rig.waits) != 2 || rig.waits[0] != 5*time.Second || rig.waits[1] != 10*time.Second {
		t.Errorf("waits = %v, want 5s then 10s after slow_down", rig.waits)
	}
	if _, err := rig.mcp.StoredCredentials(); !errors.Is(err, ErrCredentialsNotFound) {
		t.Error("a denied sign-in stored a credential")
	}
}

func TestMCPRefreshNamesTheLocalClientAndKeepsTheScope(t *testing.T) {
	rig := newMCPRig(t)
	if err := rig.mcp.store.Save(rig.mcp.storeKey, &Credentials{
		AccessToken: "old", RefreshToken: "old-refresh", ExpiresAt: time.Now().Add(-time.Hour).Unix(),
		OAuthType: "oauth", InstallID: generateInstallIDForTest(t), Scope: MCPScope, ClientID: testMCPClientID, GrantKey: "g", GrantedAt: 1,
	}); err != nil {
		t.Fatal(err)
	}
	rig.hey.grantedScope = "read write"

	token, err := rig.mcp.AccessToken(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if token != "refreshed-access" {
		t.Errorf("token = %q", token)
	}
	if got := rig.hey.refreshes[0].Get("client_id"); got != testMCPClientID {
		t.Errorf("refresh named client %q, want the local MCP client", got)
	}
	creds, _ := rig.mcp.StoredCredentials()
	if creds.Scope != "read write" || creds.GrantKey != "g" || creds.ClientID != testMCPClientID {
		t.Errorf("after refresh: %+v", creds)
	}
}

func generateInstallIDForTest(t *testing.T) string {
	t.Helper()
	id, err := NewStore(t.TempDir()).InstallID()
	if err != nil {
		t.Fatal(err)
	}
	return id
}

func TestMCPManagerNeverUsesHEYToken(t *testing.T) {
	rig := newMCPRig(t)
	t.Setenv("HEY_TOKEN", "a-full-token-someone-exported")

	if ok, _ := rig.mcp.AuthenticationStatus(); ok {
		t.Error("HEY_TOKEN signed hey mcp in")
	}
	req := httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/boxes.json", nil)
	if err := rig.mcp.AuthenticateRequest(context.Background(), req); err == nil || req.Header.Get("Authorization") != "" {
		t.Errorf("HEY_TOKEN authenticated a hey mcp request: %q", req.Header.Get("Authorization"))
	}
	if ok, _ := rig.cli.AuthenticationStatus(); !ok {
		t.Error("the CLI stopped honoring HEY_TOKEN")
	}
}

func TestMCPRevokeNamesTheLocalClientAndTheGrant(t *testing.T) {
	rig := newMCPRig(t)
	if _, err := rig.mcp.LoginMCP(context.Background(), quiet); err != nil {
		t.Fatal(err)
	}
	if err := rig.mcp.Revoke(context.Background()); err != nil {
		t.Fatal(err)
	}
	if len(rig.hey.revocations) != 1 {
		t.Fatalf("revocations = %d", len(rig.hey.revocations))
	}
	got := rig.hey.revocations[0]
	if got.Get("client_id") != testMCPClientID || got.Get("token") != "device-refresh" {
		t.Errorf("revocation = client %q, token is the refresh token: %v", got.Get("client_id"), got.Get("token") == "device-refresh")
	}
}

func TestHasScopeReadsTheExpandedSet(t *testing.T) {
	for _, tc := range []struct {
		granted string
		want    []string
		ok      bool
	}{
		{"read write send", []string{"read", "send"}, true},
		{"write send", []string{"read", "send"}, true},
		{"read write", []string{"send"}, false},
		{"send", []string{"read", "send"}, false},
		{"", []string{"read"}, false},
	} {
		if got := HasScope(tc.granted, tc.want...); got != tc.ok {
			t.Errorf("HasScope(%q, %v) = %v, want %v", tc.granted, tc.want, got, tc.ok)
		}
	}
}
