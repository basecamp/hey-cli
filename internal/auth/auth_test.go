package auth

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"strings"
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

func TestLoginOAuthFlow(t *testing.T) {
	redirectURIs := make(chan string, 1)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/oauth/tokens" {
			t.Errorf("path = %q, want /oauth/tokens", r.URL.Path)
		}
		if err := r.ParseForm(); err != nil {
			t.Fatalf("ParseForm: %v", err)
		}
		if got := r.Form.Get("code"); got != "callback-code" {
			t.Errorf("code = %q, want callback-code", got)
		}
		if got, want := r.Form.Get("redirect_uri"), <-redirectURIs; got != want {
			t.Errorf("redirect_uri = %q, want the callback listener's %q", got, want)
		}
		if got := r.Form.Get("code_verifier"); got == "" {
			t.Error("code_verifier is empty")
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"access_token":"oauth-access","refresh_token":"oauth-refresh","expires_in":3600}`)
	}))
	defer server.Close()

	t.Setenv("HEY_TOKEN", "")
	mgr := testManager(t, server)
	listen := mgr.listen
	mgr.listen = func(ctx context.Context, network, address string) (net.Listener, error) {
		if network != "tcp" || address != "127.0.0.1:0" {
			t.Errorf("listen arguments = %q, %q, want an ephemeral loopback port", network, address)
		}
		return listen(ctx, network, address)
	}
	mgr.callbackWait = func(_ context.Context, state, authURL string, listener net.Listener, opts LoginOptions) (string, error) {
		if state == "" {
			t.Error("state is empty")
		}
		if !opts.NoBrowser {
			t.Error("NoBrowser = false, want true")
		}
		addr, ok := listener.Addr().(*net.TCPAddr)
		if !ok || !addr.IP.Equal(net.IPv4(127, 0, 0, 1)) || addr.Port == 0 {
			t.Fatalf("listener address = %v, want a bound 127.0.0.1 port", listener.Addr())
		}
		redirectURI := "http://" + addr.String() + "/callback"
		redirectURIs <- redirectURI
		u, err := url.Parse(authURL)
		if err != nil {
			t.Fatalf("Parse auth URL: %v", err)
		}
		if u.Path != "/oauth/authorizations/new" {
			t.Errorf("auth path = %q", u.Path)
		}
		query := u.Query()
		want := map[string]string{
			"client_id":             oauthClientID,
			"grant_type":            "authorization_code",
			"redirect_uri":          redirectURI,
			"state":                 state,
			"code_challenge_method": "S256",
			"install_id":            installID,
		}
		for key, value := range want {
			if got := query.Get(key); got != value {
				t.Errorf("auth query %s = %q, want %q", key, got, value)
			}
		}
		if query.Get("code_challenge") == "" {
			t.Error("code_challenge is empty")
		}
		return "callback-code", nil
	}

	if err := mgr.Login(t.Context(), LoginOptions{NoBrowser: true}); err != nil {
		t.Fatalf("Login: %v", err)
	}
	creds, err := mgr.GetStore().Load(mgr.CredentialKey())
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if creds.AccessToken != "oauth-access" || creds.RefreshToken != "oauth-refresh" {
		t.Errorf("credentials = %#v", creds)
	}
	if creds.OAuthType != "oauth" || creds.TokenEndpoint != server.URL+"/oauth/tokens" {
		t.Errorf("OAuth metadata = %#v", creds)
	}
	if creds.ExpiresAt <= time.Now().Unix() {
		t.Errorf("ExpiresAt = %d, want future expiry", creds.ExpiresAt)
	}
}

func TestLoginDoesNotSaveCredentialsOnFailure(t *testing.T) {
	tests := []struct {
		name       string
		waitErr    error
		statusCode int
		want       string
		wantCalls  int
	}{
		{name: "callback failure", waitErr: errors.New("state mismatch"), want: "state mismatch"},
		{name: "exchange failure", statusCode: http.StatusUnauthorized, want: "token exchange failed", wantCalls: 1},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			calls := 0
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls++
				w.WriteHeader(tt.statusCode)
				_, _ = io.WriteString(w, "denied")
			}))
			defer server.Close()

			t.Setenv("HEY_TOKEN", "")
			mgr := testManager(t, server)
			mgr.callbackWait = func(context.Context, string, string, net.Listener, LoginOptions) (string, error) {
				return "callback-code", tt.waitErr
			}
			err := mgr.Login(t.Context(), LoginOptions{})
			if err == nil || !strings.Contains(err.Error(), tt.want) {
				t.Fatalf("error = %v, want substring %q", err, tt.want)
			}
			if calls != tt.wantCalls {
				t.Errorf("requests = %d, want %d", calls, tt.wantCalls)
			}
			if _, err := mgr.GetStore().Load(mgr.CredentialKey()); err == nil {
				t.Fatal("credentials were saved after failed login")
			}
		})
	}
}

func TestWaitForCallback(t *testing.T) {
	tests := []struct {
		name      string
		query     string
		wantCode  string
		wantError string
		wantBody  string
	}{
		{name: "success", query: "?state=expected&code=authorization-code", wantCode: "authorization-code", wantBody: "Authorization successful"},
		{name: "access denied", query: "?state=expected&error=access_denied", wantError: "OAuth error: access_denied", wantBody: "You denied access"},
		{name: "OAuth error", query: "?state=expected&error=server_error", wantError: "OAuth error: server_error", wantBody: "Authorization failed"},
		{name: "missing code", query: "?state=expected", wantError: "missing authorization code", wantBody: "Authorization failed"},
		{name: "state mismatch", query: "?state=wrong&code=authorization-code", wantError: "state mismatch", wantBody: "authorization link is invalid"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			listenConfig := &net.ListenConfig{}
			listener, err := listenConfig.Listen(t.Context(), "tcp", "127.0.0.1:0")
			if err != nil {
				t.Fatalf("Listen: %v", err)
			}
			t.Cleanup(func() { _ = listener.Close() })
			mgr := NewManager("http://example.test", http.DefaultClient, t.TempDir())
			type result struct {
				code string
				err  error
			}
			resultCh := make(chan result, 1)
			go func() {
				code, waitErr := mgr.waitForCallback(t.Context(), "expected", "http://example.test/authorize", listener, LoginOptions{NoBrowser: true})
				resultCh <- result{code: code, err: waitErr}
			}()

			client := &http.Client{Timeout: 2 * time.Second}
			response, err := client.Get("http://" + listener.Addr().String() + "/callback" + tt.query) //nolint:noctx // local one-shot test request
			if err != nil {
				t.Fatalf("GET callback: %v", err)
			}
			body, err := io.ReadAll(response.Body)
			_ = response.Body.Close()
			if err != nil {
				t.Fatalf("reading callback response: %v", err)
			}
			if !strings.Contains(string(body), tt.wantBody) {
				t.Errorf("response body does not contain %q", tt.wantBody)
			}
			var got result
			select {
			case got = <-resultCh:
			case <-time.After(2 * time.Second):
				t.Fatal("waitForCallback did not return")
			}
			if got.code != tt.wantCode {
				t.Errorf("code = %q, want %q", got.code, tt.wantCode)
			}
			if tt.wantError == "" && got.err != nil {
				t.Fatalf("waitForCallback: %v", got.err)
			}
			if tt.wantError != "" && (got.err == nil || !strings.Contains(got.err.Error(), tt.wantError)) {
				t.Fatalf("error = %v, want substring %q", got.err, tt.wantError)
			}
		})
	}
}

func TestLoginListenFailure(t *testing.T) {
	t.Setenv("HEY_TOKEN", "")
	mgr := NewManager("http://example.test", http.DefaultClient, t.TempDir())
	mgr.listen = func(context.Context, string, string) (net.Listener, error) {
		return nil, errors.New("address unavailable")
	}
	mgr.callbackWait = func(context.Context, string, string, net.Listener, LoginOptions) (string, error) {
		t.Fatal("callback wait ran without a listener")
		return "", nil
	}
	err := mgr.Login(t.Context(), LoginOptions{NoBrowser: true})
	if err == nil || !strings.Contains(err.Error(), "failed to start callback server") {
		t.Fatalf("error = %v", err)
	}
}

func TestWaitForCallbackContextCancellation(t *testing.T) {
	listenConfig := &net.ListenConfig{}
	listener, err := listenConfig.Listen(t.Context(), "tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("Listen: %v", err)
	}
	t.Cleanup(func() { _ = listener.Close() })
	mgr := NewManager("http://example.test", http.DefaultClient, t.TempDir())
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err = mgr.waitForCallback(ctx, "state", "auth-url", listener, LoginOptions{NoBrowser: true})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("error = %v, want context.Canceled", err)
	}
}

func TestLoginWithCookieAuthenticateAndLogout(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Fatal("authentication should not make an HTTP request")
	}))
	defer server.Close()

	t.Setenv("HEY_TOKEN", "")
	mgr := testManager(t, server)
	if err := mgr.LoginWithCookie("cookie-value"); err != nil {
		t.Fatalf("LoginWithCookie: %v", err)
	}
	if !mgr.IsAuthenticated() {
		t.Fatal("IsAuthenticated = false after cookie login")
	}

	req := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "http://example.test/messages", nil)
	if err := mgr.AuthenticateRequest(t.Context(), req); err != nil {
		t.Fatalf("AuthenticateRequest: %v", err)
	}
	if got := req.Header.Get("Cookie"); got != "session_token=cookie-value" {
		t.Errorf("Cookie = %q, want session_token cookie", got)
	}
	if got := req.Header.Get("Authorization"); got != "" {
		t.Errorf("Authorization = %q, want empty for cookie auth", got)
	}

	token, err := mgr.AccessToken(t.Context())
	if err != nil {
		t.Fatalf("AccessToken: %v", err)
	}
	if token != "cookie-value" {
		t.Errorf("AccessToken = %q, want cookie fallback", token)
	}
	if err := mgr.Refresh(t.Context()); err != nil {
		t.Fatalf("cookie Refresh: %v", err)
	}
	if err := mgr.Logout(); err != nil {
		t.Fatalf("Logout: %v", err)
	}
	if mgr.IsAuthenticated() {
		t.Fatal("IsAuthenticated = true after logout")
	}
}

func TestAuthenticateRequestBearerPrecedence(t *testing.T) {
	server := httptest.NewServer(http.NotFoundHandler())
	defer server.Close()

	t.Run("environment", func(t *testing.T) {
		t.Setenv("HEY_TOKEN", "environment-token")
		mgr := testManager(t, server)
		if err := mgr.LoginWithCookie("stored-cookie"); err != nil {
			t.Fatalf("LoginWithCookie: %v", err)
		}
		req := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "http://example.test", nil)
		if err := mgr.AuthenticateRequest(t.Context(), req); err != nil {
			t.Fatalf("AuthenticateRequest: %v", err)
		}
		if got := req.Header.Get("Authorization"); got != "Bearer environment-token" {
			t.Errorf("Authorization = %q", got)
		}
		if got := req.Header.Get("Cookie"); got != "" {
			t.Errorf("Cookie = %q, want empty", got)
		}
	})

	t.Run("stored access token", func(t *testing.T) {
		t.Setenv("HEY_TOKEN", "")
		mgr := testManager(t, server)
		creds := &Credentials{AccessToken: "stored-token", SessionCookie: "stored-cookie"}
		if err := mgr.GetStore().Save(mgr.CredentialKey(), creds); err != nil {
			t.Fatalf("Save: %v", err)
		}
		req := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "http://example.test", nil)
		if err := mgr.AuthenticateRequest(t.Context(), req); err != nil {
			t.Fatalf("AuthenticateRequest: %v", err)
		}
		if got := req.Header.Get("Authorization"); got != "Bearer stored-token" {
			t.Errorf("Authorization = %q", got)
		}
		if got := req.Header.Get("Cookie"); got != "" {
			t.Errorf("Cookie = %q, want empty when bearer is available", got)
		}
	})
}

func TestMissingCredentialsDoNotModifyRequest(t *testing.T) {
	server := httptest.NewServer(http.NotFoundHandler())
	defer server.Close()

	tests := []struct {
		name  string
		creds *Credentials
		want  string
	}{
		{name: "not logged in", want: "not authenticated"},
		{name: "empty credentials", creds: &Credentials{}, want: "no access token or session cookie"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Setenv("HEY_TOKEN", "")
			mgr := testManager(t, server)
			if tt.creds != nil {
				if err := mgr.GetStore().Save(mgr.CredentialKey(), tt.creds); err != nil {
					t.Fatalf("Save: %v", err)
				}
			}
			req := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "http://example.test", nil)
			req.Header.Set("Authorization", "original")
			err := mgr.AuthenticateRequest(t.Context(), req)
			if err == nil || !strings.Contains(err.Error(), tt.want) {
				t.Fatalf("error = %v, want substring %q", err, tt.want)
			}
			if got := req.Header.Get("Authorization"); got != "original" {
				t.Errorf("Authorization = %q, want original header preserved", got)
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

func TestAuthenticateRequestRefreshesAndPreservesRefreshToken(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/oauth/tokens" {
			t.Errorf("path = %q, want /oauth/tokens", r.URL.Path)
		}
		if err := r.ParseForm(); err != nil {
			t.Fatalf("ParseForm: %v", err)
		}
		if got := r.Form.Get("refresh_token"); got != "keep-refresh" {
			t.Errorf("refresh_token = %q, want keep-refresh", got)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = fmt.Fprint(w, `{"access_token":"fresh-access","expires_in":7200}`)
	}))
	defer server.Close()

	t.Setenv("HEY_TOKEN", "")
	mgr := testManager(t, server)
	creds := &Credentials{
		AccessToken:  "expired-access",
		RefreshToken: "keep-refresh",
		ExpiresAt:    time.Now().Add(-time.Hour).Unix(),
	}
	if err := mgr.GetStore().Save(mgr.CredentialKey(), creds); err != nil {
		t.Fatalf("Save: %v", err)
	}

	req := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "http://example.test", nil)
	if err := mgr.AuthenticateRequest(t.Context(), req); err != nil {
		t.Fatalf("AuthenticateRequest: %v", err)
	}
	if got := req.Header.Get("Authorization"); got != "Bearer fresh-access" {
		t.Errorf("Authorization = %q", got)
	}
	stored, err := mgr.GetStore().Load(mgr.CredentialKey())
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if stored.RefreshToken != "keep-refresh" {
		t.Errorf("RefreshToken = %q, want preserved token", stored.RefreshToken)
	}
	if stored.ExpiresAt <= time.Now().Unix() {
		t.Errorf("ExpiresAt = %d, want future expiry", stored.ExpiresAt)
	}
}

func TestRefreshUsesStoredEndpointAndRotatesToken(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/custom-refresh" {
			t.Errorf("path = %q, want /custom-refresh", r.URL.Path)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = fmt.Fprint(w, `{"access_token":"fresh-access","refresh_token":"rotated-refresh","expires_in":3600}`)
	}))
	defer server.Close()

	t.Setenv("HEY_TOKEN", "")
	mgr := testManager(t, server)
	creds := &Credentials{
		AccessToken:   "old-access",
		RefreshToken:  "old-refresh",
		TokenEndpoint: server.URL + "/custom-refresh",
	}
	if err := mgr.GetStore().Save(mgr.CredentialKey(), creds); err != nil {
		t.Fatalf("Save: %v", err)
	}
	if err := mgr.Refresh(t.Context()); err != nil {
		t.Fatalf("Refresh: %v", err)
	}
	stored, err := mgr.GetStore().Load(mgr.CredentialKey())
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if stored.AccessToken != "fresh-access" || stored.RefreshToken != "rotated-refresh" {
		t.Errorf("stored credentials = %#v", stored)
	}
}

func TestRefreshFailuresPreserveCredentials(t *testing.T) {
	tests := []struct {
		name      string
		creds     *Credentials
		status    int
		want      string
		wantCalls int
	}{
		{name: "not authenticated", want: "not authenticated", wantCalls: 0},
		{name: "no refresh token", creds: &Credentials{AccessToken: "access"}, want: "no refresh token", wantCalls: 0},
		{name: "server failure", creds: &Credentials{AccessToken: "access", RefreshToken: "refresh"}, status: http.StatusUnauthorized, want: "token refresh failed", wantCalls: 1},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			calls := 0
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls++
				w.WriteHeader(tt.status)
				_, _ = fmt.Fprint(w, "denied")
			}))
			defer server.Close()

			t.Setenv("HEY_TOKEN", "")
			mgr := testManager(t, server)
			if tt.creds != nil {
				if err := mgr.GetStore().Save(mgr.CredentialKey(), tt.creds); err != nil {
					t.Fatalf("Save: %v", err)
				}
			}
			err := mgr.Refresh(t.Context())
			if err == nil || !strings.Contains(err.Error(), tt.want) {
				t.Fatalf("error = %v, want substring %q", err, tt.want)
			}
			if calls != tt.wantCalls {
				t.Errorf("requests = %d, want %d", calls, tt.wantCalls)
			}
			if tt.creds != nil {
				stored, loadErr := mgr.GetStore().Load(mgr.CredentialKey())
				if loadErr != nil {
					t.Fatalf("Load: %v", loadErr)
				}
				if stored.AccessToken != tt.creds.AccessToken || stored.RefreshToken != tt.creds.RefreshToken {
					t.Errorf("credentials changed after failed refresh: %#v", stored)
				}
			}
		})
	}
}

func TestRefreshWithoutAnAccessTokenKeepsTheWorkingOne(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = fmt.Fprint(w, `{"refresh_token":"rotated-refresh","expires_in":3600}`)
	}))
	defer server.Close()

	t.Setenv("HEY_TOKEN", "")
	mgr := testManager(t, server)
	if err := mgr.GetStore().Save(mgr.CredentialKey(), &Credentials{AccessToken: "working-access", RefreshToken: "old-refresh"}); err != nil {
		t.Fatalf("Save: %v", err)
	}

	err := mgr.Refresh(t.Context())
	if err == nil || !strings.Contains(err.Error(), "no access token") {
		t.Fatalf("error = %v, want a refresh without an access token reported", err)
	}

	stored, err := mgr.GetStore().Load(mgr.CredentialKey())
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if stored.AccessToken != "working-access" || stored.RefreshToken != "old-refresh" {
		t.Errorf("stored credentials = %#v, want the working ones left alone", stored)
	}
	if !mgr.IsAuthenticated() {
		t.Error("a refresh that answered nothing left the CLI unauthenticated")
	}
}

func TestRefreshForgetsAnExpiryTheServerStopsSending(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = fmt.Fprint(w, `{"access_token":"fresh-access","refresh_token":"rotated-refresh"}`)
	}))
	defer server.Close()

	t.Setenv("HEY_TOKEN", "")
	mgr := testManager(t, server)
	creds := &Credentials{
		AccessToken:  "expired-access",
		RefreshToken: "old-refresh",
		ExpiresAt:    time.Now().Add(-time.Hour).Unix(),
	}
	if err := mgr.GetStore().Save(mgr.CredentialKey(), creds); err != nil {
		t.Fatalf("Save: %v", err)
	}

	if err := mgr.Refresh(t.Context()); err != nil {
		t.Fatalf("Refresh: %v", err)
	}

	stored, err := mgr.GetStore().Load(mgr.CredentialKey())
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if stored.ExpiresAt != 0 {
		t.Errorf("ExpiresAt = %d, want no expiry rather than the one it just replaced", stored.ExpiresAt)
	}
}

func TestRefreshAdoptsATokenAnotherProcessStored(t *testing.T) {
	calls := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		w.Header().Set("Content-Type", "application/json")
		_, _ = fmt.Fprint(w, `{"access_token":"ours","refresh_token":"rotated-refresh","expires_in":3600}`)
	}))
	defer server.Close()

	t.Setenv("HEY_TOKEN", "")
	mgr := testManager(t, server)
	if err := mgr.GetStore().Save(mgr.CredentialKey(), &Credentials{AccessToken: "elsewhere", RefreshToken: "rotated-refresh"}); err != nil {
		t.Fatalf("Save: %v", err)
	}

	stale := &Credentials{AccessToken: "waited-with-this", RefreshToken: "consumed-refresh"}
	if err := mgr.refreshLocked(t.Context(), stale); err != nil {
		t.Fatalf("refreshLocked: %v", err)
	}

	if calls != 0 {
		t.Errorf("refresh requests = %d, want none once another process had already refreshed", calls)
	}
	stored, err := mgr.GetStore().Load(mgr.CredentialKey())
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if stored.AccessToken != "elsewhere" {
		t.Errorf("stored access token = %q, want the one the other process left", stored.AccessToken)
	}
}

func TestConcurrentManagersRefreshOnce(t *testing.T) {
	calls := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if err := r.ParseForm(); err != nil {
			t.Fatalf("ParseForm: %v", err)
		}
		// Rotation: the refresh token is spent by the first refresh that presents it.
		if r.Form.Get("refresh_token") != "first-refresh" {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		calls++
		w.Header().Set("Content-Type", "application/json")
		_, _ = fmt.Fprint(w, `{"access_token":"fresh-access","refresh_token":"second-refresh","expires_in":3600}`)
	}))
	defer server.Close()

	t.Setenv("HEY_TOKEN", "")
	t.Setenv("HEY_NO_KEYRING", "1")
	configDir := t.TempDir()
	watching := NewManager(server.URL, server.Client(), configDir)
	reading := NewManager(server.URL, server.Client(), configDir)

	expired := &Credentials{
		AccessToken:  "expired-access",
		RefreshToken: "first-refresh",
		ExpiresAt:    time.Now().Add(-time.Hour).Unix(),
	}
	if err := watching.GetStore().Save(watching.CredentialKey(), expired); err != nil {
		t.Fatalf("Save: %v", err)
	}

	tokens := make(chan string, 2)
	failures := make(chan error, 2)
	for _, mgr := range []*Manager{watching, reading} {
		go func() {
			token, err := mgr.AccessToken(t.Context())
			if err != nil {
				failures <- err
				return
			}
			tokens <- token
		}()
	}

	for range 2 {
		select {
		case err := <-failures:
			t.Fatalf("AccessToken: %v", err)
		case token := <-tokens:
			if token != "fresh-access" {
				t.Errorf("token = %q, want both processes on the refreshed token", token)
			}
		}
	}
	if calls != 1 {
		t.Errorf("refresh requests = %d, want one for the two processes sharing the credentials", calls)
	}
}

func TestLoginOptionsLoggerReceivesProgress(t *testing.T) {
	listenConfig := &net.ListenConfig{}
	listener, err := listenConfig.Listen(t.Context(), "tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("Listen: %v", err)
	}
	t.Cleanup(func() { _ = listener.Close() })
	mgr := NewManager("http://example.test", http.DefaultClient, t.TempDir())

	// Capture stderr: a configured Logger must own all progress output.
	origStderr := os.Stderr
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	os.Stderr = w
	t.Cleanup(func() { os.Stderr = origStderr })

	var logged []string
	opts := LoginOptions{NoBrowser: true, Logger: func(msg string) { logged = append(logged, msg) }}

	done := make(chan struct{})
	go func() {
		defer close(done)
		_, _ = mgr.waitForCallback(t.Context(), "expected", "http://example.test/authorize", listener, opts)
	}()

	client := &http.Client{Timeout: 2 * time.Second}
	response, err := client.Get("http://" + listener.Addr().String() + "/callback?state=expected&code=abc123") //nolint:noctx // local one-shot test request
	if err != nil {
		t.Fatalf("GET callback: %v", err)
	}
	_ = response.Body.Close()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("waitForCallback did not return")
	}

	_ = w.Close()
	os.Stderr = origStderr
	captured, _ := io.ReadAll(r)

	all := strings.Join(logged, "\n")
	if !strings.Contains(all, "http://example.test/authorize") {
		t.Errorf("Logger did not receive the authorization URL: %q", all)
	}
	if !strings.Contains(all, "Waiting for authentication") {
		t.Errorf("Logger did not receive the waiting notice: %q", all)
	}
	if len(captured) != 0 {
		t.Errorf("stderr should stay silent when a Logger is set, got %q", captured)
	}
}
