package config

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"path/filepath"
	"sort"
)

// LocalConfig describes the security-sensitive settings supplied by the nearest
// repository-local .hey/config.json file.
type LocalConfig struct {
	Path         string `json:"path"`
	BaseURL      string `json:"base_url,omitempty"`
	AccountID    string `json:"account_id,omitempty"`
	ServerOrigin string `json:"server_origin"`
	Digest       string `json:"digest"`
	Trusted      bool   `json:"trusted"`
}

// TrustedLocalConfig identifies a repository-local configuration trusted by this user.
type TrustedLocalConfig struct {
	Path         string `json:"path"`
	ServerOrigin string `json:"server_origin"`
	BaseURL      string `json:"base_url,omitempty"`
	AccountID    string `json:"account_id,omitempty"`
	Digest       string `json:"digest"`
}

type trustedLocalConfigRecord struct {
	ServerOrigin string `json:"server_origin"`
	BaseURL      string `json:"base_url,omitempty"`
	AccountID    string `json:"account_id,omitempty"`
	Digest       string `json:"digest"`
}

func makeLocalConfig(path string, file fileConfig, effectiveBaseURL string, trusted map[string]trustedLocalConfigRecord) (*LocalConfig, error) {
	if file.BaseURL == "" && file.AccountID == "" {
		return nil, nil
	}
	canonicalPath, err := canonicalConfigPath(path)
	if err != nil {
		return nil, err
	}
	local := &LocalConfig{
		Path:      canonicalPath,
		BaseURL:   file.BaseURL,
		AccountID: file.AccountID,
	}
	if err := fingerprintLocalConfig(local, effectiveBaseURL); err != nil {
		return nil, err
	}
	local.Trusted = trusted[canonicalPath].Digest == local.Digest
	return local, nil
}

func fingerprintLocalConfig(local *LocalConfig, effectiveBaseURL string) error {
	origin, err := serverOrigin(effectiveBaseURL)
	if err != nil {
		return err
	}
	payload, err := json.Marshal(struct {
		BaseURL      string `json:"base_url,omitempty"`
		AccountID    string `json:"account_id,omitempty"`
		ServerOrigin string `json:"server_origin"`
	}{
		BaseURL:      local.BaseURL,
		AccountID:    local.AccountID,
		ServerOrigin: origin,
	})
	if err != nil {
		return fmt.Errorf("could not fingerprint local config %s: %w", local.Path, err)
	}
	sum := sha256.Sum256(payload)
	local.ServerOrigin = origin
	local.Digest = hex.EncodeToString(sum[:])
	return nil
}

func (c *Config) refreshLocalConfigTrust() {
	if c.localConfig == nil {
		return
	}
	if err := fingerprintLocalConfig(c.localConfig, c.BaseURL); err != nil {
		c.localConfig.Trusted = false
		return
	}
	c.localConfig.Trusted = c.trustedLocalConfigs[c.localConfig.Path].Digest == c.localConfig.Digest
}

func canonicalConfigPath(path string) (string, error) {
	absolute, err := filepath.Abs(path)
	if err != nil {
		return "", fmt.Errorf("could not resolve local config path %s: %w", path, err)
	}
	resolved, err := filepath.EvalSymlinks(absolute)
	if err != nil {
		return "", fmt.Errorf("could not resolve local config path %s: %w", path, err)
	}
	return filepath.Clean(resolved), nil
}

// UntrustedLocalConfig returns the local configuration when one of its values is
// effective for this invocation and the user has not trusted its current contents.
func (c *Config) UntrustedLocalConfig() *LocalConfig {
	if c.localConfig == nil || c.localConfig.Trusted {
		return nil
	}
	if c.SourceOf("base_url") != SourceLocal && c.SourceOf("account_id") != SourceLocal {
		return nil
	}
	local := *c.localConfig
	return &local
}

// LocalConfig returns the discovered local configuration and its trust status.
func (c *Config) LocalConfig() *LocalConfig {
	if c.localConfig == nil {
		return nil
	}
	local := *c.localConfig
	return &local
}

// TrustLocalConfig trusts the current security-sensitive values in the discovered
// local configuration. A later change produces a different digest and requires trust again.
func (c *Config) TrustLocalConfig() error {
	if c.localConfig == nil {
		return fmt.Errorf("no local .hey/config.json with security-sensitive settings was found")
	}
	file, err := loadGlobalFileConfig()
	if err != nil {
		return err
	}
	if err := migrateLegacyAccountDefault(&file); err != nil {
		return err
	}
	if file.TrustedLocalConfigs == nil {
		file.TrustedLocalConfigs = make(map[string]trustedLocalConfigRecord)
	}
	record := trustedLocalConfigRecord{
		ServerOrigin: c.localConfig.ServerOrigin,
		BaseURL:      c.localConfig.BaseURL,
		AccountID:    c.localConfig.AccountID,
		Digest:       c.localConfig.Digest,
	}
	file.TrustedLocalConfigs[c.localConfig.Path] = record
	if err := saveGlobalConfig(file); err != nil {
		return err
	}
	if c.trustedLocalConfigs == nil {
		c.trustedLocalConfigs = make(map[string]trustedLocalConfigRecord)
	}
	c.trustedLocalConfigs[c.localConfig.Path] = record
	c.localConfig.Trusted = true
	return nil
}

// UntrustLocalConfig removes trust for the discovered local configuration.
func (c *Config) UntrustLocalConfig() error {
	if c.localConfig == nil {
		return fmt.Errorf("no local .hey/config.json with security-sensitive settings was found")
	}
	file, err := loadGlobalFileConfig()
	if err != nil {
		return err
	}
	if err := migrateLegacyAccountDefault(&file); err != nil {
		return err
	}
	delete(file.TrustedLocalConfigs, c.localConfig.Path)
	if err := saveGlobalConfig(file); err != nil {
		return err
	}
	delete(c.trustedLocalConfigs, c.localConfig.Path)
	c.localConfig.Trusted = false
	return nil
}

// TrustedLocalConfigs returns the local configurations trusted in user-level config.
func TrustedLocalConfigs() ([]TrustedLocalConfig, error) {
	file, err := loadGlobalFileConfig()
	if err != nil {
		return nil, err
	}
	configs := make([]TrustedLocalConfig, 0, len(file.TrustedLocalConfigs))
	for path, record := range file.TrustedLocalConfigs {
		configs = append(configs, TrustedLocalConfig{
			Path:         path,
			ServerOrigin: record.ServerOrigin,
			BaseURL:      record.BaseURL,
			AccountID:    record.AccountID,
			Digest:       record.Digest,
		})
	}
	sort.Slice(configs, func(i, j int) bool { return configs[i].Path < configs[j].Path })
	return configs, nil
}
