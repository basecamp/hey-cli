package client

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"math/rand/v2"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/basecamp/hey-cli/internal/auth"
	"github.com/basecamp/hey-cli/internal/apierr"
	"github.com/basecamp/hey-cli/internal/version"
)

const maxRetries = 3

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

func (c *Client) doRequestAccept(method, path string, body io.Reader, contentType, accept string) (*http.Response, error) {
	base := strings.TrimRight(c.BaseURL, "/")
	reqURL := base + path

	ctx := context.Background()
	req, err := http.NewRequestWithContext(ctx, method, reqURL, body)
	if err != nil {
		return nil, apierr.ErrAPI(0, fmt.Sprintf("could not create request: %v", err))
	}

	if err = c.AuthMgr.AuthenticateRequest(ctx, req); err != nil {
		return nil, apierr.ErrAuth(fmt.Sprintf("authentication failed: %v", err))
	}

	req.Header.Set("Accept", accept)
	req.Header.Set("User-Agent", version.UserAgent())
	if contentType != "" {
		req.Header.Set("Content-Type", contentType)
	}

	resp, err := c.HTTPClient.Do(req)
	if err != nil {
		return nil, apierr.ErrNetwork(err)
	}

	return resp, nil
}

func (c *Client) readBody(resp *http.Response) ([]byte, error) {
	data, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, apierr.ErrNetwork(err)
	}
	if resp.StatusCode >= 400 {
		return nil, responseError(resp, data)
	}
	return data, nil
}

// doOnce performs a single HTTP request without retry. Used for non-idempotent
// mutations (POST, PATCH, PUT, DELETE) where retrying could duplicate side effects.
func (c *Client) doOnce(method, path string, body []byte, contentType, accept string) ([]byte, error) {
	var bodyReader io.Reader
	if body != nil {
		bodyReader = bytes.NewReader(body)
	}
	resp, err := c.doRequestAccept(method, path, bodyReader, contentType, accept)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	return c.readBody(resp)
}

// doWithRetry performs an HTTP request with exponential backoff retry.
// Only safe for idempotent operations (GET, HEAD).
func (c *Client) doWithRetry(method, path string, body []byte, contentType, accept string) ([]byte, error) {
	var lastErr error
	for attempt := range maxRetries {
		var bodyReader io.Reader
		if body != nil {
			bodyReader = bytes.NewReader(body)
		}

		resp, err := c.doRequestAccept(method, path, bodyReader, contentType, accept)
		if err != nil {
			oerr := apierr.AsError(err)
			if !oerr.Retryable || attempt == maxRetries-1 {
				return nil, err
			}
			lastErr = err
			backoff(attempt)
			continue
		}

		data, err := c.readBody(resp)
		resp.Body.Close()
		if err != nil {
			oerr := apierr.AsError(err)
			if oerr.Code == "rate_limit" && attempt < maxRetries-1 {
				retryAfter := 0
				if v := resp.Header.Get("Retry-After"); v != "" {
					fmt.Sscanf(v, "%d", &retryAfter)
				}
				if retryAfter > 0 {
					time.Sleep(time.Duration(retryAfter) * time.Second)
				} else {
					backoff(attempt)
				}
				lastErr = err
				continue
			}
			if !oerr.Retryable || attempt == maxRetries-1 {
				return nil, err
			}
			lastErr = err
			backoff(attempt)
			continue
		}
		return data, nil
	}
	return nil, lastErr
}

func backoff(attempt int) {
	base := time.Duration(1<<uint(attempt)) * time.Second
	jitter := time.Duration(rand.Int64N(int64(base / 2)))
	time.Sleep(base + jitter)
}

func responseError(resp *http.Response, data []byte) *apierr.Error {
	switch resp.StatusCode {
	case 401:
		return apierr.ErrAuth("unauthorized — run `hey auth login` to authenticate")
	case 403:
		return apierr.ErrForbidden("forbidden (403)")
	case 404:
		return apierr.ErrNotFound("resource", strings.TrimSuffix(resp.Request.URL.Path, ".json"))
	case 429:
		retryAfter := 0
		if v := resp.Header.Get("Retry-After"); v != "" {
			fmt.Sscanf(v, "%d", &retryAfter)
		}
		return apierr.ErrRateLimit(retryAfter)
	default:
		if resp.StatusCode >= 500 {
			return apierr.ErrAPI(resp.StatusCode, fmt.Sprintf("server error %d: %s", resp.StatusCode, string(data)))
		}
		return apierr.ErrAPI(resp.StatusCode, fmt.Sprintf("API error %d: %s", resp.StatusCode, string(data)))
	}
}

func (c *Client) Get(path string) ([]byte, error) {
	return c.doWithRetry("GET", path, nil, "", "application/json")
}

func (c *Client) GetHTML(path string) ([]byte, error) {
	return c.doWithRetry("GET", path, nil, "", "text/html")
}

func (c *Client) GetJSON(path string, v any) error {
	data, err := c.Get(path)
	if err != nil {
		return err
	}
	return json.Unmarshal(data, v)
}

func (c *Client) PostJSON(path string, body any) ([]byte, error) {
	var encoded []byte
	if body != nil {
		var err error
		encoded, err = json.Marshal(body)
		if err != nil {
			return nil, apierr.ErrAPI(0, fmt.Sprintf("could not encode body: %v", err))
		}
	}
	return c.doOnce("POST", path, encoded, "application/json", "application/json")
}

func (c *Client) PostForm(path string, values url.Values) ([]byte, error) {
	return c.doOnce("POST", path, []byte(values.Encode()), "application/x-www-form-urlencoded", "application/json")
}

func (c *Client) PatchJSON(path string, body any) ([]byte, error) {
	var encoded []byte
	if body != nil {
		var err error
		encoded, err = json.Marshal(body)
		if err != nil {
			return nil, apierr.ErrAPI(0, fmt.Sprintf("could not encode body: %v", err))
		}
	}
	return c.doOnce("PATCH", path, encoded, "application/json", "application/json")
}

func (c *Client) PutJSON(path string, body any) ([]byte, error) {
	var encoded []byte
	if body != nil {
		var err error
		encoded, err = json.Marshal(body)
		if err != nil {
			return nil, apierr.ErrAPI(0, fmt.Sprintf("could not encode body: %v", err))
		}
	}
	return c.doOnce("PUT", path, encoded, "application/json", "application/json")
}

func (c *Client) Delete(path string) ([]byte, error) {
	return c.doOnce("DELETE", path, nil, "", "application/json")
}
