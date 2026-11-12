package config

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func writeConfigFile(t *testing.T, path, body string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(body), 0600); err != nil {
		t.Fatal(err)
	}
}

func profileConfigHome(t *testing.T) string {
	t.Helper()
	tmp := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", tmp)
	t.Setenv("HEY_BASE_URL", "")
	t.Setenv("HEY_ACCOUNT_ID", "")
	t.Chdir(tmp)
	return tmp
}

// basecamp-cli's mergeProfile: an entry for the same server refines the farther one;
// another server, or another account on the same one, replaces it whole; an entry with
// no base_url is no entry.
func TestMergeProfileRefinesOrReplaces(t *testing.T) {
	profiles := map[string]*ProfileConfig{}
	mergeProfile(profiles, "work", profileEntry{BaseURL: "https://app.hey.com", AccountID: "42"})

	mergeProfile(profiles, "work", profileEntry{BaseURL: "https://APP.hey.com:443/"})
	if p := profiles["work"]; p.AccountID != "42" {
		t.Errorf("same server dropped the account: %#v", p)
	}
	mergeProfile(profiles, "work", profileEntry{BaseURL: "https://app.hey.com", AccountID: "7"})
	if p := profiles["work"]; p.AccountID != "7" {
		t.Errorf("another account did not replace: %#v", p)
	}
	mergeProfile(profiles, "work", profileEntry{BaseURL: "https://staging.hey.com"})
	if p := profiles["work"]; p.AccountID != "" || p.BaseURL != "https://staging.hey.com" {
		t.Errorf("another server carried the account across: %#v", p)
	}

	entries, err := parseProfiles("config.json", map[string]json.RawMessage{
		"bare":   json.RawMessage(`{"account_id":"1"}`),
		"number": json.RawMessage(`{"base_url":"https://app.hey.com","account_id":42}`),
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := entries["bare"]; ok {
		t.Error("an entry with no base_url was kept")
	}
	if entries["number"].AccountID != "42" {
		t.Errorf("numeric account = %#v", entries["number"])
	}
	if _, err := parseProfiles("config.json", map[string]json.RawMessage{"bad": json.RawMessage(`[]`)}); err == nil {
		t.Error("a profile that is not an object was accepted")
	}
	if _, err := parseProfiles("config.json", map[string]json.RawMessage{"bad": json.RawMessage(`{"base_url":"https://app.hey.com","account_id":"-1"}`)}); err == nil {
		t.Error("an invalid account was accepted")
	}
}

func TestApplyProfileTakesItsServerAndAccount(t *testing.T) {
	home := profileConfigHome(t)
	writeConfigFile(t, filepath.Join(home, configDirName, configFile), `{
		"account_defaults":{"https://app.hey.com":"9"},
		"profiles":{
			"second":{"base_url":"https://app.hey.com"},
			"staging":{"base_url":"https://staging.hey.com","account_id":"42"},
			"insecure":{"base_url":"http://staging.hey.com"}}}`)

	cfg, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	if cfg.AccountID != "9" || cfg.SourceOf("account_id") != SourceGlobal {
		t.Fatalf("top-level account = %q (%s)", cfg.AccountID, cfg.SourceOf("account_id"))
	}
	if err := cfg.ApplyProfile("second", SourceFlag); err != nil {
		t.Fatal(err)
	}
	if cfg.AccountID != AllAccounts || cfg.SourceOf("account_id") != SourceDefault || cfg.SourceOf("base_url") != SourceProfile {
		t.Errorf("second took the top-level sign-in's account: %q (%s)", cfg.AccountID, cfg.SourceOf("account_id"))
	}

	cfg, _ = Load()
	if err := cfg.ApplyProfile("staging", SourceFlag); err != nil {
		t.Fatal(err)
	}
	if cfg.BaseURL != "https://staging.hey.com" || cfg.AccountID != "42" || cfg.SourceOf("profile") != SourceFlag {
		t.Errorf("staging = %q %q", cfg.BaseURL, cfg.AccountID)
	}

	t.Setenv("HEY_BASE_URL", "https://beta.hey.com")
	cfg, _ = Load()
	if err := cfg.ApplyProfile("staging", SourceFlag); err == nil || cfg.ActiveProfile != "" {
		t.Errorf("HEY_BASE_URL on another server took the profile's credential there: %v", err)
	}
	t.Setenv("HEY_BASE_URL", "https://STAGING.hey.com:443")
	t.Setenv("HEY_ACCOUNT_ID", "5")
	cfg, _ = Load()
	if err := cfg.ApplyProfile("staging", SourceFlag); err != nil {
		t.Fatal(err)
	}
	if cfg.AccountID != "5" || cfg.SourceOf("base_url") != SourceEnv {
		t.Errorf("the environment lost to the profile: %q %q", cfg.BaseURL, cfg.AccountID)
	}
	if err := cfg.CheckProfileServer("--base-url", "https://beta.hey.com"); err == nil {
		t.Error("--base-url on another server was taken under a profile")
	}
	if err := cfg.CheckProfileServer("--base-url", "https://staging.hey.com/"); err != nil {
		t.Errorf("the profile's own server was refused: %v", err)
	}
	t.Setenv("HEY_ACCOUNT_ID", "")

	t.Setenv("HEY_BASE_URL", "")
	cfg, _ = Load()
	if err := cfg.ApplyProfile("insecure", SourceFlag); err == nil || !strings.Contains(err.Error(), "HTTPS") {
		t.Errorf("an insecure profile was applied: %v", err)
	}
	if cfg.ActiveProfile != "" {
		t.Errorf("a refused profile became active: %q", cfg.ActiveProfile)
	}
}

// A repository config trusted before profiles existed keeps its digest.
func TestLocalConfigDigestWithoutProfilesIsUnchanged(t *testing.T) {
	local := &LocalConfig{Path: "/workspace/.hey/config.json", BaseURL: "https://app.hey.com", AccountID: "42"}
	if err := fingerprintLocalConfig(local, "https://app.hey.com"); err != nil {
		t.Fatal(err)
	}
	payload, _ := json.Marshal(struct {
		BaseURL      string `json:"base_url,omitempty"`
		AccountID    string `json:"account_id,omitempty"`
		ServerOrigin string `json:"server_origin"`
	}{"https://app.hey.com", "42", "https://app.hey.com"})
	sum := sha256.Sum256(payload)
	if local.Digest != hex.EncodeToString(sum[:]) {
		t.Errorf("digest changed for a configuration with no profiles")
	}

	withProfiles := *local
	withProfiles.Profiles = map[string]any{"work": map[string]any{"base_url": "https://work.example.com"}}
	if err := fingerprintLocalConfig(&withProfiles, "https://app.hey.com"); err != nil {
		t.Fatal(err)
	}
	if withProfiles.Digest == local.Digest {
		t.Error("the digest does not cover the profiles")
	}
	withDefault := *local
	withDefault.DefaultProfile = "work"
	if err := fingerprintLocalConfig(&withDefault, "https://app.hey.com"); err != nil {
		t.Fatal(err)
	}
	if withDefault.Digest == local.Digest {
		t.Error("the digest does not cover default_profile")
	}
}

func TestProfileWritersKeepTheRestOfTheFile(t *testing.T) {
	home := profileConfigHome(t)
	path := filepath.Join(home, configDirName, configFile)
	writeConfigFile(t, path, `{"base_url":"https://app.hey.com","unknown":{"kept":true},"profiles":{"work":{"base_url":"https://app.hey.com","extra":"kept"}}}`)

	isDefault, err := RegisterProfile("beta", ProfileConfig{BaseURL: "https://beta.hey.com", AccountID: "3"})
	if err != nil || isDefault {
		t.Fatalf("register: %v, default %v", err, isDefault)
	}
	if err := SetDefaultProfile("beta"); err != nil {
		t.Fatal(err)
	}
	// An ordinary rewrite carries the profile keys through untouched.
	cfg, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	if err := cfg.SaveBaseURL("https://app.hey.com"); err != nil {
		t.Fatal(err)
	}
	if err := UnregisterProfile("beta"); err != nil {
		t.Fatal(err)
	}

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var written map[string]any
	if err := json.Unmarshal(data, &written); err != nil {
		t.Fatal(err)
	}
	profiles, _ := written["profiles"].(map[string]any)
	work, _ := profiles["work"].(map[string]any)
	if work["extra"] != "kept" || written["unknown"] == nil || profiles["beta"] != nil || written["default_profile"] != nil {
		t.Errorf("config = %s", data)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0600 {
		t.Errorf("mode = %v", info.Mode().Perm())
	}

	for _, body := range []string{`{"profiles":["work"]}`, `null`, `{`} {
		writeConfigFile(t, path, body)
		if err := GlobalConfigTakesProfiles(); err == nil || !strings.Contains(err.Error(), "refusing to rewrite it") {
			t.Errorf("%s: %v", body, err)
		}
	}
}

func TestValidProfileName(t *testing.T) {
	for name, want := range map[string]bool{
		"work": true, "staging-2": true, "beta_1": true, "A1": true,
		"": false, "-work": false, "_work": false, "bad name": false, "a/b": false, "..": false, "café": false,
	} {
		if got := ValidProfileName(name); got != want {
			t.Errorf("ValidProfileName(%q) = %v, want %v", name, got, want)
		}
	}
	for name, want := range map[string]bool{"work": true, ".": false, "..": false, "../x": false, "a/b": false} {
		if got := ProfileNameIsPathSafe(name); got != want {
			t.Errorf("ProfileNameIsPathSafe(%q) = %v, want %v", name, got, want)
		}
	}
}

// A repository's account_id was trusted for the files' server: it does not follow a
// profile that names no account to another one.
func TestARepositoryAccountStaysOnItsServer(t *testing.T) {
	home := profileConfigHome(t)
	writeConfigFile(t, filepath.Join(home, configDirName, configFile), `{"profiles":{
		"elsewhere":{"base_url":"https://staging.hey.com"},
		"here":{"base_url":"https://app.hey.com"}}}`)
	workspace := filepath.Join(home, "workspace")
	writeConfigFile(t, filepath.Join(workspace, ".hey", configFile), `{"account_id":"42"}`)
	t.Chdir(workspace)

	for name, want := range map[string]string{"elsewhere": AllAccounts, "here": "42"} {
		cfg, err := Load()
		if err != nil {
			t.Fatal(err)
		}
		if err := cfg.ApplyProfile(name, SourceFlag); err != nil {
			t.Fatal(err)
		}
		if cfg.AccountID != want {
			t.Errorf("%s: account = %q, want %q", name, cfg.AccountID, want)
		}
	}
}

func TestProfileAccountMustBeAStringOrNumber(t *testing.T) {
	if _, err := parseProfiles("config.json", map[string]json.RawMessage{"work": json.RawMessage(`{"base_url":"https://app.hey.com","account_id":true}`)}); err == nil || !strings.Contains(err.Error(), "account_id must be a string or number") {
		t.Errorf("account_id true: %v", err)
	}
}

// An entry the loader skips does not stop the first real profile becoming the default.
func TestTheFirstRealProfileBecomesTheDefault(t *testing.T) {
	home := profileConfigHome(t)
	writeConfigFile(t, filepath.Join(home, configDirName, configFile), `{"profiles":{"draft":{"account_id":"1"}}}`)
	isDefault, err := RegisterProfile("work", ProfileConfig{BaseURL: "https://app.hey.com"})
	if err != nil || !isDefault {
		t.Errorf("register: %v, default %v", err, isDefault)
	}
}

// An empty account_id reads as none, as the top-level account_id does.
func TestAnEmptyProfileAccountIsNone(t *testing.T) {
	profiles, err := parseProfiles("config.json", map[string]json.RawMessage{"work": json.RawMessage(`{"base_url":"https://app.hey.com","account_id":""}`)})
	if err != nil || profiles["work"].AccountID != "" {
		t.Errorf("profiles = %#v, %v", profiles, err)
	}
}
