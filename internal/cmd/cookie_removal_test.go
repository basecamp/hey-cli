package cmd

import (
	"errors"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	hey "github.com/basecamp/hey-sdk/go/pkg/hey"

	"github.com/basecamp/hey-cli/internal/auth"
)

type authWireTransport func(*http.Request) (*http.Response, error)

func (f authWireTransport) RoundTrip(req *http.Request) (*http.Response, error) { return f(req) }

func TestSDKCookieRemovalOnWire(t *testing.T) {
	for _, mode := range []string{"cookie only", "bearer and obsolete cookie", "expired OAuth", "401 refresh", "401 replaced by cookie", "revoked OAuth", "refresh only"} {
		t.Run(mode, func(t *testing.T) {
			t.Setenv("HEY_TOKEN", "")
			t.Setenv("HEY_NO_KEYRING", "1")
			t.Setenv("XDG_CACHE_HOME", t.TempDir())
			const origin = "https://mail.example.test"
			creds := &auth.Credentials{AccessToken: "original-token", SessionCookie: "obsolete-cookie"}
			if mode == "cookie only" {
				creds.AccessToken = ""
				creds.OAuthType = "cookie"
			}
			if mode == "expired OAuth" || mode == "401 refresh" || mode == "revoked OAuth" || mode == "refresh only" {
				creds.RefreshToken = "original-refresh"
				creds.OAuthType = "oauth"
			}
			if mode == "expired OAuth" || mode == "revoked OAuth" {
				creds.ExpiresAt = time.Now().Add(-time.Hour).Unix()
			}
			if mode == "refresh only" {
				creds.AccessToken = ""
			}

			apiCalls, refreshCalls := 0, 0
			var manager *auth.Manager
			transport := authWireTransport(func(r *http.Request) (*http.Response, error) {
				if r.Header.Get("Cookie") != "" {
					t.Error("Cookie header reached the wire")
				}
				status, body := http.StatusOK, `[]`
				if r.URL.Path == "/oauth/tokens" {
					refreshCalls++
					if err := r.ParseForm(); err != nil || r.Form.Get("refresh_token") != "original-refresh" {
						t.Error("refresh did not use the OAuth grant")
					}
					body = `{"access_token":"renewed-token","refresh_token":"renewed-refresh","expires_in":3600,"token_type":"Bearer"}`
					if mode == "revoked OAuth" {
						status, body = http.StatusBadRequest, `{"error":"invalid_grant"}`
					}
				} else {
					apiCalls++
					want := "Bearer original-token"
					if mode == "expired OAuth" || mode == "refresh only" || (mode == "401 refresh" && apiCalls == 2) {
						want = "Bearer renewed-token"
					}
					if r.Header.Get("Authorization") != want {
						t.Errorf("API Authorization = %q, want %q", r.Header.Get("Authorization"), want)
					}
					if apiCalls == 1 && (mode == "401 refresh" || mode == "401 replaced by cookie") {
						status, body = http.StatusUnauthorized, `{"error":"unauthorized"}`
						if mode == "401 replaced by cookie" {
							if err := manager.GetStore().Save(origin, &auth.Credentials{SessionCookie: "replacement-cookie", OAuthType: "cookie"}); err != nil {
								t.Fatal(err)
							}
						}
					}
				}
				return &http.Response{StatusCode: status, Header: http.Header{"Content-Type": {"application/json"}}, Body: io.NopCloser(strings.NewReader(body)), Request: r}, nil
			})
			manager = auth.NewManager(origin, &http.Client{Transport: transport}, t.TempDir())
			if err := manager.GetStore().Save(origin, creds); err != nil {
				t.Fatal(err)
			}
			if mode == "refresh only" {
				if authenticated, err := manager.AuthenticationStatus(); err != nil || !authenticated {
					t.Fatal("refresh-only sign-in was not usable for data commands")
				}
			}
			initSDK(manager, origin)
			// Keep the production SDK config and auth bridge; substitute only the wire.
			client := hey.NewClient(sdkClientCfg, nil, append(sdkClientOpts, hey.WithTransport(transport))...)
			_, err := client.Boxes().List(t.Context())
			wantFailure := mode == "cookie only" || mode == "401 replaced by cookie" || mode == "revoked OAuth"
			if (err != nil) != wantFailure {
				t.Fatalf("SDK error = %v, wantFailure = %v", err, wantFailure)
			}
			if mode == "cookie only" || mode == "401 replaced by cookie" {
				if manager.IsAuthenticated() {
					t.Fatal("obsolete credential still authenticated")
				}
				if token, err := manager.AccessToken(t.Context()); token != "" || err == nil || !strings.Contains(err.Error(), "cookie authentication is no longer supported") {
					t.Fatal("cookie-only record was not rejected with sign-in guidance")
				}
			}
			if mode == "revoked OAuth" {
				if _, err := manager.GetStore().Load(origin); !errors.Is(err, auth.ErrCredentialsNotFound) {
					t.Fatal("revoked OAuth grant survived refresh refusal")
				}
			}
			wantAPI, wantRefresh := 1, 0
			switch mode {
			case "cookie only":
				wantAPI = 0
			case "expired OAuth", "refresh only":
				wantRefresh = 1
			case "revoked OAuth":
				wantAPI, wantRefresh = 0, 1
			case "401 refresh":
				wantAPI, wantRefresh = 2, 1
			}
			if apiCalls != wantAPI || refreshCalls != wantRefresh {
				t.Errorf("wire calls: API=%d refresh=%d; want API=%d refresh=%d", apiCalls, refreshCalls, wantAPI, wantRefresh)
			}
		})
	}
}

func TestRefreshOnlyStatusRemainsUsable(t *testing.T) {
	t.Setenv("HEY_NO_KEYRING", "1")
	home := t.TempDir()
	const origin = "https://mail.example.test"
	store := auth.NewStore(filepath.Join(home, "hey-cli"))
	if err := store.Save(origin, &auth.Credentials{RefreshToken: "valid-refresh", SessionCookie: "obsolete-cookie", OAuthType: "oauth"}); err != nil {
		t.Fatal(err)
	}
	_, response, err := runAuthCommand(t, home, origin, "", true, "auth", "status")
	if err != nil {
		t.Fatal(err)
	}
	data, _ := response.Data.(map[string]any)
	if data["authenticated"] != true || data["refresh_available"] != true || data["sign_in_required"] != nil {
		t.Fatal("refresh-only OAuth was incorrectly treated as an obsolete cookie sign-in")
	}
}

func TestLegacyCookieMigration(t *testing.T) {
	for _, mode := range []string{"cookie only", "mixed tokens", "stored token", "stored cookie", "foreign origin", "failed store"} {
		t.Run(mode, func(t *testing.T) {
			home := t.TempDir()
			dir := filepath.Join(home, "hey-cli")
			if err := os.MkdirAll(dir, 0700); err != nil {
				t.Fatal(err)
			}
			const origin = "https://mail.example.test"
			legacy := `{"base_url":"https://mail.example.test","session_cookie":"obsolete-cookie","future_setting":true}`
			if mode == "mixed tokens" || mode == "stored cookie" || mode == "failed store" {
				legacy = `{"base_url":"https://mail.example.test","access_token":"legacy-token","refresh_token":"legacy-refresh","session_cookie":"obsolete-cookie","future_setting":true}`
			}
			if mode == "foreign origin" {
				legacy = `{"base_url":"https://other.example.test","session_cookie":"obsolete-cookie","future_setting":true}`
			}
			if err := os.WriteFile(filepath.Join(dir, "config.json"), []byte(legacy), 0600); err != nil {
				t.Fatal(err)
			}
			t.Setenv("HEY_NO_KEYRING", "1")
			store := auth.NewStore(dir)
			if mode == "stored token" || mode == "stored cookie" {
				creds := &auth.Credentials{SessionCookie: "stored-cookie"}
				if mode == "stored token" {
					creds.AccessToken = "stored-token"
				}
				if err := store.Save(origin, creds); err != nil {
					t.Fatal(err)
				}
			}
			if mode == "failed store" {
				if err := os.Mkdir(filepath.Join(dir, "credentials.json"), 0700); err != nil {
					t.Fatal(err)
				}
			}
			_, response, err := runAuthCommand(t, home, origin, "", true, "auth", "status")
			if mode == "failed store" {
				if err == nil {
					t.Fatal("unreadable store reported success")
				}
			} else if err != nil {
				t.Fatal(err)
			}
			raw, err := os.ReadFile(filepath.Join(dir, "config.json"))
			if err != nil {
				t.Fatal(err)
			}
			preserved := mode == "foreign origin" || mode == "failed store"
			if strings.Contains(string(raw), "obsolete-cookie") != preserved || !strings.Contains(string(raw), "future_setting") {
				t.Fatal("migration did not preserve/scrub the expected legacy fields")
			}
			if mode == "failed store" {
				if !strings.Contains(string(raw), "legacy-token") || !strings.Contains(string(raw), "legacy-refresh") {
					t.Fatal("failed migration dropped tokens")
				}
				return
			}
			creds, loadErr := store.Load(origin)
			wantAuth := mode == "mixed tokens" || mode == "stored cookie" || mode == "stored token"
			if data, _ := response.Data.(map[string]any); data["authenticated"] != wantAuth {
				t.Errorf("authenticated = %v, want %v", data["authenticated"], wantAuth)
			}
			if wantAuth {
				if loadErr != nil {
					t.Fatal(loadErr)
				}
				wantToken := "legacy-token"
				if mode == "stored token" {
					wantToken = "stored-token"
				}
				if creds.AccessToken != wantToken {
					t.Fatal("migration dropped or replaced bearer token")
				}
				if mode != "stored token" && (creds.RefreshToken != "legacy-refresh" || creds.SessionCookie != "") {
					t.Fatal("migration lost refresh token or copied cookie")
				}
			} else if loadErr == nil {
				t.Fatal("migration resurrected obsolete cookie in store")
			}
		})
	}
}

func TestStoredTokenIgnoresEnvironmentWithObsoleteCookie(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HEY_NO_KEYRING", "1")
	const origin = "https://mail.example.test"
	store := auth.NewStore(filepath.Join(home, "hey-cli"))
	if err := store.Save(origin, &auth.Credentials{SessionCookie: "obsolete-cookie"}); err != nil {
		t.Fatal(err)
	}
	stdout, _, err := runAuthCommand(t, home, origin, "environment-token", false, "auth", "token", "--stored")
	if err == nil || stdout != "" {
		t.Fatal("--stored silently substituted an environment token for the obsolete credential")
	}
	stdout, _, err = runAuthCommand(t, home, origin, "environment-token", false, "auth", "token")
	if err != nil || stdout != "environment-token" {
		t.Fatal("supported environment bearer token stopped working")
	}
	if err := store.Save(origin, &auth.Credentials{AccessToken: "stored-token", SessionCookie: "obsolete-cookie"}); err != nil {
		t.Fatal(err)
	}
	stdout, _, err = runAuthCommand(t, home, origin, "environment-token", false, "auth", "token", "--stored")
	if err != nil || stdout != "stored-token" {
		t.Fatal("--stored did not return the supported stored bearer token")
	}
}

func TestObsoleteCookieDoesNotBlockIndependentCommands(t *testing.T) {
	for _, args := range [][]string{{"help"}, {"auth", "status"}, {"setup", "--skip-agents", "--skip-omarchy"}, {"login", "--token", "replacement-token"}} {
		t.Run(strings.Join(args, " "), func(t *testing.T) {
			home := t.TempDir()
			t.Setenv("HEY_NO_KEYRING", "1")
			t.Setenv("HEY_NONINTERACTIVE", "1")
			const origin = "https://mail.example.test"
			store := auth.NewStore(filepath.Join(home, "hey-cli"))
			if err := store.Save(origin, &auth.Credentials{SessionCookie: "obsolete-cookie", OAuthType: "cookie"}); err != nil {
				t.Fatal(err)
			}
			// Help writes prose regardless of output mode; use the ordinary writer.
			if _, _, err := runAuthCommand(t, home, origin, "", false, args...); err != nil {
				t.Fatal(err)
			}
		})
	}
}
