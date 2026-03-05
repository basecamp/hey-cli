package auth

import (
	"os"
	"path/filepath"
	"testing"
)

func testStore(t *testing.T) *Store {
	t.Helper()
	t.Setenv("HEY_NO_KEYRING", "1")
	return NewStore(t.TempDir())
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

func TestFilePermissions(t *testing.T) {
	s := testStore(t)
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
}
