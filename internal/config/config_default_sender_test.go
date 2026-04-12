package config

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func TestDefaultSenderDefault(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", tmp)
	t.Setenv("HEY_BASE_URL", "")

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}

	if cfg.DefaultSender != "" {
		t.Errorf("DefaultSender = %q, want empty string", cfg.DefaultSender)
	}
	if src := cfg.SourceOf("default_sender"); src != SourceDefault {
		t.Errorf("source = %q, want %q", src, SourceDefault)
	}
}

func TestDefaultSenderFromGlobalConfig(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", tmp)
	t.Setenv("HEY_BASE_URL", "")

	dir := filepath.Join(tmp, configDirName)
	if err := os.MkdirAll(dir, 0700); err != nil {
		t.Fatal(err)
	}

	data, _ := json.Marshal(map[string]string{"default_sender": "erik@parrotapp.com"})
	if err := os.WriteFile(filepath.Join(dir, configFile), data, 0600); err != nil {
		t.Fatal(err)
	}

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}

	if cfg.DefaultSender != "erik@parrotapp.com" {
		t.Errorf("DefaultSender = %q, want %q", cfg.DefaultSender, "erik@parrotapp.com")
	}
	if src := cfg.SourceOf("default_sender"); src != SourceGlobal {
		t.Errorf("source = %q, want %q", src, SourceGlobal)
	}
}

func TestDefaultSenderSetFromFlag(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", tmp)
	t.Setenv("HEY_BASE_URL", "")

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}

	if err := cfg.SetFromFlag("default_sender", "test@example.com"); err != nil {
		t.Fatalf("SetFromFlag: %v", err)
	}

	if cfg.DefaultSender != "test@example.com" {
		t.Errorf("DefaultSender = %q, want %q", cfg.DefaultSender, "test@example.com")
	}
	if src := cfg.SourceOf("default_sender"); src != SourceFlag {
		t.Errorf("source = %q, want %q", src, SourceFlag)
	}
}

func TestDefaultSenderUnset(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", tmp)
	t.Setenv("HEY_BASE_URL", "")

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}

	// Set then unset
	if err := cfg.SetFromFlag("default_sender", "test@example.com"); err != nil {
		t.Fatalf("SetFromFlag: %v", err)
	}
	cfg.UnsetField("default_sender")

	if cfg.DefaultSender != "" {
		t.Errorf("DefaultSender = %q after unset, want empty", cfg.DefaultSender)
	}
	if src := cfg.SourceOf("default_sender"); src != SourceDefault {
		t.Errorf("source = %q after unset, want %q", src, SourceDefault)
	}
}

func TestDefaultSenderSaveAndReload(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", tmp)
	t.Setenv("HEY_BASE_URL", "")

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}

	if err := cfg.SetFromFlag("default_sender", "saved@example.com"); err != nil {
		t.Fatalf("SetFromFlag: %v", err)
	}
	if err := cfg.Save(); err != nil {
		t.Fatalf("Save: %v", err)
	}

	// Reload and verify persistence
	cfg2, err := Load()
	if err != nil {
		t.Fatalf("Load after save: %v", err)
	}

	if cfg2.DefaultSender != "saved@example.com" {
		t.Errorf("DefaultSender after reload = %q, want %q", cfg2.DefaultSender, "saved@example.com")
	}
}

func TestDefaultSenderOmittedFromJSONWhenEmpty(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", tmp)
	t.Setenv("HEY_BASE_URL", "")

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}

	// Save with empty default_sender
	if err := cfg.Save(); err != nil {
		t.Fatalf("Save: %v", err)
	}

	// Read the raw JSON and verify default_sender is not present
	configPath := filepath.Join(tmp, configDirName, configFile)
	data, err := os.ReadFile(configPath)
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}

	var raw map[string]any
	if err := json.Unmarshal(data, &raw); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}

	if _, exists := raw["default_sender"]; exists {
		t.Errorf("default_sender should be omitted from JSON when empty, got: %s", string(data))
	}
}

func TestDefaultSenderInValues(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", tmp)
	t.Setenv("HEY_BASE_URL", "")

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}

	if err := cfg.SetFromFlag("default_sender", "val@example.com"); err != nil {
		t.Fatalf("SetFromFlag: %v", err)
	}

	values := cfg.Values()
	// Should have at least 2 values (base_url + default_sender)
	if len(values) < 2 {
		t.Fatalf("Values() returned %d entries, want at least 2", len(values))
	}

	found := false
	for _, v := range values {
		if v.Value == "val@example.com" {
			found = true
			if v.Source != SourceFlag {
				t.Errorf("default_sender source = %q, want %q", v.Source, SourceFlag)
			}
		}
	}
	if !found {
		t.Error("default_sender not found in Values() output")
	}
}
