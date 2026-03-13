package smoke_test

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestAuthStatus(t *testing.T) {
	resp := heyJSON(t, "auth", "status")
	data := dataAs[map[string]any](t, resp)

	if auth, ok := data["authenticated"].(bool); !ok || !auth {
		t.Fatalf("expected authenticated=true, got %v", data["authenticated"])
	}
	if bu, ok := data["base_url"].(string); !ok || bu != baseURL {
		t.Errorf("expected base_url=%s, got %v", baseURL, data["base_url"])
	}
}

func TestAuthToken(t *testing.T) {
	stdout, stderr, code := hey(t, "auth", "token")
	if code != 0 {
		t.Fatalf("auth token failed (exit %d): %s", code, stderr)
	}
	stdout = strings.TrimSpace(stdout)
	if stdout == "" {
		t.Error("expected auth token to output a non-empty token")
	}
}

func TestAuthTokenStored(t *testing.T) {
	stdout, stderr, code := hey(t, "auth", "token", "--stored")
	if code != 0 {
		t.Fatalf("auth token --stored failed (exit %d): %s", code, stderr)
	}
	stdout = strings.TrimSpace(stdout)
	if stdout == "" {
		t.Error("expected auth token --stored to output a non-empty token")
	}
}

func TestAuthRefresh(t *testing.T) {
	_, stderr, code := hey(t, "auth", "refresh")
	if code != 0 {
		t.Fatalf("auth refresh failed (exit %d): %s", code, stderr)
	}
}

func TestAuthLogoutAndRelogin(t *testing.T) {
	// Logout
	heyOK(t, "auth", "logout")

	// Verify we're logged out.
	stdout := heyOK(t, "auth", "status", "--json")
	var resp Response
	if err := json.Unmarshal([]byte(stdout), &resp); err != nil {
		t.Fatalf("failed to parse JSON: %v", err)
	}
	data := dataAs[map[string]any](t, resp)
	if auth, _ := data["authenticated"].(bool); auth {
		t.Errorf("expected authenticated=false after logout")
	}

	// Commands requiring auth should fail.
	_, _, code := hey(t, "boxes", "--json")
	if code == 0 {
		t.Errorf("expected 'hey boxes' to fail when not authenticated")
	}

	// Re-login with cookie.
	heyOK(t, "auth", "login", "--cookie", sessionCookie)

	// Verify we're logged back in.
	resp2 := heyJSON(t, "auth", "status")
	data2 := dataAs[map[string]any](t, resp2)
	if auth, _ := data2["authenticated"].(bool); !auth {
		t.Errorf("expected authenticated=true after re-login")
	}
}
