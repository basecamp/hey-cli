package cmd

import (
	"bytes"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/basecamp/hey-cli/internal/auth"
	"github.com/basecamp/hey-cli/internal/config"
)

// fakeRevocation is HEY's DELETE /oauth/tokens, answering status and recording each form.
type fakeRevocation struct {
	t           *testing.T
	status      int
	mu          sync.Mutex
	revocations []url.Values
}

func (f *fakeRevocation) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodDelete || r.URL.Path != "/oauth/tokens" {
		f.t.Errorf("unexpected HTTP request: %s %s", r.Method, r.URL.Path)
		http.NotFound(w, r)
		return
	}
	raw, _ := io.ReadAll(r.Body)
	form, _ := url.ParseQuery(string(raw))
	f.mu.Lock()
	f.revocations = append(f.revocations, form)
	f.mu.Unlock()
	w.WriteHeader(f.status)
}

// storeCLIOAuthCredential stores the CLI's own OAuth sign-in for baseURL, as `hey auth
// login` would. The environment must already point at the test's config home.
func storeCLIOAuthCredential(t *testing.T, baseURL string) {
	t.Helper()
	storeCLICredential(t, baseURL, &auth.Credentials{
		AccessToken: "cli-access", RefreshToken: "cli-refresh", OAuthType: "oauth",
		ExpiresAt: time.Now().Add(time.Hour).Unix(), TokenEndpoint: baseURL + "/oauth/tokens",
	})
}

func storeCLICredential(t *testing.T, baseURL string, creds *auth.Credentials) {
	t.Helper()
	mgr := auth.NewManager(baseURL, http.DefaultClient, config.ConfigDir())
	if err := mgr.GetStore().Save(mgr.CredentialKey(), creds); err != nil {
		t.Fatal(err)
	}
}

// runLogout runs `hey auth logout --json` in the environment a prior runAuthCommand set,
// returning stdout and stderr.
func runLogout(t *testing.T, baseURL string) (string, string, error) {
	t.Helper()
	t.Setenv("HEY_TOKEN", "")
	root := newRootCmd()
	var stdout, stderr bytes.Buffer
	root.SetOut(&stdout)
	root.SetErr(&stderr)
	root.SetArgs([]string{"--base-url", baseURL, "--json", "auth", "logout"})
	err := root.Execute()
	return stdout.String(), stderr.String(), err
}

func signedOut(t *testing.T, configHome, baseURL string) bool {
	t.Helper()
	_, status, err := runAuthCommand(t, configHome, baseURL, "", true, "auth", "status")
	if err != nil {
		t.Fatalf("auth status: %v", err)
	}
	data, _ := status.Data.(map[string]any)
	return data["authenticated"] == false
}

func TestLogoutRevokesTheCLIsSignInAtHEY(t *testing.T) {
	hey := &fakeRevocation{t: t, status: http.StatusOK}
	server := httptest.NewServer(hey)
	defer server.Close()
	configHome := t.TempDir()
	if !signedOut(t, configHome, server.URL) {
		t.Fatal("signed in before the test stored a credential")
	}
	storeCLIOAuthCredential(t, server.URL)

	_, logout, err := runAuthCommand(t, configHome, server.URL, "", true, "auth", "logout")
	if err != nil {
		t.Fatalf("auth logout: %v", err)
	}
	if logout.Summary != "Logged out" {
		t.Errorf("logout summary = %q", logout.Summary)
	}
	if data, _ := logout.Data.(map[string]any); data["revoked"] != true {
		t.Errorf("logout data = %v, want revoked", logout.Data)
	}
	if len(hey.revocations) != 1 {
		t.Fatalf("revocations = %d, want 1", len(hey.revocations))
	}
	if got := hey.revocations[0]; got.Get("client_id") != "khMWSVDVSq78oyKA3KtxmYRv" || got.Get("token") != "cli-refresh" {
		t.Errorf("revocation = client %q, token is the refresh token: %v", got.Get("client_id"), got.Get("token") == "cli-refresh")
	}
	if !signedOut(t, configHome, server.URL) {
		t.Error("still signed in after logout")
	}
}

// A HEY that can't confirm the revocation (an outage, a HEY without OAuth revocation)
// still gets the CLI signed out here, with a warning that says where to finish.
func TestLogoutClearsTheCredentialWhenTheRevocationFails(t *testing.T) {
	for _, status := range []int{http.StatusInternalServerError, http.StatusNotFound} {
		t.Run(http.StatusText(status), func(t *testing.T) {
			hey := &fakeRevocation{t: t, status: status}
			server := httptest.NewServer(hey)
			defer server.Close()
			configHome := t.TempDir()
			signedOut(t, configHome, server.URL)
			storeCLIOAuthCredential(t, server.URL)

			stdout, stderr, err := runLogout(t, server.URL)
			if err != nil {
				t.Fatalf("auth logout: %v", err)
			}
			if !strings.Contains(stdout, `"revoked": false`) && !strings.Contains(stdout, `"revoked":false`) {
				t.Errorf("logout output = %s, want revoked false", stdout)
			}
			if !strings.Contains(stderr, "couldn't revoke the CLI's sign-in at HEY") || !strings.Contains(stderr, "Connected Apps") {
				t.Errorf("stderr = %q, want the revocation warning", stderr)
			}
			if strings.Contains(stdout+stderr, "cli-refresh") || strings.Contains(stdout+stderr, "cli-access") {
				t.Error("logout printed a token")
			}
			if !signedOut(t, configHome, server.URL) {
				t.Error("still signed in after a logout whose revocation failed")
			}
		})
	}
}

// A token stored with --token isn't the CLI's own sign-in: logout forgets it and asks HEY
// nothing (the session cookie case is TestAuthCookieLoginStatusAndLogout).
func TestLogoutDoesNotRevokeAStoredToken(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Errorf("unexpected HTTP request: %s %s", r.Method, r.URL.Path)
	}))
	defer server.Close()
	configHome := t.TempDir()

	if _, _, err := runAuthCommand(t, configHome, server.URL, "", true, "auth", "login", "--token", "stored-token"); err != nil {
		t.Fatalf("auth login: %v", err)
	}
	_, logout, err := runAuthCommand(t, configHome, server.URL, "", true, "auth", "logout")
	if err != nil {
		t.Fatalf("auth logout: %v", err)
	}
	if data, _ := logout.Data.(map[string]any); data["revoked"] != false {
		t.Errorf("logout data = %v, want revoked false", logout.Data)
	}
	if !signedOut(t, configHome, server.URL) {
		t.Error("still signed in after logout")
	}
}

func TestLogoutWhenSignedOutAsksHEYNothing(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Errorf("unexpected HTTP request: %s %s", r.Method, r.URL.Path)
	}))
	defer server.Close()
	configHome := t.TempDir()

	_, logout, err := runAuthCommand(t, configHome, server.URL, "", true, "auth", "logout")
	if err != nil {
		t.Fatalf("auth logout: %v", err)
	}
	if logout.Summary != "Logged out" {
		t.Errorf("logout summary = %q", logout.Summary)
	}
}

// Credentials migrated from the old config.json carry no OAuthType, and refreshes keep
// it empty; they're the CLI's own sign-in all the same. The revocation goes to the
// endpoint the credential refreshes at.
func TestLogoutRevokesAMigratedSignIn(t *testing.T) {
	hey := &fakeRevocation{t: t, status: http.StatusOK}
	server := httptest.NewServer(hey)
	defer server.Close()
	configHome := t.TempDir()
	signedOut(t, configHome, server.URL)
	storeCLICredential(t, server.URL, &auth.Credentials{AccessToken: "cli-access", RefreshToken: "cli-refresh"})

	_, logout, err := runAuthCommand(t, configHome, server.URL, "", true, "auth", "logout")
	if err != nil {
		t.Fatalf("auth logout: %v", err)
	}
	if data, _ := logout.Data.(map[string]any); data["revoked"] != true {
		t.Errorf("logout data = %v, want revoked", logout.Data)
	}
	if len(hey.revocations) != 1 || hey.revocations[0].Get("token") != "cli-refresh" {
		t.Errorf("revocations = %d, want the migrated refresh token once", len(hey.revocations))
	}
}

// An OAuth record holding no token has nothing to revoke: no request, no warning, and
// revoked false rather than a claim that something was revoked.
func TestLogoutOfAnEmptySignInRevokesNothing(t *testing.T) {
	hey := &fakeRevocation{t: t, status: http.StatusOK}
	server := httptest.NewServer(hey)
	defer server.Close()
	configHome := t.TempDir()
	signedOut(t, configHome, server.URL)
	storeCLICredential(t, server.URL, &auth.Credentials{OAuthType: "oauth"})

	stdout, stderr, err := runLogout(t, server.URL)
	if err != nil {
		t.Fatalf("auth logout: %v", err)
	}
	if len(hey.revocations) != 0 {
		t.Errorf("revocations = %d, want none", len(hey.revocations))
	}
	if !strings.Contains(stdout, `"revoked": false`) && !strings.Contains(stdout, `"revoked":false`) {
		t.Errorf("logout output = %s, want revoked false", stdout)
	}
	if strings.Contains(stderr, "couldn't revoke") {
		t.Errorf("stderr = %q, want no revocation warning", stderr)
	}
}

// HEY_TOKEN outlives the logout: the answer says the CLI is still signed in with it.
func TestLogoutWarnsThatHEYTokenStaysInUse(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Errorf("unexpected HTTP request: %s %s", r.Method, r.URL.Path)
	}))
	defer server.Close()
	configHome := t.TempDir()
	signedOut(t, configHome, server.URL)
	t.Setenv("HEY_TOKEN", "environment-token")

	root := newRootCmd()
	var stdout, stderr bytes.Buffer
	root.SetOut(&stdout)
	root.SetErr(&stderr)
	root.SetArgs([]string{"--base-url", server.URL, "--json", "auth", "logout"})
	if err := root.Execute(); err != nil {
		t.Fatalf("auth logout: %v", err)
	}
	if !strings.Contains(stdout.String(), "env_token_active") {
		t.Errorf("logout output = %s, want env_token_active", stdout.String())
	}
	if !strings.Contains(stderr.String(), "HEY_TOKEN is set") {
		t.Errorf("stderr = %q, want the HEY_TOKEN warning", stderr.String())
	}
}
