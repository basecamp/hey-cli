package config

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
)

const configDirName = ".config/hey-cli"
const configFile = "config.json"

type Config struct {
	BaseURL string `json:"base_url"`
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

// ConfigDir returns the config directory path (~/.config/hey-cli).
func ConfigDir() string {
	home, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	return filepath.Join(home, configDirName)
}

func configPath() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("could not determine home directory: %w", err)
	}
	return filepath.Join(home, configDirName, configFile), nil
}

func Load() (*Config, error) {
	path, err := configPath()
	if err != nil {
		return nil, err
	}

	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return &Config{
				BaseURL: "https://app.hey.com",
			}, nil
		}
		return nil, fmt.Errorf("could not read config: %w", err)
	}

	var cfg Config
	if err := json.Unmarshal(data, &cfg); err != nil {
		return nil, fmt.Errorf("could not parse config: %w", err)
	}
	if cfg.BaseURL == "" {
		cfg.BaseURL = "https://app.hey.com"
	}
	return &cfg, nil
}

// LoadOld reads the config file as the old format (with embedded credentials).
// Used only during migration.
func LoadOld() (*OldConfig, error) {
	path, err := configPath()
	if err != nil {
		return nil, err
	}

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

func (c *Config) Save() error {
	path, err := configPath()
	if err != nil {
		return err
	}

	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		return fmt.Errorf("could not create config directory: %w", err)
	}

	data, err := json.MarshalIndent(c, "", "  ")
	if err != nil {
		return fmt.Errorf("could not marshal config: %w", err)
	}

	if err := os.WriteFile(path, data, 0600); err != nil {
		return fmt.Errorf("could not write config: %w", err)
	}
	return nil
}
