package auth

import (
	"crypto/rand"
	"crypto/sha256"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
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
	id, err := s.existingInstallID()
	if err != nil || id != "" {
		return id, err
	}

	// No file, or a truncated or garbage one — an earlier write interrupted by a
	// crash or a full disk, say — that must not be adopted and sent to HEY on
	// every login and refresh: mint a fresh one over it.
	id = newInstallID()
	if err := os.MkdirAll(s.fallbackDir, 0700); err != nil {
		return "", err
	}
	if err := writeFileAtomic(s.installIDPath(), []byte(id+"\n"), 0600); err != nil {
		return "", err
	}
	return id, nil
}

// existingInstallID is this directory's install_id when its file holds a well-formed one,
// and "" otherwise. It never mints.
func (s *Store) existingInstallID() (string, error) {
	data, err := os.ReadFile(s.installIDPath()) // #nosec G304 -- path built from the store's own config directory
	switch {
	case os.IsNotExist(err):
		return "", nil
	case err != nil:
		return "", err
	}
	if id := strings.TrimSpace(string(data)); isInstallID(id) {
		return id, nil
	}
	return "", nil
}

// installIDPattern is the canonical version-4 UUID shape newInstallID mints and
// the mobile apps send.
var installIDPattern = regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-4[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$`)

func isInstallID(id string) bool {
	return installIDPattern.MatchString(id)
}

// writeFileAtomic writes data to a temporary mode-perm file in the destination
// directory and renames it into place. A crash or full disk mid-write then
// leaves the previous file (or none) rather than a truncated one that the next
// run would mistake for a valid identifier.
func writeFileAtomic(path string, data []byte, perm os.FileMode) error {
	tmp, err := os.CreateTemp(filepath.Dir(path), ".install_id-*")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	defer func() {
		if tmpName != "" {
			_ = os.Remove(tmpName)
		}
	}()

	if err := tmp.Chmod(perm); err != nil {
		_ = tmp.Close()
		return err
	}
	if _, err := tmp.Write(data); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Sync(); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	if err := os.Rename(tmpName, path); err != nil {
		return err
	}
	tmpName = "" // renamed into place; nothing to clean up
	return nil
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
	return formatUUIDv4(b)
}

// adoptedInstallID is the install id for credentials saved before they carried one: a
// version-4-shaped UUID derived one-way from the refresh token, so every holder of that
// token arrives at the same id without coordinating.
func adoptedInstallID(refreshToken string) string {
	sum := sha256.Sum256([]byte("hey-cli adopted install_id\x00" + refreshToken))
	return formatUUIDv4(sum[:16])
}

func formatUUIDv4(b []byte) string {
	b[6] = (b[6] & 0x0f) | 0x40
	b[8] = (b[8] & 0x3f) | 0x80
	return fmt.Sprintf("%x-%x-%x-%x-%x", b[0:4], b[4:6], b[6:8], b[8:10], b[10:16])
}
