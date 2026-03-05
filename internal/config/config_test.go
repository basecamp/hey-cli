package config

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func TestDefaultConfig(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", tmp)
	t.Setenv("HEY_BASE_URL", "")

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}

	if cfg.BaseURL != defaultBase {
		t.Errorf("BaseURL = %q, want %q", cfg.BaseURL, defaultBase)
	}
	if src := cfg.SourceOf("base_url"); src != SourceDefault {
		t.Errorf("source = %q, want %q", src, SourceDefault)
	}
}

func TestGlobalConfigOverride(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", tmp)
	t.Setenv("HEY_BASE_URL", "")

	dir := filepath.Join(tmp, configDirName)
	if err := os.MkdirAll(dir, 0700); err != nil {
		t.Fatal(err)
	}

	data, _ := json.Marshal(map[string]string{"base_url": "https://custom.hey.com"})
	if err := os.WriteFile(filepath.Join(dir, configFile), data, 0600); err != nil {
		t.Fatal(err)
	}

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}

	if cfg.BaseURL != "https://custom.hey.com" {
		t.Errorf("BaseURL = %q, want %q", cfg.BaseURL, "https://custom.hey.com")
	}
	if src := cfg.SourceOf("base_url"); src != SourceGlobal {
		t.Errorf("source = %q, want %q", src, SourceGlobal)
	}
}

func TestEnvOverride(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", tmp)
	t.Setenv("HEY_BASE_URL", "https://env.hey.com")

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}

	if cfg.BaseURL != "https://env.hey.com" {
		t.Errorf("BaseURL = %q, want %q", cfg.BaseURL, "https://env.hey.com")
	}
	if src := cfg.SourceOf("base_url"); src != SourceEnv {
		t.Errorf("source = %q, want %q", src, SourceEnv)
	}
}

func TestFlagOverride(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", tmp)
	t.Setenv("HEY_BASE_URL", "")

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}

	if err := cfg.SetFromFlag("base_url", "https://flag.hey.com"); err != nil {
		t.Fatalf("SetFromFlag: %v", err)
	}

	if cfg.BaseURL != "https://flag.hey.com" {
		t.Errorf("BaseURL = %q, want %q", cfg.BaseURL, "https://flag.hey.com")
	}
	if src := cfg.SourceOf("base_url"); src != SourceFlag {
		t.Errorf("source = %q, want %q", src, SourceFlag)
	}
}

func TestValidateBaseURL(t *testing.T) {
	tests := []struct {
		url     string
		wantErr bool
	}{
		{"https://app.hey.com", false},
		{"http://localhost:3000", false},
		{"http://127.0.0.1:3000", false},
		{"http://app.hey.localhost:3003", false},
		{"http://insecure.example.com", true},
		{"app.hey.com", true},        // bare path, no scheme
		{"http://[::1]:3000", false}, // IPv6 loopback
	}

	for _, tt := range tests {
		t.Run(tt.url, func(t *testing.T) {
			err := validateBaseURL(tt.url)
			if (err != nil) != tt.wantErr {
				t.Errorf("validateBaseURL(%q) error = %v, wantErr %v", tt.url, err, tt.wantErr)
			}
		})
	}
}

func TestSourceTracking(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", tmp)

	// Start with default, then override with env, then flag
	t.Setenv("HEY_BASE_URL", "")
	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.SourceOf("base_url") != SourceDefault {
		t.Errorf("initial source = %q, want %q", cfg.SourceOf("base_url"), SourceDefault)
	}

	// Env override
	t.Setenv("HEY_BASE_URL", "https://env.hey.com")
	cfg, err = Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.SourceOf("base_url") != SourceEnv {
		t.Errorf("env source = %q, want %q", cfg.SourceOf("base_url"), SourceEnv)
	}

	// Flag override on top
	if err := cfg.SetFromFlag("base_url", "https://flag.hey.com"); err != nil {
		t.Fatalf("SetFromFlag: %v", err)
	}
	if cfg.SourceOf("base_url") != SourceFlag {
		t.Errorf("flag source = %q, want %q", cfg.SourceOf("base_url"), SourceFlag)
	}
}
