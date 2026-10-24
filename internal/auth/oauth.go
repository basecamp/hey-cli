package auth

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/basecamp/hey-cli/internal/version"
)

// tokenEndpointError is a non-200 answer from the OAuth token endpoint, typed so a
// caller can tell a dead grant from a server that would not answer. Code is the RFC
// 6749 §5.2 error code, and stays empty when the body carried none: an unrecognized
// failure is transient, never proof that the credential is dead.
type tokenEndpointError struct {
	Op         string // "token exchange" or "token refresh", for the message
	StatusCode int
	Code       string
	Body       string // verbatim, as the message has always shown it
	RetryAfter time.Duration
}

func (e *tokenEndpointError) Error() string {
	return fmt.Sprintf("%s failed (status %d): %s", e.Op, e.StatusCode, e.Body)
}

// grantRefused reports whether the server refused the grant itself. invalid_grant is
// the one answer RFC 6749 gives for a refresh token that is expired, revoked or spent.
// The status is checked too: a 5xx that echoes the code is an origin failing.
func (e *tokenEndpointError) grantRefused() bool {
	return e.Code == "invalid_grant" && e.StatusCode >= 400 && e.StatusCode < 500
}

// rateLimited reports whether the server declined to look at the grant at all.
func (e *tokenEndpointError) rateLimited() bool {
	return e.StatusCode == http.StatusTooManyRequests
}

func newTokenEndpointError(op string, resp *http.Response, body []byte) *tokenEndpointError {
	err := &tokenEndpointError{
		Op:         op,
		StatusCode: resp.StatusCode,
		Body:       string(body),
		RetryAfter: parseRetryAfter(resp.Header.Get("Retry-After")),
	}

	var payload struct {
		Error string `json:"error"`
	}
	if jsonErr := json.Unmarshal(body, &payload); jsonErr == nil {
		err.Code = payload.Error
	}
	return err
}

// parseRetryAfter reads the delay-seconds and HTTP-date forms of RFC 9110 §10.2.3,
// and returns 0 for anything else. A negative or absurd value is dropped rather than
// honored: the header is the server's suggestion, not a lever to pin a client with.
func parseRetryAfter(value string) time.Duration {
	value = strings.TrimSpace(value)
	if value == "" {
		return 0
	}

	if seconds, err := strconv.Atoi(value); err == nil {
		return clampRetryAfter(time.Duration(seconds) * time.Second)
	}
	if when, err := http.ParseTime(value); err == nil {
		return clampRetryAfter(time.Until(when))
	}
	return 0
}

// maxRetryAfter caps how long a server may park the client. An hour is the whole
// window the token endpoint's limit is measured over, so nothing beyond it can be
// about this limit.
const maxRetryAfter = time.Hour

func clampRetryAfter(d time.Duration) time.Duration {
	switch {
	case d <= 0:
		return 0
	case d > maxRetryAfter:
		return maxRetryAfter
	default:
		return d
	}
}

// OAuthToken represents the token response from the HEY OAuth server.
type OAuthToken struct {
	AccessToken  string    `json:"access_token"`  //nolint:gosec // G117: legitimate OAuth field
	RefreshToken string    `json:"refresh_token"` //nolint:gosec // G117: legitimate OAuth field
	TokenType    string    `json:"token_type"`
	ExpiresIn    int64     `json:"expires_in"`
	ExpiresAt    time.Time `json:"-"`
}

// exchangeCode exchanges an authorization code for tokens using PKCE.
func exchangeCode(ctx context.Context, httpClient *http.Client, tokenEndpoint, code, redirectURI, clientID, codeVerifier, installID string) (*OAuthToken, error) {
	data := url.Values{
		"grant_type":    {"authorization_code"},
		"client_id":     {clientID},
		"code":          {code},
		"redirect_uri":  {redirectURI},
		"code_verifier": {codeVerifier},
		"install_id":    {installID},
	}

	req, err := http.NewRequestWithContext(ctx, "POST", tokenEndpoint, strings.NewReader(data.Encode()))
	if err != nil {
		return nil, fmt.Errorf("creating token request: %w", err)
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("User-Agent", version.UserAgent())

	resp, err := httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("token exchange request failed: %w", err)
	}
	defer resp.Body.Close()

	return readTokenResponse("token exchange", resp)
}

// refreshOAuthToken refreshes an access token using a refresh token.
func refreshOAuthToken(ctx context.Context, httpClient *http.Client, tokenEndpoint, refreshTok, clientID, installID string) (*OAuthToken, error) {
	data := url.Values{
		"grant_type":    {"refresh_token"},
		"client_id":     {clientID},
		"refresh_token": {refreshTok},
		"install_id":    {installID},
	}

	req, err := http.NewRequestWithContext(ctx, "POST", tokenEndpoint, strings.NewReader(data.Encode()))
	if err != nil {
		return nil, fmt.Errorf("creating refresh request: %w", err)
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("User-Agent", version.UserAgent())

	resp, err := httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("token refresh request failed: %w", err)
	}
	defer resp.Body.Close()

	return readTokenResponse("token refresh", resp)
}

// readTokenResponse reads the token endpoint's answer, typing a refusal so the caller
// can tell a dead grant from a server that would not answer.
func readTokenResponse(op string, resp *http.Response) (*OAuthToken, error) {
	body, err := io.ReadAll(io.LimitReader(resp.Body, 64<<10))
	if resp.StatusCode != http.StatusOK {
		// A body that truncates on the way in does not lose the verdict with it:
		// the status and its Retry-After are already in hand.
		return nil, newTokenEndpointError(op, resp, body)
	}
	if err != nil {
		return nil, fmt.Errorf("reading %s response: %w", op, err)
	}

	var token OAuthToken
	if err := json.Unmarshal(body, &token); err != nil {
		return nil, fmt.Errorf("parsing %s response: %w", op, err)
	}

	if token.ExpiresIn > 0 {
		token.ExpiresAt = time.Now().Add(time.Duration(token.ExpiresIn) * time.Second)
	}

	return &token, nil
}

// PKCE helpers

func generateCodeVerifier() string {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		panic("crypto/rand failed: " + err.Error())
	}
	return base64.RawURLEncoding.EncodeToString(b)
}

func generateCodeChallenge(verifier string) string {
	h := sha256.Sum256([]byte(verifier))
	return base64.RawURLEncoding.EncodeToString(h[:])
}

// generateState generates a random state parameter for CSRF protection.
func generateState() string {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		panic("crypto/rand failed: " + err.Error())
	}
	return base64.RawURLEncoding.EncodeToString(b)
}
