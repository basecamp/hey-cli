package cmd

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/basecamp/hey-cli/internal/apierr"
)

func TestPlainLoginAutoDetectsDeviceFlow(t *testing.T) {
	const userCode = "AUTO-CODE"
	const deviceCode = "auto-device-code"
	var pollCount int
	var serverURL string

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/oauth/device_authorizations":
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusOK)
			json.NewEncoder(w).Encode(map[string]any{
				"device_code":               deviceCode,
				"user_code":                 userCode,
				"verification_uri":          serverURL + "/verify",
				"verification_uri_complete": serverURL + "/verify?code=" + userCode,
				"expires_in":                300,
				"interval":                  1,
			})
		case "/oauth/tokens":
			if err := r.ParseForm(); err != nil {
				t.Fatal(err)
			}
			if r.Form.Get("grant_type") != "urn:ietf:params:oauth:grant-type:device_code" {
				t.Errorf("grant_type = %s", r.Form.Get("grant_type"))
			}
			pollCount++
			w.Header().Set("Content-Type", "application/json")
			if pollCount < 2 {
				w.WriteHeader(http.StatusBadRequest)
				json.NewEncoder(w).Encode(map[string]string{"error": "authorization_pending"})
			} else {
				w.WriteHeader(http.StatusOK)
				json.NewEncoder(w).Encode(map[string]any{
					"access_token": "auto-token",
					"expires_in":   3600,
				})
			}
		case "/authorization":
			w.Header().Set("Content-Type", "application/json")
			json.NewEncoder(w).Encode(map[string]any{
				"identity": map[string]any{"id": "1", "email_address": "test@example.com"},
				"accounts": []any{},
			})
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()
	serverURL = server.URL

	configHome := t.TempDir()
	_, response, err := runAuthCommand(t, configHome, server.URL, "", true, "auth", "login")
	if err != nil {
		t.Fatalf("plain login auto-detect: %v", err)
	}
	if response.Summary != "Logged in successfully" {
		t.Errorf("summary = %q", response.Summary)
	}
	data, ok := response.Data.(map[string]any)
	if !ok || data["method"] != "device" {
		t.Errorf("expected device method, got: %#v", response.Data)
	}
	if pollCount < 2 {
		t.Errorf("device endpoint polled %d times, expected >= 2", pollCount)
	}
}

func TestPlainLoginFallsBackWhenDeviceUnavailable(t *testing.T) {
	var sawAuthorizationsRequest bool

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/oauth/device_authorizations":
			sawAuthorizationsRequest = true
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusNotFound)
			json.NewEncoder(w).Encode(map[string]string{"error": "not_found"})
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	configHome := t.TempDir()

	errChan := make(chan error, 1)
	go func() {
		_, _, err := runAuthCommand(t, configHome, server.URL, "", true, "auth", "login")
		errChan <- err
	}()

	select {
	case err := <-errChan:
		if !sawAuthorizationsRequest {
			t.Error("device authorization endpoint was not attempted")
		}
		if err != nil && strings.Contains(err.Error(), "doesn't take scoped device sign-ins") {
			t.Error("got device unavailable error - fallback did not happen")
		}
	case <-time.After(2 * time.Second):
		if !sawAuthorizationsRequest {
			t.Error("device authorization endpoint was not attempted")
		}
	}
}

func TestDeviceCodeFlagSuccess(t *testing.T) {
	const userCode = "ABCD-EFGH"
	const deviceCode = "secret-device-code"
	var pollCount int
	var serverURL string

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/oauth/device_authorizations":
			if r.Method != "POST" {
				t.Errorf("device authorization: method = %s, want POST", r.Method)
			}
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusOK)
			response := map[string]any{
				"device_code":               deviceCode,
				"user_code":                 userCode,
				"verification_uri":          serverURL + "/verify",
				"verification_uri_complete": serverURL + "/verify?code=" + userCode,
				"expires_in":                300,
				"interval":                  1,
			}
			if err := json.NewEncoder(w).Encode(response); err != nil {
				t.Fatal(err)
			}
		case "/oauth/tokens":
			if r.Method != "POST" {
				t.Errorf("token endpoint: method = %s, want POST", r.Method)
			}
			if err := r.ParseForm(); err != nil {
				t.Fatal(err)
			}
			if r.Form.Get("grant_type") != "urn:ietf:params:oauth:grant-type:device_code" {
				t.Errorf("grant_type = %s", r.Form.Get("grant_type"))
			}
			if r.Form.Get("device_code") != deviceCode {
				t.Errorf("device_code = %s", r.Form.Get("device_code"))
			}
			pollCount++
			w.Header().Set("Content-Type", "application/json")
			if pollCount < 2 {
				w.WriteHeader(http.StatusBadRequest)
				json.NewEncoder(w).Encode(map[string]string{"error": "authorization_pending"})
			} else {
				w.WriteHeader(http.StatusOK)
				response := map[string]any{
					"access_token": "device-access-token",
					"expires_in":   3600,
				}
				json.NewEncoder(w).Encode(response)
			}
		case "/authorization":
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusOK)
			json.NewEncoder(w).Encode(map[string]any{
				"identity": map[string]any{
					"id":            "1234",
					"email_address": "user@example.com",
					"first_name":    "Test",
					"last_name":     "User",
				},
				"accounts": []any{},
			})
		default:
			t.Logf("unexpected request: %s %s", r.Method, r.URL.Path)
			http.NotFound(w, r)
		}
	}))
	defer server.Close()
	serverURL = server.URL

	configHome := t.TempDir()
	_, response, err := runAuthCommand(t, configHome, server.URL, "", true, "auth", "login", "--device-code")
	if err != nil {
		t.Fatalf("--device-code login: %v", err)
	}
	if response.Summary != "Logged in successfully" {
		t.Errorf("summary = %q", response.Summary)
	}
	data, ok := response.Data.(map[string]any)
	if !ok || data["method"] != "device" {
		t.Errorf("login data = %#v", response.Data)
	}
	if pollCount < 2 {
		t.Errorf("token endpoint was polled %d times, want >= 2", pollCount)
	}

	_, status, err := runAuthCommand(t, configHome, server.URL, "", true, "auth", "status")
	if err != nil {
		t.Fatalf("auth status: %v", err)
	}
	statusData, ok := status.Data.(map[string]any)
	if !ok || statusData["authenticated"] != true {
		t.Errorf("not authenticated after device login: %#v", statusData)
	}
}

func TestDeviceLoginSlowDown(t *testing.T) {
	var pollCount int
	var lastPollTime time.Time
	const minInterval = 100 * time.Millisecond
	var serverURL string

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/oauth/device_authorizations":
			w.Header().Set("Content-Type", "application/json")
			json.NewEncoder(w).Encode(map[string]any{
				"device_code":      "device-code",
				"user_code":        "TEST-CODE",
				"verification_uri": serverURL + "/verify",
				"expires_in":       300,
				"interval":         0,
			})
		case "/oauth/tokens":
			now := time.Now()
			if !lastPollTime.IsZero() && now.Sub(lastPollTime) < minInterval {
				t.Errorf("poll %d too soon: %v after previous", pollCount+1, now.Sub(lastPollTime))
			}
			lastPollTime = now
			pollCount++
			w.Header().Set("Content-Type", "application/json")
			if pollCount == 1 {
				w.WriteHeader(http.StatusBadRequest)
				json.NewEncoder(w).Encode(map[string]string{"error": "slow_down"})
			} else if pollCount == 2 {
				w.WriteHeader(http.StatusBadRequest)
				json.NewEncoder(w).Encode(map[string]string{"error": "authorization_pending"})
			} else {
				w.WriteHeader(http.StatusOK)
				json.NewEncoder(w).Encode(map[string]any{
					"access_token": "token",
					"expires_in":   3600,
				})
			}
		case "/authorization":
			w.Header().Set("Content-Type", "application/json")
			json.NewEncoder(w).Encode(map[string]any{
				"identity": map[string]any{"id": "1", "email_address": "test@example.com"},
				"accounts": []any{},
			})
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()
	serverURL = server.URL

	configHome := t.TempDir()
	_, response, err := runAuthCommand(t, configHome, server.URL, "", true, "auth", "login", "--device-code")
	if err != nil {
		t.Fatalf("device login: %v", err)
	}
	if response.Summary != "Logged in successfully" {
		t.Errorf("summary = %q", response.Summary)
	}
	if pollCount < 3 {
		t.Errorf("polled %d times, expected at least 3", pollCount)
	}
}

func TestDeviceLoginAccessDenied(t *testing.T) {
	var serverURL string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/oauth/device_authorizations":
			w.Header().Set("Content-Type", "application/json")
			json.NewEncoder(w).Encode(map[string]any{
				"device_code":      "device-code",
				"user_code":        "DENY-ME",
				"verification_uri": serverURL + "/verify",
				"expires_in":       300,
				"interval":         1,
			})
		case "/oauth/tokens":
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusBadRequest)
			json.NewEncoder(w).Encode(map[string]string{"error": "access_denied"})
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()
	serverURL = server.URL

	configHome := t.TempDir()
	_, _, err := runAuthCommand(t, configHome, server.URL, "", true, "auth", "login", "--device-code")
	if err == nil {
		t.Fatal("expected error for access_denied, got nil")
	}
	if !strings.Contains(err.Error(), "denied") {
		t.Errorf("error should mention denial: %v", err)
	}
	var authErr *apierr.Error
	if !errors.As(err, &authErr) || authErr.Code != apierr.CodeAuth {
		t.Errorf("error code = %v, want auth", authErr)
	}
}

func TestPlainLoginDeviceErrorsNoFallback(t *testing.T) {
	var serverURL string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/oauth/device_authorizations":
			w.Header().Set("Content-Type", "application/json")
			json.NewEncoder(w).Encode(map[string]any{
				"device_code":      "device-code",
				"user_code":        "WILL-DENY",
				"verification_uri": serverURL + "/verify",
				"expires_in":       300,
				"interval":         1,
			})
		case "/oauth/tokens":
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusBadRequest)
			json.NewEncoder(w).Encode(map[string]string{"error": "access_denied"})
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()
	serverURL = server.URL

	configHome := t.TempDir()
	_, _, err := runAuthCommand(t, configHome, server.URL, "", true, "auth", "login")
	if err == nil {
		t.Fatal("expected error for access_denied on plain login, got nil")
	}
	if !strings.Contains(err.Error(), "denied") {
		t.Errorf("error should mention denial: %v", err)
	}
}

func TestDeviceLoginExpiredToken(t *testing.T) {
	var serverURL string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/oauth/device_authorizations":
			w.Header().Set("Content-Type", "application/json")
			json.NewEncoder(w).Encode(map[string]any{
				"device_code":      "device-code",
				"user_code":        "EXPIRED",
				"verification_uri": serverURL + "/verify",
				"expires_in":       1,
				"interval":         1,
			})
		case "/oauth/tokens":
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusBadRequest)
			json.NewEncoder(w).Encode(map[string]string{"error": "expired_token"})
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()
	serverURL = server.URL

	configHome := t.TempDir()
	_, _, err := runAuthCommand(t, configHome, server.URL, "", true, "auth", "login", "--device-code")
	if err == nil {
		t.Fatal("expected error for expired token, got nil")
	}
	if !strings.Contains(err.Error(), "expired") {
		t.Errorf("error should mention expiry: %v", err)
	}
}

func TestDeviceCodeServerUnavailable(t *testing.T) {
	tests := []struct {
		name       string
		statusCode int
		body       string
		wantError  string
	}{
		{
			name:       "404 not found",
			statusCode: http.StatusNotFound,
			body:       `{"error": "not_found"}`,
			wantError:  "device code flow unavailable",
		},
		{
			name:       "400 invalid_request",
			statusCode: http.StatusBadRequest,
			body:       `{"error": "invalid_request"}`,
			wantError:  "device code flow unavailable",
		},
		{
			name:       "401 unauthorized_client",
			statusCode: http.StatusUnauthorized,
			body:       `{"error": "unauthorized_client"}`,
			wantError:  "device code flow unavailable",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path == "/oauth/device_authorizations" {
					w.Header().Set("Content-Type", "application/json")
					w.WriteHeader(tt.statusCode)
					fmt.Fprint(w, tt.body)
				} else {
					http.NotFound(w, r)
				}
			}))
			defer server.Close()

			configHome := t.TempDir()
			_, _, err := runAuthCommand(t, configHome, server.URL, "", true, "auth", "login", "--device-code")
			if err == nil {
				t.Fatal("expected error, got nil")
			}
			if !strings.Contains(err.Error(), tt.wantError) {
				t.Errorf("error = %v, want substring %q", err, tt.wantError)
			}
			var authErr *apierr.Error
			if !errors.As(err, &authErr) || authErr.Code != apierr.CodeAuth {
				t.Errorf("error code = %v, want auth", authErr)
			}
		})
	}
}

func TestDeviceCodeFlagConflicts(t *testing.T) {
	tests := []struct {
		name string
		args []string
		want string
	}{
		{
			name: "device-code with token",
			args: []string{"auth", "login", "--device-code", "--token", "fake-token"},
			want: "--device-code signs in through HEY; it takes no --token",
		},
		{
			name: "device-code with mcp",
			args: []string{"auth", "login", "--device-code", "--mcp"},
			want: "--mcp signs in through HEY; it takes no --device-code",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			server := httptest.NewServer(http.NotFoundHandler())
			defer server.Close()
			configHome := t.TempDir()
			_, _, err := runAuthCommand(t, configHome, server.URL, "", true, tt.args...)
			if err == nil {
				t.Fatal("expected error, got nil")
			}
			if !strings.Contains(err.Error(), tt.want) {
				t.Errorf("error = %v, want substring %q", err, tt.want)
			}
		})
	}
}

func TestTopLevelLoginWithDeviceCode(t *testing.T) {
	const userCode = "TOP-LEVEL"
	var seenDevice bool
	var serverURL string

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/oauth/device_authorizations":
			seenDevice = true
			w.Header().Set("Content-Type", "application/json")
			json.NewEncoder(w).Encode(map[string]any{
				"device_code":      "device-code",
				"user_code":        userCode,
				"verification_uri": serverURL + "/verify",
				"expires_in":       300,
				"interval":         1,
			})
		case "/oauth/tokens":
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusOK)
			json.NewEncoder(w).Encode(map[string]any{
				"access_token": "token",
				"expires_in":   3600,
			})
		case "/authorization":
			w.Header().Set("Content-Type", "application/json")
			json.NewEncoder(w).Encode(map[string]any{
				"identity": map[string]any{"id": "1", "email_address": "test@example.com"},
				"accounts": []any{},
			})
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()
	serverURL = server.URL

	configHome := t.TempDir()
	_, response, err := runAuthCommand(t, configHome, server.URL, "", true, "login", "--device-code")
	if err != nil {
		t.Fatalf("top-level login --device-code: %v", err)
	}
	if !seenDevice {
		t.Error("device authorization endpoint was not called")
	}
	if response.Summary != "Logged in successfully" {
		t.Errorf("summary = %q", response.Summary)
	}
}
