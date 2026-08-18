package auth

import (
	"errors"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"

	keyringlib "github.com/zalando/go-keyring"
)

func testStore(t *testing.T) *Store {
	t.Helper()
	t.Setenv("HEY_NO_KEYRING", "1")
	return NewStore(t.TempDir())
}

type fakeKeyring struct {
	values    map[string]string
	setErr    error
	getErr    error
	deleteErr error
}

func newFakeKeyring() *fakeKeyring {
	return &fakeKeyring{values: make(map[string]string)}
}

func (f *fakeKeyring) Set(service, user, password string) error {
	if f.setErr != nil {
		return f.setErr
	}
	f.values[service+"/"+user] = password
	return nil
}

func (f *fakeKeyring) Get(service, user string) (string, error) {
	if f.getErr != nil {
		return "", f.getErr
	}
	value, ok := f.values[service+"/"+user]
	if !ok {
		return "", keyringlib.ErrNotFound
	}
	return value, nil
}

func (f *fakeKeyring) Delete(service, user string) error {
	if f.deleteErr != nil {
		return f.deleteErr
	}
	delete(f.values, service+"/"+user)
	return nil
}

func keyringStore(t *testing.T, fake *fakeKeyring) *Store {
	t.Helper()
	t.Setenv("HEY_NO_KEYRING", "")
	store := NewStore(t.TempDir())
	store.keyring = credentialKeyring{
		set:    fake.Set,
		get:    fake.Get,
		delete: fake.Delete,
	}
	return store
}

func TestKeyringSaveLoadDelete(t *testing.T) {
	fake := newFakeKeyring()
	store := keyringStore(t, fake)
	origin := "https://app.hey.com"
	creds := &Credentials{AccessToken: "access", RefreshToken: "refresh"}

	if err := store.Save(origin, creds); err != nil {
		t.Fatalf("Save: %v", err)
	}
	if !store.UsingKeyring() {
		t.Fatal("UsingKeyring = false after successful probe")
	}
	loaded, err := store.Load(origin)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if loaded.AccessToken != "access" || loaded.RefreshToken != "refresh" {
		t.Errorf("credentials = %#v", loaded)
	}
	if err := store.Delete(origin); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	if _, err := store.Load(origin); err == nil {
		t.Fatal("Load succeeded after keyring deletion")
	}
}

func TestKeyringFailures(t *testing.T) {
	t.Run("availability error falls back to file", func(t *testing.T) {
		fake := newFakeKeyring()
		fake.getErr = errors.New("keyring unavailable")
		store := keyringStore(t, fake)
		if err := store.Save("https://app.hey.com", &Credentials{AccessToken: "file-token"}); err != nil {
			t.Fatalf("Save fallback: %v", err)
		}
		if store.UsingKeyring() {
			t.Fatal("UsingKeyring = true after failed availability check")
		}
		if _, err := os.Stat(store.credentialsPath()); err != nil {
			t.Fatalf("fallback credentials file: %v", err)
		}
	})

	t.Run("invalid stored JSON", func(t *testing.T) {
		fake := newFakeKeyring()
		store := keyringStore(t, fake)
		if !store.UsingKeyring() {
			t.Fatal("UsingKeyring = false")
		}
		fake.values[serviceName+"/"+key("https://app.hey.com")] = "not-json"
		_, err := store.Load("https://app.hey.com")
		if err == nil || !strings.Contains(err.Error(), "invalid credentials") {
			t.Fatalf("error = %v, want invalid credentials", err)
		}
	})

	t.Run("get error", func(t *testing.T) {
		fake := newFakeKeyring()
		store := keyringStore(t, fake)
		if !store.UsingKeyring() {
			t.Fatal("UsingKeyring = false")
		}
		fake.getErr = errors.New("locked")
		_, err := store.Load("https://app.hey.com")
		if err == nil || !strings.Contains(err.Error(), "credentials not found") {
			t.Fatalf("error = %v", err)
		}
	})

	t.Run("delete error", func(t *testing.T) {
		fake := newFakeKeyring()
		store := keyringStore(t, fake)
		if !store.UsingKeyring() {
			t.Fatal("UsingKeyring = false")
		}
		fake.deleteErr = errors.New("locked")
		if err := store.Delete("https://app.hey.com"); err == nil || !strings.Contains(err.Error(), "locked") {
			t.Fatalf("Delete error = %v", err)
		}
	})
}

func TestMigrateToKeyring(t *testing.T) {
	fake := newFakeKeyring()
	store := keyringStore(t, fake)
	origins := map[string]*Credentials{
		"https://app.hey.com":     {AccessToken: "production"},
		"https://staging.hey.com": {AccessToken: "staging"},
	}
	if err := store.saveAllToFile(origins); err != nil {
		t.Fatalf("save fallback credentials: %v", err)
	}

	if err := store.MigrateToKeyring(); err != nil {
		t.Fatalf("MigrateToKeyring: %v", err)
	}
	if _, err := os.Stat(store.credentialsPath()); !os.IsNotExist(err) {
		t.Errorf("credentials file remains after migration: %v", err)
	}
	for origin, want := range origins {
		loaded, err := store.Load(origin)
		if err != nil {
			t.Fatalf("Load %s: %v", origin, err)
		}
		if loaded.AccessToken != want.AccessToken {
			t.Errorf("%s AccessToken = %q, want %q", origin, loaded.AccessToken, want.AccessToken)
		}
	}
}

func TestMigrationFailurePreservesFallbackFile(t *testing.T) {
	fake := newFakeKeyring()
	fake.setErr = errors.New("keyring write failed")
	store := keyringStore(t, fake)
	if err := store.saveAllToFile(map[string]*Credentials{
		"https://app.hey.com": {AccessToken: "production"},
	}); err != nil {
		t.Fatalf("save fallback credentials: %v", err)
	}

	err := store.MigrateToKeyring()
	if err == nil || !strings.Contains(err.Error(), "failed to migrate") {
		t.Fatalf("error = %v, want migration failure", err)
	}
	if _, err := os.Stat(store.credentialsPath()); err != nil {
		t.Fatalf("fallback file removed after failed migration: %v", err)
	}
}

func TestMigrationSkipsUnavailableKeyring(t *testing.T) {
	store := testStore(t)
	if err := store.MigrateToKeyring(); err != nil {
		t.Fatalf("MigrateToKeyring: %v", err)
	}
}

func TestSaveLoadRoundTrip(t *testing.T) {
	s := testStore(t)
	origin := "https://app.hey.com"
	creds := &Credentials{
		AccessToken:  "access-123",
		RefreshToken: "refresh-456",
		ExpiresAt:    1700000000,
		OAuthType:    "oauth",
	}

	if err := s.Save(origin, creds); err != nil {
		t.Fatalf("Save: %v", err)
	}

	loaded, err := s.Load(origin)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}

	if loaded.AccessToken != creds.AccessToken {
		t.Errorf("AccessToken = %q, want %q", loaded.AccessToken, creds.AccessToken)
	}
	if loaded.RefreshToken != creds.RefreshToken {
		t.Errorf("RefreshToken = %q, want %q", loaded.RefreshToken, creds.RefreshToken)
	}
	if loaded.ExpiresAt != creds.ExpiresAt {
		t.Errorf("ExpiresAt = %d, want %d", loaded.ExpiresAt, creds.ExpiresAt)
	}
	if loaded.OAuthType != creds.OAuthType {
		t.Errorf("OAuthType = %q, want %q", loaded.OAuthType, creds.OAuthType)
	}
}

func TestLoadNotFound(t *testing.T) {
	s := testStore(t)
	_, err := s.Load("https://app.hey.com")
	if err == nil {
		t.Fatal("expected error for missing credentials")
	}
}

func TestConcurrentKeyringAvailabilityChecksAreReadOnly(t *testing.T) {
	t.Setenv("HEY_NO_KEYRING", "")

	const storeCount = 16
	var mu sync.Mutex
	var getCalls, setCalls, deleteCalls, wrongLookups int
	keyring := credentialKeyring{
		get: func(service, user string) (string, error) {
			mu.Lock()
			defer mu.Unlock()
			getCalls++
			if service != serviceName || user != keyringAvailability {
				wrongLookups++
			}
			return "", keyringlib.ErrNotFound
		},
		set: func(_, _, _ string) error {
			mu.Lock()
			defer mu.Unlock()
			setCalls++
			return nil
		},
		delete: func(_, _ string) error {
			mu.Lock()
			defer mu.Unlock()
			deleteCalls++
			return nil
		},
	}

	stores := make([]*Store, storeCount)
	for i := range stores {
		stores[i] = NewStore(t.TempDir())
		stores[i].keyring = keyring
	}

	available := make(chan bool, storeCount)
	var wg sync.WaitGroup
	for _, store := range stores {
		wg.Add(1)
		go func() {
			defer wg.Done()
			available <- store.UsingKeyring()
		}()
	}
	wg.Wait()
	close(available)

	for usingKeyring := range available {
		if !usingKeyring {
			t.Error("missing availability entry reported the keyring unavailable")
		}
	}
	if getCalls != storeCount {
		t.Errorf("availability Get calls = %d, want %d", getCalls, storeCount)
	}
	if wrongLookups != 0 {
		t.Errorf("availability lookups with the wrong service or user = %d", wrongLookups)
	}
	if setCalls != 0 || deleteCalls != 0 {
		t.Errorf("availability checks mutated the keyring: Set = %d, Delete = %d", setCalls, deleteCalls)
	}
}

func TestKeyringAvailabilityErrorUsesFileStore(t *testing.T) {
	t.Setenv("HEY_NO_KEYRING", "")
	store := NewStore(t.TempDir())
	store.keyring = credentialKeyring{
		get: func(_, _ string) (string, error) {
			return "", errors.New("keyring unavailable")
		},
		set:    func(_, _, _ string) error { return nil },
		delete: func(_, _ string) error { return nil },
	}

	if store.UsingKeyring() {
		t.Fatal("unavailable keyring reported as available")
	}
}

func TestDeleteCredentials(t *testing.T) {
	s := testStore(t)
	origin := "https://app.hey.com"

	if err := s.Save(origin, &Credentials{AccessToken: "tok"}); err != nil {
		t.Fatalf("Save: %v", err)
	}

	if err := s.Delete(origin); err != nil {
		t.Fatalf("Delete: %v", err)
	}

	_, err := s.Load(origin)
	if err == nil {
		t.Fatal("expected error after delete")
	}
}

func TestMultipleOrigins(t *testing.T) {
	s := testStore(t)
	origins := []string{
		"https://app.hey.com",
		"https://staging.hey.com",
	}

	for i, origin := range origins {
		tok := "token-" + origin
		if err := s.Save(origin, &Credentials{AccessToken: tok, OAuthType: "oauth"}); err != nil {
			t.Fatalf("Save[%d]: %v", i, err)
		}
	}

	for i, origin := range origins {
		creds, err := s.Load(origin)
		if err != nil {
			t.Fatalf("Load[%d]: %v", i, err)
		}
		want := "token-" + origin
		if creds.AccessToken != want {
			t.Errorf("Load[%d] AccessToken = %q, want %q", i, creds.AccessToken, want)
		}
	}
}

func TestDeletePreservesOtherOrigins(t *testing.T) {
	s := testStore(t)
	first := "https://app.hey.com"
	second := "https://staging.hey.com"
	if err := s.Save(first, &Credentials{AccessToken: "first"}); err != nil {
		t.Fatalf("Save first: %v", err)
	}
	if err := s.Save(second, &Credentials{AccessToken: "second"}); err != nil {
		t.Fatalf("Save second: %v", err)
	}

	if err := s.Delete(first); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	if _, err := s.Load(first); err == nil {
		t.Fatal("deleted origin still loads")
	}
	creds, err := s.Load(second)
	if err != nil {
		t.Fatalf("Load second: %v", err)
	}
	if creds.AccessToken != "second" {
		t.Errorf("second AccessToken = %q, want second", creds.AccessToken)
	}
}

func TestInvalidCredentialsFile(t *testing.T) {
	s := testStore(t)
	if err := os.MkdirAll(s.fallbackDir, 0700); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	if err := os.WriteFile(s.credentialsPath(), []byte("not-json"), 0600); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	if _, err := s.Load("https://app.hey.com"); err == nil {
		t.Fatal("Load succeeded with invalid JSON")
	}
	if err := s.Save("https://app.hey.com", &Credentials{AccessToken: "token"}); err == nil {
		t.Fatal("Save overwrote invalid credentials file")
	}
	if err := s.Delete("https://app.hey.com"); err == nil {
		t.Fatal("Delete overwrote invalid credentials file")
	}
}

func TestCredentialsPathReadFailure(t *testing.T) {
	s := testStore(t)
	if err := os.MkdirAll(s.credentialsPath(), 0700); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}

	if _, err := s.Load("https://app.hey.com"); err == nil {
		t.Fatal("Load succeeded when credentials path is a directory")
	}
	if err := s.Save("https://app.hey.com", &Credentials{AccessToken: "token"}); err == nil {
		t.Fatal("Save succeeded when credentials path is a directory")
	}
}

func TestFallbackDirectoryCreationFailure(t *testing.T) {
	parent := t.TempDir()
	blockingFile := filepath.Join(parent, "not-a-directory")
	if err := os.WriteFile(blockingFile, []byte("block"), 0600); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	t.Setenv("HEY_NO_KEYRING", "1")
	s := NewStore(filepath.Join(blockingFile, "credentials"))

	err := s.Save("https://app.hey.com", &Credentials{AccessToken: "token"})
	if err == nil {
		t.Fatal("Save succeeded beneath a regular file")
	}
}

func TestStoreCapturesNoKeyringPreference(t *testing.T) {
	t.Setenv("HEY_NO_KEYRING", "1")
	s := NewStore(t.TempDir())
	t.Setenv("HEY_NO_KEYRING", "")

	if s.UsingKeyring() {
		t.Fatal("UsingKeyring = true after store was created with HEY_NO_KEYRING")
	}
}

func TestCredentialKeyIncludesOrigin(t *testing.T) {
	origin := "https://app.hey.com"
	if got := key(origin); got != "hey::"+origin {
		t.Errorf("key(%q) = %q", origin, got)
	}
}

func TestFilePermissions(t *testing.T) {
	t.Setenv("HEY_NO_KEYRING", "1")
	s := NewStore(filepath.Join(t.TempDir(), "credentials"))
	origin := "https://app.hey.com"

	if err := s.Save(origin, &Credentials{AccessToken: "tok"}); err != nil {
		t.Fatalf("Save: %v", err)
	}

	info, err := os.Stat(filepath.Join(s.fallbackDir, "credentials.json"))
	if err != nil {
		t.Fatalf("Stat: %v", err)
	}

	perm := info.Mode().Perm()
	if perm != 0600 {
		t.Errorf("file permissions = %o, want 0600", perm)
	}

	dirInfo, err := os.Stat(s.fallbackDir)
	if err != nil {
		t.Fatalf("Stat fallback directory: %v", err)
	}
	if got := dirInfo.Mode().Perm(); got&0077 != 0 {
		t.Errorf("directory permissions = %o, want no group/other access", got)
	}
}

func TestConcurrentSavesKeepEveryOrigin(t *testing.T) {
	t.Setenv("HEY_NO_KEYRING", "1")
	configDir := t.TempDir()
	origins := []string{"https://app.hey.com", "https://app.hey.localhost:3003", "https://work.hey.com", "https://family.hey.com"}

	saved := make(chan error, len(origins))
	for i, origin := range origins {
		// A store each, standing in for the separate processes that share the file.
		store := NewStore(configDir)
		go func() {
			saved <- store.Save(origin, &Credentials{AccessToken: "token-" + strconv.Itoa(i)})
		}()
	}
	for range origins {
		if err := <-saved; err != nil {
			t.Fatalf("Save: %v", err)
		}
	}

	reader := NewStore(configDir)
	for i, origin := range origins {
		creds, err := reader.Load(origin)
		if err != nil {
			t.Fatalf("Load(%q): %v", origin, err)
		}
		if want := "token-" + strconv.Itoa(i); creds.AccessToken != want {
			t.Errorf("Load(%q) access token = %q, want %q", origin, creds.AccessToken, want)
		}
	}
}

func TestLoadErrorNamesMissingOrigin(t *testing.T) {
	s := testStore(t)
	origin := "https://missing.hey.com"
	_, err := s.Load(origin)
	if err == nil || !strings.Contains(err.Error(), origin) {
		t.Fatalf("error = %v, want missing origin", err)
	}
}
