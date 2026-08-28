package auth

import (
	"crypto/rand"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// InstallID identifies this install to HEY as a device, minting the identifier on first
// use. It lives beside the credentials rather than in them: a device outlasts a logout,
// and HEY alerts on a sign-in from a device it hasn't seen.
func (s *Store) InstallID() (string, error) {
	unlock, err := s.lock()
	if err != nil {
		return "", err
	}
	defer unlock()

	return s.installID()
}

// installID is the unlocked variant, for a caller already holding the store lock.
func (s *Store) installID() (string, error) {
	path := s.installIDPath()

	data, err := os.ReadFile(path) //nolint:gosec // G304: path built from the store's own config directory
	if err == nil {
		if id := strings.TrimSpace(string(data)); id != "" {
			return id, nil
		}
	} else if !os.IsNotExist(err) {
		return "", err
	}

	id := newInstallID()
	if err := os.MkdirAll(s.fallbackDir, 0700); err != nil {
		return "", err
	}
	if err := os.WriteFile(path, []byte(id+"\n"), 0600); err != nil {
		return "", err
	}
	return id, nil
}

func (s *Store) installIDPath() string {
	return filepath.Join(s.fallbackDir, "install_id")
}

// newInstallID is a random version-4 UUID, the shape the mobile apps send.
func newInstallID() string {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		panic("crypto/rand failed: " + err.Error())
	}
	b[6] = (b[6] & 0x0f) | 0x40
	b[8] = (b[8] & 0x3f) | 0x80
	return fmt.Sprintf("%x-%x-%x-%x-%x", b[0:4], b[4:6], b[6:8], b[8:10], b[10:16])
}
