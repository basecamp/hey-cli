package auth

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"slices"
	"strings"

	"github.com/basecamp/hey-cli/internal/version"
)

// hey mcp's credential (haystack#9041, #9085).
//
// `hey mcp` signs in as HEY's local MCP client, "HEY MCP (local)", not as the CLI. The
// row's allowlist is read write send and never full, so a model driving hey mcp can
// reach what those scopes declare and nothing else: no settings, forwarding, trash or
// HEY World. It has its own entry in Connected Apps and can be revoked without signing
// the CLI out.
//
// The credential is stored apart from the CLI's: its own keychain item (and its own
// entry in the plaintext fallback file), under storeKey "mcp::<origin>". It refreshes
// as the local client. HEY_TOKEN never stands in for it.

// MCPScope is what `hey auth login --mcp` asks for. A scoped row that names no scope
// is refused (haystack#9085), so the sign-in always sends it.
const MCPScope = "read write send"

// mcpStoreKeyPrefix keeps hey mcp's credential out of the CLI's keychain item.
const mcpStoreKeyPrefix = "mcp::"

// ErrMCPClientUnconfigured means this HEY has no local MCP client id configured yet.
var ErrMCPClientUnconfigured = errors.New("no HEY MCP (local) client id is configured for this HEY")

// NewMCPManager is the manager for hey mcp's credential, signing in and refreshing as
// the local MCP client clientID.
func NewMCPManager(baseURL string, httpClient *http.Client, configDir, clientID string) *Manager {
	m := NewManager(baseURL, httpClient, configDir)
	m.storeKey = mcpStoreKeyPrefix + m.baseURL
	m.clientID = clientID
	m.scope = MCPScope
	m.honorEnvToken = false
	return m
}

// ClientID is the OAuth client this manager signs in as.
func (m *Manager) ClientID() string {
	return m.clientID
}

// LoginMCP signs in the local MCP client. It uses the device flow where HEY serves
// scoped device requests, and otherwise loopback authorization with the same scope,
// returning which one it used ("device" or "oauth").
func (m *Manager) LoginMCP(ctx context.Context, opts LoginOptions) (string, error) {
	switch m.clientID {
	case "":
		return "", ErrMCPClientUnconfigured
	case oauthClientID:
		// A mistyped HEY_MCP_CLIENT_ID pointing at the CLI's own row would sign hey mcp
		// in with full, which is the thing this credential exists to avoid.
		return "", fmt.Errorf("the configured MCP client id is the CLI's own; set the HEY MCP (local) client id")
	}

	err := m.LoginDevice(ctx, opts)
	if !errors.Is(err, errDeviceFlowUnavailable) {
		return "device", err
	}
	opts.log(fmt.Sprintf("\n%v, so signing in through the browser instead.\n", err))
	return "oauth", m.Login(ctx, opts)
}

// PinGrant makes grantKey the only sign-in this manager authenticates with from now on
// (pinnedGrant). hey mcp pins the sign-in it starts on.
func (m *Manager) PinGrant(grantKey string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.pinnedGrant = grantKey
}

// ErrGrantChanged is a stored credential under a different sign-in than the pinned one.
var ErrGrantChanged = errors.New("hey mcp was signed in again, or signed out, since it started; restart it")

func (m *Manager) checkPinnedGrant(creds *Credentials) error {
	if m.pinnedGrant != "" && creds.GrantKey != m.pinnedGrant {
		return errNoCredential(ErrGrantChanged.Error(), ErrGrantChanged)
	}
	return nil
}

// StoredCredentials reads this manager's stored credential.
func (m *Manager) StoredCredentials() (*Credentials, error) {
	return m.store.Load(m.storeKey)
}

// Revoke asks HEY to revoke the stored grant (DELETE /oauth/tokens). It sends the
// refresh token when there is one, since revoking it ends the whole grant.
func (m *Manager) Revoke(ctx context.Context) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	creds, err := m.store.Load(m.storeKey)
	if err != nil {
		return err
	}
	token := creds.RefreshToken
	if token == "" {
		token = creds.AccessToken
	}
	if token == "" {
		return nil
	}
	clientID := creds.ClientID
	if clientID == "" {
		clientID = m.clientID
	}

	form := url.Values{"client_id": {clientID}, "token": {token}}
	req, err := http.NewRequestWithContext(ctx, http.MethodDelete, m.baseURL+"/oauth/tokens", strings.NewReader(form.Encode()))
	if err != nil {
		return fmt.Errorf("creating revocation request: %w", err)
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("User-Agent", version.UserAgent())

	resp, err := m.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("revocation request failed: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		// The body can echo the request, so only the status is reported.
		return fmt.Errorf("HEY answered the revocation with HTTP %d", resp.StatusCode)
	}
	return nil
}

// scopeImplies is HEY's scope taxonomy as far as hey mcp reads it: write implies
// read (haystack Oauth::Scope), and full implies everything.
var scopeImplies = map[string][]string{
	"write": {"read"},
}

// expandScope returns the scopes granted holds, implications included.
func expandScope(granted string) map[string]bool {
	held := map[string]bool{}
	for _, s := range strings.Fields(granted) {
		held[s] = true
		for _, implied := range scopeImplies[s] {
			held[implied] = true
		}
	}
	return held
}

// HasScope reports whether a granted scope string holds every scope in want, by the
// expanded set rather than the literal string.
func HasScope(granted string, want ...string) bool {
	held := expandScope(granted)
	if held["full"] {
		return true
	}
	for _, w := range want {
		if !held[w] {
			return false
		}
	}
	return true
}

// grantedScope reads the scope a token response granted against what was asked for.
// An omitted scope means exactly what was requested (RFC 6749 §5.1). A narrower one is
// taken as it is, since the person may have unchecked send on the consent page. One that
// names anything not asked for, full above all, is refused: it would mean the client id
// isn't the local MCP client's.
func grantedScope(requested, returned string) (string, error) {
	if strings.TrimSpace(returned) == "" {
		return strings.Join(strings.Fields(requested), " "), nil
	}
	asked := strings.Fields(requested)
	var granted []string
	for _, s := range strings.Fields(returned) {
		if !slices.Contains(asked, s) {
			return "", fmt.Errorf("HEY granted scope %q, which this sign-in didn't ask for; check that the MCP client id is HEY MCP (local)'s", s)
		}
		if !slices.Contains(granted, s) {
			granted = append(granted, s)
		}
	}
	return strings.Join(granted, " "), nil
}
