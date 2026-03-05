package client

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/basecamp/hey-cli/internal/auth"
	"github.com/basecamp/hey-cli/internal/version"
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
		return &APIError{StatusCode: 401, Message: "unauthorized — run `hey auth login` to authenticate"}
	case 404:
		return &APIError{StatusCode: 404, Message: "not found (404)"}
	default:
		return &APIError{StatusCode: resp.StatusCode, Message: fmt.Sprintf("API error %d: %s", resp.StatusCode, string(data))}
	}
}

type Client struct {
	BaseURL    string
	AuthMgr    *auth.Manager
	HTTPClient *http.Client
}

func New(baseURL string, authMgr *auth.Manager) *Client {
	return &Client{
		BaseURL: baseURL,
		AuthMgr: authMgr,
		HTTPClient: &http.Client{
			Timeout: 30 * time.Second,
		},
	}
}

func (c *Client) doRequest(method, path string, body io.Reader, contentType string) (*http.Response, error) {
	return c.doRequestAccept(method, path, body, contentType, "application/json")
}

func (c *Client) doRequestAccept(method, path string, body io.Reader, contentType, accept string) (*http.Response, error) {
	base := strings.TrimRight(c.BaseURL, "/")
	reqURL := base + path

	ctx := context.Background()
	req, err := http.NewRequestWithContext(ctx, method, reqURL, body)
	if err != nil {
		return nil, fmt.Errorf("could not create request: %w", err)
	}

	if err = c.AuthMgr.AuthenticateRequest(ctx, req); err != nil {
		return nil, fmt.Errorf("authentication failed: %w", err)
	}

	req.Header.Set("Accept", accept)
	req.Header.Set("User-Agent", version.UserAgent())
	if contentType != "" {
		req.Header.Set("Content-Type", contentType)
	}

	resp, err := c.HTTPClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("request failed: %w", err)
	}

	return resp, nil
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

func (c *Client) GetHTML(path string) ([]byte, error) {
	resp, err := c.doRequestAccept("GET", path, nil, "", "text/html")
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	return c.readBody(resp)
}

func (c *Client) GetJSON(path string, v any) error {
	data, err := c.Get(path)
	if err != nil {
		return err
	}
	return json.Unmarshal(data, v)
}

func (c *Client) PostJSON(path string, body any) ([]byte, error) {
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

func (c *Client) PatchJSON(path string, body any) ([]byte, error) {
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

func (c *Client) PutJSON(path string, body any) ([]byte, error) {
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
