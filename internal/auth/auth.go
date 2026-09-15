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

	// refreshHoldUntil is when this process will next send a refresh, after the
	// token endpoint rate-limited one. Guarded by mu, which every path into
	// refreshLocked already holds.
	refreshHoldUntil time.Time
}

// defaultRefreshHold is how long to sit out a rate limit the server did not put a
// Retry-After on. The token endpoint's limit is a fixed window an hour wide, so a
// process pausing this long spends only a few of the allowance per window however
// long it runs — where `hey watch`, which re-authenticates on every ActionCable
// dial and redials on a fifteen-second timeout, spends all of it in minutes and
// then keeps it spent.
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
		return "", errNoCredential(fmt.Sprintf("not authenticated: %v", err), err)
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

	creds, err := m.store.Load(m.baseURL)
	if err != nil {
		return errNoCredential(fmt.Sprintf("not authenticated: %v", err), err)
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

	return errNoCredential("no access token or session cookie available", nil)
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
		return errNoCredential(fmt.Sprintf("not authenticated: %v", err), err)
	}

	// Cookie-based auth doesn't support refresh; treat as no-op.
	if creds.RefreshToken == "" && creds.SessionCookie != "" {
		return nil
	}

	return m.refreshLocked(ctx, creds)
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
		// The credential went away while we waited for the lock, which is what
		// happens when another process just had this same grant refused and
		// forgot it. Falling back to the copy we came in with would send a token
		// already known to be dead, and spend one more of an allowance that is
		// counted per address and shared by every process here.
		return errNoCredential(fmt.Sprintf("not authenticated: %v", loadErr), loadErr)
	}
	if stored.AccessToken != "" && stored.AccessToken != creds.AccessToken {
		return nil
	}
	creds = stored

	if creds.RefreshToken == "" {
		return fmt.Errorf("no refresh token available")
	}

	if held, until := m.refreshHeld(); held {
		// The endpoint rate-limited us recently. Its limit counts refusals as
		// well as successes, so asking again now cannot get through and only
		// spends the allowance a fresh sign-in will need.
		if creds.ExpiresAt > time.Now().Unix() {
			// The five-minute buffer opened this refresh early and the access
			// token has not actually expired yet, so the command carries on with
			// the one we hold and tries again later.
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
		return m.accountForRefreshFailure(err)
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

	return m.store.save(m.baseURL, creds)
}

// accountForRefreshFailure decides what a failed refresh costs the stored credential.
// Only the server refusing the grant itself clears it; every other failure leaves it
// exactly where it was.
//
// Getting this split wrong is expensive in both directions. Keeping a refused grant is
// the bug this replaces: the credential stayed on disk, so every later command re-sent
// a token the server had already killed, and because the token endpoint's limit counts
// refusals as well as successes, a background `hey watch` could spend an hour's
// allowance in minutes and go on spending it — locking the address out of the login it
// needed to recover. Clearing on anything softer is the opposite failure: a flaky
// network or a passing 502 would sign people out of a session that was fine.
func (m *Manager) accountForRefreshFailure(err error) error {
	var refusal *tokenEndpointError
	if !errors.As(err, &refusal) {
		// A transport failure — no answer from the server at all, so no verdict on
		// the grant.
		return fmt.Errorf("token refresh failed: %w", err)
	}

	if refusal.rateLimited() {
		// Says nothing about the credential: the server declined to look at it.
		m.holdRefreshes(refusal.RetryAfter)
		return errRefreshHeld(m.refreshHoldUntil)
	}

	if !refusal.grantRefused() {
		return fmt.Errorf("token refresh failed: %w", err)
	}

	// invalid_grant. The refresh token is expired, revoked or already spent, and it
	// will be refused the same way forever. Forget it here, under the lock that
	// already spans this load-refresh-save, so the next command asks for a login
	// instead of re-sending it.
	if delErr := m.store.delete(m.baseURL); delErr != nil {
		// The grant is dead whether or not the store would let go of it, and the
		// credential is still on disk for the next command to find. Hold refreshes
		// so a cleanup that failed cannot become the same hammering by another
		// name, and still report it as the auth failure it is.
		m.holdRefreshes(0)
		return &apierr.Error{
			Code:       apierr.CodeAuth,
			Message:    fmt.Sprintf("HEY refused the stored refresh token: the session has expired or was revoked. The stored credentials could not be cleared: %v", delErr),
			Hint:       "Run: hey auth logout, then: hey auth login",
			HTTPStatus: 401,
			Cause:      delErr,
		}
	}
	return apierr.ErrAuth("HEY refused the stored refresh token: the session has expired or was revoked. Changing your HEY password ends every session, including this one. The stored credentials have been cleared")
}

// refreshHeld reports whether this process is sitting out a rate limit, and until when.
func (m *Manager) refreshHeld() (bool, time.Time) {
	if m.refreshHoldUntil.IsZero() || !time.Now().Before(m.refreshHoldUntil) {
		return false, time.Time{}
	}
	return true, m.refreshHoldUntil
}

// holdRefreshes parks refreshes until the rate limit has had time to clear, honoring
// the server's Retry-After when it sends one. The hold only ever moves later, so a
// short hint cannot shorten a longer wait already in force.
func (m *Manager) holdRefreshes(retryAfter time.Duration) {
	if retryAfter <= 0 {
		retryAfter = defaultRefreshHold
	}
	if until := time.Now().Add(retryAfter); until.After(m.refreshHoldUntil) {
		m.refreshHoldUntil = until
	}
}

// errNoCredential reports a missing or unusable credential. It carries the auth code
// so the exit status and the login hint survive the trip out through the SDK: the auth
// strategy is ours and the SDK hands our errors back untouched, so an unclassified one
// would reach the envelope as a generic API failure — exiting 7 where a script, and the
// service that restarts `hey watch`, are both watching for the auth exit.
func errNoCredential(msg string, cause error) *apierr.Error {
	return &apierr.Error{
		Code:       apierr.CodeAuth,
		Message:    msg,
		Hint:       "Run: hey auth login",
		HTTPStatus: 401,
		Cause:      cause,
	}
}

func errRefreshHeld(until time.Time) error {
	return &apierr.Error{
		Code:       apierr.CodeRateLimit,
		Message:    fmt.Sprintf("HEY is rate-limiting token requests — not asking again for %s", time.Until(until).Round(time.Second)),
		Hint:       "Asking sooner cannot get through, and spends the allowance a fresh sign-in needs. Wait for the limit to clear, then run the command again",
		HTTPStatus: 429,
	}
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
