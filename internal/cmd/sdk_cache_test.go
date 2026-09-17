package cmd

import (
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/basecamp/hey-cli/internal/apierr"
	"github.com/basecamp/hey-cli/internal/auth"
)

func TestSDK401RetryAdoptsCredentialsAnotherProcessStored(t *testing.T) {
	tests := []struct {
		name        string
		initial     *auth.Credentials
		replacement *auth.Credentials
		first       string
		second      string
	}{
		{
			name:        "OAuth",
			initial:     &auth.Credentials{AccessToken: "old-oauth", RefreshToken: "old-refresh", OAuthType: "oauth"},
			replacement: &auth.Credentials{AccessToken: "new-oauth", RefreshToken: "new-refresh", OAuthType: "oauth"},
			first:       "Bearer old-oauth",
			second:      "Bearer new-oauth",
		},
		{
			name:        "static token",
			initial:     &auth.Credentials{AccessToken: "old-static", OAuthType: "token"},
			replacement: &auth.Credentials{AccessToken: "new-static", OAuthType: "token"},
			first:       "Bearer old-static",
			second:      "Bearer new-static",
		},
		{
			name:        "cookie",
			initial:     &auth.Credentials{SessionCookie: "old-cookie", OAuthType: "cookie"},
			replacement: &auth.Credentials{SessionCookie: "new-cookie", OAuthType: "cookie"},
			first:       "session_token=old-cookie",
			second:      "session_token=new-cookie",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Setenv("HEY_TOKEN", "")
			t.Setenv("HEY_NO_KEYRING", "1")
			t.Setenv("XDG_CACHE_HOME", t.TempDir())
			configDir := t.TempDir()

			var manager *auth.Manager
			var mu sync.Mutex
			var credentials []string
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				credential := r.Header.Get("Authorization")
				if credential == "" {
					credential = r.Header.Get("Cookie")
				}
				mu.Lock()
				credentials = append(credentials, credential)
				attempt := len(credentials)
				mu.Unlock()

				switch attempt {
				case 1:
					if credential != tt.first {
						t.Errorf("first credential = %q, want %q", credential, tt.first)
					}
					if err := manager.GetStore().Save(manager.CredentialKey(), tt.replacement); err != nil {
						t.Errorf("replace credentials: %v", err)
					}
					http.Error(w, "expired credential", http.StatusUnauthorized)
				case 2:
					if credential != tt.second {
						t.Errorf("retry credential = %q, want %q", credential, tt.second)
					}
					w.Header().Set("Content-Type", "application/json")
					_, _ = io.WriteString(w, `{"retried":true}`)
				default:
					t.Errorf("unexpected SDK attempt %d with %q", attempt, credential)
					http.Error(w, "too many attempts", http.StatusInternalServerError)
				}
			}))
			defer server.Close()

			manager = auth.NewManager(server.URL, server.Client(), configDir)
			if err := manager.GetStore().Save(manager.CredentialKey(), tt.initial); err != nil {
				t.Fatalf("seed credentials: %v", err)
			}
			initSDK(manager, server.URL)

			response, err := sdk.Get(t.Context(), "/auth-retry")
			if err != nil {
				t.Fatalf("SDK Get: %v", err)
			}
			if got := string(response.Data); got != `{"retried":true}` {
				t.Errorf("response = %s, want successful retry", got)
			}
			mu.Lock()
			attempts := len(credentials)
			mu.Unlock()
			if attempts != 2 {
				t.Errorf("SDK attempts = %d, want initial request and one retry", attempts)
			}
		})
	}
}

func TestInitSDKEnablesTheRevalidationCache(t *testing.T) {
	cacheHome := t.TempDir()
	t.Setenv("XDG_CACHE_HOME", cacheHome)

	initSDK(auth.NewManager("https://app.hey.com", nil, t.TempDir()), "https://app.hey.com")

	if !sdkClientCfg.CacheEnabled {
		t.Error("expected the SDK cache enabled")
	}
	if want := filepath.Join(cacheHome, "hey-cli", "http"); sdkClientCfg.CacheDir != want {
		t.Errorf("cache dir = %q, want %q", sdkClientCfg.CacheDir, want)
	}
}

// A login over standing credentials replaces them, orphaning whatever the old
// ones cached: the replacement clears the cache without waiting for a logout.
func TestLoginReplacingCredentialsClearsTheHTTPCache(t *testing.T) {
	configHome := t.TempDir()
	responses, etags := seedHTTPCache(t, configHome)

	if _, _, err := runAuthCommand(t, configHome, "https://app.hey.com", "", true, "auth", "login", "--cookie", "replacement-cookie"); err != nil {
		t.Fatalf("auth login: %v", err)
	}

	if _, err := os.Stat(responses); !os.IsNotExist(err) {
		t.Error("expected login to drop the previous credentials' cached responses")
	}
	if _, err := os.Stat(etags); !os.IsNotExist(err) {
		t.Error("expected login to drop the previous credentials' cached ETags")
	}
}

func TestLogoutClearsTheHTTPCache(t *testing.T) {
	configHome := t.TempDir()
	responses, etags := seedHTTPCache(t, configHome)

	if _, _, err := runAuthCommand(t, configHome, "https://app.hey.com", "", true, "auth", "login", "--cookie", "session-cookie"); err != nil {
		t.Fatalf("auth login: %v", err)
	}
	if _, logout, err := runAuthCommand(t, configHome, "https://app.hey.com", "", true, "auth", "logout"); err != nil || logout.Summary != "Logged out" {
		t.Fatalf("auth logout: %v (%q)", err, logout.Summary)
	}

	if _, err := os.Stat(responses); !os.IsNotExist(err) {
		t.Error("expected logout to drop the cached responses")
	}
	if _, err := os.Stat(etags); !os.IsNotExist(err) {
		t.Error("expected logout to drop the cached ETags")
	}
}

// A refresh token HEY has refused is forgotten by the manager itself, in the middle
// of whatever command sent it. Cached mail must not outlive that credential any more
// than it outlives an explicit logout.
func TestARefusedGrantClearsTheHTTPCache(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadRequest)
		_, _ = io.WriteString(w, `{"error":"invalid_grant","error_description":"The refresh token is invalid"}`)
	}))
	defer server.Close()
	configHome := t.TempDir()
	responses, etags := seedHTTPCache(t, configHome)
	manager := seedExpiredCredential(t, configHome, server)

	_, _, err := runAuthCommand(t, configHome, server.URL, "", true, "auth", "refresh")
	var classified *apierr.Error
	if !errors.As(err, &classified) || classified.Code != apierr.CodeAuth {
		t.Fatalf("error = %v, want one coded %q", err, apierr.CodeAuth)
	}

	if _, loadErr := manager.GetStore().Load(manager.CredentialKey()); loadErr == nil {
		t.Error("the refused credential is still stored")
	}
	if _, statErr := os.Stat(responses); !os.IsNotExist(statErr) {
		t.Error("expected the refused grant to drop the cached responses")
	}
	if _, statErr := os.Stat(etags); !os.IsNotExist(statErr) {
		t.Error("expected the refused grant to drop the cached ETags")
	}
}

// Anything softer than a refused grant is no verdict on the credential, so it keeps
// both the credential and the mail it fetched.
func TestATransientRefreshFailureKeepsTheCredentialAndTheHTTPCache(t *testing.T) {
	statuses := map[string]int{
		"rate limited":   http.StatusTooManyRequests,
		"origin failure": http.StatusBadGateway,
	}

	for name, status := range statuses {
		t.Run(name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.WriteHeader(status)
			}))
			defer server.Close()
			configHome := t.TempDir()
			responses, etags := seedHTTPCache(t, configHome)
			manager := seedExpiredCredential(t, configHome, server)

			if _, _, err := runAuthCommand(t, configHome, server.URL, "", true, "auth", "refresh"); err == nil {
				t.Fatal("auth refresh succeeded against a failing token endpoint")
			}

			creds, loadErr := manager.GetStore().Load(manager.CredentialKey())
			if loadErr != nil || creds.RefreshToken != "old-refresh" {
				t.Errorf("credentials = %#v, %v; want them kept", creds, loadErr)
			}
			if _, statErr := os.Stat(filepath.Join(responses, "abc123.body")); statErr != nil {
				t.Errorf("cached response: %v; want it kept", statErr)
			}
			if _, statErr := os.Stat(etags); statErr != nil {
				t.Errorf("cached ETags: %v; want them kept", statErr)
			}
		})
	}
}

// seedHTTPCache plants one cached response and its ETag under configHome, the way
// the SDK's cache lays them out, and answers where each is.
func seedHTTPCache(t *testing.T, configHome string) (responses, etags string) {
	t.Helper()
	responses = filepath.Join(configHome, "hey-cli", "http", "responses")
	etags = filepath.Join(configHome, "hey-cli", "http", "etags.json")
	if err := os.MkdirAll(responses, 0o700); err != nil {
		t.Fatal(err)
	}
	for name, content := range map[string]string{
		filepath.Join(responses, "abc123.body"): `{"cached":"mail"}`,
		etags:                                   `{"abc123":"\"v1\""}`,
	} {
		if err := os.WriteFile(name, []byte(content), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	return responses, etags
}

// seedExpiredCredential stores a credential whose access token has expired, so the
// next command has to send the refresh token to server.
func seedExpiredCredential(t *testing.T, configHome string, server *httptest.Server) *auth.Manager {
	t.Helper()
	t.Setenv("HEY_NO_KEYRING", "1")
	manager := auth.NewManager(server.URL, server.Client(), filepath.Join(configHome, "hey-cli"))
	expired := &auth.Credentials{AccessToken: "old-access", RefreshToken: "old-refresh", ExpiresAt: time.Now().Add(-time.Hour).Unix()}
	if err := manager.GetStore().Save(manager.CredentialKey(), expired); err != nil {
		t.Fatalf("seed credentials: %v", err)
	}
	return manager
}
