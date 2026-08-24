package smoke_test

import (
	"encoding/json"
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

// TestMain authenticates with a browser session cookie, and auth token refuses
// to print a cookie as a bearer token: HEY sends it as a Cookie header, so
// handing it out as "Authorization: Bearer" would 401 with nothing to explain
// it. The refusal is the command's behavior under this suite's auth method.
func TestAuthToken(t *testing.T) {
	_, stderr, code := hey(t, "auth", "token")
	if code != 3 {
		t.Fatalf("expected auth token to refuse the session cookie with exit 3, got exit %d: %s", code, stderr)
	}
	assertContains(t, stderr, "browser session cookie")
}

func TestAuthTokenStored(t *testing.T) {
	_, stderr, code := hey(t, "auth", "token", "--stored")
	if code != 3 {
		t.Fatalf("expected auth token --stored to refuse the session cookie with exit 3, got exit %d: %s", code, stderr)
	}
	assertContains(t, stderr, "browser session cookie")
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

	// Re-login with cookie.
	heyOK(t, "auth", "login", "--cookie", sessionCookie)

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
	heyOK(t, "login", "--cookie", sessionCookie)
	status = heyJSON(t, "auth", "status")
	if data := dataAs[map[string]any](t, status); data["authenticated"] != true {
		t.Errorf("expected authenticated=true after hey login --cookie")
	}
}
