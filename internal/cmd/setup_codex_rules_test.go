package cmd

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"testing"

	"github.com/basecamp/hey-cli/internal/apierr"
	"github.com/basecamp/hey-cli/internal/output"
)

// codexRulesEnv isolates a home with Codex installed at its own CODEX_HOME and
// returns the home, the rules file Codex would load, and a server that must not
// be called.
func codexRulesEnv(t *testing.T) (home, rulesPath, serverURL string) {
	t.Helper()
	isolateAgents(t)
	home = t.TempDir()
	codexHome := t.TempDir()
	t.Setenv("CODEX_HOME", codexHome)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Errorf("unexpected HTTP request: %s %s", r.Method, r.URL.Path)
	}))
	t.Cleanup(server.Close)
	return home, filepath.Join(codexHome, "rules", "hey.rules"), server.URL
}

func setupCodex(t *testing.T, home, serverURL string) (output.Response, error) {
	t.Helper()
	_, response, err := runAuthCommand(t, home, serverURL, "", true, "setup", "codex")
	return response, err
}

func writeRulesFixture(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

var allowRuleLine = regexp.MustCompile(`^prefix_rule\(pattern = \[("[a-z-]+"(?:, "[a-z-]+")*)\], decision = "allow"\)$`)

// parseAllowRules reads the rules file the way this test needs Codex to: a
// managed-marker first line, comments, and one allow rule per line. Anything
// else fails, since a parse error in any rules file makes Codex drop them all.
func parseAllowRules(t *testing.T, content string) [][]string {
	t.Helper()
	lines := strings.Split(strings.TrimSuffix(content, "\n"), "\n")
	if !strings.HasPrefix(lines[0], "# Managed by hey-cli.") {
		t.Fatalf("first line is not the managed marker: %q", lines[0])
	}
	var rules [][]string
	for _, line := range lines {
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		match := allowRuleLine.FindStringSubmatch(line)
		if match == nil {
			t.Fatalf("unexpected line in hey.rules: %q", line)
		}
		var tokens []string
		for _, quoted := range strings.Split(match[1], ", ") {
			tokens = append(tokens, strings.Trim(quoted, `"`))
		}
		rules = append(rules, tokens)
	}
	if len(rules) == 0 {
		t.Fatal("hey.rules allows nothing")
	}
	return rules
}

// allowed applies the rules as Codex does: positionally, on argv tokens.
func rulesAllow(rules [][]string, argv ...string) bool {
	for _, rule := range rules {
		if len(argv) >= len(rule) && strings.Join(argv[:len(rule)], "\x00") == strings.Join(rule, "\x00") {
			return true
		}
	}
	return false
}

func installedRules(t *testing.T) [][]string {
	t.Helper()
	home, rulesPath, serverURL := codexRulesEnv(t)
	response, err := setupCodex(t, home, serverURL)
	if err != nil {
		t.Fatalf("setup codex: %v", err)
	}
	if response.Summary != "Codex connected" {
		t.Errorf("summary = %q", response.Summary)
	}
	info, err := os.Lstat(rulesPath)
	if err != nil {
		t.Fatalf("rules not installed: %v", err)
	}
	if !info.Mode().IsRegular() || info.Mode().Perm() != 0o644 {
		t.Errorf("rules mode = %v, want a regular 0644 file", info.Mode())
	}
	return parseAllowRules(t, string(readFile(t, rulesPath)))
}

func TestSetupCodexInstallsReadOnlyRules(t *testing.T) {
	rules := installedRules(t)

	for _, argv := range [][]string{
		{"hey", "search", "invoice", "--json"},
		{"hey", "box", "imbox"},
		{"hey", "thread", "read", "12345", "--json"},
		{"hey", "contact", "note", "show", "12345"},
		{"hey", "auth", "status", "--json"},
	} {
		if !rulesAllow(rules, argv...) {
			t.Errorf("read command not allowed: %v", argv)
		}
	}
	for _, argv := range [][]string{
		{"hey", "compose", "--to", "alice@example.com"},
		{"hey", "reply", "12345", "-m", "Thanks"},
		{"hey", "draft", "send", "12345"},
		{"hey", "attachment", "save", "67890:1"},
		{"hey", "contact", "show-again", "12345"},
		{"hey", "auth", "login"},
		{"hey", "timetrack", "export"},
		{"hey", "watch"},
		{"hey", "--json", "search", "invoice"},
	} {
		if rulesAllow(rules, argv...) {
			t.Errorf("rules allow a command that must ask: %v", argv)
		}
	}
}

// Every allowed prefix must name a runnable command, so a renamed or removed
// command fails here instead of silently losing (or misdirecting) its rule.
func TestCodexRulesNameRunnableCommands(t *testing.T) {
	rules := installedRules(t)
	root := newRootCmd()
	var covered []string
	for _, rule := range rules {
		if rule[0] != "hey" || len(rule) < 2 {
			t.Errorf("rule %v does not name a hey subcommand", rule)
			continue
		}
		found, rest, err := root.Find(rule[1:])
		path := strings.Join(rule, " ")
		if err != nil || len(rest) != 0 || found.CommandPath() != path {
			t.Errorf("rule %q resolves to %q (rest %v, err %v)", path, found.CommandPath(), rest, err)
			continue
		}
		if !found.Runnable() {
			t.Errorf("rule %q names a command group, not a command", path)
		}
		for _, sub := range found.Commands() {
			covered = append(covered, sub.CommandPath())
		}
	}
	// A rule is a prefix, so it also lets a subcommand of the command it names
	// leave the sandbox. These were reviewed as read-only; a new one fails here
	// until someone decides whether it may run without asking.
	reviewed := "hey box list|hey box view|hey bundle view|hey search filters"
	slices.Sort(covered)
	if got := strings.Join(covered, "|"); got != reviewed {
		t.Errorf("subcommands covered by a rule = %q, reviewed %q", got, reviewed)
	}
}

func TestSetupCodexReplacesManagedRules(t *testing.T) {
	home, rulesPath, serverURL := codexRulesEnv(t)
	stale := "# Managed by hey-cli. An older release.\nprefix_rule(pattern = [\"hey\"], decision = \"allow\")\n"
	writeRulesFixture(t, rulesPath, stale)

	if _, err := setupCodex(t, home, serverURL); err != nil {
		t.Fatalf("setup codex: %v", err)
	}
	rules := parseAllowRules(t, string(readFile(t, rulesPath)))
	if rulesAllow(rules, "hey", "compose") {
		t.Error("the stale blanket rule survived")
	}
	entries, err := os.ReadDir(filepath.Dir(rulesPath))
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 {
		t.Errorf("rules directory holds %d entries, want only hey.rules", len(entries))
	}
}

func TestSetupCodexRefusesUnmanagedRules(t *testing.T) {
	home, rulesPath, serverURL := codexRulesEnv(t)
	custom := "# My own rules for hey.\nprefix_rule(pattern = [\"hey\"], decision = \"allow\")\n"
	writeRulesFixture(t, rulesPath, custom)

	_, err := setupCodex(t, home, serverURL)
	var cliErr *apierr.Error
	if !errors.As(err, &cliErr) || cliErr.Code != "setup_incomplete" {
		t.Fatalf("error = %v, want setup_incomplete", err)
	}
	if errs, _ := cliErr.Meta["errors"].([]string); !strings.Contains(strings.Join(errs, "|"), rulesPath) {
		t.Errorf("error does not name %s: %+v", rulesPath, cliErr)
	}
	if got := string(readFile(t, rulesPath)); got != custom {
		t.Errorf("user rules changed: %q", got)
	}
}

func TestSetupCodexRefusesSymlinkedRules(t *testing.T) {
	home, rulesPath, serverURL := codexRulesEnv(t)
	target := filepath.Join(t.TempDir(), "shared.rules")
	managedLooking := "# Managed by hey-cli.\nprefix_rule(pattern = [\"hey\"], decision = \"allow\")\n"
	writeRulesFixture(t, target, managedLooking)
	if err := os.MkdirAll(filepath.Dir(rulesPath), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(target, rulesPath); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}

	_, err := setupCodex(t, home, serverURL)
	var cliErr *apierr.Error
	if !errors.As(err, &cliErr) || cliErr.Code != "setup_incomplete" {
		t.Fatalf("error = %v, want setup_incomplete", err)
	}
	if info, err := os.Lstat(rulesPath); err != nil || info.Mode()&os.ModeSymlink == 0 {
		t.Errorf("symlink replaced: %v", err)
	}
	if got := string(readFile(t, target)); got != managedLooking {
		t.Errorf("symlink target changed: %q", got)
	}
}

func TestSetupAgentsRemoveDeletesManagedCodexRules(t *testing.T) {
	home, rulesPath, serverURL := codexRulesEnv(t)
	if _, err := setupCodex(t, home, serverURL); err != nil {
		t.Fatalf("setup codex: %v", err)
	}
	if _, err := os.Stat(rulesPath); err != nil {
		t.Fatalf("rules not installed: %v", err)
	}

	_, response, err := runAuthCommand(t, home, serverURL, "", true, "setup", "agents", "--remove")
	if err != nil {
		t.Fatalf("setup agents --remove: %v", err)
	}
	removed := stringList(t, response.Data.(map[string]any)["removed"])
	if !strings.Contains(strings.Join(removed, "|"), "Codex rules") {
		t.Errorf("removed = %v", removed)
	}
	if _, err := os.Lstat(rulesPath); !os.IsNotExist(err) {
		t.Errorf("managed rules remain: %v", err)
	}
}

func TestSetupAgentsRemovePreservesUnmanagedCodexRules(t *testing.T) {
	home, rulesPath, serverURL := codexRulesEnv(t)
	custom := "prefix_rule(pattern = [\"hey\"], decision = \"allow\")\n"
	writeRulesFixture(t, rulesPath, custom)

	_, response, err := runAuthCommand(t, home, serverURL, "", true, "setup", "agents", "--remove")
	if err != nil {
		t.Fatalf("setup agents --remove: %v", err)
	}
	if removed := stringList(t, response.Data.(map[string]any)["removed"]); strings.Contains(strings.Join(removed, "|"), "Codex rules") {
		t.Errorf("removed = %v", removed)
	}
	if got := string(readFile(t, rulesPath)); got != custom {
		t.Errorf("user rules changed: %q", got)
	}
}

func codexRulesDoctorCheck(t *testing.T, home, serverURL string) map[string]any {
	t.Helper()
	_, response, err := runAuthCommand(t, home, serverURL, "", true, "doctor")
	if err != nil {
		t.Fatalf("doctor: %v", err)
	}
	for _, raw := range response.Data.([]any) {
		if check := raw.(map[string]any); check["name"] == "Codex Rules" {
			return check
		}
	}
	t.Fatalf("doctor has no Codex Rules check: %v", response.Data)
	return nil
}

func TestDoctorChecksCodexRules(t *testing.T) {
	home, rulesPath, serverURL := codexRulesEnv(t)

	if check := codexRulesDoctorCheck(t, home, serverURL); check["status"] != "error" || check["hint"] != "Run: hey setup codex" {
		t.Errorf("missing rules: %v", check)
	}

	if _, err := setupCodex(t, home, serverURL); err != nil {
		t.Fatalf("setup codex: %v", err)
	}
	if check := codexRulesDoctorCheck(t, home, serverURL); check["status"] != "ok" {
		t.Errorf("installed rules: %v", check)
	}

	writeRulesFixture(t, rulesPath, "# Managed by hey-cli. An older release.\nprefix_rule(pattern = [\"hey\", \"search\"], decision = \"allow\")\n")
	if check := codexRulesDoctorCheck(t, home, serverURL); check["status"] != "error" || check["hint"] != "Run: hey setup codex" {
		t.Errorf("stale rules: %v", check)
	}

	writeRulesFixture(t, rulesPath, "prefix_rule(pattern = [\"hey\"], decision = \"allow\")\n")
	if check := codexRulesDoctorCheck(t, home, serverURL); check["status"] != "error" || check["hint"] != "Move it aside, then run: hey setup codex" {
		t.Errorf("unmanaged rules: %v", check)
	}
}
