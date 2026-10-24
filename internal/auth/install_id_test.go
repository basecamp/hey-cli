package auth

import (
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
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

// bindingServer stands in for HEY's refresh endpoint with install binding on: a family
// bound to one install answers that install and revokes on any other, and an unbound family
// is claimed by the first install that refreshes it.
type bindingServer struct {
	mu        sync.Mutex
	bound     string
	presented []string
	revoked   bool
}

func (b *bindingServer) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	b.mu.Lock()
	defer b.mu.Unlock()
	installID := r.FormValue("install_id")
	b.presented = append(b.presented, installID)
	if b.bound == "" {
		b.bound = installID
	}
	if b.revoked || installID != b.bound {
		b.revoked = true
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadRequest)
		_, _ = io.WriteString(w, `{"error":"invalid_grant","error_description":"issued to a different install"}`)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_, _ = fmt.Fprintf(w, `{"access_token":"access-%d","refresh_token":"refresh-%d","expires_in":3600}`, len(b.presented), len(b.presented))
}

// snapshot is what the server has seen, read under its lock.
func (b *bindingServer) snapshot() (presented []string, revoked bool) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return append([]string(nil), b.presented...), b.revoked
}

func legacyCredentials() *Credentials {
	return &Credentials{AccessToken: "expired", RefreshToken: "legacy-refresh", ExpiresAt: time.Now().Add(-time.Hour).Unix()}
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

// An earlier release logged in presenting this directory's install_id and saved credentials
// without it, so HEY bound their family to that id. The first refresh after upgrading
// presents the same id, and the session survives.
func TestUpgradedCredentialsRefreshAsTheDirectoryTheyWereIssuedTo(t *testing.T) {
	hey := &bindingServer{}
	server := httptest.NewServer(hey)
	defer server.Close()

	t.Setenv("HEY_TOKEN", "")
	mgr := sharedKeychainManager(t, server, newFakeKeyring())
	directoryID, err := mgr.GetStore().InstallID()
	if err != nil {
		t.Fatalf("InstallID: %v", err)
	}
	hey.bound = directoryID
	if err := mgr.GetStore().Save(mgr.CredentialKey(), legacyCredentials()); err != nil {
		t.Fatalf("Save: %v", err)
	}

	if _, err := mgr.AccessToken(t.Context()); err != nil {
		t.Fatalf("AccessToken: %v", err)
	}

	if presented, revoked := hey.snapshot(); revoked || len(presented) != 1 || presented[0] != directoryID {
		t.Errorf("presented %q (revoked %v), want the directory's %q", presented, revoked, directoryID)
	}
	stored, err := mgr.GetStore().Load(mgr.CredentialKey())
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if stored.InstallID != directoryID {
		t.Errorf("stored InstallID = %q, want %q kept with the rotated tokens", stored.InstallID, directoryID)
	}
}

// Two config directories sharing one keychain entry are one install to HEY: whichever
// refreshes first settles the id into the shared credential, and the other then presents it
// rather than its own directory's.
func TestDirectoriesSharingAKeychainEntryConvergeOnOneInstall(t *testing.T) {
	hey := &bindingServer{}
	server := httptest.NewServer(hey)
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
	if err := first.GetStore().Save(first.CredentialKey(), legacyCredentials()); err != nil {
		t.Fatalf("Save: %v", err)
	}

	if _, err := first.AccessToken(t.Context()); err != nil {
		t.Fatalf("first AccessToken: %v", err)
	}
	expireStoredToken(t, second)
	if _, err := second.AccessToken(t.Context()); err != nil {
		t.Fatalf("second AccessToken: %v", err)
	}

	if presented, revoked := hey.snapshot(); revoked || len(presented) != 2 || presented[0] != firstDirID || presented[1] != firstDirID {
		t.Errorf("presented %q (revoked %v), want the first directory's %q both times, never the second's %q", presented, revoked, firstDirID, secondDirID)
	}
}

// A directory without an install_id file of its own takes the id derived from the refresh
// token, the same for every holder, and mints nothing.
func TestCredentialsWithoutADirectoryIDAdoptTheDerivedOneWithoutMinting(t *testing.T) {
	hey := &bindingServer{}
	server := httptest.NewServer(hey)
	defer server.Close()

	t.Setenv("HEY_TOKEN", "")
	mgr := sharedKeychainManager(t, server, newFakeKeyring())
	if err := mgr.GetStore().Save(mgr.CredentialKey(), legacyCredentials()); err != nil {
		t.Fatalf("Save: %v", err)
	}

	if _, err := mgr.AccessToken(t.Context()); err != nil {
		t.Fatalf("AccessToken: %v", err)
	}

	want := adoptedInstallID("legacy-refresh")
	if presented, _ := hey.snapshot(); len(presented) != 1 || presented[0] != want {
		t.Errorf("presented %q, want the derived %q", presented, want)
	}
	if _, err := os.Stat(mgr.GetStore().installIDPath()); !os.IsNotExist(err) {
		t.Errorf("install_id file exists (err %v); a refresh must not mint one", err)
	}
}

// A directory whose install_id file is malformed has no usable id of its own, so it takes
// the derived one rather than minting over the file.
func TestAMalformedDirectoryIDFallsBackToTheDerivedOne(t *testing.T) {
	hey := &bindingServer{}
	server := httptest.NewServer(hey)
	defer server.Close()

	t.Setenv("HEY_TOKEN", "")
	mgr := sharedKeychainManager(t, server, newFakeKeyring())
	path := mgr.GetStore().installIDPath()
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	if err := os.WriteFile(path, []byte("truncat"), 0600); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	if err := mgr.GetStore().Save(mgr.CredentialKey(), legacyCredentials()); err != nil {
		t.Fatalf("Save: %v", err)
	}

	if _, err := mgr.AccessToken(t.Context()); err != nil {
		t.Fatalf("AccessToken: %v", err)
	}

	want := adoptedInstallID("legacy-refresh")
	if presented, _ := hey.snapshot(); len(presented) != 1 || presented[0] != want {
		t.Errorf("presented %q, want the derived %q", presented, want)
	}
	if data, _ := os.ReadFile(path); string(data) != "truncat" {
		t.Errorf("install_id file = %q, want it left as it was", data)
	}
}

func TestAdoptedInstallIDIsAStableUUIDPerRefreshToken(t *testing.T) {
	a := adoptedInstallID("token-a")
	if a != adoptedInstallID("token-a") {
		t.Error("adoption must be deterministic")
	}
	if a == adoptedInstallID("token-b") {
		t.Error("different credentials must adopt different installs")
	}
	if !isInstallID(a) {
		t.Errorf("adopted id %q is not a version-4 UUID", a)
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

// A malformed install_id in stored credentials is refused locally, never sent: HEY would
// read it as another install and revoke the session.
func TestRefreshRefusesAMalformedStoredInstallID(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Errorf("unexpected refresh request carrying install_id %q", r.FormValue("install_id"))
	}))
	defer server.Close()

	t.Setenv("HEY_TOKEN", "")
	mgr := sharedKeychainManager(t, server, newFakeKeyring())
	creds := &Credentials{AccessToken: "expired", RefreshToken: "current", ExpiresAt: time.Now().Add(-time.Hour).Unix(), InstallID: "not-an-install"}
	if err := mgr.GetStore().Save(mgr.CredentialKey(), creds); err != nil {
		t.Fatalf("Save: %v", err)
	}

	if _, err := mgr.AccessToken(t.Context()); err == nil || !strings.Contains(err.Error(), "malformed install_id") {
		t.Errorf("AccessToken error = %v, want a local malformed install_id refusal", err)
	}
}
