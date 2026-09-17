package auth

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	keyringlib "github.com/zalando/go-keyring"
)

type managerKeyring struct {
	mu sync.Mutex

	value     string
	getErr    error
	setErr    error
	deleteErr error
	getCalls  int
	setCalls  int
}

func (k *managerKeyring) get(_ string, user string) (string, error) {
	k.mu.Lock()
	defer k.mu.Unlock()

	if user == keyringAvailability {
		return "", keyringlib.ErrNotFound
	}
	k.getCalls++
	if k.getErr != nil {
		return "", k.getErr
	}
	if k.value == "" {
		return "", keyringlib.ErrNotFound
	}
	return k.value, nil
}

func (k *managerKeyring) set(_, _ string, value string) error {
	k.mu.Lock()
	defer k.mu.Unlock()

	k.setCalls++
	if k.setErr != nil {
		return k.setErr
	}
	k.value = value
	return nil
}

func (k *managerKeyring) delete(_, _ string) error {
	k.mu.Lock()
	defer k.mu.Unlock()

	if k.deleteErr != nil {
		return k.deleteErr
	}
	k.value = ""
	return nil
}

func (k *managerKeyring) replace(t *testing.T, creds *Credentials) {
	t.Helper()
	data, err := json.Marshal(creds)
	if err != nil {
		t.Fatalf("Marshal credentials: %v", err)
	}
	k.mu.Lock()
	k.value = string(data)
	k.mu.Unlock()
}

func (k *managerKeyring) calls() (gets, sets int) {
	k.mu.Lock()
	defer k.mu.Unlock()
	return k.getCalls, k.setCalls
}

func managerWithKeyring(t *testing.T, creds *Credentials) (*Manager, *managerKeyring) {
	t.Helper()
	t.Setenv("HEY_TOKEN", "")
	t.Setenv("HEY_NO_KEYRING", "")

	keyring := &managerKeyring{}
	if creds != nil {
		keyring.replace(t, creds)
	}
	mgr := NewManager("https://app.hey.com", http.DefaultClient, t.TempDir())
	mgr.store.keyring = credentialKeyring{
		get:    keyring.get,
		set:    keyring.set,
		delete: keyring.delete,
	}
	return mgr, keyring
}

func authenticate(t *testing.T, mgr *Manager) *http.Request {
	t.Helper()
	req := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "https://app.hey.com/messages", nil)
	if err := mgr.AuthenticateRequest(t.Context(), req); err != nil {
		t.Fatalf("AuthenticateRequest: %v", err)
	}
	return req
}

func TestHEYTokenBypassesCredentialStorage(t *testing.T) {
	mgr, keyring := managerWithKeyring(t, nil)
	t.Setenv("HEY_TOKEN", "environment-token")

	if token, err := mgr.AccessToken(t.Context()); err != nil || token != "environment-token" {
		t.Fatalf("AccessToken = %q, %v", token, err)
	}
	if !mgr.IsAuthenticated() {
		t.Fatal("IsAuthenticated = false")
	}
	req := authenticate(t, mgr)
	if got := req.Header.Get("Authorization"); got != "Bearer environment-token" {
		t.Errorf("Authorization = %q, want environment token", got)
	}
	gets, _ := keyring.calls()
	if gets != 0 {
		t.Errorf("credential Gets = %d, want HEY_TOKEN to bypass storage", gets)
	}
}

func TestManagerCachesSuccessfulCredentialLoads(t *testing.T) {
	mgr, keyring := managerWithKeyring(t, &Credentials{AccessToken: "cached-token"})

	for range 8 {
		req := authenticate(t, mgr)
		if got := req.Header.Get("Authorization"); got != "Bearer cached-token" {
			t.Fatalf("Authorization = %q, want cached token", got)
		}
	}
	if !mgr.IsAuthenticated() {
		t.Fatal("IsAuthenticated = false")
	}
	if token, err := mgr.AccessToken(t.Context()); err != nil || token != "cached-token" {
		t.Fatalf("AccessToken = %q, %v", token, err)
	}

	gets, _ := keyring.calls()
	if gets != 1 {
		t.Errorf("credential Gets = %d, want one for the process", gets)
	}
}

func TestConcurrentAuthenticationCoalescesCredentialLoads(t *testing.T) {
	mgr, keyring := managerWithKeyring(t, &Credentials{AccessToken: "shared-token"})

	const requests = 32
	start := make(chan struct{})
	failures := make(chan error, requests)
	var wg sync.WaitGroup
	for range requests {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			req, err := http.NewRequestWithContext(context.Background(), http.MethodGet, "https://app.hey.com/messages", nil)
			if err == nil {
				err = mgr.AuthenticateRequest(context.Background(), req)
			}
			if err == nil && req.Header.Get("Authorization") != "Bearer shared-token" {
				err = errors.New("request did not use the shared token")
			}
			if err != nil {
				failures <- err
			}
		}()
	}
	close(start)
	wg.Wait()
	close(failures)
	for err := range failures {
		t.Error(err)
	}

	gets, _ := keyring.calls()
	if gets != 1 {
		t.Errorf("credential Gets = %d, want one across concurrent requests", gets)
	}
}

func TestManagerDoesNotCacheCredentialLoadFailures(t *testing.T) {
	tests := []struct {
		name    string
		initial string
		getErr  error
	}{
		{name: "missing"},
		{name: "malformed", initial: "not-json"},
		{name: "transient", getErr: errors.New("keyring temporarily unavailable")},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			mgr, keyring := managerWithKeyring(t, nil)
			keyring.mu.Lock()
			keyring.value = tt.initial
			keyring.getErr = tt.getErr
			keyring.mu.Unlock()

			if mgr.IsAuthenticated() {
				t.Fatal("failed credential load reported authenticated")
			}
			keyring.mu.Lock()
			keyring.getErr = nil
			keyring.mu.Unlock()
			keyring.replace(t, &Credentials{AccessToken: "recovered-token"})
			if !mgr.IsAuthenticated() {
				t.Fatal("successful retry was hidden by a cached failure")
			}

			gets, _ := keyring.calls()
			if gets != 2 {
				t.Errorf("credential Gets = %d, want failed load and retry", gets)
			}
		})
	}
}

func TestManagerCredentialCacheUsesCopies(t *testing.T) {
	t.Run("loaded credentials", func(t *testing.T) {
		mgr, _ := managerWithKeyring(t, &Credentials{AccessToken: "stored-token"})

		mgr.mu.Lock()
		first, err := mgr.loadCredentialsLocked()
		mgr.mu.Unlock()
		if err != nil {
			t.Fatalf("loadCredentialsLocked: %v", err)
		}
		first.AccessToken = "mutated-by-caller"

		mgr.mu.Lock()
		second, err := mgr.loadCredentialsLocked()
		mgr.mu.Unlock()
		if err != nil {
			t.Fatalf("loadCredentialsLocked: %v", err)
		}
		if second.AccessToken != "stored-token" {
			t.Errorf("cached token = %q, want an unmodified copy", second.AccessToken)
		}
	})

	t.Run("saved credentials", func(t *testing.T) {
		mgr, _ := managerWithKeyring(t, nil)
		creds := &Credentials{AccessToken: "saved-token"}
		if err := mgr.saveCredentials(creds); err != nil {
			t.Fatalf("saveCredentials: %v", err)
		}
		creds.AccessToken = "mutated-after-save"
		req := authenticate(t, mgr)
		if got := req.Header.Get("Authorization"); got != "Bearer saved-token" {
			t.Errorf("Authorization = %q, want a copy of the saved token", got)
		}
	})
}

func TestManagerUpdatesCacheOnlyAfterSuccessfulMutations(t *testing.T) {
	t.Run("login replaces cache", func(t *testing.T) {
		mgr, keyring := managerWithKeyring(t, &Credentials{AccessToken: "old-token"})
		authenticate(t, mgr)
		if err := mgr.LoginWithToken("new-token"); err != nil {
			t.Fatalf("LoginWithToken: %v", err)
		}
		req := authenticate(t, mgr)
		if got := req.Header.Get("Authorization"); got != "Bearer new-token" {
			t.Errorf("Authorization = %q, want replacement token", got)
		}
		gets, _ := keyring.calls()
		if gets != 1 {
			t.Errorf("credential Gets = %d, want cached replacement", gets)
		}
	})

	t.Run("failed save retains cache", func(t *testing.T) {
		mgr, keyring := managerWithKeyring(t, &Credentials{AccessToken: "working-token"})
		authenticate(t, mgr)
		keyring.mu.Lock()
		keyring.setErr = errors.New("keyring is locked")
		keyring.mu.Unlock()
		if err := mgr.LoginWithToken("unsaved-token"); err == nil {
			t.Fatal("LoginWithToken succeeded despite failed persistence")
		}
		req := authenticate(t, mgr)
		if got := req.Header.Get("Authorization"); got != "Bearer working-token" {
			t.Errorf("Authorization = %q, want prior cached token", got)
		}
	})

	t.Run("logout clears cache", func(t *testing.T) {
		mgr, _ := managerWithKeyring(t, &Credentials{AccessToken: "working-token"})
		authenticate(t, mgr)
		if err := mgr.Logout(); err != nil {
			t.Fatalf("Logout: %v", err)
		}
		if mgr.IsAuthenticated() {
			t.Fatal("IsAuthenticated = true after successful logout")
		}
	})

	t.Run("failed logout retains cache", func(t *testing.T) {
		mgr, keyring := managerWithKeyring(t, &Credentials{AccessToken: "working-token"})
		authenticate(t, mgr)
		keyring.mu.Lock()
		keyring.deleteErr = errors.New("keyring is locked")
		keyring.mu.Unlock()
		if err := mgr.Logout(); err == nil {
			t.Fatal("Logout succeeded despite failed deletion")
		}
		req := authenticate(t, mgr)
		if got := req.Header.Get("Authorization"); got != "Bearer working-token" {
			t.Errorf("Authorization = %q, want retained token", got)
		}
	})
}

func TestRefreshRereadsStoredAuthentication(t *testing.T) {
	tests := []struct {
		name        string
		old         *Credentials
		replacement *Credentials
		wantHeader  string
		wantValue   string
	}{
		{
			name:        "static token",
			old:         &Credentials{AccessToken: "old-token", OAuthType: "token"},
			replacement: &Credentials{AccessToken: "replacement-token", OAuthType: "token"},
			wantHeader:  "Authorization",
			wantValue:   "Bearer replacement-token",
		},
		{
			name:        "cookie",
			old:         &Credentials{SessionCookie: "old-cookie", OAuthType: "cookie"},
			replacement: &Credentials{SessionCookie: "replacement-cookie", OAuthType: "cookie"},
			wantHeader:  "Cookie",
			wantValue:   "session_token=replacement-cookie",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			mgr, keyring := managerWithKeyring(t, tt.old)
			authenticate(t, mgr)
			keyring.replace(t, tt.replacement)

			// The SDK calls Refresh after a 401. It must bypass the process cache
			// so its retry can use authentication another process stored.
			if err := mgr.Refresh(t.Context()); err != nil {
				t.Fatalf("Refresh: %v", err)
			}
			req := authenticate(t, mgr)
			if got := req.Header.Get(tt.wantHeader); got != tt.wantValue {
				t.Errorf("%s = %q, want %q", tt.wantHeader, got, tt.wantValue)
			}
			gets, _ := keyring.calls()
			if gets != 2 {
				t.Errorf("credential Gets = %d, want initial and forced refresh reads", gets)
			}
		})
	}
}

func TestRefreshDoesNotAdoptAnEmptyStoredCredential(t *testing.T) {
	mgr, keyring := managerWithKeyring(t, &Credentials{AccessToken: "cached-token", OAuthType: "token"})
	authenticate(t, mgr)
	keyring.replace(t, &Credentials{})

	if err := mgr.Refresh(t.Context()); err == nil {
		t.Fatal("Refresh succeeded after the stored credential became empty")
	}
	if mgr.IsAuthenticated() {
		t.Fatal("IsAuthenticated = true with an empty stored credential")
	}
}

func TestFailedForcedReadClearsStaleCredentials(t *testing.T) {
	mgr, keyring := managerWithKeyring(t, &Credentials{AccessToken: "deleted-token", OAuthType: "token"})
	if got := authenticate(t, mgr).Header.Get("Authorization"); got != "Bearer deleted-token" {
		t.Fatalf("Authorization = %q, want the credential before deletion", got)
	}

	// Stand in for another process deleting the shared keyring entry.
	keyring.mu.Lock()
	keyring.value = ""
	keyring.mu.Unlock()
	if err := mgr.Refresh(t.Context()); err == nil {
		t.Fatal("Refresh succeeded after persistent credentials were deleted")
	}
	if mgr.IsAuthenticated() {
		t.Fatal("IsAuthenticated reused the credential deleted from storage")
	}
	req := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "https://app.hey.com/messages", nil)
	if err := mgr.AuthenticateRequest(t.Context(), req); err == nil {
		t.Fatal("AuthenticateRequest reused the credential deleted from storage")
	}
	if got := req.Header.Get("Authorization"); got != "" {
		t.Errorf("Authorization = %q after persistent deletion", got)
	}
}

func TestFailedRefreshSaveDoesNotPublishCredentials(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"access_token":"unpersisted-token","refresh_token":"rotated-refresh","expires_in":3600}`))
	}))
	defer server.Close()

	old := &Credentials{
		AccessToken:   "working-token",
		RefreshToken:  "working-refresh",
		TokenEndpoint: server.URL,
	}
	mgr, keyring := managerWithKeyring(t, old)
	mgr.httpClient = server.Client()
	authenticate(t, mgr)
	keyring.mu.Lock()
	keyring.setErr = errors.New("keyring is locked")
	keyring.mu.Unlock()

	if err := mgr.Refresh(t.Context()); err == nil {
		t.Fatal("Refresh succeeded despite failed persistence")
	}
	_, sets := keyring.calls()
	if sets != 1 {
		t.Fatalf("credential Sets = %d, want the failed refresh persistence attempt", sets)
	}
	req := authenticate(t, mgr)
	if got := req.Header.Get("Authorization"); got != "Bearer working-token" {
		t.Errorf("Authorization = %q, want the last persisted token", got)
	}
}

func TestConcurrentAuthenticationSharesOneRefresh(t *testing.T) {
	var refreshes atomic.Int64
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		refreshes.Add(1)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"access_token":"fresh-token","refresh_token":"fresh-refresh","expires_in":3600}`))
	}))
	defer server.Close()

	mgr, keyring := managerWithKeyring(t, &Credentials{
		AccessToken:   "expired-token",
		RefreshToken:  "old-refresh",
		ExpiresAt:     time.Now().Add(-time.Hour).Unix(),
		TokenEndpoint: server.URL,
	})
	mgr.httpClient = server.Client()

	const requests = 32
	start := make(chan struct{})
	results := make(chan error, requests)
	var wg sync.WaitGroup
	for range requests {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			req, err := http.NewRequestWithContext(context.Background(), http.MethodGet, "https://app.hey.com/messages", nil)
			if err == nil {
				err = mgr.AuthenticateRequest(context.Background(), req)
			}
			if err == nil && req.Header.Get("Authorization") != "Bearer fresh-token" {
				err = errors.New("request did not use the refreshed token")
			}
			results <- err
		}()
	}
	close(start)
	wg.Wait()
	close(results)
	for err := range results {
		if err != nil {
			t.Error(err)
		}
	}

	if got := refreshes.Load(); got != 1 {
		t.Errorf("refresh requests = %d, want one", got)
	}
	gets, sets := keyring.calls()
	if gets != 2 {
		t.Errorf("credential Gets = %d, want initial load and locked refresh reread", gets)
	}
	if sets != 1 {
		t.Errorf("credential Sets = %d, want one persisted refresh", sets)
	}
}
