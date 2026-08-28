package auth

import (
	"os"
	"path/filepath"
	"regexp"
	"testing"
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
