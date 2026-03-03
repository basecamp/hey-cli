package client

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"hey-cli/internal/config"
)

type APIError struct {
	StatusCode int
	Message    string
}

func (e *APIError) Error() string {
	return e.Message
}

func responseError(resp *http.Response, data []byte) *APIError {
	switch resp.StatusCode {
	case 401:
		return &APIError{StatusCode: 401, Message: "unauthorized — run `hey login` to authenticate"}
	case 404:
		return &APIError{StatusCode: 404, Message: "not found (404)"}
	default:
		return &APIError{StatusCode: resp.StatusCode, Message: fmt.Sprintf("API error %d: %s", resp.StatusCode, string(data))}
	}
}

type Client struct {
	Config     *config.Config
	HTTPClient *http.Client
}

func New(cfg *config.Config) *Client {
	return &Client{
		Config: cfg,
		HTTPClient: &http.Client{
			Timeout: 30 * time.Second,
		},
	}
}

func (c *Client) doRequest(method, path string, body io.Reader, contentType string) (*http.Response, error) {
	base := strings.TrimRight(c.Config.BaseURL, "/")
	reqURL := base + path

	req, err := http.NewRequest(method, reqURL, body)
	if err != nil {
		return nil, fmt.Errorf("could not create request: %w", err)
	}

	if c.Config.AccessToken != "" {
		req.Header.Set("Authorization", "Bearer "+c.Config.AccessToken)
	} else if c.Config.SessionCookie != "" {
		req.Header.Set("Cookie", "session_token="+c.Config.SessionCookie)
	}
	req.Header.Set("Accept", "application/json")
	if contentType != "" {
		req.Header.Set("Content-Type", contentType)
	}

	resp, err := c.HTTPClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("request failed: %w", err)
	}

	if resp.StatusCode == 401 && c.Config.RefreshToken != "" && c.Config.AccessToken != "" {
		resp.Body.Close()
		if err := c.refreshToken(); err == nil {
			req, _ = http.NewRequest(method, reqURL, body)
			req.Header.Set("Authorization", "Bearer "+c.Config.AccessToken)
			req.Header.Set("Accept", "application/json")
			if contentType != "" {
				req.Header.Set("Content-Type", contentType)
			}
			return c.HTTPClient.Do(req)
		}
	}

	return resp, nil
}

func (c *Client) refreshToken() error {
	base := strings.TrimRight(c.Config.BaseURL, "/")
	data := url.Values{
		"grant_type":    {"refresh_token"},
		"client_id":     {c.Config.ClientID},
		"refresh_token": {c.Config.RefreshToken},
		"install_id":    {c.Config.InstallID},
	}

	resp, err := c.HTTPClient.PostForm(base+"/oauth/tokens", data)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode != 200 {
		return fmt.Errorf("refresh failed with status %d", resp.StatusCode)
	}

	var result struct {
		AccessToken  string `json:"access_token"`
		RefreshToken string `json:"refresh_token"`
		ExpiresIn    int64  `json:"expires_in"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return err
	}

	c.Config.AccessToken = result.AccessToken
	if result.RefreshToken != "" {
		c.Config.RefreshToken = result.RefreshToken
	}
	c.Config.TokenExpiry = time.Now().Unix() + result.ExpiresIn
	return c.Config.Save()
}

func (c *Client) readBody(resp *http.Response) ([]byte, error) {
	data, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("could not read response: %w", err)
	}
	if resp.StatusCode >= 400 {
		return nil, responseError(resp, data)
	}
	return data, nil
}

func (c *Client) Get(path string) ([]byte, error) {
	resp, err := c.doRequest("GET", path, nil, "")
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	return c.readBody(resp)
}

func (c *Client) GetJSON(path string, v interface{}) error {
	data, err := c.Get(path)
	if err != nil {
		return err
	}
	return json.Unmarshal(data, v)
}

func (c *Client) PostJSON(path string, body interface{}) ([]byte, error) {
	var buf bytes.Buffer
	if body != nil {
		if err := json.NewEncoder(&buf).Encode(body); err != nil {
			return nil, fmt.Errorf("could not encode body: %w", err)
		}
	}

	resp, err := c.doRequest("POST", path, &buf, "application/json")
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	return c.readBody(resp)
}

func (c *Client) PostForm(path string, values url.Values) ([]byte, error) {
	resp, err := c.doRequest("POST", path, strings.NewReader(values.Encode()), "application/x-www-form-urlencoded")
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	return c.readBody(resp)
}

func (c *Client) PatchJSON(path string, body interface{}) ([]byte, error) {
	var buf bytes.Buffer
	if body != nil {
		if err := json.NewEncoder(&buf).Encode(body); err != nil {
			return nil, fmt.Errorf("could not encode body: %w", err)
		}
	}

	resp, err := c.doRequest("PATCH", path, &buf, "application/json")
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	return c.readBody(resp)
}

func (c *Client) PutJSON(path string, body interface{}) ([]byte, error) {
	var buf bytes.Buffer
	if body != nil {
		if err := json.NewEncoder(&buf).Encode(body); err != nil {
			return nil, fmt.Errorf("could not encode body: %w", err)
		}
	}

	resp, err := c.doRequest("PUT", path, &buf, "application/json")
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	return c.readBody(resp)
}

func (c *Client) Delete(path string) ([]byte, error) {
	resp, err := c.doRequest("DELETE", path, nil, "")
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	return c.readBody(resp)
}
