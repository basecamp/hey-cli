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
	"github.com/basecamp/hey-cli/internal/config"
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
	baseURL string
	// storeKey is where the credential lives in the store: the origin for the CLI's
	// own, "mcp::" and the origin for hey mcp's (mcp.go), and under a profile
	// "profile:<name>" and "mcp:profile:<name>" (UseProfile).
	storeKey string
	// mcp marks hey mcp's manager, whose credential is kept apart from the CLI's.
	mcp bool
	// profile is the profile this manager's credential belongs to, "" for none.
	profile string
	// clientID is the OAuth client this manager signs in and refreshes as.
	clientID string
	// scope is what a sign-in asks for. Empty asks for none, which HEY grants the
	// CLI's own row as full.
	scope string
	// honorEnvToken lets HEY_TOKEN stand in for the stored credential. Only the CLI's
	// manager honors it: the variable holds whatever token a person exported, usually
	// a full one, and hey mcp must never end up holding that.
	honorEnvToken bool
	// wait sleeps between device-flow polls; a seam for tests.
	wait func(context.Context, time.Duration) error
	// pinnedGrant, when set, is the only sign-in this manager authenticates with: a
	// credential stored under another grant key (a later `hey auth login --mcp`) is
	// refused rather than adopted, so a running hey mcp never moves onto a sign-in it
	// didn't start with: the tools it serves follow the grant it started on. Guarded
	// by mu.
	pinnedGrant string

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

	// credentialCleared runs after the manager has deleted a credential, or
	// replaced it with a signed-out record, on its own verdict. The owner of the
	// response cache can then drop what that credential fetched. Logout is not
	// that: its callers already clear the cache themselves.
	credentialCleared func()
}

// defaultRefreshHold is how long to sit out a rate limit that came without a
// Retry-After. The token endpoint's window is an hour and it counts refusals, so a
// `hey watch` redialling every fifteen seconds would spend it in minutes.
const defaultRefreshHold = 15 * time.Minute

// NewManager creates a new auth manager.
func NewManager(baseURL string, httpClient *http.Client, configDir string) *Manager {
	listenConfig := &net.ListenConfig{}
	origin := normalizeBaseURL(baseURL)
	return &Manager{
		baseURL:       origin,
		storeKey:      origin,
		clientID:      oauthClientID,
		honorEnvToken: true,
		wait:          sleepContext,
		store:         NewStore(configDir),
		httpClient:    httpClient,
		listen:        listenConfig.Listen,
	}
}

// sleepContext waits d, or until ctx is done.
func sleepContext(ctx context.Context, d time.Duration) error {
	timer := time.NewTimer(d)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
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
// If HEY_TOKEN env var is set, the CLI's manager uses it directly.
func (m *Manager) AccessToken(ctx context.Context) (string, error) {
	return m.accessToken(ctx, true)
}

// StoredAccessToken returns only the stored bearer token, ignoring HEY_TOKEN.
func (m *Manager) StoredAccessToken(ctx context.Context) (string, error) {
	return m.accessToken(ctx, false)
}

func (m *Manager) accessToken(ctx context.Context, useEnv bool) (string, error) {
	if token := m.envToken(); useEnv && token != "" {
		return token, nil
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	creds, err := m.loadCredentialsLocked()
	if err != nil {
		return "", credentialLoadError(err)
	}

	if creds.AccessToken == "" && creds.RefreshToken == "" {
		return "", missingBearer(creds)
	}

	// Recover a missing access token, or refresh an expired one (with a 5-minute buffer).
	if creds.AccessToken == "" || (creds.ExpiresAt > 0 && time.Now().Unix() >= creds.ExpiresAt-300) {
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

	return "", missingBearer(creds)
}

// AuthenticateRequest sets the appropriate auth header on an HTTP request.
// Uses only bearer tokens; obsolete browser cookies are never sent.
func (m *Manager) AuthenticateRequest(ctx context.Context, req *http.Request) error {
	return m.authenticateRequest(ctx, req, false)
}

// AuthenticateRequestFromStore rereads persistent credentials before setting the
// auth header. Long-lived connections use it when they dial again, since they do
// not have the SDK's 401 path to invalidate this manager's process-local cache.
func (m *Manager) AuthenticateRequestFromStore(ctx context.Context, req *http.Request) error {
	return m.authenticateRequest(ctx, req, true)
}

// envToken is HEY_TOKEN, for a manager that honors it. A nil manager is the CLI's, as
// it always was: HEY_TOKEN alone authenticates.
func (m *Manager) envToken() string {
	if m != nil && !m.honorEnvToken {
		return ""
	}
	return os.Getenv("HEY_TOKEN")
}

func (m *Manager) authenticateRequest(ctx context.Context, req *http.Request, reload bool) error {
	if token := m.envToken(); token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
		return nil
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	var creds *Credentials
	var err error
	if reload {
		creds, err = m.reloadCredentialsLocked()
	} else {
		creds, err = m.loadCredentialsLocked()
	}
	if err != nil {
		return credentialLoadError(err)
	}

	if creds.AccessToken != "" || creds.RefreshToken != "" {
		// Recover a missing access token as well as an expired one.
		if creds.AccessToken == "" || (creds.ExpiresAt > 0 && time.Now().Unix() >= creds.ExpiresAt-300) {
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

	return missingBearer(creds)
}

// AuthenticationStatus reports whether usable credentials exist. A missing
// credential is a known signed-out state; a storage failure leaves the state
// unknown and is returned to the caller.
func (m *Manager) AuthenticationStatus() (bool, error) {
	if m.envToken() != "" {
		return true, nil
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	creds, err := m.loadCredentialsLocked()
	if errors.Is(err, ErrCredentialsNotFound) {
		return false, nil
	}
	if err != nil {
		return false, credentialLoadError(err)
	}
	return creds.AccessToken != "" || creds.RefreshToken != "", nil
}

// IsAuthenticated reports whether usable credentials exist. Callers that need
// to distinguish signed out from unavailable storage use AuthenticationStatus.
func (m *Manager) IsAuthenticated() bool {
	authenticated, _ := m.AuthenticationStatus()
	return authenticated
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
	q.Set("client_id", m.clientID)
	if m.scope != "" {
		q.Set("scope", m.scope)
	}
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
	token, err := exchangeCode(ctx, m.httpClient, tokenEndpoint, code, redirectURI, m.clientID, codeVerifier, installID)
	if err != nil {
		return fmt.Errorf("token exchange failed: %w", err)
	}

	creds, err := m.signedInCredentials(token, tokenEndpoint, installID)
	if err != nil {
		return err
	}
	return m.saveCredentials(creds)
}

// signedInCredentials is what a completed sign-in stores. A scoped sign-in (hey mcp's)
// also records the scope HEY granted, the client its refreshes must name, and when and
// under which local key the grant was made (mcp.go).
func (m *Manager) signedInCredentials(token *OAuthToken, tokenEndpoint, installID string) (*Credentials, error) {
	creds := &Credentials{
		AccessToken:   token.AccessToken,
		RefreshToken:  token.RefreshToken,
		OAuthType:     "oauth",
		TokenEndpoint: tokenEndpoint,
		InstallID:     installID,
	}
	if !token.ExpiresAt.IsZero() {
		creds.ExpiresAt = token.ExpiresAt.Unix()
	}
	if m.scope == "" {
		return creds, nil
	}

	granted, err := grantedScope(m.scope, token.Scope)
	if err != nil {
		return nil, err
	}
	creds.Scope = granted
	creds.ClientID = m.clientID
	creds.GrantKey = generateState()
	creds.GrantedAt = time.Now().Unix()
	return creds, nil
}

// LoginWithToken stores a pre-provided bearer token.
func (m *Manager) LoginWithToken(token string) error {
	creds := &Credentials{
		AccessToken: token,
		OAuthType:   "token",
	}
	if m.profile != "" {
		// Recorded so checkProfileIssuer knows which server a profile's token is for.
		creds.TokenEndpoint = m.baseURL + "/oauth/tokens"
	}
	return m.saveCredentials(creds)
}

// OnCredentialCleared registers what to run when the manager clears a credential
// on its own — today, when the server has refused the refresh token. Replacing a
// credential with a signed-out record after deletion fails counts as clearing it.
// Logout does not: its callers clear the cache themselves.
func (m *Manager) OnCredentialCleared(fn func()) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.credentialCleared = fn
}

// Logout removes stored credentials.
func (m *Manager) Logout() error {
	m.mu.Lock()
	defer m.mu.Unlock()

	if err := m.store.Delete(m.storeKey); err != nil {
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
		return credentialLoadError(err)
	}
	return m.refreshLocked(ctx, creds)
}

func (m *Manager) loadCredentialsLocked() (*Credentials, error) {
	if m.cachedCredentials != nil {
		return cloneCredentials(m.cachedCredentials), nil
	}
	return m.reloadCredentialsLocked()
}

func (m *Manager) reloadCredentialsLocked() (*Credentials, error) {
	creds, err := m.store.Load(m.storeKey)
	if err == nil {
		err = m.checkPinnedGrant(creds)
	}
	if err == nil {
		err = m.checkProfileIssuer(creds)
	}
	if err != nil {
		m.cachedCredentials = nil
		return nil, err
	}
	m.cachedCredentials = cloneCredentials(creds)
	return cloneCredentials(creds), nil
}

// checkProfileIssuer refuses a profile's credential issued by another server than the
// one this manager talks to. A profile's credential is keyed by its name, not by a
// server, so an edited base_url would otherwise send it somewhere new; the key by
// origin that the top-level credential uses makes this check needless there.
func (m *Manager) checkProfileIssuer(creds *Credentials) error {
	if m.profile == "" || creds.TokenEndpoint == "" {
		return nil
	}
	issuer, issuerErr := config.Origin(creds.TokenEndpoint)
	server, serverErr := config.Origin(m.baseURL)
	if issuerErr == nil && serverErr == nil && issuer == server {
		return nil
	}
	err := apierr.ErrAuth(fmt.Sprintf("profile %q's sign-in was issued by %s, not %s", m.profile, creds.TokenEndpoint, m.baseURL))
	err.Hint = "Run: " + m.LoginCommand()
	return err
}

func (m *Manager) saveCredentials(creds *Credentials) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	if err := m.store.Save(m.storeKey, creds); err != nil {
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

	stored, loadErr := m.store.load(m.storeKey)
	if errors.Is(loadErr, ErrCredentialsNotFound) {
		// Another process deleted the credential while we waited for the lock.
		// Nothing stale may survive a forced read that proves it is gone.
		m.cachedCredentials = nil
		return errNotAuthenticated(loadErr)
	}
	if loadErr != nil {
		return credentialLoadError(loadErr)
	}
	if pinErr := m.checkPinnedGrant(stored); pinErr != nil {
		m.cachedCredentials = nil
		return pinErr
	}
	// Another process may have signed the profile in at another server since this one
	// loaded it.
	if issuerErr := m.checkProfileIssuer(stored); issuerErr != nil {
		m.cachedCredentials = nil
		return issuerErr
	}
	m.cachedCredentials = cloneCredentials(stored)
	changed := stored.AccessToken != creds.AccessToken
	if changed && stored.AccessToken != "" {
		return nil
	}
	creds = cloneCredentials(stored)
	if creds.AccessToken == "" && creds.RefreshToken == "" {
		return missingBearer(creds)
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
		if creds.AccessToken != "" && creds.ExpiresAt > time.Now().Unix() {
			return nil
		}
		return errRefreshHeld(until)
	}

	tokenEndpoint := creds.TokenEndpoint
	if tokenEndpoint == "" {
		tokenEndpoint = m.baseURL + "/oauth/tokens"
	}

	installID, err := m.credentialInstallID(creds)
	if err != nil {
		return err
	}

	// A refresh names the client the tokens were issued to. hey mcp's are the local
	// MCP client's, and HEY refuses a refresh token presented by another client.
	clientID := creds.ClientID
	if clientID == "" {
		clientID = m.clientID
	}

	token, err := refreshOAuthToken(ctx, m.httpClient, tokenEndpoint, creds.RefreshToken, clientID, installID)
	if err != nil {
		return m.accountForRefreshFailure(err, creds.RefreshToken)
	}
	m.refreshHoldUntil = time.Time{}
	// A 200 without an access token is not a refresh. Storing the empty string would
	// take the working token with it and leave nothing to authenticate with.
	if token.AccessToken == "" {
		return fmt.Errorf("token refresh returned no access token")
	}
	if creds.Scope != "" {
		// A refresh keeps the grant's scope. Take HEY's word when it narrows it, and
		// refuse an answer that names anything the grant never held.
		refreshed, scopeErr := grantedScope(creds.Scope, token.Scope)
		if scopeErr != nil {
			return scopeErr
		}
		creds.Scope = refreshed
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

	if err := m.store.save(m.storeKey, creds); err != nil {
		return err
	}
	m.cachedCredentials = cloneCredentials(creds)
	return nil
}

// credentialInstallID is the install a refresh presents: the one the tokens were issued
// to. Credentials saved before they carried one were issued to this config directory's
// install_id — the one every earlier login and refresh presented — so they take it when
// the directory still holds a well-formed one, and HEY's binding of their family
// matches. Without one they take an id derived from the refresh token, the same for every
// holder of the credential, never a freshly minted one. The refresh's own save keeps
// whichever they took, so every later refresh, from any config directory sharing the
// keychain entry, presents it too.
//
// A stored id that isn't a well-formed install id is refused here rather than sent: HEY
// would read it as another install and revoke the session, and substituting one would be
// another install too. Signing in again stores a fresh one.
func (m *Manager) credentialInstallID(creds *Credentials) (string, error) {
	if creds.InstallID == "" {
		directoryID, err := m.store.existingInstallID()
		switch {
		case err != nil:
			return "", fmt.Errorf("install id: %w", err)
		case directoryID != "":
			creds.InstallID = directoryID
		default:
			creds.InstallID = adoptedInstallID(creds.RefreshToken)
		}
	}
	if !isInstallID(creds.InstallID) {
		return "", fmt.Errorf("stored credentials carry a malformed install_id; run `hey login` to sign in again")
	}
	return creds.InstallID, nil
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
	if delErr := m.store.delete(m.storeKey); delErr != nil {
		// Keep the process-local guard even if the durable fallback also fails.
		m.refusedRefreshToken = sentRefreshToken

		// The keyring can occasionally refuse a delete while still allowing a
		// write. Replace the dead grant with a signed-out record so another
		// process cannot load and submit it again. A later login overwrites this
		// through the ordinary save path.
		tombstone := &Credentials{}
		if saveErr := m.store.save(m.storeKey, tombstone); saveErr != nil {
			return errRefusedGrant(errors.Join(delErr, fmt.Errorf("replace refused credential: %w", saveErr)))
		}
		m.cachedCredentials = cloneCredentials(tombstone)
		if m.credentialCleared != nil {
			m.credentialCleared()
		}
		return errRefusedGrant(nil)
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

func credentialLoadError(err error) error {
	if errors.Is(err, ErrCredentialsNotFound) {
		return errNotAuthenticated(err)
	}
	return CredentialStorageError("could not read stored credentials", err)
}

// CredentialStorageError reports a credential store that could not be read. It is not
// an authentication failure, so it keeps its own exit status; a keyring refusal gets
// the hint that stops a caller from asking for another login.
func CredentialStorageError(context string, err error) error {
	if errors.Is(err, ErrKeyringRefused) {
		return &apierr.Error{
			Code:    apierr.CodeUnknown,
			Message: fmt.Sprintf("%s: %v", context, err),
			Hint:    keyringRefusedHint(),
			Cause:   err,
		}
	}
	return fmt.Errorf("%s: %w", context, err)
}

// keyringRefusedHint names where the process runs rather than the login, because a
// new login lands in the same Keychain. The refusal comes from the desktop session, so
// the restart advice holds inside Codex's sandbox too; there it comes after the step
// that lets hey out of the sandbox.
func keyringRefusedHint() string {
	session := "the macOS Keychain won't hand your login to a process outside your Mac's desktop session, " +
		"such as one started over SSH or by an agent's background server launched from there."
	restart := "For Codex, quit it, run `codex app-server daemon restart` in Terminal on the Mac, then start Codex again."
	if inCodexSandbox() {
		return "Signing in again won't help. Let Codex run hey outside its sandbox. If it still fails there, " +
			session + " " + restart
	}
	return "Signing in again won't help: " + session + " Run hey from Terminal on the Mac. " + restart
}

// NotLoggedInHint is the remedy for a missing login. Codex's sandbox hides the system
// keyring, so a login that is saved reads as missing there.
func NotLoggedInHint() string {
	if inCodexSandbox() {
		return "Codex's sandbox hides the system keyring, so a saved login looks missing here. " +
			"If `hey auth status` in Terminal says you're logged in, let Codex run hey outside its sandbox; " +
			"otherwise run `hey auth login` in Terminal."
	}
	return "Run: hey auth login"
}

func inCodexSandbox() bool {
	return os.Getenv("CODEX_SANDBOX") != ""
}

func errNotAuthenticated(cause error) *apierr.Error {
	err := errNoCredential(fmt.Sprintf("not authenticated: %v", cause), cause)
	err.Hint = NotLoggedInHint()
	return err
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

// CredentialKey returns the credential's storage key: the base URL for the CLI's own
// credential, prefixed for hey mcp's (mcp.go), or the profile's (UseProfile).
func (m *Manager) CredentialKey() string {
	return m.storeKey
}

// ProfileCredentialKey is the key a named profile's credential is stored under,
// basecamp-cli's "profile:<name>" (internal/auth/auth.go at 48b01aaf).
func ProfileCredentialKey(profile string) string { return "profile:" + profile }

// MCPProfileCredentialKey is the key of hey mcp's credential under a profile, kept
// apart from the profile's own as "mcp::<origin>" is from the origin's.
func MCPProfileCredentialKey(profile string) string { return "mcp:" + ProfileCredentialKey(profile) }

// UseProfile keys this manager's credential by profile rather than by origin, so two
// profiles on one server hold two sign-ins and neither reads the other's.
func (m *Manager) UseProfile(profile string) {
	m.profile = profile
	if m.mcp {
		m.storeKey = MCPProfileCredentialKey(profile)
	} else {
		m.storeKey = ProfileCredentialKey(profile)
	}
}

// LoginCommand is the command that signs this manager's credential in again:
// addressed to its profile when there is one, since a bare login would store the new
// credential under the top-level key instead. It is meant to be pasted, so the name is
// shell-quoted.
func (m *Manager) LoginCommand() string {
	command := "hey auth login"
	if m.mcp {
		command += " --mcp"
	}
	if m.profile != "" {
		command += " -P " + shellQuote(m.profile)
	}
	return command
}

// shellQuote quotes a word for a POSIX shell when it needs it.
func shellQuote(word string) string {
	if word != "" && strings.Trim(word, "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789_-.") == "" {
		return word
	}
	return "'" + strings.ReplaceAll(word, "'", `'\''`) + "'"
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

// missingBearer recognizes old cookie-only records without treating their secrets
// as tokens. Keeping the record readable also lets ordinary login/logout replace it.
func missingBearer(creds *Credentials) error {
	if creds.SessionCookie != "" {
		return apierr.ErrAuth("browser session cookie authentication is no longer supported; sign in again with hey auth login")
	}
	return errNoCredential("no access token available", nil)
}
