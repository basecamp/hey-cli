package cmd

import (
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/basecamp/hey-cli/internal/apierr"
	"github.com/basecamp/hey-cli/internal/auth"
)

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
