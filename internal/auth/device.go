package auth

import (
	"context"
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

// The device authorization grant (RFC 8628), as haystack#8707 and #9085 serve it.
//
// A device request names the client and the scope, and no resource: hey mcp's tokens
// are for HEY's own API. Until a HEY host runs #9085 with OAUTH_SCOPED_DEVICE_FLOW on,
// a scoped request is refused with invalid_request (or, on #9041 alone, the row with
// unauthorized_client), and the sign-in falls back to loopback authorization.

// errDeviceFlowUnavailable means this HEY can't serve a scoped device sign-in, so the
// caller should sign in by loopback instead.
var errDeviceFlowUnavailable = errors.New("this HEY doesn't take scoped device sign-ins yet")

// deviceGrantType is RFC 8628 §3.4's grant type.
const deviceGrantType = "urn:ietf:params:oauth:grant-type:device_code"

// defaultDeviceInterval is RFC 8628 §3.2's default polling interval, and the step
// slow_down adds to it (§3.5).
const defaultDeviceInterval = 5 * time.Second

// deviceAuthorization is a device authorization response (RFC 8628 §3.2). DeviceCode
// is a secret: it is sent back to the token endpoint and never shown.
type deviceAuthorization struct {
	DeviceCode              string `json:"device_code"` //nolint:gosec // G117: legitimate OAuth field
	UserCode                string `json:"user_code"`
	VerificationURI         string `json:"verification_uri"`
	VerificationURIComplete string `json:"verification_uri_complete"`
	ExpiresIn               int64  `json:"expires_in"`
	Interval                int64  `json:"interval"`
}

// LoginDevice signs in with the device authorization grant, asking for the manager's
// scope. It returns errDeviceFlowUnavailable, wrapped with HEY's answer, when this HEY
// can't serve the request, so the caller can fall back to Login.
func (m *Manager) LoginDevice(ctx context.Context, opts LoginOptions) error {
	tokenEndpoint := m.baseURL + "/oauth/tokens"
	installID, err := m.store.InstallID()
	if err != nil {
		return fmt.Errorf("install id: %w", err)
	}

	authorization, err := m.requestDeviceAuthorization(ctx)
	if err != nil {
		return err
	}

	link := authorization.VerificationURIComplete
	if link == "" {
		link = authorization.VerificationURI
	}
	opts.log(fmt.Sprintf("\nTo sign in, open %s\nand check that HEY shows this code: %s\n", link, authorization.UserCode))
	if !opts.NoBrowser {
		if browserErr := openBrowser(link); browserErr != nil {
			opts.log("Couldn't open the browser automatically; open the link above.\n")
		}
	}
	opts.log("\nWaiting for you to approve the sign-in in HEY...\n")

	interval := time.Duration(authorization.Interval) * time.Second
	if interval <= 0 {
		interval = defaultDeviceInterval
	}
	expiresAt := time.Now().Add(time.Duration(authorization.ExpiresIn) * time.Second)

	for {
		if waitErr := m.wait(ctx, min(interval, max(time.Until(expiresAt), 0))); waitErr != nil {
			return waitErr
		}
		if !time.Now().Before(expiresAt) {
			return errors.New("the sign-in code expired before it was approved; run the sign-in again")
		}

		token, code, pollErr := m.pollDeviceToken(ctx, tokenEndpoint, authorization.DeviceCode, installID)
		if pollErr != nil {
			return pollErr
		}
		switch code {
		case "":
			creds, credErr := m.signedInCredentials(token, tokenEndpoint, installID)
			if credErr != nil {
				return credErr
			}
			return m.saveCredentials(creds)
		case "authorization_pending":
		case "slow_down":
			interval += defaultDeviceInterval
		case "access_denied":
			return errors.New("the sign-in was denied in HEY")
		case "expired_token":
			return errors.New("the sign-in code expired before it was approved; run the sign-in again")
		case "invalid_scope":
			return errors.New("HEY no longer allows this client the scope it asked for")
		default:
			return fmt.Errorf("device sign-in failed: %s", code)
		}
	}
}

// requestDeviceAuthorization asks HEY for a device code (POST /oauth/device_authorizations).
func (m *Manager) requestDeviceAuthorization(ctx context.Context) (*deviceAuthorization, error) {
	form := url.Values{"client_id": {m.clientID}}
	if m.scope != "" {
		form.Set("scope", m.scope)
	}
	status, body, err := m.postForm(ctx, m.baseURL+"/oauth/device_authorizations", form)
	if err != nil {
		return nil, fmt.Errorf("device authorization request failed: %w", err)
	}

	if status != http.StatusOK {
		code := oauthErrorCode(body)
		if deviceFlowUnavailable(status, code) {
			return nil, fmt.Errorf("%w (HTTP %d %s)", errDeviceFlowUnavailable, status, code)
		}
		return nil, fmt.Errorf("device authorization failed: HTTP %d %s", status, code)
	}

	var authorization deviceAuthorization
	if err := json.Unmarshal(body, &authorization); err != nil {
		return nil, fmt.Errorf("parsing device authorization response: %w", err)
	}
	if authorization.DeviceCode == "" || authorization.UserCode == "" || authorization.VerificationURI == "" || authorization.ExpiresIn <= 0 {
		return nil, errors.New("device authorization response is missing required fields")
	}
	return &authorization, nil
}

// deviceFlowUnavailable reports whether a refused device request means this HEY can't
// serve it at all: no endpoint (an older HEY), the rollout switch off (invalid_request
// for a request naming scope, haystack#9085), or a row the device flow refuses
// (unauthorized_client, #9041 without #9085).
func deviceFlowUnavailable(status int, code string) bool {
	switch status {
	case http.StatusNotFound, http.StatusMethodNotAllowed:
		return true
	case http.StatusBadRequest, http.StatusUnauthorized:
		return code == "invalid_request" || code == "unauthorized_client" || code == "unsupported_grant_type"
	default:
		return false
	}
}

// pollDeviceToken asks the token endpoint once whether the device code is approved. It
// returns the token, or the RFC 8628 §3.5 error code HEY answered.
func (m *Manager) pollDeviceToken(ctx context.Context, tokenEndpoint, deviceCode, installID string) (*OAuthToken, string, error) {
	form := url.Values{
		"grant_type":  {deviceGrantType},
		"client_id":   {m.clientID},
		"device_code": {deviceCode},
		"install_id":  {installID},
	}
	status, body, err := m.postForm(ctx, tokenEndpoint, form)
	if err != nil {
		return nil, "", fmt.Errorf("device token request failed: %w", err)
	}

	if status == http.StatusOK {
		var token OAuthToken
		if err := json.Unmarshal(body, &token); err != nil {
			return nil, "", fmt.Errorf("parsing device token response: %w", err)
		}
		if token.AccessToken == "" {
			return nil, "", errors.New("device token response has no access token")
		}
		if token.ExpiresIn > 0 {
			token.ExpiresAt = time.Now().Add(time.Duration(token.ExpiresIn) * time.Second)
		}
		return &token, "", nil
	}

	if code := oauthErrorCode(body); code != "" && status >= 400 && status < 500 {
		return nil, code, nil
	}
	return nil, "", fmt.Errorf("device token request failed: HTTP %d", status)
}

// postForm posts a form and answers the status and at most 64 KiB of the body.
func (m *Manager) postForm(ctx context.Context, endpoint string, form url.Values) (int, []byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, strings.NewReader(form.Encode()))
	if err != nil {
		return 0, nil, err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("User-Agent", version.UserAgent())

	resp, err := m.httpClient.Do(req)
	if err != nil {
		return 0, nil, err
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, 64<<10))
	if err != nil {
		return 0, nil, err
	}
	return resp.StatusCode, body, nil
}

// oauthErrorCode is the RFC 6749 §5.2 error code in an error response, or "".
func oauthErrorCode(body []byte) string {
	var payload struct {
		Error string `json:"error"`
	}
	if json.Unmarshal(body, &payload) != nil {
		return ""
	}
	return payload.Error
}
