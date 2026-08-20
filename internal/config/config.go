package config

import (
	"encoding/json"
	"fmt"
	"net"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/basecamp/hey-cli/internal/apierr"
)

const (
	configDirName = "hey-cli"
	configFile    = "config.json"
	defaultBase   = "https://app.hey.com"
	AllAccounts   = "all"
)

type Source string

const (
	SourceDefault Source = "default"
	SourceGlobal  Source = "global"
	SourceLocal   Source = "local"
	SourceEnv     Source = "env"
	SourceFlag    Source = "flag"
)

type Value struct {
	Value  string `json:"value"`
	Source Source `json:"source"`
}

type Config struct {
	BaseURL   string `json:"base_url"`
	AccountID string `json:"account_id,omitempty"`

	sources             map[string]Source
	globalConfig        fileConfig
	localConfig         *LocalConfig
	trustedLocalConfigs map[string]trustedLocalConfigRecord
}

type fileConfig struct {
	BaseURL             string                              `json:"base_url,omitempty"`
	AccountID           string                              `json:"account_id,omitempty"`
	AccountDefaults     map[string]string                   `json:"account_defaults,omitempty"`
	TrustedLocalConfigs map[string]trustedLocalConfigRecord `json:"trusted_local_configs,omitempty"`
}

// OldConfig represents the legacy config format with embedded credentials.
// Used only for migration.
type OldConfig struct {
	BaseURL       string `json:"base_url"`
	AccessToken   string `json:"access_token,omitempty"`
	RefreshToken  string `json:"refresh_token,omitempty"`
	TokenExpiry   int64  `json:"token_expiry,omitempty"`
	ClientID      string `json:"client_id,omitempty"`
	ClientSecret  string `json:"client_secret,omitempty"`
	InstallID     string `json:"install_id,omitempty"`
	SessionCookie string `json:"session_cookie,omitempty"`
}

func ConfigDir() string {
	if xdg := os.Getenv("XDG_CONFIG_HOME"); xdg != "" {
		return filepath.Join(xdg, configDirName)
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	return filepath.Join(home, ".config", configDirName)
}

func StateDir() string {
	if xdg := os.Getenv("XDG_STATE_HOME"); xdg != "" {
		return filepath.Join(xdg, configDirName)
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	return filepath.Join(home, ".local", "state", configDirName)
}

func CacheDir() string {
	if xdg := os.Getenv("XDG_CACHE_HOME"); xdg != "" {
		return filepath.Join(xdg, configDirName)
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	return filepath.Join(home, ".cache", configDirName)
}

func globalConfigPath() string {
	dir := ConfigDir()
	if dir == "" {
		return ""
	}
	return filepath.Join(dir, configFile)
}

func localConfigPath() string {
	// Walk up from cwd looking for .hey/config.json
	dir, err := os.Getwd()
	if err != nil {
		return ""
	}
	for {
		candidate := filepath.Join(dir, ".hey", configFile)
		if _, err := os.Stat(candidate); err == nil {
			return candidate
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			break
		}
		dir = parent
	}
	return ""
}

// Load reads the effective configuration: defaults, the global file, a
// repository-local .hey/config.json found from the working directory, then the
// environment.
func Load() (*Config, error) {
	return load(localConfigPath())
}

// LoadGlobal reads the configuration without any repository-local file. It is
// for commands that run from an arbitrary working directory on the user's
// behalf — a desktop bar poller, say — where a checkout's config must neither
// redirect them nor trip the local-config trust gate.
func LoadGlobal() (*Config, error) {
	return load("")
}

// Defaults is the configuration before any file or environment is consulted:
// the fallback for commands that must run even when the global file is broken.
func Defaults() *Config {
	return &Config{
		BaseURL:   defaultBase,
		AccountID: AllAccounts,
		sources: map[string]Source{
			"base_url":   SourceDefault,
			"account_id": SourceDefault,
		},
	}
}

func load(localPath string) (*Config, error) {
	cfg := Defaults()

	global, err := readFileConfig(globalConfigPath())
	if err != nil {
		return nil, err
	}
	cfg.globalConfig = global
	if global.BaseURL != "" {
		cfg.BaseURL = global.BaseURL
		cfg.sources["base_url"] = SourceGlobal
	}

	var local fileConfig
	if localPath != "" {
		local, err = readFileConfig(localPath)
		if err != nil {
			return nil, err
		}
		if local.BaseURL != "" {
			cfg.BaseURL = local.BaseURL
			cfg.sources["base_url"] = SourceLocal
		}
		if local.AccountID != "" {
			cfg.AccountID = local.AccountID
			cfg.sources["account_id"] = SourceLocal
		}
	}

	if env := os.Getenv("HEY_BASE_URL"); env != "" {
		cfg.BaseURL = env
		cfg.sources["base_url"] = SourceEnv
	}
	if env := os.Getenv("HEY_ACCOUNT_ID"); env != "" {
		accountID, accountErr := normalizeAccountID(env)
		if accountErr != nil {
			return nil, accountErr
		}
		cfg.AccountID = accountID
		cfg.sources["account_id"] = SourceEnv
	}

	if validationErr := validateBaseURL(cfg.BaseURL); validationErr != nil {
		return nil, validationErr
	}
	if cfg.SourceOf("account_id") == SourceDefault {
		if accountID, ok := accountDefaultFor(global, cfg.BaseURL); ok {
			cfg.AccountID = accountID
			cfg.sources["account_id"] = SourceGlobal
		}
	}
	cfg.trustedLocalConfigs = global.TrustedLocalConfigs
	if localPath != "" {
		cfg.localConfig, err = makeLocalConfig(localPath, local, cfg.BaseURL, global.TrustedLocalConfigs)
		if err != nil {
			return nil, err
		}
	}

	return cfg, nil
}

func readFileConfig(path string) (fileConfig, error) {
	if path == "" {
		return fileConfig{}, nil
	}
	data, err := os.ReadFile(path) // #nosec G304 -- path is the discovered global or repository configuration file
	if err != nil {
		if os.IsNotExist(err) {
			return fileConfig{}, nil
		}
		return fileConfig{}, fmt.Errorf("could not read config %s: %w", path, err)
	}
	var file fileConfig
	if unmarshalErr := json.Unmarshal(data, &file); unmarshalErr != nil {
		return fileConfig{}, fmt.Errorf("could not parse config %s: %w", path, unmarshalErr)
	}
	if file.AccountID != "" {
		file.AccountID, err = normalizeAccountID(file.AccountID)
		if err != nil {
			return fileConfig{}, fmt.Errorf("could not parse config %s: %w", path, err)
		}
	}
	if len(file.AccountDefaults) > 0 {
		normalizedDefaults := make(map[string]string, len(file.AccountDefaults))
		for origin, accountID := range file.AccountDefaults {
			normalizedOrigin, originErr := serverOrigin(origin)
			if originErr != nil {
				return fileConfig{}, fmt.Errorf("could not parse account default origin %s in %s: %w", origin, path, originErr)
			}
			normalized, normalizeErr := normalizeAccountID(accountID)
			if normalizeErr != nil {
				return fileConfig{}, fmt.Errorf("could not parse account default for %s in %s: %w", origin, path, normalizeErr)
			}
			if existing, duplicate := normalizedDefaults[normalizedOrigin]; duplicate && existing != normalized {
				return fileConfig{}, fmt.Errorf("conflicting account defaults for %s in %s", normalizedOrigin, path)
			}
			normalizedDefaults[normalizedOrigin] = normalized
		}
		file.AccountDefaults = normalizedDefaults
	}
	return file, nil
}

func accountDefaultFor(file fileConfig, baseURL string) (string, bool) {
	origin, err := serverOrigin(baseURL)
	if err != nil {
		return "", false
	}
	if accountID, ok := file.AccountDefaults[origin]; ok {
		return accountID, true
	}
	legacyOrigin, err := serverOrigin(firstNonEmpty(file.BaseURL, defaultBase))
	if err == nil && legacyOrigin == origin && file.AccountID != "" {
		return file.AccountID, true
	}
	return "", false
}

func firstNonEmpty(value, fallback string) string {
	if value != "" {
		return value
	}
	return fallback
}

func (c *Config) SourceOf(key string) Source {
	if c.sources == nil {
		return SourceDefault
	}
	s, ok := c.sources[key]
	if !ok {
		return SourceDefault
	}
	return s
}

func (c *Config) SetFromFlag(key, value string) error {
	switch key {
	case "base_url":
		if err := validateBaseURL(value); err != nil {
			return err
		}
		accountSource := c.SourceOf("account_id")
		c.BaseURL = value
		if c.sources == nil {
			c.sources = map[string]Source{}
		}
		c.sources["base_url"] = SourceFlag
		if accountSource == SourceDefault || accountSource == SourceGlobal {
			c.AccountID = AllAccounts
			c.sources["account_id"] = SourceDefault
			if accountID, ok := accountDefaultFor(c.globalConfig, value); ok {
				c.AccountID = accountID
				c.sources["account_id"] = SourceGlobal
			}
		}
		c.refreshLocalConfigTrust()
	case "account_id":
		accountID, err := normalizeAccountID(value)
		if err != nil {
			return err
		}
		c.AccountID = accountID
		if c.sources == nil {
			c.sources = map[string]Source{}
		}
		c.sources["account_id"] = SourceFlag
	}
	return nil
}

func (c *Config) Values() []Value {
	return []Value{
		{Value: c.BaseURL, Source: c.SourceOf("base_url")},
		{Value: c.AccountID, Source: c.SourceOf("account_id")},
	}
}

// LoadOld reads the config file as the old format (with embedded credentials).
// Used only during migration.
func LoadOld() (*OldConfig, error) {
	path := globalConfigPath()

	data, err := os.ReadFile(path) // #nosec G304 -- path is the fixed user configuration file used for migration
	if err != nil {
		return nil, err
	}

	var cfg OldConfig
	if err := json.Unmarshal(data, &cfg); err != nil {
		return nil, err
	}
	return &cfg, nil
}

// SaveBaseURL stores the global server URL while preserving account defaults for every server.
func (c *Config) SaveBaseURL(baseURL string) error {
	file, err := loadGlobalFileConfig()
	if err != nil {
		return err
	}
	if err := migrateLegacyAccountDefault(&file); err != nil {
		return err
	}
	file.BaseURL = baseURL
	return saveGlobalConfig(file)
}

// SaveAccountID stores the default linked-account filter for the effective server
// without replacing settings for any other server.
func (c *Config) SaveAccountID(accountID string) error {
	accountID, err := normalizeAccountID(accountID)
	if err != nil {
		return err
	}
	origin, err := serverOrigin(c.BaseURL)
	if err != nil {
		return err
	}

	file, err := loadGlobalFileConfig()
	if err != nil {
		return err
	}
	if err := migrateLegacyAccountDefault(&file); err != nil {
		return err
	}
	if file.AccountDefaults == nil {
		file.AccountDefaults = make(map[string]string)
	}
	file.AccountDefaults[origin] = accountID
	return saveGlobalConfig(file)
}

func loadGlobalFileConfig() (fileConfig, error) {
	return readFileConfig(globalConfigPath())
}

func migrateLegacyAccountDefault(file *fileConfig) error {
	if file.AccountID == "" {
		return nil
	}
	origin, err := serverOrigin(firstNonEmpty(file.BaseURL, defaultBase))
	if err != nil {
		return err
	}
	if file.AccountDefaults == nil {
		file.AccountDefaults = make(map[string]string)
	}
	if _, exists := file.AccountDefaults[origin]; !exists {
		file.AccountDefaults[origin] = file.AccountID
	}
	file.AccountID = ""
	return nil
}

func saveGlobalConfig(file fileConfig) error {
	path := globalConfigPath()
	if path == "" {
		return fmt.Errorf("could not determine config path")
	}
	directory := filepath.Dir(path)
	if err := os.MkdirAll(directory, 0700); err != nil {
		return fmt.Errorf("could not create config directory: %w", err)
	}
	if err := os.Chmod(directory, 0700); err != nil { // #nosec G302 -- this is a directory permission, not a file mode
		return fmt.Errorf("could not secure config directory: %w", err)
	}

	data, err := json.MarshalIndent(file, "", "  ")
	if err != nil {
		return fmt.Errorf("could not marshal config: %w", err)
	}
	temporary, err := os.CreateTemp(directory, ".config-*")
	if err != nil {
		return fmt.Errorf("could not create temporary config: %w", err)
	}
	temporaryPath := temporary.Name()
	defer func() { _ = os.Remove(temporaryPath) }()
	if err := temporary.Chmod(0600); err != nil {
		_ = temporary.Close()
		return fmt.Errorf("could not secure temporary config: %w", err)
	}
	if _, err := temporary.Write(data); err != nil {
		_ = temporary.Close()
		return fmt.Errorf("could not write temporary config: %w", err)
	}
	if err := temporary.Close(); err != nil {
		return fmt.Errorf("could not close temporary config: %w", err)
	}
	if err := os.Rename(temporaryPath, path); err != nil {
		return fmt.Errorf("could not replace config: %w", err)
	}
	return nil
}

func normalizeAccountID(accountID string) (string, error) {
	accountID = strings.TrimSpace(accountID)
	if strings.EqualFold(accountID, AllAccounts) {
		return AllAccounts, nil
	}
	id, err := strconv.ParseInt(accountID, 10, 64)
	if err != nil || id <= 0 {
		return "", apierr.ErrUsage(fmt.Sprintf("account must be a positive ID or %q (got %q)", AllAccounts, accountID))
	}
	return strconv.FormatInt(id, 10), nil
}

func serverOrigin(base string) (string, error) {
	u, err := url.Parse(base)
	if err != nil || u.Scheme == "" || u.Host == "" {
		return "", apierr.ErrUsage(fmt.Sprintf("invalid base URL %q", base))
	}
	scheme := strings.ToLower(u.Scheme)
	hostname := strings.ToLower(u.Hostname())
	port := u.Port()
	if (scheme == "https" && port == "443") || (scheme == "http" && port == "80") {
		port = ""
	}
	host := hostname
	if port != "" {
		host = net.JoinHostPort(hostname, port)
	} else if strings.Contains(hostname, ":") {
		host = "[" + hostname + "]"
	}
	return (&url.URL{Scheme: scheme, Host: host}).String(), nil
}

func validateBaseURL(base string) error {
	u, err := url.Parse(base)
	if err != nil {
		return apierr.ErrUsage(fmt.Sprintf("invalid base URL %q: %v", base, err))
	}
	if u.Scheme == "" || u.Host == "" {
		return apierr.ErrUsage(fmt.Sprintf("base URL must be an absolute URL with scheme and host (got %q)", base))
	}
	// Enforce HTTPS for non-localhost
	host := u.Hostname()
	if u.Scheme != "https" && host != "localhost" && host != "127.0.0.1" && host != "::1" && !strings.HasSuffix(host, ".localhost") {
		return apierr.ErrUsage(fmt.Sprintf("base URL must use HTTPS (got %q)", base))
	}
	return nil
}
