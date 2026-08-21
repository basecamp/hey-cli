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

type credentialKeyring struct {
	set    func(service, user, password string) error
	get    func(service, user string) (string, error)
	delete func(service, user string) error
}

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
	keyring     credentialKeyring
	lockMu      sync.Mutex
}

// NewStore creates a credential store. Keyring availability is probed lazily
// on first credential operation, not at construction time.
func NewStore(fallbackDir string) *Store {
	return &Store{
		fallbackDir: fallbackDir,
		noKeyring:   os.Getenv("HEY_NO_KEYRING") != "",
		keyring: credentialKeyring{
			set:    keyring.Set,
			get:    keyring.Get,
			delete: keyring.Delete,
		},
	}
}

func (s *Store) ensureInit() {
	s.initOnce.Do(func() {
		if s.noKeyring {
			return
		}
		testKey := "hey::test"
		err := s.keyring.set(serviceName, testKey, "test")
		if err == nil {
			_ = s.keyring.delete(serviceName, testKey)
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
	return s.load(origin)
}

// Save stores credentials for the given origin.
func (s *Store) Save(origin string, creds *Credentials) error {
	unlock, err := s.lock()
	if err != nil {
		return err
	}
	defer unlock()

	return s.save(origin, creds)
}

// Delete removes credentials for the given origin.
func (s *Store) Delete(origin string) error {
	unlock, err := s.lock()
	if err != nil {
		return err
	}
	defer unlock()

	s.ensureInit()
	if s.useKeyring {
		return s.keyring.delete(serviceName, key(origin))
	}
	return s.deleteFile(origin)
}

// load and save are the unlocked pair, for a caller already holding the lock over a
// whole read-modify-write. Everything else goes through Load and Save.
func (s *Store) load(origin string) (*Credentials, error) {
	s.ensureInit()
	if s.useKeyring {
		return s.loadFromKeyring(origin)
	}
	return s.loadFromFile(origin)
}

func (s *Store) save(origin string, creds *Credentials) error {
	s.ensureInit()
	if s.useKeyring {
		return s.saveToKeyring(origin, creds)
	}
	return s.saveToFile(origin, creds)
}

func (s *Store) loadFromKeyring(origin string) (*Credentials, error) {
	data, err := s.keyring.get(serviceName, key(origin))
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
	data, err := json.Marshal(creds) // #nosec G117 -- credentials are intentionally serialized into the system keyring
	if err != nil {
		return err
	}
	return s.keyring.set(serviceName, key(origin), string(data))
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

	data, err := json.MarshalIndent(all, "", "  ") // #nosec G117 -- credentials are intentionally serialized into a mode-0600 fallback file
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
