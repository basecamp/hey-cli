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

// tokenEndpointError is a non-200 answer from the OAuth token endpoint, kept typed
// so a caller can tell the two kinds apart: the server refusing the grant we sent,
// which no retry will ever fix, and the server refusing to answer at all, which a
// later attempt may well get through.
//
// RFC 6749 §5.2 gives the refusal a machine-readable code in a JSON body. Some
// refusals carry no body worth parsing (a proxy's 502, a plain 401), and those
// leave Code empty — which is the point: an unrecognized failure is treated as
// transient, never as proof that the credential is dead.
type tokenEndpointError struct {
	Op          string // "token exchange" or "token refresh", for the message
	StatusCode  int
	Code        string // RFC 6749 §5.2 error code, empty when the body carried none
	Description string // error_description, when the server sent one
	Body        string // the response body, verbatim, as the message has always shown it
	RetryAfter  time.Duration
}

func (e *tokenEndpointError) Error() string {
	return fmt.Sprintf("%s failed (status %d): %s", e.Op, e.StatusCode, e.Body)
}

// grantRefused reports whether the server refused the grant itself. invalid_grant is
// the single answer RFC 6749 gives for a refresh token that is expired, revoked or
// already spent, and it is the only one that proves re-sending it can never work —
// so it is the only one we act on by forgetting the credential. Everything else,
// including a bare 4xx with no code, stays transient: the cost of being wrong the
// other way is signing someone out over a blip.
//
// The status is checked alongside the code because a 5xx that happens to echo an
// error code is an origin failing, not a grant decision.
func (e *tokenEndpointError) grantRefused() bool {
	return e.Code == "invalid_grant" && e.StatusCode >= 400 && e.StatusCode < 500
}

// rateLimited reports whether the server declined to evaluate the grant at all. It
// says nothing about whether the credential is good, so the credential is kept.
func (e *tokenEndpointError) rateLimited() bool {
	return e.StatusCode == http.StatusTooManyRequests
}

// newTokenEndpointError reads what the refusal is willing to say. A body that is not
// the JSON of RFC 6749 §5.2 is not an error here — it just leaves Code empty, and an
// empty code is transient.
func newTokenEndpointError(op string, resp *http.Response, body []byte) *tokenEndpointError {
	err := &tokenEndpointError{
		Op:         op,
		StatusCode: resp.StatusCode,
		Body:       string(body),
		RetryAfter: parseRetryAfter(resp.Header.Get("Retry-After")),
	}

	var payload struct {
		Error       string `json:"error"`
		Description string `json:"error_description"`
	}
	if jsonErr := json.Unmarshal(body, &payload); jsonErr == nil {
		err.Code = payload.Error
		err.Description = payload.Description
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

	body, err := io.ReadAll(io.LimitReader(resp.Body, 64<<10))
	if err != nil {
		if resp.StatusCode != http.StatusOK {
			// The status and its headers are already in hand. A body that
			// truncates on the way in is no reason to lose the verdict with it —
			// least of all a 429, whose whole value here is the Retry-After.
			return nil, newTokenEndpointError("token exchange", resp, nil)
		}
		return nil, fmt.Errorf("reading token response: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		return nil, newTokenEndpointError("token exchange", resp, body)
	}

	var token OAuthToken
	if err := json.Unmarshal(body, &token); err != nil {
		return nil, fmt.Errorf("parsing token response: %w", err)
	}

	if token.ExpiresIn > 0 {
		token.ExpiresAt = time.Now().Add(time.Duration(token.ExpiresIn) * time.Second)
	}

	return &token, nil
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

	body, err := io.ReadAll(io.LimitReader(resp.Body, 64<<10))
	if err != nil {
		if resp.StatusCode != http.StatusOK {
			return nil, newTokenEndpointError("token refresh", resp, nil)
		}
		return nil, fmt.Errorf("reading refresh response: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		return nil, newTokenEndpointError("token refresh", resp, body)
	}

	var token OAuthToken
	if err := json.Unmarshal(body, &token); err != nil {
		return nil, fmt.Errorf("parsing refresh response: %w", err)
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
