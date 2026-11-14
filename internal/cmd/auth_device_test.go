package cmd

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/basecamp/hey-cli/internal/apierr"
)

func TestPlainLoginUsesDeviceFlowWhenAdvertised(t *testing.T) {
	const userCode = "AUTO-CODE"
	const deviceCode = "auto-device-code"
	var pollCount int
	var serverURL string

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/.well-known/oauth-authorization-server":
			w.Header().Set("Content-Type", "application/json")
			json.NewEncoder(w).Encode(map[string]any{
				"issuer":                           serverURL,
				"authorization_endpoint":           serverURL + "/oauth/authorizations/new",
				"token_endpoint":                   serverURL + "/oauth/tokens",
				"device_authorization_endpoint":    serverURL + "/oauth/device_authorizations",
				"grant_types_supported":            []string{"authorization_code", "refresh_token", "urn:ietf:params:oauth:grant-type:device_code"},
				"code_challenge_methods_supported": []string{"S256"},
			})
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
		t.Fatalf("plain login with device metadata: %v", err)
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

func TestPlainLoginFallsBackWhenNoDeviceEndpoint(t *testing.T) {
	sawMetadataRequest := make(chan bool, 1)
	var serverURL string

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/.well-known/oauth-authorization-server":
			select {
			case sawMetadataRequest <- true:
			default:
			}
			w.Header().Set("Content-Type", "application/json")
			json.NewEncoder(w).Encode(map[string]any{
				"issuer":                           serverURL,
				"authorization_endpoint":           serverURL + "/oauth/authorizations/new",
				"token_endpoint":                   serverURL + "/oauth/tokens",
				"grant_types_supported":            []string{"authorization_code", "refresh_token"},
				"code_challenge_methods_supported": []string{"S256"},
			})
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()
	serverURL = server.URL

	configHome := t.TempDir()

	done := make(chan error, 1)
	go func() {
		_, _, err := runAuthCommand(t, configHome, server.URL, "", true, "auth", "login")
		done <- err
	}()

	select {
	case <-done:
	case <-time.After(2 * time.Second):
	}

	select {
	case <-sawMetadataRequest:
	default:
		t.Error("metadata endpoint was not requested")
	}
}

func TestPlainLogin404MetadataFallsBack(t *testing.T) {
	sawMetadataRequest := make(chan bool, 1)

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/.well-known/oauth-authorization-server":
			select {
			case sawMetadataRequest <- true:
			default:
			}
			w.WriteHeader(http.StatusNotFound)
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	configHome := t.TempDir()

	done := make(chan error, 1)
	go func() {
		_, _, err := runAuthCommand(t, configHome, server.URL, "", true, "auth", "login")
		done <- err
	}()

	select {
	case <-done:
	case <-time.After(2 * time.Second):
	}

	select {
	case <-sawMetadataRequest:
	default:
		t.Error("metadata endpoint was not requested")
	}
}

func TestPlainLoginDeviceErrorNoFallback(t *testing.T) {
	var serverURL string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/.well-known/oauth-authorization-server":
			w.Header().Set("Content-Type", "application/json")
			json.NewEncoder(w).Encode(map[string]any{
				"issuer":                           serverURL,
				"authorization_endpoint":           serverURL + "/oauth/authorizations/new",
				"token_endpoint":                   serverURL + "/oauth/tokens",
				"device_authorization_endpoint":    serverURL + "/oauth/device_authorizations",
				"grant_types_supported":            []string{"authorization_code", "refresh_token", "urn:ietf:params:oauth:grant-type:device_code"},
				"code_challenge_methods_supported": []string{"S256"},
			})
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

func TestUnauthorizedClientAfterDiscoveryErrors(t *testing.T) {
	var serverURL string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/.well-known/oauth-authorization-server":
			w.Header().Set("Content-Type", "application/json")
			json.NewEncoder(w).Encode(map[string]any{
				"issuer":                           serverURL,
				"device_authorization_endpoint":    serverURL + "/oauth/device_authorizations",
				"token_endpoint":                   serverURL + "/oauth/tokens",
				"grant_types_supported":            []string{"urn:ietf:params:oauth:grant-type:device_code"},
				"code_challenge_methods_supported": []string{"S256"},
			})
		case "/oauth/device_authorizations":
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusUnauthorized)
			json.NewEncoder(w).Encode(map[string]string{"error": "unauthorized_client"})
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()
	serverURL = server.URL

	configHome := t.TempDir()
	_, _, err := runAuthCommand(t, configHome, server.URL, "", true, "auth", "login")
	if err == nil {
		t.Fatal("expected error for unauthorized_client after discovery, got nil")
	}
	if !strings.Contains(err.Error(), "unauthorized_client") {
		t.Errorf("error should mention unauthorized_client: %v", err)
	}
}

func TestDeviceCodeFlagSuccess(t *testing.T) {
	const userCode = "ABCD-EFGH"
	const deviceCode = "secret-device-code"
	var pollCount int
	var serverURL string

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/.well-known/oauth-authorization-server":
			w.Header().Set("Content-Type", "application/json")
			json.NewEncoder(w).Encode(map[string]any{
				"issuer":                           serverURL,
				"device_authorization_endpoint":    serverURL + "/oauth/device_authorizations",
				"token_endpoint":                   serverURL + "/oauth/tokens",
				"grant_types_supported":            []string{"urn:ietf:params:oauth:grant-type:device_code"},
				"code_challenge_methods_supported": []string{"S256"},
			})
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

func TestDeviceCodeFlagWithoutAdvertisedEndpoint(t *testing.T) {
	var serverURL string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/.well-known/oauth-authorization-server":
			w.Header().Set("Content-Type", "application/json")
			json.NewEncoder(w).Encode(map[string]any{
				"issuer":                           serverURL,
				"authorization_endpoint":           serverURL + "/oauth/authorizations/new",
				"token_endpoint":                   serverURL + "/oauth/tokens",
				"grant_types_supported":            []string{"authorization_code", "refresh_token"},
				"code_challenge_methods_supported": []string{"S256"},
			})
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()
	serverURL = server.URL

	configHome := t.TempDir()
	_, _, err := runAuthCommand(t, configHome, server.URL, "", true, "auth", "login", "--device-code")
	if err == nil {
		t.Fatal("expected error when --device-code used without advertised endpoint, got nil")
	}
	if !strings.Contains(err.Error(), "device code flow unavailable") {
		t.Errorf("error should mention device code flow unavailable: %v", err)
	}
}

func TestDeviceLoginSlowDown(t *testing.T) {
	var pollCount int
	var lastPollTime time.Time
	const minInterval = 100 * time.Millisecond
	var serverURL string

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/.well-known/oauth-authorization-server":
			w.Header().Set("Content-Type", "application/json")
			json.NewEncoder(w).Encode(map[string]any{
				"issuer":                        serverURL,
				"device_authorization_endpoint": serverURL + "/oauth/device_authorizations",
				"token_endpoint":                serverURL + "/oauth/tokens",
				"grant_types_supported":         []string{"urn:ietf:params:oauth:grant-type:device_code"},
			})
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
		case "/.well-known/oauth-authorization-server":
			w.Header().Set("Content-Type", "application/json")
			json.NewEncoder(w).Encode(map[string]any{
				"issuer":                        serverURL,
				"device_authorization_endpoint": serverURL + "/oauth/device_authorizations",
				"token_endpoint":                serverURL + "/oauth/tokens",
				"grant_types_supported":         []string{"urn:ietf:params:oauth:grant-type:device_code"},
			})
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

func TestDeviceLoginExpiredToken(t *testing.T) {
	var serverURL string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/.well-known/oauth-authorization-server":
			w.Header().Set("Content-Type", "application/json")
			json.NewEncoder(w).Encode(map[string]any{
				"issuer":                        serverURL,
				"device_authorization_endpoint": serverURL + "/oauth/device_authorizations",
				"token_endpoint":                serverURL + "/oauth/tokens",
				"grant_types_supported":         []string{"urn:ietf:params:oauth:grant-type:device_code"},
			})
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
		case "/.well-known/oauth-authorization-server":
			w.Header().Set("Content-Type", "application/json")
			json.NewEncoder(w).Encode(map[string]any{
				"issuer":                        serverURL,
				"device_authorization_endpoint": serverURL + "/oauth/device_authorizations",
				"token_endpoint":                serverURL + "/oauth/tokens",
				"grant_types_supported":         []string{"urn:ietf:params:oauth:grant-type:device_code"},
			})
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
