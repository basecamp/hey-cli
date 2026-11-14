package auth

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
)

// OAuthMetadata represents RFC 8414 OAuth Authorization Server Metadata.
type OAuthMetadata struct {
	Issuer                        string   `json:"issuer"`
	AuthorizationEndpoint         string   `json:"authorization_endpoint,omitempty"`
	TokenEndpoint                 string   `json:"token_endpoint"`
	DeviceAuthorizationEndpoint   string   `json:"device_authorization_endpoint,omitempty"`
	GrantTypesSupported           []string `json:"grant_types_supported,omitempty"`
	CodeChallengeMethodsSupported []string `json:"code_challenge_methods_supported,omitempty"`
}

// DiscoverOAuthMetadata fetches RFC 8414 OAuth Authorization Server Metadata
// from the well-known path for the given base URL. Returns nil metadata with
// no error when the metadata document is not found (404), allowing fallback
// to legacy auth. Other errors are returned as-is.
func (m *Manager) DiscoverOAuthMetadata(ctx context.Context) (*OAuthMetadata, error) {
	// Construct the well-known URL from the base URL
	metadataURL := m.baseURL + "/.well-known/oauth-authorization-server"

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, metadataURL, nil)
	if err != nil {
		return nil, fmt.Errorf("building metadata request: %w", err)
	}

	resp, err := m.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("fetching OAuth metadata: %w", err)
	}
	defer resp.Body.Close()

	// 404 means no metadata document - not an error, just means fallback to legacy
	if resp.StatusCode == http.StatusNotFound {
		return nil, nil
	}

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("OAuth metadata request failed: HTTP %d", resp.StatusCode)
	}

	body, err := io.ReadAll(io.LimitReader(resp.Body, 64<<10))
	if err != nil {
		return nil, fmt.Errorf("reading OAuth metadata: %w", err)
	}

	var metadata OAuthMetadata
	if err := json.Unmarshal(body, &metadata); err != nil {
		return nil, fmt.Errorf("parsing OAuth metadata: %w", err)
	}

	return &metadata, nil
}

// SupportsDeviceFlow reports whether the metadata advertises device authorization support.
func (md *oauthMetadata) SupportsDeviceFlow() bool {
	if md == nil {
		return false
	}
	if md.DeviceAuthorizationEndpoint == "" {
		return false
	}
	for _, grant := range md.GrantTypesSupported {
		if grant == "urn:ietf:params:oauth:grant-type:device_code" {
			return true
		}
	}
	return false
}
