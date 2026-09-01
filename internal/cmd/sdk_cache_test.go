package cmd

import (
	"os"
	"path/filepath"
	"testing"

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
	responses := filepath.Join(configHome, "hey-cli", "http", "responses")
	if err := os.MkdirAll(responses, 0o700); err != nil {
		t.Fatal(err)
	}
	for name, content := range map[string]string{
		filepath.Join(responses, "abc123.body"):                    `{"cached":"mail"}`,
		filepath.Join(configHome, "hey-cli", "http", "etags.json"): `{"abc123":"\"v1\""}`,
	} {
		if err := os.WriteFile(name, []byte(content), 0o600); err != nil {
			t.Fatal(err)
		}
	}

	if _, _, err := runAuthCommand(t, configHome, "https://app.hey.com", "", true, "auth", "login", "--cookie", "replacement-cookie"); err != nil {
		t.Fatalf("auth login: %v", err)
	}

	if _, err := os.Stat(responses); !os.IsNotExist(err) {
		t.Error("expected login to drop the previous credentials' cached responses")
	}
	if _, err := os.Stat(filepath.Join(configHome, "hey-cli", "http", "etags.json")); !os.IsNotExist(err) {
		t.Error("expected login to drop the previous credentials' cached ETags")
	}
}

func TestLogoutClearsTheHTTPCache(t *testing.T) {
	configHome := t.TempDir()
	responses := filepath.Join(configHome, "hey-cli", "http", "responses")
	if err := os.MkdirAll(responses, 0o700); err != nil {
		t.Fatal(err)
	}
	for name, content := range map[string]string{
		filepath.Join(responses, "abc123.body"):                    `{"cached":"mail"}`,
		filepath.Join(configHome, "hey-cli", "http", "etags.json"): `{"abc123":"\"v1\""}`,
	} {
		if err := os.WriteFile(name, []byte(content), 0o600); err != nil {
			t.Fatal(err)
		}
	}

	if _, _, err := runAuthCommand(t, configHome, "https://app.hey.com", "", true, "auth", "login", "--cookie", "session-cookie"); err != nil {
		t.Fatalf("auth login: %v", err)
	}
	if _, logout, err := runAuthCommand(t, configHome, "https://app.hey.com", "", true, "auth", "logout"); err != nil || logout.Summary != "Logged out" {
		t.Fatalf("auth logout: %v (%q)", err, logout.Summary)
	}

	if _, err := os.Stat(responses); !os.IsNotExist(err) {
		t.Error("expected logout to drop the cached responses")
	}
	if _, err := os.Stat(filepath.Join(configHome, "hey-cli", "http", "etags.json")); !os.IsNotExist(err) {
		t.Error("expected logout to drop the cached ETags")
	}
}
