package cmd

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

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

	_, response, err = runAuthCommand(t, home, server.URL, "", true, "setup", "claude")
	if err != nil {
		t.Fatalf("setup claude: %v", err)
	}
	data = response.Data.(map[string]any)
	if data["agent_detected"] != false || data["plugin_installed"] != false {
		t.Errorf("undetected claude data = %v", data)
	}
	if response.Summary != "Claude Code not detected" {
		t.Errorf("summary = %q", response.Summary)
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
