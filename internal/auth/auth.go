package auth

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/url"
	"time"

	"hey-cli/internal/client"
	"hey-cli/internal/config"
)

// SignCredentials signs email+password using HMAC-SHA256 with the client secret,
// matching Rails MessageVerifier behavior for the password grant.
func SignCredentials(email, password, secret string) string {
	creds := map[string]string{
		"email_address": email,
		"password":      password,
	}
	data, _ := json.Marshal(creds)
	encoded := base64.StdEncoding.EncodeToString(data)

	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write([]byte(encoded))
	sig := base64.StdEncoding.EncodeToString(mac.Sum(nil))

	return encoded + "--" + sig
}

type TokenResponse struct {
	AccessToken  string `json:"access_token"`
	RefreshToken string `json:"refresh_token"`
	ExpiresIn    int64  `json:"expires_in"`
	TokenType    string `json:"token_type"`
	Error        string `json:"error"`
	ErrorDesc    string `json:"error_description"`
}

// PasswordGrant performs an OAuth password grant to obtain tokens.
func PasswordGrant(cfg *config.Config, email, password string) (*TokenResponse, error) {
	sig := SignCredentials(email, password, cfg.ClientSecret)

	c := client.New(cfg)
	values := url.Values{
		"grant_type": {"password"},
		"client_id":  {cfg.ClientID},
		"sig":        {sig},
		"install_id": {cfg.InstallID},
	}

	data, err := c.PostForm("/oauth/tokens", values)
	if err != nil {
		return nil, err
	}

	var resp TokenResponse
	if err := json.Unmarshal(data, &resp); err != nil {
		return nil, fmt.Errorf("could not parse token response: %w", err)
	}

	if resp.Error != "" {
		return &resp, fmt.Errorf("%s: %s", resp.Error, resp.ErrorDesc)
	}

	cfg.AccessToken = resp.AccessToken
	cfg.RefreshToken = resp.RefreshToken
	cfg.TokenExpiry = time.Now().Unix() + resp.ExpiresIn

	if err := cfg.Save(); err != nil {
		return &resp, fmt.Errorf("tokens obtained but could not save config: %w", err)
	}

	return &resp, nil
}
