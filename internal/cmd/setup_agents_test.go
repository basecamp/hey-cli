package cmd

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/basecamp/hey-cli/internal/apierr"
	"github.com/basecamp/hey-cli/internal/output"
)

// runSetupAgents runs `hey setup agents --json` against an isolated HOME with
// the given agent directories present and the selector set.
func runSetupAgents(t *testing.T, selector string, agentDirs ...string) (map[string]any, output.Response) {
	t.Helper()
	isolateAgents(t)
	t.Setenv(agentSetupEnv, selector)
	home := t.TempDir()
	for _, dir := range agentDirs {
		if err := os.MkdirAll(filepath.Join(home, dir), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Errorf("unexpected HTTP request: %s %s", r.Method, r.URL.Path)
	}))
	t.Cleanup(server.Close)

	_, response, err := runAuthCommand(t, home, server.URL, "", true, "setup", "agents")
	if err != nil {
		t.Fatalf("setup agents: %v", err)
	}
	data, ok := response.Data.(map[string]any)
	if !ok {
		t.Fatalf("data = %T", response.Data)
	}
	if data["skill_installed"] != true {
		t.Errorf("baseline skill must always be installed: %v", data)
	}
	if _, err := os.Stat(filepath.Join(home, ".agents", "skills", "hey", "SKILL.md")); err != nil {
		t.Errorf("baseline skill file missing: %v", err)
	}
	return data, response
}

func stringList(t *testing.T, value any) []string {
	t.Helper()
	items, ok := value.([]any)
	if !ok {
		t.Fatalf("expected a list, got %T (%v)", value, value)
	}
	out := make([]string, 0, len(items))
	for _, item := range items {
		out = append(out, item.(string))
	}
	return out
}

func TestSetupAgentsRejectsPositionalArgs(t *testing.T) {
	isolateAgents(t)
	_, _, err := runAuthCommand(t, t.TempDir(), "http://app.hey.localhost:3003", "", true, "setup", "agents", "claude")
	if err == nil || !strings.Contains(err.Error(), "unknown command") && !strings.Contains(err.Error(), "arg") {
		t.Fatalf("positional args must be rejected, got %v", err)
	}
}

func TestSetupAgentsNoAgentsDetectedInstallsSkillOnly(t *testing.T) {
	data, response := runSetupAgents(t, "")
	if data["selector"] != "auto" || data["ambiguous"] != false {
		t.Errorf("selector/ambiguous = %v/%v", data["selector"], data["ambiguous"])
	}
	if got := stringList(t, data["attempted_agents"]); len(got) != 0 {
		t.Errorf("attempted = %v", got)
	}
	if response.Summary != "Installed baseline skill; no coding agents connected" {
		t.Errorf("summary = %q", response.Summary)
	}
}

func TestSetupAgentsSingleDetectedAgentIsConnected(t *testing.T) {
	data, response := runSetupAgents(t, "", ".codex")
	if got := stringList(t, data["attempted_agents"]); len(got) != 1 || got[0] != "codex" {
		t.Errorf("attempted = %v", got)
	}
	if got := stringList(t, data["errors"]); len(got) != 0 {
		t.Errorf("errors = %v", got)
	}
	agents := data["agents"].([]any)
	if len(agents) != 1 || agents[0].(map[string]any)["plugin_installed"] != true {
		t.Errorf("agents = %v", agents)
	}
	if response.Summary != "Installed baseline skill; connected Codex" {
		t.Errorf("summary = %q", response.Summary)
	}
}

func TestSetupAgentsAmbiguousDetectionNeverGuesses(t *testing.T) {
	data, response := runSetupAgents(t, "", ".claude", ".codex")
	if data["ambiguous"] != true {
		t.Errorf("ambiguous = %v", data["ambiguous"])
	}
	if got := stringList(t, data["attempted_agents"]); len(got) != 0 {
		t.Errorf("attempted = %v, ambiguity must not connect anyone", got)
	}
	manual := stringList(t, data["manual_commands"])
	if len(manual) != 2 || manual[0] != "hey setup claude" || manual[1] != "hey setup codex" {
		t.Errorf("manual_commands = %v", manual)
	}
	if response.Summary != "Multiple coding agents detected; installed baseline skill only" {
		t.Errorf("summary = %q", response.Summary)
	}
}

func TestSetupAgentsAllAttemptsEveryAgent(t *testing.T) {
	data, response := runSetupAgents(t, "all", ".claude", ".codex")
	if got := stringList(t, data["attempted_agents"]); len(got) != 2 || got[0] != "claude" || got[1] != "codex" {
		t.Errorf("attempted = %v", got)
	}
	// Claude cannot be connected without its binary: an error, a warning and
	// manual remediation, never a silent success.
	errs := stringList(t, data["errors"])
	if len(errs) == 0 || !strings.HasPrefix(errs[0], "claude: ") {
		t.Errorf("errors = %v", errs)
	}
	warnings := stringList(t, data["warnings"])
	if len(warnings) == 0 || !strings.Contains(warnings[0], "Claude Code binary not found") {
		t.Errorf("warnings = %v", warnings)
	}
	manual := stringList(t, data["manual_commands"])
	if !contains(manual, "claude plugin install hey@37signals") || !contains(manual, "hey setup claude") {
		t.Errorf("manual_commands = %v", manual)
	}
	if response.Summary != "Installed baseline skill; attempted Claude Code and Codex" {
		t.Errorf("summary = %q", response.Summary)
	}
}

func TestSetupAgentsNoneInstallsSkillOnly(t *testing.T) {
	data, _ := runSetupAgents(t, "none", ".claude", ".codex")
	if data["selector"] != "none" {
		t.Errorf("selector = %v", data["selector"])
	}
	if got := stringList(t, data["attempted_agents"]); len(got) != 0 {
		t.Errorf("attempted = %v", got)
	}
}

func TestSetupAgentsExplicitSelectorTargetsThatAgent(t *testing.T) {
	data, _ := runSetupAgents(t, "Codex", ".claude", ".codex")
	if data["selector"] != "codex" {
		t.Errorf("selector = %v", data["selector"])
	}
	if got := stringList(t, data["attempted_agents"]); len(got) != 1 || got[0] != "codex" {
		t.Errorf("attempted = %v", got)
	}
}

func TestSetupAgentsInvalidSelectorWarns(t *testing.T) {
	data, response := runSetupAgents(t, "bogus", ".codex")
	if data["selector"] != "invalid" {
		t.Errorf("selector = %v", data["selector"])
	}
	if got := stringList(t, data["attempted_agents"]); len(got) != 0 {
		t.Errorf("attempted = %v", got)
	}
	warnings := stringList(t, data["warnings"])
	if len(warnings) != 1 || !strings.Contains(warnings[0], `Unknown HEY_SETUP_AGENT value "bogus"`) {
		t.Errorf("warnings = %v", warnings)
	}
	if response.Summary != "Unknown HEY_SETUP_AGENT value; installed baseline skill only" {
		t.Errorf("summary = %q", response.Summary)
	}
}

func TestSetupAgentCommandEnvelope(t *testing.T) {
	isolateAgents(t)
	home := t.TempDir()
	if err := os.MkdirAll(filepath.Join(home, ".codex"), 0o755); err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(http.NotFoundHandler())
	defer server.Close()

	_, response, err := runAuthCommand(t, home, server.URL, "", true, "setup", "codex")
	if err != nil {
		t.Fatalf("setup codex: %v", err)
	}
	data := response.Data.(map[string]any)
	if data["agent_detected"] != true || data["plugin_installed"] != true {
		t.Errorf("data = %v", data)
	}
	if response.Summary != "Codex connected" {
		t.Errorf("summary = %q", response.Summary)
	}

	// An explicitly requested integration that is not detected is a failed
	// command: error envelope, nonzero exit.
	_, _, err = runAuthCommand(t, home, server.URL, "", true, "setup", "claude")
	var cliErr *apierr.Error
	if !errors.As(err, &cliErr) || cliErr.Code != "setup_incomplete" || cliErr.Message != "Claude Code not detected" {
		t.Fatalf("error = %v, want setup_incomplete/Claude Code not detected", err)
	}
}

func TestJoinNames(t *testing.T) {
	tests := map[string][]string{
		"":                               nil,
		"Claude Code":                    {"Claude Code"},
		"Claude Code and Codex":          {"Claude Code", "Codex"},
		"Claude Code, Codex, and Cursor": {"Claude Code", "Codex", "Cursor"},
	}
	for want, names := range tests {
		if got := joinNames(names); got != want {
			t.Errorf("joinNames(%v) = %q, want %q", names, got, want)
		}
	}
}

// The installer's automatic handoff must never destroy a hand-authored
// baseline skill, whatever the selector: it reports the refusal instead.
func TestSetupAgentsPreservesUnmarkedBaselineSkill(t *testing.T) {
	isolateAgents(t)
	t.Setenv(agentSetupEnv, "none")
	home := t.TempDir()
	custom := "# my own hey skill\n"
	dir := filepath.Join(home, ".agents", "skills", "hey")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "SKILL.md"), []byte(custom), 0o600); err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(http.NotFoundHandler())
	defer server.Close()

	_, response, err := runAuthCommand(t, home, server.URL, "", true, "setup", "agents")
	if err != nil {
		t.Fatalf("setup agents: %v", err)
	}
	data := response.Data.(map[string]any)
	if data["skill_installed"] != false {
		t.Error("must not report the skill installed over a refused directory")
	}
	errs := stringList(t, data["errors"])
	if len(errs) != 1 || !strings.Contains(errs[0], "not written by hey-cli") {
		t.Errorf("errors = %v", errs)
	}
	if got, _ := os.ReadFile(filepath.Join(dir, "SKILL.md")); string(got) != custom {
		t.Errorf("user skill changed: %q", got)
	}
	if ownedSkillDir(dir) {
		t.Error("user directory was claimed")
	}
	if response.Summary != "Baseline skill installation failed" {
		t.Errorf("summary = %q", response.Summary)
	}
}

// `hey setup codex` on a machine without Codex must not create ~/.codex and
// then count its own creation as detection.
func TestSetupCodexDoesNotFabricateCodex(t *testing.T) {
	isolateAgents(t)
	home := t.TempDir()
	server := httptest.NewServer(http.NotFoundHandler())
	defer server.Close()

	_, _, err := runAuthCommand(t, home, server.URL, "", true, "setup", "codex")
	var cliErr *apierr.Error
	if !errors.As(err, &cliErr) || cliErr.Code != "setup_incomplete" || cliErr.Message != "Codex not detected" {
		t.Fatalf("error = %v, want setup_incomplete/Codex not detected", err)
	}
	if _, err := os.Stat(filepath.Join(home, ".codex")); !os.IsNotExist(err) {
		t.Error("~/.codex was fabricated")
	}
}

// A styled `hey setup <agent>` that did not connect must say so and exit
// nonzero — never "start a new session" over a failed integration.
func TestSetupAgentStyledReportsNotConnected(t *testing.T) {
	isolateAgents(t)
	home := t.TempDir()
	if err := os.MkdirAll(filepath.Join(home, ".claude"), 0o755); err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(http.NotFoundHandler())
	defer server.Close()
	origColor := colorDisabled
	colorDisabled = true
	t.Cleanup(func() { colorDisabled = origColor })

	stdout, _, err := runAuthCommand(t, home, server.URL, "", false, "setup", "claude", "--styled")
	var cliErr *apierr.Error
	if !errors.As(err, &cliErr) || cliErr.Code != "setup_incomplete" {
		t.Fatalf("error = %v, want setup_incomplete", err)
	}
	if strings.Contains(stdout, "Start a new Claude Code session") {
		t.Errorf("claimed success over a failed setup:\n%s", stdout)
	}
	if !strings.Contains(stdout, "Claude Code setup failed") {
		t.Errorf("failure not surfaced:\n%s", stdout)
	}
}

// Commands that never touch the server must not move secrets around: the
// installer runs `setup agents` with HEY_NO_KEYRING=1, which would otherwise
// migrate legacy config.json tokens into plaintext credentials.json.
func TestSetupAgentsDoesNotMigrateLegacyCredentials(t *testing.T) {
	isolateAgents(t)
	t.Setenv(agentSetupEnv, "none")
	home := t.TempDir()
	configDir := filepath.Join(home, "hey-cli")
	if err := os.MkdirAll(configDir, 0o700); err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(http.NotFoundHandler())
	defer server.Close()
	legacy := `{"base_url":"` + server.URL + `","access_token":"legacy-token"}`
	if err := os.WriteFile(filepath.Join(configDir, "config.json"), []byte(legacy), 0o600); err != nil {
		t.Fatal(err)
	}

	if _, _, err := runAuthCommand(t, home, server.URL, "", true, "setup", "agents"); err != nil {
		t.Fatalf("setup agents: %v", err)
	}
	if _, err := os.Stat(filepath.Join(configDir, "credentials.json")); !os.IsNotExist(err) {
		t.Fatal("setup agents migrated legacy credentials into credentials.json")
	}

	// A command that does use credentials still migrates them.
	if _, _, err := runAuthCommand(t, home, server.URL, "", true, "auth", "status"); err != nil {
		t.Fatalf("auth status: %v", err)
	}
	if _, err := os.Stat(filepath.Join(configDir, "credentials.json")); err != nil {
		t.Errorf("auth status should have migrated legacy credentials: %v", err)
	}
}

// A targeted agent whose skill path is occupied by an unmanaged skill is a
// conflict, not a connection: the presence check alone must not flip
// plugin_installed back to true over the handler's refusal.
func TestSetupAgentsConflictedInstallIsNotConnected(t *testing.T) {
	isolateAgents(t)
	t.Setenv(agentSetupEnv, "codex")
	home := t.TempDir()
	custom := "# my own hey skill\n"
	codexSkill := filepath.Join(home, ".codex", "skills", "hey")
	if err := os.MkdirAll(codexSkill, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(codexSkill, "SKILL.md"), []byte(custom), 0o600); err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(http.NotFoundHandler())
	defer server.Close()

	_, response, err := runAuthCommand(t, home, server.URL, "", true, "setup", "agents")
	if err != nil {
		t.Fatalf("setup agents: %v", err)
	}
	data := response.Data.(map[string]any)
	errs := stringList(t, data["errors"])
	if len(errs) == 0 || !strings.Contains(errs[0], "not written by hey-cli") {
		t.Fatalf("errors = %v", errs)
	}
	agents := data["agents"].([]any)
	if agents[0].(map[string]any)["plugin_installed"] != false {
		t.Error("a refused install must not report plugin_installed")
	}
	if response.Summary != "Installed baseline skill; attempted Codex" {
		t.Errorf("summary = %q", response.Summary)
	}
	if got, _ := os.ReadFile(filepath.Join(codexSkill, "SKILL.md")); string(got) != custom {
		t.Errorf("user skill changed: %q", got)
	}
}

// Config-writing commands rewrite config.json through a struct with no
// credential fields, so they must migrate legacy credentials FIRST — skipping
// migration there would delete the tokens instead of moving them.
func TestConfigSetMigratesLegacyCredentialsBeforeRewriting(t *testing.T) {
	isolateAgents(t)
	home := t.TempDir()
	configDir := filepath.Join(home, "hey-cli")
	if err := os.MkdirAll(configDir, 0o700); err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(http.NotFoundHandler())
	defer server.Close()
	legacy := `{"base_url":"` + server.URL + `","access_token":"legacy-token","refresh_token":"legacy-refresh"}`
	if err := os.WriteFile(filepath.Join(configDir, "config.json"), []byte(legacy), 0o600); err != nil {
		t.Fatal(err)
	}

	if _, _, err := runAuthCommand(t, home, server.URL, "", true, "config", "set", "onboarded", "true"); err != nil {
		t.Fatalf("config set: %v", err)
	}
	creds, err := os.ReadFile(filepath.Join(configDir, "credentials.json"))
	if err != nil {
		t.Fatalf("legacy credentials were not migrated before the rewrite: %v", err)
	}
	if !strings.Contains(string(creds), "legacy-token") {
		t.Errorf("migrated credentials missing the token: %s", creds)
	}
}

// When migration itself fails, the legacy credentials must survive every
// config rewrite — including the wizard's onboarded flag — until a later run
// migrates them.
func TestConfigRewritesPreserveLegacyCredentialsWhenMigrationFails(t *testing.T) {
	isolateAgents(t)
	home := t.TempDir()
	configDir := filepath.Join(home, "hey-cli")
	if err := os.MkdirAll(configDir, 0o700); err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(http.NotFoundHandler())
	defer server.Close()
	legacy := `{"base_url":"` + server.URL + `","access_token":"legacy-token"}`
	if err := os.WriteFile(filepath.Join(configDir, "config.json"), []byte(legacy), 0o600); err != nil {
		t.Fatal(err)
	}
	// A directory where credentials.json should be makes the file store's
	// load and save both fail, so migration cannot complete.
	if err := os.MkdirAll(filepath.Join(configDir, "credentials.json"), 0o700); err != nil {
		t.Fatal(err)
	}

	if _, _, err := runAuthCommand(t, home, server.URL, "", true, "config", "set", "onboarded", "true"); err != nil {
		t.Fatalf("config set: %v", err)
	}
	raw, err := os.ReadFile(filepath.Join(configDir, "config.json"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(raw), "legacy-token") {
		t.Fatalf("failed migration + rewrite deleted the only credentials: %s", raw)
	}
	if !strings.Contains(string(raw), `"onboarded": true`) {
		t.Errorf("rewrite lost its own change: %s", raw)
	}
}

// An agent detected by its home directory alone, whose setup succeeded,
// gets no missing-binary remediation: the warning is for absences that
// actually prevented the connection.
func TestSetupAgentsNoBinaryWarningAfterSuccessfulSetup(t *testing.T) {
	data, response := runSetupAgents(t, "", ".codex")
	if got := stringList(t, data["warnings"]); len(got) != 0 {
		t.Errorf("warnings = %v, want none after a successful skill-only setup", got)
	}
	if got := stringList(t, data["manual_commands"]); len(got) != 0 {
		t.Errorf("manual_commands = %v, want none after success", got)
	}
	if response.Summary != "Installed baseline skill; connected Codex" {
		t.Errorf("summary = %q", response.Summary)
	}
}

// A symlinked baseline SKILL.md is the state every write path refuses, so
// the read-side predicates must not report it healthy.
func TestBaselineSkillInstalledRequiresRegularFile(t *testing.T) {
	isolateAgents(t)
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	dir := filepath.Join(home, ".agents", "skills", "hey")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	elsewhere := filepath.Join(home, "elsewhere.md")
	if err := os.WriteFile(elsewhere, []byte("# elsewhere"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(elsewhere, filepath.Join(dir, "SKILL.md")); err != nil {
		t.Fatal(err)
	}
	writeOwnershipMarker(dir)
	if baselineSkillInstalled() {
		t.Error("a symlinked SKILL.md must not count as installed, marker or not")
	}

	// A regular file without the marker is a hand-authored skill: refused by
	// install, so never reported as a healthy installation either.
	if err := os.Remove(filepath.Join(dir, "SKILL.md")); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(filepath.Join(dir, ownershipMarkerFile)); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "SKILL.md"), []byte("# mine"), 0o644); err != nil {
		t.Fatal(err)
	}
	if baselineSkillInstalled() {
		t.Error("an unmarked SKILL.md must not count as installed")
	}
	writeOwnershipMarker(dir)
	if !baselineSkillInstalled() {
		t.Error("a marked regular SKILL.md is installed")
	}
}

// A prior run's save-success/scrub-failure must not strand secrets in
// config.json forever: with a usable stored credential, the next run retries
// the scrub — removing the legacy fields, preserving unrelated keys, and
// leaving the stored credentials untouched.
func TestMigrationRetriesScrubWhenStoreAlreadyPopulated(t *testing.T) {
	isolateAgents(t)
	home := t.TempDir()
	configDir := filepath.Join(home, "hey-cli")
	if err := os.MkdirAll(configDir, 0o700); err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(http.NotFoundHandler())
	defer server.Close()

	// A populated credential store, as a completed earlier migration leaves it.
	if _, _, err := runAuthCommand(t, home, server.URL, "", true, "auth", "login", "--token", "stored-token"); err != nil {
		t.Fatalf("auth login: %v", err)
	}
	storedBefore, err := os.ReadFile(filepath.Join(configDir, "credentials.json"))
	if err != nil {
		t.Fatal(err)
	}

	// The scrub failed back then: legacy fields still sit in config.json,
	// alongside a key from some future version.
	raw, err := os.ReadFile(filepath.Join(configDir, "config.json"))
	if os.IsNotExist(err) {
		raw = []byte("{}")
	} else if err != nil {
		t.Fatal(err)
	}
	var cfgMap map[string]any
	if err := json.Unmarshal(raw, &cfgMap); err != nil {
		t.Fatal(err)
	}
	cfgMap["base_url"] = server.URL
	cfgMap["access_token"] = "legacy-token"
	cfgMap["session_cookie"] = "legacy-cookie"
	cfgMap["future_setting"] = map[string]any{"nested": true}
	seeded, _ := json.Marshal(cfgMap)
	if err := os.WriteFile(filepath.Join(configDir, "config.json"), seeded, 0o600); err != nil {
		t.Fatal(err)
	}

	if _, _, err := runAuthCommand(t, home, server.URL, "", true, "auth", "status"); err != nil {
		t.Fatalf("auth status: %v", err)
	}

	after, err := os.ReadFile(filepath.Join(configDir, "config.json"))
	if err != nil {
		t.Fatal(err)
	}
	var saved map[string]any
	if err := json.Unmarshal(after, &saved); err != nil {
		t.Fatal(err)
	}
	if _, ok := saved["access_token"]; ok {
		t.Errorf("legacy token not scrubbed on retry: %s", after)
	}
	if _, ok := saved["session_cookie"]; ok {
		t.Errorf("legacy cookie not scrubbed on retry: %s", after)
	}
	if _, ok := saved["future_setting"]; !ok {
		t.Errorf("unrelated key destroyed by the retry: %s", after)
	}
	storedAfter, err := os.ReadFile(filepath.Join(configDir, "credentials.json"))
	if err != nil || string(storedAfter) != string(storedBefore) {
		t.Errorf("stored credentials changed during the retry: %v", err)
	}
}

func TestSetupAgentsRemoveDeletesManagedSkillsAndPreservesUserFiles(t *testing.T) {
	isolateAgents(t)
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	for _, dir := range []string{".claude", ".codex"} {
		if err := os.MkdirAll(filepath.Join(home, dir), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := installSkillFiles(); err != nil {
		t.Fatal(err)
	}
	if _, err := linkSkillToClaude(); err != nil {
		t.Fatal(err)
	}
	if _, err := installSkillToCodex(); err != nil {
		t.Fatal(err)
	}
	baseline := filepath.Join(home, ".agents", "skills", "hey")
	if err := os.WriteFile(filepath.Join(baseline, "notes.txt"), []byte("keep me"), 0o600); err != nil {
		t.Fatal(err)
	}

	server := httptest.NewServer(http.NotFoundHandler())
	defer server.Close()
	_, response, err := runAuthCommand(t, home, server.URL, "", true, "setup", "agents", "--remove")
	if err != nil {
		t.Fatalf("setup agents --remove: %v", err)
	}
	if response.Summary != "Coding-agent integrations removed" {
		t.Errorf("summary = %q", response.Summary)
	}
	for _, path := range []string{
		filepath.Join(home, ".claude", "skills", "hey"),
		filepath.Join(home, ".codex", "skills", "hey"),
		filepath.Join(baseline, skillFilename),
		filepath.Join(baseline, ownershipMarkerFile),
	} {
		if _, statErr := os.Lstat(path); !os.IsNotExist(statErr) {
			t.Errorf("managed path remains: %s", path)
		}
	}
	if data, readErr := os.ReadFile(filepath.Join(baseline, "notes.txt")); readErr != nil || string(data) != "keep me" {
		t.Errorf("user file = %q, %v", data, readErr)
	}
}

func TestSetupAgentsRemovePreservesUnmanagedSkills(t *testing.T) {
	isolateAgents(t)
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	paths := []string{
		filepath.Join(home, ".agents", "skills", "hey"),
		filepath.Join(home, ".claude", "skills", "hey"),
		filepath.Join(home, ".codex", "skills", "hey"),
	}
	for _, path := range paths {
		if err := os.MkdirAll(path, 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(path, skillFilename), []byte("user skill"), 0o600); err != nil {
			t.Fatal(err)
		}
	}

	server := httptest.NewServer(http.NotFoundHandler())
	defer server.Close()
	if _, _, err := runAuthCommand(t, home, server.URL, "", true, "setup", "agents", "--remove"); err != nil {
		t.Fatalf("setup agents --remove: %v", err)
	}
	for _, path := range paths {
		data, readErr := os.ReadFile(filepath.Join(path, skillFilename))
		if readErr != nil || string(data) != "user skill" {
			t.Errorf("unmanaged skill %s = %q, %v", path, data, readErr)
		}
	}
}

func TestClaudePluginStepsForceGitHubHTTPS(t *testing.T) {
	isolateAgents(t)
	var gotEnv []string
	stubRunAgentCommand(t, func(ctx context.Context, _ string, _ ...string) ([]byte, error) {
		gotEnv = agentCommandEnvironment(ctx)
		return nil, nil
	})

	if _, err := runClaudePluginStep(context.Background(), time.Second, "claude", "plugin", "install", "hey@37signals"); err != nil {
		t.Fatal(err)
	}
	want := []string{
		"GIT_CONFIG_GLOBAL=" + os.DevNull,
		"GIT_CONFIG_COUNT=2",
		"GIT_CONFIG_KEY_0=url.https://github.com/.insteadOf",
		"GIT_CONFIG_VALUE_0=git@github.com:",
		"GIT_CONFIG_KEY_1=url.https://github.com/.insteadOf",
		"GIT_CONFIG_VALUE_1=ssh://git@github.com/",
	}
	if strings.Join(gotEnv, "|") != strings.Join(want, "|") {
		t.Errorf("command environment = %v, want %v", gotEnv, want)
	}
}

func TestCommandEnvironmentReplacesExistingValues(t *testing.T) {
	got := commandEnvironment(
		[]string{"PATH=/usr/bin", "GIT_CONFIG_GLOBAL=/home/jane/.gitconfig"},
		[]string{"GIT_CONFIG_GLOBAL=" + os.DevNull},
	)
	if strings.Join(got, "|") != "PATH=/usr/bin|GIT_CONFIG_GLOBAL="+os.DevNull {
		t.Errorf("environment = %v", got)
	}
}

func TestSetupAgentsRemoveUninstallsClaudePlugin(t *testing.T) {
	isolateAgents(t)
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	pluginsDir := filepath.Join(home, ".claude", "plugins")
	if err := os.MkdirAll(pluginsDir, 0o755); err != nil {
		t.Fatal(err)
	}
	registry := `{"version":2,"plugins":{"hey@37signals":[{"version":"1.1.0"}]}}`
	if err := os.WriteFile(filepath.Join(pluginsDir, "installed_plugins.json"), []byte(registry), 0o600); err != nil {
		t.Fatal(err)
	}
	binDir := t.TempDir()
	claudePath := filepath.Join(binDir, "claude")
	if err := os.WriteFile(claudePath, []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", binDir)
	var gotName string
	var gotArgs []string
	stubRunAgentCommand(t, func(_ context.Context, name string, args ...string) ([]byte, error) {
		gotName, gotArgs = name, args
		return nil, nil
	})

	server := httptest.NewServer(http.NotFoundHandler())
	defer server.Close()
	if _, _, err := runAuthCommand(t, home, server.URL, "", true, "setup", "agents", "--remove"); err != nil {
		t.Fatalf("setup agents --remove: %v", err)
	}
	if gotName != claudePath || strings.Join(gotArgs, " ") != "plugin uninstall hey@37signals" {
		t.Errorf("claude command = %q %v", gotName, gotArgs)
	}
}

func TestSetupAgentsRemoveReportsFailedPluginUninstall(t *testing.T) {
	isolateAgents(t)
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	pluginsDir := filepath.Join(home, ".claude", "plugins")
	if err := os.MkdirAll(pluginsDir, 0o755); err != nil {
		t.Fatal(err)
	}
	registry := `{"version":2,"plugins":{"hey@37signals":[{"version":"1.1.0"}]}}`
	if err := os.WriteFile(filepath.Join(pluginsDir, "installed_plugins.json"), []byte(registry), 0o600); err != nil {
		t.Fatal(err)
	}
	binDir := t.TempDir()
	if err := os.WriteFile(filepath.Join(binDir, "claude"), []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", binDir)
	stubRunAgentCommand(t, func(_ context.Context, _ string, _ ...string) ([]byte, error) {
		return []byte("network unreachable"), errors.New("exit status 1")
	})

	server := httptest.NewServer(http.NotFoundHandler())
	defer server.Close()
	_, _, err := runAuthCommand(t, home, server.URL, "", true, "setup", "agents", "--remove")
	var apiErr *apierr.Error
	if !errors.As(err, &apiErr) {
		t.Fatalf("error = %v, want *apierr.Error", err)
	}
	if apiErr.Code != "setup_remove_failed" {
		t.Errorf("code = %q, want setup_remove_failed", apiErr.Code)
	}
	if !strings.Contains(apiErr.Message, "Claude Code plugin: network unreachable") {
		t.Errorf("message = %q, want the failing component and its output named", apiErr.Message)
	}
}

func TestSetupAgentsRemoveReportsMissingClaudeBinary(t *testing.T) {
	isolateAgents(t)
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	pluginsDir := filepath.Join(home, ".claude", "plugins")
	if err := os.MkdirAll(pluginsDir, 0o755); err != nil {
		t.Fatal(err)
	}
	registry := `{"version":2,"plugins":{"hey@37signals":[{"version":"1.1.0"}]}}`
	if err := os.WriteFile(filepath.Join(pluginsDir, "installed_plugins.json"), []byte(registry), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", t.TempDir())
	stubRunAgentCommand(t, func(_ context.Context, _ string, _ ...string) ([]byte, error) {
		t.Error("no claude command should run when the binary is missing")
		return nil, nil
	})

	server := httptest.NewServer(http.NotFoundHandler())
	defer server.Close()
	_, _, err := runAuthCommand(t, home, server.URL, "", true, "setup", "agents", "--remove")
	var apiErr *apierr.Error
	if !errors.As(err, &apiErr) {
		t.Fatalf("error = %v, want *apierr.Error", err)
	}
	if apiErr.Code != "setup_remove_failed" {
		t.Errorf("code = %q, want setup_remove_failed", apiErr.Code)
	}
	if !strings.Contains(apiErr.Message, "Claude Code plugin: claude binary not found") {
		t.Errorf("message = %q, want the missing binary named", apiErr.Message)
	}
}

func TestRemoveOwnedSkillFilesDeclinesSymlinkedDirectory(t *testing.T) {
	target := t.TempDir()
	for _, name := range []string{skillFilename, installedVersionFile, ownershipMarkerFile} {
		if err := os.WriteFile(filepath.Join(target, name), []byte("managed"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	link := filepath.Join(t.TempDir(), "hey")
	if err := os.Symlink(target, link); err != nil {
		t.Fatal(err)
	}

	removed, err := removeOwnedSkillFiles(link)
	if err != nil || removed {
		t.Fatalf("removeOwnedSkillFiles = %v, %v; want false, nil", removed, err)
	}
	for _, name := range []string{skillFilename, installedVersionFile, ownershipMarkerFile} {
		if _, statErr := os.Stat(filepath.Join(target, name)); statErr != nil {
			t.Errorf("symlink target lost %s: %v", name, statErr)
		}
	}
	if _, statErr := os.Lstat(link); statErr != nil {
		t.Errorf("symlink itself removed: %v", statErr)
	}
}

// Legacy credentials belong to the server recorded beside them. With the
// effective base URL pointed elsewhere, migration must neither misfile them
// under the wrong store key nor scrub the only copy for their real origin.
func TestMigrationLeavesForeignOriginLegacyCredentialsAlone(t *testing.T) {
	isolateAgents(t)
	home := t.TempDir()
	configDir := filepath.Join(home, "hey-cli")
	if err := os.MkdirAll(configDir, 0o700); err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(http.NotFoundHandler())
	defer server.Close()

	// Store already holds credentials for the dev server this run targets.
	if _, _, err := runAuthCommand(t, home, server.URL, "", true, "auth", "login", "--token", "dev-token"); err != nil {
		t.Fatalf("auth login: %v", err)
	}
	// The legacy fields target production.
	raw, err := os.ReadFile(filepath.Join(configDir, "config.json"))
	if os.IsNotExist(err) {
		raw = []byte("{}")
	} else if err != nil {
		t.Fatal(err)
	}
	var cfgMap map[string]any
	if err := json.Unmarshal(raw, &cfgMap); err != nil {
		t.Fatal(err)
	}
	cfgMap["access_token"] = "production-token"
	seeded, _ := json.Marshal(cfgMap)
	if err := os.WriteFile(filepath.Join(configDir, "config.json"), seeded, 0o600); err != nil {
		t.Fatal(err)
	}

	if _, _, err := runAuthCommand(t, home, server.URL, "", true, "auth", "status"); err != nil {
		t.Fatalf("auth status: %v", err)
	}
	after, err := os.ReadFile(filepath.Join(configDir, "config.json"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(after), "production-token") {
		t.Fatalf("foreign-origin legacy credential scrubbed: %s", after)
	}
	creds, err := os.ReadFile(filepath.Join(configDir, "credentials.json"))
	if err != nil || strings.Contains(string(creds), "production-token") {
		t.Errorf("foreign-origin legacy credential misfiled into the store: %v", err)
	}
}
