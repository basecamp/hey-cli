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

// Never include token command output in a failure: these are real dev OAuth tokens.
func TestAuthToken(t *testing.T) {
	stdout, _, code := hey(t, "auth", "token")
	if code != 0 || strings.TrimSpace(stdout) == "" || strings.Contains(stdout, sessionCookie) {
		t.Fatalf("auth token did not return an OAuth bearer token (exit %d; empty=%v; cookie=%v)", code, strings.TrimSpace(stdout) == "", strings.Contains(stdout, sessionCookie))
	}
}

func TestAuthTokenStored(t *testing.T) {
	stdout, _, code := hey(t, "auth", "token", "--stored")
	if code != 0 || strings.TrimSpace(stdout) == "" || strings.Contains(stdout, sessionCookie) {
		t.Fatalf("auth token --stored did not return an OAuth bearer token (exit %d; empty=%v; cookie=%v)", code, strings.TrimSpace(stdout) == "", strings.Contains(stdout, sessionCookie))
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
	_, _, code := hey(t, "box", "list", "--json")
	if code == 0 {
		t.Errorf("expected 'hey box list' to fail when not authenticated")
	}

	// Re-login through browser OAuth.
	if err := setupCLI("auth", "login"); err != nil {
		t.Fatal(err)
	}

	// Verify we're logged back in.
	resp2 := heyJSON(t, "auth", "status")
	data2 := dataAs[map[string]any](t, resp2)
	if auth, _ := data2["authenticated"].(bool); !auth {
		t.Errorf("expected authenticated=true after re-login")
	}
}

func TestLoginLogoutShortcuts(t *testing.T) {
	// hey logout == hey auth logout.
	heyOK(t, "logout")
	status := heyJSON(t, "auth", "status")
	if data := dataAs[map[string]any](t, status); data["authenticated"] == true {
		t.Errorf("expected authenticated=false after hey logout")
	}

	// hey login == hey auth login.
	if err := setupCLI("login"); err != nil {
		t.Fatal(err)
	}
	status = heyJSON(t, "auth", "status")
	if data := dataAs[map[string]any](t, status); data["authenticated"] != true {
		t.Errorf("expected authenticated=true after hey login")
	}
}
