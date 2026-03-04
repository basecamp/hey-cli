package auth

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"sync"

	"github.com/zalando/go-keyring"
)

const serviceName = "hey"

// Credentials holds OAuth tokens and metadata.
type Credentials struct {
	AccessToken   string `json:"access_token"`  //nolint:gosec // G117: legitimate credential field
	RefreshToken  string `json:"refresh_token"` //nolint:gosec // G117: legitimate credential field
	ExpiresAt     int64  `json:"expires_at"`
	OAuthType     string `json:"oauth_type"`
	TokenEndpoint string `json:"token_endpoint"`
	SessionCookie string `json:"session_cookie,omitempty"`
}

// Store handles credential storage, preferring system keychain.
type Store struct {
	initOnce    sync.Once
	useKeyring  bool
	noKeyring   bool
	fallbackDir string
}

// NewStore creates a credential store. Keyring availability is probed lazily
// on first credential operation, not at construction time.
func NewStore(fallbackDir string) *Store {
	return &Store{
		fallbackDir: fallbackDir,
		noKeyring:   os.Getenv("HEY_NO_KEYRING") != "",
	}
}

func (s *Store) ensureInit() {
	s.initOnce.Do(func() {
		if s.noKeyring {
			return
		}
		testKey := "hey::test"
		err := keyring.Set(serviceName, testKey, "test")
		if err == nil {
			_ = keyring.Delete(serviceName, testKey)
			s.useKeyring = true
			return
		}
		fmt.Fprintf(os.Stderr, "warning: system keyring unavailable, credentials stored in plaintext at %s\n",
			filepath.Join(s.fallbackDir, "credentials.json"))
	})
}

func key(origin string) string {
	return fmt.Sprintf("hey::%s", origin)
}

// Load retrieves credentials for the given origin.
func (s *Store) Load(origin string) (*Credentials, error) {
	s.ensureInit()
	if s.useKeyring {
		return s.loadFromKeyring(origin)
	}
	return s.loadFromFile(origin)
}

// Save stores credentials for the given origin.
func (s *Store) Save(origin string, creds *Credentials) error {
	s.ensureInit()
	if s.useKeyring {
		return s.saveToKeyring(origin, creds)
	}
	return s.saveToFile(origin, creds)
}

// Delete removes credentials for the given origin.
func (s *Store) Delete(origin string) error {
	s.ensureInit()
	if s.useKeyring {
		return keyring.Delete(serviceName, key(origin))
	}
	return s.deleteFile(origin)
}

func (s *Store) loadFromKeyring(origin string) (*Credentials, error) {
	data, err := keyring.Get(serviceName, key(origin))
	if err != nil {
		return nil, fmt.Errorf("credentials not found: %w", err)
	}

	var creds Credentials
	if err := json.Unmarshal([]byte(data), &creds); err != nil {
		return nil, fmt.Errorf("invalid credentials: %w", err)
	}
	return &creds, nil
}

func (s *Store) saveToKeyring(origin string, creds *Credentials) error {
	data, err := json.Marshal(creds)
	if err != nil {
		return err
	}
	return keyring.Set(serviceName, key(origin), string(data))
}

func (s *Store) credentialsPath() string {
	return filepath.Join(s.fallbackDir, "credentials.json")
}

func (s *Store) loadAllFromFile() (map[string]*Credentials, error) {
	data, err := os.ReadFile(s.credentialsPath())
	if err != nil {
		if os.IsNotExist(err) {
			return make(map[string]*Credentials), nil
		}
		return nil, err
	}

	var all map[string]*Credentials
	if err := json.Unmarshal(data, &all); err != nil {
		return nil, err
	}
	return all, nil
}

func (s *Store) saveAllToFile(all map[string]*Credentials) error {
	if err := os.MkdirAll(s.fallbackDir, 0700); err != nil {
		return err
	}

	data, err := json.MarshalIndent(all, "", "  ")
	if err != nil {
		return err
	}

	tmpFile, err := os.CreateTemp(s.fallbackDir, "credentials-*.json.tmp")
	if err != nil {
		return err
	}
	tmpPath := tmpFile.Name()

	if _, err := tmpFile.Write(data); err != nil {
		_ = tmpFile.Close()
		_ = os.Remove(tmpPath) //nolint:gosec // G703: path from os.CreateTemp
		return err
	}
	if err := tmpFile.Chmod(0600); err != nil {
		_ = tmpFile.Close()
		_ = os.Remove(tmpPath) //nolint:gosec // G703: path from os.CreateTemp
		return err
	}
	if err := tmpFile.Close(); err != nil {
		_ = os.Remove(tmpPath) //nolint:gosec // G703: path from os.CreateTemp
		return err
	}

	destPath := s.credentialsPath()
	if err := os.Rename(tmpPath, destPath); err != nil { //nolint:gosec // G703: paths constructed internally
		if runtime.GOOS == "windows" {
			_ = os.Remove(destPath)
			return os.Rename(tmpPath, destPath) //nolint:gosec // G703: same
		}
		_ = os.Remove(tmpPath) //nolint:gosec // G703: path from os.CreateTemp
		return err
	}
	return nil
}

func (s *Store) loadFromFile(origin string) (*Credentials, error) {
	all, err := s.loadAllFromFile()
	if err != nil {
		return nil, err
	}

	creds, ok := all[origin]
	if !ok {
		return nil, fmt.Errorf("credentials not found for %s", origin)
	}
	return creds, nil
}

func (s *Store) saveToFile(origin string, creds *Credentials) error {
	all, err := s.loadAllFromFile()
	if err != nil {
		return err
	}

	all[origin] = creds
	return s.saveAllToFile(all)
}

func (s *Store) deleteFile(origin string) error {
	all, err := s.loadAllFromFile()
	if err != nil {
		return err
	}

	delete(all, origin)
	return s.saveAllToFile(all)
}

// MigrateToKeyring migrates credentials from file to keyring.
func (s *Store) MigrateToKeyring() error {
	s.ensureInit()
	if !s.useKeyring {
		return nil
	}

	all, err := s.loadAllFromFile()
	if err != nil {
		return nil //nolint:nilerr // No file to migrate is not an error
	}

	for origin, creds := range all {
		if err := s.saveToKeyring(origin, creds); err != nil {
			return fmt.Errorf("failed to migrate %s: %w", origin, err)
		}
	}

	_ = os.Remove(s.credentialsPath())
	return nil
}

// UsingKeyring returns true if the store is using the system keyring.
func (s *Store) UsingKeyring() bool {
	s.ensureInit()
	return s.useKeyring
}
