package auth

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func testManager(t *testing.T, server *httptest.Server) *Manager {
	t.Helper()
	t.Setenv("HEY_NO_KEYRING", "1")
	return NewManager(server.URL, server.Client(), t.TempDir())
}

func TestHEYTokenPrecedence(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Fatal("server should not be called when HEY_TOKEN is set")
	}))
	defer server.Close()

	t.Setenv("HEY_TOKEN", "env-token-123")
	mgr := testManager(t, server)

	token, err := mgr.AccessToken(context.Background())
	if err != nil {
		t.Fatalf("AccessToken: %v", err)
	}
	if token != "env-token-123" {
		t.Errorf("token = %q, want %q", token, "env-token-123")
	}
}

func TestIsAuthenticated(t *testing.T) {
	server := httptest.NewServer(http.NotFoundHandler())
	defer server.Close()

	t.Run("with_HEY_TOKEN", func(t *testing.T) {
		t.Setenv("HEY_TOKEN", "tok")
		mgr := testManager(t, server)
		if !mgr.IsAuthenticated() {
			t.Error("expected authenticated with HEY_TOKEN set")
		}
	})

	t.Run("with_stored_token", func(t *testing.T) {
		t.Setenv("HEY_TOKEN", "")
		mgr := testManager(t, server)
		if err := mgr.LoginWithToken("stored-tok"); err != nil {
			t.Fatalf("LoginWithToken: %v", err)
		}
		if !mgr.IsAuthenticated() {
			t.Error("expected authenticated with stored token")
		}
	})

	t.Run("unauthenticated", func(t *testing.T) {
		t.Setenv("HEY_TOKEN", "")
		mgr := testManager(t, server)
		if mgr.IsAuthenticated() {
			t.Error("expected not authenticated with no credentials")
		}
	})
}

func TestNormalizeBaseURL(t *testing.T) {
	tests := []struct {
		input string
		want  string
	}{
		{"https://app.hey.com/", "https://app.hey.com"},
		{"https://app.hey.com///", "https://app.hey.com"},
		{"https://app.hey.com", "https://app.hey.com"},
	}
	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			got := normalizeBaseURL(tt.input)
			if got != tt.want {
				t.Errorf("normalizeBaseURL(%q) = %q, want %q", tt.input, got, tt.want)
			}
		})
	}
}

func TestTokenRefreshOnExpiry(t *testing.T) {
	refreshCalls := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/oauth/tokens" {
			t.Fatalf("unexpected path: %s", r.URL.Path)
		}
		refreshCalls++
		resp := OAuthToken{
			AccessToken:  "refreshed-token",
			RefreshToken: "new-refresh",
			ExpiresIn:    3600,
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(resp)
	}))
	defer server.Close()

	t.Setenv("HEY_TOKEN", "")
	mgr := testManager(t, server)

	// Save credentials that are expired (ExpiresAt in the past)
	expired := &Credentials{
		AccessToken:   "old-token",
		RefreshToken:  "refresh-tok",
		ExpiresAt:     time.Now().Unix() - 600,
		OAuthType:     "oauth",
		TokenEndpoint: fmt.Sprintf("%s/oauth/tokens", server.URL),
	}
	if err := mgr.GetStore().Save(mgr.CredentialKey(), expired); err != nil {
		t.Fatalf("Save: %v", err)
	}

	token, err := mgr.AccessToken(context.Background())
	if err != nil {
		t.Fatalf("AccessToken: %v", err)
	}

	if token != "refreshed-token" {
		t.Errorf("token = %q, want %q", token, "refreshed-token")
	}
	if refreshCalls != 1 {
		t.Errorf("refresh calls = %d, want 1", refreshCalls)
	}
}
