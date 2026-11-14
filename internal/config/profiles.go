package config

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"

	"github.com/basecamp/hey-cli/internal/apierr"
	"github.com/basecamp/hey-cli/internal/terminal"
)

// Profiles are basecamp-cli's (internal/config/config.go, ProfileConfig, at
// 48b01aaf): a name for a server and the account on it, chosen per invocation by
// --profile, HEY_PROFILE or default_profile, each with a credential of its own. They
// let one install keep staging, a beta and a second account beside each other. With
// none configured, the top-level base_url and account defaults are all there is, as
// before profiles.
//
// profiles and default_profile choose where a credential is sent, so they are
// authority keys: the global config sets them, and a repository's .hey/config.json
// only once `hey config trust-local` has trusted its exact contents.

// ProfileConfig is one named profile.
type ProfileConfig struct {
	BaseURL   string `json:"base_url"`
	AccountID string `json:"account_id,omitempty"`
	// Endpoint is EndpointApp for a profile answered by the HEY app on this computer,
	// and "" (the server) otherwise. Only an experimental build honours it (ExperimentalApp).
	Endpoint string `json:"endpoint,omitempty"`
}

// profileEntry is a profile as one config file writes it.
type profileEntry struct {
	BaseURL   string
	AccountID string
	Endpoint  string
}

// The endpoints a profile names: HEY's server, or the HEY app on this computer, reached
// through the door it opens while "Let Agents Use HEY" is on.
const (
	EndpointServer = "server"
	EndpointApp    = "app"
)

// ExperimentalEnv turns on what is not ready to be anyone's default. Its value is a
// comma-separated list; "app" lets a profile's endpoint be the HEY app.
const ExperimentalEnv = "HEY_EXPERIMENTAL"

// ExperimentalApp reports whether HEY_EXPERIMENTAL lists "app". Without it a profile whose
// endpoint is the app is refused, and nothing about the app shows in help.
func ExperimentalApp() bool {
	for _, feature := range strings.Split(os.Getenv(ExperimentalEnv), ",") {
		if strings.TrimSpace(feature) == EndpointApp {
			return true
		}
	}
	return false
}

// ActiveEndpoint is the endpoint this invocation answers from: EndpointApp only for an
// active profile that names the app, which ApplyProfile has already refused unless
// ExperimentalApp.
func (c *Config) ActiveEndpoint() string {
	if p := c.Profiles[c.ActiveProfile]; c.ActiveProfile != "" && p != nil && p.Endpoint == EndpointApp {
		return EndpointApp
	}
	return EndpointServer
}

// SourcePrompt is a profile chosen at the terminal's picker.
const SourcePrompt Source = "prompt"

// SourceProfile is a value the active profile set.
const SourceProfile Source = "profile"

var validProfileName = regexp.MustCompile(`^[a-zA-Z0-9][a-zA-Z0-9_-]*$`)

// ValidProfileName reports whether name may name a new profile: letters, numbers,
// hyphens and underscores, as it becomes part of a credential key and a cache path.
func ValidProfileName(name string) bool {
	return validProfileName.MatchString(name)
}

// ProfileNameIsPathSafe reports whether a profile name stays one directory below the
// directory it is joined to. A name from a config file was never validated, so it
// becomes a path component only when it is a single one.
func ProfileNameIsPathSafe(name string) bool {
	return name != "." && filepath.IsLocal(name) && filepath.Base(name) == name
}

// ValidateBaseURL refuses a server URL hey would not send a credential to.
func ValidateBaseURL(base string) error {
	return validateBaseURL(base)
}

// NormalizeAccountID reads a linked mail account ID, or "all".
func NormalizeAccountID(accountID string) (string, error) {
	return normalizeAccountID(accountID)
}

// parseProfiles reads a config file's profiles. An entry with no base_url is left
// out, as if the file did not name the profile, unless it names the app as its endpoint:
// the app needs no server named, and takes HEY's own.
func parseProfiles(path string, raw map[string]json.RawMessage) (map[string]profileEntry, error) {
	if len(raw) == 0 {
		return nil, nil
	}
	entries := make(map[string]profileEntry, len(raw))
	for name, data := range raw {
		var fields map[string]any
		if err := json.Unmarshal(data, &fields); err != nil || fields == nil {
			return nil, fmt.Errorf("could not parse config %s: profile %q is not an object", path, name)
		}
		baseURL, _ := fields["base_url"].(string)
		endpoint, err := parseEndpoint(path, name, fields)
		if err != nil {
			return nil, err
		}
		if baseURL == "" && endpoint != EndpointApp {
			continue
		}
		if baseURL == "" {
			baseURL = defaultBase
		}
		entry := profileEntry{BaseURL: baseURL, Endpoint: endpoint}
		if raw, present := fields["account_id"]; present && raw != nil && raw != "" {
			account := stringOrNumber(raw)
			if account == "" {
				return nil, fmt.Errorf("could not parse config %s: profile %q: account_id must be a string or number", path, name)
			}
			normalized, err := normalizeAccountID(account)
			if err != nil {
				return nil, fmt.Errorf("could not parse config %s: profile %q: %w", path, name, err)
			}
			entry.AccountID = normalized
		}
		entries[name] = entry
	}
	return entries, nil
}

// parseEndpoint reads a profile's endpoint: "" for none or "server", EndpointApp for the
// app. Any other value is refused rather than read as the server, since a mistyped "app"
// would otherwise send the profile's requests to HEY's server.
func parseEndpoint(path, name string, fields map[string]any) (string, error) {
	raw, present := fields["endpoint"]
	if !present || raw == nil {
		return "", nil
	}
	endpoint, isString := raw.(string)
	switch {
	case isString && endpoint == "":
		return "", nil
	case isString && endpoint == EndpointServer:
		// Kept, not read as "", so a nearer file's "server" overrides a farther "app".
		return EndpointServer, nil
	case isString && endpoint == EndpointApp:
		return EndpointApp, nil
	default:
		return "", fmt.Errorf("could not parse config %s: profile %q: endpoint must be %q or %q", path, name, EndpointServer, EndpointApp)
	}
}

// EntryIsProfile reports whether a config file's entry names a profile, as parseProfiles
// reads it: one with a base_url, or one the app answers, which needs none.
func EntryIsProfile(fields map[string]any) bool {
	baseURL, _ := fields["base_url"].(string)
	endpoint, _ := fields["endpoint"].(string)
	return baseURL != "" || endpoint == EndpointApp
}

func stringOrNumber(value any) string {
	switch v := value.(type) {
	case string:
		return v
	case float64:
		return strconv.FormatFloat(v, 'f', -1, 64)
	default:
		return ""
	}
}

// mergeProfile layers one file's entry for a profile over the entry the farther files
// made of it, by basecamp-cli's rule: an entry for the same server (the same origin)
// refines it field by field, and one for another server, or for another account on
// the same one, replaces it whole, since an account on one server means nothing on
// another.
func mergeProfile(profiles map[string]*ProfileConfig, name string, entry profileEntry) {
	p := profiles[name]
	otherAccount := p != nil && p.AccountID != "" && entry.AccountID != "" && p.AccountID != entry.AccountID
	if p == nil || otherAccount || originOrSelf(p.BaseURL) != originOrSelf(entry.BaseURL) {
		p = &ProfileConfig{}
		profiles[name] = p
	}
	p.BaseURL = entry.BaseURL
	if entry.AccountID != "" {
		p.AccountID = entry.AccountID
	}
	if entry.Endpoint != "" {
		p.Endpoint = entry.Endpoint
	}
}

func originOrSelf(baseURL string) string {
	if origin, err := serverOrigin(baseURL); err == nil {
		return origin
	}
	return strings.TrimRight(baseURL, "/")
}

// applyFileProfiles adds a file's profiles and default_profile to the configuration.
func (c *Config) applyFileProfiles(file fileConfig, source Source) {
	if len(file.profiles) > 0 && c.Profiles == nil {
		c.Profiles = make(map[string]*ProfileConfig, len(file.profiles))
	}
	for name, entry := range file.profiles {
		if source == SourceGlobal {
			if c.globalProfiles == nil {
				c.globalProfiles = map[string]bool{}
			}
			c.globalProfiles[name] = true
		}
		// A credential is keyed by the profile's name, so a repository entry that
		// moved a global profile to another server would take its sign-in there.
		if existing := c.Profiles[name]; source == SourceLocal && existing != nil && originOrSelf(existing.BaseURL) != originOrSelf(entry.BaseURL) {
			c.warnings = append(c.warnings, fmt.Sprintf("warning: ignoring profile %q from local config at %s: it names %s, and the global config's %q is for %s",
				terminal.SanitizeLine(name), terminal.SanitizeLine(file.path), terminal.SanitizeLine(entry.BaseURL), terminal.SanitizeLine(name), terminal.SanitizeLine(existing.BaseURL)))
			continue
		}
		mergeProfile(c.Profiles, name, entry)
	}
	if file.defaultProfile != "" {
		c.DefaultProfile = file.defaultProfile
		c.sources["default_profile"] = source
	}
}

// ignoreLocalProfiles records why an untrusted repository's profile keys were left
// out, in basecamp-cli's words.
func (c *Config) ignoreLocalProfiles(path string, file fileConfig) {
	shown := terminal.SanitizeLine(path)
	remedy := "\n  (authority key from local/repo config; run `hey config trust-local` to allow)"
	if file.defaultProfile != "" {
		c.warnings = append(c.warnings, fmt.Sprintf("warning: ignoring default_profile %q from local config at %s%s", terminal.SanitizeLine(file.defaultProfile), shown, remedy))
	}
	if file.hasProfiles {
		c.warnings = append(c.warnings, fmt.Sprintf("warning: ignoring profiles from local config at %s%s", shown, remedy))
	}
}

// Warnings are what loading the configuration had to say: keys it ignored, and why.
func (c *Config) Warnings() []string {
	return c.warnings
}

// ProfileNames lists the configured profiles, sorted.
func (c *Config) ProfileNames() []string {
	names := make([]string, 0, len(c.Profiles))
	for name := range c.Profiles {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

// ApplyProfile makes name the active profile: its server and account replace the
// files' values, and HEY_BASE_URL and HEY_ACCOUNT_ID still win over it, as basecamp-cli
// re-applies its environment after the profile. Flags are the caller's to apply after.
// A repository's base_url is the files' and so loses to the profile's; its account_id
// stays only where the profile names none.
//
// A profile that names no account does not take the top-level account defaults, which
// belong to the top-level sign-in: a second account's profile on the same server would
// otherwise inherit the first one's mail account.
func (c *Config) ApplyProfile(name string, source Source) error {
	p, ok := c.Profiles[name]
	if !ok {
		return fmt.Errorf("profile %q not found", name)
	}
	if p.Endpoint == EndpointApp {
		if err := c.checkAppProfile(name, p); err != nil {
			return err
		}
	}
	if err := validateBaseURL(p.BaseURL); err != nil {
		return err
	}
	if c.sources == nil {
		c.sources = map[string]Source{}
	}
	// The credential is keyed by the profile's name, not by a server, so nothing
	// downstream would notice it going elsewhere: an override naming another server is
	// refused rather than sent the profile's token.
	if c.SourceOf("base_url") == SourceEnv && originOrSelf(c.BaseURL) != originOrSelf(p.BaseURL) {
		return errProfileServerOverride("HEY_BASE_URL", c.BaseURL, name, p.BaseURL)
	}
	filesOrigin := originOrSelf(c.BaseURL)
	c.ActiveProfile = name
	c.sources["profile"] = source
	if c.SourceOf("base_url") != SourceEnv {
		c.BaseURL = p.BaseURL
		c.sources["base_url"] = SourceProfile
	}
	switch {
	case c.SourceOf("account_id") == SourceEnv:
	case p.AccountID != "":
		c.AccountID = p.AccountID
		c.sources["account_id"] = SourceProfile
	case c.SourceOf("account_id") == SourceGlobal,
		// A repository's account was trusted for the files' server, not another one.
		c.SourceOf("account_id") == SourceLocal && filesOrigin != originOrSelf(p.BaseURL):
		c.AccountID = AllAccounts
		c.sources["account_id"] = SourceDefault
	}
	// The repository config's trust stays as the files had it: its record is for the
	// server the files name, and the profile keys it covers were honoured or ignored
	// by that record before any profile was chosen. Re-reading it against the
	// profile's server would ignore the profiles it had just approved on the next run.
	return nil
}

// checkAppProfile refuses a profile whose endpoint is the app where this run cannot honour
// it: a build without ExperimentalApp, which must not quietly answer from HEY's server
// instead, and a mail account named for it, since the app answers for whichever account
// its window shows.
func (c *Config) checkAppProfile(name string, p *ProfileConfig) error {
	if !ExperimentalApp() {
		return apierr.ErrUsageHint(
			fmt.Sprintf("profile %q is answered by the HEY app on this computer, which is not in this build of hey", terminal.SanitizeLine(name)),
			"Use another profile: hey profile list")
	}
	var by string
	switch {
	case p.AccountID != "":
		by = fmt.Sprintf("its account_id %s", p.AccountID)
	case c.SourceOf("account_id") == SourceEnv:
		by = "HEY_ACCOUNT_ID"
	case c.SourceOf("account_id") == SourceLocal:
		by = "this repository's account_id"
	default:
		return nil
	}
	return apierr.ErrUsageHint(
		fmt.Sprintf("profile %q is answered by the HEY app on this computer, which takes no mail account, but %s names one", terminal.SanitizeLine(name), by),
		"The app answers for the account its window shows: drop the account, or use a server profile")
}

// CheckProfileServer refuses a server override, named by, that is not the active
// profile's own server. hey config set base_url is no override: it sets the top-level
// URL, which no profile's credential uses.
func (c *Config) CheckProfileServer(by, value string) error {
	if c.ActiveProfile == "" {
		return nil
	}
	if p := c.Profiles[c.ActiveProfile]; p != nil && originOrSelf(value) != originOrSelf(p.BaseURL) {
		return errProfileServerOverride(by, value, c.ActiveProfile, p.BaseURL)
	}
	return nil
}

// ProfileInGlobalConfig reports whether the global config file defines name, as
// opposed to a trusted repository config alone.
func (c *Config) ProfileInGlobalConfig(name string) bool {
	return c.globalProfiles[name]
}

func errProfileServerOverride(by, override, name, profileURL string) error {
	return apierr.ErrUsageHint(
		fmt.Sprintf("%s %s is not profile %q's server (%s)", by, terminal.SanitizeLine(override), name, terminal.SanitizeLine(profileURL)),
		"A profile's credential goes only to its own server. Drop the override, or use another profile: hey profile create")
}

// GlobalConfigTakesProfiles reports whether the global config file can take a profile
// entry, without writing anything: the refusal the writers make, made before a step
// that cannot be taken back (a sign-in, a credential's removal).
func GlobalConfigTakesProfiles() error {
	data, path, err := loadGlobalConfigMap()
	if err != nil {
		return err
	}
	_, err = globalProfilesMap(data, path)
	return err
}

// RegisterProfile adds a profile to the global config file. The first profile
// registered becomes the default; isDefault reports whether this one did.
func RegisterProfile(name string, p ProfileConfig) (isDefault bool, err error) {
	data, path, err := loadGlobalConfigMap()
	if err != nil {
		return false, err
	}
	profiles, err := globalProfilesMap(data, path)
	if err != nil {
		return false, err
	}
	entry := map[string]any{"base_url": p.BaseURL}
	if p.AccountID != "" {
		entry["account_id"] = p.AccountID
	}
	encoded, err := json.Marshal(entry)
	if err != nil {
		return false, fmt.Errorf("could not marshal profile: %w", err)
	}
	profiles[name] = encoded
	// Counted as the loader counts them: an entry with no base_url is no profile, unless
	// the app answers it (EntryIsProfile).
	isDefault = true
	for other, raw := range profiles {
		var fields map[string]any
		if other == name || json.Unmarshal(raw, &fields) != nil {
			continue
		}
		if EntryIsProfile(fields) {
			isDefault = false
		}
	}
	if err := setRaw(data, "profiles", profiles); err != nil {
		return false, err
	}
	if isDefault {
		if err := setRaw(data, "default_profile", name); err != nil {
			return false, err
		}
	}
	return isDefault, writeGlobalConfigMap(path, data)
}

// UnregisterProfile removes a profile from the global config file, and default_profile
// when it named this one. Credentials are the caller's to remove.
func UnregisterProfile(name string) error {
	data, path, err := loadGlobalConfigMap()
	if err != nil {
		return err
	}
	profiles, err := globalProfilesMap(data, path)
	if err != nil {
		return err
	}
	delete(profiles, name)
	if len(profiles) == 0 {
		delete(data, "profiles")
	} else if err := setRaw(data, "profiles", profiles); err != nil {
		return err
	}
	var current string
	if raw, ok := data["default_profile"]; ok && json.Unmarshal(raw, &current) == nil && current == name {
		delete(data, "default_profile")
	}
	return writeGlobalConfigMap(path, data)
}

// SetDefaultProfile names the profile used when nothing else chooses one.
func SetDefaultProfile(name string) error {
	data, path, err := loadGlobalConfigMap()
	if err != nil {
		return err
	}
	if err := setRaw(data, "default_profile", name); err != nil {
		return err
	}
	return writeGlobalConfigMap(path, data)
}

// bindProfileAccount sets the account on an existing profile entry in the global config
// file, leaving every other field of the entry as it is.
func bindProfileAccount(name, account string) error {
	data, path, err := loadGlobalConfigMap()
	if err != nil {
		return err
	}
	profiles, err := globalProfilesMap(data, path)
	if err != nil {
		return err
	}
	var entry map[string]any
	if raw, ok := profiles[name]; !ok || json.Unmarshal(raw, &entry) != nil || entry == nil {
		return apierr.ErrUsage(fmt.Sprintf("Profile %q not found in %s", name, path))
	}
	entry["account_id"] = account
	encoded, err := json.Marshal(entry)
	if err != nil {
		return fmt.Errorf("could not marshal profile: %w", err)
	}
	profiles[name] = encoded
	if err := setRaw(data, "profiles", profiles); err != nil {
		return err
	}
	return writeGlobalConfigMap(path, data)
}

// loadGlobalConfigMap returns the global config file's top-level keys and its path. A
// missing file is an empty config; one that cannot be read or parsed is refused, since
// writing the map back would replace whatever was there with a partial decode.
func loadGlobalConfigMap() (map[string]json.RawMessage, string, error) {
	path := globalConfigPath()
	if path == "" {
		return nil, "", fmt.Errorf("could not determine config path")
	}
	data := map[string]json.RawMessage{}
	contents, err := os.ReadFile(path) // #nosec G304 -- the fixed global config path
	if os.IsNotExist(err) {
		return data, path, nil
	}
	if err != nil {
		return nil, path, fmt.Errorf("failed to read config file %s: %w", path, err)
	}
	if err := json.Unmarshal(contents, &data); err != nil {
		return nil, path, fmt.Errorf("config file %s is not valid JSON, refusing to rewrite it: %w", path, err)
	}
	if data == nil {
		return nil, path, fmt.Errorf("config file %s is not a JSON object, refusing to rewrite it", path)
	}
	return data, path, nil
}

// globalProfilesMap returns the config's profiles object, empty when absent. A present
// value of any other shape is refused, as a parse failure is.
func globalProfilesMap(data map[string]json.RawMessage, path string) (map[string]json.RawMessage, error) {
	raw, present := data["profiles"]
	if !present {
		return map[string]json.RawMessage{}, nil
	}
	var profiles map[string]json.RawMessage
	if err := json.Unmarshal(raw, &profiles); err != nil || profiles == nil {
		return nil, fmt.Errorf("config file %s has a \"profiles\" value that is not an object, refusing to rewrite it", path)
	}
	return profiles, nil
}

func setRaw(data map[string]json.RawMessage, key string, value any) error {
	encoded, err := json.Marshal(value)
	if err != nil {
		return fmt.Errorf("could not marshal config: %w", err)
	}
	data[key] = encoded
	return nil
}
