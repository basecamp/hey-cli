package auth

import (
	"context"
	"encoding/base64"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"
)

func TestExchangeCodeRequest(t *testing.T) {
	before := time.Now()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			t.Errorf("method = %s, want POST", r.Method)
		}
		if got := r.Header.Get("Content-Type"); got != "application/x-www-form-urlencoded" {
			t.Errorf("Content-Type = %q, want form encoding", got)
		}
		if err := r.ParseForm(); err != nil {
			t.Fatalf("ParseForm: %v", err)
		}
		want := url.Values{
			"grant_type":    {"authorization_code"},
			"client_id":     {"client-id"},
			"code":          {"authorization-code"},
			"redirect_uri":  {"http://127.0.0.1/callback"},
			"code_verifier": {"verifier"},
			"install_id":    {"installation"},
		}
		for key, values := range want {
			if got := r.Form[key]; len(got) != 1 || got[0] != values[0] {
				t.Errorf("form[%q] = %q, want %q", key, got, values)
			}
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"access_token":"access","refresh_token":"refresh","token_type":"Bearer","expires_in":3600}`)
	}))
	defer server.Close()

	token, err := exchangeCode(t.Context(), server.Client(), server.URL, "authorization-code", "http://127.0.0.1/callback", "client-id", "verifier", "installation")
	if err != nil {
		t.Fatalf("exchangeCode: %v", err)
	}
	if token.AccessToken != "access" || token.RefreshToken != "refresh" || token.TokenType != "Bearer" {
		t.Errorf("token = %#v", token)
	}
	if token.ExpiresAt.Before(before.Add(3599*time.Second)) || token.ExpiresAt.After(time.Now().Add(3601*time.Second)) {
		t.Errorf("ExpiresAt = %s, want about one hour from now", token.ExpiresAt)
	}
}

func TestRefreshOAuthTokenRequest(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			t.Errorf("method = %s, want POST", r.Method)
		}
		if err := r.ParseForm(); err != nil {
			t.Fatalf("ParseForm: %v", err)
		}
		want := url.Values{
			"grant_type":    {"refresh_token"},
			"client_id":     {"client-id"},
			"refresh_token": {"old-refresh"},
			"install_id":    {"installation"},
		}
		for key, values := range want {
			if got := r.Form[key]; len(got) != 1 || got[0] != values[0] {
				t.Errorf("form[%q] = %q, want %q", key, got, values)
			}
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"access_token":"new-access"}`)
	}))
	defer server.Close()

	token, err := refreshOAuthToken(t.Context(), server.Client(), server.URL, "old-refresh", "client-id", "installation")
	if err != nil {
		t.Fatalf("refreshOAuthToken: %v", err)
	}
	if token.AccessToken != "new-access" {
		t.Errorf("AccessToken = %q, want new-access", token.AccessToken)
	}
	if !token.ExpiresAt.IsZero() {
		t.Errorf("ExpiresAt = %s, want zero without expires_in", token.ExpiresAt)
	}
}

func TestOAuthTokenResponseFailures(t *testing.T) {
	tests := []struct {
		name     string
		status   int
		body     string
		want     string
		exchange bool
	}{
		{name: "exchange status", status: http.StatusUnauthorized, body: "denied", want: "token exchange failed (status 401): denied", exchange: true},
		{name: "exchange invalid JSON", status: http.StatusOK, body: "not-json", want: "parsing token response", exchange: true},
		{name: "refresh status", status: http.StatusBadGateway, body: "upstream unavailable", want: "token refresh failed (status 502): upstream unavailable"},
		{name: "refresh invalid JSON", status: http.StatusOK, body: "not-json", want: "parsing refresh response"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(tt.status)
				_, _ = io.WriteString(w, tt.body)
			}))
			defer server.Close()

			var err error
			if tt.exchange {
				_, err = exchangeCode(t.Context(), server.Client(), server.URL, "code", "redirect", "client", "verifier", "install")
			} else {
				_, err = refreshOAuthToken(t.Context(), server.Client(), server.URL, "refresh", "client", "install")
			}
			if err == nil || !strings.Contains(err.Error(), tt.want) {
				t.Fatalf("error = %v, want substring %q", err, tt.want)
			}
		})
	}
}

type failingRoundTripper struct {
	err error
}

func (f failingRoundTripper) RoundTrip(*http.Request) (*http.Response, error) {
	return nil, f.err
}

func TestOAuthTransportAndRequestFailures(t *testing.T) {
	transportErr := errors.New("connection refused")
	client := &http.Client{Transport: failingRoundTripper{err: transportErr}}

	if _, err := exchangeCode(t.Context(), client, "http://example.test/token", "code", "redirect", "client", "verifier", "install"); err == nil || !strings.Contains(err.Error(), "token exchange request failed") {
		t.Fatalf("exchange error = %v", err)
	}
	if _, err := refreshOAuthToken(t.Context(), client, "http://example.test/token", "refresh", "client", "install"); err == nil || !strings.Contains(err.Error(), "token refresh request failed") {
		t.Fatalf("refresh error = %v", err)
	}

	if _, err := exchangeCode(context.Background(), client, "://bad-url", "code", "redirect", "client", "verifier", "install"); err == nil || !strings.Contains(err.Error(), "creating token request") {
		t.Fatalf("invalid exchange endpoint error = %v", err)
	}
	if _, err := refreshOAuthToken(context.Background(), client, "://bad-url", "refresh", "client", "install"); err == nil || !strings.Contains(err.Error(), "creating refresh request") {
		t.Fatalf("invalid refresh endpoint error = %v", err)
	}
}

func TestOAuthContextCancellation(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		<-r.Context().Done()
	}))
	defer server.Close()

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err := exchangeCode(ctx, server.Client(), server.URL, "code", "redirect", "client", "verifier", "install")
	if err == nil || !strings.Contains(err.Error(), "context canceled") {
		t.Fatalf("error = %v, want context canceled", err)
	}
}

func TestPKCEHelpers(t *testing.T) {
	const verifier = "dBjftJeZ4CVP-mB92K27uhbUJU1p1r_wW1gFWFOEjXk"
	const wantChallenge = "E9Melhoa2OwvFrEMTJguCHaoeK1t8URWbuGJSstw-cM"
	if got := generateCodeChallenge(verifier); got != wantChallenge {
		t.Errorf("challenge = %q, want RFC 7636 value %q", got, wantChallenge)
	}

	firstVerifier := generateCodeVerifier()
	secondVerifier := generateCodeVerifier()
	if firstVerifier == secondVerifier {
		t.Error("independent verifier generations matched")
	}
	decodedVerifier, err := base64.RawURLEncoding.DecodeString(firstVerifier)
	if err != nil {
		t.Fatalf("verifier is not raw URL-safe base64: %v", err)
	}
	if len(decodedVerifier) != 32 {
		t.Errorf("decoded verifier length = %d, want 32", len(decodedVerifier))
	}

	firstState := generateState()
	secondState := generateState()
	if firstState == secondState {
		t.Error("independent state generations matched")
	}
	decodedState, err := base64.RawURLEncoding.DecodeString(firstState)
	if err != nil {
		t.Fatalf("state is not raw URL-safe base64: %v", err)
	}
	if len(decodedState) != 16 {
		t.Errorf("decoded state length = %d, want 16", len(decodedState))
	}
}
