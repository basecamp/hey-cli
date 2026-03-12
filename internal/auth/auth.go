// Package auth provides OAuth authentication for HEY.
package auth

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"os"
	"strings"
	"sync"
	"time"
)

// Built-in OAuth client credentials for the CLI app.
const (
	oauthClientID = "khMWSVDVSq78oyKA3KtxmYRv"
	installID     = "hey-cli"
)

// Manager handles OAuth authentication.
type Manager struct {
	baseURL    string
	store      *Store
	httpClient *http.Client
	mu         sync.Mutex
}

// NewManager creates a new auth manager.
func NewManager(baseURL string, httpClient *http.Client, configDir string) *Manager {
	return &Manager{
		baseURL:    normalizeBaseURL(baseURL),
		store:      NewStore(configDir),
		httpClient: httpClient,
	}
}

// normalizeBaseURL strips trailing slashes for consistent credential keys.
func normalizeBaseURL(u string) string {
	return strings.TrimRight(u, "/")
}

// AccessToken returns a valid access token, refreshing if needed.
// If HEY_TOKEN env var is set, it's used directly.
func (m *Manager) AccessToken(ctx context.Context) (string, error) {
	if token := os.Getenv("HEY_TOKEN"); token != "" {
		return token, nil
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	creds, err := m.store.Load(m.baseURL)
	if err != nil {
		return "", fmt.Errorf("not authenticated: %w", err)
	}

	// Check if token is expired (with 5-minute buffer)
	if creds.ExpiresAt > 0 && time.Now().Unix() >= creds.ExpiresAt-300 {
		if err = m.refreshLocked(ctx, creds); err != nil {
			return "", err
		}
		creds, err = m.store.Load(m.baseURL)
		if err != nil {
			return "", fmt.Errorf("failed to load refreshed credentials: %w", err)
		}
	}

	if creds.AccessToken != "" {
		return creds.AccessToken, nil
	}

	// Fall back to session cookie for cookie-based auth.
	if creds.SessionCookie != "" {
		return creds.SessionCookie, nil
	}

	return "", fmt.Errorf("no access token or session cookie available")
}

// AuthenticateRequest sets the appropriate auth header on an HTTP request.
// Uses Bearer token if available, otherwise falls back to session cookie.
func (m *Manager) AuthenticateRequest(ctx context.Context, req *http.Request) error {
	if token := os.Getenv("HEY_TOKEN"); token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
		return nil
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	creds, err := m.store.Load(m.baseURL)
	if err != nil {
		return fmt.Errorf("not authenticated: %w", err)
	}

	if creds.AccessToken != "" {
		// Auto-refresh if needed
		if creds.ExpiresAt > 0 && time.Now().Unix() >= creds.ExpiresAt-300 {
			if err = m.refreshLocked(ctx, creds); err != nil {
				return err
			}
			creds, err = m.store.Load(m.baseURL)
			if err != nil {
				return fmt.Errorf("failed to load refreshed credentials: %w", err)
			}
		}
		req.Header.Set("Authorization", "Bearer "+creds.AccessToken)
		return nil
	}

	if creds.SessionCookie != "" {
		req.Header.Set("Cookie", "session_token="+creds.SessionCookie)
		return nil
	}

	return fmt.Errorf("no access token or session cookie available")
}

// IsAuthenticated checks if there are valid credentials.
func (m *Manager) IsAuthenticated() bool {
	if os.Getenv("HEY_TOKEN") != "" {
		return true
	}

	creds, err := m.store.Load(m.baseURL)
	if err != nil {
		return false
	}
	return creds.AccessToken != "" || creds.SessionCookie != ""
}

// LoginOptions configures the login flow.
type LoginOptions struct {
	NoBrowser bool
}

// Login initiates the browser-based OAuth login flow with PKCE.
func (m *Manager) Login(ctx context.Context, opts LoginOptions) error {
	authEndpoint := m.baseURL + "/oauth/authorizations/new"
	tokenEndpoint := m.baseURL + "/oauth/tokens"
	callbackAddr := "127.0.0.1:8976"
	redirectURI := "http://" + callbackAddr + "/callback"

	state := generateState()
	codeVerifier := generateCodeVerifier()
	codeChallenge := generateCodeChallenge(codeVerifier)

	// Build authorization URL
	u, err := url.Parse(authEndpoint)
	if err != nil {
		return fmt.Errorf("invalid auth endpoint: %w", err)
	}
	q := u.Query()
	q.Set("client_id", oauthClientID)
	q.Set("grant_type", "authorization_code")
	q.Set("redirect_uri", redirectURI)
	q.Set("state", state)
	q.Set("code_challenge", codeChallenge)
	q.Set("code_challenge_method", "S256")
	q.Set("install_id", installID)
	u.RawQuery = q.Encode()
	authURL := u.String()

	// Start local callback server
	code, err := m.waitForCallback(ctx, state, authURL, callbackAddr, opts)
	if err != nil {
		return err
	}

	// Exchange code for tokens
	token, err := exchangeCode(ctx, m.httpClient, tokenEndpoint, code, redirectURI, oauthClientID, codeVerifier, installID)
	if err != nil {
		return fmt.Errorf("token exchange failed: %w", err)
	}

	creds := &Credentials{
		AccessToken:   token.AccessToken,
		RefreshToken:  token.RefreshToken,
		OAuthType:     "oauth",
		TokenEndpoint: tokenEndpoint,
	}
	if !token.ExpiresAt.IsZero() {
		creds.ExpiresAt = token.ExpiresAt.Unix()
	}

	return m.store.Save(m.baseURL, creds)
}

// LoginWithToken stores a pre-provided bearer token.
func (m *Manager) LoginWithToken(token string) error {
	creds := &Credentials{
		AccessToken: token,
		OAuthType:   "token",
	}
	return m.store.Save(m.baseURL, creds)
}

// LoginWithCookie stores a session cookie.
func (m *Manager) LoginWithCookie(cookie string) error {
	creds := &Credentials{
		SessionCookie: cookie,
		OAuthType:     "cookie",
	}
	return m.store.Save(m.baseURL, creds)
}

// Logout removes stored credentials.
func (m *Manager) Logout() error {
	return m.store.Delete(m.baseURL)
}

// Refresh forces a token refresh.
func (m *Manager) Refresh(ctx context.Context) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	creds, err := m.store.Load(m.baseURL)
	if err != nil {
		return fmt.Errorf("not authenticated: %w", err)
	}

	// Cookie-based auth doesn't support refresh; treat as no-op.
	if creds.RefreshToken == "" && creds.SessionCookie != "" {
		return nil
	}

	return m.refreshLocked(ctx, creds)
}

func (m *Manager) refreshLocked(ctx context.Context, creds *Credentials) error {
	if creds.RefreshToken == "" {
		return fmt.Errorf("no refresh token available")
	}

	tokenEndpoint := creds.TokenEndpoint
	if tokenEndpoint == "" {
		tokenEndpoint = m.baseURL + "/oauth/tokens"
	}

	token, err := refreshOAuthToken(ctx, m.httpClient, tokenEndpoint, creds.RefreshToken, oauthClientID, installID)
	if err != nil {
		return fmt.Errorf("token refresh failed: %w", err)
	}

	creds.AccessToken = token.AccessToken
	if token.RefreshToken != "" {
		creds.RefreshToken = token.RefreshToken
	}
	if !token.ExpiresAt.IsZero() {
		creds.ExpiresAt = token.ExpiresAt.Unix()
	}

	return m.store.Save(m.baseURL, creds)
}

// GetStore returns the credential store.
func (m *Manager) GetStore() *Store {
	return m.store
}

// CredentialKey returns the base URL used as the credential storage key.
func (m *Manager) CredentialKey() string {
	return m.baseURL
}

func (m *Manager) waitForCallback(ctx context.Context, expectedState, authURL, callbackAddr string, opts LoginOptions) (string, error) {
	lc := net.ListenConfig{}
	listener, err := lc.Listen(ctx, "tcp", callbackAddr)
	if err != nil {
		return "", fmt.Errorf("failed to start callback server: %w", err)
	}
	defer func() { _ = listener.Close() }()

	codeCh := make(chan string, 1)
	errCh := make(chan error, 1)
	var shutdownOnce sync.Once

	server := &http.Server{
		ReadHeaderTimeout: 10 * time.Second,
		ReadTimeout:       15 * time.Second,
		WriteTimeout:      10 * time.Second,
		IdleTimeout:       30 * time.Second,
	}

	shutdownServer := func() {
		shutdownOnce.Do(func() {
			shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second) //nolint:gosec // G118: cancel deferred in goroutine; async shutdown required to avoid handler self-deadlock
			go func() {
				defer cancel()
				if shutdownErr := server.Shutdown(shutdownCtx); shutdownErr != nil && !errors.Is(shutdownErr, http.ErrServerClosed) {
					fmt.Fprintf(os.Stderr, "warning: callback server shutdown failed: %v\n", shutdownErr)
				}
			}()
		})
	}

	server.Handler = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		state := r.URL.Query().Get("state")
		code := r.URL.Query().Get("code")
		errParam := r.URL.Query().Get("error")

		if errParam != "" {
			errCh <- fmt.Errorf("OAuth error: %s", errParam)
			fmt.Fprint(w, "<html><body><h1>Authentication failed</h1><p>You can close this window.</p></body></html>")
			shutdownServer()
			return
		}

		if state != expectedState {
			errCh <- fmt.Errorf("state mismatch: CSRF protection failed")
			fmt.Fprint(w, "<html><body><h1>Authentication failed</h1><p>State mismatch.</p></body></html>")
			shutdownServer()
			return
		}

		codeCh <- code
		fmt.Fprint(w, "<html><body><h1>Authentication successful!</h1><p>You can close this window.</p></body></html>")
		shutdownServer()
	})

	go server.Serve(listener) //nolint:errcheck

	if !opts.NoBrowser {
		if err := openBrowser(authURL); err != nil {
			fmt.Fprintf(os.Stderr, "\nCouldn't open browser automatically.\nOpen this URL in your browser:\n%s\n\nWaiting for authentication...\n", authURL)
		} else {
			fmt.Fprintf(os.Stderr, "\nOpening browser for authentication...\nIf the browser doesn't open, visit: %s\n\nWaiting for authentication...\n", authURL)
		}
	} else {
		fmt.Fprintf(os.Stderr, "\nOpen this URL in your browser:\n%s\n\nWaiting for authentication...\n", authURL)
	}

	select {
	case code := <-codeCh:
		return code, nil
	case err := <-errCh:
		return "", err
	case <-ctx.Done():
		return "", ctx.Err()
	case <-time.After(5 * time.Minute):
		return "", fmt.Errorf("authentication timeout")
	}
}
