package config

import (
	"encoding/json"
	"maps"
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
	if cfg.AccountID != AllAccounts {
		t.Errorf("AccountID = %q, want %q", cfg.AccountID, AllAccounts)
	}
	if src := cfg.SourceOf("account_id"); src != SourceDefault {
		t.Errorf("account source = %q, want %q", src, SourceDefault)
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

func TestAccountIDPrecedence(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(tmp, "config"))
	t.Setenv("HEY_BASE_URL", "")
	t.Setenv("HEY_ACCOUNT_ID", "303")
	if err := os.MkdirAll(filepath.Join(tmp, "workspace", ".hey"), 0700); err != nil {
		t.Fatal(err)
	}
	t.Chdir(filepath.Join(tmp, "workspace"))

	globalDir := filepath.Join(tmp, "config", configDirName)
	if err := os.MkdirAll(globalDir, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(globalDir, configFile), []byte(`{"account_id":"101"}`), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(tmp, "workspace", ".hey", configFile), []byte(`{"account_id":"202"}`), 0600); err != nil {
		t.Fatal(err)
	}

	cfg, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	if cfg.AccountID != "303" || cfg.SourceOf("account_id") != SourceEnv {
		t.Fatalf("environment account = %q (%s), want 303 (env)", cfg.AccountID, cfg.SourceOf("account_id"))
	}
	if err := cfg.SetFromFlag("account_id", "00404"); err != nil {
		t.Fatal(err)
	}
	if cfg.AccountID != "404" || cfg.SourceOf("account_id") != SourceFlag {
		t.Fatalf("flag account = %q (%s), want 404 (flag)", cfg.AccountID, cfg.SourceOf("account_id"))
	}
}

func TestInvalidAccountID(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	t.Setenv("HEY_ACCOUNT_ID", "personal")
	if _, err := Load(); err == nil {
		t.Fatal("Load returned no error for invalid account")
	}
}

func TestSaveAccountIDPreservesGlobalSettings(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", tmp)
	t.Setenv("HEY_BASE_URL", "https://env.hey.com")
	t.Setenv("HEY_ACCOUNT_ID", "")

	dir := filepath.Join(tmp, configDirName)
	if err := os.MkdirAll(dir, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, configFile), []byte(`{"base_url":"https://global.hey.com"}`), 0600); err != nil {
		t.Fatal(err)
	}

	cfg, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	if err := cfg.SaveAccountID("42"); err != nil {
		t.Fatal(err)
	}

	data, err := os.ReadFile(filepath.Join(dir, configFile))
	if err != nil {
		t.Fatal(err)
	}
	var file fileConfig
	if err := json.Unmarshal(data, &file); err != nil {
		t.Fatal(err)
	}
	if file.BaseURL != "https://global.hey.com" || file.AccountID != "" || file.AccountDefaults["https://env.hey.com"] != "42" {
		t.Fatalf("saved config = %#v", file)
	}
}

func TestSaveBaseURLPreservesGlobalAccount(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", tmp)
	t.Setenv("HEY_BASE_URL", "")
	t.Setenv("HEY_ACCOUNT_ID", "202")

	dir := filepath.Join(tmp, configDirName)
	if err := os.MkdirAll(dir, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, configFile), []byte(`{"base_url":"https://old.hey.com","account_id":"101"}`), 0600); err != nil {
		t.Fatal(err)
	}

	cfg, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	if err := cfg.SetFromFlag("base_url", "https://new.hey.com"); err != nil {
		t.Fatal(err)
	}
	if err := cfg.SaveBaseURL(cfg.BaseURL); err != nil {
		t.Fatal(err)
	}

	data, err := os.ReadFile(filepath.Join(dir, configFile))
	if err != nil {
		t.Fatal(err)
	}
	var file fileConfig
	if err := json.Unmarshal(data, &file); err != nil {
		t.Fatal(err)
	}
	if file.BaseURL != "https://new.hey.com" || file.AccountID != "" || file.AccountDefaults["https://old.hey.com"] != "101" {
		t.Fatalf("saved config = %#v", file)
	}
}

func TestAccountDefaultsAreBoundToServerOrigin(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	t.Setenv("HEY_ACCOUNT_ID", "")
	dir := filepath.Join(os.Getenv("XDG_CONFIG_HOME"), configDirName)
	if err := os.MkdirAll(dir, 0700); err != nil {
		t.Fatal(err)
	}
	data := `{
		"base_url":"https://app.hey.com",
		"account_defaults":{
			"https://app.hey.com":"101",
			"http://app.hey.localhost:3003":"202"
		}
	}`
	if err := os.WriteFile(filepath.Join(dir, configFile), []byte(data), 0600); err != nil {
		t.Fatal(err)
	}

	for _, test := range []struct {
		baseURL string
		want    string
	}{
		{"https://app.hey.com", "101"},
		{"http://app.hey.localhost:3003", "202"},
		{"https://staging.hey.com", AllAccounts},
	} {
		t.Run(test.baseURL, func(t *testing.T) {
			t.Setenv("HEY_BASE_URL", test.baseURL)
			cfg, err := Load()
			if err != nil {
				t.Fatal(err)
			}
			if cfg.AccountID != test.want {
				t.Fatalf("account for %s = %q, want %q", test.baseURL, cfg.AccountID, test.want)
			}
		})
	}
}

func TestServerOriginCanonicalizesDefaultPorts(t *testing.T) {
	tests := []struct {
		base string
		want string
	}{
		{"https://APP.HEY.COM:443/path", "https://app.hey.com"},
		{"http://app.hey.localhost:80/path", "http://app.hey.localhost"},
		{"https://app.hey.com:8443/path", "https://app.hey.com:8443"},
		{"http://[::1]:80/path", "http://[::1]"},
		{"https://[2001:db8::1]:8443/path", "https://[2001:db8::1]:8443"},
		{"https://[fe80::1%25eth0]:8443/path", "https://[fe80::1%25eth0]:8443"},
	}
	for _, tt := range tests {
		t.Run(tt.base, func(t *testing.T) {
			got, err := serverOrigin(tt.base)
			if err != nil {
				t.Fatal(err)
			}
			if got != tt.want {
				t.Fatalf("serverOrigin(%q) = %q, want %q", tt.base, got, tt.want)
			}
		})
	}
}

func TestAccountDefaultMatchesEquivalentDefaultPortOrigin(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	t.Setenv("HEY_BASE_URL", "https://app.hey.com:443")
	t.Setenv("HEY_ACCOUNT_ID", "")
	dir := filepath.Join(os.Getenv("XDG_CONFIG_HOME"), configDirName)
	if err := os.MkdirAll(dir, 0700); err != nil {
		t.Fatal(err)
	}
	data := `{"account_defaults":{"https://app.hey.com":"101"}}`
	if err := os.WriteFile(filepath.Join(dir, configFile), []byte(data), 0600); err != nil {
		t.Fatal(err)
	}

	cfg, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	if cfg.AccountID != "101" || cfg.SourceOf("account_id") != SourceGlobal {
		t.Fatalf("default-port account = %q (%s), want 101 (global)", cfg.AccountID, cfg.SourceOf("account_id"))
	}
}

func TestZoneScopedOriginAccountDefaultRoundTrips(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	t.Setenv("HEY_BASE_URL", "https://[fe80::1%25eth0]:8443")
	t.Setenv("HEY_ACCOUNT_ID", "")

	cfg, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	if err := cfg.SaveAccountID("101"); err != nil {
		t.Fatal(err)
	}
	reloaded, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	if reloaded.AccountID != "101" || reloaded.SourceOf("account_id") != SourceGlobal {
		t.Fatalf("zone-scoped account = %q (%s), want 101 (global)", reloaded.AccountID, reloaded.SourceOf("account_id"))
	}
}

func TestBaseURLFlagResolvesAccountForNewOrigin(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	t.Setenv("HEY_BASE_URL", "")
	t.Setenv("HEY_ACCOUNT_ID", "")
	dir := filepath.Join(os.Getenv("XDG_CONFIG_HOME"), configDirName)
	if err := os.MkdirAll(dir, 0700); err != nil {
		t.Fatal(err)
	}
	data := `{
		"base_url":"https://app.hey.com",
		"account_defaults":{
			"https://app.hey.com":"101",
			"https://staging.hey.com":"202"
		}
	}`
	if err := os.WriteFile(filepath.Join(dir, configFile), []byte(data), 0600); err != nil {
		t.Fatal(err)
	}

	cfg, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	if err := cfg.SetFromFlag("base_url", "https://staging.hey.com"); err != nil {
		t.Fatal(err)
	}
	if cfg.AccountID != "202" {
		t.Fatalf("staging account = %q, want 202", cfg.AccountID)
	}
	if err := cfg.SetFromFlag("base_url", "https://other.hey.com"); err != nil {
		t.Fatal(err)
	}
	if cfg.AccountID != AllAccounts || cfg.SourceOf("account_id") != SourceDefault {
		t.Fatalf("unknown-origin account = %q (%s), want all (default)", cfg.AccountID, cfg.SourceOf("account_id"))
	}
	if err := cfg.SetFromFlag("account_id", "303"); err != nil {
		t.Fatal(err)
	}
	if cfg.AccountID != "303" || cfg.SourceOf("account_id") != SourceFlag {
		t.Fatalf("explicit account = %q (%s), want 303 (flag)", cfg.AccountID, cfg.SourceOf("account_id"))
	}
}

func TestLocalConfigTrustTracksPathAndSensitiveValues(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(tmp, "config"))
	t.Setenv("HEY_BASE_URL", "")
	t.Setenv("HEY_ACCOUNT_ID", "")
	workspace := filepath.Join(tmp, "workspace")
	localDir := filepath.Join(workspace, ".hey")
	if err := os.MkdirAll(localDir, 0700); err != nil {
		t.Fatal(err)
	}
	t.Chdir(workspace)
	path := filepath.Join(localDir, configFile)
	if err := os.WriteFile(path, []byte(`{"base_url":"https://app.hey.com","account_id":"202"}`), 0600); err != nil {
		t.Fatal(err)
	}

	cfg, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	if cfg.UntrustedLocalConfig() == nil {
		t.Fatal("new local config was trusted without user approval")
	}
	if err := cfg.TrustLocalConfig(); err != nil {
		t.Fatal(err)
	}
	globalPath := filepath.Join(os.Getenv("XDG_CONFIG_HOME"), configDirName, configFile)
	if info, err := os.Stat(globalPath); err != nil || info.Mode().Perm() != 0600 {
		t.Fatalf("global config permissions = %v, %v; want 0600", info, err)
	}
	reloaded, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	if local := reloaded.LocalConfig(); local == nil || !local.Trusted || reloaded.UntrustedLocalConfig() != nil {
		t.Fatalf("trusted local config = %#v", local)
	}

	t.Setenv("HEY_BASE_URL", "https://staging.hey.com")
	otherServer, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	if otherServer.UntrustedLocalConfig() == nil {
		t.Fatal("local account trust carried to another server origin")
	}
	t.Setenv("HEY_BASE_URL", "")

	if err := os.WriteFile(path, []byte(`{"base_url":"https://app.hey.com","account_id":"303"}`), 0600); err != nil {
		t.Fatal(err)
	}
	changed, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	if local := changed.UntrustedLocalConfig(); local == nil || local.AccountID != "303" {
		t.Fatalf("changed local config did not invalidate trust: %#v", local)
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

func TestCoverRoundTrip(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", tmp)
	t.Setenv("HEY_BASE_URL", "")

	if got := Cover(); got != "" {
		t.Errorf("Cover before anything was saved = %q, want empty", got)
	}
	if err := SaveCover("topo"); err != nil {
		t.Fatalf("SaveCover: %v", err)
	}
	if got := Cover(); got != "topo" {
		t.Errorf("Cover = %q, want topo", got)
	}

	// Uncovering writes no key rather than a blank one.
	if err := SaveCover(""); err != nil {
		t.Fatalf("SaveCover(\"\"): %v", err)
	}
	if got := Cover(); got != "" {
		t.Errorf("Cover after uncovering = %q, want empty", got)
	}
	data, err := os.ReadFile(filepath.Join(tmp, "hey-cli", "config.json"))
	if err != nil {
		t.Fatalf("read config: %v", err)
	}
	var file map[string]any
	if err := json.Unmarshal(data, &file); err != nil {
		t.Fatalf("unmarshal config: %v", err)
	}
	if _, present := file["cover"]; present {
		t.Errorf("uncovering left a cover key behind: %s", data)
	}
}

// A cover is one setting in a shared file; saving it must not take the others out.
func TestSaveCoverKeepsOtherSettings(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", tmp)
	t.Setenv("HEY_BASE_URL", "")

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if err := cfg.SaveAccountID("12345"); err != nil {
		t.Fatalf("SaveAccountID: %v", err)
	}
	if err := cfg.SaveBaseURL("https://app.hey.localhost:3003"); err != nil {
		t.Fatalf("SaveBaseURL: %v", err)
	}
	before, err := loadGlobalFileConfig()
	if err != nil {
		t.Fatalf("read config: %v", err)
	}

	if err := SaveCover("peace"); err != nil {
		t.Fatalf("SaveCover: %v", err)
	}

	after, err := loadGlobalFileConfig()
	if err != nil {
		t.Fatalf("read config after saving: %v", err)
	}
	if after.BaseURL != before.BaseURL {
		t.Errorf("base_url = %q, want %q", after.BaseURL, before.BaseURL)
	}
	if !maps.Equal(after.AccountDefaults, before.AccountDefaults) {
		t.Errorf("account defaults = %v, want %v", after.AccountDefaults, before.AccountDefaults)
	}
	if after.Cover != "peace" {
		t.Errorf("cover = %q, want peace", after.Cover)
	}
}

func TestOnboardedRoundTrip(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", tmp)
	t.Setenv("HEY_BASE_URL", "")

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.Onboarded {
		t.Error("fresh config should not be onboarded")
	}

	if err := cfg.SaveOnboarded(true); err != nil {
		t.Fatalf("SaveOnboarded: %v", err)
	}
	if !cfg.Onboarded {
		t.Error("SaveOnboarded should update the in-memory config")
	}

	data, err := os.ReadFile(filepath.Join(tmp, configDirName, configFile))
	if err != nil {
		t.Fatalf("read config: %v", err)
	}
	var raw map[string]any
	if err := json.Unmarshal(data, &raw); err != nil {
		t.Fatalf("parse config: %v", err)
	}
	if onboarded, ok := raw["onboarded"].(bool); !ok || !onboarded {
		t.Errorf("config file onboarded = %v, want JSON bool true", raw["onboarded"])
	}

	reloaded, err := Load()
	if err != nil {
		t.Fatalf("reload: %v", err)
	}
	if !reloaded.Onboarded {
		t.Error("reloaded config should be onboarded")
	}
}

func TestOnboardedIgnoresLocalConfig(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", tmp)
	t.Setenv("HEY_BASE_URL", "")

	repo := t.TempDir()
	if err := os.MkdirAll(filepath.Join(repo, ".hey"), 0700); err != nil {
		t.Fatal(err)
	}
	data, _ := json.Marshal(map[string]any{"onboarded": true})
	if err := os.WriteFile(filepath.Join(repo, ".hey", configFile), data, 0600); err != nil {
		t.Fatal(err)
	}
	cwd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chdir(repo); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chdir(cwd) })

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.Onboarded {
		t.Error("a repository .hey/config.json must not set onboarded")
	}
}

func TestNonInteractiveEnv(t *testing.T) {
	tests := map[string]bool{
		"":        false,
		"true":    true,
		"1":       true,
		"TRUE":    true,
		"false":   false,
		"0":       false,
		"bananas": false,
	}
	for value, want := range tests {
		t.Run("value="+value, func(t *testing.T) {
			t.Setenv("HEY_NONINTERACTIVE", value)
			if got := NonInteractiveEnv(); got != want {
				t.Errorf("NonInteractiveEnv() with %q = %v, want %v", value, got, want)
			}
		})
	}
}

// A config rewrite must never delete state it does not understand: legacy
// embedded credentials awaiting migration and any future version's keys ride
// through verbatim. ScrubLegacyCredentials is the one deliberate removal.
func TestSaveGlobalConfigPreservesUnknownKeys(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", tmp)
	t.Setenv("HEY_BASE_URL", "")

	dir := filepath.Join(tmp, configDirName)
	if err := os.MkdirAll(dir, 0700); err != nil {
		t.Fatal(err)
	}
	seed := `{"base_url":"https://app.hey.com","access_token":"legacy-token","future_setting":{"nested":true}}`
	if err := os.WriteFile(filepath.Join(dir, configFile), []byte(seed), 0600); err != nil {
		t.Fatal(err)
	}

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if err := cfg.SaveOnboarded(true); err != nil {
		t.Fatalf("SaveOnboarded: %v", err)
	}

	raw, err := os.ReadFile(filepath.Join(dir, configFile))
	if err != nil {
		t.Fatal(err)
	}
	var saved map[string]any
	if err := json.Unmarshal(raw, &saved); err != nil {
		t.Fatal(err)
	}
	if saved["access_token"] != "legacy-token" {
		t.Errorf("rewrite deleted unmigrated credentials: %s", raw)
	}
	if _, ok := saved["future_setting"]; !ok {
		t.Errorf("rewrite deleted an unknown key: %s", raw)
	}
	if saved["onboarded"] != true {
		t.Errorf("rewrite lost its own change: %s", raw)
	}

	if err := ScrubLegacyCredentials(); err != nil {
		t.Fatalf("ScrubLegacyCredentials: %v", err)
	}
	raw, _ = os.ReadFile(filepath.Join(dir, configFile))
	saved = map[string]any{}
	_ = json.Unmarshal(raw, &saved)
	if _, ok := saved["access_token"]; ok {
		t.Errorf("scrub left migrated credentials behind: %s", raw)
	}
	if _, ok := saved["future_setting"]; !ok {
		t.Errorf("scrub removed an unrelated key: %s", raw)
	}
}
