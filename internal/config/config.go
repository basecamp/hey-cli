package config

import (
	"encoding/json"
	"fmt"
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

	sources map[string]Source
}

type fileConfig struct {
	BaseURL   string `json:"base_url,omitempty"`
	AccountID string `json:"account_id,omitempty"`
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

func Load() (*Config, error) {
	cfg := &Config{
		BaseURL:   defaultBase,
		AccountID: AllAccounts,
		sources: map[string]Source{
			"base_url":   SourceDefault,
			"account_id": SourceDefault,
		},
	}

	// Layer 1: global config
	if err := cfg.loadFile(globalConfigPath(), SourceGlobal); err != nil {
		return nil, err
	}

	// Layer 2: local config (.hey/config.json walking up)
	if local := localConfigPath(); local != "" {
		if err := cfg.loadFile(local, SourceLocal); err != nil {
			return nil, err
		}
	}

	// Layer 3: environment variables
	if env := os.Getenv("HEY_BASE_URL"); env != "" {
		cfg.BaseURL = env
		cfg.sources["base_url"] = SourceEnv
	}
	if env := os.Getenv("HEY_ACCOUNT_ID"); env != "" {
		accountID, err := normalizeAccountID(env)
		if err != nil {
			return nil, err
		}
		cfg.AccountID = accountID
		cfg.sources["account_id"] = SourceEnv
	}

	if err := validateBaseURL(cfg.BaseURL); err != nil {
		return nil, err
	}

	return cfg, nil
}

func (c *Config) loadFile(path string, source Source) error {
	if path == "" {
		return nil
	}

	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return fmt.Errorf("could not read config %s: %w", path, err)
	}

	var file fileConfig
	if err := json.Unmarshal(data, &file); err != nil {
		return fmt.Errorf("could not parse config %s: %w", path, err)
	}

	if file.BaseURL != "" {
		c.BaseURL = file.BaseURL
		c.sources["base_url"] = source
	}
	if file.AccountID != "" {
		accountID, err := normalizeAccountID(file.AccountID)
		if err != nil {
			return fmt.Errorf("could not parse config %s: %w", path, err)
		}
		c.AccountID = accountID
		c.sources["account_id"] = source
	}

	return nil
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
		c.BaseURL = value
		if c.sources == nil {
			c.sources = map[string]Source{}
		}
		c.sources["base_url"] = SourceFlag
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

	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}

	var cfg OldConfig
	if err := json.Unmarshal(data, &cfg); err != nil {
		return nil, err
	}
	return &cfg, nil
}

// SaveBaseURL stores the global server URL while preserving the global account default.
func (c *Config) SaveBaseURL(baseURL string) error {
	file, err := loadGlobalFileConfig()
	if err != nil {
		return err
	}
	file.BaseURL = baseURL
	return saveGlobalConfig(file)
}

// SaveAccountID stores the default linked-account filter without replacing
// other global settings with local, environment, or flag overrides.
func (c *Config) SaveAccountID(accountID string) error {
	accountID, err := normalizeAccountID(accountID)
	if err != nil {
		return err
	}

	file, err := loadGlobalFileConfig()
	if err != nil {
		return err
	}
	file.AccountID = accountID
	return saveGlobalConfig(file)
}

func loadGlobalFileConfig() (fileConfig, error) {
	path := globalConfigPath()
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return fileConfig{}, nil
		}
		return fileConfig{}, fmt.Errorf("could not read config %s: %w", path, err)
	}

	var file fileConfig
	if err := json.Unmarshal(data, &file); err != nil {
		return fileConfig{}, fmt.Errorf("could not parse config %s: %w", path, err)
	}
	return file, nil
}

func saveGlobalConfig(file fileConfig) error {
	path := globalConfigPath()
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		return fmt.Errorf("could not create config directory: %w", err)
	}

	data, err := json.MarshalIndent(file, "", "  ")
	if err != nil {
		return fmt.Errorf("could not marshal config: %w", err)
	}
	if err := os.WriteFile(path, data, 0600); err != nil {
		return fmt.Errorf("could not write config: %w", err)
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
