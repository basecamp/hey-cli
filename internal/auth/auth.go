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

	"github.com/basecamp/hey-cli/internal/apierr"
)

// Built-in OAuth client ID for the CLI app.
const oauthClientID = "khMWSVDVSq78oyKA3KtxmYRv"

type callbackWaiter func(context.Context, string, string, net.Listener, LoginOptions) (string, error)
type listenerFactory func(context.Context, string, string) (net.Listener, error)

// The callback listener binds an ephemeral loopback port (RFC 8252 §7.3):
// a fixed port lets any local process squat it and block sign-in.
const callbackListenAddr = "127.0.0.1:0"

// Manager handles OAuth authentication.
type Manager struct {
	baseURL      string
	store        *Store
	httpClient   *http.Client
	callbackWait callbackWaiter
	listen       listenerFactory
	mu           sync.Mutex

	// cachedCredentials avoids opening a keyring session for every request. It is
	// process-local; refreshLocked still rereads the store to coordinate rotation
	// with other processes. Guarded by mu.
	cachedCredentials *Credentials

	// refreshHoldUntil parks refreshes after the token endpoint rate-limited one.
	// Guarded by mu, which every path into refreshLocked already holds.
	refreshHoldUntil time.Time

	// refusedRefreshToken is a grant the server refused that the store could not
	// delete, so this process remembers not to send it again. Guarded by mu.
	refusedRefreshToken string

	// credentialCleared runs after the manager has deleted a credential on its own
	// verdict, so the owner of the response cache can drop what that credential
	// fetched. Logout is not that: its callers already clear the cache themselves.
	credentialCleared func()
}

// defaultRefreshHold is how long to sit out a rate limit that came without a
// Retry-After. The token endpoint's window is an hour and it counts refusals, so a
// `hey watch` redialling every fifteen seconds would spend it in minutes.
const defaultRefreshHold = 15 * time.Minute

// NewManager creates a new auth manager.
func NewManager(baseURL string, httpClient *http.Client, configDir string) *Manager {
	listenConfig := &net.ListenConfig{}
	return &Manager{
		baseURL:    normalizeBaseURL(baseURL),
		store:      NewStore(configDir),
		httpClient: httpClient,
		listen:     listenConfig.Listen,
	}
}

// normalizeBaseURL strips trailing slashes for consistent credential keys.
func normalizeBaseURL(u string) string {
	return strings.TrimRight(u, "/")
}

func cloneCredentials(creds *Credentials) *Credentials {
	if creds == nil {
		return nil
	}
	clone := *creds
	return &clone
}

// AccessToken returns a valid access token, refreshing if needed.
// If HEY_TOKEN env var is set, it's used directly.
func (m *Manager) AccessToken(ctx context.Context) (string, error) {
	if token := os.Getenv("HEY_TOKEN"); token != "" {
		return token, nil
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	creds, err := m.loadCredentialsLocked()
	if err != nil {
		return "", errNotAuthenticated(err)
	}

	// Check if token is expired (with 5-minute buffer)
	if creds.ExpiresAt > 0 && time.Now().Unix() >= creds.ExpiresAt-300 {
		if err = m.refreshLocked(ctx, creds); err != nil {
			return "", err
		}
		creds, err = m.loadCredentialsLocked()
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

	return "", errNoCredential("no access token or session cookie available", nil)
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

	creds, err := m.loadCredentialsLocked()
	if err != nil {
		return errNotAuthenticated(err)
	}

	if creds.AccessToken != "" {
		// Auto-refresh if needed
		if creds.ExpiresAt > 0 && time.Now().Unix() >= creds.ExpiresAt-300 {
			if err = m.refreshLocked(ctx, creds); err != nil {
				return err
			}
			creds, err = m.loadCredentialsLocked()
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

	return errNoCredential("no access token or session cookie available", nil)
}

// IsAuthenticated checks if there are valid credentials.
func (m *Manager) IsAuthenticated() bool {
	if os.Getenv("HEY_TOKEN") != "" {
		return true
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	creds, err := m.loadCredentialsLocked()
	if err != nil {
		return false
	}
	return creds.AccessToken != "" || creds.SessionCookie != ""
}

// LoginOptions configures the login flow.
type LoginOptions struct {
	NoBrowser bool

	// Logger receives login progress messages (browser hand-off, the
	// authorization URL, waiting notice). Nil keeps the default os.Stderr
	// output unchanged. Messages may span multiple lines.
	Logger func(msg string)
}

// log routes a progress message to the configured Logger, or to os.Stderr
// verbatim when none is set so `hey auth login` output stays as it was.
func (o LoginOptions) log(msg string) {
	if o.Logger != nil {
		o.Logger(msg)
		return
	}
	fmt.Fprint(os.Stderr, msg)
}

// Login initiates the browser-based OAuth login flow with PKCE.
func (m *Manager) Login(ctx context.Context, opts LoginOptions) error {
	authEndpoint := m.baseURL + "/oauth/authorizations/new"
	tokenEndpoint := m.baseURL + "/oauth/tokens"

	// Listen before building the authorization URL: the redirect_uri has to
	// carry whichever port the kernel handed out.
	listener, err := m.listen(ctx, "tcp", callbackListenAddr)
	if err != nil {
		return fmt.Errorf("failed to start callback server: %w", err)
	}
	defer func() { _ = listener.Close() }()
	redirectURI := "http://" + listener.Addr().String() + "/callback"

	installID, err := m.store.InstallID()
	if err != nil {
		return fmt.Errorf("install id: %w", err)
	}

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

	// Serve the callback on the listener we already hold
	waitForCallback := m.callbackWait
	if waitForCallback == nil {
		waitForCallback = m.waitForCallback
	}
	code, err := waitForCallback(ctx, state, authURL, listener, opts)
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

	return m.saveCredentials(creds)
}

// LoginWithToken stores a pre-provided bearer token.
func (m *Manager) LoginWithToken(token string) error {
	creds := &Credentials{
		AccessToken: token,
		OAuthType:   "token",
	}
	return m.saveCredentials(creds)
}

// LoginWithCookie stores a session cookie.
func (m *Manager) LoginWithCookie(cookie string) error {
	creds := &Credentials{
		SessionCookie: cookie,
		OAuthType:     "cookie",
	}
	return m.saveCredentials(creds)
}

// OnCredentialCleared registers what to run when the manager clears a credential
// on its own — today, when the server has refused the refresh token. It does not
// run for Logout, whose callers clear the cache themselves, and not when the store
// refused the deletion, because the credential is then still there to be used.
func (m *Manager) OnCredentialCleared(fn func()) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.credentialCleared = fn
}

// Logout removes stored credentials.
func (m *Manager) Logout() error {
	m.mu.Lock()
	defer m.mu.Unlock()

	if err := m.store.Delete(m.baseURL); err != nil {
		return err
	}
	m.cachedCredentials = nil
	return nil
}

// Refresh forces a token refresh.
func (m *Manager) Refresh(ctx context.Context) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	creds, err := m.loadCredentialsLocked()
	if err != nil {
		return errNotAuthenticated(err)
	}
	return m.refreshLocked(ctx, creds)
}

func (m *Manager) loadCredentialsLocked() (*Credentials, error) {
	if m.cachedCredentials != nil {
		return cloneCredentials(m.cachedCredentials), nil
	}

	creds, err := m.store.Load(m.baseURL)
	if err != nil {
		return nil, err
	}
	m.cachedCredentials = cloneCredentials(creds)
	return cloneCredentials(creds), nil
}

func (m *Manager) saveCredentials(creds *Credentials) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	if err := m.store.Save(m.baseURL, creds); err != nil {
		return err
	}
	m.cachedCredentials = cloneCredentials(creds)
	return nil
}

// refreshLocked holds the store's cross-process lock for the whole load-refresh-save,
// because `hey tui` and `hey watch` are separate processes and the mutex only serializes
// this one. Credentials are read again inside the lock: a token another process refreshed
// while we waited for it is the one to keep, and with rotation the refresh token we came
// in with has already been consumed.
func (m *Manager) refreshLocked(ctx context.Context, creds *Credentials) error {
	unlock, err := m.store.lock()
	if err != nil {
		return err
	}
	defer unlock()

	stored, loadErr := m.store.load(m.baseURL)
	if loadErr != nil {
		// Another process may have deleted the credential while we waited for
		// the lock. Nothing stale may survive a forced read's failure.
		m.cachedCredentials = nil
		return errNotAuthenticated(loadErr)
	}
	m.cachedCredentials = cloneCredentials(stored)
	if stored.AccessToken != creds.AccessToken || stored.SessionCookie != creds.SessionCookie {
		return nil
	}
	creds = cloneCredentials(stored)

	// Cookie-based auth has nothing to refresh. The fresh read above still lets
	// a 401 adopt a cookie another process stored before the SDK retries.
	if creds.RefreshToken == "" && creds.SessionCookie != "" {
		return nil
	}
	if creds.RefreshToken == "" {
		return fmt.Errorf("no refresh token available")
	}

	if m.refusedRefreshToken != "" && creds.RefreshToken == m.refusedRefreshToken {
		// Refused once already, and the store would not let go of it.
		return errRefusedGrant(nil)
	}

	if held, until := m.refreshHeld(); held {
		// Asking again inside the rate limit cannot get through. A token that has
		// not actually expired is still good, so carry on with it: the SDK's own
		// retry after a 401 costs one request at most.
		if creds.ExpiresAt > time.Now().Unix() {
			return nil
		}
		return errRefreshHeld(until)
	}

	tokenEndpoint := creds.TokenEndpoint
	if tokenEndpoint == "" {
		tokenEndpoint = m.baseURL + "/oauth/tokens"
	}

	installID, err := m.store.installID()
	if err != nil {
		return fmt.Errorf("install id: %w", err)
	}

	token, err := refreshOAuthToken(ctx, m.httpClient, tokenEndpoint, creds.RefreshToken, oauthClientID, installID)
	if err != nil {
		return m.accountForRefreshFailure(err, creds.RefreshToken)
	}
	m.refreshHoldUntil = time.Time{}
	// A 200 without an access token is not a refresh. Storing the empty string would
	// take the working token with it and leave nothing to authenticate with.
	if token.AccessToken == "" {
		return fmt.Errorf("token refresh returned no access token")
	}

	creds.AccessToken = token.AccessToken
	if token.RefreshToken != "" {
		creds.RefreshToken = token.RefreshToken
	}
	if token.ExpiresAt.IsZero() {
		creds.ExpiresAt = 0
	} else {
		creds.ExpiresAt = token.ExpiresAt.Unix()
	}

	if err := m.store.save(m.baseURL, creds); err != nil {
		return err
	}
	m.cachedCredentials = cloneCredentials(creds)
	return nil
}

// accountForRefreshFailure decides what a failed refresh costs the stored credential.
// Only invalid_grant clears it: that is the one answer that proves re-sending can never
// work. Anything softer keeps it, or a passing 502 would sign people out.
func (m *Manager) accountForRefreshFailure(err error, sentRefreshToken string) error {
	var refusal *tokenEndpointError
	if !errors.As(err, &refusal) {
		// No answer from the server, so no verdict on the grant.
		return fmt.Errorf("token refresh failed: %w", err)
	}

	if refusal.rateLimited() {
		m.holdRefreshes(refusal.RetryAfter)
		return errRefreshHeld(m.refreshHoldUntil)
	}

	if !refusal.grantRefused() {
		return fmt.Errorf("token refresh failed: %w", err)
	}

	// Forget it under the lock that already spans this load-refresh-save, so the
	// next command asks for a login instead of re-sending it.
	if delErr := m.store.delete(m.baseURL); delErr != nil {
		// The credential is still on disk for the next command to load, so the
		// refusal is remembered here instead.
		m.refusedRefreshToken = sentRefreshToken
		return errRefusedGrant(delErr)
	}
	m.cachedCredentials = nil
	// Cached mail must not outlive the credential that fetched it, here as much
	// as on an explicit logout.
	if m.credentialCleared != nil {
		m.credentialCleared()
	}
	return errRefusedGrant(nil)
}

// refreshHeld reports whether this process is sitting out a rate limit, and until when.
func (m *Manager) refreshHeld() (bool, time.Time) {
	if m.refreshHoldUntil.IsZero() || !time.Now().Before(m.refreshHoldUntil) {
		return false, time.Time{}
	}
	return true, m.refreshHoldUntil
}

// holdRefreshes parks refreshes until the rate limit has had time to clear, honoring
// the server's Retry-After when it sends one. The hold only ever moves later.
func (m *Manager) holdRefreshes(retryAfter time.Duration) {
	if retryAfter <= 0 {
		retryAfter = defaultRefreshHold
	}
	if until := time.Now().Add(retryAfter); until.After(m.refreshHoldUntil) {
		m.refreshHoldUntil = until
	}
}

// errNoCredential is an auth failure with the store's reason attached. It has to be
// classified here: the SDK hands our errors back untouched, so an unclassified one
// would reach the envelope as a generic API failure without the login hint.
func errNoCredential(msg string, cause error) *apierr.Error {
	err := apierr.ErrAuth(msg)
	err.Cause = cause
	return err
}

func errNotAuthenticated(cause error) *apierr.Error {
	return errNoCredential(fmt.Sprintf("not authenticated: %v", cause), cause)
}

// errRefusedGrant reports a refresh token the server answered invalid_grant for.
// cleanupErr is the failure to clear it from the store, when there was one.
func errRefusedGrant(cleanupErr error) *apierr.Error {
	if cleanupErr != nil {
		err := errNoCredential(fmt.Sprintf("HEY refused the stored refresh token: the session has expired or was revoked. The stored credentials could not be cleared: %v", cleanupErr), cleanupErr)
		err.Hint = "Run: hey auth logout, then: hey auth login"
		return err
	}
	return apierr.ErrAuth("HEY refused the stored refresh token: the session has expired or was revoked. Changing your HEY password ends every session, including this one. The stored credentials have been cleared")
}

func errRefreshHeld(until time.Time) *apierr.Error {
	wait := time.Until(until).Round(time.Second)
	err := apierr.ErrRateLimit(int(wait / time.Second))
	err.Message = fmt.Sprintf("HEY is rate-limiting token requests — not asking again for %s", wait)
	err.Hint = "Wait for the limit to clear, then run the command again"
	return err
}

// GetStore returns the credential store.
func (m *Manager) GetStore() *Store {
	return m.store
}

// CredentialKey returns the base URL used as the credential storage key.
func (m *Manager) CredentialKey() string {
	return m.baseURL
}

func (m *Manager) waitForCallback(ctx context.Context, expectedState, authURL string, listener net.Listener, opts LoginOptions) (string, error) {
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
				// The caller closes the listener on the way out of Login, and
				// Shutdown closes it too; whichever gets there second sees
				// net.ErrClosed, which is the server being down, not a failure.
				if shutdownErr := server.Shutdown(shutdownCtx); shutdownErr != nil &&
					!errors.Is(shutdownErr, http.ErrServerClosed) && !errors.Is(shutdownErr, net.ErrClosed) {
					fmt.Fprintf(os.Stderr, "warning: callback server shutdown failed: %v\n", shutdownErr)
				}
			}()
		})
	}

	mux := http.NewServeMux()
	mux.HandleFunc("/callback", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.Header().Set("Cache-Control", "no-store")

		state := r.URL.Query().Get("state")
		code := r.URL.Query().Get("code")
		errParam := r.URL.Query().Get("error")

		// Sends are non-blocking: only the first request's result matters,
		// and waitForCallback stops reading once it returns.
		fail := func(err error, page string) {
			select {
			case errCh <- err:
			default:
			}
			fmt.Fprint(w, page)
			shutdownServer()
		}

		switch {
		case state != expectedState:
			fail(fmt.Errorf("state mismatch: CSRF protection failed"), callbackInvalid)
		case errParam == "access_denied":
			fail(fmt.Errorf("OAuth error: %s", errParam), callbackDenied)
		case errParam != "":
			fail(fmt.Errorf("OAuth error: %s", errParam), callbackError)
		case code == "":
			fail(fmt.Errorf("OAuth callback missing authorization code"), callbackError)
		default:
			select {
			case codeCh <- code:
			default:
			}
			fmt.Fprint(w, callbackSuccess)
			shutdownServer()
		}
	})
	server.Handler = mux

	go server.Serve(listener) //nolint:errcheck

	if !opts.NoBrowser {
		if err := openBrowser(authURL); err != nil {
			opts.log(fmt.Sprintf("\nCouldn't open browser automatically.\nOpen this URL in your browser:\n%s\n\nWaiting for authentication...\n", authURL))
		} else {
			opts.log(fmt.Sprintf("\nOpening browser for authentication...\nIf the browser doesn't open, visit: %s\n\nWaiting for authentication...\n", authURL))
		}
	} else {
		opts.log(fmt.Sprintf("\nOpen this URL in your browser:\n%s\n\nWaiting for authentication...\n", authURL))
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
