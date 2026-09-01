package cmd

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/basecamp/hey-cli/internal/auth"
	"github.com/basecamp/hey-cli/internal/output"
)

func runAuthCommand(t *testing.T, configHome, baseURL, envToken string, jsonOutput bool, args ...string) (string, output.Response, error) {
	t.Helper()
	t.Setenv("HEY_TOKEN", envToken)
	t.Setenv("HEY_NO_KEYRING", "1")
	t.Setenv("HEY_BASE_URL", "")
	// Never let a test detect the developer's Omarchy and talk to its shell.
	t.Setenv("OMARCHY_PATH", "")
	t.Setenv("HOME", configHome)
	t.Setenv("XDG_CONFIG_HOME", configHome)
	t.Setenv("XDG_STATE_HOME", configHome)
	t.Setenv("XDG_CACHE_HOME", configHome)

	root := newRootCmd()
	var stdout, stderr bytes.Buffer
	root.SetOut(&stdout)
	root.SetErr(&stderr)
	commandArgs := []string{"--base-url", baseURL}
	if jsonOutput {
		commandArgs = append(commandArgs, "--json")
	}
	root.SetArgs(append(commandArgs, args...))
	err := root.Execute()

	var response output.Response
	if jsonOutput && stdout.Len() > 0 {
		if decodeErr := json.Unmarshal(stdout.Bytes(), &response); decodeErr != nil {
			t.Fatalf("decode output %q: %v", stdout.String(), decodeErr)
		}
	}
	return stdout.String(), response, err
}

func TestAuthCookieLoginStatusAndLogout(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Errorf("unexpected HTTP request: %s %s", r.Method, r.URL.Path)
	}))
	defer server.Close()
	configHome := t.TempDir()

	_, login, err := runAuthCommand(t, configHome, server.URL, "", true, "auth", "login", "--cookie", "session-cookie")
	if err != nil {
		t.Fatalf("auth login: %v", err)
	}
	if login.Summary != "Logged in with session cookie" {
		t.Errorf("login summary = %q", login.Summary)
	}
	method, ok := login.Data.(map[string]any)
	if !ok || method["method"] != "cookie" {
		t.Errorf("login data = %#v", login.Data)
	}

	_, status, err := runAuthCommand(t, configHome, server.URL, "", true, "auth", "status")
	if err != nil {
		t.Fatalf("auth status: %v", err)
	}
	statusData, ok := status.Data.(map[string]any)
	if !ok {
		t.Fatalf("status data = %T", status.Data)
	}
	if statusData["authenticated"] != true || statusData["auth_type"] != "cookie" || statusData["storage"] != "file" {
		t.Errorf("status data = %#v", statusData)
	}

	_, logout, err := runAuthCommand(t, configHome, server.URL, "", true, "auth", "logout")
	if err != nil {
		t.Fatalf("auth logout: %v", err)
	}
	if logout.Summary != "Logged out" {
		t.Errorf("logout summary = %q", logout.Summary)
	}

	_, status, err = runAuthCommand(t, configHome, server.URL, "", true, "auth", "status")
	if err != nil {
		t.Fatalf("auth status after logout: %v", err)
	}
	statusData, ok = status.Data.(map[string]any)
	if !ok || statusData["authenticated"] != false {
		t.Errorf("status after logout = %#v", status.Data)
	}
}

func TestAuthTokenLoginAndStoredTokenOutput(t *testing.T) {
	server := httptest.NewServer(http.NotFoundHandler())
	defer server.Close()
	configHome := t.TempDir()

	_, response, err := runAuthCommand(t, configHome, server.URL, "", true, "auth", "login", "--token", "stored-token")
	if err != nil {
		t.Fatalf("auth login: %v", err)
	}
	if response.Summary != "Logged in with token" {
		t.Errorf("summary = %q", response.Summary)
	}

	stdout, _, err := runAuthCommand(t, configHome, server.URL, "", false, "auth", "token", "--stored")
	if err != nil {
		t.Fatalf("auth token: %v", err)
	}
	if stdout != "stored-token" {
		t.Errorf("token output = %q", stdout)
	}
}

func TestAuthLoginRejectsConflictingMethods(t *testing.T) {
	server := httptest.NewServer(http.NotFoundHandler())
	defer server.Close()
	_, _, err := runAuthCommand(t, t.TempDir(), server.URL, "", true, "auth", "login", "--device", "--no-browser")
	if err == nil || !strings.Contains(err.Error(), "choose only one") {
		t.Fatalf("error = %v, want conflicting method error", err)
	}
}

// A session cookie is sent as a Cookie header, so printing it as a bearer token gave
// the caller something that 401s with nothing to explain it -- and put the cookie in
// their shell history.
func TestAuthTokenRefusesToPrintASessionCookie(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Errorf("unexpected HTTP request: %s %s", r.Method, r.URL.Path)
	}))
	defer server.Close()
	configHome := t.TempDir()

	if _, _, err := runAuthCommand(t, configHome, server.URL, "", true, "auth", "login", "--cookie", "session-cookie"); err != nil {
		t.Fatalf("auth login: %v", err)
	}

	stdout, _, err := runAuthCommand(t, configHome, server.URL, "", false, "auth", "token")
	if err == nil {
		t.Fatal("auth token printed a session cookie")
	}
	if !strings.Contains(err.Error(), "session cookie") {
		t.Errorf("error = %v", err)
	}
	if strings.Contains(stdout, "session-cookie") {
		t.Errorf("stdout = %q, the cookie must not be printed", stdout)
	}
}

func TestAuthStatusUsesEnvironmentTokenWithoutStorage(t *testing.T) {
	server := httptest.NewServer(http.NotFoundHandler())
	defer server.Close()

	_, response, err := runAuthCommand(t, t.TempDir(), server.URL, "environment-token", true, "auth", "status")
	if err != nil {
		t.Fatalf("auth status: %v", err)
	}
	data, ok := response.Data.(map[string]any)
	if !ok || data["authenticated"] != true || data["method"] != "env_var" {
		t.Errorf("status = %#v", response.Data)
	}
}

func TestAuthRefreshCommand(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/oauth/tokens" {
			t.Errorf("request = %s %s", r.Method, r.URL.Path)
		}
		if err := r.ParseForm(); err != nil {
			t.Fatalf("ParseForm: %v", err)
		}
		if got := r.Form.Get("refresh_token"); got != "old-refresh" {
			t.Errorf("refresh_token = %q", got)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"access_token":"new-access","refresh_token":"new-refresh","expires_in":3600}`)
	}))
	defer server.Close()
	configHome := t.TempDir()
	t.Setenv("HEY_NO_KEYRING", "1")
	manager := auth.NewManager(server.URL, server.Client(), filepath.Join(configHome, "hey-cli"))
	if err := manager.GetStore().Save(manager.CredentialKey(), &auth.Credentials{AccessToken: "old-access", RefreshToken: "old-refresh"}); err != nil {
		t.Fatalf("seed credentials: %v", err)
	}

	_, response, err := runAuthCommand(t, configHome, server.URL, "", true, "auth", "refresh")
	if err != nil {
		t.Fatalf("auth refresh: %v", err)
	}
	if response.Summary != "Token refreshed" {
		t.Errorf("summary = %q", response.Summary)
	}
	creds, err := manager.GetStore().Load(manager.CredentialKey())
	if err != nil {
		t.Fatalf("load refreshed credentials: %v", err)
	}
	if creds.AccessToken != "new-access" || creds.RefreshToken != "new-refresh" || creds.ExpiresAt <= time.Now().Unix() {
		t.Errorf("refreshed credentials = %#v", creds)
	}
}

func TestAuthRefreshFailure(t *testing.T) {
	server := httptest.NewServer(http.NotFoundHandler())
	defer server.Close()
	_, _, err := runAuthCommand(t, t.TempDir(), server.URL, "", true, "auth", "refresh")
	if err == nil || !strings.Contains(err.Error(), "refresh failed: not authenticated") {
		t.Fatalf("error = %v", err)
	}
}

func TestDoctorCommandReportsEnvironment(t *testing.T) {
	server := httptest.NewServer(http.NotFoundHandler())
	defer server.Close()
	configHome := t.TempDir()
	t.Setenv("SHELL", "/bin/zsh")
	skillPath := filepath.Join(configHome, ".agents", "skills", "hey", "SKILL.md")
	if err := os.MkdirAll(filepath.Dir(skillPath), 0755); err != nil {
		t.Fatalf("create skill directory: %v", err)
	}
	if err := os.WriteFile(skillPath, []byte("# HEY"), 0600); err != nil {
		t.Fatalf("write skill: %v", err)
	}
	writeOwnershipMarker(filepath.Dir(skillPath)) // a hey-cli-written install

	_, response, err := runAuthCommand(t, configHome, server.URL, "environment-token", true, "doctor")
	if err != nil {
		t.Fatalf("doctor: %v", err)
	}
	if response.Summary != "Doctor checks complete" {
		t.Errorf("summary = %q", response.Summary)
	}
	checks, ok := response.Data.([]any)
	if !ok {
		t.Fatalf("checks = %T", response.Data)
	}
	byName := make(map[string]map[string]any)
	for _, raw := range checks {
		check, ok := raw.(map[string]any)
		if !ok {
			t.Fatalf("check = %T", raw)
		}
		name, _ := check["name"].(string)
		byName[name] = check
	}
	if got := byName["Authentication"]["message"]; got != "Authenticated via HEY_TOKEN env var" {
		t.Errorf("authentication message = %q", got)
	}
	if got := byName["Credentials"]["status"]; got != "warning" {
		t.Errorf("credentials status = %q", got)
	}
	if got := byName["Shell"]["message"]; got != "/bin/zsh" {
		t.Errorf("shell = %q", got)
	}
	if got := byName["Agent Skill"]["status"]; got != "ok" {
		t.Errorf("Agent Skill status = %q", got)
	}
	if _, ok := byName["CLI Version"]; !ok {
		t.Error("CLI Version check missing")
	}
	if _, ok := byName["Go Version"]; !ok {
		t.Error("Go Version check missing")
	}
}

func TestDoctorCommandReportsMissingAuthentication(t *testing.T) {
	server := httptest.NewServer(http.NotFoundHandler())
	defer server.Close()
	_, response, err := runAuthCommand(t, t.TempDir(), server.URL, "", true, "doctor")
	if err != nil {
		t.Fatalf("doctor: %v", err)
	}
	checks, ok := response.Data.([]any)
	if !ok {
		t.Fatalf("checks = %T", response.Data)
	}
	for _, raw := range checks {
		check, _ := raw.(map[string]any)
		if check["name"] == "Authentication" {
			if check["status"] != "error" || !strings.Contains(check["message"].(string), "Not authenticated") {
				t.Errorf("authentication check = %#v", check)
			}
			return
		}
	}
	t.Fatal("Authentication check missing")
}

func TestLoginLogoutShortcutsMirrorAuthCommands(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Errorf("unexpected HTTP request: %s %s", r.Method, r.URL.Path)
	}))
	defer server.Close()
	configHome := t.TempDir()

	_, login, err := runAuthCommand(t, configHome, server.URL, "", true, "login", "--cookie", "session-cookie")
	if err != nil {
		t.Fatalf("login: %v", err)
	}
	if login.Summary != "Logged in with session cookie" {
		t.Errorf("login summary = %q", login.Summary)
	}

	_, status, err := runAuthCommand(t, configHome, server.URL, "", true, "auth", "status")
	if err != nil {
		t.Fatalf("auth status: %v", err)
	}
	if data, _ := status.Data.(map[string]any); data["authenticated"] != true {
		t.Errorf("status after hey login = %#v", status.Data)
	}

	_, logout, err := runAuthCommand(t, configHome, server.URL, "", true, "logout")
	if err != nil {
		t.Fatalf("logout: %v", err)
	}
	if logout.Summary != "Logged out" {
		t.Errorf("logout summary = %q", logout.Summary)
	}

	_, status, err = runAuthCommand(t, configHome, server.URL, "", true, "auth", "status")
	if err != nil {
		t.Fatalf("auth status: %v", err)
	}
	if data, _ := status.Data.(map[string]any); data["authenticated"] != false {
		t.Errorf("status after hey logout = %#v", status.Data)
	}

	root := newRootCmd()
	login2, _, err := root.Find([]string{"login"})
	if err != nil || !strings.Contains(login2.Example, "hey login --token") || strings.Contains(login2.Example, "hey auth login") {
		t.Errorf("hey login example = %q", login2.Example)
	}
}

func TestAuthStatusReportsInstallID(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Errorf("unexpected HTTP request: %s %s", r.Method, r.URL.Path)
	}))
	defer server.Close()
	configHome := t.TempDir()

	if _, _, err := runAuthCommand(t, configHome, server.URL, "", true, "auth", "login", "--cookie", "session-cookie"); err != nil {
		t.Fatalf("auth login: %v", err)
	}

	// JSON output carries install_id...
	installID := statusInstallID(t, configHome, server.URL, "")
	if installID == "" || strings.Count(installID, "-") != 4 {
		t.Fatalf("status install_id = %q, want a UUID", installID)
	}

	// ...and the styled output prints the matching Install: line.
	styled, _, err := runAuthCommand(t, configHome, server.URL, "", false, "auth", "status", "--styled")
	if err != nil {
		t.Fatalf("auth status (styled): %v", err)
	}
	if !strings.Contains(styled, "Install:   "+installID) {
		t.Errorf("styled status = %q, want an Install: line with %q", styled, installID)
	}

	// The identifier is install-scoped: it survives logout and shows when
	// authenticating through HEY_TOKEN, paths that both return before the
	// signed-in status output.
	if _, _, err := runAuthCommand(t, configHome, server.URL, "", true, "auth", "logout"); err != nil {
		t.Fatalf("auth logout: %v", err)
	}
	if after := statusInstallID(t, configHome, server.URL, ""); after != installID {
		t.Errorf("install_id after logout = %q, want %q", after, installID)
	}
	if env := statusInstallID(t, configHome, server.URL, "environment-token"); env != installID {
		t.Errorf("install_id with HEY_TOKEN = %q, want %q", env, installID)
	}
}

func statusInstallID(t *testing.T, configHome, baseURL, envToken string) string {
	t.Helper()
	_, status, err := runAuthCommand(t, configHome, baseURL, envToken, true, "auth", "status")
	if err != nil {
		t.Fatalf("auth status: %v", err)
	}
	data, ok := status.Data.(map[string]any)
	if !ok {
		t.Fatalf("status data = %T", status.Data)
	}
	id, _ := data["install_id"].(string)
	return id
}
