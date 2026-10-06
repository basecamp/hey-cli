package auth

import (
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"regexp"
	"testing"
	"time"
)

var uuidV4 = regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-4[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$`)

func TestInstallIDIsMintedOnceAndPersists(t *testing.T) {
	t.Setenv("HEY_NO_KEYRING", "1")
	configDir := t.TempDir()
	store := NewStore(configDir)

	first, err := store.InstallID()
	if err != nil {
		t.Fatalf("InstallID: %v", err)
	}
	if !uuidV4.MatchString(first) {
		t.Fatalf("install id = %q, want a v4 UUID", first)
	}

	second, err := store.InstallID()
	if err != nil {
		t.Fatalf("InstallID: %v", err)
	}
	if second != first {
		t.Errorf("install id changed between calls: %q then %q", first, second)
	}

	if other, _ := NewStore(configDir).InstallID(); other != first {
		t.Errorf("install id = %q from a second store on the same directory, want %q", other, first)
	}

	info, err := os.Stat(filepath.Join(configDir, "install_id"))
	if err != nil {
		t.Fatalf("Stat: %v", err)
	}
	if perm := info.Mode().Perm(); perm != 0600 {
		t.Errorf("install_id mode = %o, want 0600", perm)
	}
}

func TestInstallIDSurvivesLogout(t *testing.T) {
	t.Setenv("HEY_NO_KEYRING", "1")
	configDir := t.TempDir()
	mgr := NewManager("https://app.hey.com", nil, configDir)

	id, err := mgr.GetStore().InstallID()
	if err != nil {
		t.Fatalf("InstallID: %v", err)
	}
	if err := mgr.LoginWithToken("token"); err != nil {
		t.Fatalf("LoginWithToken: %v", err)
	}
	if err := mgr.Logout(); err != nil {
		t.Fatalf("Logout: %v", err)
	}

	if after, _ := mgr.GetStore().InstallID(); after != id {
		t.Errorf("install id = %q after logout, want %q", after, id)
	}
}

func TestInstallIDsDifferPerInstall(t *testing.T) {
	t.Setenv("HEY_NO_KEYRING", "1")
	a, _ := NewStore(t.TempDir()).InstallID()
	b, _ := NewStore(t.TempDir()).InstallID()
	if a == b {
		t.Errorf("two installs share install id %q", a)
	}
}

func TestInstallIDReplacesAMalformedFile(t *testing.T) {
	t.Setenv("HEY_NO_KEYRING", "1")
	configDir := t.TempDir()
	path := filepath.Join(configDir, "install_id")

	// A truncated or garbage file — e.g. a write interrupted by a crash or a
	// full disk, or the old constant "hey-cli" identifier — is not a usable
	// identity and must be reminted, never sent to HEY as-is.
	if err := os.WriteFile(path, []byte("hey-cli"), 0600); err != nil {
		t.Fatalf("seed: %v", err)
	}

	id, err := NewStore(configDir).InstallID()
	if err != nil {
		t.Fatalf("InstallID: %v", err)
	}
	if !uuidV4.MatchString(id) {
		t.Fatalf("install id = %q, want a v4 UUID", id)
	}

	// The mint is durable: the replacement is written back, so a second store
	// reads the same value rather than reminting again.
	if again, _ := NewStore(configDir).InstallID(); again != id {
		t.Errorf("install id = %q on reload, want the reminted %q", again, id)
	}
}

// Two config directories sharing one keychain entry are one install to HEY: whichever
// refreshes first settles the id that every later refresh presents, so neither revokes the
// other's session by presenting a different install.
func TestDirectoriesSharingAKeychainEntryRefreshAsOneInstall(t *testing.T) {
	var presented []string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if err := r.ParseForm(); err != nil {
			t.Fatalf("ParseForm: %v", err)
		}
		presented = append(presented, r.Form.Get("install_id"))
		w.Header().Set("Content-Type", "application/json")
		_, _ = fmt.Fprintf(w, `{"access_token":"access-%d","refresh_token":"refresh-%d","expires_in":3600}`, len(presented), len(presented))
	}))
	defer server.Close()

	t.Setenv("HEY_TOKEN", "")
	fake := newFakeKeyring()
	first := sharedKeychainManager(t, server, fake)
	second := sharedKeychainManager(t, server, fake)

	firstDirID, _ := first.GetStore().InstallID()
	secondDirID, _ := second.GetStore().InstallID()
	if firstDirID == secondDirID {
		t.Fatal("each config directory should hold its own install_id file")
	}

	// Credentials saved before they carried an install, as every upgraded install has.
	legacy := &Credentials{AccessToken: "expired", RefreshToken: "refresh-0", ExpiresAt: time.Now().Add(-time.Hour).Unix()}
	if err := first.GetStore().Save(first.CredentialKey(), legacy); err != nil {
		t.Fatalf("Save: %v", err)
	}

	if _, err := first.AccessToken(t.Context()); err != nil {
		t.Fatalf("first AccessToken: %v", err)
	}
	expireStoredToken(t, second)
	if _, err := second.AccessToken(t.Context()); err != nil {
		t.Fatalf("second AccessToken: %v", err)
	}

	if len(presented) != 2 || presented[0] != firstDirID || presented[1] != firstDirID {
		t.Errorf("presented install_ids = %q, want the first refresh's %q both times", presented, firstDirID)
	}
	stored, err := second.GetStore().Load(second.CredentialKey())
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if stored.InstallID != firstDirID {
		t.Errorf("stored InstallID = %q, want %q", stored.InstallID, firstDirID)
	}
}

// Credentials that carry their install refresh as it, without consulting or minting this
// directory's file: a directory that never logged in mustn't present a fresh install.
func TestRefreshPresentsTheCredentialsInstallWithoutMintingOne(t *testing.T) {
	issuedTo := newInstallID()
	var presented string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if err := r.ParseForm(); err != nil {
			t.Fatalf("ParseForm: %v", err)
		}
		presented = r.Form.Get("install_id")
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"access_token":"fresh","refresh_token":"next","expires_in":3600}`)
	}))
	defer server.Close()

	t.Setenv("HEY_TOKEN", "")
	mgr := sharedKeychainManager(t, server, newFakeKeyring())
	creds := &Credentials{AccessToken: "expired", RefreshToken: "current", ExpiresAt: time.Now().Add(-time.Hour).Unix(), InstallID: issuedTo}
	if err := mgr.GetStore().Save(mgr.CredentialKey(), creds); err != nil {
		t.Fatalf("Save: %v", err)
	}

	if _, err := mgr.AccessToken(t.Context()); err != nil {
		t.Fatalf("AccessToken: %v", err)
	}

	if presented != issuedTo {
		t.Errorf("presented install_id = %q, want the credentials' %q", presented, issuedTo)
	}
	if _, err := os.Stat(mgr.GetStore().installIDPath()); !os.IsNotExist(err) {
		t.Errorf("install_id file exists (err %v); a refresh with a known install must not mint one", err)
	}
}

func sharedKeychainManager(t *testing.T, server *httptest.Server, fake *fakeKeyring) *Manager {
	t.Helper()
	t.Setenv("HEY_NO_KEYRING", "")
	mgr := NewManager(server.URL, server.Client(), t.TempDir())
	mgr.store.keyring = credentialKeyring{set: fake.Set, get: fake.Get, delete: fake.Delete}
	return mgr
}

func expireStoredToken(t *testing.T, mgr *Manager) {
	t.Helper()
	creds, err := mgr.GetStore().Load(mgr.CredentialKey())
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	creds.ExpiresAt = time.Now().Add(-time.Hour).Unix()
	if err := mgr.GetStore().Save(mgr.CredentialKey(), creds); err != nil {
		t.Fatalf("Save: %v", err)
	}
}
