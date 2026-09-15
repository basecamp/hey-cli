package auth

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/basecamp/hey-cli/internal/version"
)

// OAuthToken represents the token response from the HEY OAuth server.
type OAuthToken struct {
	AccessToken  string    `json:"access_token"`  //nolint:gosec // G117: legitimate OAuth field
	RefreshToken string    `json:"refresh_token"` //nolint:gosec // G117: legitimate OAuth field
	TokenType    string    `json:"token_type"`
	ExpiresIn    int64     `json:"expires_in"`
	ExpiresAt    time.Time `json:"-"`
}

// DeviceAuthorization represents an RFC 8628 device authorization response.
type DeviceAuthorization struct {
	DeviceCode              string `json:"device_code"`
	UserCode                string `json:"user_code"`
	VerificationURI         string `json:"verification_uri"`
	VerificationURIComplete string `json:"verification_uri_complete"`
	ExpiresIn               int64  `json:"expires_in"`
	Interval                int64  `json:"interval"`
}

type deviceTokenError struct {
	Code string `json:"error"`
}

func requestDeviceAuthorization(ctx context.Context, httpClient *http.Client, endpoint, clientID, installID string) (*DeviceAuthorization, error) {
	data := url.Values{"client_id": {clientID}, "install_id": {installID}}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, strings.NewReader(data.Encode()))
	if err != nil {
		return nil, fmt.Errorf("creating device authorization request: %w", err)
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("User-Agent", version.UserAgent())

	resp, err := httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("device authorization request failed: %w", err)
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, 64<<10))
	if err != nil {
		return nil, fmt.Errorf("reading device authorization response: %w", err)
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("device authorization failed (status %d): %s", resp.StatusCode, string(body))
	}

	var authorization DeviceAuthorization
	if err := json.Unmarshal(body, &authorization); err != nil {
		return nil, fmt.Errorf("parsing device authorization response: %w", err)
	}
	if authorization.DeviceCode == "" || authorization.UserCode == "" || authorization.VerificationURI == "" || authorization.ExpiresIn <= 0 {
		return nil, errors.New("device authorization response is missing required fields")
	}
	return &authorization, nil
}

func exchangeDeviceCode(ctx context.Context, httpClient *http.Client, tokenEndpoint, deviceCode, clientID, installID string) (*OAuthToken, string, error) {
	data := url.Values{
		"grant_type":  {"urn:ietf:params:oauth:grant-type:device_code"},
		"device_code": {deviceCode},
		"client_id":   {clientID},
		"install_id":  {installID},
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, tokenEndpoint, strings.NewReader(data.Encode()))
	if err != nil {
		return nil, "", fmt.Errorf("creating device token request: %w", err)
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("User-Agent", version.UserAgent())

	resp, err := httpClient.Do(req)
	if err != nil {
		return nil, "", fmt.Errorf("device token request failed: %w", err)
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, 64<<10))
	if err != nil {
		return nil, "", fmt.Errorf("reading device token response: %w", err)
	}
	if resp.StatusCode == http.StatusOK {
		var token OAuthToken
		if err := json.Unmarshal(body, &token); err != nil {
			return nil, "", fmt.Errorf("parsing device token response: %w", err)
		}
		if token.AccessToken == "" {
			return nil, "", errors.New("device token response is missing access_token")
		}
		if token.ExpiresIn > 0 {
			token.ExpiresAt = time.Now().Add(time.Duration(token.ExpiresIn) * time.Second)
		}
		return &token, "", nil
	}

	var oauthErr deviceTokenError
	if err := json.Unmarshal(body, &oauthErr); err == nil && oauthErr.Code != "" && (resp.StatusCode == http.StatusBadRequest || resp.StatusCode == http.StatusForbidden) {
		return nil, oauthErr.Code, nil
	}
	return nil, "", fmt.Errorf("device token exchange failed (status %d): %s", resp.StatusCode, string(body))
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
		return nil, fmt.Errorf("reading token response: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("token exchange failed (status %d): %s", resp.StatusCode, string(body))
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
		return nil, fmt.Errorf("reading refresh response: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("token refresh failed (status %d): %s", resp.StatusCode, string(body))
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
